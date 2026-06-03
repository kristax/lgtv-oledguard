package main

import (
	"fmt"
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

// Draw enhanced debug overlay with face sizes, filter status, and detection data panel
func drawDebugOverlay(frame *gocv.Mat, faces gocv.Mat, faceFound, eyesFound bool, cfg Config, idleSec int64, state string) {
	green := color.RGBA{0, 255, 0, 255}
	red := color.RGBA{255, 60, 60, 255}
	_ = color.RGBA{255, 200, 0, 255}
	cyan := color.RGBA{0, 220, 220, 255}
	white := color.RGBA{255, 255, 255, 255}
	darkBG := color.RGBA{0, 0, 0, 180}

	n := faces.Rows()
	landmarkCols := faces.Cols()
	minSize := cfg.Detection.MinFaceSize

	var totalFaces, passFaces int
	maxW, maxH := 0, 0

	for r := 0; r < n; r++ {
		fx := int(faces.GetFloatAt(r, 0))
		fy := int(faces.GetFloatAt(r, 1))
		fw := int(faces.GetFloatAt(r, 2))
		fh := int(faces.GetFloatAt(r, 3))

		totalFaces++
		passesFilter := fw >= minSize && fh >= minSize
		if passesFilter {
			passFaces++
		}
		if fw > maxW {
			maxW = fw
		}
		if fh > maxH {
			maxH = fh
		}

		// Color-code: green = passes, yellow = borderline, red = filtered out
		boxColor := red
		label := fmt.Sprintf("%dx%d", fw, fh)
		if passesFilter {
			boxColor = green
			if eyesFound {
				label = fmt.Sprintf("%dx%d EYES", fw, fh)
			} else {
				label = fmt.Sprintf("%dx%d face", fw, fh)
			}
		} else {
			label = fmt.Sprintf("%dx%d <MIN", fw, fh)
		}

		gocv.Rectangle(frame, image.Rect(fx, fy, fx+fw, fy+fh), boxColor, 2)
		gocv.PutText(frame, label, image.Pt(fx, fy-5), gocv.FontHersheySimplex, 0.4, boxColor, 1)

		// Draw eye landmarks if available
		if landmarkCols >= 8 && passesFilter {
			for _, idx := range []int{4, 6} {
				px := int(faces.GetFloatAt(r, idx))
				py := int(faces.GetFloatAt(r, idx+1))
				if px > 0 && py > 0 {
					gocv.Circle(frame, image.Pt(px, py), 3, cyan, -1)
				}
			}
		}
	}

	// ---- Info panel (top-right, semi-transparent) ----
	panelX := frame.Cols() - 280
	panelY := 10
	lineH := 22

	lines := []string{
		fmt.Sprintf("State: %s", state),
		fmt.Sprintf("Idle: %ds", idleSec),
		fmt.Sprintf("MinFaceSize: %dpx", minSize),
		fmt.Sprintf("Faces raw: %d  pass: %d", totalFaces, passFaces),
	}
	if maxW > 0 {
		lines = append(lines, fmt.Sprintf("Face range: %d-%d x %d-%d px",
			minSize, maxW, minSize, maxH))
	}
	if eyesFound {
		lines = append(lines, "Eyes: DETECTED")
	} else if faceFound {
		lines = append(lines, "Eyes: not found")
	} else {
		lines = append(lines, "Eyes: --")
	}

	panelH := lineH*len(lines) + 16
	gocv.Rectangle(frame, image.Rect(panelX-8, panelY-4, panelX+272, panelY+panelH), darkBG, -1)

	for i, line := range lines {
		y := panelY + 16 + i*lineH
		textColor := white
		if i == len(lines)-1 && eyesFound {
			textColor = green
		} else if i == len(lines)-1 && !faceFound {
			textColor = red
		}
		gocv.PutText(frame, line, image.Pt(panelX, y), gocv.FontHersheySimplex, 0.45, textColor, 1)
	}

	// Bottom-left timestamp
	gocv.PutText(frame, fmt.Sprintf("%s", ""),
		image.Pt(10, frame.Rows()-20),
		gocv.FontHersheySimplex, 0.4, color.RGBA{150, 150, 150, 255}, 1)
}

