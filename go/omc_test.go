package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestOMCDescriptorMatchesBuildAndRegistry(t *testing.T) {
	source, err := os.ReadFile("assets/omc.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, []byte(omcDescriptorJSON)) {
		t.Fatal("embedded OMC descriptor differs from its release source")
	}
	var descriptor struct {
		SchemaVersion int    `json:"schema_version"`
		PluginID      string `json:"plugin_id"`
		PluginVersion string `json:"plugin_version"`
		I18N          struct {
			Description map[string]string `json:"description"`
		} `json:"i18n"`
	}
	decoder := json.NewDecoder(strings.NewReader(omcDescriptorJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.SchemaVersion != 1 || descriptor.PluginID != pluginStoreID || descriptor.PluginVersion != pluginVersion {
		t.Fatalf("descriptor identity/version differs from build: schema=%d id=%q version=%q, build=%q", descriptor.SchemaVersion, descriptor.PluginID, descriptor.PluginVersion, pluginVersion)
	}
	locales := []string{"zh", "en", "zh-Hant", "ms"}
	if len(descriptor.I18N.Description) != len(locales) {
		t.Fatal("OMC description must contain exactly the four supported locales")
	}
	for _, locale := range locales {
		if strings.TrimSpace(descriptor.I18N.Description[locale]) == "" {
			t.Errorf("missing OMC description for %s", locale)
		}
	}
	registryJSON, err := os.ReadFile("../registry.json")
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Plugins []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(registryJSON, &registry); err != nil {
		t.Fatal(err)
	}
	if len(registry.Plugins) != 1 || registry.Plugins[0].ID != descriptor.PluginID || registry.Plugins[0].Version != descriptor.PluginVersion {
		t.Fatal("registry and descriptor release identities differ")
	}
	metadata := registration().(map[string]any)["metadata"].(pluginapi.Metadata)
	if metadata.Version != descriptor.PluginVersion || metadata.Name != "OMC ZCode OAuth" || len(metadata.ConfigFields) != 6 {
		t.Fatal("native CPA metadata or configuration changed")
	}
}

func TestOMCResourceRegistrationIsStaticOnly(t *testing.T) {
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
	if len(registration.Routes) != 0 || len(registration.Resources) != 3 {
		t.Fatal("expected two static logos and one optional declaration")
	}
	want := []string{"/zai.png", "/bigmodel.png", "/omc.json"}
	for i, route := range registration.Resources {
		if route.Path != want[i] || route.Menu != "" {
			t.Errorf("resource %d path=%q menu=%q, want %q with no menu", i, route.Path, route.Menu, want[i])
		}
	}
}

func TestOMCResourceReturnsOnlyBuildBoundPublicBytes(t *testing.T) {
	for _, req := range []pluginapi.ManagementRequest{
		{Method: http.MethodGet, Path: omcResourcePath},
		{
			Method:  http.MethodGet,
			Path:    omcResourcePath,
			Headers: http.Header{"Authorization": {"Bearer synthetic-secret-not-public"}, "Cookie": {"session=synthetic-session"}},
			Query:   url.Values{"state": {"synthetic-state"}, "token": {"synthetic-token"}},
			Body:    []byte(`{"key":"synthetic-private-key","device":"synthetic-device"}`),
		},
	} {
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		// No initialized auth directory or host callback is needed for discovery.
		value, err := handleMethod("management.handle", raw)
		if err != nil {
			t.Fatal(err)
		}
		response := value.(pluginapi.ManagementResponse)
		if response.StatusCode != http.StatusOK || string(response.Body) != omcDescriptorJSON {
			t.Fatal("OMC resource is not the unchanged embedded document")
		}
		if response.Headers.Get("Content-Type") != "application/json; charset=utf-8" || response.Headers.Get("X-Content-Type-Options") != "nosniff" || response.Headers.Get("Cache-Control") != "no-cache" {
			t.Fatalf("incorrect discovery headers: %v", response.Headers)
		}
		// Response buffers cannot be used to corrupt subsequent public responses.
		response.Body[0] = '!'
		again, err := handleManagementResource(req)
		if err != nil || string(again.Body) != omcDescriptorJSON {
			t.Fatal("resource response mutated the embedded document")
		}
	}
}

func TestOMCResourceRequiresExactPathAndGET(t *testing.T) {
	for _, path := range []string{
		"/omc.json", omcResourcePath + "/", omcResourcePath + ".bak", omcResourcePath + "?state=synthetic",
		"/prefix" + omcResourcePath, "/v0/resource/plugins/another-plugin/omc.json",
		"/v0/resource/plugins/" + pluginStoreID + "/subdir/omc.json",
		"/v0/resource/plugins/" + pluginStoreID + "/../omc.json",
		"/v0/management/plugins/" + pluginStoreID + "/omc.json",
	} {
		t.Run(path, func(t *testing.T) {
			response, err := handleManagementResource(pluginapi.ManagementRequest{Method: http.MethodGet, Path: path})
			if err != nil || response.StatusCode != http.StatusNotFound {
				t.Fatalf("path %q: response=%+v err=%v", path, response, err)
			}
		})
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions, "get", ""} {
		response, err := handleManagementResource(pluginapi.ManagementRequest{Method: method, Path: omcResourcePath})
		if err != nil || response.StatusCode != http.StatusMethodNotAllowed || response.Headers.Get("Allow") != http.MethodGet {
			t.Errorf("method %q: response=%+v err=%v", method, response, err)
		}
	}
}

func TestOptionalOMCParsingFailureDoesNotGateCPARegistration(t *testing.T) {
	// The optional consumer may reject an invalid document; CPA registration
	// must not parse it or acquire an OMC/network dependency as a result.
	original := omcDescriptorJSON
	omcDescriptorJSON = "invalid optional document"
	t.Cleanup(func() { omcDescriptorJSON = original })
	for _, method := range []string{"plugin.register", "auth.identifier", "executor.identifier", "quota.identifier", "model.static"} {
		if _, err := handleMethod(method, nil); err != nil {
			t.Errorf("%s depends on optional OMC parsing: %v", method, err)
		}
	}
}
