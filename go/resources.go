package main

import (
	_ "embed"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const pluginStoreID = "omc-zcode-oauth"
const resourcePrefix = "/v0/resource/plugins/" + pluginStoreID

//go:embed assets/zai.png
var zaiLogoPNG []byte

//go:embed assets/bigmodel.png
var bigModelLogoPNG []byte

// handleManagementResource serves only deployed public bytes. It deliberately
// has no host callback, auth directory, configuration or OAuth-state dependency.
func handleManagementResource(req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	var body []byte
	contentType := "image/png"
	cacheControl := "public, max-age=86400"
	switch req.Path {
	case resourcePrefix + "/zai.png":
		body = zaiLogoPNG
	case resourcePrefix + "/bigmodel.png":
		body = bigModelLogoPNG
	case omcResourcePath:
		body = []byte(omcDescriptorJSON)
		contentType = "application/json; charset=utf-8"
		cacheControl = "no-cache"
	default:
		return pluginapi.ManagementResponse{StatusCode: http.StatusNotFound, Body: []byte("not found")}, nil
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
			"Content-Type":           {contentType},
			"Cache-Control":          {cacheControl},
			"X-Content-Type-Options": {"nosniff"},
		},
		Body: append([]byte(nil), body...),
	}, nil
}
