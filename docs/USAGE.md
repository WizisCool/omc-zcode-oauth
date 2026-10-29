# Usage

## Requirements

- CLIProxyAPI v8 with dynamic plugin support and the quota provider.
- Go 1.27.1 for local development. Release builds are produced by CI.

## Install from the plugin store

```yaml
plugins:
  enabled: true
  dir: /path/to/plugins
  store-sources:
    - https://raw.githubusercontent.com/WizisCool/omc-zcode-oauth/refs/heads/main/registry.json
  configs:
    omc-zcode-oauth:
      enabled: true
      site: zai
      mode: own
      prefer_gift: false
      max_concurrency: 3
      app_version: 3.16.3
      team_language: en
```

Use CPA's plugin store to install `omc-zcode-oauth`, or call:

```text
POST /v0/management/plugin-store/omc-zcode-oauth/install
```

A specific tagged release can be selected with `?version=0.1.0`. The release
archive is installed under
`<plugins-dir>/<os>/<arch>/omc-zcode-oauth.so`; macOS uses
`omc-zcode-oauth.dylib`. Restart CPA if it reports that the loaded plugin must
be replaced.

Manual layout:

```text
<plugins-dir>/linux/amd64/omc-zcode-oauth.so
<plugins-dir>/linux/arm64/omc-zcode-oauth.so
<plugins-dir>/darwin/arm64/omc-zcode-oauth.dylib
```

## Build locally

```sh
cd go
go mod download
GOPROXY=off GOSUMDB=off go test ./...
GOPROXY=off GOSUMDB=off go vet ./...
cd ..
make build VERSION=0.1.0
make package VERSION=0.1.0
```

`make build` uses the current machine's OS and architecture. Official release
artifacts for Linux amd64, Linux arm64 and Darwin arm64 are built by the
`.github/workflows/build.yml` workflow when a `v*` tag is pushed.

## Sign in

Open CPA's Providers page and choose a ZCode sign-in, or call:

```text
GET /v0/management/zcode-auth-url?site=zai
GET /v0/management/zcode-auth-url?site=bigmodel
```

Add `mode=team` to select a team seat. Complete the browser login, then poll:

```text
GET /v0/management/get-auth-status?state=<opaque-state>
```

The state is encrypted and expires. Keep the auth directory private; its
`type: zcode` files contain session material. ZCode session tokens cannot be
renewed, so start a new sign-in when they expire.

## Models and routing

Use the ordinary model name for a Coding Plan. A model ending in `-Trial`
selects a currently usable matching gift bucket and never falls back to paid
traffic. A gift-only credential routes ordinary model names to the ZCode gift
service.

`prefer_gift` is an operator preference for choosing `-Trial` model names; it
does not silently reroute a named paid model. Expired, exhausted and
future-starting gift buckets are not advertised as usable.

## Quota

`GET /v0/management/quota/providers` lists `zcode`. Fetch a credential's quota:

```text
POST /v0/management/quota/fetch
{"auth_index":"<auth index>"}
```

The response contains the subscription, 5-hour and weekly Coding Plan windows,
MCP usage as an informational bucket, live and pending gift buckets, personal
reset counts, remaining gift tokens, pending claims and server-time offset.
Quota reads retain the credential's known subscription if an optional endpoint
is unavailable. Resets stay on the vendor website.

## Error handling

- `3008` and `3009` are retried in place with bounded delays and identical
  request bytes.
- `1005`, `1113` and other quota `429` responses cool the reporting credential.
- Aliyun WAF HTML is surfaced as a network/IP block, not an account failure.
- `405` with `3012` or `unusual activity` is terminal for the account. The
  plugin never retries it and writes `.zcode-risk-<hash>.blocked` in the auth
  directory so later plugin calls cannot reach the upstream account.

Keep the auth directory writable and persistent. If the marker cannot be
written, the current process still blocks future upstream calls and reports a
storage error. Remove a risk marker only after the vendor clears the account
and the operator has reviewed it.

Team reset counts are not generated because the stored credential does not
contain the business token those endpoints require.

## Troubleshooting

- **The provider is absent:** enable the global `plugins` switch and
  `plugins.configs.omc-zcode-oauth.enabled`, then restart CPA if the library was
  replaced.
- **No models appear:** inspect model discovery errors in CPA logs and confirm
  the credential status is active.
- **Login stays pending:** complete the browser flow for the selected site.
  International and China accounts are separate; use `site=zai` or
  `site=bigmodel` to match the account.
- **A trial model is absent:** the gift plan may not have started, may have
  ended, or may have no remaining units.
- **A 3012 marker blocks calls:** check the account in ZCode. Only remove the
  marker after the vendor clears the account.
