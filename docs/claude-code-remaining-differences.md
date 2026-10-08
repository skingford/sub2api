# Claude Code 2.1.292：release 剩余差异复核

更新：2026-10-08。审查基线 `ee2f9fea3cacee7380dc280fb549efa7db4b0cc9`。
本轮执行本地复现并整理差异，未修改生产实现。

## 结论

原生 2.1.292 请求的正文保留与 cch 计算已经有验证依据，但完整请求和运行行为还没有全面一致。
最大的缺口在普通 API 转为 OAuth 的分支；原生透传中也发现了额外请求头和响应解压问题。

本轮扩展为完整应用头集合比较，覆盖 27 个原生转发组合，正文全部一致。
使用未修改官方 Linux x64 CLI 补跑假 OAuth 的 `/context` 和 403 场景；再用保留原生代码的
Bun 运行时探针对照响应解压。全部 CLI 运行都在 Docker `--network none` 中，只连接回环服务。

机器可读结果见 [审查摘要](claude-2292-gap-audit.json)。优先级表示对客户端一致性与功能的影响，
没有将差异解释为已证实的服务端封禁信号。

## 1. 普通 API 转换仍使用旧模板，并会形成混合配置

### 实际出站结果

给同一条普通 Messages 请求选择 OAuth 账号，并分别配置 CLI 版本为 2.1.258、2.1.292：

| 项目 | 普通 API 转换结果 | 本次原生 2.1.292 参照 |
|---|---|---|
| CLI 版本 | 可改为 2.1.292 | 2.1.292 |
| SDK 头 | **0.94.0** | **0.128.0** |
| 运行时头 | **v24.3.0** | **v26.3.0** |
| 平台模板 | Linux / arm64 | 被测 Linux / x64 |
| 自动原生 TLS profile | **未选择** | `claude_2_1_292_x64` |
| 默认 max_tokens | 128000 | 此 Sonnet 4.6 场景为 32000 |
| 未传 thinking 时 | 补 temperature=1，未生成 thinking / effort | 此 CLI 场景为 adaptive / omitted，effort=high |
| beta | 旧固定组合 | 依版本、认证、模型与运行状态生成 |
| session / prompt / request-class 头 | 本轮普通转换未生成完整集合 | 参照样本含对应会话和请求分类字段 |

模型参数是这个具体场景的差异，不能把 32000 或 effort=high 当作所有模型的统一默认值。
调用方显式给出的参数也不应为了外观一致而被静默覆盖。

### cch 的分类边界需要澄清

实测发现：普通请求经旧逻辑注入 billing 和 metadata 后，`preserveNativeClaudeRequest`
的正文兜底会把**转换后的正文**识别为原生形态。CLI 版本设为 2.1.292 时，最终也会生成 cch，
而 SDK、运行时、beta 和传输仍使用旧配置。

所以“看到 cch 就说明经过完整原生路径”不成立。此前报告中“只对已有原生分类启用”的描述需要
结合分类发生的时机阅读：它没有严格证明输入来自原生 CLI。

**优先处理：** 将完整版本配置作为一个整体核对，保留转换前的请求来源判断，并明确普通 API
选择哪些 CLI 行为。只更新版本号、只补 cch 或直接把所有请求改成 x64 都不足以完成这项工作。

依据：`internal/pkg/claude/constants.go`、`gateway_forward.go`、`gateway_claude_oauth_body.go`、
`gateway_claude_native.go`；摘要中的 `generic_profiles`。

## 2. 原生 OAuth count_tokens 被多加一个头

未修改官方 CLI 在本地假 OAuth `/context` 场景发出的三条 count_tokens 请求，都没有
`X-Stainless-Timeout`。相同头与正文进入生产请求构建链路后，网关补入：

```http
X-Stainless-Timeout: 600
```

三条正文和原有应用头均保留，自动原生 TLS 配置也被选择，但完整头集合已经不同。
根因是 `applyClaudeOAuthHeaderDefaults` 对原生缺省项仍填入旧默认头。

旧回归只遍历捕获样本中的头，验证“已有头没丢”，未验证出站是否多出其他头。
此次对双向头集合比较才暴露该问题。

**优先修复：** 原生请求保留有意省略的可选头，并给 messages / count_tokens、API Key / OAuth
分别增加完整头集合断言。认证路线切换所需头部变化要单独标明。

依据：`gateway_upstream_request.go:465`、摘要 `native_oauth_count_header_diffs`。

## 3. deflate 响应解压存在功能差异

向两种运行时提供同一个 JSON 响应：`{"input_tokens":128}`。

| 响应编码 | 原生 Bun 1.4.3 探针 | Sub2API 生产响应处理 |
|---|---|---|
| gzip | 正确解码 | 正确解码 |
| raw deflate | 正确解码 | 正确解码 |
| 带 zlib 封装的 deflate | **正确解码** | **读取失败：`flate: corrupt input before offset 5`** |

Sub2API 的 `Content-Encoding: deflate` 分支直接使用 `flate.NewReader`，没有处理这里的 zlib
封装。这是实际读取错误，会影响采用该格式的响应。当前官方生产服务是否返回这种格式，本轮未验证。

**优先修复：** 增加 zlib / raw deflate 的兼容处理，并验证 JSON、SSE、错误体、关闭行为和连接复用。
原生端证据来自修改 JS 入口的运行时探针，原生 HTTP / 解压机器码保持不变。

