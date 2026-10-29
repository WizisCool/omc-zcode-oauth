package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const zcodeRoot = "https://zcode.z.ai"

type credential struct {
	Site    string `json:"site"`
	Base    string `json:"base"`
	Key     string `json:"key"`
	JWT     string `json:"jwt"`
	Device  string `json:"device"`
	Plan    string `json:"plan"`
	Org     string `json:"org"`
	Project string `json:"project"`
	UID     string `json:"uid"`
}

func (c credential) root() string {
	if c.Site == "bigmodel" {
		return "https://bigmodel.cn"
	}
	return "https://api.z.ai"
}
func (c credential) base() string {
	if c.Site == "bigmodel" {
		return "https://open.bigmodel.cn/api/anthropic"
	}
	return "https://api.z.ai/api/anthropic"
}
func readCredential(b []byte) (credential, error) {
	var c credential
	if json.Unmarshal(b, &c) != nil || (c.Site != "zai" && c.Site != "bigmodel") || c.Device == "" || (c.Key == "" && c.JWT == "") {
		return c, errors.New("invalid zcode credential")
	}
	return c, nil
}
func field(m map[string]any, k, fallback string) string {
	if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

type vendorLoginFlow struct {
	FlowID   string
	URL      string
	Interval uint32
	Expires  int64
}

func beginVendorFlow(sender httpSender, site, appVersion string, bearerBytes []byte) (vendorLoginFlow, error) {
	var flow vendorLoginFlow
	bearer, device := deriveStateValues(bearerBytes)
	data, err := apiCall(sender, "POST", zcodeRoot+"/api/v1/oauth/cli/init", "Bearer "+bearer, device, nil, map[string]string{"provider": site})
	if err != nil {
		return flow, err
	}
	var response struct {
		FlowID   string `json:"flow_id"`
		URL      string `json:"authorize_url"`
		Interval uint32 `json:"poll_interval_sec"`
		Expires  int64  `json:"expires_at"`
	}
	if json.Unmarshal(data, &response) != nil || response.FlowID == "" || response.Expires <= time.Now().Unix() {
		return flow, errors.New("ZCode returned an invalid login flow")
	}
	u, err := url.Parse(response.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return flow, errors.New("ZCode returned an invalid authorization URL")
	}
	redirect := zcodeRoot + "/app/oauth/login?" + url.Values{"redirect": {"zcode://oauth/callback"}, "app_version": {appVersion}}.Encode()
	q := u.Query()
	if site == "bigmodel" {
		q.Set("redirect", redirect)
	} else {
		q.Set("redirect_uri", redirect)
	}
	u.RawQuery = q.Encode()
	return vendorLoginFlow{FlowID: response.FlowID, URL: u.String(), Interval: response.Interval, Expires: response.Expires}, nil
}

func startLogin(req pluginapi.AuthLoginStartRequest, sender httpSender) (pluginapi.AuthLoginStartResponse, error) {
	var out pluginapi.AuthLoginStartResponse
	cfg := getConfig()
	mode := field(req.Metadata, "mode", cfg.Mode)
	if mode != "own" && mode != "team" {
		return out, errors.New("invalid login mode")
	}
	if req.Host.AuthDir == "" {
		return out, errors.New("CPA auth directory unavailable")
	}
	setLoginAuthDir(req.Host.AuthDir)
	key, err := loadStateKey(req.Host.AuthDir)
	if err != nil {
		return out, err
	}
	nonce, err := newNonce()
	if err != nil {
		return out, errors.New("cannot generate login state")
	}
	bearerBytes, err := newRandomBytes(32)
	if err != nil {
		return out, errors.New("cannot generate login bearer")
	}
	site := field(req.Metadata, "site", "")
	if site == "" {
		flowID, err := newRandomFlowID()
		if err != nil {
			return out, errors.New("cannot generate login state")
		}
		expires := time.Now().Add(10 * time.Minute).Unix()
		state, err := sealState(key, loginState{FlowID: flowID, Mode: mode, Expires: expires, Nonce: nonce, Bearer: bearerBytes, Choice: true})
		if err != nil {
			return out, err
		}
		chooser, err := loginChooserURL(req.BaseURL, state)
		if err != nil {
			return out, err
		}
		return pluginapi.AuthLoginStartResponse{Provider: "zcode", URL: chooser, State: state, ExpiresAt: time.Unix(expires, 0).UTC()}, nil
	}
	if site != "zai" && site != "bigmodel" {
		return out, errors.New("invalid login site")
	}
	flow, err := beginVendorFlow(sender, site, cfg.AppVersion, bearerBytes)
	if err != nil {
		return out, err
	}
	state, err := sealState(key, loginState{FlowID: flow.FlowID, Site: site, Mode: mode, Expires: flow.Expires, Interval: flow.Interval, Nonce: nonce, Bearer: bearerBytes})
	if err != nil {
		return out, err
	}
	return pluginapi.AuthLoginStartResponse{Provider: "zcode", URL: flow.URL, State: state, ExpiresAt: time.Unix(flow.Expires, 0).UTC()}, nil
}

func pollLogin(req pluginapi.AuthLoginPollRequest, sender httpSender) (pluginapi.AuthLoginPollResponse, error) {
	pending := pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending}
	if req.Host.AuthDir != "" {
		setLoginAuthDir(req.Host.AuthDir)
	}
	key, err := loadStateKey(currentLoginAuthDir())
	if err != nil {
		return pending, err
	}
	s, err := openState(key, req.State)
	if err != nil {
		return pending, err
	}
	choiceID := s.FlowID
	if s.Choice {
		selected, ok := pendingLogin(choiceID)
		if !ok {
			if time.Now().Unix() >= s.Expires {
				return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "ZCode sign-in expired; sign in again"}, nil
			}
			return pending, nil
		}
		s = loginState{FlowID: selected.FlowID, Site: selected.Site, Mode: selected.Mode, Expires: selected.Expires, Interval: selected.Interval, Bearer: selected.Bearer}
	}
	if time.Now().Unix() >= s.Expires {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "ZCode sign-in expired; sign in again"}, nil
	}
	response, err := pollVendorFlow(s, sender)
	if err != nil {
		return pending, err
	}
	if s.Choice || choiceID != "" {
		if response.Status != pluginapi.AuthLoginStatusPending {
			clearPendingLogin(choiceID)
		}
	}
	return response, nil
}

