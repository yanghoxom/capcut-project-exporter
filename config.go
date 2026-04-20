package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	ProjectDir string `json:"project_dir"`
	OutputDir  string `json:"output_dir"`
}

func configDir() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, ".config")
	}
	return filepath.Join(appData, "capcut-export")
}

func configPath() string {
	return filepath.Join(configDir(), "config.json")
}

func LoadConfig() Config {
	var cfg Config
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	return cfg
}

func SaveConfig(cfg Config) error {
	_ = os.MkdirAll(configDir(), 0755)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0644)
}

func DefaultCapcutDir() string {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return ""
	}
	return filepath.Join(localAppData, "CapCut", "User Data", "Projects", "com.lveditor.draft")
}
