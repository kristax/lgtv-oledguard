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

const (
	captureWidth              = 1280
	captureHeight             = 720
	eyeFallbackWidth          = 640
	eyeFallbackHeight         = 480
	screenOnStateDetectDuration = 60
	requiredDetections        = 2
	requiredLossCount         = 10
	cameraWarmupFrames        = 5
)

var (
	ynFaceDetector       gocv.FaceDetectorYN
	eyeGlassesClassifier gocv.CascadeClassifier // haarcascade_eye_tree_eyeglasses
	eyeBasicClassifier   gocv.CascadeClassifier // haarcascade_eye
)

func main() {
	infoLogger := log.New(os.Stdout, "INFO: ", log.LstdFlags)
	errLogger := log.New(os.Stderr, "ERROR: ", log.LstdFlags)
	deviceID := 0

	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)
	modelPath := filepath.Join(execDir, "face_detection_yunet_2023mar.onnx")
	infoLogger.Printf("Model path: %s", modelPath)

	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		errLogger.Fatalf("YuNet model not found: %s", modelPath)
	}

	// YuNet DNN face detector
	ynFaceDetector = gocv.NewFaceDetectorYN(modelPath, "", image.Pt(640, 640))
	ynFaceDetector.SetInputSize(image.Pt(captureWidth, captureHeight))
	ynFaceDetector.SetScoreThreshold(0.5)
	ynFaceDetector.SetNMSThreshold(0.3)
	ynFaceDetector.SetTopK(5000)
	infoLogger.Println("YuNet DNN face detector loaded (2023mar, model=640x640, input=1280x720)")

	// Load BOTH eye cascades
	base := "C:\\opencv\\build\\install\\etc\\haarcascades"

	eyeGlassesClassifier = gocv.NewCascadeClassifier()
	defer eyeGlassesClassifier.Close()
	if !eyeGlassesClassifier.Load(filepath.Join(base, "haarcascade_eye_tree_eyeglasses.xml")) {
		errLogger.Fatalf("Error reading eye_glasses cascade")
	}

	eyeBasicClassifier = gocv.NewCascadeClassifier()
	defer eyeBasicClassifier.Close()
	if !eyeBasicClassifier.Load(filepath.Join(base, "haarcascade_eye.xml")) {
		errLogger.Fatalf("Error reading eye cascade")
	}

	infoLogger.Println("Eye cascades loaded: glasses + basic")

	var (
		lossEyes    atomic.Int32
		detectEyes  atomic.Int32
		screenState atomic.Bool
		webcamState atomic.Bool
		webcam      *gocv.VideoCapture
	)

	screenState.Store(true)

	screenOn := func() {
		exec.Command("LGTVcli.exe", "-screenon").Start()
		screenState.Store(true)
	}
	screenOff := func() {
		exec.Command("LGTVcli.exe", "-screenoff").Start()
		screenState.Store(false)
	}

	camOn := func() {
		var err error
		webcam, err = gocv.OpenVideoCapture(deviceID)
		if err != nil {
			errLogger.Fatalf("Error opening video capture %v, cam id: %d", err, deviceID)
		}
		webcam.Set(gocv.VideoCaptureFrameWidth, float64(captureWidth))
		webcam.Set(gocv.VideoCaptureFrameHeight, float64(captureHeight))
		actualW := webcam.Get(gocv.VideoCaptureFrameWidth)
		actualH := webcam.Get(gocv.VideoCaptureFrameHeight)
		infoLogger.Printf("camera opened at %.0fx%.0f (requested %dx%d)",
			actualW, actualH, captureWidth, captureHeight)
		webcamState.Store(true)
	}

	camOff := func() {
		if webcam != nil {
			for i := 0; i < 3; i++ {
				dummy := gocv.NewMat()
				webcam.Read(&dummy)
				dummy.Close()
			}
			webcam.Close()
			webcam = nil
		}
		webcamState.Store(false)
	}

	defer func() {
		if webcamState.Load() && webcam != nil {
			webcam.Close()
		}
	}()

	go func() {
		inputhook.HookMouse(func(x int64, y int64, event int, data uint64) {
			if !screenState.Load() {
				screenOn()
			}
			lossEyes.Store(0)
		})
	}()
	go func() {
		inputhook.HookKeyboard(func(keyEvent int, keyCode int) {
			if !screenState.Load() {
				screenOn()
			}
			lossEyes.Store(0)
		})
	}()

	go func() {
		warmup := 0
		for {
			if !screenState.Load() {
				time.Sleep(10 * time.Second)
				continue
			}
			if !webcamState.Load() {
				camOn()
				warmup = cameraWarmupFrames
				time.Sleep(500 * time.Millisecond)
				continue
			}
			if warmup > 0 {
				dummy := gocv.NewMat()
				webcam.Read(&dummy)
				dummy.Close()
				warmup--
				if warmup == 0 {
					infoLogger.Println("camera warmed up, starting detection")
				}
				time.Sleep(100 * time.Millisecond)
				continue
			}

			if detectedEyes(webcam) {
				detectEyes.Add(1)
				infoLogger.Printf("[DNN] detect eyes counter: %d", detectEyes.Load())
			} else {
				lossEyes.Add(1)
				errLogger.Printf("[DNN] lost eyes counter: %d", lossEyes.Load())
			}

			if detectEyes.Load() >= requiredDetections {
				lossEyes.Store(0)
				detectEyes.Store(0)
				infoLogger.Println("detect eyes - keeping screen on")
				camOff()
				time.Sleep(screenOnStateDetectDuration * time.Second)
				continue
			}
			if lossEyes.Load() >= requiredLossCount {
				lossEyes.Store(0)
				detectEyes.Store(0)
				infoLogger.Println("loss eyes - turning screen off")
				screenOff()
				camOff()
			}
			time.Sleep(700 * time.Millisecond)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	infoLogger.Println("running - press Ctrl+C to exit")
	<-sigChan
	infoLogger.Println("shutting down...")
}

// ============================================================
// Three-path detection:
// A) YuNet landmarks ? tight eye ROIs ? both cascades (primary, most accurate)
// B) YuNet face bbox ? full face ROI ? both cascades (broader search)
// C) Direct eye detection at 640x480 (fallback if no face found)
// ============================================================

func detectedEyes(webcam *gocv.VideoCapture) bool {
	frame := gocv.NewMat()
	defer frame.Close()
	if ok := webcam.Read(&frame); !ok || frame.Empty() {
		return false
	}

	faces := gocv.NewMat()
	ynFaceDetector.Detect(frame, &faces)

	numFaces := faces.Rows()
	if numFaces == 0 {
		faces.Close()
		// Path C: fallback direct eye detection
		med := gocv.NewMat()
		defer med.Close()
		gocv.Resize(frame, &med, image.Pt(eyeFallbackWidth, eyeFallbackHeight), 0, 0, gocv.InterpolationLinear)
		r := eyeGlassesClassifier.DetectMultiScaleWithParams(med, 1.1, 3.0, 0.0, image.Pt(15, 15), image.Pt(0, 0))
		if len(r) > 0 {
			return true
		}
		r2 := eyeBasicClassifier.DetectMultiScaleWithParams(med, 1.1, 3.0, 0.0, image.Pt(15, 15), image.Pt(0, 0))
		return len(r2) > 0
	}

	fbounds := image.Rect(0, 0, frame.Cols(), frame.Rows())

	for r := 0; r < numFaces; r++ {
		fx := int(faces.GetFloatAt(r, 0))
		fy := int(faces.GetFloatAt(r, 1))
		fw := int(faces.GetFloatAt(r, 2))
		fh := int(faces.GetFloatAt(r, 3))
		if fw < 40 || fh < 40 {
			continue
		}

		// ====== Path A: Landmark-based tight eye ROIs ======
		// YuNet output [14 cols]: x,y,w,h, re_x,re_y, le_x,le_y, nose_x,nose_y, rm_x,rm_y, lm_x,lm_y
		landmarkCols := faces.Cols()
		if landmarkCols >= 8 {
			reX := int(faces.GetFloatAt(r, 4))
			reY := int(faces.GetFloatAt(r, 5))
			leX := int(faces.GetFloatAt(r, 6))
			leY := int(faces.GetFloatAt(r, 7))

			// Check each eye with a tight 40x40 window centered on landmark
			eyes := [][2]int{{reX, reY}, {leX, leY}}
			for _, eyePt := range eyes {
				eyeRect := image.Rect(eyePt[0]-20, eyePt[1]-20, eyePt[0]+20, eyePt[1]+20).Intersect(fbounds)
				if eyeRect.Empty() {
					continue
				}
				eyeROI := frame.Region(eyeRect)
				if detectEyeInROI(eyeROI) {
					eyeROI.Close()
					faces.Close()
					return true
				}
				eyeROI.Close()
			}
		}

		// ====== Path B: Full face ROI (broader search, catches what landmarks miss) ======
		faceRect := image.Rect(fx, fy, fx+fw, fy+fh).Intersect(fbounds)
		if !faceRect.Empty() {
			faceROI := frame.Region(faceRect)
			if detectEyeInROI(faceROI) {
				faceROI.Close()
				faces.Close()
				return true
			}
			faceROI.Close()
		}
	}

	faces.Close()
	return false
}

// detectEyeInROI runs both eye cascades on a Mat region.
func detectEyeInROI(roi gocv.Mat) bool {
	// Glasses cascade: more sensitive (trained for glasses)
	r1 := eyeGlassesClassifier.DetectMultiScaleWithParams(roi, 1.05, 2.0, 0.0, image.Pt(8, 8), image.Pt(0, 0))
	if len(r1) > 0 {
		return true
	}
	// Basic cascade: catches eyes when glasses reflections confuse the glasses cascade
	r2 := eyeBasicClassifier.DetectMultiScaleWithParams(roi, 1.08, 2.0, 0.0, image.Pt(10, 10), image.Pt(0, 0))
	return len(r2) > 0
}
