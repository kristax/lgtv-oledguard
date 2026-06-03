package main

import (
	"image"
	"log"
	"math"
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

const (
	captureWidth        = 1280
	captureHeight       = 720
	activeTimeout       = 2 * time.Minute
	passiveCheckInterval = 2 * time.Second
	noFaceCycles        = 5
	noEyesCycles        = 15
	cameraWarmupFrames  = 5
	minEyeDistance      = 15.0
)

var (
	ynFaceDetector     gocv.FaceDetectorYN
	eyeGlassesCascade  gocv.CascadeClassifier
	eyeBasicCascade    gocv.CascadeClassifier
	lastActivity       atomic.Int64
	screenOn           atomic.Bool
)

func main() {
	infoLogger := log.New(os.Stdout, "INFO: ", log.LstdFlags)
	errLogger := log.New(os.Stderr, "ERROR: ", log.LstdFlags)

	// Find model: try exe dir first, then cwd
	execPath, _ := os.Executable()
	modelPath := filepath.Join(filepath.Dir(execPath), "face_detection_yunet_2023mar.onnx")
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		modelPath = "face_detection_yunet_2023mar.onnx" // fallback: relative to CWD
	}
	infoLogger.Printf("Model path: %s", modelPath)
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		errLogger.Fatalf("YuNet model not found at %s", modelPath)
	}

	ynFaceDetector = gocv.NewFaceDetectorYN(modelPath, "", image.Pt(640, 640))
	ynFaceDetector.SetInputSize(image.Pt(captureWidth, captureHeight))
	ynFaceDetector.SetScoreThreshold(0.5)
	ynFaceDetector.SetNMSThreshold(0.3)
	ynFaceDetector.SetTopK(5000)
	infoLogger.Println("YuNet: 640x640 model, 1280x720 input")

	base := "C:\\opencv\\build\\install\\etc\\haarcascades"
	eyeGlassesCascade = gocv.NewCascadeClassifier()
	defer eyeGlassesCascade.Close()
	eyeGlassesCascade.Load(filepath.Join(base, "haarcascade_eye_tree_eyeglasses.xml"))
	eyeBasicCascade = gocv.NewCascadeClassifier()
	defer eyeBasicCascade.Close()
	eyeBasicCascade.Load(filepath.Join(base, "haarcascade_eye.xml"))
	infoLogger.Println("Fallback cascades loaded")

	screenOn.Store(true)
	lastActivity.Store(time.Now().Unix())

	go func() {
		inputhook.HookMouse(func(x int64, y int64, event int, data uint64) {
			lastActivity.Store(time.Now().Unix())
			if !screenOn.Load() {
				infoLogger.Println("mouse wake")
				exec.Command("LGTVcli.exe", "-screenon").Start()
				screenOn.Store(true)
			}
		})
	}()
	go func() {
		inputhook.HookKeyboard(func(keyEvent int, keyCode int) {
			lastActivity.Store(time.Now().Unix())
			if !screenOn.Load() {
				infoLogger.Println("key wake")
				exec.Command("LGTVcli.exe", "-screenon").Start()
				screenOn.Store(true)
			}
		})
	}()

	go func() {
		var (
			webcam   *gocv.VideoCapture
			noFaceC  int
			noEyesC  int
			passive  bool
			warmup   int
		)
		for {
			now := time.Now().Unix()
			idle := now - lastActivity.Load()

			if screenOn.Load() && !passive && idle >= int64(activeTimeout.Seconds()) {
				infoLogger.Println("idle 2min ? camera on")
				var err error
				webcam, err = gocv.OpenVideoCapture(0)
				if err == nil {
					webcam.Set(gocv.VideoCaptureFrameWidth, float64(captureWidth))
					webcam.Set(gocv.VideoCaptureFrameHeight, float64(captureHeight))
					warmup = cameraWarmupFrames
					passive = true
					noFaceC = 0
					noEyesC = 0
				} else {
					errLogger.Printf("camera: %v", err)
				}
			}

			if !screenOn.Load() {
				if passive && webcam != nil {
					webcam.Close()
					webcam = nil
					passive = false
					infoLogger.Println("screen off ? camera off")
				}
				time.Sleep(500 * time.Millisecond)
				continue
			}

			if passive && webcam != nil {
				if warmup > 0 {
					dummy := gocv.NewMat()
					webcam.Read(&dummy)
					dummy.Close()
					warmup--
					if warmup == 0 {
						infoLogger.Println("camera ready")
					}
					time.Sleep(200 * time.Millisecond)
					continue
				}

				face, eyes := detectPresence(webcam)
				if face && eyes {
					noFaceC = 0
					noEyesC = 0
				} else if face && !eyes {
					noFaceC = 0
					noEyesC++
					if noEyesC >= noEyesCycles {
						infoLogger.Println("no eyes 30s ? off")
						exec.Command("LGTVcli.exe", "-screenoff").Start()
						screenOn.Store(false)
						noEyesC = 0
					}
				} else {
					noEyesC = 0
					noFaceC++
					if noFaceC >= noFaceCycles {
						infoLogger.Println("no face 10s ? off")
						exec.Command("LGTVcli.exe", "-screenoff").Start()
						screenOn.Store(false)
						noFaceC = 0
					}
				}
				time.Sleep(passiveCheckInterval)
				continue
			}

			time.Sleep(1 * time.Second)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	infoLogger.Println("ready ? Ctrl+C to exit")
	<-sigChan
	infoLogger.Println("done")
}

func detectPresence(webcam *gocv.VideoCapture) (faceFound, eyesFound bool) {
	frame := gocv.NewMat()
	defer frame.Close()
	if ok := webcam.Read(&frame); !ok || frame.Empty() {
		return
	}

	faces := gocv.NewMat()
	ynFaceDetector.Detect(frame, &faces)
	n := faces.Rows()
	if n == 0 {
		faces.Close()
		return
	}

	landmarkCols := faces.Cols()
	for r := 0; r < n; r++ {
		fw := int(faces.GetFloatAt(r, 2))
		fh := int(faces.GetFloatAt(r, 3))
		if fw < 30 || fh < 30 {
			continue
		}
		faceFound = true

		if landmarkCols >= 8 {
			reX := faces.GetFloatAt(r, 4)
			reY := faces.GetFloatAt(r, 5)
			leX := faces.GetFloatAt(r, 6)
			leY := faces.GetFloatAt(r, 7)
			dx := leX - reX
			dy := leY - reY
			if math.Sqrt(float64(dx*dx+dy*dy)) > minEyeDistance && reX > 0 && leX > 0 {
				eyesFound = true
				faces.Close()
				return
			}
		}
	}

	// Haar fallback if landmarks unavailable
	if faceFound && !eyesFound && landmarkCols < 8 {
		for r := 0; r < n; r++ {
			fx := int(faces.GetFloatAt(r, 0))
			fy := int(faces.GetFloatAt(r, 1))
			fw := int(faces.GetFloatAt(r, 2))
			fh := int(faces.GetFloatAt(r, 3))
			if fw < 30 || fh < 30 {
				continue
			}
			rect := image.Rect(fx, fy, fx+fw, fy+fh).Intersect(image.Rect(0, 0, frame.Cols(), frame.Rows()))
			if !rect.Empty() {
				roi := frame.Region(rect)
				r1 := eyeGlassesCascade.DetectMultiScaleWithParams(roi, 1.1, 3.0, 0.0, image.Pt(12, 12), image.Pt(0, 0))
				r2 := eyeBasicCascade.DetectMultiScaleWithParams(roi, 1.1, 3.0, 0.0, image.Pt(12, 12), image.Pt(0, 0))
				roi.Close()
				if len(r1) > 0 || len(r2) > 0 {
					eyesFound = true
					faces.Close()
					return
				}
			}
		}
	}

	faces.Close()
	return
}
