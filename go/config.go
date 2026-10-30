package main

import (
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type config struct {
	Mode           string `yaml:"mode"`
	MaxConcurrency int    `yaml:"max_concurrency"`
	AppVersion     string `yaml:"app_version"`
	TeamLanguage   string `yaml:"team_language"`
	PublicBaseURL  string `yaml:"public_base_url"`
}

var configMu sync.RWMutex
var currentConfig = defaults()

func defaults() config {
	return config{Mode: "own", MaxConcurrency: 3, AppVersion: "3.16.3", TeamLanguage: "en"}
}
func getConfig() config { configMu.RLock(); defer configMu.RUnlock(); return currentConfig }
func configure(raw []byte) error {
	c := defaults()
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("invalid plugin configuration")
		}
	}
	if (c.Mode != "own" && c.Mode != "team") || (c.TeamLanguage != "zh" && c.TeamLanguage != "en") || c.MaxConcurrency < 1 || c.MaxConcurrency > 100 || strings.TrimSpace(c.AppVersion) == "" {
		return fmt.Errorf("invalid zcode plugin configuration")
	}
	c.PublicBaseURL = strings.TrimSpace(c.PublicBaseURL)
	if c.PublicBaseURL != "" {
		if _, ok := httpBaseURL(c.PublicBaseURL); !ok {
			return fmt.Errorf("invalid zcode plugin configuration: public_base_url must be an absolute HTTP(S) URL")
		}
	}
	configMu.Lock()
	currentConfig = c
	configMu.Unlock()
	return nil
}
