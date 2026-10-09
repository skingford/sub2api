# 已推送修复版本的源码与隔离 CLI 复核（CC-20261009-013）

**结论：仍不能称为全部一致。** 常规原生转发、Haiku 5.5 参数、普通压缩迁移、CCH 和原生传输
通过本轮复测；扩大输入和入口覆盖后，确认四处未覆盖的问题：JSON Schema 丢失、Chat 停止词
丢失、Chat 输出上限被提高，以及 Unicode 摘要归一化不一致导致托管续聊失败。

本轮只增加审查工具与证据，未修改生产代码。详细数据见
[结构化结果](claude-postfix-audit-20261009.json)。旧报告与修复记录保留，由本条补充边界。

后续修复：维护者要求完整修复后，四项问题及固定验收合同的实现、验证见
[CC-20261010-001](claude-constraint-contract-20261010.md)。下文保留修复前的证据。

## 固定基线

- 被审 HEAD：`afb0c04a361a10ac17dc55ea00a573f1b415fcc7`；运行时代码为
  `765a12ec4199dfeb490b60b0cfcc47db10bdff14`，分支 `codex/claude-source-runtime-audit-20261009`。
- 上游基线：`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。未同步上游、合并或部署。
- 从 HEAD 执行 `git archive`，固定 3,332 个后端源码 / 模块 / 回归 JSON 文件。
- 未修改 Linux x64 CLI 2.1.292 / 2.1.295，SDK 0.128.0、runtime v26.3.0；SHA-256 分别为
  `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`、
  `4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358`。
- 两版重新提取 2,260 / 2,340 个 JS 模块，各含三个 Zstd 模块；4,600 个解码模块逐字节匹配
  前次提取。源码指分发二进制中的打包 JS，不是重建出的原始 TypeScript。
- CLI 在 Docker `--network none`、第一方域名回环映射、全新配置目录及 tmpfs 中运行，
  使用假凭据和本地 TLS 模拟响应。Go 1.27.2，生产转发与传输均由冻结源码编译。

本机材料：`/Users/kingford/claude-capture/postfix-audit-20261009-m7wo9q_4/`。

## 四处确认的问题

### F1：Responses / Chat Completions 静默丢失 JSON Schema

对六个已验证模型分别通过三个入口传入同一个 `{ok: boolean}` schema：
入口矩阵使用 OAuth 账号，以覆盖生产转换和身份处理；没有把它计作全部账号配置的独立验证。

| 入口 | 输入字段 | 最终生产转发 |
|---|---|---|
| Messages | `output_config.format` | 六个模型均保留 schema |
| Responses | `text.format` | 六个模型均丢失 schema |
| Chat Completions | `response_format.json_schema` | 六个模型均丢失 schema |

这 18 个组件请求均收到本地成功响应，转换过程没有将不支持的约束报告为错误。
问题位于 [responses_to_anthropic_request.go](../backend/internal/pkg/apicompat/responses_to_anthropic_request.go)
第 25 行附近的输出构造及后续转换：没有消费 `req.Text.Format`，目标 `AnthropicOutputConfig`
也只有 effort。Chat 第一段已经把 `response_format` 放进 `Responses.Text.Format`，随后仍丢失。

原生 CLI 的独立控制需要分清：在本次环境中，`--json-schema` 使用
`StructuredOutput.input_schema` 工具形式；显式 EXTRA_BODY 则可以把 format 放入请求体。
三模型原生捕获均能看到 schema，没有把“CLI 总使用 output_config.format”当作前提。
模拟器没有真实执行结构化生成，证据只证明约束进入或丢失于待发送请求。

建议为 Claude 目标建立明确的 schema 映射及所需能力标记，或在不能支持时显式拒绝。
不能仅返回成功而删除调用方的结构化输出约束。

### F2：Chat Completions 静默丢失 stop

六模型分别传 `stop=["STOP_AUDIT"]`。最终 Chat→Responses→Anthropic 请求均没有
`stop_sequences`；等价 Messages 输入六例全部保留。当前 Responses 中间结构没有 stop 字段，
[chatcompletions_to_responses.go](../backend/internal/pkg/apicompat/chatcompletions_to_responses.go)
也未传递 `ChatCompletionsRequest.Stop`。

未修改 CLI 的三模型 EXTRA_BODY 对照实际发送了相同 stop_sequences，PCAP 与接收记录一致。
这里没有把非标准 Responses.stop 当作合同，也没有推断真实服务一定在该字符串处停止；
发现的是用户已提供的停止条件在转发前丢失。

### F3：Chat 显式 max_tokens=64 被提高到 128

六模型的三个入口对照结果一致：Messages 为 64，Responses 的 max_output_tokens 为 64，
Chat 的 max_tokens 被转换成 128。另用未修改 CLI 的 Sonnet 4.6、Opus 5.5、Haiku 5.5
显式发送 max_tokens=64，三份新 PCAP 均核对成功。

原因是同一 Chat→Responses 转换器第 63–68 行无条件套用中间 Responses 协议的
`minMaxOutputTokens=128`，即使真正目标是 Anthropic。它改变了显式上限；这与用户未指定
参数时选择一个缺省值不同。建议根据最终目标处理限制，不能静默提高调用方上限。

### F4：摘要含 BOM / NEL 时，托管续聊误判历史冲突

从 2.1.295 的 `chunk-53bsrq2x.js` 精确提取 `Q6o` 与其正则常量，确认其使用 JavaScript
`trim()`。网关 [claude_recovery_compaction.go](../backend/internal/service/claude_recovery_compaction.go)
第 110–113 行使用 Go `strings.TrimSpace`。两者的空白字符集合不同：

| 摘要首尾字符 | 原生 JS | Go | 原生 CLI 捕获回放到生产恢复管理 |
|---|---|---|---|
| 普通空格 | 去除 | 去除 | 六次请求全部接受 |
| U+FEFF（BOM） | 去除 | 保留 | 第三次请求，即首次压缩后续聊，历史冲突 |
| U+0085（NEL） | 保留 | 去除 | 同样在第三次请求发生历史冲突 |

**两个 CLI 版本均复现**：各三场景 / 18 请求，原生 CLI 均正常完成，PCAP 零丢包；
把原始请求和对应 SSE 回复按顺序送入生产 `Begin / BeforeSend / Finish` 后，BOM 和 NEL
场景各自失败，空格对照通过。保存的摘要与 CLI 实际重建的首条用户消息有确切字符差异。
生产 handler 将该错误映射到 409；这是原生捕获加恢复组件回放，未冒充完整线上部署。

归一化函数在 release `7ce838ee3` 与当前代码中相同，问题并非本次 2.1.295 修复新增。
上一轮修复覆盖了新版本识别和保留消息尾文，本次扩展到 Unicode 摘要边界。
建议匹配 ECMAScript 空白定义，同时保留摘要来源与保留历史校验，不应取消历史冲突检查。

## 原生字节不一致的策略场景

额外测试通过晚期 EXTRA_BODY 写入 `thinking.display=updates` 与 low effort，但 CLI 没有
同时声明 `thinking-display-updates` beta。网关现有能力清理将 display 改为 omitted，并正确重算
CCH。因此该单个原生请求的四个转发配置不再逐字节相同，effort 和其他正文内容保持。

该行为由 [gateway_request.go](../backend/internal/service/gateway_request.go) 第 1017–1020 行
明确决定，属于既有策略，不列为第五个新缺陷。也不能将这四条差异从分母删除后宣称全部一致。
两版各自的空 anthropic-version 仍补默认值；认证类型转换的 beta 差异仍单列。

## 逐项对照结果

| 项目 | 本轮证据 / 结果 |
|---|---|
| 源码与二进制 | 固定哈希重新提取，4,600 个模块与原来源一致 |
| 模型 / 认证 / 工具 / 图片 / 多轮 / resume / count_tokens | 重新运行既有原生矩阵及新增组合 |
| 生成、计数、gzip 原始正文 | 1,528 个生产转发组合，1,524 个完全一致；四例是上述显式策略 |
| 方法、URI / query、Content-Length、GetBody、认证选择 | 全部转发组合通过 |
| 六模型 × 三 API 入口 × 显式控制 | 102 个观察；F1–F3 已确认 |
| 普通连续压缩与迁移 | 292 三模型 / 12 会话，295 四模型 / 16 会话全部通过，混入 0 |
| Unicode 摘要 | 六场景均有原生 PCAP；生产恢复管理的四个 BOM / NEL 场景失败 |
| 签名及工具往返 | 新采集 55 thinking-tool；既有生产转换和签名专项回归通过 |
| CCH | 独立原生运行时 605 向量重新采集，PCAP 零丢包；Go 全部匹配 |
| TLS / 完整头值 / 头序 / 直连与三种代理 | 两版本共 48 个真实发送组合，严格核验全部通过 |
| 请求日志 | 24 组字节、摘要、完整性、凭据脱敏核验通过 |
| 403 来源标记及 JSON 错误恢复 | 原始 / 生产转换响应分别回放，结果详见结构化记录 |
| 工程回归 | service 1,690 个通过事件；五组件包 389 个通过事件；CCH 606 个通过事件 |

另外保留默认策略差异：Sonnet 5.5 的 Messages 缺省 effort 为 medium，两个 OpenAI 入口为
high；Opus / Sonnet 5.5 的 OpenAI 转换省略 display，而 Messages 显式写 omitted。
这是已确认的字段差异，不能仅凭客户端源码断言服务端行为完全相同或不同。
本次 Haiku 5.5 缺省参数在三个入口一致，显式 xhigh 和签名往返修复保持有效。

原生主捕获共 228 场景 / 382 请求；错误回放另 44 场景 / 116 请求。合计 **272 场景 /
498 请求**的最终 PCAP 全匹配、内核丢包 0，其中 168 条 gzip。错误原始 / 转换后配对的
请求次数、编码降级顺序和 CLI 退出状态全部相同。605 个原生运行时 CCH 向量另行计数，
不混入未修改 CLI 的场景总数。1,528 个转发组合含 OAuth passthrough 的配置重复，不算
独立实现；schema 场景仅采集模拟环境行为，不宣称已经生成符合约束的真实模型输出。

## 复现、留痕与范围

新增采集器：`postfix_edges_lab.py`（18 组参数 / 工具组合）、`token_limit_lab.py`（三模型低上限）、
`summary_edges_lab.py`（三种摘要空白）。仍使用验证 README 中的固定二进制、network-none、
回环域名、独立配置、tmpfs 及 `verify_comprehensive.py`。

两个 Go overlay 分别观测 API 入口最终构造与生产恢复管理；PASS 只说明数据收集完成，
必须读取输出中的 schema / stop / max_tokens / error 字段判断一致性。
详细来源哈希、命令及原始材料保存在本机证据目录。未提交完整提取代码、二进制、TLS 私钥或原始大正文。

本轮没有改生产源码、重跑数据库集成、重新发布或部署；完整工程结果仍见 CC-20261009-012，
不能当作本轮重跑。本轮重新运行的是专项测试与上述 CLI / 抓包 / 组件对照。
HTTP/2、TLS 恢复、295 MacOS / ARM、所有远程特性开关、真实签名、官方接受、订阅与计费均不在结论内。
未修改 CLI、修改 JS 入口的 CCH oracle、提取 JS 函数和合成响应分别记录，不混作同一种证据。
