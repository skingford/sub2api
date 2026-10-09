# Claude Code 遥测与个人自用中转

整理日期：2026-10-09（Asia/Shanghai）。实验及官方说明核对日期：2026-10-08。
适用版本：Claude Code 2.1.292 / SDK 0.128.0 / Linux x64。
记录：CC-20261009-001；[结构化实验摘要](claude-telemetry-validation.json)。

本文讨论**维护者自己的订阅接入自己的 Sub2API，转换为 API 方便个人管理**，
不把该场景描述成代他人转发订阅凭据。本文记录客户端隐私设置、请求行为和可控风险，
不以协议兼容或关闭遥测推断服务端授权、订阅资格或封禁概率。

## 结论

- 仅修改 `ANTHROPIC_BASE_URL`，不会把 CLI 的所有请求都改到中转地址。
  本次捕获到模型请求去往模拟中转地址，而事件遥测仍使用官方域名。
- 关闭遥测应在**运行 Claude CLI 的进程环境**中配置。只给 Sub2API 服务端设置这些变量，
  不会替客户端 CLI 关闭遥测。
- `DISABLE_TELEMETRY` 与 `DISABLE_ERROR_REPORTING` 控制可选上报；
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` 的范围更广，但仍不是禁止全部联网。
- 关闭可选遥测后，模型请求中的提示词、工具结果和会话标识仍会发送；认证、组织策略、
  WebFetch 及扩展功能还可能产生其他请求。
- 六组实验均为未修改官方 CLI、假凭据、Docker 断网环境和本地模拟响应。
  没有测试真实账号，也没有证据能够量化关闭遥测对封号概率的影响。

## 用户如何主动关闭

### 方案一：关闭可选指标、错误报告和自定义 OpenTelemetry

在启动 CLI 的终端中执行：

```bash
export DISABLE_TELEMETRY=1
export DISABLE_ERROR_REPORTING=1
export CLAUDE_CODE_ENABLE_TELEMETRY=0
claude
```

`CLAUDE_CODE_ENABLE_TELEMETRY` 控制用户或管理员配置的 OpenTelemetry（OTel），
与第一方遥测的禁用开关分别处理。不要把它设成 `0` 就理解为已关闭所有官方上报。
本次 `relay-no-telemetry` 实验显式设置前两个变量，第三个未设置，也没有配置 OTel exporter；
上述配置额外明确关闭可能已启用的自定义 OTel。

### 方案二：尽量减少非必要联网

```bash
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
export CLAUDE_CODE_ENABLE_TELEMETRY=0
export CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL=1
claude
```

总开关覆盖第一方可选遥测和更多非必要功能。官方插件市场自动安装有独立开关，
不属于总开关覆盖范围。以上是个人自用、希望减少额外出站时可选的配置；
使用 Remote Control 或其他联网功能时，需要先接受下文所列功能影响。

两档配置选择一档即可。关闭开关不会撤回之前已经上传的数据，
也不会自动清理本地会话、debug 日志或历史反馈包。

### 持久化与启动环境

`export` 只影响当前 shell 及其后续启动的子进程，不会修改已运行的 CLI。
需要重新启动 CLI；IDE 内启动的 CLI、后台服务和容器应在各自启动环境配置。
Docker Compose 的 `environment` 应加在**运行 CLI 的服务**中，而不是仅加在 Sub2API 服务中。

也可将选定方案合并到 CLI 用户级 `~/.claude/settings.json` 的 `env` 对象；
设置了 `CLAUDE_CONFIG_DIR` 时使用该配置目录的 `settings.json`。不要覆盖原文件里的其他设置。
例如方案二：

```json
{
  "env": {
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "CLAUDE_CODE_ENABLE_TELEMETRY": "0",
    "CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL": "1"
  }
}
```

设置文件写法依据官方说明；本次动态实验直接注入进程环境，并通过 `--setting-sources ''`
隔离用户设置，未单独测试设置文件优先级。存在组织管理策略时，还应检查实际生效配置。

**恢复时不要把所有禁用变量改成字符串 `"0"`。** 2.1.292 的非必要流量和指标遥测门控
直接判断对应环境变量是否为非空字符串。恢复这些功能应删除 shell、设置文件和启动器中
相应变量，再启动新进程：

```bash
unset CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC
unset DISABLE_TELEMETRY
unset DISABLE_ERROR_REPORTING
```

`CLAUDE_CODE_ENABLE_TELEMETRY=0` 使用另一套布尔解析，不能将上述规则推广到全部变量。

## 使用中转时，实际发出了什么

下面是 **PCAP 解密后的 HTTP 请求**，按请求目的地归类。实验中官方域名和中转域名
都映射到容器回环地址，没有访问真实官方服务；“官方域名”表示 CLI 选择的目的域名。
这也不表示它会绕过用户配置的系统 HTTP/HTTPS 代理，实验没有覆盖这种代理配置。

| 场景 | 模拟中转地址 | 官方域名 | 事件上报批次数 |
|---|---|---|---:|
| `api-default`：API Key，官方 base，可选遥测开关未设置 | 无 | 探活、settings、policy_limits、功能开关、bootstrap、penguin 配置、模型、metrics_enabled、metrics、事件 | 2 |
| `relay-default`：API Key，中转 base，可选遥测开关未设置 | 探活、模型 | 功能开关、bootstrap、penguin 配置、事件 | 1 |
| `relay-no-telemetry`：中转 base，关闭指标和错误报告 | 探活、模型 | bootstrap、penguin 配置 | 0 |
| `relay-essential`：中转 base，仅必要流量，OTel=0 | 模型 | 本次未观察到 | 0 |
| `oauth-default`：假 OAuth 环境变量，官方 base | 无 | 探活、settings、policy_limits、功能开关、bootstrap、模型、事件 | 1 |
| `oauth-essential`：假 OAuth，仅必要流量，OTel=0 | 无 | settings、policy_limits、模型 | 0 |

具体方法、路径和次数见[实验摘要](claude-telemetry-validation.json)。主要路径包括：

- 模型：`POST /v1/messages?beta=true`。
- 探活：`HEAD /api/hello`，本次跟随模型 base URL。
- 第一方事件：`POST /api/event_logging/v2/batch`。
- 功能开关：`POST /api/eval/...`。
- 启动配置：`GET /api/claude_cli/bootstrap`、`GET /api/claude_code_penguin_mode`。
- 组织设置与策略：`GET /api/claude_code/settings`、`GET /api/claude_code/policy_limits`。

`relay-default` 捕获的一个事件批次包含 103 个事件。抽查事件的数据字段包含
`event_name`、`client_timestamp`、`model`、`session_id`、`env`、`process`、
`additional_metadata`、`event_id` 和 `device_id`；该请求没有 Authorization 或 x-api-key。
没有认证头不等于事件匿名，也不代表本实验发送了真实账号凭据。
不同事件的字段和数量不能由这一次短会话推广。

静态提取还定位了独立的 Datadog 指标及错误上报地址：
`http-intake.logs.us5.datadoghq.com/api/v2/logs` 和
`browser-intake-us5-datadoghq.com/api/v2/logs`。
本次六组短场景未捕获到这两类 HTTP 请求；只能确认代码通路存在，不能宣称验证了真实账号下的触发和停用条件。

## 关闭后仍可能联网，以及功能影响

| 类别 | 关闭后的边界 | 证据类型 |
|---|---|---|
| 模型调用 | 仍发送提示词和所需上下文。本次六组请求均保留 `device_id/account_uuid/session_id` 字段；假凭据下 `account_uuid` 为空 | 本次捕获 |
| 认证与组织策略 | “仅必要流量”不等于阻断认证和策略读取；假 OAuth 场景仍请求 settings、policy_limits。真实登录刷新未测试 | 捕获与范围限制 |
| 功能开关、Remote Control | 关闭指标或非必要流量会影响功能开关评估，可能使 Remote Control 不可用；不能写成所有版本必然失效 | 官方说明；本次未运行远控 |
| 错误诊断 | 停止错误上报会减少提供给厂商的诊断信息；本地 debug、日志保存是另一回事 | 配置含义与官方说明 |
| 用户反馈 | 严格方案会限制相关反馈功能；只关闭指标不能据此认为主动提交的反馈也被关闭，反馈有 `DISABLE_FEEDBACK_COMMAND` 独立开关 | 官方说明与静态代码 |
| WebFetch | 域名安全检查不受总开关控制，使用时仍可能把目标 hostname 发给官方；本次没有调用 WebFetch | 官方说明 |
| MCP、插件、Hooks | 各自可能联网，CLI 遥测开关不是它们的通用出站防火墙 | 实验未覆盖这些功能 |
| OTel 与内容日志 | 自定义 OTel 需单独检查；还应检查已有内容日志配置及本地文件。不能用第一方开关推断所有日志都停止 | 静态代码；未动态配置 exporter |
| 自动更新与市场安装 | 本次所有场景都预先禁用两项，不能用本次抓包比较它们的默认行为。市场自动安装需独立关闭；禁用自动更新时需自行维护版本 | 实验控制条件与官方说明 |

官方数据使用页允许主动关闭可选指标和错误报告，并说明总开关对插件市场自动安装和
WebFetch 安全检查的例外。WebFetch 的预检具有安全用途，不应把关闭它视为无代价的隐私设置。
参见 [Telemetry services](https://code.claude.com/docs/en/data-usage#telemetry-services)
及 [WebFetch domain safety check](https://code.claude.com/docs/en/data-usage#webfetch-domain-safety-check)
（核对日期 2026-10-08）。

## Sub2API 的职责与个人自用风险

当前 [common.go](../backend/internal/server/routes/common.go) 只为
`POST /api/event_logging/batch` 返回空 200。它仅处理**实际到达 Sub2API 的该路径**，
既不覆盖本次 CLI 的 `/api/event_logging/v2/batch`，也无法拦截客户端另行请求官方域名。
因此不能以这段路由证明“Sub2API 已屏蔽 CLI 的全部遥测”。

若使用其他 API 客户端调用 Sub2API、没有运行 Claude CLI，就不会凭空产生这套 CLI 遥测。
需要分别检查该 API 客户端和 Sub2API 的日志、账号资料读取、用量查询及认证刷新行为。
给 Sub2API 容器设置 CLI 环境变量，不能推断 Go 服务本身的相关任务会停止。

对个人自用，可控的工作包括：

1. 在实际运行 CLI 的环境中选择隐私设置，升级版本后复核，而不是依赖旧端点拦截规则。
2. 保护订阅凭据、网关 API Key 和日志；测试继续使用假凭据和独立配置目录。
3. 按[会话与重试约定](claude-code-gap-fixes.md)保持身份一致，避免混用会话、重复发送和叠加重试。
   需要[托管恢复](claude-managed-recovery.md)时按其明确边界使用，不将其解释成恢复官方权限的方法。
4. 尊重上游限流、权限拒绝和撤销结果。正常退避与错误分类有助于减少无效请求；
   不使用伪造遥测或随机更换设备标识作为“防封”方案。

关闭可选遥测能够减少额外数据外发；模型请求仍向上游提供认证、用量、内容和会话信息。
源码提取和本地模拟无法恢复服务端风控规则，不能得出“已经不会封号”或某个封号百分比。

## 实验证据与局限

- 基线：主题分支 HEAD `93275632a6a66dfc2648e9504a7d16bcf32da5d7`；
  release `b4430850844263e3fd3d2d180f514099bffad04e`；
  上游 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。
  遥测实验直接运行 CLI 对模拟器，没有运行 Sub2API；工作区其他未提交修改不是本次验收对象。
- 官方 Linux x64 2.1.292 二进制 SHA-256：
  `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。
  静态依据来自该二进制的内嵌 JavaScript；CLI 执行时未修改二进制。
