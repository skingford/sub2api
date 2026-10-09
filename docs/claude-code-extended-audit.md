# Claude Code 2.1.292：扩展流程差异复核

日期：2026-10-08（Asia/Shanghai）。记录：CC-20261008-009。

后续修复与最终验证见 [CC-20261008-010 扩展对齐修复](claude-alignment-fixes.md)。本文保留审查基线的原始发现。

本轮依据知识库 `wiki/逆向工程/` 的五篇分析及本仓库历次记录，审查当前
`93275632a6a66dfc2648e9504a7d16bcf32da5d7`。本地 release 基线为 `b4430850`，
上游基线为 `3f1a2ea0`。托管恢复属于当前主题分支；严格转发和普通转换的问题也存在于该 release 基线。
此前 CC-20261008-005 的七类差异已经由 006、007 修复，不再列为未修问题。

本次只增加实验代码、证据摘要和报告，没有修改生产代码、合并分支或部署。

## 结论

| 类型 | 当前仍有的差异 | 影响与优先级 |
|---|---|---|
| 严格转发 | 丢弃 `x-cc-compaction-request`、`x-cc-context-compacted` | P2；原生压缩标记未完整转发，服务端影响未验证 |
| 普通 API → OAuth | `thinking.display=updates` 保留，但对应 beta 缺失；调用方显式提供 beta 也被过滤 | P2；正文能力与最终头不配套 |
| 托管恢复 | 接受 `/compact` 摘要请求，压缩后的下一条主请求才拒绝历史 | P1，限启用托管模式；CLI 已换成压缩历史后无法在同一托管对话正常续聊 |
| 普通转换 / 托管范围 | 原生 `sonnet` 在本实验中解析到 5.5，完整默认配置和托管模型门槛仍只有三个旧模型 | P2；普通转换使用旧兜底，托管直接拒绝；原生透传正常 |
| 托管明确限制 | `CLAUDE_CODE_EXTRA_METADATA` 的普通扩展字段也被拒绝 | 兼容范围限制；不能把它误写成只拒绝子代理身份 |

## 1. 压缩请求仍丢两个原生头

未修改 CLI 在 `/compact` 的摘要生成请求同时发送：

```http
x-claude-code-request-class: compaction
x-claude-code-compaction: manual
x-cc-compaction-request: manual
```

压缩后的下一轮同时发送：

```http
x-claude-code-context-compacted: manual
x-cc-context-compacted: manual
```

当前白名单保留 `x-claude-code-*`，遗漏两个 `x-cc-*`。API Key / OAuth × 普通 / passthrough
四种组合各复现一次，总计 **8 个出站报文缺头**。这些请求正文、原生 cch 和其他应用头保持；
OAuth 路线增加 OAuth beta 属于认证方式变化，单独处理。

依据：[请求头白名单](../backend/internal/service/gateway_service.go)，证据摘要中的
`production_observations`（case=`compact`）。建议补齐两个实测头及双向完整头集合回归；
不能仅以正文相等判定原生流程一致。

## 2. 显式 thinking 显示能力与 beta 不配套

固定 Sonnet 4.6、相同假凭证和提示，额外核对六种运行方式：

| 调用方式 | 原生 thinking.display | thinking-display-updates beta |
|---|---|---|
| API Key，JSON 输出，命令行提示，不带 verbose | omitted | 无 |
| API Key，JSON 输出，stdin 提示，不带 verbose | omitted | 无 |
| API Key，JSON 输出，stdin 提示，带 verbose | updates | 有 |
| OAuth，JSON 输出，命令行提示，不带 verbose | omitted | 无 |
| OAuth，JSON 输出，stdin 提示，带 verbose | updates | 有 |
| OAuth，stream-json 输入 / 输出，带 verbose | updates | 有 |

因此普通转换的默认 `display=omitted` **本身不是错误**；它匹配已经验证的普通输出模式。
不能为了接近另一个 CLI 模式，给全部请求无条件改成 updates。

实际缺口在显式参数路径：发送普通 API 请求
`thinking={"type":"adaptive","display":"updates"}`，生产转换保留正文，但没有带
`thinking-display-updates-2026-08-18`。即使请求头显式携带该 beta，最终仍被
`computeFinalAnthropicBeta` 的兼容能力白名单过滤。

两种输入都调用生产 Forward 复现。模拟器返回 200 仅供观察构建结果，**不是官方接受证明**。
原生转发不存在这个丢 beta 问题。建议在明确支持该能力后建立条件映射，并同步处理管理员过滤策略；
或者明确拒绝不支持的正文组合。

依据：[最终 beta 构造](../backend/internal/service/gateway_upstream_request.go)、
[版本默认值](../backend/internal/service/gateway_claude_2292_defaults.go)；摘要中的 `explicit_display`、`mode_profiles`。

## 3. 托管模式在压缩完成后才拒绝

原生 `/compact` 产生三次请求：普通对话、压缩摘要、压缩后继续。直接运行 CLI 对本地 TLS
模拟器时三次均完成；CLI 把旧历史替换成压缩文本，仍使用同一 session。

将原始请求和对应的实际模拟响应依序送入生产 `Begin → BeforeSend → Finish`：

1. 第一轮接受并记录。
2. `request-class=compaction` 的摘要请求历史仍是合法扩展，接受并记录。
3. 下一条主请求只有压缩后的一个用户消息；已保存历史含四条消息，`Begin` 返回历史冲突。

