# LGTV OLED Guard - CLAUDE.md

## Build & Run

```bash
# Debug build (console visible)
go build -o gocv.exe .

# Release build (no console window, stripped)
go build -ldflags="-H windowsgui -s -w" -o gocv.exe .
```

Requires Go 1.22+, OpenCV 4.x with GoCV bindings, MinGW on Windows.
Runtime requires `face_detection_yunet_2023mar.onnx` next to the exe.

## Release Workflow

After a feature or fix, commit → push → package → create GitHub release.

```bash
# 1. Build
go build -ldflags="-H windowsgui -s -w" -o gocv.exe .

# 2. Package all required resources
# Include: gocv.exe, config.json, face_detection_yunet_2023mar.onnx
zip package.zip gocv.exe config.json face_detection_yunet_2023mar.onnx

# 3. Commit and push
git add <files>
git commit -m "feat: <description>"
git push origin main

# 4. Create release with package (version auto-increments: v12, v13, ...)
gh release create v<N> package.zip --title "LGTV OLED Guard v<N>" --notes "<bilingual notes>"
```

**Release notes must be bilingual (Chinese + English).** Example format:
```
## / 

- 
- 

## What's New

- Feature description
- ...
```

Tags are not always pushed separately; `gh release create` handles tag creation.
Commit messages use English with conventional prefixes: `feat:`, `fix:`, `docs:`.

**After releasing**, check if README.md / README.zh.md need updating (also bilingual).

## Architecture

Flat Go package (`package main`) — no sub-packages. Entry: `gocv_main.go`.

| File | Purpose |
|------|---------|
| `gocv_main.go` | Entry point, state machine (active/passive/screen_off), input hooks |
| `gocv_config.go` | Config struct, defaults, JSON load, atomic.Value storage |
| `gocv_server.go` | HTTP server (127.0.0.1:19999), REST API, MJPEG debug stream |
| `gocv_systray.go` | Win32 system tray, tray icon, popup menu, global hotkey |
| `gocv_detector.go` | YuNet face/eye detection, Haar cascade fallback |
| `gocv_autostart.go` | Windows Registry Run key registration |
| `ui/index.html` | Embedded web control panel (vanilla HTML/CSS/JS, no framework) |

State machine: active (camera off, 1s sleep) → passive (camera on, detection every N seconds) → screen_off (500ms sleep, wait for wake).

## Key Patterns

- **Config**: `config.json` next to exe, atomic save (write to `.tmp` then rename). In-memory via `atomic.Value`.
- **Win32**: All Win32 calls use `syscall.NewLazyDLL` + `NewProc`. Window message pump on OS-locked thread (`runtime.LockOSThread()`).
- **Hotkey**: `RegisterHotKey` with `MOD_NOREPEAT`, re-registration via `PostMessageW` to tray window (must stay on same thread).
- **Screen control**: Shells out to `LGTVcli.exe -screenon/-screenoff` (configurable via `lg_tv_cmd`).
- **2s wake guard**: `screenOffTime` atomic prevents keyboard/mouse from waking screen within 2 seconds of turning off.
- **Embedded UI**: `//go:embed ui/index.html` compiles HTML into binary. No external web assets.

## Gotchas

- Tray icon is embedded via `rsrc.syso` (ID #1). Fallback generates a 16x16 teal-green circle programmatically.
- `face_detection_yunet_2026may.onnx` crashes with OpenCV 4.10 — use `*_2023mar.onnx`.
- `Config.System.AutoStart` is preserved across config saves (not overwritten by UI POST).
- HTTP server has auto-restart loop with 1s delay on crash.
- Debug MJPEG stream is single-client; returns 409 if already active.
