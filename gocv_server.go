package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"log"
	"net/http"
	"sync"
	"time"

	"gocv.io/x/gocv"
)

//go:embed ui/index.html
var uiHTML string

var (
	debugStreamActive bool
	debugStopCh       = make(chan struct{}, 1)
	debugStreamMu     sync.Mutex

	status struct {
		mu          sync.RWMutex
		faceFound   bool
		eyesFound   bool
		state       string
		noFaceCount int
		noEyesCount int
		idleSec     int64
	}
)

func startHTTPServer(cfg Config) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(uiHTML))
	})

	mux.HandleFunc("/debug/stream", handleDebugStream)
	mux.HandleFunc("/debug/stop", handleDebugStop)
	mux.HandleFunc("/debug/status", handleDebugStatus)

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(getConfig())
			return
		}
		if r.Method == "POST" {
			var c Config
			if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if c.System.ServerPort == 0 {
				c.System.ServerPort = 19999
			}
			old := getConfig()
			c.System.AutoStart = old.System.AutoStart
			if err := updateConfig(c); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
	})

	mux.HandleFunc("/api/screen/off", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		requestScreenOff()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/autostart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		old := getConfig()
		old.System.AutoStart = !old.System.AutoStart
		SetAutoStart(old.System.AutoStart)
		updateConfig(old)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"enabled": old.System.AutoStart})
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		status.mu.RLock()
		defer status.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"face":          fmt.Sprintf("%v", status.faceFound),
			"eyes":          fmt.Sprintf("%v", status.eyesFound),
			"state":         status.state,
			"no_face_count": status.noFaceCount,
			"no_eyes_count": status.noEyesCount,
			"idle_sec":      status.idleSec,
		})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.System.ServerPort)
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		log.Printf("HTTP server: http://%s", addr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()
	return srv
}

func updateStatus(faceFound, eyesFound bool, state string, noFaceCount, noEyesCount int, idleSec int64) {
	status.mu.Lock()
	defer status.mu.Unlock()
	status.faceFound = faceFound
	status.eyesFound = eyesFound
	status.state = state
	status.noFaceCount = noFaceCount
	status.noEyesCount = noEyesCount
	status.idleSec = idleSec
}

func handleDebugStop(w http.ResponseWriter, r *http.Request) {
	select {
	case debugStopCh <- struct{}{}:
	default:
	}
	debugStreamMu.Lock()
	debugStreamActive = false
	debugStreamMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func handleDebugStatus(w http.ResponseWriter, r *http.Request) {
	debugStreamMu.Lock()
	active := debugStreamActive
	debugStreamMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"active": active})
}

func handleDebugStream(w http.ResponseWriter, r *http.Request) {
	debugStreamMu.Lock()
	if debugStreamActive {
		debugStreamMu.Unlock()
		http.Error(w, "debug stream already active", 409)
		return
	}
	debugStreamActive = true
	debugStreamMu.Unlock()

	select {
	case <-debugStopCh:
	default:
	}

	defer func() {
		debugStreamMu.Lock()
		debugStreamActive = false
		debugStreamMu.Unlock()
	}()

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	cfg := getConfig()

	cam, err := gocv.OpenVideoCapture(cfg.Camera.DeviceID)
	if err != nil {
		http.Error(w, "camera error: "+err.Error(), 500)
		return
	}
	defer cam.Close()
	cam.Set(gocv.VideoCaptureFrameWidth, float64(cfg.Camera.Width))
	cam.Set(gocv.VideoCaptureFrameHeight, float64(cfg.Camera.Height))

	frame := gocv.NewMat()
	defer frame.Close()

	for {
		select {
		case <-debugStopCh:
			return
		default:
		}

		if ok := cam.Read(&frame); !ok || frame.Empty() {
			time.Sleep(50 * time.Millisecond)
			continue
		}

		cfg = getConfig()

		faceFound, eyesFound, faces := detectPresenceDebug(cam, cfg)
		drawDebugOverlay(&frame, faces, faceFound, eyesFound)
		faces.Close()

		gocv.PutText(&frame, time.Now().Format("15:04:05"),
			image.Pt(10, int(frame.Rows())-10),
			gocv.FontHersheySimplex, 0.5, color.RGBA{255, 255, 255, 255}, 1)

		nbuf, err := gocv.IMEncode(gocv.JPEGFileExt, frame)
		if err != nil {
			continue
		}

		_, writeErr := w.Write([]byte("--frame\r\nContent-Type: image/jpeg\r\n\r\n"))
		if writeErr != nil {
			return
		}
		_, writeErr = w.Write(nbuf.GetBytes())
		if writeErr != nil {
			return
		}
		_, writeErr = w.Write([]byte("\r\n"))
		if writeErr != nil {
			return
		}
	}
}

var screenOffRequest = make(chan struct{}, 1)

func requestScreenOff() {
	select {
	case screenOffRequest <- struct{}{}:
	default:
	}
}

