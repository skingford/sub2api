# 反编译源码、隔离 CLI 与网关逐项复核（2026-10-09）

**结论：不能宣称全部一致。** 当前 release 对 2.1.292 的已测原生请求保持一致；
2.1.295 原生正文也可以保留，但托管压缩续聊、Haiku 5.5 普通 API 转换、原生传输配置
存在三个明确的兼容缺口。第一项包含两个经单变量实验确认的原因。

本轮是审查，**没有修改网关生产代码**。实验修正仅用于证明原因，不能当作已交付修复。
对应记录为 `CC-20261009-011`；数值、来源哈希与错误回放明细见
[结构化结果](claude-source-runtime-audit-20261009.json)。

后续状态：维护者已要求修复，三项问题的代码与验证记录见
[CC-20261009-012 修复说明](claude-295-compatibility-fix-20261009.md)。下文保留修复前的审查结果。

## 固定基线与证据性质

- 被审 release：`7ce838ee33663b8b4dc93296a51fad25b2daa49c`，包含 CC-20261009-009 三项协议修复。
- 上游基线：`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`；没有同步或合并上游。
- 开始时工作分支 HEAD：`f5188a2e64e8f13b236579fb15fbccf4eae1f8a2`。
  该分支的远端 CLI 配置修复仍保留，本轮从 release 新建审查分支。
- Go 1.27.2；对 release 执行 `git archive`，冻结 3,190 个 Go / SQL / 模块文件。
  结束时与当前后端逐文件 SHA-256 一致。
