package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var waitFunc = time.Sleep

var waits = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 900 * time.Millisecond, 1600 * time.Millisecond, 2800 * time.Millisecond, 4000 * time.Millisecond}

type credentialGate struct {
	cond   *sync.Cond
	active int
}

var gateMu sync.Mutex
var gates = map[string]*credentialGate{}

func acquire(id string) func() {
	gateMu.Lock()
	g := gates[id]
	if g == nil {
		g = &credentialGate{cond: sync.NewCond(&gateMu)}
		gates[id] = g
	}
	for g.active >= getConfig().MaxConcurrency {
		g.cond.Wait()
	}
	g.active++
	gateMu.Unlock()
	return func() { gateMu.Lock(); g.active--; g.cond.Broadcast(); gateMu.Unlock() }
}

var riskState struct {
	sync.RWMutex
	authDir string
	blocked map[string]bool
}

func setAuthDir(dir string) {
	if dir == "" {
		return
	}
	riskState.Lock()
	riskState.authDir = dir
	if riskState.blocked == nil {
		riskState.blocked = map[string]bool{}
	}
	riskState.Unlock()
}
func riskHash(c credential, id string) string {
	identity := c.Site + "\x00" + c.UID
	if c.UID == "" {
		identity = id
	}
	h := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(h[:16])
}
func blockedAccount(c credential, id string) (bool, error) {
	key := riskHash(c, id)
	riskState.RLock()
	dir, blocked := riskState.authDir, riskState.blocked[key]
	riskState.RUnlock()
	if blocked {
		return true, nil
	}
	if dir == "" {
		return false, errors.New("risk marker directory unavailable")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false, errors.New("risk marker directory unavailable")
	}
	_, err := os.Stat(filepath.Join(dir, ".zcode-risk-"+key+".blocked"))
	if err == nil {
		return true, nil
	}
	if !os.IsNotExist(err) {
		return false, errors.New("cannot check risk marker")
	}
	return false, nil
}
func blockAccount(c credential, id string) error {
	key := riskHash(c, id)
	riskState.Lock()
	if riskState.blocked == nil {
		riskState.blocked = map[string]bool{}
	}
	riskState.blocked[key] = true
	dir := riskState.authDir
	riskState.Unlock()
	if dir == "" {
		return errors.New("risk marker directory unavailable")
	}
	f, err := os.OpenFile(filepath.Join(dir, ".zcode-risk-"+key+".blocked"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

type requestPlan struct {
	HTTP       pluginapi.HTTPRequest
	Gift       bool
	Model      string
	Renamed    bool
	Credential credential
	AuthID     string
}

func prepareRequest(req pluginapi.ExecutorRequest, sender httpSender) (requestPlan, error) {
	var plan requestPlan
	c, err := readCredential(req.StorageJSON)
	if err != nil {
		return plan, pluginabi.NewError("invalid_auth", "invalid ZCode credential", 401)
	}
	blocked, err := blockedAccount(c, req.AuthID)
	if err != nil {
		return plan, pluginabi.NewError("risk_store_unavailable", "cannot check ZCode account risk state", 503)
	}
	if blocked {
		return plan, pluginabi.NewError("force_cooldown", "ZCode account blocked by risk control (3012); contact ZCode support", 403)
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return plan, pluginabi.NewError("invalid_request", "model is required", 400)
	}
	gift := c.Key == ""
	if gift {
		if c.JWT == "" {
			return plan, pluginabi.NewError("invalid_auth", "ZCode gift sign-in required", 401)
		}
		b, e := fetchBalance(sender, c)
		if e != nil {
			return plan, pluginabi.NewError("gift_unavailable", "could not verify ZCode gift balance", 503)
		}
		if !giftHasModel(b, model) {
			return plan, pluginabi.NewError("quota_exhausted", "no usable ZCode gift bucket for requested model", 429)
		}
	}
	sid := sessionID(req.Headers.Get("x-session-id"), c.Device)
	body, renamed, err := prepareBody(req.Payload, c, model, sid, gift)
	if err != nil {
		return plan, pluginabi.NewError("invalid_request", "invalid Anthropic Messages request", 400)
	}
	target := c.base() + "/v1/messages"
	headers := req.Headers.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	profile := (*pluginapi.HTTPWireProfile)(nil)
	if gift {
		target = zcodeRoot + "/api/v1/zcode-plan/anthropic/v1/messages"
		headers, err = giftHeaders(c.JWT, sid)
		if err != nil {
			return plan, pluginabi.NewError("random_error", "cannot create request ID", 500)
		}
		profile = &pluginapi.HTTPWireProfile{DisableAutoCompression: true}
	} else {
		if c.Key == "" {
			return plan, pluginabi.NewError("invalid_auth", "Coding Plan key unavailable", 401)
		}
		if len(req.Query) > 0 {
			target += "?" + req.Query.Encode()
		}
		headers.Del("Authorization")
		headers.Del("x-api-key")
		headers.Del("Content-Length")
		headers.Set("Authorization", "Bearer "+c.Key)
		headers.Set("x-api-key", c.Key)
		headers.Set("x-session-id", sid)
		headers.Set("Content-Type", "application/json")
	}
	return requestPlan{HTTP: pluginapi.HTTPRequest{Method: "POST", URL: target, Headers: headers, Body: body, WireProfile: profile}, Gift: gift, Model: model, Renamed: renamed, Credential: c, AuthID: req.AuthID}, nil
}

type disposition struct {
	Retry   bool
	Code    string
	Status  int
	Message string
	Risk    bool
}

func errorCode(raw []byte) string {
	var d map[string]json.RawMessage
	if json.Unmarshal(raw, &d) != nil {
		return ""
	}
	for _, key := range []string{"code", "error"} {
		v := d[key]
		if len(v) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			if key == "code" {
				return s
			}
			continue
		}
		var n json.Number
		if key == "code" && json.Unmarshal(v, &n) == nil {
			return n.String()
		}
		if key == "error" {
			var inner map[string]json.RawMessage
			if json.Unmarshal(v, &inner) == nil {
				for _, k := range []string{"code", "type"} {
					if x := inner[k]; len(x) > 0 {
						if json.Unmarshal(x, &s) == nil {
							return s
						}
						if json.Unmarshal(x, &n) == nil {
							return n.String()
						}
					}
				}
			}
		}
	}
	return ""
}
func classify(status int, body []byte) disposition {
	lower := strings.ToLower(string(body))
	code := errorCode(body)
	if strings.Contains(lower, "errors.aliyun.com") {
		return disposition{Code: "request_scoped", Status: 400, Message: fmt.Sprintf("Aliyun WAF blocked the network/IP (upstream HTTP %d)", status)}
	}
	if (status == 405 && code == "3012") || strings.Contains(lower, "unusual activity") {
		return disposition{Code: "force_cooldown", Status: 403, Message: "ZCode account blocked by risk control (3012); contact ZCode support", Risk: true}
	}
	if code == "3008" || code == "3009" {
		return disposition{Retry: true, Code: code, Status: 429, Message: "ZCode concurrency limit reached (code " + code + ")"}
	}
	if code == "1113" || code == "1005" || status == 429 {
		return disposition{Code: "quota_exhausted", Status: 429, Message: "ZCode quota exhausted (code " + code + ")"}
	}
	if status >= 400 {
		return disposition{Code: "upstream_error", Status: status, Message: fmt.Sprintf("ZCode upstream failed (HTTP %d, code %s)", status, code)}
	}
	return disposition{}
}
func decisionError(d disposition) error { return pluginabi.NewError(d.Code, d.Message, d.Status) }
func recordRisk(plan requestPlan, d *disposition) {
	if d.Risk && blockAccount(plan.Credential, plan.AuthID) != nil {
		d.Message += "; could not persist the account risk marker, keep the plugin stopped until storage is fixed"
	}
}
func execute(req pluginapi.ExecutorRequest, sender httpSender) (pluginapi.ExecutorResponse, error) {
	release := acquire(req.AuthID)
	defer release()
	plan, err := prepareRequest(req, sender)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	for attempt := 0; ; attempt++ {
		resp, e := sender.Do(plan.HTTP)
		if e != nil {
			return pluginapi.ExecutorResponse{}, pluginabi.NewError("transport_error", "ZCode upstream transport failed", 503)
		}
		d := classify(resp.StatusCode, resp.Body)
		recordRisk(plan, &d)
		if d.Retry && attempt < len(waits) {
			waitFunc(waits[attempt])
			continue
		}
		if d.Retry {
			d.Code = "concurrency_exhausted"
			d.Status = 503
		}
		if d.Code != "" {
			return pluginapi.ExecutorResponse{}, decisionError(d)
		}
		body := resp.Body
		if plan.Renamed {
			body = restoreSearchName(body)
		}
		parsed, _ := url.Parse(plan.HTTP.URL)
		return pluginapi.ExecutorResponse{Payload: body, Headers: resp.Headers, Metadata: map[string]any{"upstream_host": parsed.Host, "upstream_model": plan.Model}}, nil
	}
}

type hostStreamResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	StreamID   string      `json:"stream_id"`
}
type hostReadResponse struct {
	Payload []byte `json:"payload"`
	Error   string `json:"error"`
	Done    bool   `json:"done"`
}

func readHostStream(id string) (hostReadResponse, error) {
	var r hostReadResponse
	err := hostRPC("host.http.stream_read", map[string]any{"stream_id": id}, &r)
	if err != nil {
		return r, errors.New("upstream stream read failed")
	}
	if r.Error != "" {
		return r, errors.New("upstream stream failed")
	}
	return r, nil
}
func closeHostStream(id string) {
	_ = hostRPC("host.http.stream_close", map[string]any{"stream_id": id}, nil)
}
func emitStream(id string, payload []byte) error {
	return hostRPC("host.stream.emit", map[string]any{"stream_id": id, "payload": payload}, nil)
}
func closeStream(id, message string) {
	_ = hostRPC("host.stream.close", map[string]any{"stream_id": id, "error": message}, nil)
}
func readWholeStream(id string) ([]byte, error) {
	defer closeHostStream(id)
	var out []byte
	for {
		r, e := readHostStream(id)
		if e != nil {
			return nil, e
		}
		out = append(out, r.Payload...)
		if len(out) > 4<<20 {
			return nil, errors.New("upstream error body too large")
		}
		if r.Done {
			return out, nil
		}
	}
}
func restoreStreamLine(line []byte) []byte {
	prefix := []byte("data:")
	if !bytes.HasPrefix(line, prefix) || !bytes.Contains(line, []byte("ToolSearchTool")) {
		return line
	}
	var event map[string]any
	if json.Unmarshal(bytes.TrimSpace(line[len(prefix):]), &event) != nil {
		return line
	}
	b := asMap(event["content_block"])
	if b["type"] != "tool_use" || b["name"] != "ToolSearchTool" {
		return line
	}
	b["name"] = "ToolSearch"
	raw, err := json.Marshal(event)
	if err != nil {
		return line
	}
	return append([]byte("data: "), raw...)
}

// emptyStreamWaits bounds the replay of a request whose upstream closed before
// the first payload. ZCode drops those quickly, so two short waits absorb a flap
// without hammering an account that is genuinely broken.
var emptyStreamWaits = []time.Duration{300 * time.Millisecond, 900 * time.Millisecond}

// Streaming host seams. Tests replace them to exercise retry behaviour without a
// plugin host; the defaults are the host RPCs used in production.
var (
	readStreamChunk  = readHostStream
	emitStreamChunk  = emitStream
	openSourceStream = openUpstreamStream
)

// openUpstreamStream replays the prepared request and returns the upstream
// stream ID, rejecting a response that is not an SSE stream.
func openUpstreamStream(plan requestPlan, callbackID string) (string, error) {
	var response hostStreamResponse
	if err := hostRPC("host.http.do_stream", map[string]any{"host_callback_id": callbackID, "request": plan.HTTP}, &response); err != nil {
		return "", errors.New("upstream stream failed")
	}
	if response.StreamID == "" {
		return "", errors.New("upstream stream unavailable")
	}
	if response.StatusCode >= 400 || strings.Contains(strings.ToLower(response.Headers.Get("Content-Type")), "application/json") {
		closeHostStream(response.StreamID)
		return "", errors.New("upstream stream rejected")
	}
	return response.StreamID, nil
}

// streamFramer turns upstream SSE bytes into the chunk shape the host expects for
// the client protocol. An Anthropic client receives the upstream bytes untouched;
// every other protocol is converted host-side, and that converter only accepts
// one event per chunk, prefixed with "data:" and starting at byte zero.
type streamFramer struct {
	dataOnly bool
	pending  []byte
}

func (f *streamFramer) next(chunk []byte) [][]byte {
	if !f.dataOnly {
		if len(chunk) == 0 {
			return nil
		}
		return [][]byte{chunk}
	}
	f.pending = bytes.ReplaceAll(append(f.pending, chunk...), []byte("\r\n"), []byte("\n"))
	var frames [][]byte
	for {
		i := bytes.Index(f.pending, []byte("\n\n"))
		if i < 0 {
			return frames
		}
		if frame := streamDataFrame(f.pending[:i]); frame != nil {
			frames = append(frames, frame)
		}
		f.pending = f.pending[i+2:]
	}
}

// flush releases a trailing event that the upstream ended without a blank line.
func (f *streamFramer) flush() [][]byte {
	if !f.dataOnly || len(f.pending) == 0 {
		return nil
	}
	frame := streamDataFrame(f.pending)
	f.pending = nil
	if frame == nil {
		return nil
	}
	return [][]byte{frame}
}

// streamDataFrame rebuilds one SSE event as a single "data:" payload.
func streamDataFrame(event []byte) []byte {
	var data []byte
	for _, line := range bytes.Split(event, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		if len(data) > 0 {
			data = append(data, '\n')
		}
		data = append(data, bytes.TrimSpace(line[len("data:"):])...)
	}
	if len(data) == 0 {
		return nil
	}
	return append(append([]byte("data: "), data...), '\n', '\n')
}

// restoreDataFrame applies the search-tool rename to a single data frame and
// keeps its event separator intact.
func restoreDataFrame(frame []byte) []byte {
	restored := restoreStreamLine(bytes.TrimRight(frame, "\n"))
	return append(append(restored, '\n'), '\n')
}

// pumpStream copies one upstream stream into the target and reports whether the
// upstream ended cleanly. emitted tracks payloads the client already received so
// a caller never replays a stream that produced output.
func pumpStream(source, target string, renamed, dataOnly bool, emitted *bool) bool {
	var held []byte
	framer := streamFramer{dataOnly: dataOnly}
	emit := func(frame []byte) bool {
		if len(frame) == 0 {
			return true
		}
		if emitStreamChunk(target, frame) != nil {
			return false
		}
		*emitted = true
		return true
	}
	for {
		r, err := readStreamChunk(source)
		if err != nil {
			return false
		}
		if dataOnly {
			for _, frame := range framer.next(r.Payload) {
				if renamed {
					frame = restoreDataFrame(frame)
				}
				if !emit(frame) {
					return false
				}
			}
		} else if payload := r.Payload; len(payload) > 0 {
			if renamed {
				held = append(held, payload...)
				if len(held) > 4<<20 {
					return false
				}
				var ready []byte
				for {
					i := bytes.IndexByte(held, '\n')
					if i < 0 {
						break
					}
					ready = append(ready, restoreStreamLine(held[:i])...)
					ready = append(ready, '\n')
					held = held[i+1:]
				}
				payload = ready
			}
			if !emit(payload) {
				return false
			}
		}
		if r.Done {
			if dataOnly {
				for _, frame := range framer.flush() {
					if renamed {
						frame = restoreDataFrame(frame)
					}
					if !emit(frame) {
						return false
					}
				}
			} else if len(held) > 0 {
				if !emit(restoreStreamLine(held)) {
					return false
				}
			}
			return true
		}
	}
}

// forwardStream streams one upstream response into the target stream. Nothing is
// emitted before the first upstream payload, so an upstream that closes empty or
// breaks before that point is replayed with the identical request bytes; once
// payload reached the client a failure is terminal.
func forwardStream(source, target string, plan requestPlan, callbackID string, dataOnly bool, release func()) {
	defer release()
	emitted := false
	for attempt := 0; ; attempt++ {
		finished := pumpStream(source, target, plan.Renamed, dataOnly, &emitted)
		closeHostStream(source)
		if finished && emitted {
			closeStream(target, "")
			return
		}
		if emitted || attempt >= len(emptyStreamWaits) {
			closeStream(target, "upstream stream closed before first payload")
			return
		}
		waitFunc(emptyStreamWaits[attempt])
		next, err := openSourceStream(plan, callbackID)
		if err != nil {
			closeStream(target, "upstream stream closed before first payload")
			return
		}
		source = next
	}
}

// dataFramedChunks reports whether stream chunks must be re-framed as one
// `data:` payload per upstream event. CPA rewrites SourceFormat to this plugin's
// own input format, so the client protocol is read from the request path the
// host records: a Messages client consumes the upstream SSE untouched, while
// every other client is converted by a host converter that only parses a chunk
// beginning at byte zero with "data:" and holding exactly one event.
func dataFramedChunks(req pluginapi.ExecutorRequest) bool {
	path := strings.ToLower(strings.TrimSpace(requestPath(req)))
	if path != "" {
		return !strings.HasPrefix(path, "/v1/messages") && !strings.HasPrefix(path, "/v8/messages")
	}
	source := strings.ToLower(strings.TrimSpace(req.SourceFormat))
	return source != "" && source != "claude"
}

func requestPath(req pluginapi.ExecutorRequest) string {
	path, _ := req.Metadata["request_path"].(string)
	return path
}

func executeStream(req pluginapi.ExecutorRequest, callbackID, streamID string, sender httpSender) (map[string]any, error) {
	if streamID == "" {
		return nil, pluginabi.NewError("invalid_request", "stream ID required", 400)
	}
	release := acquire(req.AuthID)
	plan, err := prepareRequest(req, sender)
	if err != nil {
		release()
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		var response hostStreamResponse
		err = hostRPC("host.http.do_stream", map[string]any{"host_callback_id": callbackID, "request": plan.HTTP}, &response)
		if err != nil {
			release()
			return nil, pluginabi.NewError("transport_error", "ZCode upstream stream failed", 503)
		}
		if response.StreamID == "" {
			release()
			return nil, pluginabi.NewError("transport_error", "ZCode upstream stream unavailable", 503)
		}
		if response.StatusCode >= 400 || strings.Contains(strings.ToLower(response.Headers.Get("Content-Type")), "application/json") {
			raw, e := readWholeStream(response.StreamID)
			if e != nil {
				release()
				return nil, pluginabi.NewError("transport_error", "ZCode upstream stream failed", 503)
			}
			d := classify(response.StatusCode, raw)
			recordRisk(plan, &d)
			if d.Retry && attempt < len(waits) {
				waitFunc(waits[attempt])
				continue
			}
			if d.Retry {
				d.Code = "concurrency_exhausted"
				d.Status = 503
			}
			if d.Code != "" {
				release()
				return nil, decisionError(d)
			}
			if plan.Renamed {
				raw = restoreSearchName(raw)
			}
			release()
			return map[string]any{"headers": response.Headers, "chunks": []pluginapi.ExecutorStreamChunk{{Payload: raw}}}, nil
		}
		go forwardStream(response.StreamID, streamID, plan, callbackID, dataFramedChunks(req), release)
		return map[string]any{"headers": response.Headers}, nil
	}
}
