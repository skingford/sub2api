# Claude Code 2.1.292：剩余差异修复与调用约定

更新：2026-10-08。关联 `CC-20261008-006`、`CC-20261008-007`，接续 `CC-20261008-005` 的审查。
实现基于 release `ee2f9fea`，在 PR #4 中继续维护。历史审查结果保持原样。

后续 CC-20261008-010 补齐压缩头、显式 thinking 显示能力，以及 Sonnet / Opus 5.5 的已测配置；
托管恢复的压缩与 metadata 扩展同步修复，见 [扩展对齐修复](claude-alignment-fixes.md)。下文保留本轮原始范围与证据。

## 修复范围

| 审查项目 | 当前处理 |
|---|---|
| 普通 API 使用旧 SDK / runtime / 平台模板 | 默认版本更新为 2.1.292，SDK 0.128.0、runtime v26.3.0、Linux x64；转换配置与原生透传分别标记，匹配时采用实测 TLS / HTTP/1.1 配置 |
| 转换后正文被再次识别成原生输入 | 转换前固定来源判断；普通转换明确使用自己的 profile，生成 billing 不会改变来源分类 |
| OAuth count_tokens 多补 timeout | 原生请求不再填充可选缺省头；普通转换的计数头也省略 timeout 和 prompt ID，request-class 为 auxiliary |
| zlib 封装的 deflate 解压失败 | 识别 zlib / raw deflate；保留损坏流错误、关闭与连接复用行为 |
| metadata 账号 A 与凭证账号 B 不一致 | 两个 UUID 都明确且不同时，本地 400 拒绝；未知 / 空 UUID 不猜测；不修改上游 thinking 签名 |
| 不同对话因首句相同复用 session ID | 新会话使用随机 UUID；调用方显式续用会话；数据库原子保存永久账号绑定，阻止中途换号 |
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
- `claude_native_passthrough=false` 仍是原生输入的保留策略回退开关；普通 API 的转换 profile 有独立来源标记。会话 ID 的硬约束优先于此开关：旧身份改写或会话伪装若改变 ID，请求在本地拒绝。
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
- 2026-10-09 补证后，单独指定 temperature 会保留模型默认 thinking，与原生后置温度覆盖一致；
  top_p / top_k 与强制工具选择仍按原有规则处理。显式 disabled 清理额外键，按直接控制补齐
  支持模型的温度 / effort 缺省值。见 [参数与 gzip 对齐](claude-parameter-wire-alignment.md)。
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

生产网关使用 PostgreSQL `claude_session_ownership` 保存不可改绑的 session → account ID。
入站 UUID 与出站 UUID 保持相同，不按账号另造一个 UUID，也不把多个会话映射到固定会话 ID：

1. 首次发送前通过唯一约束原子登记。多个实例同时处理同一新 session 时，只允许获胜账号发送；选到其他账号的请求返回 503，不向上游发出。
2. 原生 CLI、OAuth 转换、API Key 透传、Messages、count_tokens、Chat Completions 和 Responses 共用同一 session UUID 的归属。
3. 续聊调度只保留绑定账号作为候选；优先级、模型路由、冷却、限流、排除列表和故障切换都不能把会话迁移到其他账号。原账号不支持新模型或不可用时返回错误，恢复后可续聊。
4. 数据库记录没有 TTL，删除账号也保留归属记录。Redis 过期、清空、进程重启不会使会话重新分配。归属记录不等于活跃槽：并发与活跃会话额度仍按原规则释放，绑定竞争失败也释放已占的槽位。
5. 改 API Key 或分组不会创建新的会话归属。调用方仍需通过原有鉴权、分组和可用性检查；知道 UUID 不会获得账号访问权限。
6. 数据库未配置、读取失败或写入失败时拒绝发送。`X-Sub2API-Session-Id` 指向不存在的记录时返回 400；客户端首次自行生成 UUID 应使用 `X-Claude-Code-Session-Id`。
7. 原始 metadata、两种会话头和最终出站头的 UUID 必须一致，配置重写也不能悄悄改变会话身份。只有网关续聊头时也传递同一个上游会话头；协议转换前先保存 metadata 中的原始 ID。

数据库迁移 `242_claude_session_ownership.sql` 随应用迁移执行。此表是会话归属记录，需要与业务库一起备份和恢复；不要清表或按时间清理。数据库本身丢失或回滚到较早快照时，无法凭空恢复丢失的历史归属。

升级前只有 Redis 的历史会话无法完整追溯，尤其是已经过期的记录。部署本次改动后应新建会话，再建立持久归属。滚动部署期间旧实例仍可能换号，需要统一切换到新版本后使用这项保证。

调用方必须在各轮、计数和重试中携带同一个 UUID。OAuth 转换在没有 ID 时创建新会话并返回 ID；没有任何会话标识的普通 API Key 请求不能被可靠识别为同一对话。不要用相同首句、IP 或 UA 代替 session ID。

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
  提前提供 ID、账号 UUID 冲突、会话账号绑定、未知续聊 ID、缓存丢失、四种 API 入口拒绝和原生计数头。
- 原生模型补抓及审查阶段的计数样本都来自未修改 CLI；PCAP 解密原始字节和接收端一致。
- 同一批算法与原有原生 / 旧兼容回归继续验证；完整后端 unit 57 个包、integration 51 个包通过，lint 0 issues。
- CC-20261008-007 的后端完整 unit 57 个包、integration 51 个包通过，完整 lint 0 issues。随后补充并发竞争失败的槽位释放，会话 / 身份设置 / 请求契约专项再次通过；最终受影响包 lint 0 issues。
- 数据库使用 32 路并发和独立仓储实例验证唯一归属、旧记录不失效、重新创建仓储后仍不改绑；最终 SQL 版本复验通过。
- 首轮集成检查曾遇到本地 Redis 测试容器启动失败，完整重跑通过；没有将跳过测试视为通过。
- 全部 CLI / 传输实验使用假凭证与本地服务，没有部署或访问真实官方模型服务。
- TLS 会话恢复、HTTP/2、ARM64、未覆盖模型 / 工具 / 交互状态、真实订阅与风控仍是未验证范围。

### 最终发送对照

三个模型的生产构建器输出经原生 Bun 运行时重算 cch 后，正文逐字节一致。
自动 profile 选择下，直连、HTTP CONNECT、HTTPS CONNECT、SOCKS5 的正文、头值和顺序、
ClientHello 非随机部分均匹配同一组原生运行时结果。PCAP 与接收正文一致且丢包为 0。
这里验证的是相同构造输入的发送行为；运行时探针修改了 JS 入口，不能把它当作整个 CLI 产品流程完全相同。

[最终传输摘要](../backend/internal/service/testdata/claude_code_2_1_292/gap-fix-wire-verification.json)；
[来源与原始样本校验](../backend/internal/service/testdata/claude_code_2_1_292/gap-fix-provenance.json)。

[会话约束验证摘要与源码哈希](claude-session-validation.json)。完整日志保存在本机 `claude-capture/session-ownership-20261008/`。
