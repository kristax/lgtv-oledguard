package main

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const autoStartKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const autoStartValue = "PresenceDetector"

func SetAutoStart(enabled bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, autoStartKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if enabled {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		return key.SetStringValue(autoStartValue, exePath)
	}
	return key.DeleteValue(autoStartValue)
}

func IsAutoStartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, autoStartKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	_, _, err = key.GetStringValue(autoStartValue)
	return err == nil
}
