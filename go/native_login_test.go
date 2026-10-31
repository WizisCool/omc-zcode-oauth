package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Invalid site values are refused without any vendor request. Empty and "auto"
// are not invalid: they fall back to Z.AI (see TestNativeLoginDefaultsToZai).
func TestNativeLoginRejectsInvalidSiteWithoutSideEffects(t *testing.T) {
	t.Cleanup(func() { _ = configure(nil) })
	for _, tc := range []struct {
		name     string
		metadata map[string]any
	}{
		{"unknown", map[string]any{"site": "other"}}, {"url", map[string]any{"site": "https://example.test"}},
		{"null", map[string]any{"site": nil}}, {"number", map[string]any{"site": 1}},
		{"duplicate", map[string]any{"site": []string{"zai", "bigmodel"}}},
		{"json-array", map[string]any{"site": []any{"zai"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := configure(nil); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "absent-auth")
			sender := &fakeSender{reply: func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
				t.Error("invalid site contacted the vendor")
				return pluginapi.HTTPResponse{}, fmt.Errorf("unexpected request")
			}}
			start, err := startLogin(pluginapi.AuthLoginStartRequest{Host: pluginapi.HostConfigSummary{AuthDir: dir}, Metadata: tc.metadata}, sender)
			if err == nil || !strings.Contains(err.Error(), "site") || !strings.Contains(err.Error(), "zai") || !strings.Contains(err.Error(), "bigmodel") || start.URL != "" || start.State != "" {
				t.Fatalf("expected actionable site error with no flow: err=%v", err)
			}
			if len(sender.requests) != 0 {
				t.Fatal("rejected site made a vendor request")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("rejected site created auth state: %v", err)
			}
		})
	}
}

// A console that sends no site hint — OMC's connect flow — must still reach
// the vendor's authorize page. Empty and "auto" resolve to Z.AI.
func TestNativeLoginDefaultsToZai(t *testing.T) {
	t.Cleanup(func() { _ = configure(nil) })
	for _, tc := range []struct {
		name     string
		config   string
		metadata map[string]any
	}{
		{"no site hint anywhere", "", nil},
		{"metadata empty string", "", map[string]any{"site": ""}},
		{"metadata whitespace", "", map[string]any{"site": " "}},
		{"legacy auto configuration", "\nsite: auto", nil},
		{"metadata auto overrides configured", "site: bigmodel", map[string]any{"site": "auto"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := configure([]byte(tc.config)); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Unix()
			var initProvider string
			sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
				if !strings.HasSuffix(r.URL, "/oauth/cli/init") {
					return pluginapi.HTTPResponse{}, fmt.Errorf("unexpected request")
				}
				var body map[string]string
				if err := json.Unmarshal(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				initProvider = body["provider"]
				return jsonReply(fmt.Sprintf(`{"flow_id":"0123456789abcdef0123456789abcdef","authorize_url":"https://chat.z.ai/api/oauth/authorize?client_id=c1&state=old","poll_interval_sec":2,"expires_at":%d}`, now+300)), nil
			}}
			start, err := startLogin(pluginapi.AuthLoginStartRequest{Host: pluginapi.HostConfigSummary{AuthDir: t.TempDir()}, Metadata: tc.metadata}, sender)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(start.URL)
			if err != nil {
				t.Fatal(err)
			}
			if !u.IsAbs() || u.Host != "chat.z.ai" {
				t.Fatalf("login without a site hint did not reach the vendor authorize page: %q", start.URL)
			}
			if initProvider != "zai" {
				t.Fatalf("init provider = %q, want zai", initProvider)
			}
			if start.State == "" {
				t.Fatal("missing login state")
			}
		})
	}
}

