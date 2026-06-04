package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	WM_TRAYICON = 0x0400 + 1
	WM_COMMAND  = 0x0111
	WM_DESTROY  = 0x0002

	NIM_ADD    = 0
	NIM_DELETE = 2

	NIF_MESSAGE = 1
	NIF_ICON    = 2
	NIF_TIP     = 4

	IDM_OPEN   = 1001
	IDM_SCROFF = 1002
	IDM_EXIT   = 1003

	CW_USEDEFAULT = 0x80000000
	SW_HIDE       = 0
)

// Classic NOTIFYICONDATA (no GUID, compatible with all Windows versions)
type NOTIFYICONDATA struct {
	CbSize           uint32
	HWnd             syscall.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            syscall.Handle
	SzTip            [128]uint16
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	nid    NOTIFYICONDATA
	hwnd   syscall.Handle
	quitCh = make(chan struct{})
)

func init() {
	runtime.LockOSThread()
}

func runTray() {
	hideConsole()

	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	className, _ := syscall.UTF16PtrFromString("PresenceDetectorTray3")

	type WNDCLASSEX struct {
		CbSize        uint32
		Style         uint32
		LpfnWndProc   uintptr
		CbClsExtra    int32
		CbWndExtra    int32
		HInstance     syscall.Handle
		HIcon         syscall.Handle
		HCursor       syscall.Handle
		HbrBackground syscall.Handle
		LpszMenuName  *uint16
		LpszClassName *uint16
		HIconSm       syscall.Handle
	}

	// Load icon resource embedded in exe (ID 1 from rsrc.syso)
	icon, _, _ := user32.NewProc("LoadIconW").Call(instance, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("#1"))))
	if icon == 0 {
		// Fallback: try programmatic icon
		icon2 := createTrayIcon()
		icon = uintptr(icon2)
	}

	wc := WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		LpfnWndProc:   syscall.NewCallback(windowProc),
		HInstance:     syscall.Handle(instance),
		HIcon:         syscall.Handle(icon),
		HCursor:       syscall.Handle(icon),
		LpszClassName: className,
		HIconSm:       syscall.Handle(icon),
	}

	r, _, _ := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc)))
	if r == 0 {
		errLogger.Println("RegisterClassExW failed")
		return
	}

	h, _, _ := user32.NewProc("CreateWindowExW").Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("PD"))),
		0,
		uintptr(CW_USEDEFAULT), uintptr(CW_USEDEFAULT),
		uintptr(CW_USEDEFAULT), uintptr(CW_USEDEFAULT),
		0, 0, instance, 0,
	)
	if h == 0 {
		errLogger.Println("CreateWindowExW failed")
		return
	}
	hwnd = syscall.Handle(h)
	user32.NewProc("ShowWindow").Call(h, SW_HIDE)

	// Add tray icon
	tip := syscall.StringToUTF16("Presence Detector")
	nid = NOTIFYICONDATA{
		CbSize:           uint32(unsafe.Sizeof(NOTIFYICONDATA{})),
		HWnd:             hwnd,
		UID:              1,
		UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		UCallbackMessage: WM_TRAYICON,
		HIcon:            syscall.Handle(icon),
	}
	copy(nid.SzTip[:], tip)

	rr, _, _ := shell32.NewProc("Shell_NotifyIconW").Call(NIM_ADD, uintptr(unsafe.Pointer(&nid)))
	if rr == 0 {
		errLogger.Println("Shell_NotifyIcon NIM_ADD failed")
		return
	}
	defer shell32.NewProc("Shell_NotifyIconW").Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
	// icon is from exe resource or CreateIcon, destroyed on unload

	infoLogger.Println("tray icon created")

	var msg struct {
		HWnd    syscall.Handle
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      struct{ X, Y int32 }
	}

	for {
		ret, _, _ := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) {
			break
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}

	close(quitCh)
}

func showPopupMenu(h syscall.Handle) {
	menu, _, _ := user32.NewProc("CreatePopupMenu").Call()

	s1 := syscall.StringToUTF16Ptr("Open Panel")
	user32.NewProc("AppendMenuW").Call(menu, 0, IDM_OPEN, uintptr(unsafe.Pointer(s1)))
	s2 := syscall.StringToUTF16Ptr("Turn Screen Off")
	user32.NewProc("AppendMenuW").Call(menu, 0, IDM_SCROFF, uintptr(unsafe.Pointer(s2)))
	user32.NewProc("AppendMenuW").Call(menu, 0x800, 0, 0)
	s3 := syscall.StringToUTF16Ptr("Exit")
	user32.NewProc("AppendMenuW").Call(menu, 0, IDM_EXIT, uintptr(unsafe.Pointer(s3)))

	var pt struct{ X, Y int32 }
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&pt)))
	user32.NewProc("SetForegroundWindow").Call(uintptr(h))
	user32.NewProc("TrackPopupMenu").Call(menu, 0x2|0x1, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(h), 0)
	user32.NewProc("DestroyMenu").Call(menu)
}

