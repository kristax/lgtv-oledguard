package main

import (
	"encoding/json"
	"os"
	// "path/filepath" unused - SaveConfigFile moved to server.go
	"sync/atomic"
)

type CameraConfig struct {
	DeviceID int `json:"device_id"`
	Width    int `json:"width"`
	Height   int `json:"height"`
}

type DetectionConfig struct {
	ModelSize      int     `json:"model_size"`
	ScoreThreshold float32 `json:"score_threshold"`
	NMSThreshold   float32 `json:"nms_threshold"`
	MinEyeDist     float64 `json:"min_eye_dist"`
	MinFaceSize    int     `json:"min_face_size"`
}

type TimingConfig struct {
	ActiveTimeoutSec   int `json:"active_timeout_sec"`
	PassiveIntervalSec int `json:"passive_interval_sec"`
	NoFaceCycles       int `json:"no_face_cycles"`
	NoEyesCycles       int `json:"no_eyes_cycles"`
	CameraWarmupFrames int `json:"camera_warmup_frames"`
}

type SystemConfig struct {
	AutoStart  bool   `json:"auto_start"`
	ServerPort int    `json:"server_port"`
	LGTVCmd    string `json:"lg_tv_cmd"`
}

type Config struct {
	Camera    CameraConfig    `json:"camera"`
	Detection DetectionConfig `json:"detection"`
	Timing    TimingConfig    `json:"timing"`
	System    SystemConfig    `json:"system"`
}

func DefaultConfig() Config {
	return Config{
		Camera: CameraConfig{
			DeviceID: 0,
			Width:    1280,
			Height:   720,
		},
		Detection: DetectionConfig{
			ModelSize:      640,
			ScoreThreshold: 0.5,
			NMSThreshold:   0.3,
			MinEyeDist:     15.0,
			MinFaceSize:    30,
		},
		Timing: TimingConfig{
			ActiveTimeoutSec:   120,
			PassiveIntervalSec: 2,
			NoFaceCycles:       5,
			NoEyesCycles:       15,
			CameraWarmupFrames: 5,
		},
		System: SystemConfig{
			AutoStart:  false,
			ServerPort: 19999,
			LGTVCmd:    "LGTVcli.exe",
		},
	}
}

var appConfig atomic.Value

func getConfig() Config {
	v := appConfig.Load()
	if v == nil {
		return DefaultConfig()
	}
	return v.(Config)
}

func updateConfig(c Config) error {
	appConfig.Store(c)
	return SaveConfigFile(c)
}

func LoadConfigFile(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, SaveConfigFile(cfg)
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), err
	}
	return cfg, nil
}

// SaveConfigFile is now in server.go (atomic write)


