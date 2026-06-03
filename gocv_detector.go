package main

import (
	"image"
	"image/color"
	"math"

	"gocv.io/x/gocv"
)

func detectPresence(webcam *gocv.VideoCapture, cfg Config) (faceFound, eyesFound bool) {
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
		if fw < cfg.Detection.MinFaceSize || fh < cfg.Detection.MinFaceSize {
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
			if math.Sqrt(float64(dx*dx+dy*dy)) > cfg.Detection.MinEyeDist && reX > 0 && leX > 0 {
				eyesFound = true
				faces.Close()
				return
			}
		}
	}

	if faceFound && !eyesFound && landmarkCols < 8 {
		for r := 0; r < n; r++ {
			fx := int(faces.GetFloatAt(r, 0))
			fy := int(faces.GetFloatAt(r, 1))
			fw := int(faces.GetFloatAt(r, 2))
			fh := int(faces.GetFloatAt(r, 3))
			if fw < cfg.Detection.MinFaceSize || fh < cfg.Detection.MinFaceSize {
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

// Debug detection: returns faces Mat for drawing overlays
func detectPresenceDebug(webcam *gocv.VideoCapture, cfg Config) (faceFound, eyesFound bool, faces gocv.Mat) {
	frame := gocv.NewMat()
	defer frame.Close()
	if ok := webcam.Read(&frame); !ok || frame.Empty() {
		return false, false, gocv.NewMat()
	}

	facesMat := gocv.NewMat()
	ynFaceDetector.Detect(frame, &facesMat)
	n := facesMat.Rows()

	if n == 0 {
		return false, false, facesMat
	}

	landmarkCols := facesMat.Cols()
	for r := 0; r < n; r++ {
		fw := int(facesMat.GetFloatAt(r, 2))
		fh := int(facesMat.GetFloatAt(r, 3))
		if fw < cfg.Detection.MinFaceSize || fh < cfg.Detection.MinFaceSize {
			continue
		}
		faceFound = true

		if landmarkCols >= 8 {
			reX := facesMat.GetFloatAt(r, 4)
			reY := facesMat.GetFloatAt(r, 5)
			leX := facesMat.GetFloatAt(r, 6)
			leY := facesMat.GetFloatAt(r, 7)
			dx := leX - reX
			dy := leY - reY
			if math.Sqrt(float64(dx*dx+dy*dy)) > cfg.Detection.MinEyeDist && reX > 0 && leX > 0 {
				eyesFound = true
				return faceFound, eyesFound, facesMat
			}
		}
	}

	return faceFound, eyesFound, facesMat
}

// Draw detection overlays on frame (for debug stream)
func drawDebugOverlay(frame *gocv.Mat, faces gocv.Mat, faceFound, eyesFound bool) {
	green := color.RGBA{0, 255, 0, 255}
	red := color.RGBA{255, 0, 0, 255}
	yellow := color.RGBA{255, 255, 0, 255}
	white := color.RGBA{255, 255, 255, 255}

	n := faces.Rows()
	landmarkCols := faces.Cols()

	for r := 0; r < n; r++ {
		fx := int(faces.GetFloatAt(r, 0))
		fy := int(faces.GetFloatAt(r, 1))
		fw := int(faces.GetFloatAt(r, 2))
		fh := int(faces.GetFloatAt(r, 3))

		boxColor := red
		label := "face"
		if faceFound && eyesFound {
			boxColor = green
			label = "face+eyes"
		} else if faceFound {
			boxColor = yellow
			label = "face only"
		}

		gocv.Rectangle(frame, image.Rect(fx, fy, fx+fw, fy+fh), boxColor, 2)
		gocv.PutText(frame, label, image.Pt(fx, fy-5), gocv.FontHersheySimplex, 0.5, boxColor, 1)

		// Draw landmarks if available
		if landmarkCols >= 8 {
			points := []struct{ x, y int }{
				{int(faces.GetFloatAt(r, 4)), int(faces.GetFloatAt(r, 5))},  // right eye
				{int(faces.GetFloatAt(r, 6)), int(faces.GetFloatAt(r, 7))},  // left eye
			}
			for _, p := range points {
				if p.x > 0 && p.y > 0 {
					gocv.Circle(frame, image.Pt(p.x, p.y), 3, green, -1)
				}
			}
		}
	}

	// Status overlay at top-left
	status := "no face"
	if eyesFound {
		status = "FACE+EYES PRESENT"
	} else if faceFound {
		status = "FACE ONLY - countdown"
	}
	gocv.PutText(frame, status, image.Pt(10, 30), gocv.FontHersheySimplex, 0.7, white, 2)
}