- Linux x64 CLI 2.1.292：`a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。
- Linux x64 CLI 2.1.295：`4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358`，
  本轮从固定官方分发路径下载，并与该版本 manifest 校验。
- Docker 镜像：`sha256:eb7d83c374af3c3a113387e520f7f0247719f733e4a8ed0e9da73c98a69a91b4`。
  CLI 均在 `--network none` 中运行，第一方域名映射至回环 TLS 接收器，使用假凭据和新配置目录。

重新从两个二进制只读提取 2,260 / 2,340 个嵌入 JS 模块。
两版均有 3 个模块使用 Zstd，先解压再定位函数；保存原始记录偏移、压缩前后长度及哈希。
这里的“源码”是分发二进制中的打包 JS，不是恢复出的原始 TypeScript；CCH 另外使用原生机器码 oracle。
2.1.292 的既有 16 处源码锚点全部重新核验；新版重点定位归因、SDK、gzip、模型目录和压缩摘要函数。
完整提取代码、实验二进制、TLS 私钥和大正文仅保存在本地证据目录，不纳入仓库。

本机证据根目录：
`/Users/kingford/claude-capture/source-runtime-crosscheck-20261009-333q2v1p/`。

## 本轮新发现

### F1：2.1.295 托管恢复中的 `/compact` 续聊失败（优先处理）

复现入口是 `TestClaudeAlignmentNativeCLILab`：未修改 CLI 连接本地服务，服务使用生产
恢复管理、账号选择和 Forward，持久化及上游回复使用测试实现。覆盖 Sonnet 4.6、
Sonnet 5.5、Opus 5.5 × API Key / OAuth，每组两条独立会话，连续压缩、续聊并迁移账号。
这属于服务组件联调，不是完整部署。

| 实验 | 结果 | 确认的问题 |
|---|---|---|
| 2.1.292 + 原始 release | 六组全部通过，12 会话、96 生成、24 count_tokens，跨会话混入 0 | 基线正常 |
| 2.1.295 + 原始 release | 六组全部失败，第一次压缩后续聊返回历史冲突；CLI 重试至测试超时 | 新版本不兼容 |
| 2.1.295 + 仅放开版本识别的实验副本 | 首次压缩可继续；第二次压缩后的摘要尾文仍被拒绝 | 两个独立原因 |
| 2.1.295 + 版本识别及精确尾文的实验副本 | 六组全部通过，12 会话、96 生成、24 count_tokens，跨会话混入 0 | 因果对照成立，不代表生产已修复 |

原因一：[claude_recovery_compaction.go](../backend/internal/service/claude_recovery_compaction.go)
第 48 行只允许 2.1.292 在缺少分类头时通过正文识别压缩。CLI 连接自定义 origin 时不发这些头，
2.1.295 的压缩请求因此被当作普通历史，下一次缩短后的消息无法匹配保存的会话。
同版本的第一方 origin 捕获带有 `x-cc-compaction-request`、`x-claude-code-compaction`
和 `x-claude-code-request-class`，不能将这个版本识别限制泛化到所有路径。

原因二：同文件第 203–225 行的摘要包装验证不接受 2.1.295 新增的“后续消息为压缩前保留原文、
摘要未看到它们”说明。该说明由 `chunk-53bsrq2x.js` 的 `RZt / DZt` 生成，已在第二次压缩后
实际请求中捕获。旧版分支只认识另一种短说明及旧 followup 组合。

建议后续修复按已验证版本和精确结构扩展，继续核对服务端已保存的摘要及保留消息。
不要通过跳过历史检查或全量接受任意尾文解决。实验副本没有作为生产补丁提交。

证据：`analysis/recovery-2.1.295.log`、六份 `first-rejected-body.json`、
`analysis/recovery-gate-control.log`、`analysis/recovery-wrapper-control.log` 及成功对照明细。

### F2：Haiku 5.5 普通 API 转换缺少原生默认参数

新版 `chunk-bps84pg6.js` 已定义 Haiku 5.5；实测裸 `haiku` 在 2.1.292 解析成 Haiku 4.5，
在 2.1.295 解析成 Haiku 5.5。显式 Haiku 5.5 在两版 CLI 的 token / effort 默认值也不同。

对相同完整模型 ID，普通 API 的生产转换与 2.1.295 原生默认请求比较：

| 字段 | 2.1.295 原生 Haiku 5.5 | 当前普通 API 转换 |
|---|---|---|
| max_tokens | 128000 | 128000 |
| thinking | adaptive，display=omitted | 缺失 |
| output_config.effort | medium | 缺失 |
| temperature | 缺失 | 1 |
| context_management | 保留 thinking 的 clear_thinking 编辑 | 缺失 |

原因位于 [gateway_claude_2292_defaults.go](../backend/internal/service/gateway_claude_2292_defaults.go)
的已验证模型表及其转换分支；现有列表没有 Haiku 5.5。显式 xhigh、temperature、关闭 thinking
场景也保留在逐字段结果中。原生 CLI 直通的这些样本全部保留原文；上述差异属于普通请求转换，
不能写成“所有 Haiku 请求都损坏”。参数存在不代表真实服务认可这些组合。

建议为 Haiku 5.5 建立有版本依据的模型能力配置，补充默认值及显式控制的回归，不要只改别名。

### F3：2.1.295 没有选择原生传输配置

[gateway_claude_native.go](../backend/internal/service/gateway_claude_native.go) 第 35–37 行
仅允许已测的 2.1.292 / SDK 0.128.0 / runtime v26.3.0 / x64 组合。
2.1.295 的常规请求进入默认 Go 传输。

两版各用普通 JSON、运行时 gzip、分块 gzip，做直连 / HTTP CONNECT / HTTPS CONNECT /
SOCKS5 × 日志关 / 开的 24 组真实发送：

- 2.1.292：24/24 正文、完整头值、头序、归一化 ClientHello 一致；日志正文完整且假凭据脱敏。
- 2.1.295：24/24 正文一致，24/24 头序与 ClientHello 不一致。HTTP 头大小写也变化，
  原生显式 `Connection: keep-alive` 被省略；忽略大小写后，其余应用头值一致。
- 两版均零丢包；各 12 组日志样本字节、摘要、完整性及脱敏通过。
- 另直接核验两版各 5 条原生模型 / count_tokens 连接，非随机 ClientHello 都匹配原黄金摘要
  `8845ac2401a951ffc4acef2824c3422124c7883e0c9bc4b5f90d3c6be05da2f9`。

这证明目前没有传输等价，**不证明上游因此拒绝、识别或封禁**。
建议建立明确的 2.1.295 配置资格并继续覆盖两种 gzip 头序，不能仅因版本较新便自动套模板。

## 逐项核对矩阵

| 项目 | 源码 / 运行证据 | 2.1.292 | 2.1.295 |
|---|---|---|---|
| 二进制、嵌入模块来源 | 固定哈希、Bun 记录偏移；新版 Zstd 解压 | 已核验 | 已核验 |
| messages 原始 JSON 与 gzip 字节 | 新采集 + PCAP + 生产 Forward 四组合 | 一致 | 一致 |
| count_tokens | 实际入口解压、原生识别、ForwardCountTokens | 一致 | 一致 |
| 方法、路径、query、Content-Length、GetBody | 全部 1,192 个构建组合 | 一致 | 一致 |
| API Key / OAuth 认证选择 | 凭据不参加字节等价，认证方式单独核验 | 正确 | 正确 |
| 应用头及 beta | 同认证全量双向头比较 | 296/298 相同 | 296/298 相同 |
| 空 anthropic-version | 主动空值场景 | 补 2023-06-01，既有策略 | 同左 |
| 五模型默认参数、thinking / effort | 模型表、请求构造、显式参数捕获 | 已测五模型路径通过；见既有边界 | 同五模型路径；Haiku 5.5 见 F2 |
| Haiku、Sonnet、Opus、1m 别名 | 10 个新增版本边界场景 | Haiku 指向 4.5 | Haiku 指向 5.5 |
| Read、并行工具、图片、thinking 工具回填 | 未修改 CLI 消费合成 SSE 并发送下一轮 | 原文保留 | 原文保留 |
| structured output、metadata、resume、multi-turn | 新采集与四组合回放 | 原文保留 | 原文保留 |
| gzip 大小门槛、Unicode、等级、分块 1/2 | comprehensive / deep_native / gzip_fix | 实测采集一致 | 实测采集一致 |
| 400/401/403/408/409/429/503/529 与重试头 | SDK 源码及模拟响应 | 场景记录与报文已核对 | 场景记录与报文已核对 |
| cf-ray、request-id、JSON 解析错误恢复 | 原始 / 生产转换错误分别交给 CLI | 22 场景一致 | 22 场景一致 |
| CCH | 独立原生运行时 593 向量 + Go 比较 | 全同 | 原文保留；没有建立完整新版算法资格 |
| 归因后缀 UTF-16 | 两版精确提取函数 + 394 个向量 + Go | 全同 | 全同 |
| TLS、HTTP 头序、四种路径 | 两版各 24 组实际发送 | 一致 | F3 |
| 请求日志透明性 | 编码正文、摘要、完整性、凭据脱敏 | 12/12 通过 | 12/12 通过 |
| 连续 `/compact`、续聊、账号迁移 | 两会话 × 三模型 × 两种账号 | 完整序列通过 | 生产失败，见 F1 |
| 会话、SSE、请求/响应编码及参数回归 | service 与六个组件包的专项测试 | 通过 | 测试基线为当前代码；新版实际失败单列 |
| 成功响应头过滤 | 生产 FilterHeaders 默认 / additional_allowed | 既有显式配置差异 | 同一网关策略 |

### 总数及计数解释

- 每版 95 个新 CLI 场景、149 条 messages / count_tokens，其中 44 条 gzip；两版合计 190 场景、298 请求。
- 每条请求 × 两种账号 × passthrough 开关：每版 596、合计 **1,192 个组合，正文及 wire 全同**。
  OAuth 的 passthrough 开关不是另一套实现，不能把它算作额外独立协议覆盖。
- 每版另 22 个错误回放场景、58 请求（40 gzip）。主捕获与错误回放总计 **234 场景、414 请求**，
  所有最终 PCAP 与接收记录相符，内核丢包 0。拒绝场景正常退出非零，不等于成功生成。
- 各 108 组配置策略观察：2.1.292 为 104 个本地 200、4 个 400；2.1.295 为 96 个本地 200、12 个 400。
  400 包括旧身份策略冲突及未验证 CCH 版本的改写 / 解压保护。该保护属于现有限制，不当作新回归；
  模拟 200 不表示官方接受。
- 归因后缀 394 个唯一向量；现有 Go 测试每次读取 10 个，拆成 40 批，最后一批补 6 个重复项，
  不把补齐项算作新覆盖。CCH 的 593 个向量使用另一份修改 JS 入口的原生运行时探针，
  与未修改 CLI 场景严格分开。
- service 专项 1,663 个通过事件（含子测试及部分共用 Anthropic 路径）；六个组件包 322 个通过事件；
  CCH / 成功响应头 overlay 595 个通过事件。不把通过事件当成独立端到端场景数。

普通 API 对比仍保留之前已知的 verbose / stream `thinking.display=updates` 与默认 omitted 差异，
以及晚期 EXTRA_BODY 关闭 thinking 的温度 / context_management 差异。
原生请求保留与普通 API 构造是两种不同输入合同，不能用后者的差异否定前者的逐字节结果。
成功响应默认过滤仍会移除 request-id 和已测 Anthropic 限流头，显式 additional_allowed 可保留。

## 复现入口与留痕

工具说明见 [验证 README](../.github/claude-validation/README.md)。新增
`extract_cli_sources.py` 支持两个固定二进制的只读提取；`version_edges_lab.py` 增加十个边界场景；
`comprehensive_lab.py` 用 `CLAUDE_LAB_CLI_VERSION` 选择固定哈希，默认仍为 2.1.292。

核心复核命令：

```bash
# 在不可变快照的 backend 内编译，overlay 放入现有三份审查文件。
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=go1.27.2 \
  go test -c -overlay=/absolute/path/to/overlay.json -tags=unit \
  -o /absolute/path/to/service-audit.test ./internal/service

