# Claude Code 对齐后的综合复核

日期：2026-10-09（Asia/Shanghai）。记录：CC-20261009-005。
固定审查提交 `c20ac40abd2cd06d19df8448af53a0579cd311fd`，包含参数 / gzip 修复 `5447bd74`
和请求日志 `be3642a6`。release 基线 `b4430850`，上游 `3f1a2ea0`。

**常规请求和已修复路径保持通过，但新增边界仍有三项待修问题。**
本轮只新增审查工具及证据，不修改生产实现；不把这份报告误写成这些问题已经修复。

## 结果与范围

固定官方 Linux x64 CLI 2.1.292 / SDK 0.128.0，SHA-256：
`a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。
未修改 CLI，独立 HOME / 配置，假凭证，Docker `--network none` 和回环 TLS 服务。

本轮重新采集原 51 组，并增加压缩级别 1 / 9、600,000 字符系统提示下的分块模式 1 / 2、
Unicode gzip、大计数请求、top_p / top_k 的 EXTRA_BODY 分支，共 **59 场景、99 请求**。
其中 8 条压缩请求，全部原始发送字节与 PCAP 一致、丢包 0。

| 对照 | 本轮结果 |
|---|---|
| 生产转发组合 | 396/396 逻辑 JSON 一致；392/396 原发送字节一致 |
| URL / 长度 / GetBody | 全部通过；长度对应最终发送内容 |
| 同认证应用头 | 196/198 一致；两个差异均为压缩计数丢 Content-Encoding |
| 真实传输，日志关 / 开 | 4 种正文 × 4 连接路径 × 2 日志状态，共 32 组；正文、完整头值和非随机 ClientHello 全部一致 |
| 真实头序 | 24/32 与原生一致；8 个分块 gzip 组合不同 |
| 日志正文证据 | 16/16 开启日志组合，原始字节、偏移、SHA-256 和完成标记匹配；认证头脱敏、写入失败 0 |
| 托管恢复 | 12 会话、96 生成、24 计数通过，跨会话混入 0 |
| 回归 | component 6 包、149 个通过事件；service 374 个通过事件；repository 9 个集成测试通过，事件数包含子测试 |

API Key / OAuth × passthrough 配置的四组合包含 OAuth 配置重复，不是四套独立实现。
跨认证必需的 OAuth beta 单独处理。实际传输路径为直连、HTTP CONNECT、HTTPS CONNECT、SOCKS5。

原生运行时生成字段、归因 / cch 黄金向量、会话、恢复、日志与传输回归通过。本轮没有重跑
全部 337 个独立 cch 运行时探针、全仓库 unit / integration / lint，不沿用旧执行次数冒充新结果。

## 1. P2：大型 count_tokens 的 gzip 未保留

新场景 `gzip-count` 开启请求压缩并提供较大的系统提示。原生 `/context` 的三条计数请求中，
一条确实使用 gzip，另外两条未压缩。压缩请求没有 messages 的 billing / cch 块。

同一条捕获经过四个生产配置组合后，JSON 内容不变，但出站取消 gzip 和 Content-Encoding。
因此产生 **4 个原发送字节差异**、**2 个同认证头值差异**。

原因在 [finalizeNativeClaudeRequest](../backend/internal/service/gateway_claude_native.go)：
先要求 billing 满足已验证 cch 布局，再将原 gzip 恢复限定为 `/v1/messages`，计数请求不能进入。
建议把压缩保留条件与 cch 计算条件分开，为实测 count_tokens 增加独立门控与原字节回归。

这是原生保真缺口。本轮没有证据证明未压缩的计数 JSON 被官方拒绝。

## 2. P2：分块 gzip 的 Content-Encoding 头序不同

600,000 字符系统提示、`CLAUDE_CODE_GZIP_REQUEST_BODY_BLOCKS=2` 的未修改 CLI 请求为：

```text
Accept → Content-Encoding → Content-Type → User-Agent → …
```

当前 Go 原生传输统一输出：

```text
Accept → Content-Type → User-Agent → … → Content-Encoding → Connection → …
```

模式 2 在 JavaScript 层先压缩，主动设置 Content-Encoding，再以字节正文交给 fetch；
普通路径让运行时执行压缩，编码头追加在应用头之后。两种来源不能统一套后者的头序。
本轮分块模式在四条连接路径、日志开 / 关共 **8 组均复现**；压缩字节、头值和握手不受影响。

静态依据来自固定二进制中的 `chunk-9yn9h839.js`，`US / aM` 分支；已核对模块字节确实
存在于同一 Linux 二进制中。Go 位置为 [nativeClaudeHeaders](../backend/internal/repository/claude_native_transport.go)。

不能只用 gzip 的 OS 字节猜压缩来源：本轮普通 Unicode / 指定级别压缩也出现 OS=255。
模式 1 在空缓存首轮仍走运行时压缩，本轮没有证明多轮块复用。头序影响限于协议对照，
没有证据证明它触发真实服务端拒绝或风控。

## 3. P2：编码头覆盖有大小写冲突，并能发出错误编码

这是实际可保存的账号头覆盖配置，不是直接修改测试请求头：

```json
{"header_override_enabled":true,"header_overrides":{"content-encoding":"identity"}}
```

问题链路：

1. [账号覆盖](../backend/internal/service/account_header_override.go) 将未知头名按小写写入；
   `headerWireCasing` 尚未登记 Content-Encoding。
2. [getHeaderRaw / setHeaderRaw](../backend/internal/service/header_util.go) 对这个未登记字段不做
   全大小写扫描，finalizer 的大写查询看不到小写覆盖值。
3. 原 gzip 恢复又写入 `Content-Encoding: gzip`，两个不同大小写的键并存。
4. 原生传输将二者归一为一个键时，后写值取决于 Go map 遍历顺序。

实际传输实验使用生产构建器导出的请求，经过生产 HTTP 组件及严格本地解码器：

| 账号配置 | 实际观察 |
|---|---|
| 不改模型，无编码覆盖 | 2/2 gzip 字节完整，200 |
| 不改模型，覆盖 gzip | 2/2 gzip 字节完整，200；构建器内部仍出现重复大小写键 |
| 改模型，无编码覆盖 | 2/2 未压缩 JSON 有效，200 |
| 改模型，覆盖 identity | 2/2 未压缩 JSON 有效，200 |
| 改模型，覆盖 gzip | 2/2 声明 gzip 却发送 JSON，本地接收器 BadGzipFile / 400 |
| 不改模型，覆盖 identity | 12 次中 8 次实际为 gzip / 200，4 次实际为 identity 配 gzip 数据 / 400 |

22 次发送的 PCAP 原始字节均已核对，丢包 0。以上次数不是故障概率估计，也不是官方响应。
建议优先处理这项：编码头必须与实际正文编码一致，同时修正大小写归一与去重；
也应评估禁止静态覆写这类正文结构头，而不是允许管理员仅更换声明。

旧测试通过 `req.Header.Set` 直接放入规范大小写，不能覆盖真实账号配置的这一分支。
本轮首版观测器也调用了同一个有大小写限制的 getter，故只看它的结果会漏报；
最终同时保存原始键变体，并以独立实际传输为判据，保留早期观测记录。

## 仍然明确保留的语义边界

- 普通 API 的 disabled thinking 按直接控制处理，与 CLI 的 EXTRA_BODY-only 后置覆盖仍有
  temperature / context_management 缺省差异。这已在 CC-20261009-004 说明，不算新回归。
- top_p / top_k 的 EXTRA_BODY 请求保留了 CLI 事先构造的 thinking / effort / 上下文；
  普通 API 仍采用已有的采样保护策略，三类字段不同。它们没有被上轮仅针对 temperature 的修复覆盖。
- verbose 的 display=updates 与普通输出的 omitted 是模式差别，不能列成五个新缺陷。
- 5.5 参数限制、真实签名、订阅 / 计费、OAuth 刷新和真实服务端接受不由本地成功响应证明。

## 实验边界与可复现性

固定源码快照始终未改变。原生采集与生产组件重放、实际传输、数据库回归分别计证，
不冒充一个带真实鉴权 / 数据库 / 官方上游的完整部署验收。
本轮未覆盖 HTTP/2、TLS 恢复、ARM64、其他 CLI 版本、全部功能开关和所有工具 / 远程代理。

初次组合运行漏挂 policy 测试的 testdata，转发和参数输出已完成，policy 随后独立重跑。
原双线程 HTTPS 模拟代理有一次 TLS 解码失败，未单独确定责任来源；改用每连接单线程
非阻塞 TLS 转发的独立回环代理后，32 组全部完成并通过原始字节核对。失败记录保留，
不将它写成已证明的生产日志回归，也不把重跑成功当成不存在任何偶发网络问题。

[结构化证据](claude-post-alignment-audit-20261009.json) 保存逐组合差异、源码定位、检查结果与哈希。
原始材料：`/Users/kingford/claude-capture/post-align-audit-20261009-rkgawtwu/`。
复现工具见 [验证说明](../.github/claude-validation/README.md#对齐后的日志与压缩边界复核)。
维护者随后要求提交全部代码；本轮提交包含审查工具及报告，并归档此前未提交的文档，
不代表上述生产问题已经修复或已部署。
