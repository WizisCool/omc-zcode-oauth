package main

import (
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type config struct {
	Site           string `yaml:"site"`
	Mode           string `yaml:"mode"`
	PreferGift     bool   `yaml:"prefer_gift"`
	MaxConcurrency int    `yaml:"max_concurrency"`
	AppVersion     string `yaml:"app_version"`
	TeamLanguage   string `yaml:"team_language"`
}

var configMu sync.RWMutex
var currentConfig = defaults()

func defaults() config {
	return config{Site: "zai", Mode: "own", MaxConcurrency: 3, AppVersion: "3.16.3", TeamLanguage: "en"}
}
func getConfig() config { configMu.RLock(); defer configMu.RUnlock(); return currentConfig }
func configure(raw []byte) error {
	c := defaults()
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("invalid plugin configuration")
		}
	}
	if (c.Site != "zai" && c.Site != "bigmodel") || (c.Mode != "own" && c.Mode != "team") || (c.TeamLanguage != "zh" && c.TeamLanguage != "en") || c.MaxConcurrency < 1 || c.MaxConcurrency > 100 || strings.TrimSpace(c.AppVersion) == "" {
		return fmt.Errorf("invalid zcode plugin configuration")
	}
	configMu.Lock()
	currentConfig = c
	configMu.Unlock()
	return nil
}
