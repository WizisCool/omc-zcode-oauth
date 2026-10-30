# Usage

## Requirements

- CLIProxyAPI v8 with dynamic plugin support and the quota provider.
- A CPA build that accepts the plugin's declared ABI and RPC schema: the plugin
  registers ABI 1 and schema 6 from its pinned SDK, and an older host refuses it at
  registration with `plugin schema version 6 is not supported`.
- Go 1.27.1 for local development. Release builds are produced by CI.

The dynamic dependencies observed in the v0.1.3 archives set that release's floor: the
linux/amd64 and linux/arm64 libraries resolve no symbol newer than glibc 2.34, both
macOS libraries declare a minimum of macOS 15.0, and the windows/amd64 library links the
Universal CRT. That is an observation about those artifacts, not an ABI guarantee or a
claim about other architectures and later releases; check the archive you install.

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
      mode: own
      max_concurrency: 3
      app_version: 3.16.3
      team_language: en
      # auto (default) shows the Z.AI / BigModel chooser page; zai or
      # bigmodel sends the browser straight to that vendor's authorize page.
      site: auto
      # Optional: absolute URL of this CPA management UI, e.g. behind NGINX.
      # public_base_url: https://cpa.example.com
```

Use CPA's plugin store to install `omc-zcode-oauth`, or call:

```text
POST /v0/management/plugin-store/omc-zcode-oauth/install
```

Select release `0.1.4` with `?version=0.1.4`.
The archive contains one library file and is installed under
`<plugins-dir>/<os>/<arch>/` as `omc-zcode-oauth-v<version>.so`, `.dylib` or `.dll`,
depending on the target platform: the installed version is part of the file name. CPA
also loads a hand-placed unversioned `omc-zcode-oauth.so`; that file carries no version,
so a pinned version in `plugins.configs` does not match it. The release publishes
`checksums.txt` covering every archive. Restart CPA if it reports that the loaded plugin
must be replaced.

Manual layout, for the version you install:

```text
<plugins-dir>/linux/amd64/omc-zcode-oauth-v<version>.so
<plugins-dir>/linux/arm64/omc-zcode-oauth-v<version>.so
<plugins-dir>/darwin/amd64/omc-zcode-oauth-v<version>.dylib
<plugins-dir>/darwin/arm64/omc-zcode-oauth-v<version>.dylib
<plugins-dir>/windows/amd64/omc-zcode-oauth-v<version>.dll
<plugins-dir>/windows/arm64/omc-zcode-oauth-v<version>.dll
```

## Description declaration

Releases from 0.1.4 publish an optional `<plugin_id>.omc.json` (`schema_version` 1)
with localized descriptions. A release that does not carry the file — including 0.1.3
— leaves readers on the description from `registry.json`.

When a release carries the file, the plugin serves the same bytes at
`/v0/resource/plugins/omc-zcode-oauth/omc.json`. The parts that matter here:

- `plugin_id` is `omc-zcode-oauth`, and `plugin_version` is the release version
  without a leading `v`. A document naming another id or version is ignored, not
  guessed at.
- `i18n.description` may contain any subset of `zh`, `en`, `zh-Hant` and `ms`; each
  value is plain text.
- It declares no menu, settings label or action, and it never changes the plugin name,
  logo or the description CPA's store already shows.
- CPA serves plugin resources without management authentication, and answers 404 for
  them in Home mode. When the resource is unavailable, a reader uses the release asset;
  when neither exists, the CPA description is the result.

The served body is never re-serialized: the plugin embeds the same file that packaging
copies into the release, so served and published bytes are comparable by hash. Bump the
file together with `registry.json`. The archive itself keeps its layout of one library
file at the root.

## Build locally

```sh
cd go
go mod download
GOPROXY=off GOSUMDB=off go test ./...
GOPROXY=off GOSUMDB=off go vet ./...
cd ..
make build VERSION=0.1.4
make package VERSION=0.1.4
```

`make build` uses the current machine's OS and architecture. Official release
artifacts for Linux, macOS and Windows on both amd64 and arm64 are built by the
`.github/workflows/build.yml` workflow when a `v*` tag is pushed.

## Sign in

Open CPA's Providers page and choose the ZCode sign-in tile. With the default
`site: auto`, the plugin opens a site chooser with separate Z.AI and BigModel.cn
entries, each using that site's logo. Choose the site where the subscription or
gift plan belongs.

Set `site: zai` or `site: bigmodel` to remove that page: the tile then returns
the vendor's own authorize URL, so the dialog shows `chat.z.ai` or
`bigmodel.cn` and no CPA-hosted address is involved. `site` is the first field
of the plugin configuration form, rendered as a dropdown. Site selection order
is the `site` query parameter, then `site` from the plugin configuration, then
the chooser:

```yaml
plugins:
  configs:
    omc-zcode-oauth:
      site: bigmodel