func openBrowser() {
	cfg := getConfig()
	url := fmt.Sprintf("http://127.0.0.1:%d", cfg.System.ServerPort)
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func hideConsole() {
	cw, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if cw != 0 {
		user32.NewProc("ShowWindow").Call(cw, 0)
	}
}

func windowProc(h syscall.Handle, msg uint32, wp uintptr, lp uintptr) uintptr {
	switch msg {
	case WM_TRAYICON:
		switch uint32(lp & 0xFFFF) {
		case 0x0205: // WM_RBUTTONUP
			showPopupMenu(h)
		case 0x0201: // WM_LBUTTONUP
			openBrowser()
		}
		return 0

	case WM_COMMAND:
		switch uint32(wp & 0xFFFF) {
		case IDM_OPEN:
			openBrowser()
		case IDM_SCROFF:
			requestScreenOff()
		case IDM_EXIT:
			user32.NewProc("PostQuitMessage").Call(0)
		}
		return 0

	case WM_DESTROY:
		user32.NewProc("PostQuitMessage").Call(0)
		return 0
	}

	r, _, _ := user32.NewProc("DefWindowProcW").Call(uintptr(h), uintptr(msg), wp, lp)
	return r
}

// createTrayIcon generates a 16x16 icon using CreateDIBSection (true alpha support for Win11 tray)
func createTrayIcon() syscall.Handle {
	w, h := 16, 16

	// Build 32bpp BGRA pixel data with alpha channel
	pixels := make([]byte, w*h*4)
	cx, cy, r2 := 7.5, 7.5, 49.0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			idx := ((h-1-y)*w + x) * 4 // BMP rows are bottom-up
			if dx*dx+dy*dy <= r2 {
				// Inside circle: teal-green #4ecca3, fully opaque
				pixels[idx]   = 0xA3 // B
				pixels[idx+1] = 0xCC // G
				pixels[idx+2] = 0x4E // R
				pixels[idx+3] = 0xFF // A
			}
			// Outside: already zero (transparent)
		}
	}

	// BITMAPINFO for CreateDIBSection
	type BITMAPINFOHEADER struct {
		BiSize          uint32
		BiWidth         int32
		BiHeight        int32
		BiPlanes        uint16
		BiBitCount      uint16
		BiCompression   uint32
		BiSizeImage     uint32
		BiXPelsPerMeter int32
		BiYPelsPerMeter int32
		BiClrUsed       uint32
		BiClrImportant  uint32
	}
	bih := BITMAPINFOHEADER{
		BiSize:    40,
		BiWidth:   int32(w),
		BiHeight:  int32(h),
		BiPlanes:  1,
		BiBitCount: 32,
	}

	var bits unsafe.Pointer
	hBmp, _, _ := syscall.NewLazyDLL("gdi32.dll").NewProc("CreateDIBSection").Call(
		0,
		uintptr(unsafe.Pointer(&bih)),
		0, // DIB_RGB_COLORS
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if hBmp == 0 {
		// Fallback: try CreateIcon
		return createTrayIconFallback()
	}

	// Copy pixels into DIB section
	dst := unsafe.Slice((*byte)(bits), len(pixels))
	copy(dst, pixels)

	// Create 1bpp mask bitmap (all 1s = fully opaque where color pixels exist)
	maskBytes := make([]byte, ((w+15)/16*2)*h) // row-aligned 16-bit
	for i := range maskBytes {
		maskBytes[i] = 0xFF
	}
	hMask, _, _ := syscall.NewLazyDLL("gdi32.dll").NewProc("CreateBitmap").Call(
		uintptr(w), uintptr(h), 1, 1,
		uintptr(unsafe.Pointer(&maskBytes[0])),
	)

	// Create icon from color + mask
	type ICONINFO struct {
		FIcon    uint32
		XHotspot uint32
		YHotspot uint32
		HbmMask  syscall.Handle
		HbmColor syscall.Handle
	}
	ii := ICONINFO{
		FIcon:    1,
		HbmMask:  syscall.Handle(hMask),
		HbmColor: syscall.Handle(hBmp),
	}

	icon, _, _ := user32.NewProc("CreateIconIndirect").Call(uintptr(unsafe.Pointer(&ii)))

	// Clean up the bitmaps (icon owns copies)
	syscall.NewLazyDLL("gdi32.dll").NewProc("DeleteObject").Call(hBmp)
	syscall.NewLazyDLL("gdi32.dll").NewProc("DeleteObject").Call(hMask)

	return syscall.Handle(icon)
}

// createTrayIconFallback uses CreateIcon (XOR+AND mask, no alpha) as a last resort
func createTrayIconFallback() syscall.Handle {
	w, h := 16, 16
	andPlane := make([]byte, w*h/8)
	for i := range andPlane {
		andPlane[i] = 0xFF
	}
	xorPlane := make([]byte, w*h*4)
	cx, cy, r2 := 7.5, 7.5, 49.0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			idx := (y*w + x) * 4
			if dx*dx+dy*dy <= r2 {
				xorPlane[idx]   = 0xA3
				xorPlane[idx+1] = 0xCC
				xorPlane[idx+2] = 0x4E
			}
		}
	}
	icon, _, _ := user32.NewProc("CreateIcon").Call(0,
		uintptr(w), uintptr(h), 1, 32,
		uintptr(unsafe.Pointer(&andPlane[0])),
		uintptr(unsafe.Pointer(&xorPlane[0])),
	)
	return syscall.Handle(icon)
}