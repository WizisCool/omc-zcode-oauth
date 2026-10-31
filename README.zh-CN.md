<p align="center">
  <img src="assets/logo.png" width="96" height="96" alt="OMC ZCode OAuth logo">
</p>

<h1 align="center">OMC ZCode OAuth</h1>

<p align="center">
  <a href="https://github.com/WizisCool/oh-my-cpa">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/WizisCool/oh-my-cpa/main/web/src/assets/brand/omc-wordmark-dark.svg">
      <img src="https://raw.githubusercontent.com/WizisCool/oh-my-cpa/main/web/src/assets/brand/omc-wordmark-light.svg" alt="Oh-My-CPA" height="22">
    </picture>
  </a>
</p>

<p align="center">
  为 <a href="https://github.com/router-for-me/CLIProxyAPI">CLIProxyAPI</a>
  提供原生的 Z.ai / BigModel OAuth 登录
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

OMC ZCode OAuth 为 CLIProxyAPI 增加 `zcode` provider。在面板的 OAuth 流程里用
Z.ai 或 BigModel.cn 账号登录后，CPA 即获得模型发现、Anthropic Messages 执行、
Coding Plan / 团队 / 礼包路由和标准化配额——无需独立进程，也无需本地 ZCode 客户端。

本项目是 [Oh-My-CPA](https://github.com/WizisCool/oh-my-cpa) 生态的一部分——
Oh-My-CPA 是 CLIProxyAPI 的自托管控制面板与用量观测台。

## 两个站点，一个 provider

> v0.1.5 已修复 Issue #1，移除公共动态 OAuth 资源。v0.1.3/v0.1.4 仍受影响，
> 对外开放前请先升级并阅读 [安全说明](docs/USAGE.md)。

ZCode 账号有两个登录入口。两者产生同一种凭证；可以每次登录时选择站点，也可以在
配置中固定。

| 站点 | `site` 取值 | 授权端点 |
| --- | --- | --- |
| Z.ai | `zai` | `https://chat.z.ai/api/oauth/authorize` |
| BigModel.cn | `bigmodel` | `https://bigmodel.cn/login?appId=zcode` |

- `site: zai`（默认）：OAuth 卡片直接跳转 Z.ai 的授权页。
- `site: bigmodel`：OAuth 卡片直接跳转 BigModel.cn 的授权页。
- 受认证请求的 `?site=` 参数可对单次登录覆盖配置；无效值直接拒绝，不会回退。
  缺失、为空或旧版 `auto` 的站点选择一律归为 Z.AI。

## 面板无关

所有面向用户的部分都由宿主渲染：OAuth 卡片、配置字段、静态资源和配额视图都通过
CPA 的插件 ABI 与管理 API 呈现。插件在 [Oh-My-CPA](https://github.com/WizisCool/oh-my-cpa)
中是一等公民，在 CPA 自带的 Management Center 或任何对接 CPA 管理 API 的面板中
表现一致。

OAuth 发起和轮询使用 CPA 原生受认证管理 API，公共插件资源仅提供静态 Logo 与可选
描述声明。`public_base_url` 为兼容旧配置保留并继续校验，但不再影响登录流程。

## 功能

- Z.ai 与 BigModel OAuth，通过原生受认证入口登录，默认站点 Z.AI。
- 个人 Coding Plan、团队席位与纯礼包凭证。
- 基于 ZCode 客户端目录的模型发现。
- 纯礼包凭证将普通厂商模型名路由到礼包服务。
- 流式与非流式 Anthropic Messages 请求。
- 标准化的订阅、5 小时、每周、MCP 与礼包桶配额。
- 按凭证的并发控制。
- ZCode 并发错误的有限重试，风控失效关闭（fail-closed）处理。

## 安装

将本仓库添加为插件商店源：

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

然后在插件商店安装 `omc-zcode-oauth`，并在面板中打开它的 OAuth 卡片。商店与
共享库名称为 `omc-zcode-oauth`；CPA 凭证 provider 标识保持为 `zcode`。登录、配置、
配额与故障排查见 [docs/USAGE.md](docs/USAGE.md)。

推送 `v*` 标签时由 GitHub Actions 创建发布。

## 配置

| 字段 | 类型 | 默认值 | 可选值 |
| --- | --- | --- | --- |
| `site` | 枚举 | `zai` | `zai`、`bigmodel` |
| `mode` | 枚举 | `own` | `own`、`team` |
| `max_concurrency` | 整数 | `3` | 每凭证 1–100 个并发请求 |
| `app_version` | 字符串 | `3.16.3` | ZCode 客户端版本 |
| `team_language` | 枚举 | `en` | `zh`、`en` |
| `public_base_url` | 字符串 | 空 | 兼容旧配置的 URL，仍校验但不用于原生 OAuth |

## 开发

插件实现位于 [`go/`](go/)。模块下载完成后，测试无需网络：

```sh
cd go
go mod download
GOPROXY=off GOSUMDB=off go test ./...
GOPROXY=off GOSUMDB=off go vet ./...
```

CI 从标签构建以下发布产物：

```text
omc-zcode-oauth_<version>_linux_amd64.zip
omc-zcode-oauth_<version>_linux_arm64.zip
omc-zcode-oauth_<version>_darwin_amd64.zip
omc-zcode-oauth_<version>_darwin_arm64.zip
omc-zcode-oauth_<version>_windows_amd64.zip
omc-zcode-oauth_<version>_windows_arm64.zip
checksums.txt
```

每个压缩包内含命名正确的 `omc-zcode-oauth.so`、`omc-zcode-oauth.dylib` 或
`omc-zcode-oauth.dll`。CI 构建全部六个目标；本地原生构建使用 `make build`，
`make package` 生成相同格式的压缩包。

提交规范、测试要求与生态开发习惯参见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 项目结构

```text
.github/workflows/   CI 测试、原生构建与标签发布
.github/scripts/     发布压缩包打包脚本
assets/logo.png      Z.AI 与插件商店 logo
assets/bigmodel.png  BigModel.cn 登录 logo
go/                  Go/cgo 插件实现与测试
docs/USAGE.md        部署安装与使用指南
registry.json        CLIProxyAPI 插件商店元数据
```

Go 实现独立于面板。ZCode 提示词兼容性数据取自官方 ZCode `v3.14.3` 源码；
参见 [NOTICE.md](NOTICE.md)。

## 许可证

[MIT](LICENSE)
