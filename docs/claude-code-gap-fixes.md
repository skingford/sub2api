# Claude Code 2.1.292：剩余差异修复与调用约定

更新：2026-10-08。关联 `CC-20261008-006`，接续 `CC-20261008-005` 的审查。
实现基于 release `ee2f9fea`，在 PR #4 中继续维护。历史审查结果保持原样。

## 修复范围

| 审查项目 | 当前处理 |
|---|---|
| 普通 API 使用旧 SDK / runtime / 平台模板 | 默认版本更新为 2.1.292，SDK 0.128.0、runtime v26.3.0、Linux x64；转换配置与原生透传分别标记，匹配时采用实测 TLS / HTTP/1.1 配置 |
| 转换后正文被再次识别成原生输入 | 转换前固定来源判断；普通转换明确使用自己的 profile，生成 billing 不会改变来源分类 |
| OAuth count_tokens 多补 timeout | 原生请求不再填充可选缺省头；普通转换的计数头也省略 timeout 和 prompt ID，request-class 为 auxiliary |
| zlib 封装的 deflate 解压失败 | 识别 zlib / raw deflate；保留损坏流错误、关闭与连接复用行为 |
| metadata 账号 A 与凭证账号 B 不一致 | 两个 UUID 都明确且不同时，本地 400 拒绝；未知 / 空 UUID 不猜测；不修改上游 thinking 签名 |
| 不同对话因首句相同复用 session ID | 新会话使用随机 UUID；调用方显式续用会话；共享缓存保存账号绑定，避免中途静默换号 |
| 网关叠加重试和账号切换 | 原生及新转换路径每次调用只进行一次上游发送；HTTP 拒绝、传输错误和提前流错误交给调用方处理，保留拒绝状态及重试相关头 |
| 缺省压缩声明少 zstd | 未传 Accept-Encoding 时补 gzip, deflate, br, zstd；已有值保留 |

“修复”覆盖已确认的客户端差异及代理职责选择。它不表示任意 API 输入已经包含 CLI 的本地项目、
工具、记忆、历史诊断和交互状态，也不代表真实服务端已经接受或不会封号。

## 1. 完整版本配置

普通 API → OAuth 的转换使用已验证的 **2.1.292 / Linux x64** 配置。
该配置的 SDK 头、runtime 头、平台头、生成的 billing、cch 与自动传输选择一起生效。

- 在转换前记录请求来源，不再由生成后的 billing / metadata 反推客户端种类。
- 一次请求使用固定版本，后台版本设置变化不影响已经开始的请求。
- 若管理员选择了没有完整实测配置的版本，转换在本地返回 400，不继续发送新 UA 配旧 SDK 的请求。
- 历史版本设置仍可读取；“配置值格式合法”与“具有完整可用转换配置”是两项检查。
- 原生客户端的未知版本继续按原有保留策略处理，不套用 2.1.292 的未验证算法 / 传输特征。
- `claude_native_passthrough=false` 仍是原生输入的保留策略回退开关；普通 API 的转换 profile 有独立来源标记。
- `claude_native_transport=false`、自定义目标和显式绑定 TLS 模板仍按既有配置优先级处理。

部署升级时检查当前 Claude 客户端版本设置。普通转换目前应选择 **2.1.292**，不能依靠自动更新
单个版本号获得新版请求支持。新增版本需要补齐二进制、默认值、算法与传输证据。

## 2. 按模型填默认参数

本轮用同一份未修改的官方 Linux x64 2.1.292 二进制，在断网容器中补抓了三个模型的假 OAuth 请求：

| 模型 | 默认 max_tokens | 默认 thinking | 默认 effort |
|---|---:|---|---|
| claude-sonnet-4-6 | 32000 | adaptive / display=omitted | high |
| claude-opus-4-6 | 64000 | adaptive / display=omitted | high |
| claude-haiku-4-5-20251001 | 32000 | enabled / budget_tokens=31999 / display=omitted | 不添加 |

Haiku 的 beta 顺序与 Sonnet / Opus 分开处理，不再无条件添加 effort beta。
消息级 output_config 等条件能力只在相应字段或显式请求存在时添加。

- 显式 max_tokens、temperature、top_p、top_k、thinking、effort 和强制 tool_choice 保留调用方语义。
- 指定采样参数或强制工具选择时，不自动加入可能冲突的 thinking。
- Haiku 的自动 thinking budget 不超过 max_tokens；额度不足以形成合法 budget 时不强加。
- count_tokens 不使用生成参数默认值。
- Chat Completions / Responses 适配器的内部兜底值不等于调用方显式 max_tokens；调用方未指定时，
  由上述实测模型配置补齐。
- 其他模型继续使用已有兼容规则，尚未宣称其参数默认值全面匹配原生。
- diagnostics.previous_message_id 等历史状态需要调用方提供，不生成猜测值。

来源：[模型样本](../backend/internal/service/testdata/claude_code_2_1_292/model-defaults.json)。

