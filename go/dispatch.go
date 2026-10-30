package main

import (
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var pluginVersion = "0.1.4"

type rpcAuthStart struct {
	pluginapi.AuthLoginStartRequest
	CallbackID string `json:"host_callback_id"`
}
type rpcAuthPoll struct {
	pluginapi.AuthLoginPollRequest
	CallbackID string `json:"host_callback_id"`
}
type rpcModel struct {
	pluginapi.AuthModelRequest
	CallbackID string `json:"host_callback_id"`
}
type rpcQuota struct {
	pluginapi.QuotaFetchRequest
	CallbackID string `json:"host_callback_id"`
}
type rpcManagement struct {
	pluginapi.ManagementRequest
	CallbackID string `json:"host_callback_id"`
}
type rpcExecution struct {
	pluginapi.ExecutorRequest
	CallbackID string `json:"host_callback_id"`
	StreamID   string `json:"stream_id"`
}

func unmarshal(raw []byte, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if json.Unmarshal(raw, out) != nil {
		return pluginabi.NewError("invalid_request", "invalid plugin request", 400)
	}
	return nil
}
func registration() any {
	return map[string]any{"schema_version": pluginabi.SchemaVersion, "metadata": pluginapi.Metadata{Name: "OMC ZCode OAuth", Version: pluginVersion, Author: "JUNZE", GitHubRepository: "https://github.com/WizisCool/omc-zcode-oauth", Logo: "https://raw.githubusercontent.com/WizisCool/omc-zcode-oauth/refs/heads/main/assets/logo.png", ConfigFields: []pluginapi.ConfigField{
		{Name: "site", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"zai", "bigmodel", "auto"}, Description: "ZCode site to sign in. zai and bigmodel send the browser straight to that vendor's authorize page; auto shows the Z.AI/BigModel chooser page instead (default auto)."},
		{Name: "mode", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"own", "team"}, Description: "Default OAuth plan selection (default own)."},
		{Name: "max_concurrency", Type: pluginapi.ConfigFieldTypeInteger, Description: "Maximum simultaneous requests per credential (default 3)."},
		{Name: "app_version", Type: pluginapi.ConfigFieldTypeString, Description: "ZCode client version (default 3.16.3)."},
		{Name: "team_language", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"zh", "en"}, Description: "Team business API language (default en)."},
		{Name: "public_base_url", Type: pluginapi.ConfigFieldTypeString, Description: "Public base URL of the CPA management UI (for example https://cpa.example.com) used for the OAuth site chooser link behind a reverse proxy (default empty: origin-relative link)."},
	}}, "capabilities": map[string]any{"auth_provider": true, "model_provider": true, "executor": true, "executor_model_scope": "oauth", "executor_input_formats": []string{"claude"}, "executor_output_formats": []string{"claude"}, "quota_provider": true, "management_api": true}}
}
func handleMethod(method string, raw []byte) (any, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		var r struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if err := unmarshal(raw, &r); err != nil {
			return nil, err
		}
		if err := configure(r.ConfigYAML); err != nil {
			return nil, pluginabi.NewError("invalid_config", err.Error(), 400)
		}
		return registration(), nil
	case "plugin.quiesce", "plugin.shutdown":
		return map[string]any{}, nil
	case "auth.identifier", "executor.identifier", "quota.identifier":
		return map[string]string{"identifier": "zcode"}, nil
	case "auth.parse":
		var r pluginapi.AuthParseRequest
		if err := unmarshal(raw, &r); err != nil {
			return nil, err
		}
		setAuthDir(r.Host.AuthDir)
		return parseAuth(r), nil
	case "auth.login.start":
		var r rpcAuthStart
		if err := unmarshal(raw, &r); err != nil {
			return nil, err
		}
		setAuthDir(r.Host.AuthDir)
		v, err := startLogin(r.AuthLoginStartRequest, cpaSender{})
		if err != nil {
			return nil, pluginabi.NewError("auth_error", err.Error(), 502)
		}
		return v, nil
	case "auth.login.poll":
		var r rpcAuthPoll
		if err := unmarshal(raw, &r); err != nil {
			return nil, err
		}
		setAuthDir(r.Host.AuthDir)
		v, err := pollLogin(r.AuthLoginPollRequest, cpaSender{})
		if err != nil {
			return nil, pluginabi.NewError("auth_error", err.Error(), 400)
		}
		return v, nil
	case "auth.refresh":
		return nil, pluginabi.NewError("reauth_required", "ZCode sessions cannot be refreshed; sign in again", 400)
	case "management.register":
		return map[string]any{"resources": []map[string]any{
			{"path": "/oauth-select", "menu": "", "description": "Choose Z.AI or BigModel.cn before sign-in."},
			{"path": "/oauth-start", "menu": "", "description": "Start the selected ZCode site sign-in."},
			{"path": "/zai.png", "menu": "", "description": "Z.AI sign-in logo."},
			{"path": "/bigmodel.png", "menu": "", "description": "BigModel sign-in logo."},
			{"path": "/omc.json", "menu": "", "description": "Public, optional OMC plugin declaration."},
		}}, nil
	case "management.handle":
		var r rpcManagement
		if err := unmarshal(raw, &r); err != nil {
			return nil, err
		}
		return handleManagementResource(r.ManagementRequest, r.CallbackID)
	case "model.static":
		return pluginapi.ModelResponse{Provider: "zcode"}, nil
	case "model.for_auth":
		var r rpcModel
		if err := unmarshal(raw, &r); err != nil {
			return nil, err
		}
		setAuthDir(r.Host.AuthDir)
		return modelsForAuth(r.AuthModelRequest, cpaSender{})
	case "quota.describe":
		return quotaDescription(), nil
	case "quota.fetch":
		var r rpcQuota
		if err := unmarshal(raw, &r); err != nil {
			return nil, err
		}
		setAuthDir(r.Host.AuthDir)
		return quotaFetch(r.QuotaFetchRequest, cpaSender{})
	case "quota.reset":
		return quotaReset(), nil
	case "executor.execute":
		var r rpcExecution
		if err := unmarshal(raw, &r); err != nil {
			return nil, err
		}
		return execute(r.ExecutorRequest, cpaSender{callbackID: r.CallbackID})
	case "executor.execute_stream":
		var r rpcExecution
		if err := unmarshal(raw, &r); err != nil {
			return nil, err
		}
		return executeStream(r.ExecutorRequest, r.CallbackID, r.StreamID, cpaSender{callbackID: r.CallbackID})
	case "executor.count_tokens":
		return nil, pluginabi.NewError("not_supported", "ZCode does not provide a verified token-count endpoint", http.StatusNotImplemented)
	case "executor.http_request":
		return nil, pluginabi.NewError("not_supported", "unsupported executor HTTP request", http.StatusNotImplemented)
	default:
		return nil, pluginabi.NewError("unknown_method", "unknown plugin method", http.StatusNotImplemented)
	}
}