func pollVendorFlow(s loginState, sender httpSender) (pluginapi.AuthLoginPollResponse, error) {
	pending := pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending}
	bearer, device := deriveStateValues(s.Bearer)
	data, err := apiCall(sender, "GET", zcodeRoot+"/api/v1/oauth/cli/poll/"+url.PathEscape(s.FlowID), "Bearer "+bearer, device, nil, nil)
	if err != nil {
		var e *apiError
		if errors.As(err, &e) && e.Status >= 400 && e.Status < 500 && e.Status != 408 && e.Status != 429 {
			return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: fmt.Sprintf("ZCode sign-in failed (HTTP %d, code %s)", e.Status, e.Code)}, nil
		}
		return pending, nil
	}
	var response struct {
		Status string `json:"status"`
		Token  string `json:"token"`
		ZAI    struct {
			Access string `json:"access_token"`
		} `json:"zai"`
		Bigmodel struct {
			Access    string `json:"access_token"`
			Alternate string `json:"accessToken"`
		} `json:"bigmodel"`
		User struct {
			Email string          `json:"email"`
			Name  string          `json:"name"`
			ID    json.RawMessage `json:"user_id"`
		} `json:"user"`
	}
	if json.Unmarshal(data, &response) != nil {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "ZCode returned an invalid login response"}, nil
	}
	if response.Status == "pending" || response.Status == "" {
		return pending, nil
	}
	if response.Status != "ready" {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "ZCode sign-in was declined"}, nil
	}
	siteToken := response.ZAI.Access
	if s.Site == "bigmodel" {
		siteToken = response.Bigmodel.Access
		if siteToken == "" {
			siteToken = response.Bigmodel.Alternate
		}
	}
	if siteToken == "" || response.Token == "" {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "ZCode login response is missing required credentials"}, nil
	}
	uid := rawString(response.User.ID)
	label := strings.TrimSuffix(response.User.Email, "@phone.local")
	if label == "" {
		label = response.User.Name
	}
	if uid == "" {
		uid = label
	}
	if uid == "" {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "ZCode login response is missing user identity"}, nil
	}
	c, err := chooseCredential(sender, s.Site, s.Mode, siteToken, response.Token, device, uid)
	if err != nil {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: err.Error()}, nil
	}
	if c.Org != "" && c.Project != "" {
		label += " (team)"
	}
	dataJSON, err := json.Marshal(c)
	if err != nil {
		return pending, errors.New("cannot encode credential")
	}
	sum := sha256.Sum256([]byte(s.Site + "\x00" + uid + "\x00" + c.Org + "\x00" + c.Project))
	id := "zcode-" + hex.EncodeToString(sum[:12]) + ".json"
	auth := pluginapi.AuthData{Provider: "zcode", ID: id, FileName: id, Label: label, StorageJSON: dataJSON, Metadata: map[string]any{"email": label, "type": "zcode"}}
	if c.Key == "" {
		if expiry := jwtExpiry(c.JWT); expiry > 0 {
			auth.NextRefreshAfter = time.Unix(expiry, 0).UTC()
		}
	}
	return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusSuccess, Auth: auth}, nil
}

func newRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(rand.Reader, b)
	return b, err
}

func newRandomFlowID() (string, error) {
	b, err := newRandomBytes(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func rawString(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return strings.TrimSpace(s)
	}
	if len(v) > 0 && string(v) != "null" {
		return string(v)
	}
	return ""
}
func jwtExpiry(s string) int64 {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return 0
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var c struct {
		Exp int64 `json:"exp"`
	}
	_ = json.Unmarshal(b, &c)
	return c.Exp
}
func parseAuth(req pluginapi.AuthParseRequest) pluginapi.AuthParseResponse {
	if req.Provider != "" && req.Provider != "zcode" {
		return pluginapi.AuthParseResponse{}
	}
	var metadata struct {
		Type  string `json:"type"`
		Email string `json:"email"`
	}
	if json.Unmarshal(req.RawJSON, &metadata) != nil || metadata.Type != "zcode" {
		return pluginapi.AuthParseResponse{}
	}
	c, err := readCredential(req.RawJSON)
	if err != nil {
		return pluginapi.AuthParseResponse{}
	}
	name := req.FileName
	if name == "" {
		name = req.Path
	}
	a := pluginapi.AuthData{Provider: "zcode", ID: name, FileName: req.FileName, Label: metadata.Email, StorageJSON: append([]byte(nil), req.RawJSON...), Metadata: map[string]any{"email": metadata.Email, "type": "zcode"}}
	if c.Key == "" {
		if exp := jwtExpiry(c.JWT); exp > 0 {
			a.NextRefreshAfter = time.Unix(exp, 0).UTC()
		}
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: a}
}