## 3. 会话与提示 ID

### 服务端创建会话

首次普通 API 请求可以不传会话头。网关生成随机 UUID，并通过响应头返回：

```http
X-Sub2API-Session-Id: <UUID>
```

之后同一会话的请求带回这个值。新会话不带旧值。相同首句不会再合并两个独立会话。
该响应头已加入 CORS expose headers，浏览器调用方可以读取。

### 调用方提前创建 ID

如果调用方需要在第一次失败及重试之前就知道会话 ID，可以先生成随机 UUID，发送：

```http
X-Claude-Code-Session-Id: <CONVERSATION_UUID>
x-claude-code-prompt-id: <PROMPT_UUID>
```

同一会话保持 conversation UUID；同一次逻辑提示的重试保持 prompt UUID，新提示换新 UUID。
没有提供 prompt ID 时，网关为本次调用生成。计数请求不发送 prompt ID。
UUID 格式错误或相互冲突的标识在本地拒绝。

### 账号绑定

生产网关使用已有共享缓存记录会话归属：

1. 按 API Key ID 隔离普通转换的路由键，原生计数请求沿用同一 CLI 会话的消息路由键。
2. 为新会话保存所选账号；稳定身份记录与可被调度器替换的粘性路由记录使用不同键。
3. 后续请求若选到其他账号，返回 503，要求等待原账号或开启新会话，不发送混合身份请求。
4. 网关返回的 X-Sub2API-Session-Id 若已过期或不存在，返回 400，要求新建会话。
5. 使用现有一小时粘性会话 TTL，并在使用时续期。缓存读取 / 写入失败返回 503。
6. 独立组件没有配置 gateway cache 时只能保留 UUID，不能提供跨请求的账号绑定；生产部署需保留共享缓存。

原生 metadata 中明确的 account_uuid 若与上游账号不同，直接返回 400。
空 UUID 不等于已确认匹配；服务端如何处理 token-only 会话，仍需另行验证。
普通 API 的非原生 user_id 标签可转换为网关生成的原生身份结构；它不能冒充上游账号身份。

## 4. 重试由调用方管理

原生 CLI 已有自己的重试控制器。网关再重试或换账号，会形成另一套请求序列。
本轮选择透明的单次发送约定：

- HTTP 错误返回对应状态；保留 request-id、Retry-After、retry-after-ms、x-should-retry。
- 错误消息继续经过网关脱敏，保留现有账号冷却 / 错误记录。
- 不因 403、503、529 或提前流错误自动重发、切换账号；传输错误也返回给调用方。
- 已经产生的用量仍正常记录，不因错误返回丢弃部分用量。
- 普通 API 调用方需要配置自己的重试；这不是将整个 CLI 的重试调度器移植到网关。
- 新一次发送会生成新的 x-client-request-id；原生传入的请求关联字段继续保留。

## 5. 解压与完整头集合

生产响应解码已覆盖 gzip、brotli、zstd、raw deflate 和带 zlib 封装的 deflate。
解码失败作为读取错误返回，不能把损坏流当成成功正文。测试包含 JSON、错误体、SSE、损坏流和连接复用。

原生 OAuth count_tokens 新增三份独立捕获样本，回归检查双向完整应用头集合：既不能丢头，
也不能多加可选头。传输层没有显式 Accept-Encoding 时使用实测的四项声明。

## 6. 验证与留痕

- 新增契约回归覆盖完整 profile、来源分类、配置中途变化、模型默认值、显式参数、随机会话、
  提前提供 ID、账号 UUID 冲突、会话账号绑定、过期状态、缓存键隔离、四种 API 入口拒绝和原生计数头。
- 原生模型补抓及审查阶段的计数样本都来自未修改 CLI；PCAP 解密原始字节和接收端一致。
- 同一批算法与原有原生 / 旧兼容回归继续验证；完整后端 unit 57 个包、integration 51 个包通过，lint 0 issues。
- 全部 CLI / 传输实验使用假凭证与本地服务，没有部署或访问真实官方模型服务。
- TLS 会话恢复、HTTP/2、ARM64、未覆盖模型 / 工具 / 交互状态、真实订阅与风控仍是未验证范围。

### 最终发送对照

三个模型的生产构建器输出经原生 Bun 运行时重算 cch 后，正文逐字节一致。
自动 profile 选择下，直连、HTTP CONNECT、HTTPS CONNECT、SOCKS5 的正文、头值和顺序、
ClientHello 非随机部分均匹配同一组原生运行时结果。PCAP 与接收正文一致且丢包为 0。
这里验证的是相同构造输入的发送行为；运行时探针修改了 JS 入口，不能把它当作整个 CLI 产品流程完全相同。

[最终传输摘要](../backend/internal/service/testdata/claude_code_2_1_292/gap-fix-wire-verification.json)；
[来源与原始样本校验](../backend/internal/service/testdata/claude_code_2_1_292/gap-fix-provenance.json)。
