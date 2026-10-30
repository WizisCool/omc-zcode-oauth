package main

import (
	"encoding/json"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type catalog struct {
	Config struct {
		ProviderConfigRules struct {
			ProviderRules []struct {
				ProviderID      string   `json:"providerId"`
				BuiltinModelIDs []string `json:"builtinModelIds"`
			} `json:"providerRules"`
		} `json:"providerConfigRules"`
		ModelConfigRules struct {
			BuiltinProviderModelRules []struct {
				ProviderID string `json:"providerId"`
				ModelID    string `json:"modelId"`
				Config     struct {
					Enabled *bool `json:"enabled"`
				} `json:"config"`
			} `json:"builtinProviderModelRules"`
			ModelRules []struct {
				Match  string `json:"modelMatch"`
				Config struct {
					Properties struct {
						Context     int64 `json:"contextWindow"`
						InputFormat struct {
							SupportsImage bool `json:"supportsImage"`
						} `json:"inputFormat"`
					} `json:"properties"`
					OptionSpecs struct {
						MaxOutputTokens struct {
							Max int64 `json:"max"`
						} `json:"maxOutputTokens"`
						ReasoningLevel struct {
							Values []string `json:"values"`
						} `json:"reasoningLevel"`
					} `json:"optionSpecs"`
				} `json:"config"`
			} `json:"modelRules"`
		} `json:"modelConfigRules"`
	} `json:"config"`
}

var catalogCache struct {
	sync.Mutex
	at         time.Time
	appVersion string
	value      *catalog
}

func zcodePlatform() string {
	osname := runtime.GOOS
	if osname == "windows" {
		osname = "win32"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return osname + "-" + arch
}
func fetchCatalog(sender httpSender) *catalog {
	cfg := getConfig()
	catalogCache.Lock()
	defer catalogCache.Unlock()
	if catalogCache.appVersion == cfg.AppVersion && time.Since(catalogCache.at) < 10*time.Minute {
		return catalogCache.value
	}
	catalogCache.at = time.Now()
	catalogCache.appVersion = cfg.AppVersion
	catalogCache.value = nil
	target := zcodeRoot + "/api/v1/client/configs?" + url.Values{"app_version": {cfg.AppVersion}, "platform": {zcodePlatform()}}.Encode()
	device, err := randomUUID()
	if err != nil {
		return nil
	}
	raw, err := apiCall(sender, "GET", target, "", device, nil, nil)
	if err != nil {
		return nil
	}
	var metadata struct {
		Configs struct {
			Builtin string `json:"builtin_provider_config_json"`
		} `json:"configs"`
	}
	if json.Unmarshal(raw, &metadata) != nil {
		return nil
	}
	parsed, err := url.Parse(metadata.Configs.Builtin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil
	}
	resp, err := sender.Do(pluginapi.HTTPRequest{Method: "GET", URL: parsed.String(), Headers: map[string][]string{"User-Agent": {"ZCode/" + cfg.AppVersion}}})
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var doc catalog
	if json.Unmarshal(resp.Body, &doc) != nil {
		return nil
	}
	catalogCache.value = &doc
	return &doc
}
func fallbackModels() []pluginapi.ModelInfo {
	return []pluginapi.ModelInfo{
		modelInfo("GLM-5.3", 1000000, 128000, []string{"low", "high", "max"}),
		modelInfo("GLM-5.3-Flash", 1000000, 128000, []string{"low", "high", "max"}),
		modelInfo("GLM-5.2", 1000000, 128000, []string{"none", "high", "max"}),
		modelInfo("GLM-5-Turbo", 200000, 64000, []string{"none", "high"}),
	}
}
func modelInfo(id string, context, output int64, levels []string) pluginapi.ModelInfo {
	id = strings.TrimSpace(id)
	// The routing model ID is lowercase: ZCode echoes lowercase model names and
	// CPA only resolves an uppercase request against a lowercase registration,
	// never the other way round. The vendor spelling stays in the display fields.
	return pluginapi.ModelInfo{ID: strings.ToLower(id), Name: id, DisplayName: id, Object: "model", OwnedBy: "zcode", Type: "zcode", ContextLength: context, InputTokenLimit: context, MaxCompletionTokens: output, OutputTokenLimit: output, Thinking: &pluginapi.ThinkingSupport{Levels: levels}}
}
func catalogModels(doc *catalog, c credential) []pluginapi.ModelInfo {
	if doc == nil {
		return fallbackModels()
	}
	plan := "account:zai-individual-coding-plan"
	if c.Site == "bigmodel" {
		plan = "account:bigmodel-individual-coding-plan"
	}
	if c.Key == "" {
		plan = "account:zai-start-plan"
	}
	ids := []string{}
	seen := map[string]bool{}
	disabled := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		k := strings.ToLower(id)
		if id != "" && !seen[k] {
			seen[k] = true
			ids = append(ids, id)
		}
	}
	for _, r := range doc.Config.ProviderConfigRules.ProviderRules {
		if r.ProviderID == plan {
			for _, id := range r.BuiltinModelIDs {
				add(id)
			}
		}
	}
	for _, r := range doc.Config.ModelConfigRules.BuiltinProviderModelRules {
		if r.ProviderID != plan {
			continue
		}
		if r.Config.Enabled != nil && !*r.Config.Enabled {
			disabled[strings.ToLower(r.ModelID)] = true
		} else {
			add(r.ModelID)
		}
	}
	var out []pluginapi.ModelInfo
	fallbacks := fallbackModels()
	for _, id := range ids {
		if disabled[strings.ToLower(id)] {
			continue
		}
		m := modelInfo(id, 0, 0, nil)
		for _, f := range fallbacks {
			if strings.EqualFold(f.ID, id) {
				m = f
				m.Name, m.DisplayName = id, id
				break
			}
		}
		for _, rule := range doc.Config.ModelConfigRules.ModelRules {
			re, e := regexp.Compile("(?i)^(?:" + rule.Match + ")$")
			if e != nil || !re.MatchString(id) {
				continue
			}
			c := rule.Config
			if c.Properties.Context > 0 {
				m.ContextLength = c.Properties.Context
				m.InputTokenLimit = c.Properties.Context
			}
			if c.OptionSpecs.MaxOutputTokens.Max > 0 {
				m.MaxCompletionTokens = c.OptionSpecs.MaxOutputTokens.Max
				m.OutputTokenLimit = c.OptionSpecs.MaxOutputTokens.Max
			}
			if len(c.OptionSpecs.ReasoningLevel.Values) > 0 {
				var levels []string
				for _, v := range c.OptionSpecs.ReasoningLevel.Values {
					switch v {
					case "disabled":
						v = "none"
					case "enabled":
						v = "high"
					}
					found := false
					for _, x := range levels {
						if x == v {
							found = true
						}
					}
					if !found {
						levels = append(levels, v)
					}
				}
				m.Thinking = &pluginapi.ThinkingSupport{Levels: levels}
			}
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return fallbacks
	}
	return out
}
func modelsForAuth(req pluginapi.AuthModelRequest, sender httpSender) (pluginapi.ModelResponse, error) {
	c, err := readCredential(req.StorageJSON)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	models := catalogModels(fetchCatalog(sender), c)
	if c.Key != "" {
		return pluginapi.ModelResponse{Provider: "zcode", Models: models}, nil
	}
	balance, err := fetchBalance(sender, c)
	if err != nil {
		return pluginapi.ModelResponse{Provider: "zcode", Models: models}, nil
	}
	// A gift-only credential reaches exactly the models with an active gift
	// bucket, so that bucket set is the truthful catalogue; the vendor catalogue
	// only supplies metadata for names it also knows. An account with no usable
	// bucket keeps the catalogue so a flaky balance read cannot hide everything.
	known := make(map[string]pluginapi.ModelInfo, len(models))
	for _, m := range models {
		known[strings.ToLower(m.ID)] = m
	}
	now := balance.now()
	gift := make([]pluginapi.ModelInfo, 0, len(models))
	seen := map[string]bool{}
	for _, b := range balance.Balances {
		if !bucketUsable(b, now) || !bucketPlanActive(balance, b, now) {
			continue
		}
		for _, name := range bucketModels(b) {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			m, ok := known[key]
			if !ok {
				m = modelInfo(name, 0, 0, nil)
			}
			gift = append(gift, m)
		}
	}
	if len(gift) == 0 {
		return pluginapi.ModelResponse{Provider: "zcode", Models: models}, nil
	}
	return pluginapi.ModelResponse{Provider: "zcode", Models: gift}, nil
}
