package main

import (
	_ "embed"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const pluginStoreID = "omc-zcode-oauth"
const resourcePrefix = "/v0/resource/plugins/" + pluginStoreID

// publicLogoBaseURL is the published location of the same artwork that the
// resource routes serve. OAuth entry logos use it because a panel reads a
// plugin's logo as a document (the store manifests are URLs too) and only the
// CPA-native resource routes are guaranteed to be served to an unauthenticated
// management client.
const publicLogoBaseURL = "https://raw.githubusercontent.com/omc-community/omc-zcode-oauth/refs/heads/main/assets/"

// resourceAssetURL is the public address of one embedded logo. Only the two
// names registered below are addressable, so the set of URLs the plugin can
// publish stays equal to the set of bytes it ships.
func resourceAssetURL(name string) string {
	switch name {
	case "zai.png", "bigmodel.png":
		return publicLogoBaseURL + name
	default:
		return ""
	}
}

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
