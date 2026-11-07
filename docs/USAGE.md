# Usage

> **Security fix (Issue #1):** v0.1.5 removes the dynamic public OAuth resources.
> v0.1.3 and v0.1.4 still expose them. Upgrade before using either older release;
> selecting a fixed site alone does not remove those routes.

## Requirements

- CLIProxyAPI v8 with dynamic plugin support and the quota provider.
- A CPA build that accepts the plugin's declared ABI and RPC schema: the plugin
  registers ABI 1 and schema 10 from its pinned SDK, and an older host refuses it at
  registration with `plugin schema version 10 is not supported`.
- **CPA v8.0.49 or later** to load 0.2.x at all (schema 10 first shipped there), and
  **CPA v8.0.51 or later** for the separate Z.AI and BigModel sign-in cards: per-entry
  name and logo (`oauth_provider_entries`) first shipped in v8.0.51. On an older host
  both entries still work — a panel that predates the field shows one card per
  *identifier* decorated with the plugin's own name and logo.
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
    - https://raw.githubusercontent.com/omc-community/omc-zcode-oauth/refs/heads/main/registry.json
  configs:
    omc-zcode-oauth:
      enabled: true
      mode: own
      max_concurrency: 3
      app_version: 3.16.3
      team_language: en
      # zai (default) or bigmodel. Login goes straight to that vendor's
      # authorize page; a site in the authenticated auth-url request overrides it.
      site: zai
      # Legacy setting accepted for compatibility; no longer used for OAuth.
      # public_base_url: https://cpa.example.com
```

Use CPA's plugin store to install `omc-zcode-oauth`, or call:

```text
POST /v0/management/plugin-store/omc-zcode-oauth/install
```

Select release `0.2.2` with `?version=0.2.2`.
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
make build VERSION=0.2.2
make package VERSION=0.2.2
```

`make build` uses the current machine's OS and architecture. Official release
artifacts for Linux, macOS and Windows on both amd64 and arm64 are built by the
`.github/workflows/build.yml` workflow when a `v*` tag is pushed.

## Sign in

The plugin registers **two OAuth login entries**, one per vendor portal:

| Entry identifier | Portal | Sign-in host |
| --- | --- | --- |
| `zcode-zai` | Z.AI | `chat.z.ai` |
| `zcode-bigmodel` | BigModel | `bigmodel.cn` |

Both produce the same credential shape (`"type": "zcode"`) and use the same
executor. On a panel that supports it, each entry renders as its own card with its
own name and logo (CPA v8.0.51+); the identifier is what CPA routes the login,
poll, and callback by.

`site` is no longer the way to pick a portal — the entry does that. It survives as
the fallback for a login request that names no entry, and is the first
configuration field:

```yaml
plugins:
  configs:
    omc-zcode-oauth:
      # Fallback only: used when a request names no entry (e.g. a legacy panel
      # that only knows the singular identifier). Defaults to zai.
      site: zai
```

Saving the configuration reconfigures the plugin without a restart. Existing
sign-in attempts retain their original site and mode.

Resolution order for the portal is: the entry the request names, then an explicit
`site` query parameter, then the configured `site`, then Z.AI. An explicit invalid
`site` is rejected rather than falling back to the configuration. A legacy
`auto` selection resolves to Z.AI. Old pending chooser sessions require a new
sign-in; existing session and credential files remain compatible, and every
credential written from 0.2.0 records its entry as `oauth_provider`.

These are CPA's existing management endpoints, not public plugin resources:

```text
GET /v8/management/oauth/auth-url?provider=zcode-zai
GET /v8/management/oauth/auth-url?provider=zcode-bigmodel
GET /v0/management/zcode-zai-auth-url
GET /v0/management/zcode-bigmodel-auth-url
# Legacy: names no entry, so the configured site decides the portal.
GET /v8/management/oauth/auth-url?provider=zcode&site=bigmodel
```

The caller must supply the management key in the `Authorization: Bearer ...`
header. Do not put the key in a URL. Use the URL returned by CPA to complete the
vendor's browser sign-in. A public request without that header cannot initiate
a flow. Public plugin resources serve only the exact static `zai.png`,
`bigmodel.png` and `omc.json` assets; query parameters never trigger an action.
CPA's standard HTTP handler returns a generic `500` for a rejected login start;
it does not expose the plugin's full error to the panel. Check the native plugin
configuration first; CPA logs contain the detailed reason.
`public_base_url` remains accepted and validated for existing configurations, but
has no effect on native OAuth and does not restore the removed public chooser.

The authorization URL from `/api/v1/oauth/cli/init` is preserved verbatim,
including the vendor's state and callback parameter (`redirect_uri` for Z.ai,
`redirect` for BigModel). The browser returns to ZCode's server-side
`/api/v1/oauth/cli/callback/zai` or `/api/v1/oauth/cli/callback/bigmodel`, which
completes the flow consumed by the plugin's CLI polling request. CPA's encrypted
polling state is separate from the vendor's OAuth state; do not replace either
state or substitute CPA's management callback URL. No VS Code or ZCode desktop
app, localhost listener, or manual callback URL submission is needed, including
when CPA runs on a remote server.

Add `mode=team` to select a team seat. Complete the browser login, then poll:

```text
GET /v0/management/get-auth-status?state=<opaque-state>
```

Polling also requires the management authorization header. Cancel a pending flow
with `DELETE /v0/management/oauth-session?state=<opaque-state>` (or the native v8
`DELETE /v8/management/oauth/session` equivalent). Cancellation is handled by CPA;
the plugin does not implement a separate cancellation endpoint.

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
- **Login asks for an explicit site:** no longer applicable — a missing, empty
  or legacy `auto` site now defaults to Z.AI. Only an invalid explicit `site`
  value is rejected; set the native `site` field to `bigmodel` for China-portal
  accounts. A pending chooser from an older build must be restarted.
- **`503 ... last upstream error: empty_stream`:** CPA benches a credential
  after a failed upstream attempt, and with a single credential every request in
  that cooldown window repeats the same stale message. The plugin already
  replays an empty stream twice; if it keeps happening, wait out the cooldown and
  check the account and gift bucket on the vendor site.
- **Login stays pending:** complete the browser flow for the selected site.
  International and China accounts are separate; choose `Z.AI` or
  `BigModel.cn` to match the account. If an older plugin build opened a desktop
  app prompt or left the browser at `/app/oauth/login`, update the plugin and
  start a new sign-in. The old authorization URL still contains the desktop
  redirect; a new flow is needed to use the server-side CLI callback.
- **A model is absent:** the vendor catalogue may not expose it, or a gift-only
  credential may have no active bucket containing that model.
- **A 3012 marker blocks calls:** check the account in ZCode. Only remove the
  marker after the vendor clears the account.
