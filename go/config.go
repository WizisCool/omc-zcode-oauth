package main

import (
	"fmt"
	"net/url"
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
	Site           string `yaml:"site"`
}

var configMu sync.RWMutex
var currentConfig = defaults()

func defaults() config {
	return config{Mode: "own", MaxConcurrency: 3, AppVersion: "3.16.3", TeamLanguage: "en", Site: "auto"}
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
	c.Site = strings.TrimSpace(strings.ToLower(c.Site))
	if c.Site == "" {
		c.Site = "auto"
	}
	if c.Site != "auto" && c.Site != "zai" && c.Site != "bigmodel" {
		return fmt.Errorf("invalid zcode plugin configuration: site must be auto, zai or bigmodel")
	}
	configMu.Lock()
	currentConfig = c
	configMu.Unlock()
	return nil
}

// httpBaseURL validates the legacy public_base_url setting. It is not used to
// construct OAuth links or expose any management action as a public resource.
func httpBaseURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, false
	}
	u.User = nil
	u.Path, u.RawPath = strings.TrimSuffix(u.Path, "/"), ""
	u.RawQuery, u.ForceQuery, u.Fragment = "", false, ""
	return u, true
}
