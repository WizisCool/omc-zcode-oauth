# Contributing to OMC ZCode OAuth

`omc-zcode-oauth` is part of the [Oh-My-CPA](https://github.com/WizisCool/oh-my-cpa)
ecosystem. It provides native Z.ai and BigModel OAuth sign-in, model discovery,
Anthropic Messages execution, and normalized quota for CLIProxyAPI (CPA).

We welcome issues, feedback, and pull requests. Please adhere to the ecosystem
standards described below.

---

## Local Development

### Prerequisites

- Go 1.24+ with `CGO_ENABLED=1`
- `make`, `gcc` (or equivalent C toolchain for c-shared builds)
- Docker (optional, for end-to-end testing with a local CPA container)

### Building and Testing

All dependencies are vendored or pre-cached; tests run without network access:

```bash
cd go
go mod download

# Run unit tests and static checks offline
GOPROXY=off GOSUMDB=off go test ./...
GOPROXY=off GOSUMDB=off go vet ./...

# Build the native shared library for your current platform
cd ..
make build VERSION=0.1.3-local

# Package release archives for all 6 targets (Linux/macOS/Windows on amd64/arm64)
make package VERSION=0.1.3-local
```

The compiled binary is placed at `build/omc-zcode-oauth.so` (or `.dylib` / `.dll`).

---

## Commit Guidelines

This project strictly follows the **Oh-My-CPA Conventional Commits** standard.
Commits must be atomic, focused, and independently verified.

### Subject Format

```text
<type>(<scope>): <imperative subject>
```

- **Types**:
  - `feat`: A new user-facing feature or configuration option.
  - `fix`: A bug fix or behavioral correction.
  - `refactor`: Code change that neither fixes a bug nor adds a feature.
  - `perf`: A code change that improves performance.
  - `test`: Adding missing tests or correcting existing tests.
  - `docs`: Documentation-only changes.
  - `chore`: Maintenance tasks, build tooling, or release preparations.

- **Scopes**:
  - `oauth`: Sign-in flows, site selection, token exchange, and chooser UI.
  - `models`: Model catalog discovery, model info mapping, and gift filtering.
  - `executor`: Request execution, stream pumps, SSE framing, and retries.
  - `quota`: Balance queries, plan quota normalization, and rate limits.
  - `config`: Plugin configuration fields, parsing, and validation.
  - `registry`: Plugin store metadata (`registry.json`) and packaging.
  - `readme`: Project overview, documentation links, and branding.
  - `tooling`: Makefiles, CI workflows, and build scripts.
  - `release`: Release tags and version bumps.

### Commit Body Structure

Each non-trivial commit body should contain:

1. **Why**: The problem, bug symptom, or user need that prompted the change.
   Reference upstream issues or error messages where applicable.
2. **What changed**: The concrete architectural or behavioral changes made across
   the affected subsystems.
3. **Boundaries / Exclusions**: What was intentionally not changed or left out of
   scope.
4. **Verification**: A clear record of tests run, edge cases exercised, and live
   verification results.

#### Example

```text
fix(executor): re-frame sse events for non-claude client protocols

When downstream clients call OpenAI endpoints (/v1/chat/completions) or
OpenAI Responses (/v1/responses), CPA runs an internal stream translator
(such as ConvertClaudeResponseToOpenAI). That converter expects each
emitted chunk to begin with "data:" at byte zero; chunks starting with an
"event:" line are rejected, producing 0 translated frames and causing an
empty_stream 500 error even when upstream responded with valid SSE.

Add streamFramer to re-frame upstream SSE events into single
"data: {...}\n\n" chunks whenever the client is not using Claude Messages.
The client protocol is determined from Metadata["request_path"].

Anthropic /v1/messages requests remain untouched and receive raw upstream
SSE bytes.

Verification:
- Go test suite: TestStreamFramerEmitsOneDataFramePerEvent,
  TestStreamFramerFlushesTrailingEventAndSkipsEmptyData,
  TestStreamFramerKeepsClaudeFramesUntouched,
  TestStreamDataFramingReachesOpenAIConverter.
- Live CPA verification: /v1/chat/completions (HTTP 200, 66+ chunks),
  /v1/messages (HTTP 200, 45+ events), /v1/responses (HTTP 200, 56+ events).
- go test ./... and go vet ./... pass with GOPROXY=off.
```

---

## Testing & Quality Standards

- **Lowest layer first**: Write regression tests at the lowest possible layer
  (`go/*_test.go`). A bug fix must include a test that reproduces the failure
  without the fix.
- **Offline & hermetic**: Unit tests must pass with `GOPROXY=off GOSUMDB=off`.
  Never make live network calls in unit tests; use `fakeSender` and inject seams
  (`waitFunc`, `readStreamChunk`, `openSourceStream`).
- **No flaky sleeps**: Use synchronous channels, dependency injection, or
  virtual clocks (`waitFunc`) instead of `time.Sleep`.
- **CPA ABI safety**:
  - Catch panics and return `pluginabi.Error` with machine codes and HTTP status.
  - Do not expose internal auth directories, raw token bytes, or upstream
    network errors to client-facing error pages or responses.
  - Model IDs for CPA routing must always be lowercase (`strings.ToLower(id)`)
    because CPA registry lookups are case-sensitive.

---

## Documentation & Language Rules

- **Code & technical prose**: Code comments, commit messages, and primary
  technical documentation (`README.md`, `docs/USAGE.md`, `CONTRIBUTING.md`)
  are written in English.
- **Chinese translations**: User-facing documentation provides a Chinese
  mirror (`README.zh-CN.md`). Keep the English and Chinese versions in sync.
- **Domain vocabulary**: Preserve exact upstream and CPA terminology:
  - Provider identifier: `zcode`
  - Plugin identifier: `omc-zcode-oauth`
  - Portal names: `Z.ai` (`zai`), `BigModel.cn` (`bigmodel`)
  - Plans: `Coding Plan` (`own`), `Team Plan` (`team`), `Gift` (`gift`)

---

## Security & Secrets

- **Zero credential leaks**: Never commit API keys, JWT tokens, device IDs,
  session states, or `.oauth` / `.json` auth files.
- Test fixtures must use synthetic IDs (e.g. `zcode-000000000000000000000000.json`)
  or ephemeral `t.TempDir()`.
- Report security issues privately through GitHub Security Advisories or by
  contacting the maintainer directly.
