package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type fakeSender struct {
	mu       sync.Mutex
	requests []pluginapi.HTTPRequest
	reply    func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error)
}

func (f *fakeSender) Do(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	return f.reply(req)
}
func jsonReply(data string) pluginapi.HTTPResponse {
	return pluginapi.HTTPResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"code":0,"data":` + data + `}`)}
}
func storage(c credential) []byte { b, _ := json.Marshal(c); return b }
func fixtureBalance(now int64) string {
	return fmt.Sprintf(`{"server_time":%d,"plans":[{"plan_id":"p1","user_plan_id":"u1","name":"Active","status":"active","starts_at":%d,"ends_at":%d},{"plan_id":"p2","user_plan_id":"u2","name":"Tomorrow","status":"active","starts_at":%d,"ends_at":%d}],"balances":[{"plan_id":"p1","user_plan_id":"u1","entitlement_id":"a","capabilities":["model:GLM-5.3-Flash"],"total_units":100,"used_units":25,"remaining_units":75,"expires_at":%d},{"plan_id":"p2","user_plan_id":"u2","capabilities":["model:GLM-5.2"],"total_units":50,"remaining_units":50,"expires_at":%d}]}`, now, now-60, now+3600, now+300, now+7200, now+3600, now+7200)
}
func osStatMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}

func TestEmbeddedPromptHashes(t *testing.T) {
	cases := map[string]struct{ text, want string }{"cli": {prompt.CliPrefix, "46dd360a22c87a92"}, "stable0": {prompt.StableSections[0], "3f21ff9a88a03a76"}, "stable1": {prompt.StableSections[1], "39a1e86c93452c01"}, "before": {prompt.BeforeEnvironment, "bbdc66b399d297ff"}, "after": {prompt.AfterEnvironment, "1732b7f098a925d7"}}
	for name, c := range cases {
		sum := sha256.Sum256([]byte(c.text))
		if got := hex.EncodeToString(sum[:8]); got != c.want {
			t.Errorf("%s hash %s != %s", name, got, c.want)
		}
	}
}
func TestEncryptedStateAcrossCallsAndTampering(t *testing.T) {
	dir := t.TempDir()
	key, err := loadStateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	nonce, _ := newNonce()
	bearer := bytes.Repeat([]byte{0x2a}, 32)
	s := loginState{FlowID: "0123456789abcdef0123456789abcdef", Site: "bigmodel", Mode: "team", Expires: time.Now().Add(time.Minute).Unix(), Interval: 2, Nonce: nonce, Bearer: bearer}
	encoded, err := sealState(key, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 128 {
		t.Fatalf("state length=%d", len(encoded))
	}
	if strings.Contains(encoded, hex.EncodeToString(bearer)) {
		t.Fatal("state exposes bearer")
	}
	key2, err := loadStateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	round, err := openState(key2, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if round.FlowID != s.FlowID || round.Site != s.Site || round.Mode != s.Mode || !bytes.Equal(round.Bearer, bearer) {
		t.Fatal("state did not round-trip")
	}
	modified := []byte(encoded)
	if modified[10] == 'A' {
		modified[10] = 'B'
	} else {
		modified[10] = 'A'
	}
	if _, err = openState(key2, string(modified)); err == nil {
		t.Fatal("tampered state accepted")
	}
	if info, err := osStatMode(filepath.Join(dir, ".zcode-oauth-state.key")); err != nil || info&0077 != 0 {
		t.Fatalf("state key permissions=%o err=%v", info, err)
	}
}
func TestStartAndPollOAuth(t *testing.T) {
	now := time.Now().Unix()
	var initBearer, initDevice string
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		switch {
		case strings.HasSuffix(r.URL, "/oauth/cli/init"):
			initBearer = r.Headers.Get("Authorization")
			initDevice = r.Headers.Get("X-Device-Mid")
			return jsonReply(fmt.Sprintf(`{"flow_id":"0123456789abcdef0123456789abcdef","authorize_url":"https://example.test/login?redirect=old","poll_interval_sec":2,"expires_at":%d}`, now+300)), nil
		case strings.Contains(r.URL, "/oauth/cli/poll/"):
			if r.Headers.Get("Authorization") != initBearer || r.Headers.Get("X-Device-Mid") != initDevice {
				t.Error("poll identity changed")
			}
			return jsonReply(`{"status":"pending"}`), nil
		default:
			t.Errorf("unexpected URL %s", r.URL)
			return pluginapi.HTTPResponse{}, nil
		}
	}}
	dir := t.TempDir()
	start, err := startLogin(pluginapi.AuthLoginStartRequest{Host: pluginapi.HostConfigSummary{AuthDir: dir}, Metadata: map[string]any{"site": "bigmodel", "mode": "team"}}, sender)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(start.URL)
	if err != nil {
		t.Fatal(err)
	}
	back, err := url.Parse(u.Query().Get("redirect"))
	if err != nil || back.Host != "zcode.z.ai" || back.Query().Get("redirect") != "zcode://oauth/callback" || back.Query().Get("app_version") != "3.16.3" {
		t.Fatalf("wrong redirect")
	}
	if !strings.HasPrefix(initBearer, "Bearer ") || len(initBearer) != 71 || !uuidPattern.MatchString(initDevice) {
		t.Fatal("invalid login identity")
	}
	poll, err := pollLogin(pluginapi.AuthLoginPollRequest{Host: pluginapi.HostConfigSummary{AuthDir: dir}, State: start.State}, sender)
	if err != nil || poll.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("poll status %q err %v", poll.Status, err)
	}
}
func TestOAuthSiteChooserStartsSelectedSite(t *testing.T) {
	now := time.Now().Unix()
	var initCalls int
	var initProvider, initBearer, initDevice string
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		switch {
		case strings.HasSuffix(r.URL, "/oauth/cli/init"):
			initCalls++
			initBearer = r.Headers.Get("Authorization")
			initDevice = r.Headers.Get("X-Device-Mid")
			var body map[string]string
			if err := json.Unmarshal(r.Body, &body); err != nil {
				t.Fatal(err)
			}
			initProvider = body["provider"]
			return jsonReply(fmt.Sprintf(`{"flow_id":"0123456789abcdef0123456789abcdef","authorize_url":"https://bigmodel.cn/login?state=old","poll_interval_sec":2,"expires_at":%d}`, now+300)), nil
		case strings.Contains(r.URL, "/oauth/cli/poll/"):
			if r.Headers.Get("Authorization") != initBearer || r.Headers.Get("X-Device-Mid") != initDevice {
				t.Error("poll identity changed")
			}
			return jsonReply(`{"status":"pending"}`), nil
		default:
			t.Fatalf("unexpected URL %s", r.URL)
			return pluginapi.HTTPResponse{}, nil
		}
	}}
	dir := t.TempDir()
	start, err := startLogin(pluginapi.AuthLoginStartRequest{BaseURL: "http://127.0.0.1:8317/v0/management/oauth-callback", Host: pluginapi.HostConfigSummary{AuthDir: dir}}, sender)
	if err != nil {
		t.Fatal(err)
	}
	if initCalls != 0 {
		t.Fatal("site chooser contacted the vendor before a site was selected")
	}
	u, err := url.Parse(start.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.IsAbs() || u.Host != "" || u.Path != chooserPath || u.Query().Get("state") != start.State {
		t.Fatalf("wrong chooser URL %s", start.URL)
	}
	page, err := handleOAuthResource(pluginapi.ManagementRequest{Method: http.MethodGet, Path: u.Path, Query: u.Query()}, "")
	if err != nil || page.StatusCode != http.StatusOK || !bytes.Contains(page.Body, []byte("Z.AI")) || !bytes.Contains(page.Body, []byte("BigModel.cn")) {
		t.Fatalf("bad chooser page status=%d err=%v", page.StatusCode, err)
	}
	for _, site := range []string{"zai", "bigmodel"} {
		if !bytes.Contains(page.Body, []byte(`href="oauth-start?site=`+site+`&amp;state=`)) {
			t.Fatalf("chooser link for %s is not origin-relative", site)
		}
	}
	logo, err := handleOAuthResource(pluginapi.ManagementRequest{Method: http.MethodGet, Path: "/v0/resource/plugins/omc-zcode-oauth/bigmodel.png"}, "")
	if err != nil || logo.StatusCode != http.StatusOK || logo.Headers.Get("Content-Type") != "image/png" || !bytes.Equal(logo.Body, bigModelLogoPNG) {
		t.Fatal("BigModel logo resource unavailable")
	}
	redirect, err := handleOAuthResourceWithSender(pluginapi.ManagementRequest{Method: http.MethodGet, Path: "/v0/resource/plugins/omc-zcode-oauth/oauth-start", Query: url.Values{"state": {start.State}, "site": {"bigmodel"}}}, "", sender)
	if err != nil || redirect.StatusCode != http.StatusFound {
		t.Fatalf("start selected site status=%d err=%v body=%s", redirect.StatusCode, err, redirect.Body)
	}
	loginURL, err := url.Parse(redirect.Headers.Get("Location"))
	if err != nil || loginURL.Host != "bigmodel.cn" {
		t.Fatalf("wrong selected login URL")
	}
	if initCalls != 1 || initProvider != "bigmodel" {
		t.Fatalf("init calls=%d provider=%q", initCalls, initProvider)
	}
	poll, err := pollLogin(pluginapi.AuthLoginPollRequest{Host: pluginapi.HostConfigSummary{AuthDir: dir}, State: start.State}, sender)
	if err != nil || poll.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("poll status=%q err=%v", poll.Status, err)
	}
}