依据：`internal/repository/http_upstream.go:1567`；摘要 `response_codecs`、`native_response_codecs`。

## 4. 身份字段与选中账号可能不同

构造一个 metadata.account_uuid 为 A 的原生请求，转发时选择 UUID 为 B 的 OAuth 账号：

- 出站 Authorization 确实使用选中账号 B 的凭证；
- metadata.account_uuid 仍为 A，因为原生路径保留客户端身份字段。

两者不再来自同一个账号上下文。这不是 cch 数学算法错误，也不是已证实的服务端拒绝原因。
之前“保持正文”验证不能回答跨账号转发的身份语义是否正确。

**待明确策略：** 会话与上游账号的绑定、允许的账号切换，以及不匹配时是拒绝、重新建立会话还是
按明确的转换策略处理。需要授权的真实账号观察才能确认服务端语义。

依据：摘要 `metadata_case`；`gateway_claude_native.go` 原生 metadata 保留路径。

## 5. 会话 ID 的生成方式不同

普通转换使用 SHA-256 对“账号 ID + 客户端区分值 + 首条用户文本”求值，再整理成 UUID 形状。
相同账号 / 客户端 / 首句开启两个独立对话，会得到相同 session_id。

原生对照：在两个独立配置目录运行同样的基础提示，发送的 messages 完全相同，但 session_id 不同。
原生多轮场景则在会话内保持 session_id。

**待明确策略：** 使用调用方显式会话标识，或维护会话生命周期；不能只靠首句文本推断新旧会话，
也不能每轮都生成新 UUID。

依据：`gateway_claude_oauth_body.go:489`、`:513`；摘要 `session_reference`、`session_case`。

## 6. 重试与请求 ID 生命周期不同

本轮模拟相同的 403 permission_error，准备在下一次请求返回成功：

- 未修改 CLI / 假 OAuth：只发出 **1 次**模型请求，以错误退出，没有继续重试。
- 网关 OAuth 转发：发出 **2 次**请求并取得模拟成功；两次 x-client-request-id 相同，
  SDK Retry-Count 均为 0，没有新增 dispatch-id。

网关源码的该分支最多尝试 5 次、300 ms 指数退避至 3 s、总预算 10 s；它还存在账号切换逻辑。
原生 503 抓包中则观察到新的 x-client-request-id 和 `anthropic-dispatch-id: v2p`。
不能把“能透传 dispatch-id”写成“网关内部的重试算法已经复制了 CLI”。

**待明确策略：** 原生请求是否把拒绝直接返回给客户端，以及网关承担重试时如何区分认证失败、
瞬时故障、一次逻辑请求与每次发送尝试。额外请求和时间行为已确认，封号影响未验证。

依据：`gateway_forward.go:23`、`:36`；摘要 `native_permission_error`、`gateway_permission_error`。
网关复现使用非流式请求；被测重试判定由账号类型和状态码决定，与 stream 字段无关。

## 7. Accept-Encoding 的缺省补齐仍不同

真实 2.1.292 messages 和 count_tokens 样本均观测到：

```http
Accept-Encoding: gzip, deflate, br, zstd
```

原生 HTTP/1.1 适配器在调用方没有该头时只补 `gzip, deflate, br`。
如果来访 CLI 已携带 zstd，白名单会保留，因此这不是所有原生请求都会出现的差异。

此前四条传输路径实验复制了原始 Accept-Encoding，因此“那个重放场景的头值一致”仍成立；
该结果没有覆盖缺省补齐分支。

**后续修复：** 按已测版本核对默认压缩能力，并与实际响应解码能力共同验证。
依据：`internal/repository/claude_native_transport.go`；摘要 `transport-default-accept-encoding`。

## 已验证与未验证的边界

### 本轮未推翻的结论

- 27 个原生转发组合的正文逐字节保留；原生 cch / UTF-16 算法既有向量仍是有效证据。
- 原有四种连接路径的特定重放样本，TLS 非随机字段及 HTTP 头序对照结论继续有效。
- thinking signature / redacted data 继续以不透明值处理。
- API Key 转 OAuth 时更换凭证及增加 OAuth beta 是认证路线的差异，不据此判为请求丢失。

### 尚未覆盖

其他 CLI 版本、ARM64、TLS 会话恢复、HTTP/2、图片 / 长上下文压缩 / 并行工具 / 子代理完整流程、
真实登录刷新与 profile、订阅计费及服务端风控。这里应写“未验证”，不能写成已确认有问题。

另两项容易误判：messages 抓包里的 timeout=5 来自实验的 `API_TIMEOUT_MS=5000`，不能直接
断言默认 600 秒错误；`cli` 与 `sdk-cli` 入口差异也必须先对齐运行模式。

## 验证方式和留痕

- 源码基线：release `ee2f9fea`，Go 1.27.0。
- 使用 Go `-overlay` 注入审查用测试，调用现有生产 service / repository；未改动源码工作树。
- service / repository 审查测试通过意味着复现与采集完成，不意味着这些差异已修复。
- 官方 CLI OAuth count 和 403 两组 PCAP 与接收正文核对一致，内核丢包为 0；403 进程退出 1 是被测响应结果。
- 解压对照为断网原生运行时探针与本地生产组件测试，没有访问真实 Anthropic 服务。
- 本机完整实验：`/Users/kingford/claude-capture/remaining-gaps-20261008/`。知识库同步归档脚本、输出与文件哈希。
- 本轮只添加审查文档和证据摘要，生产代码与依赖保持审查基线。
