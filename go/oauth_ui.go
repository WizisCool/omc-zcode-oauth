package main

import (
	_ "embed"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const pluginStoreID = "omc-zcode-oauth"

//go:embed assets/zai.png
var zaiLogoPNG []byte

//go:embed assets/bigmodel.png
var bigModelLogoPNG []byte

type selectedLogin struct {
	FlowID   string
	Site     string
	Mode     string
	Expires  int64
	Interval uint32
	Bearer   []byte
}

var selectedLoginMu sync.Mutex
var selectedLogins = map[string]selectedLogin{}

var loginDirMu sync.RWMutex
var loginDir string

func setLoginAuthDir(dir string) {
	loginDirMu.Lock()
	loginDir = strings.TrimSpace(dir)
	loginDirMu.Unlock()
}

func currentLoginAuthDir() string {
	loginDirMu.RLock()
	defer loginDirMu.RUnlock()
	return loginDir
}

func putSelectedLogin(choiceID string, selected selectedLogin) {
	selectedLoginMu.Lock()
	now := time.Now().Unix()
	for key, value := range selectedLogins {
		if value.Expires <= now || len(selectedLogins) > 64 {
			delete(selectedLogins, key)
		}
	}
	selectedLogins[choiceID] = selected
	selectedLoginMu.Unlock()
}

func pendingLogin(choiceID string) (selectedLogin, bool) {
	selectedLoginMu.Lock()
	selected, ok := selectedLogins[choiceID]
	selectedLoginMu.Unlock()
	if !ok || time.Now().Unix() >= selected.Expires {
		return selectedLogin{}, false
	}
	return selected, true
}

func clearPendingLogin(choiceID string) {
	selectedLoginMu.Lock()
	delete(selectedLogins, choiceID)
	selectedLoginMu.Unlock()
}

// chooserPath is the browser-facing site chooser route registered under
// /v0/resource/plugins/<pluginID>/.
const chooserPath = "/v0/resource/plugins/" + pluginStoreID + "/oauth-select"

// loginChooserURL builds the link CPA shows for the site chooser. CPA hands
// plugins its loopback management callback, so any absolute link derived from it
// is unreachable for public or reverse-proxied deployments. A configured
// public_base_url therefore wins, and without one the link stays path-only so
// the browser resolves it against the origin and path prefix that served the
// management UI.
func loginChooserURL(publicBase, state string) string {
	query := url.Values{"state": {state}}.Encode()
	if u, ok := httpBaseURL(publicBase); ok {
		u.Path += chooserPath
		u.RawQuery = query
		return u.String()
	}
	return chooserPath + "?" + query
}

// httpBaseURL parses an absolute http(s) base URL, keeping only the origin and
// path prefix a chooser link needs.
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

func beginSelectedLogin(state, site string, sender httpSender) (string, error) {
	if site != "zai" && site != "bigmodel" {
		return "", errors.New("choose Z.AI or BigModel.cn")
	}
	key, err := loadStateKey(currentLoginAuthDir())
	if err != nil {
		return "", err
	}
	s, err := openState(key, strings.TrimSpace(state))
	if err != nil || !s.Choice {
		return "", errors.New("this sign-in link is invalid; return to CPA and start again")
	}
	if time.Now().Unix() >= s.Expires {
		return "", errors.New("this sign-in link expired; return to CPA and start again")
	}
	bearer, err := newRandomBytes(32)
	if err != nil {
		return "", errors.New("cannot generate login bearer")
	}
	flow, err := beginVendorFlow(sender, site, getConfig().AppVersion, bearer)
	if err != nil {
		return "", err
	}
	putSelectedLogin(s.FlowID, selectedLogin{FlowID: flow.FlowID, Site: site, Mode: s.Mode, Expires: flow.Expires, Interval: flow.Interval, Bearer: bearer})
	return flow.URL, nil
}

func oauthChooserHTML(state string) []byte {
	escape := func(site string) string {
		return html.EscapeString("oauth-start?" + url.Values{"site": {site}, "state": {state}}.Encode())
	}
	return []byte(fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Choose ZCode site</title>
<style>
:root{color-scheme:light dark;font-family:Inter,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f4f5f7;color:#17181a}
main{width:min(680px,calc(100%% - 32px));padding:32px;border:1px solid #e0e2e6;border-radius:24px;background:#fff;box-shadow:0 24px 70px rgba(0,0,0,.08)}
h1{margin:0;font-size:26px;letter-spacing:-.03em}
p{margin:10px 0 24px;color:#666a73;line-height:1.6}
.choices{display:grid;grid-template-columns:1fr 1fr;gap:14px}
.choice{display:flex;align-items:center;gap:16px;min-height:112px;padding:18px;border:1px solid #dfe2e8;border-radius:18px;color:inherit;text-decoration:none;background:#fff;transition:transform .16s ease,border-color .16s ease,box-shadow .16s ease}
.choice:hover{transform:translateY(-2px);border-color:#a9adb5;box-shadow:0 12px 30px rgba(0,0,0,.08)}
.choice img{width:58px;height:58px;border-radius:14px;object-fit:cover;flex:none}
.choice strong{display:block;font-size:19px}
.choice small{display:block;margin-top:5px;color:#717681;font-size:13px}
.note{margin:22px 0 0;font-size:13px}
@media(max-width:560px){main{padding:24px}.choices{grid-template-columns:1fr}}
@media(prefers-color-scheme:dark){body{background:#111214;color:#f3f4f6}main{background:#1b1d21;border-color:#30333a}.choice{background:#202329;border-color:#383c44}.choice small,p,.note{color:#a7abb4}}
</style>
</head>
<body><main>
<h1>选择 ZCode 登录站点</h1>
<p>订阅、赠送额度和账号数据不会跨站共享。请选择账号所在的站点。</p>
<div class="choices">
<a class="choice" href="%s"><img src="zai.png" alt="Z.AI"><span><strong>Z.AI</strong><small>国际站 · z.ai</small></span></a>
<a class="choice" href="%s"><img src="bigmodel.png" alt="BigModel"><span><strong>BigModel.cn</strong><small>中国站 · bigmodel.cn</small></span></a>
</div>
<p class="note">选择后会跳转到对应站点的官方授权页面。</p>
</main></body></html>`, escape("zai"), escape("bigmodel")))
}

func oauthErrorHTML(message string) []byte {
	return []byte(fmt.Sprintf(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>ZCode sign-in error</title><body style="font-family:system-ui;padding:40px;max-width:680px;margin:auto"><h1>无法开始登录</h1><p>%s</p><p>请关闭当前窗口，回到 CPA 重新发起登录。</p></body></html>`, html.EscapeString(message)))
}

func handleOAuthResource(req pluginapi.ManagementRequest, callbackID string) (pluginapi.ManagementResponse, error) {
	return handleOAuthResourceWithSender(req, callbackID, cpaSender{callbackID: callbackID})
}

func handleOAuthResourceWithSender(req pluginapi.ManagementRequest, callbackID string, sender httpSender) (pluginapi.ManagementResponse, error) {
	if !strings.EqualFold(req.Method, http.MethodGet) {
		return pluginapi.ManagementResponse{StatusCode: http.StatusMethodNotAllowed, Body: []byte("method not allowed")}, nil
	}
	state := strings.TrimSpace(req.Query.Get("state"))
	switch {
	case strings.HasSuffix(req.Path, "/oauth-select"):
		// The auth directory is only known after a login start or poll, so an
		// unknown directory means this page outlived the process that made the
		// link; treat it like any other stale link instead of leaking internals.
		key, err := loadStateKey(currentLoginAuthDir())
		if err != nil {
			return pluginapi.ManagementResponse{StatusCode: http.StatusBadRequest, Headers: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: oauthErrorHTML("登录链接无效或已过期")}, nil
		}
		s, err := openState(key, state)
		if err != nil || !s.Choice || time.Now().Unix() >= s.Expires {
			return pluginapi.ManagementResponse{StatusCode: http.StatusBadRequest, Headers: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: oauthErrorHTML("登录链接无效或已过期")}, nil
		}
		return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{
			"Content-Type":            {"text/html; charset=utf-8"},
			"Cache-Control":           {"no-store"},
			"Content-Security-Policy": {"default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"},
			"Referrer-Policy":         {"no-referrer"},
			"X-Content-Type-Options":  {"nosniff"},
		}, Body: oauthChooserHTML(state)}, nil
	case strings.HasSuffix(req.Path, "/oauth-start"):
		u, err := beginSelectedLogin(state, strings.TrimSpace(req.Query.Get("site")), sender)
		if err != nil {
			return pluginapi.ManagementResponse{StatusCode: http.StatusBadRequest, Headers: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: oauthErrorHTML(err.Error())}, nil
		}
		return pluginapi.ManagementResponse{StatusCode: http.StatusFound, Headers: http.Header{"Location": {u}, "Cache-Control": {"no-store"}, "Referrer-Policy": {"no-referrer"}}}, nil
	case strings.HasSuffix(req.Path, "/zai.png"):
		return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": {"image/png"}, "Cache-Control": {"public, max-age=86400"}}, Body: zaiLogoPNG}, nil
	case strings.HasSuffix(req.Path, "/bigmodel.png"):
		return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": {"image/png"}, "Cache-Control": {"public, max-age=86400"}}, Body: bigModelLogoPNG}, nil
	default:
		return pluginapi.ManagementResponse{StatusCode: http.StatusNotFound, Body: []byte("not found")}, nil
	}
}
