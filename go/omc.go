package main

import (
	_ "embed"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const omcResourcePath = "/v0/resource/plugins/" + pluginStoreID + "/omc.json"

// omcDescriptorJSON is public, build-bound documentation, never runtime state.
// Release packaging copies this same file without reserializing it.
//
//go:embed assets/omc.json
var omcDescriptorJSON string

func handleManagementResource(req pluginapi.ManagementRequest, callbackID string) (pluginapi.ManagementResponse, error) {
	if req.Path != omcResourcePath {
		return handleOAuthResource(req, callbackID)
	}
	if req.Method != http.MethodGet {
		return pluginapi.ManagementResponse{
			StatusCode: http.StatusMethodNotAllowed,
			Headers:    http.Header{"Allow": {http.MethodGet}},
			Body:       []byte("method not allowed"),
		}, nil
	}
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers: http.Header{
			"Content-Type":           {"application/json; charset=utf-8"},
			"Cache-Control":          {"no-cache"},
			"X-Content-Type-Options": {"nosniff"},
		},
		Body: []byte(omcDescriptorJSON),
	}, nil
}