func TestNativeLoginModeAndLegacyPublicBase(t *testing.T) {
	t.Cleanup(func() { _ = configure(nil) })
	for _, site := range []string{"zai", "bigmodel"} {
		for _, mode := range []string{"own", "team"} {
			t.Run(site+"/"+mode, func(t *testing.T) {
				if err := configure([]byte("site: " + site + "\nmode: " + mode + "\npublic_base_url: https://management.example.test/cpa\n")); err != nil {
					t.Fatal(err)
				}
				dir := t.TempDir()
				sender := &fakeSender{reply: func(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
					var init map[string]string
					if err := json.Unmarshal(req.Body, &init); err != nil || init["provider"] != site {
						t.Fatal("wrong native vendor selection")
					}
					return jsonReply(fmt.Sprintf(`{"flow_id":"0123456789abcdef0123456789abcdef","authorize_url":"https://vendor.example.test/authorize","poll_interval_sec":2,"expires_at":%d}`, time.Now().Add(time.Minute).Unix())), nil
				}}
				start, err := startLogin(pluginapi.AuthLoginStartRequest{Host: pluginapi.HostConfigSummary{AuthDir: dir}}, sender)
				if err != nil {
					t.Fatal(err)
				}
				key, err := loadStateKey(dir)
				if err != nil {
					t.Fatal(err)
				}
				state, err := openState(key, start.State)
				if err != nil || state.Site != site || state.Mode != mode || state.Choice {
					t.Fatalf("wrong native state: err=%v", err)
				}
				if !strings.HasPrefix(start.URL, "https://vendor.example.test/") || strings.Contains(start.URL, "management.example.test") || strings.Contains(start.URL, start.State) {
					t.Fatal("legacy public_base_url changed native login URL or exposed local state")
				}
			})
		}
	}
}

// Synthetic opaque states were generated by the unmodified v0.1.4 state codec.
// Keeping literal vectors proves compatibility without regenerating expectations
// with the code under test. They expire in 2100 and contain no real credentials.
var legacyLoginStates = map[string]string{
	"zai":      "AgICAgICAgICAgICBtLJSUpXNXuEzLzIXKXBsaG20uzoso5y3oTj-Hv4d3x0fB00bmyg7EQtUeqcJF1Y7xJXfGq13AWOJDoZrtt8XmAhCAWMQJWLjkLJlWN1",
	"bigmodel": "AgICAgICAgICAgICBtPJSUpXNXuEzLzIXKXBsaG20uzoso5y3oTj-Hv4d3x0fB00bmyg7EQtUeqcJF1Y7xJXfGq13AWOJDoZrtsIBqhMySM29IoZOBrssOP_",
	"chooser":  "AgICAgICAgICAgICBtrJSUpXNXuEzLzIXKXBsaG20uzoso5y3oTj-Hv4d3x0fB00bmyg7EQtUeqcJF1Y7xJXfGq13AWOJDoZrttamCNPATBZ4GkePIPguWcn",
}

func TestLegacyLoginStatesRemainCompatible(t *testing.T) {
	for site, state := range legacyLoginStates {
		t.Run(site, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".zcode-oauth-state.key"), bytes.Repeat([]byte{1}, 32), 0600); err != nil {
				t.Fatal(err)
			}
			sender := &fakeSender{reply: func(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
				if site == "chooser" {
					t.Fatal("retired chooser resumed vendor login")
				}
				bearer, device := deriveStateValues(bytes.Repeat([]byte{3}, 32))
				if req.URL != zcodeRoot+"/api/v1/oauth/cli/poll/0123456789abcdef0123456789abcdef" || req.Headers.Get("Authorization") != "Bearer "+bearer || req.Headers.Get("X-Device-Mid") != device {
					t.Fatal("legacy non-chooser state changed vendor identity")
				}
				return jsonReply(`{"status":"pending"}`), nil
			}}
			result, err := pollLogin(pluginapi.AuthLoginPollRequest{Host: pluginapi.HostConfigSummary{AuthDir: dir}, State: state}, sender)
			if err != nil {
				t.Fatal(err)
			}
			if site == "chooser" {
				if result.Status != pluginapi.AuthLoginStatusError || !strings.Contains(result.Message, "start sign-in again") || len(sender.requests) != 0 {
					t.Fatalf("legacy chooser did not request restart: status=%s", result.Status)
				}
			} else if result.Status != pluginapi.AuthLoginStatusPending || len(sender.requests) != 1 {
				t.Fatal("legacy non-chooser stopped polling")
			}
		})
	}
}