func TestStartLoginUsesConfiguredPublicBase(t *testing.T) {
	t.Cleanup(func() {
		if err := configure(nil); err != nil {
			t.Fatal(err)
		}
	})
	if err := configure([]byte("public_base_url: https://cpa.example.com\n")); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{reply: func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		t.Fatal("the site chooser contacted the vendor before a site was selected")
		return pluginapi.HTTPResponse{}, nil
	}}
	start, err := startLogin(pluginapi.AuthLoginStartRequest{Host: pluginapi.HostConfigSummary{AuthDir: t.TempDir()}}, sender)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://cpa.example.com" + chooserPath + "?state=" + url.QueryEscape(start.State)
	if start.URL != want {
		t.Fatalf("start URL = %q, want %q", start.URL, want)
	}
}

func TestChooserURLIsReachableThroughProxy(t *testing.T) {
	const state = "state-token"
	const relative = "/v0/resource/plugins/omc-zcode-oauth/oauth-select?state=state-token"
	for _, tc := range []struct {
		name   string
		public string
		want   string
	}{
		{"no public base stays origin-relative", "", relative},
		{"public base URL wins", "https://cpa.example.com", "https://cpa.example.com" + relative},
		{"public base URL keeps its path prefix", "https://example.com/cpa/", "https://example.com/cpa" + relative},
		{"invalid public base falls back", "cpa.example.com", relative},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := loginChooserURL(tc.public, state); got != tc.want {
				t.Fatalf("chooser URL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConfigValidatesPublicBaseURL(t *testing.T) {
	t.Cleanup(func() {
		if err := configure(nil); err != nil {
			t.Fatal(err)
		}
	})
	if err := configure([]byte("public_base_url: https://cpa.example.com/cpa\n")); err != nil {
		t.Fatal(err)
	}
	if got := getConfig().PublicBaseURL; got != "https://cpa.example.com/cpa" {
		t.Fatalf("public_base_url = %q", got)
	}
	for _, raw := range []string{"public_base_url: cpa.example.com\n", "public_base_url: ftp://cpa.example.com\n", "public_base_url: https://\n"} {
		if err := configure([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid configuration %q", raw)
		}
	}
}

func TestConfiguredSiteSkipsLocalChooser(t *testing.T) {
	t.Cleanup(func() {
		if err := configure(nil); err != nil {
			t.Fatal(err)
		}
	})
	now := time.Now().Unix()
	for _, tc := range []struct {
		name                                   string
		site, metadata, wantProvider, wantHost string
	}{
		{"configured zai goes straight to the vendor", "zai", "", "zai", "chat.z.ai"},
		{"configured bigmodel goes straight to the vendor", "bigmodel", "", "bigmodel", "bigmodel.cn"},
		{"request metadata overrides the configured site", "bigmodel", "zai", "zai", "chat.z.ai"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := configure([]byte("site: " + tc.site + "\n")); err != nil {
				t.Fatal(err)
			}
			var initProvider string
			sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
				if !strings.HasSuffix(r.URL, "/oauth/cli/init") {
					t.Fatalf("unexpected URL %s", r.URL)
				}
				var body map[string]string
				if err := json.Unmarshal(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				initProvider = body["provider"]
				authorize := "https://chat.z.ai/api/oauth/authorize?client_id=c1&state=old"
				if body["provider"] == "bigmodel" {
					authorize = "https://bigmodel.cn/login?appId=zcode&state=old"
				}
				return jsonReply(fmt.Sprintf(`{"flow_id":"0123456789abcdef0123456789abcdef","authorize_url":%q,"poll_interval_sec":2,"expires_at":%d}`, authorize, now+300)), nil
			}}
			metadata := map[string]any{}
			if tc.metadata != "" {
				metadata["site"] = tc.metadata
			}
			start, err := startLogin(pluginapi.AuthLoginStartRequest{Host: pluginapi.HostConfigSummary{AuthDir: t.TempDir()}, Metadata: metadata}, sender)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(start.URL)
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != tc.wantHost || initProvider != tc.wantProvider {
				t.Fatalf("login URL host = %q provider = %q, want host %q provider %q", u.Host, initProvider, tc.wantHost, tc.wantProvider)
			}
			if start.State == "" {
				t.Fatal("missing login state")
			}
		})
	}
}

func TestConfigValidatesSite(t *testing.T) {
	t.Cleanup(func() {
		if err := configure(nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, raw := range []string{"site: zai\n", "site: BigModel\n", "site:\n"} {
		if err := configure([]byte(raw)); err != nil {
			t.Fatalf("rejected %q: %v", raw, err)
		}
	}
	if got := getConfig().Site; got != "auto" {
		t.Fatalf("empty site should fall back to auto, got %q", got)
	}
	for _, raw := range []string{"site: z.ai\n", "site: both\n"} {
		if err := configure([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid %q", raw)
		}
	}
}

func TestPollStatusClassification(t *testing.T) {
	dir := t.TempDir()
	key, _ := loadStateKey(dir)
	nonce, _ := newNonce()
	state, _ := sealState(key, loginState{FlowID: "0123456789abcdef0123456789abcdef", Site: "zai", Mode: "own", Expires: time.Now().Add(time.Minute).Unix(), Nonce: nonce, Bearer: bytes.Repeat([]byte{1}, 32)})
	for _, tc := range []struct {
		status int
		want   pluginapi.AuthLoginStatus
	}{{408, pluginapi.AuthLoginStatusPending}, {429, pluginapi.AuthLoginStatusPending}, {500, pluginapi.AuthLoginStatusPending}, {403, pluginapi.AuthLoginStatusError}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			sender := &fakeSender{reply: func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
				return pluginapi.HTTPResponse{StatusCode: tc.status, Body: []byte(`{"code":1}`)}, nil
			}}
			got, err := pollLogin(pluginapi.AuthLoginPollRequest{Host: pluginapi.HostConfigSummary{AuthDir: dir}, State: state}, sender)
			if err != nil || got.Status != tc.want {
				t.Fatalf("status %q err %v", got.Status, err)
			}
		})
	}
}
func TestGiftEligibility(t *testing.T) {
	now := time.Now().Unix()
	var b balanceData
	if err := json.Unmarshal([]byte(fixtureBalance(now)), &b); err != nil {
		t.Fatal(err)
	}
	if !giftHasModel(b, "glm-5.3-flash") {
		t.Error("active bucket not eligible")
	}
	if giftHasModel(b, "GLM-5.2") {
		t.Error("future plan was eligible")
	}
	b.Balances[0].Remaining = number{}
	b.Balances[0].Used = number{}
	if !giftHasModel(b, "GLM-5.3-Flash") {
		t.Error("missing remaining should be unknown")
	}
	b.Balances[0].Used = number{Value: 100, Known: true}
	if giftHasModel(b, "GLM-5.3-Flash") {
		t.Error("spent bucket was eligible")
	}
	b.Plans[0].Starts = number{}
	b.Balances[0].Used = number{}
	if giftHasModel(b, "GLM-5.3-Flash") {
		t.Error("plan with unknown start was eligible")
	}
}
func TestGiftRequestWrapperAndPaidRoute(t *testing.T) {
	setAuthDir(t.TempDir())
	now := time.Now().Unix()
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		if strings.Contains(r.URL, "billing/balance") {
			return jsonReply(fixtureBalance(now)), nil
		}
		t.Fatalf("unexpected upstream call")
		return pluginapi.HTTPResponse{}, nil
	}}
	c := credential{Site: "bigmodel", JWT: "session.fake", Device: "00000000-0000-4000-8000-000000000001", UID: "uid"}
	payload := []byte(`{"model":"GLM-5.3-Flash","system":[{"type":"text","text":"agent rules"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}},{"type":"text","text":"last"}]}],"tools":[{"name":"Bash","cache_control":{"type":"ephemeral"}}],"max_tokens":100}`)
	req := pluginapi.ExecutorRequest{AuthID: "fixture", Model: "GLM-5.3-Flash", Payload: payload, StorageJSON: storage(c), Headers: http.Header{"Anthropic-Beta": {"bad"}, "X-Session-Id": {"conversation"}}, Query: url.Values{"beta": {"true"}}}
	plan, err := prepareRequest(req, sender)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Gift || plan.HTTP.URL != zcodeRoot+"/api/v1/zcode-plan/anthropic/v1/messages" || strings.Contains(plan.HTTP.URL, "?") {
		t.Fatalf("bad gift route")
	}
	if plan.HTTP.Headers.Get("Anthropic-Beta") != "" || len(plan.HTTP.Headers) != 19 {
		t.Fatalf("gift headers count=%d leaked_beta=%v", len(plan.HTTP.Headers), plan.HTTP.Headers.Get("Anthropic-Beta") != "")
	}
	var body map[string]any
	if json.Unmarshal(plan.HTTP.Body, &body) != nil {
		t.Fatal("invalid body")
	}
	if body["model"] != "GLM-5.3-Flash" {
		t.Fatal("gift model changed")
	}
	system := body["system"].([]any)
	if len(system) != 3 || asMap(system[0])["text"] != prompt.CliPrefix || asMap(system[1])["text"] != strings.Join(prompt.StableSections, "\n\n") || !strings.Contains(asMap(system[2])["text"].(string), "agent rules") {
		t.Fatal("wrong system blocks")
	}
	messages := body["messages"].([]any)
	prefix := asMap(messages[0])
	if prefix["role"] != "user" || !strings.Contains(asMap(prefix["content"].([]any)[0])["text"].(string), "<system-reminder>") {
		t.Fatal("missing system reminder")
	}
	last := asMap(messages[1])["content"].([]any)
	if _, ok := asMap(last[0])["cache_control"]; ok {
		t.Fatal("earlier cache control retained")
	}
	if _, ok := asMap(last[1])["cache_control"]; !ok {
		t.Fatal("last content not cached")
	}
	if _, ok := asMap(body["tools"].([]any)[0])["cache_control"]; ok {
		t.Fatal("tool cache control retained")
	}
	var user map[string]string
	if json.Unmarshal([]byte(asMap(body["metadata"])["user_id"].(string)), &user) != nil || user["device_id"] != c.Device || user["session_id"] != plan.HTTP.Headers.Get("x-session-id") {
		t.Fatal("wrong user metadata")
	}
	c.Key = "paid.fake"
	req.StorageJSON = storage(c)
	paid, err := prepareRequest(req, sender)
	if err != nil {
		t.Fatal(err)
	}
	if paid.Gift || !strings.HasPrefix(paid.HTTP.URL, "https://open.bigmodel.cn/api/anthropic/v1/messages") || paid.HTTP.Headers.Get("x-api-key") != c.Key || paid.HTTP.Headers.Get("Authorization") != "Bearer "+c.Key {
		t.Fatal("named paid model did not use plan")
	}
}
func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		want        string
		outStatus   int
		retry, risk bool
	}{{"user cap", 429, `{"code":3008}`, "3008", 429, true, false}, {"model cap", 429, `{"error":{"code":"3009"}}`, "3009", 429, true, false}, {"balance", 200, `{"code":1113}`, "quota_exhausted", 429, false, false}, {"quota", 200, `{"error":{"code":1005}}`, "quota_exhausted", 429, false, false}, {"generic 429", 429, `{}`, "quota_exhausted", 429, false, false}, {"risk", 405, `{"code":3012}`, "force_cooldown", 403, false, true}, {"unusual", 403, `{"message":"unusual activity"}`, "force_cooldown", 403, false, true}, {"WAF", 403, `<html>errors.aliyun.com</html>`, "request_scoped", 400, false, false}, {"5xx", 503, `{}`, "upstream_error", 503, false, false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := classify(tc.status, []byte(tc.body))
			if d.Code != tc.want || d.Status != tc.outStatus || d.Retry != tc.retry || d.Risk != tc.risk {
				t.Fatalf("decision=%+v", d)
			}
		})
	}
}
func TestBusyRetriesReuseBytes(t *testing.T) {
	setAuthDir(t.TempDir())
	old := waitFunc
	defer func() { waitFunc = old }()
	var actual []time.Duration
	waitFunc = func(d time.Duration) { actual = append(actual, d) }
	var bodies [][]byte
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		bodies = append(bodies, append([]byte(nil), r.Body...))
		if len(bodies) <= 6 {
			return pluginapi.HTTPResponse{StatusCode: 429, Body: []byte(`{"code":3008}`)}, nil
		}
		return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"type":"message","content":[]}`)}, nil
	}}
	c := credential{Site: "zai", Key: "testkey", Device: "00000000-0000-4000-8000-000000000001", UID: "retry"}
	_, err := execute(pluginapi.ExecutorRequest{AuthID: "retry", Model: "GLM-5.3", StorageJSON: storage(c), Payload: []byte(`{"model":"GLM-5.3","messages":[]}`)}, sender)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(waits) || len(bodies) != 7 {
		t.Fatalf("waits=%d asks=%d", len(actual), len(bodies))
	}
	for i := range waits {
		if actual[i] != waits[i] || !bytes.Equal(bodies[0], bodies[i+1]) {
			t.Fatal("retry changed request bytes or delays")
		}
	}
}
func TestRiskMarkerBlocksOtherModelsAndRestarts(t *testing.T) {
	dir := t.TempDir()
	setAuthDir(dir)
	c := credential{Site: "zai", Key: "testkey", Device: "00000000-0000-4000-8000-000000000001", UID: "risk-test"}
	if err := blockAccount(c, "one"); err != nil {
		t.Fatal(err)
	}
	riskState.Lock()
	riskState.blocked = map[string]bool{}
	riskState.Unlock()
	if blocked, err := blockedAccount(c, "two"); err != nil || !blocked {
		t.Fatal("persisted account marker missing")
	}
	sender := &fakeSender{reply: func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		t.Fatal("blocked account reached upstream")
		return pluginapi.HTTPResponse{}, nil
	}}
	if _, err := prepareRequest(pluginapi.ExecutorRequest{AuthID: "two", Model: "other-model", StorageJSON: storage(c)}, sender); err == nil {
		t.Fatal("blocked account was accepted")
	}
}

func TestRiskMarkerFailureDoesNotRetry(t *testing.T) {
	dir := t.TempDir()
	setAuthDir(dir)
	c := credential{Site: "zai", Key: "testkey", Device: "device", UID: "unwritable-risk"}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	d := classify(405, []byte(`{"code":3012}`))
	recordRisk(requestPlan{Credential: c, AuthID: "risk-failure"}, &d)
	if d.Retry || !d.Risk || !strings.Contains(d.Message, "could not persist") {
		t.Fatalf("risk decision did not report persistence failure: %+v", d)
	}
	if blocked, _ := blockedAccount(c, "risk-failure"); !blocked {
		t.Fatal("account must remain blocked in the current process")
	}
	riskState.Lock()
	riskState.blocked = map[string]bool{}
	riskState.Unlock()
	if _, err := blockedAccount(c, "risk-failure"); err == nil {
		t.Fatal("unavailable marker directory must fail closed")
	}
}

func TestRiskResponseNeverRetriedAndBlocksOtherModels(t *testing.T) {
	setAuthDir(t.TempDir())
	var calls int
	sender := &fakeSender{reply: func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		calls++
		return pluginapi.HTTPResponse{StatusCode: 405, Body: []byte(`{"code":3012}`)}, nil
	}}
	c := credential{Site: "zai", Key: "fixture-key", Device: "fixture-device", UID: "blocked-once"}
	req := pluginapi.ExecutorRequest{AuthID: "risk-execution", Model: "model-a", StorageJSON: storage(c), Payload: []byte(`{"model":"model-a","messages":[]}`)}
	if _, err := execute(req, sender); err == nil || !strings.Contains(err.Error(), "3012") {
		t.Fatal("risk response did not return the expected error")
	}
	req.Model = "model-b"
	if _, err := execute(req, sender); err == nil || calls != 1 {
		t.Fatalf("risk response reached the upstream again: calls=%d", calls)
	}
}

func TestLoginReportsLookupFailures(t *testing.T) {
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		if strings.Contains(r.URL, "getCustomerInfo") {
			return pluginapi.HTTPResponse{StatusCode: 502, Body: []byte(`{"code":502}`)}, nil
		}
		if strings.Contains(r.URL, "billing/balance") {
			return pluginapi.HTTPResponse{StatusCode: 503, Body: []byte(`{"code":503}`)}, nil
		}
		t.Fatal("unexpected lookup")
		return pluginapi.HTTPResponse{}, nil
	}}
	_, err := chooseCredential(sender, "bigmodel", "own", "fixture-token", "fixture-jwt", "fixture-device", "fixture-uid")
	if err == nil || !strings.Contains(err.Error(), "customer lookup failed") || !strings.Contains(err.Error(), "gift balance lookup failed") {
		t.Fatalf("lookup failures were misreported: %v", err)
	}
}

func TestRecordedBalanceQuota(t *testing.T) {
	raw, err := os.ReadFile("testdata/balance-recorded.json")
	if err != nil {
		t.Fatal(err)
	}
	var balance balanceData
	if err := json.Unmarshal(raw, &balance); err != nil {
		t.Fatal(err)
	}
	if !giftHasModel(balance, "GLM-5.3-Flash") {
		t.Fatal("recorded active gift bucket was not discovered")
	}
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		switch {
		case strings.Contains(r.URL, "/billing/balance"):
			return jsonReply(string(raw)), nil
		case strings.Contains(r.URL, "/billing/preview"):
			return jsonReply(`{"plans":[]}`), nil
		default:
			t.Fatalf("unexpected recorded quota request")
			return pluginapi.HTTPResponse{}, nil
		}
	}}
	q, err := quotaFetch(pluginapi.QuotaFetchRequest{StorageJSON: storage(credential{Site: "zai", JWT: "fixture-jwt", Device: "fixture-device", Plan: "ZCODE GLOBAL BUILD"})}, sender)
	if err != nil || q.Subscription == nil || q.Subscription.Plan != "ZCODE GLOBAL BUILD" || q.Subscription.TierName != "Gift" {
		t.Fatal("recorded balance subscription not normalized")
	}
	if len(q.Groups) != 1 || len(q.Groups[0].Buckets) != 1 || q.Groups[0].Buckets[0].Window != "ZCODE GLOBAL BUILD · glm-5.3-flash" || q.Groups[0].Buckets[0].RemainingFraction != 299998296.0/300000000 {
		t.Fatal("recorded balance bucket not normalized")
	}
}

func TestPersonalLoginMintsPlanKeyAndPreservesLabel(t *testing.T) {
	dir := t.TempDir()
	key, _ := loadStateKey(dir)
	nonce, _ := newNonce()
	state, _ := sealState(key, loginState{FlowID: "0123456789abcdef0123456789abcdef", Site: "bigmodel", Mode: "own", Expires: time.Now().Add(time.Minute).Unix(), Nonce: nonce, Bearer: bytes.Repeat([]byte{3}, 32)})
	var endpoints []string
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		u, _ := url.Parse(r.URL)
		endpoints = append(endpoints, u.Path)
		switch {
		case strings.Contains(u.Path, "/oauth/cli/poll/"):
			return jsonReply(`{"status":"ready","token":"fake.jwt.token","bigmodel":{"accessToken":"fake-site-token"},"user":{"email":"123@phone.local","user_id":"123"}}`), nil
		case u.Path == "/api/biz/customer/getCustomerInfo":
			if r.Headers.Get("Authorization") != "fake-site-token" {
				t.Fatal("BigModel site token not sent bare")
			}
			return jsonReply(`{"organizations":[{"organizationId":"org1","organizationName":"默认机构","projects":[{"projectId":"project1","projectName":"默认项目","projectType":"1"}]}]}`), nil
		case strings.HasSuffix(u.Path, "/api_keys"):
			return jsonReply(`[{"name":"zcode-api-key","apiKey":"key-id"}]`), nil
		case strings.HasSuffix(u.Path, "/copy/key-id"):
			return jsonReply(`{"secretKey":"test-secret"}`), nil
		case u.Path == "/api/biz/subscription/list":
			if r.Headers.Get("Authorization") != "key-id.test-secret" {
				t.Fatal("plan key not sent bare")
			}
			return jsonReply(`[{"status":"VALID","productName":"Coding Pro"}]`), nil
		default:
			t.Errorf("unexpected endpoint %s", u.Path)
			return pluginapi.HTTPResponse{}, nil
		}
	}}
	got, err := pollLogin(pluginapi.AuthLoginPollRequest{Host: pluginapi.HostConfigSummary{AuthDir: dir}, State: state}, sender)
	if err != nil || got.Status != pluginapi.AuthLoginStatusSuccess {
		t.Fatalf("login status %s err %v", got.Status, err)
	}
	c, err := readCredential(got.Auth.StorageJSON)
	if err != nil {
		t.Fatal(err)
	}
	if c.Site != "bigmodel" || c.Key != "key-id.test-secret" || c.Plan != "Coding Pro" || c.UID != "123" || got.Auth.Label != "123" || !got.Auth.NextRefreshAfter.IsZero() {
		t.Fatal("wrong personal credential fields")
	}
	if len(endpoints) != 5 {
		t.Fatalf("endpoint count %d", len(endpoints))
	}
}
func TestQuotaNormalizedFixture(t *testing.T) {
	now := time.Now().Unix()
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		switch {
		case strings.Contains(r.URL, "/usage/quota/limit"):
			return jsonReply(fmt.Sprintf(`{"limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":20,"nextResetTime":%d},{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":60},{"type":"TIME_LIMIT","unit":5,"number":1,"percentage":25}]}`, (now+3600)*1000)), nil
		case strings.Contains(r.URL, "customer-package-reset/list"):
			return jsonReply(`{"fiveHourResets":[{"available":true},{"available":false}],"weekResets":[{"available":true}]}`), nil
		case strings.Contains(r.URL, "billing/balance"):
			return jsonReply(fixtureBalance(now)), nil
		case strings.Contains(r.URL, "billing/preview"):
			return jsonReply(`{"plans":[{"plan_id":"a"},{"plan_id":"b"}]}`), nil
		default:
			t.Fatalf("unexpected quota endpoint")
			return pluginapi.HTTPResponse{}, nil
		}
	}}
	c := credential{Site: "zai", Key: "fixture-key", JWT: "fixture-jwt", Device: "device", Plan: "Coding Pro", UID: "id"}
	q, err := quotaFetch(pluginapi.QuotaFetchRequest{StorageJSON: storage(c)}, sender)
	if err != nil {
		t.Fatal(err)
	}
	if q.Subscription == nil || q.Subscription.Plan != "Coding Pro" || q.Subscription.TierName != "Coding Plan" || len(q.Groups) != 2 {
		t.Fatalf("unexpected quota shape")
	}
	paid := q.Groups[0].Buckets
	if len(paid) != 3 || paid[0].Window != "5h" || paid[0].RemainingFraction != 0.8 || paid[1].Window != "Weekly" || paid[1].RemainingFraction != 0.4 || !strings.Contains(paid[2].Description, "not a model limit") {
		t.Fatal("wrong paid quota")
	}
	gift := q.Groups[1].Buckets
	if len(gift) != 2 || gift[0].RemainingFraction != 0.75 || gift[1].RemainingFraction != 0 || !strings.Contains(gift[1].Description, "未开始") {
		t.Fatal("wrong gift quota")
	}
	metrics := map[string]float64{}
	for _, m := range q.Summary {
		metrics[m.Key] = m.Value
	}
	for k, v := range map[string]float64{"five_hour_resets": 1, "weekly_resets": 1, "gift_tokens_remaining": 75, "pending_claims": 2} {
		if metrics[k] != v {
			t.Errorf("metric %s=%v expected %v", k, metrics[k], v)
		}
	}
}
func TestModelsFromVendorCatalogAndGiftBalance(t *testing.T) {
	catalogCache.Lock()
	catalogCache.value = nil
	catalogCache.at = time.Time{}
	catalogCache.Unlock()
	now := time.Now().Unix()
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		switch {
		case strings.Contains(r.URL, "/client/configs"):
			return jsonReply(`{"configs":{"builtin_provider_config_json":"https://config.example.test/builtin.json"}}`), nil
		case strings.Contains(r.URL, "config.example.test"):
			return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"config":{"providerConfigRules":{"providerRules":[{"providerId":"account:zai-individual-coding-plan","builtinModelIds":["GLM-5.3-Flash"]}]},"modelConfigRules":{"modelRules":[{"modelMatch":"GLM-5.3-Flash","config":{"properties":{"contextWindow":900000},"optionSpecs":{"maxOutputTokens":{"max":120000},"reasoningLevel":{"values":["disabled","high"]}}}}]}}}`)}, nil
		case strings.Contains(r.URL, "billing/balance"):
			return jsonReply(fixtureBalance(now)), nil
		default:
			t.Fatalf("unexpected model endpoint")
			return pluginapi.HTTPResponse{}, nil
		}
	}}
	c := credential{Site: "zai", Key: "key", JWT: "jwt", Device: "device"}
	response, err := modelsForAuth(pluginapi.AuthModelRequest{StorageJSON: storage(c)}, sender)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Models) != 1 || response.Models[0].ID != "GLM-5.3-Flash" || response.Models[0].ContextLength != 900000 || response.Models[0].MaxCompletionTokens != 120000 || response.Models[0].Thinking.Levels[0] != "none" {
		t.Fatalf("wrong catalog models: %v", len(response.Models))
	}
}

func TestQuotaFailureKeepsKnownPlan(t *testing.T) {
	sender := &fakeSender{reply: func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		return pluginapi.HTTPResponse{}, fmt.Errorf("transport unavailable")
	}}
	c := credential{Site: "zai", Key: "key", JWT: "jwt", Device: "device", Plan: "Known plan"}
	q, err := quotaFetch(pluginapi.QuotaFetchRequest{StorageJSON: storage(c)}, sender)
	if err != nil {
		t.Fatal(err)
	}
	if q.Subscription == nil || q.Subscription.Plan != "Known plan" || q.Subscription.TierName != "Coding Plan" {
		t.Fatal("failed reads hid known subscription")
	}
}
func TestGiftModelCheckHasNoUpstreamFallback(t *testing.T) {
	setAuthDir(t.TempDir())
	now := time.Now().Unix()
	var calls []string
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		calls = append(calls, r.URL)
		if strings.Contains(r.URL, "billing/balance") {
			return jsonReply(fixtureBalance(now)), nil
		}
		return pluginapi.HTTPResponse{}, fmt.Errorf("unexpected request")
	}}
	c := credential{Site: "zai", JWT: "jwt", Device: "device", UID: "no-fallback"}
	_, err := execute(pluginapi.ExecutorRequest{AuthID: "no-fallback", Model: "GLM-5.2", StorageJSON: storage(c), Payload: []byte(`{"model":"GLM-5.2","messages":[]}`)}, sender)
	if err == nil {
		t.Fatal("unavailable gift model should fail")
	}
	if len(calls) != 1 || !strings.Contains(calls[0], "billing/balance") {
		t.Fatal("gift eligibility failure made an upstream request")
	}
}
func TestConcurrencyGatePerCredential(t *testing.T) {
	configMu.Lock()
	old := currentConfig
	currentConfig.MaxConcurrency = 1
	configMu.Unlock()
	defer func() { configMu.Lock(); currentConfig = old; configMu.Unlock() }()
	release := acquire("concurrency-fixture")
	arrived := make(chan struct{})
	go func() { unlock := acquire("concurrency-fixture"); close(arrived); unlock() }()
	select {
	case <-arrived:
		t.Fatal("second request entered before release")
	case <-time.After(20 * time.Millisecond):
	}
	other := acquire("other-fixture")
	other()
	release()
	select {
	case <-arrived:
	case <-time.After(time.Second):
		t.Fatal("waiting request not released")
	}
}
func TestAuthParseAndRefreshBehavior(t *testing.T) {
	raw := []byte(`{"type":"zcode","site":"zai","key":"fixture-key","jwt":"fixture-jwt","device":"fixture-device","plan":"plan","uid":"id","email":"alice"}`)
	p := parseAuth(pluginapi.AuthParseRequest{Provider: "zcode", FileName: "fixture.json", RawJSON: raw})
	if !p.Handled || p.Auth.Provider != "zcode" || p.Auth.ID != "fixture.json" {
		t.Fatal("credential not recognized")
	}
	if parseAuth(pluginapi.AuthParseRequest{Provider: "other", RawJSON: raw}).Handled {
		t.Fatal("other provider claimed")
	}
	_, err := handleMethod("auth.refresh", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "sign in again") {
		t.Fatal("refresh did not request relogin")
	}
}
