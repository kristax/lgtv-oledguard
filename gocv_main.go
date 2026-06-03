package main

import (
	"image"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vence722/inputhook"
	"gocv.io/x/gocv"
)

var (
	ynFaceDetector    gocv.FaceDetectorYN
	eyeGlassesCascade gocv.CascadeClassifier
	eyeBasicCascade   gocv.CascadeClassifier
	lastActivity      atomic.Int64
	screenOn          atomic.Bool
)

func main() {
	infoLogger := log.New(os.Stdout, "INFO: ", log.LstdFlags)
	errLogger := log.New(os.Stderr, "ERROR: ", log.LstdFlags)

	// Load or create config
	execPath, _ := os.Executable()
	configDir := filepath.Dir(execPath)
	if configDir == "." {
		configDir, _ = os.Getwd()
	}
	configPath := filepath.Join(configDir, "config.json")
	cfg, err := LoadConfigFile(configPath)
	if err != nil {
		errLogger.Printf("Config error: %v ? using defaults", err)
		cfg = DefaultConfig()
	}
	appConfig.Store(cfg)
	infoLogger.Printf("Config loaded from %s", configPath)

	// Sync auto-start with config
	if cfg.System.AutoStart {
		SetAutoStart(true)
	}

	// Find YuNet model
	modelPath := filepath.Join(configDir, "face_detection_yunet_2023mar.onnx")
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		modelPath = "face_detection_yunet_2023mar.onnx"
	}
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		errLogger.Fatalf("YuNet model not found: %s", modelPath)
	}
	infoLogger.Printf("Model: %s", modelPath)

	// Init YuNet
	ynFaceDetector = gocv.NewFaceDetectorYN(modelPath, "", image.Pt(cfg.Detection.ModelSize, cfg.Detection.ModelSize))
	ynFaceDetector.SetInputSize(image.Pt(cfg.Camera.Width, cfg.Camera.Height))
	ynFaceDetector.SetScoreThreshold(cfg.Detection.ScoreThreshold)
	ynFaceDetector.SetNMSThreshold(cfg.Detection.NMSThreshold)
	ynFaceDetector.SetTopK(5000)

	// Init fallback cascades
	base := "C:\\opencv\\build\\install\\etc\\haarcascades"
	eyeGlassesCascade = gocv.NewCascadeClassifier()
	eyeGlassesCascade.Load(filepath.Join(base, "haarcascade_eye_tree_eyeglasses.xml"))
	eyeBasicCascade = gocv.NewCascadeClassifier()
	eyeBasicCascade.Load(filepath.Join(base, "haarcascade_eye.xml"))

	// Start HTTP server
	srv := startHTTPServer(cfg)
	defer srv.Close()

	// Init state
	screenOn.Store(true)
	lastActivity.Store(time.Now().Unix())
	updateStatus(false, false, "active", 0, 0)

	// Mouse/Keyboard hooks
	go func() {
		inputhook.HookMouse(func(x int64, y int64, event int, data uint64) {
			lastActivity.Store(time.Now().Unix())
			if !screenOn.Load() {
				infoLogger.Println("mouse wake")
				exec.Command(cfg.System.LGTVCmd, "-screenon").Start()
				screenOn.Store(true)
			}
		})
	}()
	go func() {
		inputhook.HookKeyboard(func(keyEvent int, keyCode int) {
			lastActivity.Store(time.Now().Unix())
			if !screenOn.Load() {
				infoLogger.Println("key wake")
				exec.Command(cfg.System.LGTVCmd, "-screenon").Start()
				screenOn.Store(true)
			}
		})
	}()

	// Main state-machine loop
	go func() {
		var (
			webcam  *gocv.VideoCapture
			noFaceC int
			noEyesC int
			passive bool
			warmup  int
		)
		for {
			// Handle screen-off requests from web UI
			select {
			case <-screenOffRequest:
				if screenOn.Load() {
					infoLogger.Println("manual screen off")
					if passive && webcam != nil {
						webcam.Close()
						webcam = nil
						passive = false
					}
					exec.Command(cfg.System.LGTVCmd, "-screenoff").Start()
					screenOn.Store(false)
					noFaceC = 0
					noEyesC = 0
				}
			default:
			}

			cfg := getConfig()
			now := time.Now().Unix()
			idle := now - lastActivity.Load()

			// Active ? Passive transition
			if screenOn.Load() && !passive && idle >= int64(cfg.Timing.ActiveTimeoutSec) {
				infoLogger.Println("idle ? camera on")
				var err error
				webcam, err = gocv.OpenVideoCapture(cfg.Camera.DeviceID)
				if err == nil {
					webcam.Set(gocv.VideoCaptureFrameWidth, float64(cfg.Camera.Width))
					webcam.Set(gocv.VideoCaptureFrameHeight, float64(cfg.Camera.Height))
					warmup = cfg.Timing.CameraWarmupFrames
					passive = true
					noFaceC = 0
					noEyesC = 0
				}
			}

			// Screen OFF
			if !screenOn.Load() {
				if passive && webcam != nil {
					webcam.Close()
					webcam = nil
					passive = false
				}
				updateStatus(false, false, "screen_off", 0, 0)
				time.Sleep(500 * time.Millisecond)
				continue
			}

			// Passive mode detection
			if passive && webcam != nil {
				if warmup > 0 {
					dummy := gocv.NewMat()
					webcam.Read(&dummy)
					dummy.Close()
					warmup--
					time.Sleep(200 * time.Millisecond)
					continue
				}

				face, eyes := detectPresence(webcam, cfg)

				if face && eyes {
					noFaceC = 0
					noEyesC = 0
				} else if face && !eyes {
					noFaceC = 0
					noEyesC++
					if noEyesC >= cfg.Timing.NoEyesCycles {
						infoLogger.Println("no eyes ? screen off")
						exec.Command(cfg.System.LGTVCmd, "-screenoff").Start()
						screenOn.Store(false)
						noEyesC = 0
					}
				} else {
					noEyesC = 0
					noFaceC++
					if noFaceC >= cfg.Timing.NoFaceCycles {
						infoLogger.Println("no face ? screen off")
						exec.Command(cfg.System.LGTVCmd, "-screenoff").Start()
						screenOn.Store(false)
						noFaceC = 0
					}
				}

				stateStr := "passive"
				if !passive {
					stateStr = "active"
				}
				updateStatus(face, eyes, stateStr, noFaceC, noEyesC)

				time.Sleep(time.Duration(cfg.Timing.PassiveIntervalSec) * time.Second)
				continue
			}

			// Active mode: just update status
			updateStatus(false, false, "active", 0, 0)
			time.Sleep(1 * time.Second)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	infoLogger.Printf("ready ? http://127.0.0.1:%d", cfg.System.ServerPort)
	<-sigChan
	infoLogger.Println("shutting down")
}
