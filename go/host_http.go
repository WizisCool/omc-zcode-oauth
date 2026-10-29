package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type httpSender interface {
	Do(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error)
}
type cpaSender struct{ callbackID string }

func (s cpaSender) Do(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	var out pluginapi.HTTPResponse
	err := hostRPC("host.http.do", map[string]any{"host_callback_id": s.callbackID, "request": req}, &out)
	if err != nil {
		return out, fmt.Errorf("upstream transport failed")
	}
	return out, nil
}

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("upstream error HTTP %d code %s", e.Status, e.Code)
}
func apiCall(sender httpSender, method, target, auth, device string, extra http.Header, body any) (json.RawMessage, error) {
	h := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}, "User-Agent": {"ZCode/" + getConfig().AppVersion}}
	if auth != "" {
		h.Set("Authorization", auth)
	}
	if device != "" {
		h.Set("X-Device-Mid", device)
	}
	for k, v := range extra {
		h[k] = append([]string(nil), v...)
	}
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("invalid request")
		}
	}
	resp, err := sender.Do(pluginapi.HTTPRequest{Method: method, URL: target, Headers: h, Body: b})
	if err != nil {
		return nil, err
	}
	var env struct {
		Code json.RawMessage `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(resp.Body, &env) != nil {
		return nil, &apiError{Status: resp.StatusCode, Message: "non-JSON upstream response"}
	}
	var code string
	if len(env.Code) > 0 {
		if json.Unmarshal(env.Code, &code) != nil {
			code = string(env.Code)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (code != "" && code != "0" && code != "200") {
		return nil, &apiError{Status: resp.StatusCode, Code: code, Message: env.Msg}
	}
	return env.Data, nil
}
