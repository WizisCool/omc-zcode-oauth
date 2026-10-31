package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestPublicResourceRegistrationIsStaticOnly(t *testing.T) {
	value, err := handleMethod("management.register", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var registration pluginapi.ManagementRegistrationResponse
	if err := json.Unmarshal(raw, &registration); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, resource := range registration.Resources {
		paths = append(paths, resource.Path)
		if resource.Menu != "" {
			t.Error("static resources must not add a menu")
		}
	}
	if !reflect.DeepEqual(paths, []string{"/zai.png", "/bigmodel.png", "/omc.json"}) || len(registration.Routes) != 0 {
		t.Fatalf("public resource registration is not static-only: paths=%v routes=%v", paths, registration.Routes)
	}
}

// resourceHandlers exercise both the direct byte-serving helper and the exported
// management RPC entry point, including an untrusted callback ID in the latter.
func resourceHandlers() map[string]func(pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	return map[string]func(pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error){
		"helper": handleManagementResource,
		"rpc": func(req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
			raw, err := json.Marshal(rpcManagement{ManagementRequest: req, CallbackID: "synthetic-untrusted-callback"})
			if err != nil {
				return pluginapi.ManagementResponse{}, err
			}
			value, err := handleMethod("management.handle", raw)
			if err != nil {
				return pluginapi.ManagementResponse{}, err
			}
			return value.(pluginapi.ManagementResponse), nil
		},
	}
}

func resourceFilesystemSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		out[path] = info.Mode().String() + info.ModTime().String()
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] += string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPublicResourcesCannotCreateAuthStateOrRedirect(t *testing.T) {
	for _, exists := range []bool{false, true} {
		root := t.TempDir()
		dir := filepath.Join(root, "auth")
		// Seed the same directory as an authenticated native login. This also
		// initializes the old handler's directory when this test is run with
		// the pre-fix implementation for the red regression check.
		sender := &fakeSender{reply: func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
			return jsonReply(fmt.Sprintf(`{"flow_id":"0123456789abcdef0123456789abcdef","authorize_url":"https://vendor.example.test/authorize","expires_at":%d}`, time.Now().Add(time.Minute).Unix())), nil
		}}
		if _, err := startLogin(pluginapi.AuthLoginStartRequest{Host: pluginapi.HostConfigSummary{AuthDir: dir}, Metadata: map[string]any{"site": "zai"}}, sender); err != nil {
			t.Fatal(err)
		}
		key, err := loadStateKey(dir)
		if err != nil {
			t.Fatal(err)
		}
		nonce, err := newNonce()
		if err != nil {
			t.Fatal(err)
		}
		legacyState, err := sealState(key, loginState{FlowID: "0123456789abcdef0123456789abcdef", Choice: true, Expires: time.Now().Add(time.Minute).Unix(), Nonce: nonce, Bearer: bytes.Repeat([]byte{3}, 32)})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "synthetic-existing-credential.json"), []byte("synthetic sentinel, not a credential"), 0600); err != nil {
			t.Fatal(err)
		}
		if !exists {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}
		before := resourceFilesystemSnapshot(t, root)
		configBefore := getConfig()
		for name, handler := range resourceHandlers() {
			for _, state := range []string{"", "synthetic-untrusted-state", legacyState} {
				for _, suffix := range []string{"/oauth-select", "/oauth-start"} {
					for _, method := range []string{"", "get", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT", "UNKNOWN"} {
						response, err := handler(pluginapi.ManagementRequest{Method: method, Path: resourcePrefix + suffix, Query: url.Values{"state": {state}, "site": {"bigmodel", "zai"}}, Body: []byte("synthetic"), Headers: http.Header{"Authorization": {"Bearer synthetic"}}})
						if err != nil || response.StatusCode != http.StatusNotFound || response.Headers.Get("Location") != "" {
							t.Fatalf("%s retired public route %s %s not passive: status=%d err=%v", name, method, suffix, response.StatusCode, err)
						}
					}
				}
			}
		}
		if !reflect.DeepEqual(before, resourceFilesystemSnapshot(t, root)) {
			t.Fatalf("public requests mutated auth directory (exists=%v)", exists)
		}
		if !reflect.DeepEqual(configBefore, getConfig()) {
			t.Fatal("public requests mutated configuration")
		}
	}
}

func TestPublicResourcesAreExactQueryIndependentBytes(t *testing.T) {
	prefix := "/v0/resource/plugins/" + pluginStoreID
	for _, handler := range resourceHandlers() {
		for _, resource := range []struct{ path, contentType string }{
			{prefix + "/zai.png", "image/png"},
			{prefix + "/bigmodel.png", "image/png"},
			{omcResourcePath, "application/json; charset=utf-8"},
		} {
			expected, err := os.ReadFile(filepath.Join("assets", filepath.Base(resource.path)))
			if err != nil {
				t.Fatal(err)
			}
			for _, query := range []url.Values{nil, {"state": {"one"}, "site": {"zai"}}, {"state": {"two"}, "site": {"bigmodel", "zai"}, "token": {"synthetic-secret"}}} {
				response, err := handler(pluginapi.ManagementRequest{Method: http.MethodGet, Path: resource.path, Query: query, Body: []byte(`{"state":"synthetic-body"}`), Headers: http.Header{"Authorization": {"Bearer synthetic-secret"}}})
				if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(response.Body, expected) || response.Headers.Get("Content-Type") != resource.contentType || response.Headers.Get("Location") != "" {
					t.Errorf("static resource %q depended on request input: response=%+v err=%v", resource.path, response, err)
				}
				if len(response.Body) > 0 {
					response.Body[0] ^= 0xff
				}
				again, err := handler(pluginapi.ManagementRequest{Method: http.MethodGet, Path: resource.path})
				if err != nil || !bytes.Equal(again.Body, expected) {
					t.Fatalf("response buffer changed embedded resource %s", resource.path)
				}
			}
			for _, variant := range []string{"/prefix" + resource.path, resource.path + "/", resource.path + ".bak", strings.Replace(resource.path, "/plugins/"+pluginStoreID+"/", "/plugins/other/", 1), prefix + "/nested/" + filepath.Base(resource.path), resource.path + "?state=inline"} {
				response, err := handler(pluginapi.ManagementRequest{Method: http.MethodGet, Path: variant})
				if err != nil || response.StatusCode != http.StatusNotFound {
					t.Errorf("nonexact public path was accepted: %q status=%d err=%v", variant, response.StatusCode, err)
				}
			}
		}
	}
}

func TestRetiredPublicOAuthRoutesRejectEveryMethod(t *testing.T) {
	for _, handler := range resourceHandlers() {
		prefix := "/v0/resource/plugins/" + pluginStoreID
		for _, suffix := range []string{"/oauth-select", "/oauth-start"} {
			for _, path := range []string{prefix + suffix, prefix + suffix + "/", prefix + "/nested" + suffix, "/other" + suffix, prefix + "/.." + suffix, prefix + suffix + "?state=inline"} {
				for _, method := range []string{"", "get", http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodTrace, http.MethodConnect, "UNKNOWN"} {
					response, err := handler(pluginapi.ManagementRequest{Method: method, Path: path, Query: url.Values{"state": {"synthetic"}, "site": {"bigmodel"}}, Body: []byte("synthetic")})
					if err != nil || response.StatusCode != http.StatusNotFound || response.Headers.Get("Location") != "" {
						t.Errorf("retired route %q method=%q was not a passive 404: status=%d err=%v", path, method, response.StatusCode, err)
					}
				}
			}
		}
	}
}