# Docker network-none 内，分别传入每版新捕获目录并读取生成的逐项 JSON。
CLAUDE_EXTENDED_AUDIT_INPUT=/captures \
CLAUDE_REQUEST_RECHECK_OUTPUT=/output/forwarding.json \
CLAUDE_GENERIC_AUDIT_OUTPUT=/output/generic.json \
  /opt/service-audit.test \
  -test.run='^TestNativeRequestRecheck$|^TestComprehensiveGenericParameters$'

# 原始生产代码的新版托管恢复复现；此测试预期失败，不能忽略失败日志。
CLAUDE_RECOVERY_NATIVE_CLI=/opt/claude \
CLAUDE_RECOVERY_LAB_OUTPUT=/output/recovery \
  /opt/service-audit.test -test.run='^TestClaudeAlignmentNativeCLILab$' -test.v
```

本地 `scripts/run_captures.py`、`run_forward.py`、`run_errors.py` 和
`analysis/` 保留了完整运行参数、逐项结果及日志。传输探针由冻结的生产 HTTPUpstream 编译；
选择三个已经证明正文、应用头全同的请求，再按生产实际选中的 profile 发送。
新版传输核对器保留头序 / TLS 差异，不将其断言为通过。

实验准备中曾出现路径拼接类型错误、未先解压 Zstd 的读取错误，以及一次测试名未匹配。
均在正式结论前纠正；保留已有证据，不将“零测试”算作验证通过。
最终提取器核验确认两版均有三个压缩模块，纠正中途“新版才使用压缩封装”的判断；
所有 4,600 个模块的解码字节与此前提取内容一致，字符偏移与字节偏移分别记录。
最终仓库工具另做两个版本的单场景 smoke 与 PCAP 校验，这两条不重复计入上面的主审查总数。

## 结论边界

本轮重新运行专项验证，没有重跑全仓库 unit / integration / lint；生产源码没有变化。
数据库事务等完整 integration 的历史结果仍见 CC-20261009-009，不能冒充本轮重跑。
未部署、未推送、未合并，也未访问真实模型服务。官方接受、订阅资格、签名和计费未验证。
真实 thinking 签名用合成数据代替，HTTP/2、TLS 恢复、ARM、Windows、全部远程功能开关以及
CLI UI / hooks / 插件行为不在本轮等价声明中。

优先顺序为 F1 托管压缩兼容、F2 模型转换、F3 新版本传输资格；修复应另留同 PR 的证据记录。
本报告保留 CC-20261009-008 / 009 的历史结论，确认 009 的三项修复在本轮错误回放中仍有效。
