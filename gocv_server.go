package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"gocv.io/x/gocv"
)

var (
	debugStreamActive bool
	debugStreamMu     sync.Mutex

	// Shared status for the web UI
	status struct {
		mu            sync.RWMutex
		faceFound     bool
		eyesFound     bool
		state         string
		noFaceCount   int
		noEyesCount   int
	}
)

const uiHTML = ` + "`" + $html + "`" + `

func startHTTPServer(cfg Config) *http.Server {
	mux := http.NewServeMux()

	// UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(uiHTML))
	})

	// Debug MJPEG stream
	mux.HandleFunc("/debug/stream", handleDebugStream)

	// API: get config
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
			c.System.AutoStart = old.System.AutoStart // preserve auto-start (set via dedicated API)
			if err := updateConfig(c); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
	})

	// API: screen off
	mux.HandleFunc("/api/screen/off", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		requestScreenOff()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// API: auto-start toggle
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

	// API: status
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

func updateStatus(faceFound, eyesFound bool, state string, noFaceCount, noEyesCount int) {
	status.mu.Lock()
	defer status.mu.Unlock()
	status.faceFound = faceFound
	status.eyesFound = eyesFound
	status.state = state
	status.noFaceCount = noFaceCount
	status.noEyesCount = noEyesCount
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
		if !debugStreamActive {
			return
		}
		if ok := cam.Read(&frame); !ok || frame.Empty() {
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// Re-read config every frame for real-time parameter tuning
		cfg = getConfig()

		faceFound, eyesFound, faces := detectPresenceDebug(cam, cfg)
		drawDebugOverlay(&frame, faces, faceFound, eyesFound)
		faces.Close()

		// Also draw timestamp
		gocv.PutText(&frame, time.Now().Format("15:04:05"), image.Pt(10, int(frame.Rows())-10),
			gocv.FontHersheySimplex, 0.5, color.RGBA{255, 255, 255, 255}, 1)

		buf, err := gocv.IMEncode(".jpg", frame)
		if err != nil {
			continue
		}

		w.Write([]byte("--frame\r\nContent-Type: image/jpeg\r\n\r\n"))
		w.Write(buf)
		w.Write([]byte("\r\n"))
	}
}

// Signal sender for screen-off from web UI
var screenOffRequest = make(chan struct{}, 1)

func requestScreenOff() {
	select {
	case screenOffRequest <- struct{}{}:
	default:
	}
}
