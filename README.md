<p align="center">
  <img src="assets/logo.png" width="96" height="96" alt="OMC ZCode OAuth logo">
</p>

<h1 align="center">OMC ZCode OAuth</h1>

<p align="center">
  A native <a href="https://github.com/router-for-me/CLIProxyAPI">CLIProxyAPI</a>
  plugin for Z.ai and BigModel accounts.
</p>

OMC ZCode OAuth adds the `zcode` provider to CLIProxyAPI. It signs in through
CPA's Providers flow and exposes model discovery, Anthropic Messages execution,
Coding Plan and gift routing, and normalized quota information without a separate
process or local ZCode client.

## Features

- Z.ai and BigModel OAuth through an in-app two-site chooser.
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

Then install `omc-zcode-oauth` from CPA's plugin store. Its OAuth tile opens a
two-entry chooser for Z.AI and BigModel.cn before redirecting to the selected
site. Releases are created by GitHub Actions whenever a `v*` tag is pushed. See
[docs/USAGE.md](docs/USAGE.md) for configuration, login, quota and
troubleshooting. The store and shared-library name is `omc-zcode-oauth`; the CPA
credential provider identifier remains `zcode`.

## Configuration

| Field | Type | Default | Values |
| --- | --- | --- | --- |
| `mode` | enum | `own` | `own`, `team` |
| `max_concurrency` | integer | `3` | 1–100 requests per credential |
| `app_version` | string | `3.16.3` | ZCode client version |
| `team_language` | enum | `en` | `zh`, `en` |

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
omc-zcode-oauth_<version>_darwin_arm64.zip
checksums.txt
```

Each archive contains the correctly named `omc-zcode-oauth.so` or
`omc-zcode-oauth.dylib`. Local native builds use `make build`; `make package`
creates the same archive format.

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