func TestNativeSitesStartPendingAndCompleteWithCompatibleCredentials(t *testing.T) {
	t.Cleanup(func() { _ = configure(nil) })
	if err := configure(nil); err != nil {
		t.Fatal(err)
	}
	for _, site := range []string{"zai", "bigmodel"} {
		t.Run(site, func(t *testing.T) {
			dir := t.TempDir()
			polls := 0
			var bearer, device string
			sender := &fakeSender{reply: func(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
				u, err := url.Parse(req.URL)
				if err != nil {
					t.Fatal(err)
				}
				switch {
				case strings.HasSuffix(u.Path, "/oauth/cli/init"):
					bearer, device = req.Headers.Get("Authorization"), req.Headers.Get("X-Device-Mid")
					return jsonReply(fmt.Sprintf(`{"flow_id":"0123456789abcdef0123456789abcdef","authorize_url":"https://vendor.example.test/authorize","poll_interval_sec":2,"expires_at":%d}`, time.Now().Add(time.Minute).Unix())), nil
				case strings.Contains(u.Path, "/oauth/cli/poll/"):
					if req.Headers.Get("Authorization") != bearer || req.Headers.Get("X-Device-Mid") != device {
						t.Fatal("poll identity changed")
					}
					polls++
					if polls == 1 {
						return jsonReply(`{"status":"pending"}`), nil
					}
					return jsonReply(`{"status":"ready","token":"synthetic.jwt.token","zai":{"access_token":"synthetic-site-token"},"bigmodel":{"accessToken":"synthetic-site-token"},"user":{"email":"synthetic@example.test","user_id":"123"}}`), nil
				case u.Path == "/api/auth/z/login":
					return jsonReply(`{"access_token":"synthetic-business-token"}`), nil
				case u.Path == "/api/biz/customer/getCustomerInfo":
					return jsonReply(`{"organizations":[{"organizationId":"org1","organizationName":"默认机构","projects":[{"projectId":"project1","projectName":"默认项目","projectType":"1"}]}]}`), nil
				case strings.HasSuffix(u.Path, "/api_keys"):
					return jsonReply(`[{"name":"zcode-api-key","apiKey":"synthetic-key-id"}]`), nil
				case strings.HasSuffix(u.Path, "/copy/synthetic-key-id"):
					return jsonReply(`{"secretKey":"synthetic-secret"}`), nil
				case u.Path == "/api/biz/subscription/list":
					return jsonReply(`[{"status":"VALID","productName":"Coding Pro"}]`), nil
				default:
					t.Fatalf("unexpected synthetic vendor path %q", u.Path)
					return pluginapi.HTTPResponse{}, nil
				}
			}}
			host := pluginapi.HostConfigSummary{AuthDir: dir}
			start, err := startLogin(pluginapi.AuthLoginStartRequest{Host: host, Metadata: map[string]any{"site": site}}, sender)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := pollLogin(pluginapi.AuthLoginPollRequest{Host: host, State: start.State}, sender)
			if err != nil || pending.Status != pluginapi.AuthLoginStatusPending {
				t.Fatalf("pending: %v %v", pending.Status, err)
			}
			// Config changes must not alter an already sealed, non-chooser login.
			if err := configure([]byte("site: auto\n")); err != nil {
				t.Fatal(err)
			}
			complete, err := pollLogin(pluginapi.AuthLoginPollRequest{Host: host, State: start.State}, sender)
			if err != nil || complete.Status != pluginapi.AuthLoginStatusSuccess {
				t.Fatalf("complete: %v %v", complete.Status, err)
			}
			cred, err := readCredential(complete.Auth.StorageJSON)
			if err != nil || cred.Site != site || cred.Key != "synthetic-key-id.synthetic-secret" || cred.UID != "123" || complete.Auth.Provider != "zcode" || complete.Auth.Label != "synthetic@example.test" {
				t.Fatal("native completion changed credential compatibility")
			}
			// CPA persists StorageJSON merged with the provider metadata.
			var persisted map[string]any
			if err := json.Unmarshal(complete.Auth.StorageJSON, &persisted); err != nil {
				t.Fatal(err)
			}
			for name, value := range complete.Auth.Metadata {
				persisted[name] = value
			}
			raw, err := json.Marshal(persisted)
			if err != nil {
				t.Fatal(err)
			}
			parsed := parseAuth(pluginapi.AuthParseRequest{Provider: "zcode", RawJSON: raw, FileName: complete.Auth.FileName, Host: host})
			if !parsed.Handled || !bytes.Equal(parsed.Auth.StorageJSON, raw) {
				t.Fatal("existing auth parser no longer accepts the persisted credential")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != ".zcode-oauth-state.key" {
				t.Fatal("plugin wrote a credential instead of returning it to CPA")
			}
		})
	}
}
