<p align="center">
  <img src="assets/logo.png" width="96" height="96" alt="OMC ZCode OAuth logo">
</p>

<h1 align="center">OMC ZCode OAuth</h1>

<p align="center">
  <a href="https://github.com/WizisCool/oh-my-cpa"><img src="https://raw.githubusercontent.com/WizisCool/oh-my-cpa/main/web/src/assets/brand/omc-wordmark-light.svg#gh-light-mode-only" alt="Oh-My-CPA" height="22"></a>
  <a href="https://github.com/WizisCool/oh-my-cpa"><img src="https://raw.githubusercontent.com/WizisCool/oh-my-cpa/main/web/src/assets/brand/omc-wordmark-dark.svg#gh-dark-mode-only" alt="Oh-My-CPA" height="22"></a>
</p>

<p align="center">
  Native Z.ai / BigModel OAuth login for
  <a href="https://github.com/router-for-me/CLIProxyAPI">CLIProxyAPI</a>
</p>

<p align="center">
  <a href="https://github.com/WizisCool/omc-zcode-oauth/releases"><img src="https://img.shields.io/github/v/release/WizisCool/omc-zcode-oauth?label=release" alt="Release"></a>
  <a href="https://github.com/WizisCool/omc-zcode-oauth/actions/workflows/build.yml"><img src="https://github.com/WizisCool/omc-zcode-oauth/actions/workflows/build.yml/badge.svg" alt="CI"></a>
  <img src="https://img.shields.io/badge/CLIProxyAPI-v8+-4f46e5" alt="CLIProxyAPI v8+">
  <img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT license">
</p>

<p align="center">
  <a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a>
</p>

OMC ZCode OAuth adds the `zcode` provider to CLIProxyAPI. Sign in with a Z.ai or
BigModel.cn account from your panel's OAuth flow, and CPA gains model discovery,
Anthropic Messages execution, Coding Plan / team / gift routing and normalized
quota — no separate process, no local ZCode client.

The plugin is developed and maintained as part of the
[Oh-My-CPA](https://github.com/WizisCool/oh-my-cpa) ecosystem — the self-hosted
control plane and usage console for CLIProxyAPI.

## Two portals, one provider

ZCode accounts can sign in through two portals. Both produce the same
credential; pick a site per login, or fix the choice in config.

| Portal | `site` value | Authorize endpoint |
| --- | --- | --- |
| Z.ai | `zai` | `https://chat.z.ai/api/oauth/authorize` |
| BigModel.cn | `bigmodel` | `https://bigmodel.cn/login?appId=zcode` |

- `site: auto` (default): the OAuth tile opens an in-app chooser listing both sites.
- `site: zai` / `site: bigmodel`: the OAuth tile opens that vendor's authorize
  page directly.
- A request-scoped `?site=` parameter overrides the configured site for a
  single login.

## Panel-agnostic by design

Everything user-facing is rendered by the host: the OAuth tile, config fields,
resource pages and quota views all go through CPA's plugin ABI and management
API. The plugin is first-class in [Oh-My-CPA](https://github.com/WizisCool/oh-my-cpa)
and works the same in CPA's own Management Center or any other panel that speaks
CPA's management API.

Chooser links are origin-relative, so login works whether CPA is reached on
`127.0.0.1`, a public hostname or a reverse proxy. Set `public_base_url` when
you need an absolute link — a different origin for the management UI, a path
prefix, or a copyable cross-device URL.

## Features

- Z.ai and BigModel OAuth with an in-app chooser or a fixed site.
- Personal Coding Plan, team-seat and gift-only credentials.
- Model discovery from ZCode's client catalogue.
- Gift-only credentials route ordinary vendor model names through the gift service.
- Streaming and non-streaming Anthropic Messages requests.
- Normalized subscription, 5-hour, weekly, MCP and gift-bucket quota.
- Per-credential concurrency control.
- Bounded retry for ZCode concurrency errors and fail-closed handling for risk
  controls.

## Installation

Add this repository as a plugin-store source:

```yaml
plugins:
  enabled: true
  dir: /path/to/plugins
  store-sources:
    - https://raw.githubusercontent.com/WizisCool/omc-zcode-oauth/refs/heads/main/registry.json
  configs:
    omc-zcode-oauth:
      enabled: true
      mode: own
```

Then install `omc-zcode-oauth` from the plugin store and open its OAuth tile in
your panel. The store and shared-library name is `omc-zcode-oauth`; the CPA
credential provider identifier remains `zcode`. See
[docs/USAGE.md](docs/USAGE.md) for login, configuration, quota and
troubleshooting.

Releases are created by GitHub Actions whenever a `v*` tag is pushed.

## Configuration

| Field | Type | Default | Values |
| --- | --- | --- | --- |
| `site` | enum | `auto` | `zai`, `bigmodel`, `auto` |
| `mode` | enum | `own` | `own`, `team` |
| `max_concurrency` | integer | `3` | 1–100 requests per credential |
| `app_version` | string | `3.16.3` | ZCode client version |
| `team_language` | enum | `en` | `zh`, `en` |
| `public_base_url` | string | empty | Absolute URL of the CPA management UI, e.g. `https://cpa.example.com` or `https://example.com/cpa` |

## Development

The plugin implementation lives in [`go/`](go/). Tests run without network
access after modules are downloaded:

```sh
cd go
go mod download
GOPROXY=off GOSUMDB=off go test ./...
GOPROXY=off GOSUMDB=off go vet ./...
```

CI builds these release artifacts from a tag:

```text
omc-zcode-oauth_<version>_linux_amd64.zip
omc-zcode-oauth_<version>_linux_arm64.zip
omc-zcode-oauth_<version>_darwin_amd64.zip
omc-zcode-oauth_<version>_darwin_arm64.zip
omc-zcode-oauth_<version>_windows_amd64.zip
omc-zcode-oauth_<version>_windows_arm64.zip
checksums.txt
```

Each archive contains the correctly named `omc-zcode-oauth.so`,
`omc-zcode-oauth.dylib`, or `omc-zcode-oauth.dll`. CI builds all six targets;
local native builds use `make build`, and `make package` creates the same
archive format.

See [CONTRIBUTING.md](CONTRIBUTING.md) for commit conventions, test expectations, and
ecosystem development standards.

## Project layout

```text
.github/workflows/   CI tests, native builds and tagged releases
.github/scripts/     Release archive packager
assets/logo.png      Z.AI and plugin-store logo
assets/bigmodel.png  BigModel.cn sign-in logo
go/                  Go/cgo plugin implementation and tests
docs/USAGE.md        Operator installation and usage guide
registry.json        CLIProxyAPI plugin-store metadata
```

The Go implementation is independent. ZCode prompt compatibility data is taken
from the official ZCode `v3.14.3` source; see [NOTICE.md](NOTICE.md).

## License

[MIT](LICENSE)
