package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileConfig is loaded from config.properties next to the sessionsvr binary.
// Only PASSWORD is required today; unknown keys are ignored so the file can be
// extended without breaking older builds.
type FileConfig struct {
	Password string
}

func defaultConfigPath() string {
	exe, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exe), "config.properties")
	}
	return "config.properties"
}

func LoadConfig(path string) (*FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := &FileConfig{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(key)) {
		case "PASSWORD":
			cfg.Password = strings.TrimSpace(value)
		}
	}
	return cfg, nil
}
