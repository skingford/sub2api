# Claude Code 请求参数与算法再次复核

日期：2026-10-08（Asia/Shanghai）。记录：CC-20261008-011，接续 CC-20261008-009。

**不能据此认定所有模式与原生 CLI 完全一致。原生转发在本轮已测范围内一致；普通 API
转换、托管恢复和未覆盖的平台仍需分别判断。**

本轮没有修改生产代码。工作区在审查期间持续出现其他修改，因此固定了 **22:25:34** 的源码
快照后执行验证。基线 HEAD 为 `93275632a6a66dfc2648e9504a7d16bcf32da5d7`，
release 为 `b4430850`，上游为 `3f1a2ea0`；快照包含当时未提交的部分修复。
随后新增的 5.5 profile、托管 compaction 等修改不属于这个快照，不能用本轮结果替它们验收。

完整 3,260 个源码文件的清单 SHA-256：
`2d060dc6e52074874f14033e5590f4407a5bd131a230e45899a6219c7d6befd7`。
[机器可读记录](claude-request-recheck-20261008.json) 保存关键源文件哈希、原始证据哈希、
每个转发组合的结果和快照之后变化的文件列表。

## 已验证结果

未修改的官方 CLI **2.1.292 / SDK 0.128.0 / Linux x64**，二进制 SHA-256
`a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。
Docker 使用 `--network none`、假凭证、独立配置和回环 TLS 服务。

17 个场景得到 27 条请求：24 条 messages、3 条 count_tokens。包含并行工具、图片、合成
thinking、compact、resume、额外 metadata、模型别名、输出模式、中文 / emoji 和 503 重试。
各请求经过 API Key / OAuth × passthrough 关闭 / 开启，得到 108 个组合；OAuth 的两种
passthrough 设置并不代表不同发送实现。

| 对照项 | 本轮结果 | 结论范围 |
|---|---|---|
| 原生捕获真实性 | 27/27 接收正文与 PCAP 解密字节一致，内核丢包 0 | 未修改 CLI 对本地模拟器 |
| 参数与序列化 | 108/108 正文逐字节一致 | 包括 messages、tools、system、metadata、thinking、cache_control 等实际出现的字段 |
| 传参路径 | 108/108 方法、路径与 query、Content-Length、GetBody 一致 | 请求构建完成时检查，不冒充完整部署联调 |
| 应用请求头 | 同认证类型 54/54 无增删或值变化 | 凭证替换和传输负责的 Host / Connection 等字段单独处理 |
| 跨认证转发 | 48 个 API Key → OAuth 组合增加 OAuth beta，其余应用头保持 | 认证路线变化，不是无条件字节等价 |
| cch | 新抓到的 24 条带 cch 请求复算全部一致；81 个原生运行时黄金向量回归通过 | 2.1.292；计数样本不含 cch，保持缺省 |
| cc_version 归因后缀 | 10 个提取原函数向量通过 | UTF-16 索引和 SHA-256；原生归因保留，普通转换的逻辑输入不保证等同 CLI |
| TLS ClientHello | 20 个承载模型 / 计数的连接匹配 Go 黄金摘要，长度均为 1,499 字节 | 清零随机数、session ID 和临时公钥；Linux x64，HTTP/1.1 |
| HTTP 发送回归 | 原生 HTTP/1.1 头序、正文、复用、取消和代理证书校验专项通过 | 本轮没有重新跑所有真实代理路径 |

ClientHello 归一化 SHA-256：
`8845ac2401a951ffc4acef2824c3422124c7883e0c9bc4b5f90d3c6be05da2f9`。
这里比较完整非随机握手内容，不只比较 JA3。

## 与上一轮差异的关系

快照已经包含两项修复，且本轮证据支持其效果：

- `x-cc-compaction-request`、`x-cc-context-compacted` 在原生转发中保留。
- 显式 `thinking.display=updates` 的普通转换会发送配套 beta；调用方显式提供该 beta 也保留。

额外 metadata 的测试请求也通过快照中的托管 Begin / BeforeSend / Finish；这只覆盖该
合成字段和测试存储，不代表任意扩展身份都受支持。

快照仍可复现以下差异：

- **普通 5.5 转换**：原生 Sonnet 5.5 为 adaptive thinking、effort=medium、上下文编辑，
  转换输出则没有这些默认字段并添加 temperature=1；max_tokens 均为 128000。
  普通 API 与 CLI 的运行模式不同，不能把 display=omitted 与 verbose 的 updates 差异一并误判为错误。
- **托管 compact**：接受摘要后，压缩后的下一轮仍因历史不匹配被拒绝；同一快照也拒绝托管 5.5。

工作区随后已出现针对上述行为的新实现。**这两项是冻结快照的发现，不是对随后实现的失败判定。**
普通 API 转换还缺少 CLI 本地工具、项目上下文和历史状态；托管模式有自己的会话与恢复语义，
这些模式不能用“某一份正文一致”推广成整个 CLI 产品行为相同。

## 验证方式与限制

固定快照执行以下专项，4 个包通过，394 个通过事件（含子测试），没有失败：

```bash
GOTOOLCHAIN=go1.27.0 go test -tags=unit \
  ./internal/pkg/claude ./internal/pkg/tlsfingerprint ./internal/service ./internal/repository \
  -run 'TestCCH2292|TestClaudeNative|TestClaudeCode2292|TestClaude2292Contract|TestClaudeSession|TestNativeClaude|TestTLSClientPoolSeparatesProfileContents' \
  -count=1 -json
```

Go overlay 另运行扩展历史 / 参数审查和 `TestNativeRequestRecheck`；通过只表示采集成功，
108 个组合的等价结论来自输出字段检查。CLI 捕获与生产组件重放分两阶段，网络 / 存储边界
使用测试实现，不代表完整网关部署、真实数据库与官方模型服务的端到端验证。

首次计数重放直接调用服务时遗漏 handler 设置的 CLI 校验 context，产生了假 UA 差异。
已修正采集器：使用生产 `NewClaudeCodeValidator` 和 `SetClaudeCodeClient`，再对全部
17 个场景重跑；最终 108 个组合均识别为原生，假差异消失。初次日志保留，未作为生产缺陷发布。

本轮没有重新运行全量 unit / integration / lint。没有验证真实订阅、计费、OAuth 刷新、
官方接受、真实 thinking 签名、风控、HTTP/2、TLS 会话恢复、ARM64 或任意新版本。
`--json-schema` 场景只证明已捕获请求的转发保真，不证明完整结构化输出成功。

原始证据及源码快照：`/Users/kingford/claude-capture/request-recheck-20261008-c0q5bkcy/`。
复现工具见 [隔离验证说明](../.github/claude-validation/README.md#请求参数与算法复核)。
本轮仅新增审查脚本、报告及证据摘要，未提交、推送、合并或部署；后续提交关联
`Claude-Change-ID: CC-20261008-011`，当前实现基线所属 PR 为 #5。