```

Saving the plugin configuration reconfigures the plugin in place, so switching
sites does not need a restart. Sign-in attempts started earlier keep the site
they were created with.

The chooser link is origin-relative by default, so the browser resolves it
against the host and path prefix that served the management UI. That covers
direct access, public hostnames and NGINX reverse proxies; make sure the proxy
forwards `/v0/resource/plugins/` alongside `/v0/management/` and `/v1/`. A
configured `site` makes this link unnecessary.

Set `public_base_url` when the management UI is served from a different origin,
lives under a path prefix, or you need an absolute link that can be copied to
another device:

```yaml
plugins:
  configs:
    omc-zcode-oauth:
      public_base_url: https://cpa.example.com
```

With `https://example.com/cpa` configured, the chooser link becomes
`https://example.com/cpa/v0/resource/plugins/omc-zcode-oauth/oauth-select`; the
path prefix is preserved so prefixed proxies keep working.

For direct or automated login, the same endpoints can skip the chooser:

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

Model IDs are the vendor model IDs from ZCode's catalogue, lowercased. Lowercase
matters: ZCode echoes lowercase model names, and CPA resolves a mixed-case
request against a lowercase registration but never the reverse, so a
`GLM-5.3-Flash` request for a `glm-5.3-flash` model fails with `no auth
available`. Re-read the model list after upgrading from a build that exposed
mixed-case IDs. The plugin does not create `-Trial` aliases.

A credential with a Coding Plan key uses the paid Anthropic endpoint. A
gift-only credential, identified by an empty Coding Plan key, only lists models
that have an active gift bucket and routes them to the ZCode gift service after
re-verifying that bucket. Expired, exhausted and future-starting buckets do not
pass that check, and a gift request never falls back to paid traffic.

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
- A stream that the vendor closes before its first payload is replayed up to two
  times with the same request bytes, because nothing has reached the client yet.
  Once a payload was emitted the stream is never replayed: the failure is
  reported instead so the client never sees duplicated output.
- Stream chunks follow the client protocol. A Messages client receives the
  upstream SSE untouched; every other client (Chat Completions, Responses,
  Gemini) gets exactly one `data:` event per chunk, because CPA's converters
  only parse a chunk that starts at byte zero with `data:` and holds one event.
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
- **The site chooser does not open:** confirm the browser can reach
  `<your CPA base URL>/v0/resource/plugins/omc-zcode-oauth/oauth-select`. The
  returned link is origin-relative, so a proxy that forwards only
  `/v0/management/` or `/v1/` breaks it; also set `public_base_url` when the
  management UI runs on a different origin or path prefix than CPA.
- **`503 ... last upstream error: empty_stream`:** CPA benches a credential
  after a failed upstream attempt, and with a single credential every request in
  that cooldown window repeats the same stale message. The plugin already
  replays an empty stream twice; if it keeps happening, wait out the cooldown and
  check the account and gift bucket on the vendor site.
- **Login stays pending:** complete the browser flow for the selected site.
  International and China accounts are separate; choose `Z.AI` or
  `BigModel.cn` to match the account.
- **A model is absent:** the vendor catalogue may not expose it, or a gift-only
  credential may have no active bucket containing that model.
- **A 3012 marker blocks calls:** check the account in ZCode. Only remove the
  marker after the vendor clears the account.