入口目前只单独拒绝 `request-class=auxiliary` 的生成请求，不会提前拒绝 `compaction`。
既有“无法匹配的 compact 会停止”说明仍成立，但不足以描述停止发生在 CLI 已经压缩之后。
即使账号始终可用、没有发生迁移，也会出现该差异。

建议先明确托管模式的压缩契约：支持可信的历史替换事务，或在 CLI 改写本地历史前拒绝压缩请求并
给出明确说明。不要删掉历史校验来放行任意截断，也不要把不同 session 的材料合并。

本轮执行生产服务、加解密及历史核对，使用测试存储，不是完整部署 / PostgreSQL 联调。
HTTP 409 映射来自入口源码；采集的服务层结果是 `ErrRecoveryConflict`。

依据：[恢复入口](../backend/internal/handler/gateway_claude_recovery.go)、
[恢复服务](../backend/internal/service/claude_recovery_service.go)、
[历史校验](../backend/internal/service/claude_recovery_history.go)；摘要 `managed_service` / `compact`。

## 4. CLI 默认模型与已验证模型集合不同

本次固定二进制、空配置、API Key、`--model sonnet` 的原生请求为 `claude-sonnet-5-5`，
包含 adaptive thinking、effort=medium、上下文编辑，以及对应的按轮控制 beta。
这只说明该二进制在本实验配置中的解析行为，不证明真实账号拥有该模型。

相同模型的普通 API → OAuth 转换仍使用未覆盖模型兜底：max_tokens=128000 相同，
但自动添加 temperature=1，不添加 thinking、effort 或上下文编辑。转换并没有报不支持。
托管恢复则在解析阶段直接拒绝，因为仅接受 Sonnet 4.6、Opus 4.6、Haiku 4.5。
原生 5.5 捕获在本次四个转发组合中正文保持一致。

普通 API 与 CLI 的工具、系统提示本来就不同；这里比对的是同一模型的缺省参数和能力集合，
不是要求把 CLI 全部本地状态硬塞给普通 API。建议单独完成 5.5 的 API Key / OAuth、
权限模式和按轮控制采集，再扩展 profile；不能只放宽模型名单。

依据：[普通转换模型分支](../backend/internal/service/gateway_claude_oauth_body.go)、
[托管模型门槛](../backend/internal/service/claude_recovery_history.go)；摘要 `generic_profiles`。

## 5. 扩展 metadata 属于托管模式当前限制

给未修改 CLI 设置 `CLAUDE_CODE_EXTRA_METADATA={"audit_tag":"local-synthetic-tag"}`，
原生 `metadata.user_id` 包含该字段及三个核心身份字段。严格转发四种组合原样保留。
托管模式只接受 `device_id/account_uuid/session_id` 三个键，因而拒绝这条正常 CLI 请求。

这是明确的保守策略，但范围比“拒绝 parent_session_id / tk 等子代理与远程身份”更广。
若要支持普通标签，须同时定义保存 / 重写和隔离规则；目前 `recoveryRewriteSession`
重新格式化为三个字段，不能只去掉解析阶段的拒绝。

## 新增的正向证据与边界

- 8 个扩展场景的 15 条模型请求，经过 API Key / OAuth × 普通 / passthrough，**60/60 正文逐字节一致**。
- 两个并行 Read 的工具 ID / 结果对应关系保持；图片 Read 确实产生内联 PNG 的 base64 工具结果。
- 并行工具、图片、thinking + 工具、简单 `--resume` 的捕获历史通过生产托管核对和发送状态检查。
  这没有证明工具回合中允许换号；这些工具结果请求仍不是迁移边界。
- thinking signature 为模拟器合成值，只证明不透明值的保存与校验，没有证明签名真实性。
- `--resume` 只覆盖相同目录、未压缩、未编辑、同一模型的简单恢复，不推广到任意历史。
- `--json-schema` 场景产生两次请求并退出 0，但模拟响应没有产生 `structured_output`；本次不宣称
  结构化输出完整功能已验证。
- 六个模式对照增加 6 条请求，合计 **21 条、14 个场景**。全部 PCAP 解密字节一致，内核丢包 0。
- 本轮未重新证明四种代理链路、完整 ClientHello、HTTP/2、TLS 恢复、ARM64、真实登录刷新、
  profile、订阅计费或服务端风控；以前限定场景的证据继续保留。

## 复现与记录

执行器和审查用 Go overlay 见 [.github/claude-validation/README.md](../.github/claude-validation/README.md#扩展流程审查)。
本轮官方 Linux x64 二进制 SHA-256：
`a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`，SDK 0.128.0。
全部 CLI 运行均为 Docker `--network none`、回环域名、假凭证，没有挂载用户登录目录。

[机器可读摘要](claude-extended-audit-20261008.json) 保存逐组合结果、模式对照、生产源码和原始材料哈希。
本机证据目录：`/Users/kingford/claude-capture/extended-audit-20261008/`。
最初模式采集器未处理 verbose JSON 的数组输出而中止；修正采集器后在新目录 modes2 重跑，
没有覆写失败记录，也没有把采集器错误算成 CLI 问题。

Go 1.27.0 的审查采集、服务历史序列、普通转换和显式 beta 实验均执行成功。
“测试 PASS”表示差异已被采集，不表示缺口已修复。没有为本轮报告改动重复执行全量 unit / integration / lint。
