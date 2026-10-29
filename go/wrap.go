package main

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"
)

//go:embed assets/prompt.json
var promptJSON []byte
var prompt struct {
	CliPrefix         string   `json:"cliPrefix"`
	StableSections    []string `json:"stableSections"`
	BeforeEnvironment string   `json:"beforeEnvironment"`
	AfterEnvironment  string   `json:"afterEnvironment"`
	Environment       struct {
		Heading        string `json:"heading"`
		InvokedLine    string `json:"invokedLine"`
		PlatformLabel  string `json:"platformLabel"`
		OSVersionLabel string `json:"osVersionLabel"`
		PoweredByLine  string `json:"poweredByLine"`
	} `json:"environment"`
	Context struct {
		Intro              string `json:"intro"`
		Outro              string `json:"outro"`
		CurrentDateHeading string `json:"currentDateHeading"`
		CurrentDateLine    string `json:"currentDateLine"`
	} `json:"context"`
}

func init() {
	if err := json.Unmarshal(promptJSON, &prompt); err != nil || len(prompt.StableSections) != 2 {
		panic("invalid embedded ZCode prompt")
	}
}

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func stableUUID(seed string) string {
	h := sha256.Sum256([]byte(seed))
	h[6] = (h[6] & 15) | 64
	h[8] = (h[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[:4], h[4:6], h[6:8], h[8:10], h[10:16])
}
func randomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
func sessionID(in, device string) string {
	in = strings.TrimSpace(in)
	if uuidPattern.MatchString(in) {
		return strings.ToLower(in)
	}
	if in != "" {
		return stableUUID("zcode-session\x00" + in)
	}
	return stableUUID("zcode-session-device\x00" + device)
}
func userID(device, sid string) string {
	b, _ := json.Marshal(map[string]string{"device_id": device, "account_uuid": "", "session_id": sid})
	return string(b)
}
func cachedText(s string) map[string]any {
	return map[string]any{"type": "text", "text": s, "cache_control": map[string]any{"type": "ephemeral"}}
}
func clientLanguage() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		s := os.Getenv(k)
		if s != "" && s != "C" && s != "C.UTF-8" {
			return strings.ReplaceAll(strings.Split(s, ".")[0], "_", "-")
		}
	}
	return "unknown"
}
func clientTimezone() string {
	if s := os.Getenv("TZ"); s != "" {
		return s
	}
	if s := time.Now().Location().String(); s != "" && s != "Local" {
		return s
	}
	return "unknown"
}
func giftHeaders(jwt, sid string) (http.Header, error) {
	requestID, err := randomUUID()
	if err != nil {
		return nil, err
	}
	traceID, err := randomUUID()
	if err != nil {
		return nil, err
	}
	category := runtime.GOOS
	if category == "darwin" {
		category = "macos"
	} else if category == "windows" {
		category = "windows"
	} else {
		category = "linux"
	}
	return http.Header{
		"Content-Type": {"application/json"}, "Authorization": {"Bearer " + jwt}, "Anthropic-Version": {"2023-06-01"}, "Http-Referer": {"https://zcode.z.ai"}, "User-Agent": {"ZCode/" + getConfig().AppVersion + " ai-sdk/anthropic/3.0.81"}, "X-Zcode-App-Version": {getConfig().AppVersion}, "X-Title": {"Z Code@cli"}, "X-Release-Channel": {"production"}, "X-Client-Language": {clientLanguage()}, "X-Client-Timezone": {clientTimezone()}, "X-Zcode-Agent": {"glm"}, "X-Platform": {zcodePlatform()}, "X-Os-Category": {category}, "X-Os-Version": {osRelease()}, "X-Request-Id": {requestID}, "X-Zcode-Session-Type": {"main"}, "X-Zcode-Trace-Id": {traceID}, "X-Zcode-Query-Source": {"main_turn"}, "X-Session-Id": {sid},
	}, nil
}
func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}
func searchless(body map[string]any) bool {
	tools, ok := body["tools"].([]any)
	if !ok {
		return false
	}
	found, alias := false, false
	for _, v := range tools {
		t := asMap(v)
		if t["name"] == "ToolSearch" {
			found = true
		}
		if t["name"] == "ToolSearchTool" {
			alias = true
		}
	}
	if !found || alias {
		return false
	}
	var out []any
	for _, v := range tools {
		t, ok := v.(map[string]any)
		if !ok {
			out = append(out, v)
			continue
		}
		if t["name"] == "DeferredToolPlaceholder" {
			continue
		}
		delete(t, "defer_loading")
		if t["name"] == "ToolSearch" {
			t["name"] = "ToolSearchTool"
		}
		out = append(out, t)
	}
	body["tools"] = out
	if messages, ok := body["messages"].([]any); ok {
		for _, v := range messages {
			m := asMap(v)
			if m["role"] != "assistant" {
				continue
			}
			if blocks, ok := m["content"].([]any); ok {
				for _, x := range blocks {
					b := asMap(x)
					if b["type"] == "tool_use" && b["name"] == "ToolSearch" {
						b["name"] = "ToolSearchTool"
					}
				}
			}
		}
	}
	choice := asMap(body["tool_choice"])
	if choice["type"] == "tool" && choice["name"] == "ToolSearch" {
		choice["name"] = "ToolSearchTool"
	}
	return true
}
func restoreSearchName(raw []byte) []byte {
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return raw
	}
	changed := false
	if blocks, ok := body["content"].([]any); ok {
		for _, v := range blocks {
			b := asMap(v)
			if b["type"] == "tool_use" && b["name"] == "ToolSearchTool" {
				b["name"] = "ToolSearch"
				changed = true
			}
		}
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(body)
	if err != nil {
		return raw
	}
	return out
}
func prepareBody(raw []byte, c credential, model, sid string, gift bool) ([]byte, bool, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false, errors.New("invalid messages body")
	}
	messages, ok := body["messages"].([]any)
	if !ok {
		return nil, false, errors.New("messages array required")
	}
	body["model"] = model
	renamed := searchless(body)
	meta := asMap(body["metadata"])
	existing, _ := meta["user_id"].(string)
	if gift || strings.TrimSpace(existing) == "" {
		meta["user_id"] = userID(c.Device, sid)
	}
	body["metadata"] = meta
	if gift {
		var own []string
		switch s := body["system"].(type) {
		case string:
			if strings.TrimSpace(s) != "" {
				own = append(own, s)
			}
		case []any:
			for _, v := range s {
				m := asMap(v)
				text, _ := m["text"].(string)
				if m["type"] == "text" && strings.TrimSpace(text) != "" {
					own = append(own, text)
				}
			}
		}
		e := prompt.Environment
		provider := "zai-api"
		if c.Site == "bigmodel" {
			provider = "bigmodel-api"
		}
		osname := runtime.GOOS
		if osname == "windows" {
			osname = "win32"
		}
		arch := runtime.GOARCH
		if arch == "amd64" {
			arch = "x64"
		}
		powered := strings.ReplaceAll(strings.ReplaceAll(e.PoweredByLine, "{provider}", provider), "{model}", model)
		environment := strings.Join([]string{e.Heading, e.InvokedLine, "- " + e.PlatformLabel + ": " + osname, "- " + e.OSVersionLabel + ": " + osname + " " + osRelease() + " " + arch, powered}, "\n")
		dynamic := append([]string{prompt.BeforeEnvironment, environment, prompt.AfterEnvironment}, own...)
		body["system"] = []any{cachedText(prompt.CliPrefix), cachedText(strings.Join(prompt.StableSections, "\n\n")), cachedText("\n\n" + strings.Join(dynamic, "\n\n"))}
		date := time.Now().Format("2006-01-02")
		context := prompt.Context
		reminder := strings.Join([]string{context.Intro, context.CurrentDateHeading + "\n" + strings.ReplaceAll(context.CurrentDateLine, "{date}", date), "", context.Outro}, "\n")
		messages = append([]any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "<system-reminder>" + reminder + "</system-reminder>"}}}}, messages...)
		var last map[string]any
		for _, v := range messages {
			m, ok := v.(map[string]any)
			if !ok || m["role"] == "system" {
				continue
			}
			last = m
			if blocks, ok := m["content"].([]any); ok {
				for _, v := range blocks {
					if b, ok := v.(map[string]any); ok {
						delete(b, "cache_control")
					}
				}
			}
		}
		if last != nil {
			switch content := last["content"].(type) {
			case string:
				last["content"] = []any{cachedText(content)}
			case []any:
				if len(content) > 0 {
					if b, ok := content[len(content)-1].(map[string]any); ok {
						b["cache_control"] = map[string]any{"type": "ephemeral"}
					}
				}
			}
		}
		if tools, ok := body["tools"].([]any); ok {
			for _, v := range tools {
				if t, ok := v.(map[string]any); ok {
					delete(t, "cache_control")
				}
			}
		}
		body["messages"] = messages
	}
	out, err := json.Marshal(body)
	return out, renamed, err
}