- 六个独立 HOME / 配置目录，API Key 或 OAuth 环境变量均为合成值，没有挂载宿主机登录目录。
  Docker `--network none`，域名映射回环，外部测试地址返回 `ENETUNREACH`。
- 使用单轮 `-p` / JSON 输出、Sonnet 4.6、default 权限模式、空 tools 与 MCP 配置、
  禁止会话持久化，并关闭自动更新和官方市场自动安装。各进程退出 0，各产生一条模型请求。
  “default”只表示该组未设置可选遥测开关，不是原始安装的全部默认行为。
- 模拟器为功能开关返回空 features、为事件返回合成成功、为 metrics_enabled 返回 true；
  settings / policy 等未实现端点返回 404。没有真实账号 profile、服务端功能开关或生产错误条件。
- 捕获器最初仅记录 GET/POST，遗漏由默认处理器响应的 HEAD 探活。
  后续 PCAP 核对发现并保留了该差异；本文及结构化摘要以 PCAP 的完整端点计数为准，
  没有覆盖原始摘要。六组内核丢包均为 0，已记录请求的端点及次数与 PCAP 一致。
  本轮没有进行全部请求正文逐字节校验。
- 抓包只监控回环 TCP 443，不能据此宣称覆盖所有失败的 DNS、其他端口连接尝试或全部功能。
  未验证长会话、真实订阅、全部错误上报条件、IDE、远控、WebFetch、OTel exporter、
  插件或真实服务端接受情况。

本机原始证据目录：`/Users/kingford/claude-capture/telemetry-audit-20261008/`。
该目录保留执行器、运行参数、合成请求、PCAP、TLS 解密材料、提取模块及官方说明快照。
仓库只归档[去除凭据和原始正文的摘要](claude-telemetry-validation.json)，记录相应来源和文件哈希；
不提交原始日志、二进制、完整提取源码或 TLS 私钥。

本次 2026-10-09 归档核对已有文件、JSON、链接和差异，没有重跑 CLI 或全量代码测试，
没有改变用户设置、生产实现、服务部署或提交状态。
