# Claude 改动记录

本文件记录 skingford/sub2api 的 Claude 相关改动。已发布记录保留，后续纠正以新条目关联。
详细实验范围见 [请求对齐报告](claude-code-request-parity.md)。

## CC-20261003-001：保留原生 CLI 请求能力

- 上游基线：`b8dece9000c68815a5b867ca5a1e6f236e173905`，v0.2.13。
- 样本：Claude Code 2.1.286 / darwin-x64，本地 HTTP 模拟服务、虚构 API Key，共 6 条合成报文。
- 原因：新消息级控制 beta 组合被当成不支持，控制消息遭删除；thinking 活跃时还会补入缺省 temperature。
- 改动：保留原生控制消息与温度缺省；新增条件请求头透传；safeguards 缺对应 beta 时返回明确错误。
- 范围：`gateway_request.go`、`gateway_claude_oauth_body.go`、请求构建器、header 工具及抓包回归样本。
- 验证：复现原有 12 个抓包转发失败；修复后完整后端 unit / integration 通过，golangci-lint 0 issues。
- 提交：`a12d2c6c`；验证文档 `224f659b`；[PR #1](https://github.com/skingford/sub2api/pull/1)。
- 限制：没有真实上游接受或计费验证。关于请求 ID 的最初判断由下一条记录纠正。

## CC-20261003-002：按目标地址补请求关联 ID

- 基线：上一条改动；继续分析 2.1.286 解包代码。
- 原因：localhost 捕获不覆盖官方地址分支，不能据此全局删除请求 ID。
- 改动：四个 Anthropic messages / count_tokens 构建入口仅在官方 HTTPS origin 缺失时补请求 ID，保留已有值及账号覆盖限制；纠正 cch 全面取消等缺少证据的注释。
- 范围：`gateway_anthropic_request_id.go`、四个构建器及对应回归测试。
- 验证：11 个原始函数隔离用例、完整后端 unit / integration 通过，golangci-lint 0 issues。
- 提交：`d66ddafb`；验证文档 `42864360`；[PR #1](https://github.com/skingford/sub2api/pull/1)。
- 限制：关联 ID 不证明官方客户端身份或订阅资格；客户端代码不能还原服务端封禁规则。

## BASE-20261007：先同步上游，再继续修改

- fork 的 `main` 从 `b8dece90` 快进到 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，v0.2.14，共 8 个上游提交。
- 工作分支通过 `e475db32` 合并该基线，无冲突；已有 Claude 提交保留。
- 此次上游更新涉及安装初始化、支付回调、模型发现和前端依赖，没有修改本轮涉及的 Claude 请求构建文件。
- `main` 和工作分支均已推送。后续验证以该基线为准。

## CC-20261007-001：2.1.291 报文回归与缓存诊断能力

- 状态：代码与验证完成。
- 基线：`3f1a2ea0`，工作分支包含合并提交 `e475db32`。
- 样本：官方 CLI 2.1.291 / Linux x64 与 macOS x64；假 Key、本地 HTTPS；Linux 保留官方域名并在断网容器内映射到回环地址。
- 依据：Linux 报文带 `cache-diagnosis-2026-04-07` 与 `diagnostics.previous_message_id`；源码使用同一能力标记决定是否构造诊断字段。
- 改动：按最终 beta 保留或清理 diagnostics；兼容分支保留调用方显式请求的 cache-diagnosis beta，不默认开启；新增两份原始正文逐字节转发回归。
- 文件：`internal/pkg/claude/constants.go`、`gateway_request.go`、`gateway_upstream_request.go`、`gateway_claude_2291_parity_test.go` 和 `testdata/claude_code_2_1_291/`。
- 验证：修复前复现能力过滤和兼容分支失败；修复后针对性回归、完整后端 unit / integration 均通过，golangci-lint 0 issues。两份新样本的 4 个认证分支组合正文逐字节一致。
- 提交：`b39704bb`，包含 `Claude-Change-ID: CC-20261007-001`；[PR #1](https://github.com/skingford/sub2api/pull/1)。
- 限制：本地模拟结果不等于真实订阅 OAuth、官方计费、服务端识别或封号验证。

## CC-20261007-002：官方 latest 2.1.292 升级对照

- 状态：隔离实验、针对性回归及最终全量检查完成。
- 基线：上游 `3f1a2ea0`，包含前一条代码提交 `b39704bb`。
- 版本：官方分发指针 latest=2.1.292、stable=2.1.285；本机入口检测时已为 2.1.292，已核对官方 manifest 哈希并完成 macOS 沙箱验证。
- 证据：使用相同模型、提示词、权限模式、假 Key 和 TLS 模拟器，对比 2.1.291 / 2.1.292 的 Linux 第一方 origin 和 macOS 回环请求。
- 结果：请求头顺序、beta 和正文业务字段未发现变化；原始差异包括版本归因、cch 取值、随机设备 / 会话 / prompt / request ID，以及 macOS 端口。排除这些明确列出的变化后，两种平台的请求均相等。此结论不证明 cch 算法不变或所有场景不变。
- TLS：两种密钥日志环境变量仍不生效，直接参数仍不被识别；Linux 服务器密钥和 macOS Node.js 对照均通过。
- 采集修正：首轮 Linux PCAP 不完整且对照校验失败；启用 tcpdump 即时采集后重跑，正式样本通过完整请求解密与哈希核对。没有使用失败的 PCAP 宣称协议变化。
- 文件：复用转发断言的测试辅助函数、新增 `testdata/claude_code_2_1_292/`；生产请求构建逻辑没有新增变更。
- 验证：包括最新样本的完整后端 unit（57 个包）、integration（51 个包）均通过；golangci-lint 0 issues。2.1.291 / 2.1.292 合计 8 个转发组合正文逐字节一致。
- 提交：`42ffc0f6`，包含 `Claude-Change-ID: CC-20261007-002`；[PR #1](https://github.com/skingford/sub2api/pull/1)。

## CC-20261007-003：建立 release 维护分支与个人 README

- 原因：维护者要求 fork 使用独立维护主分支，并保留后续 Claude 相关改动记录。
- 分支基线：从包含全部已验证改动的 `0ba43bc5` 建立 `release`；`main` 保留在上游 v0.2.14 / `3f1a2ea0`。
- 规则：`release` 作为 fork 默认维护分支，后续主题分支和常规 PR 以它为基准；历史 PR #1 保留相对上游的草稿差异记录。
- 文件：README 改为 skingford 维护版说明，列出维护范围、证据、验证状态和源码镜像部署步骤，保留上游署名及 LGPL 许可证；开发指南和本地 agent 指令记录分支约定。
- 验证：README 本地链接、Compose 镜像覆盖配置及 Git 差异检查；本条没有修改运行时代码，沿用前两条已完成的后端全量验证。
- 提交关联：本条提交携带 `Claude-Change-ID: CC-20261007-003`，可通过 Git 日志定位。首次维护分支按维护者指示直接建立，不创建额外 PR。

## CC-20261007-004：保护 release 维护历史

- 原因：维护者指出 GitHub 提示 release 尚未受保护，需要防止维护历史被强推覆盖或分支被删除。
- GitHub 配置：规则集 `release-maintenance-protection`，ID `24660893`，active，仅匹配 `refs/heads/release`，规则为 `non_fast_forward` 和 `deletion`，无绕过名单。
- 范围：服务端保护设置、`.github/rulesets/release-protection.json`、README 和开发指南；没有修改运行时代码。
- CI：配置时工作流已启用，但 release 没有运行记录；本轮未增加必需状态检查、审批人数或普通推送限制。
- 验证：读取 GitHub 的分支生效规则，确认两条规则均来自该规则集，`release.protected=true`。没有通过实际强推或删除来测试。
- 提交关联：`Claude-Change-ID: CC-20261007-004`；[查看保护规则](https://github.com/skingford/sub2api/rules/24660893)。

## CC-20261007-005：main 定时同步，release 保留人工合并

- 原因：维护者要求 `main` 定时同步上游，再由人工决定何时合并到 `release`。
- 基线：配置时 fork 与上游 `main` 均为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，双方独有提交数均为 0；本轮无需更新 `main`。
- 计划：Codex 会话任务 `sync-sub2api-main-from-upstream`，ACTIVE，每天 09:00（Asia/Shanghai），依赖维护者电脑开机及应用运行。
- 行为：核对远端、获取 `main`、验证祖先关系、仅普通快进推送并复查；相同则不操作，分叉或失败则停止并提示。任务保留工作区和本地分支，不自动更新 `release`。
- 留痕：自动同步结果记录前后提交及 Claude 路径变动；人工合入 `release` 时再更新本记录中的基线和验证结果。自动同步不会在 `main` 或 `release` 添加记录提交。
- 文件：README、开发指南和本地 agent 指令记下维护约定。本条不修改 Claude 运行时代码、协议版本或现有抓包结论。
- 验证：重新获取双方远端提交，确认 `git rev-list --left-right --count origin/main...upstream/main` 为 `0 0`；创建后读取任务配置确认 ACTIVE、每日计划及当前会话绑定。本轮验证了无更新分支，首次计划触发和有新提交时的实际推送尚未发生。
- 提交关联：`Claude-Change-ID: CC-20261007-005`；后续人工合并按上一节规则单独留痕。

## CC-20261007-006：改用 GitHub 云端定时同步

- 关联：替代 CC-20261007-005 的本机调度方式；保留其历史记录。维护者要求不依赖本地电脑或 Codex 运行。
- 基线：配置开始时 fork 与上游 `main` 均为 `3f1a2ea0`，`release` 为 `51c60017`。
- 调度：原 Codex 任务已暂停；新增 `Sync upstream main` GitHub Actions 工作流，每天 UTC 01:00（北京时间 09:00），支持手动触发。
- 范围：工作流和脚本保存在默认维护分支 `release`；在临时裸仓库检查祖先关系，只普通快进推送已检查的 SHA 到 fork 的 `main`。不自动合并 `release`、不执行上游代码、不调用 Claude 服务。
- 留痕：Actions 运行日志与摘要记录前后提交、提交数及变动文件；人工同步 `release` 时继续记录基线和 Claude 改动的验证。
- 文件：`.github/workflows/sync-upstream.yml`、`.github/sync-tools/sync-upstream-main.sh`、`.github/sync-tools/test_sync_upstream.py`、README、开发指南和本地 agent 指令。
- 验证：shell 语法及工作流结构检查通过；5 个临时 Git 仓库测试通过，覆盖无更新、快进、fork 领先、分叉和推送被拒绝。各场景均检查 `release` 提交保持不变。云端首次运行结果在执行后补记。
- 限制：GitHub 调度可能延迟或因长期无活动停用；上游工作流文件更新若被默认令牌权限拒绝，需要按开发指南配置受限的同步令牌。
- 提交关联：`Claude-Change-ID: CC-20261007-006`；本条不修改 Claude 运行时代码或现有抓包结论。
- 云端验证补记：实现提交 `2d925595` 已推送到 `release`；[GitHub Actions 运行 37646923456](https://github.com/skingford/sub2api/actions/runs/37646923456) 成功，5 个测试全部通过。实际同步步骤确认双方 `main` 均为 `3f1a2ea0`，执行无更新分支，没有写入任何分支。
- 调度状态补记：GitHub 工作流 ID `377584102` 为 active；本机 Codex 任务配置已核对为 PAUSED。实际有新提交时的 GitHub 推送及首次定时触发尚未发生；临时 Git 仓库已验证快进推送和失败保护。

## CC-20261008-001：实际身份服务、工具与重试报文验证

- 基线：上游 `3f1a2ea0`；从 `release / f6b76203` 建立 `codex/claude-2292-validation`，没有自动合并上游到 release。
- 版本：官方 Linux x64 CLI 2.1.292，SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`；SDK 头 0.128.0。Docker 断网、假 API Key / OAuth 环境变量、本地 TLS 模拟器。
- 样本：12 条原生模型或计数请求，覆盖 Read 回填、503 重试、两轮对话、假 OAuth、Unicode 与 `/context`；全部正文与正式 PCAP 解密字节核对一致，正式捕获无内核丢包。
- 复现：2 个转发组合丢弃 `anthropic-dispatch-id`；18 个身份配置组合在 CLI 版本未变时仍重算原生归因后缀，破坏 Unicode 和带自动插入提示块的请求。
- 修复：白名单透传原生重试关联头；billing 版本已经相同时不再重算归因。纠正仍宣称“新版取消 cch”或全面字节对齐的旧注释；不补造 cch，不扩大真实订阅验证结论。
- 范围：请求头白名单、header casing、billing 版本同步、抓包回归、身份服务矩阵、隔离采集与传输对照脚本、请求报告。
- 验证：24 个原生转发组合、180 个身份配置组合（每组重复两次）通过；完整后端 unit / integration 均通过，golangci-lint 0 issues。token 计数样本替换为无丢包捕获后，新增报文与身份矩阵再次通过。
- 发现的边界：默认 OAuth metadata 重写和同版本账号缓存仍会产生明确差异；两种 Go 传输与原生 CLI 的 TLS 握手、HTTP 头顺序不同。未调整生产设置、旧兼容身份模板或 TLS 模板，未访问真实官方模型服务。
- 提交关联：`Claude-Change-ID: CC-20261008-001`；详细结果见 [请求对齐报告](claude-code-request-parity.md#2026-10-08启用身份服务及扩展场景验证)。
- 实现提交：`9535cf86`；[PR #2](https://github.com/skingford/sub2api/pull/2)，目标为 `release`，保留为待审阅草稿。
- 复现补记：已用仓库 Dockerfile 构建镜像并完整复跑 7 个 CLI 场景，12 条请求的原始字节再次通过 PCAP 校验。完整 unit 为 57 个包，integration 为 51 个包，lint 0 issues。原始证据保存在维护者本机 `claude-capture/validation-2.1.292-20261008/`，仓库保存合成报文、来源哈希、核对摘要和复现代码。

## CC-20261008-002：原生保真、UTF-16 归因与传输配置

- 基线：在 PR #2 的 `36f380d7` 上继续；上游仍为 `3f1a2ea0`，release 未自动合并。
- 版本与证据：CLI 2.1.292 / SDK 0.128.0；Linux x64 断网容器与 macOS x64 沙箱、本地 CONNECT 模拟器。只读提取 bundle 中的 Rk / dne，保留文件和函数哈希及 10 个原函数向量。
- 行为：原生请求默认跳过账号身份缓存、metadata / 会话重写和旧正文整流，提供单账号回退开关；带 cch 的原生正文若被模型或 beta 策略修改则本地返回 400。凭证、计费和能力策略继续由原有路径处理。
- 算法：修复 UTF-8 字节索引与 JavaScript UTF-16 索引差异；原生归因和上游 thinking 签名不重算。cch 最终算法尚未恢复，不生成猜测值。
- 传输：新增严格限定版本 / 平台的 TLS 配置和 HTTP/1.1 头序；直连、HTTP / HTTPS CONNECT、SOCKS5 均保持代理路径。TLS 模板内容进入客户端缓存键，并冻结模板副本，防止复用旧配置。
- 验证：四条传输路径的正文、头值、头序及归一化 ClientHello 与原生样本一致；macOS 模型和计数请求补证一致；永久回归覆盖原生身份、模板缓存、连接复用、取消、代理证书校验、算法向量和握手摘要。最终全量结果在完成后补记。
- 范围：请求构建与正文策略、传输适配器、TLS dialer/profile、fhttp 依赖、测试、抓包脚本与文档。非原生兼容构造、未知版本和真实账号风控不在等价结论内。
- 提交关联：`Claude-Change-ID: CC-20261008-002`，继续更新 [PR #2](https://github.com/skingford/sub2api/pull/2)；证据与边界见 [请求报告](claude-code-request-parity.md#2026-10-08原生保真归因算法与传输修复)。
- 安全依赖：govulncheck 发现 fhttp 的传递依赖 CIRCL 1.6.2 命中 GO-2026-4550；升级为 1.6.5 后，无当前代码可达漏洞，相关回归再次通过。
- 全量验证补记：unit 57 个包、integration 51 个包通过。原始提取、macOS 沙箱、传输与扫描记录保存在维护者本机 `claude-capture/native-parity-20261008/`；仓库保留来源哈希、原函数向量、传输核对摘要和自动回归。
- 最终检查：golangci-lint 0 issues；CIRCL 1.6.5 的相关服务、传输与握手回归通过，govulncheck 无当前代码可达漏洞。保留扫描发现和修复记录，不将其写成全依赖树不存在任何已知问题。
- 实现提交：`c0d89305`，已推送至 PR #2；依赖修复后的四条传输路径重新通过完整核对，摘要注明 `proxy-results-final` 及对应依赖版本。

## CC-20261008-003：恢复 2.1.292 原生 cch 计算

- 基线：PR #2 的 `288737c2`；上游 `3f1a2ea0`。工作保留在主题分支，release 未自动合并。
- 关联：补充 CC-20261008-002 尚未恢复的 cch。已有历史报告和抓包不覆盖改写。
- 源码依据：两平台 2.1.292 二进制定位 seed `0x4D659218E32A3268`，反汇编确认原始字节扫描、字段省略和 xxHash64 低 20 位写回。
- 验证：81 个断网原生运行时探针向量，另核对历史 10 条与新采集 9 条 Linux 带 cch 请求，并补证 1 条 macOS 原生 cch（合计 20 条未修改 CLI 请求）。新一轮未修改 CLI 共 12 条模型 / 计数请求、7 个场景；接收端与 PCAP 原始字节一致，无内核丢包。
- 行为：原生 2.1.292 第一方 messages / count_tokens 在全部正文策略和最终头覆盖之后计算；补回自定义 base URL 客户端省略的 cch；更新 Body、Content-Length 和 GetBody。未知版本继续完整性保护，不支持的 billing 布局本地拒绝。
- 范围：新增 `internal/pkg/claude/cch.go`、原生请求最终处理、四个构建入口、81 个独立向量、隔离探针复现脚本和报告。复用已有 xxhash 依赖。
- 回归修正：历史 macOS 回环样本转发到第一方地址时应新增 cch，独立运行时补证为 `80254`；保留原始抓包，另存第一方预期正文并更新旧断言。
- 证据与边界：[cch 专项报告](claude-code-cch-2.1.292.md)。运行时探针明确标注入口经过修改，不冒充未修改 CLI；全部 CLI 实验使用假凭证和本地服务，不验证真实订阅、官方接受或封禁规则。
- 提交关联：`Claude-Change-ID: CC-20261008-003`，继续更新 [PR #2](https://github.com/skingford/sub2api/pull/2)。最终全量结果见下方补记。

- 隔离复现补记：使用仓库脚本重新生成探针并复跑 81 条向量，接收端、PCAP 与已提交预期全部一致，丢包 0。macOS 第一方原生样本复算为 `0b0ea`，与接收记录一致。
- 集成验证补记：首次全量运行的 repository 包因 Redis 测试容器启动超时失败；其余 50 个包通过。该包单独重跑通过（144.551 秒），合计 51 个包通过。golangci-lint 0 issues。
- 最终单元验证：完整 unit 57 个包通过，service 包 206.919 秒；六个实际构建组合和所有原生 / 旧兼容回归均通过。实验与检查日志保存在本机 `claude-capture/cch-investigation-20261008/analysis/`。
- 实现提交：[efcfbc3b](https://github.com/skingford/sub2api/commit/efcfbc3b753ce391ee11d29a001bfc1be586e7d7)，已推送至 PR #2；release 保持待人工合并。

## CC-20261008-004：合入 release 并整理维护文档

- 授权：维护者要求将已验证改动合入 release 并归档文档。
- 合并：[PR #2](https://github.com/skingford/sub2api/pull/2) 于 2026-10-08 11:03:26（Asia/Shanghai）合并；release 从 `f6b76203` 更新为 `81b7e5cec78e38c3bbca282cbc82822d54c76005`，保留主题分支全部原始提交。
- 范围：纳入 CC-20261008-001、002、003，包括请求与重试头保留、身份缓存绕过、UTF-16 归因、限定版 TLS / HTTP 头顺序、cch 最终正文计算及实验资产。
- 验证基线：合并前 PR head 为 `45424008431bd27b5d0ce53c7df7523211dbe87b`；GitHub CI 的后端测试、前端、lint、shell、release helpers 及前后端安全扫描全部成功。CLA 工作流标为 skipped。
- 云端证据：[CI](https://github.com/skingford/sub2api/actions/runs/37719057674)、[Security Scan](https://github.com/skingford/sub2api/actions/runs/37719057805)。本地 unit 57 个包、integration 51 个包、lint 0 issues 的结果沿用前条记录，包含 Redis 启动超时后的成功重跑。
- 合并校验：release 合并提交的文件树与上述已验证 head 完全一致；本条后续仅整理 README、两份请求文档和改动记录，检查链接与差异，不重复执行模型请求或全量代码测试。
- 分支约定：main 继续跟踪上游；本次没有合并历史 PR #1、发布 tag / GitHub Release 或部署运行服务。
- 知识库：同步维护者的 `wiki/逆向工程/`，新增原生算法与 release 对齐记录，级联更新请求参数、隔离实验和审计边界。
- 提交关联：本条使用 `Claude-Change-ID: CC-20261008-004`；文档后续 PR 以 release 为目标。历史“待合并”记录由本条更新，不删除原记录。

## CC-20261008-005：已合并版本的剩余差异审查

- 基线：release `ee2f9fea3cacee7380dc280fb549efa7db4b0cc9`，CLI 2.1.292，原上游基线 `3f1a2ea0`。
- 范围：本轮只补审查报告与证据摘要，没有修改生产实现或依赖，也没有再次合并 release。
- 方法：Go overlay 调用生产 service / repository；断网 Docker 补跑未修改 CLI 的 OAuth count 和 403；原生运行时探针对照响应解压。全部使用假凭证和回环服务。
- 结果：27 个原生转发组合正文一致；发现 count_tokens 多补 timeout 头、zlib 封装 deflate 解码失败、旧版普通 API 模板及转换后分类、账号 UUID 语义、确定性会话 ID、重试与请求 ID、压缩能力缺省值等差异。
- 边界：区分真实差异、认证路线的必需变化和未验证场景；没有把差异写成已证明的封禁原因。此前特定重放样本的 cch / TLS / 头序结果保留。
- 验证：service / repository 审查采集通过；原生 OAuth count 和 403 的 PCAP 正文逐字节一致、丢包 0；文档链接与 JSON 校验。没有为文档变更重复运行全量测试。
- 证据：[剩余差异报告](claude-code-remaining-differences.md)、[机器可读摘要](claude-2292-gap-audit.json)；本机 `claude-capture/remaining-gaps-20261008/` 保存测试源码、输出与原生抓包。
- 提交关联：`Claude-Change-ID: CC-20261008-005`，审查文档 PR 目标为 release，保持待后续修复评审。

## CC-20261008-006：同步修复审查发现的差异

- 基线：release `ee2f9fea`，接续审查提交 `fe6c5a85` / CC-20261008-005；在 PR #4 中继续，保留审查历史。
- 版本：固定 2.1.292；官方 Linux x64 二进制 SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。补跑 Sonnet 4.6、Opus 4.6、Haiku 4.5 的假 OAuth 场景。
- 请求：升级完整默认头组合，转换前固定来源、请求内固定版本；按三种已测模型补默认值，保留显式控制；原生请求不补可选缺省头，计数请求完整头集合回归。
- 身份：随机会话 UUID 与显式续聊头，独立账号绑定与租户隔离路由键；已知账号 UUID 冲突本地拒绝。生成后的 metadata 不会反过来变成原生输入判据。
- 错误：原生和新转换路径采用单次上游发送，调用方管理重试；保持拒绝状态和重试信号、用量、账号状态记录及错误脱敏。
- 传输：缺省压缩声明补 zstd；修复 zlib / raw deflate 解码，新增错误体、SSE、损坏流和连接复用检查。
- 配置：普通转换只接受有完整实测配置的版本；历史版本设置可读取。没有共享缓存的独立组件不能保证跨请求账号绑定。
- 文件：网关请求与协议适配入口、会话路由、响应解码、模型与头配置、回归和样本、维护文档。没有新增依赖或执行真实模型请求。
- 验证进度：专项回归及三模型 PCAP 校验通过；全量测试、lint 和最终传输对照完成后补记。旧断言按新原生证据与单次发送约定更新，不删除历史原始记录。
- 关联：[修复与调用约定](claude-code-gap-fixes.md)，`Claude-Change-ID: CC-20261008-006`，继续 [PR #4](https://github.com/skingford/sub2api/pull/4)。

- 最终验证：完整 unit 57 个包、integration 51 个包通过；golangci-lint 0 issues。生产代码最后修改时间早于本轮全量检查启动时间；没有以旧检查冒充修改后的结果。
- 发送复验：从生产请求构建器导出三个模型的普通 API → OAuth 报文，交给原生 Bun 运行时重算 cch，三份最终正文逐字节一致。自动传输选择下，直连、HTTP / HTTPS CONNECT、SOCKS5 四条路径的正文、完整头值、头序和 ClientHello 非随机部分均与对应原生运行时结果一致，PCAP 校验通过且丢包为 0。
- 证据：[最终传输对照](../backend/internal/service/testdata/claude_code_2_1_292/gap-fix-wire-verification.json)、[模型 / 计数来源](../backend/internal/service/testdata/claude_code_2_1_292/gap-fix-provenance.json)；本机 `claude-capture/gap-fixes-20261008/` 保存构建器导出、原生运行时探针、PCAP 与检查日志。运行时探针修改 JS 入口，未冒充完整 CLI 产品流程。

## CC-20261008-007：会话账号归属改为持久、原子且不可迁移

- 基线：PR #4 已发布提交 `3d7d660b`，release `ee2f9fea`；上游仍为 `3f1a2ea0`。接续 CC-20261008-006 的缓存绑定实现，保留原记录。
- 原因：维护者要求同一 session 只能由同一账号处理。原来的 Get/Set 与一小时 TTL 无法防止并发首次绑定和缓存失效后改绑，原生透传也缺少同等保护。
- 版本 / 范围：Claude Code 2.1.292；Anthropic OAuth、API Key 和已带 session 的原生请求，覆盖消息、计数及两种 API 适配入口。此约束是网关会话策略，不冒充官方服务端机制。
- 实现：PostgreSQL UUID 唯一约束原子登记账号，永久保留归属；调度只接受原账号，发送前再次校验。无数据库时拒绝发送；缓存删除、API Key / 分组变化、账号删除或冷却均不授权改绑。原始 metadata 与出站会话头冲突时拒绝请求。两种协议适配器在转换前保存原始 ID，网关续聊头也传递到实际出站会话头。原子绑定竞争失败时释放并发和活跃会话槽。
- 文件：迁移 `242_claude_session_ownership.sql`、账号仓储会话实现、服务会话守卫、调度过滤、请求构建器及单元 / PostgreSQL 并发回归。
- 边界：没有 session 标识不能推断跨轮关系；升级前过期的 Redis 记录无法补回历史，数据库丢失 / 回滚也需要恢复原记录。部署时统一切换版本并新建会话。
- 验证：完整 unit 57 个包、integration 51 个包通过；全量 lint 0 issues。最后追加绑定竞争失败的槽位清理后，会话 / 身份设置 / 请求契约专项再次通过；SQL 最终版本的 32 路并发及持久性检查再次通过。最终受影响包 lint 复验 0 issues；[结构化验证记录](claude-session-validation.json) 保存命令对应日志摘要与最终源码 SHA-256。
- 回归说明：旧身份改写中改变 session ID 的组合改为验证本地拒绝；计数接口检查会话头，不强制添加原生不存在的 metadata。首次全量集成检查出现本地 Redis 测试容器启动失败，后续完整重跑通过；测试夹具补上持久会话存储模拟，并修正作用域以同时支持 unit / integration / lint。全部验证使用本地模拟服务 / 数据库，不执行真实模型请求。
- 关联：[调用与部署约定](claude-code-gap-fixes.md#账号绑定)，[PR #4](https://github.com/skingford/sub2api/pull/4)，提交使用 `Claude-Change-ID: CC-20261008-007`。

## CC-20261008-008：托管恢复、双层会话与隔离

- 授权：维护者要求完整实现检查点与受控迁移，强调不同 session_id 的内容、身份和状态不能串传。
- 基线：release `b4430850844263e3fd3d2d180f514099bffad04e`；上游 `3f1a2ea0`。新分支 `codex/claude-managed-recovery`，目标 release。
- 版本：默认严格模式不变；托管模式仅接受已验证的 CLI 2.1.292 与三个已覆盖模型。
- 实现：认证用户 / 分组 / 客户端 UUID 隔离逻辑对话，独立上游 UUID 永久绑定账号；数据库处理权、代次和发送前状态转换阻止串传与不确定重放。固定恢复前缀，核对真实响应、消息边界及 thinking 来源。
- 检查点：AES-GCM 绑定身份和用途；后台持久队列、增量摘要、原文引用校验、用户指令与内联附件保留；过期删除内容，保留归属 / 操作墓碑。摘要费用与未知用量独立记录并设日调用上限。
- 协议：Messages、Chat Completions、Responses 和计数入口接入；恢复后对同一账号 / session 的实际正文计数；身份、归因与最终 cch 一起更新。未验证层次、无法匹配历史、未闭合工具回合及不确定响应均停止自动迁移。
- 文件：配置、迁移 243、事务仓储、恢复服务、协议入口 / 请求构建、响应记录、摘要客户端、测试与文档。
- 验证：完整 unit 57 个包、integration 51 个包通过；完整 / 受影响包 lint 0 issues。最后的保留清理启动、幂等键哈希及审计入口调整后，会话与审计专项再次通过。
- 原生实验：未修改的 Linux x64 2.1.292，Docker --network none；API Key / OAuth 各两条独立 CLI 对话，合计 8 次生成、4 次计数。客户端 ID 保持，上游 ID 分别更新，跨会话标记混入为 0；实验使用测试存储端口，PostgreSQL 行为单独在真实容器验证。
- 修正记录：首次原生 OAuth 实验的模拟上游用 Header.Get 读取原始小写头，导致断言错误，改用大小写兼容访问后通过；入口包装导致既有静态审计测试找不到原函数，改为原入口 defer 完成处理并增加恢复准备的审计顺序断言。没有修改旧测试以跳过保护。
- 证据：[验证摘要](claude-managed-recovery-validation.json)，本机 claude-capture/managed-recovery-20261008/ 保存日志和原生实验。没有真实模型请求、没有启用生产配置。
- 说明：[托管恢复配置与边界](claude-managed-recovery.md)。提交使用 `Claude-Change-ID: CC-20261008-008`，不代表已部署或真实服务端接受。

## CC-20261008-009：扩展 CLI 流程与剩余差异复核

- 基线：当前主题分支 `93275632a6a66dfc2648e9504a7d16bcf32da5d7`；release `b4430850`，上游 `3f1a2ea0`。接续 006、007、008，不覆盖或改写历史结论。
- 版本：未修改的官方 Linux x64 CLI 2.1.292，SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`；SDK 0.128.0。Docker 断网、独立配置、假凭证及回环 TLS 模拟器。
- 方法：14 个场景、21 条模型请求，PCAP 原始字节核对一致且丢包 0；通过 Go overlay 调用不变的生产 Forward 和恢复服务，持久层及网络边界使用测试端口。
- 结果：60 个转发组合正文相同；新确认两个 x-cc 压缩头丢失、显式 thinking.display=updates 与被过滤的 beta 不配套、托管接受 compact 后拒绝续聊、5.5 模型配置覆盖不足。额外 metadata 是托管明确限制；普通输出和 verbose 的 thinking 默认差异单独记录，不误判默认 omitted 为错误。
- 正向补证：并行 Read、图片回填、合成 thinking 工具回合和简单 resume 通过生产托管历史核对。结构化输出场景未证明 schema 完成；没有真实签名、订阅、官方接受或风控验证。
- 文件：新增扩展 Docker 采集器、审查 overlay、复现说明、[报告](claude-code-extended-audit.md)与[证据摘要](claude-extended-audit-20261008.json)。生产实现和依赖未修改。
- 验证：审查、普通 profile、显式 beta 专项通过，Python 语法 / Go 格式及文档差异检查；未重复全量 unit / integration / lint。原始材料保存在本机 `claude-capture/extended-audit-20261008/`。
- 提交 / PR：本轮尚未提交、推送或创建新 PR；当前审查对象的托管恢复实现关联 PR #5。本记录后续提交使用 `Claude-Change-ID: CC-20261008-009`。

## CC-20261008-011：固定快照的请求参数与算法复核

- 基线：HEAD `93275632a6a66dfc2648e9504a7d16bcf32da5d7`、release `b4430850`、上游 `3f1a2ea0`；2026-10-08 22:25:34（Asia/Shanghai）保存完整源码快照，包含当时未提交的部分修复。后续工作区修改不属于本次验收对象。
- 版本 / 证据：未修改官方 CLI 2.1.292 / SDK 0.128.0 / Linux x64，二进制 SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`；17 个隔离场景、27 条模型 / 计数请求，PCAP 字节一致且丢包 0。
- 结果：108 个生产转发组合正文、方法、路径与 query、长度及 GetBody 一致；同认证类型 54 个组合应用头一致，跨认证增加 OAuth beta 单列。新捕获 24 条 cch 复算、81 个既有原生运行时向量和 10 个归因原函数向量通过；20 个原生连接的非随机 ClientHello 匹配 Go 黄金摘要。
- 差异：快照的两个压缩头、显式 thinking beta 已修复；快照仍缺普通 5.5 默认配置并在托管 compact 后拒绝续聊。随后针对这些行为的新修改未纳入本轮，不以旧快照判定新实现失败。
- 方法修正：最初计数审查漏设 handler 原生校验上下文，导致假 UA 差异；采集器使用生产校验器和上下文后对全部场景重跑，最终无该差异。保留初始记录。
- 文件：新增 `.github/claude-validation/request_recheck_test.go`、`verify_native_hello.py`，补充验证 README、[复核报告](claude-request-recheck-20261008.md)和[结构化证据](claude-request-recheck-20261008.json)，在 `.gitignore` 放行这两份报告。本轮没有修改生产实现。
- 验证：固定快照 4 个包专项通过，394 个通过事件（含子测试）；两阶段采集与重放、Python 语法、Go 格式、文档差异检查。未重复全量 unit / integration / lint，未验证真实服务端接受、权限或计费。
- 提交 / PR：未提交、推送或创建新 PR，审查基线关联 PR #5；后续提交使用 `Claude-Change-ID: CC-20261008-011`。原始证据及完整快照保存在本机 `claude-capture/request-recheck-20261008-c0q5bkcy/`；保留 CC-20261008-009 历史记录。

## CC-20261008-010：修复扩展流程差异并支持已验证原生压缩

- 授权：维护者要求完整修复 CC-20261008-009 确认的差异；在 `codex/claude-managed-recovery` 的 `93275632` 上继续，release `b4430850`、上游 `3f1a2ea0` 未更新。编号 011 是另一轮固定快照审查，不作为本次最终修复验收。
- 版本：未修改官方 Linux x64 CLI 2.1.292 / SDK 0.128.0，SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。全部实验使用断网 Docker、独立配置、假凭证和本地服务。
- 转发：补齐两个 x-cc 压缩头及原始大小写；显式 thinking.display=updates 配套 beta，并在管理员过滤后按原生方式回退 omitted；保留实测旧版 fallback beta 对应的原生字段，不默认生成 fallback。
- 模型与 metadata：补充 Sonnet / Opus 5.5 的完整默认参数和 beta 配置，纳入托管范围；修改上游身份时保留普通扩展字段，继续拒绝 parent_session_id / tk 等分支身份。5.5 消息级控制字段进入历史哈希。
- 压缩：验证摘要输入对应本会话历史前缀，记录完整摘要响应，再核对压缩后前缀与保留回复；事务清除旧检查点、取消旧摘要租约并清理过期恢复前缀。不同用户 / 分组 / session 的材料不能混用，旧 UUID 不改绑。
- 实时补充：自定义 API 地址的 CLI 会省略压缩分类头。新增固定版本完整指令形态与已记录历史的联合识别；同时保留真实请求 / 回复的普通续聊分支，避免把用户引用该指令误判成必须压缩。
- 样本：新补 34 条 5.5 模型 / 计数请求，PCAP 字节一致、丢包 0；连同既有 15 条扩展样本，共 196 个转发配置回归。连续压缩与历史篡改、显示策略、模型默认值和 metadata 专项通过。
- 持久与实时验证：PostgreSQL 检查旧摘要不能回写、恢复前缀原子清理及不可改绑；未修改 CLI 直接调用生产恢复 / Forward 服务，三个模型 × 两种上游认证 × 两条会话共 72 次生成、12 次计数，连续压缩后迁移通过，跨会话混入 0。实时实验采用测试存储 / 上游，数据库单独验证，不冒充完整部署。
- 范围：请求头、beta / 模型默认值、托管历史与事务保存、原生实验、永久回归、样本与说明。无新增依赖或数据库迁移。详见 [修复报告](claude-alignment-fixes.md)；原始材料在本机 `claude-capture/alignment-fixes-20261008/`。
- 验证过程：首次实时实验暴露自定义 origin 的缺头分支，保留失败记录后完成修复；测试清理顺序及模拟响应 ID 同时修正。系统默认 lint 因构建 Go 版本过旧无法运行，改用已有 Go 1.27 兼容工具。最终全量结果见下方补记。
- 提交 / PR：本轮尚未提交、推送或合并；原实现关联 PR #5，后续提交使用 `Claude-Change-ID: CC-20261008-010`。未部署、未访问真实官方模型服务，不代表真实签名、订阅或风控验证。

- 最终补记：完整 unit 57 个包、integration 51 个包通过，golangci-lint 2.13.0 / Go 1.27.0 检查 0 issues。首次全量唯一失败为旧 Opus 5.5 断言要求省略 effort；依据新原生样本改为 medium，保留全部签名历史断言后完整重跑通过。
- 迁移后补证：实时矩阵进一步覆盖迁移后再次压缩，最终 12 条会话、96 次生成、24 次计数全部通过，跨会话混入 0。最终检查期间 3170 个后端源码 / 测试 / SQL 文件哈希保持一致；[验证与源码记录](claude-alignment-validation.json)保留命令、日志哈希、模型样本与运行边界。
- 2026-10-09 提交补记：审查与修复已保存为本地提交 `719ceb3522dfcab8035d2552a6ff620554eeb94b`（CC-20261008-009、CC-20261008-010），包含代码、回归样本、复现脚本和验证记录。提交内容与已通过全量检查的源码哈希一致；未推送、合并或部署。

## CC-20261009-001：归档遥测关闭与个人自用中转分析

- 授权 / 范围：维护者要求将对话结论整理到项目文档。场景是自己的订阅经自己的 Sub2API 转 API 自用管理，不描述为代他人转发；本轮只归档文档和证据摘要。
- 基线：HEAD `93275632a6a66dfc2648e9504a7d16bcf32da5d7`；release `b4430850844263e3fd3d2d180f514099bffad04e`；上游 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。工作区其他未提交修复不属于本轮验收对象。
- 版本 / 来源：未修改官方 Linux x64 CLI 2.1.292 / SDK 0.128.0，二进制 SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。依据该二进制内嵌 JavaScript、2026-10-08 的六组 Docker 断网采集及当日核对的官方数据说明。
- 结果：仅改中转 base 仍产生官方域名事件上报；关闭指标和错误报告后仍有启动配置请求；仅必要流量的中转短场景只观察到模型请求，而假 OAuth 场景仍请求 settings / policy_limits。当前旧事件路由不等于拦截所有客户端遥测。模型 metadata 身份字段仍保留。
- 方法边界：假凭据、独立配置、空工具和 MCP、短 print 模式；所有场景均预先关闭自动更新和市场安装。HEAD 探活由 PCAP 补回，保留原始 GET/POST 摘要；验证端点与次数，不冒充全部正文逐字节验证或所有出站尝试覆盖。没有真实订阅、上游接受或风控验证。
- 文件：新增 [使用与风险说明](claude-code-telemetry.md)、[结构化证据](claude-telemetry-validation.json)；README 增加入口，`.gitignore` 精确放行两份文档，本记录追加留痕。原始执行器、PCAP、来源模块与说明快照留在本机 `claude-capture/telemetry-audit-20261008/`，不提交 TLS 私钥、原始正文或完整提取源码。
- 验证：归档时核对六组已记录请求与 PCAP 端点计数、零内核丢包、CLI 退出码、metadata 结构及来源 SHA-256；JSON 解析、文档相对链接和 `git diff --check`。本轮未重跑 CLI、unit / integration / lint，没有更改生产行为或用户遥测设置。
- 提交 / PR：本轮未提交、推送或新建 PR。所在分支原实现关联 PR #5，不能将其视为本轮文档已经发布；后续提交使用 `Claude-Change-ID: CC-20261009-001`，PR 目标为 release。
- 2026-10-09 提交补记：维护者要求提交全部改动后，本记录的遥测文档与结构化摘要已随本地提交 `08a37c23a25347f8d7e82055cab8066075a28f86` 归档；未修改用户遥测配置，未推送或部署。

## CC-20261009-003：基于嵌入源码、机器码和隔离运行的全面对比

- 授权：维护者要求结合反编译源码和 Docker 隔离环境再次全面对比。审查固定在 2026-10-09 07:50:41 的源码快照；后端文件集合及内容与后来提交的 `719ceb35` / `fee18477` 一致。release `b4430850`、上游 `3f1a2ea0` 未更新，随后未提交的请求追踪改动另列为未验证。
- 版本：未修改官方 Linux x64 CLI 2.1.292 / SDK 0.128.0，SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。只读提取 2,260 个嵌入 JavaScript 模块、定位关键协议分支，并重新反汇编 Linux cch seed / 扫描函数；不宣称恢复全部原始 TypeScript。
- 捕获与转发：51 个场景、89 条请求，PCAP 原始字节一致且丢包 0；356 个转发组合中，未压缩的 348 个正文一致，gzip 的 8 个因解压和 cch 重算不同；方法、URL、最终长度与 GetBody 均一致。同认证应用头 174/178 一致，剩余为 gzip 编码头删除。
- 算法与传输：337 个 cch 独立原生运行时对照通过（含固定种子新增 256 个）；138 个提取原函数归因向量通过；60 个原生 ClientHello 非随机内容一致，四条自动选择的实际传输路径正文、完整头值、头序及握手均一致。原生运行时修改入口与未修改 CLI 抓包分别标识。
- 差异：新增确认请求 gzip 分支、普通 API 显式关闭 thinking、disabled thinking 额外字段清理、显式 temperature 与自动 thinking 的组合策略不同。均与常规原生转发分开，不推断真实服务端拒绝。旧 5.5 默认值和托管 compact 缺口在本次实现中已通过复验。
- 恢复与持久性：未修改 CLI 直接调用生产恢复 / Forward 服务，12 会话、96 生成、24 计数，连续压缩、迁移及迁移后压缩通过，跨会话混入 0；7 个 PostgreSQL 集成测试通过，测试存储与数据库证据分开。
- 实验纠正：目录重名后补抓缺失四组；effort 参数最初放在 `--` 后，独立重抓替换该组；补全 gzip 原始 PCAP 字段读取及缺失代理 helper 挂载。保留失败记录，不将接线错误算成产品缺陷。最终集合和报告不使用错误 effort 样本。
- 文件：新增 comprehensive 采集器、参数与 cch overlay 审查、PCAP 核对器；扩展 request_recheck 的真实解压入口；补充 README、`.gitignore`、[完整报告](claude-comprehensive-audit-20261009.md)及[结构化证据](claude-comprehensive-audit-20261009.json)。未修改生产实现。
- 验证：component 专项 148 个、service 专项 351 个通过事件（含子测试）、数据库 7 个通过；算法和实验结果另计。无 CLI 路径时两个原生实验入口跳过，扩展入口另在专门容器通过。未重跑全仓库 unit / integration / lint，不代表官方接受、真实签名、订阅、计费或风控验证。
- 提交 / PR：本轮审查未提交、推送、合并或部署；被审实现关联 PR #5。后续提交使用 `Claude-Change-ID: CC-20261009-003`，原始材料及快照保存在本机 `claude-capture/comprehensive-20261009-qlx_n3xe/`。保留 010、011 的历史范围和结论。


## CC-20261009-002：完整网关请求证据日志

- 授权：维护者要求尽可能完整记录请求，尤其是 Claude，供账号异常后分析；本次实现独立、默认开启、不采样的应用层 HTTP 证据日志。
- 基线：当前工作分支 `codex/claude-managed-recovery` 的 HEAD `fee184776`（接续 `719ceb35`）；release `b4430850`，上游 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。保留同时存在的审查与遥测文档改动，不合入 main、不修改远端。
- 版本 / 来源：现有 Claude Code 2.1.292 / SDK 0.128.0 的原生传输和网关入口；依据本仓库请求构建、HTTPUpstream、解压及流式处理实现。此次不更新协议 profile，也不以日志替代既有原生 PCAP 对照。
- 行为：记录原始入站、共享 HTTPUpstream 的最终请求、原始及显式解压响应、客户端实收正文；关联请求、用户、Key、分组、账号、代理、传输与状态。Claude 增加账号调度快照和结果用量。原生 transport 记录有效应用头与头序并桥接连接 / 首字节回调；不改变请求头或正文。
- 完整性 / 保留：逐片 base64、偏移、SHA-256、EOF / 提前关闭 / 截断标记、写入失败计数；默认不截断，100 MiB × 100 历史文件并设置 30 天轮转条件。目录 0700、文件 0600；凭据头、Cookie、代理 userinfo 和敏感 query 脱敏，对话正文保留。磁盘错误显式报告；导出校验材料缺失，不把缺日志当成功。
- 文件：`internal/config/request_trace*`、`internal/pkg/requesttrace/*`、网关路由 / 追踪中间件、`repository/http_upstream*` / `claude_native_transport.go`、Claude 消息 / count_tokens 结果日志、Compose / 配置样例、`deploy/export-request-trace.py` 及测试、[使用说明](request-tracing.md)。无数据库迁移或新增依赖。
- 覆盖边界：HTTP 网关及共享上游客户端；不捕获后台独立任务、专用客户端的出站或 WebSocket 帧。只记录实际读取 / 写出的字节，不主动排空。是应用层日志，不证明网络交付、官方接受或封禁原因；未部署或访问真实官方模型服务。
- 验证：新增本地模拟测试覆盖正文 / SSE 字节一致、压缩 403 双份证据、凭据脱敏、重定向、原生头与连接回调、并发隔离、轮转 / 权限、磁盘失败、配置关闭、导出跨文件重建及缺失检测。完整检查实际结果待本轮下方补记；未将旧快照审查当作本次验收。
- 提交 / PR：尚未提交、推送或创建 PR；后续提交使用 `Claude-Change-ID: CC-20261009-002`，PR 目标 release。工作区已有其他任务的未提交文档与实验，不属于本次代码交付。


### CC-20261009-002 验证补记

- 完整 unit 58 个包、完整 integration 52 个包通过；integration 最终使用 `go test -p 1 -tags=integration ./...` 串行执行。完整 golangci-lint 2.13.0 / Go 1.27.0 检查 0 issues。
- 日志包 / 中间件 / 仓储专项及 race 检测通过；原生 HTTP 同连接的第二次请求开启日志，正文、完整头序、凭据透传与连接复用原有断言全部保持通过。Python 导出器 3 项检查、Compose 环境透传、格式与文档链接检查通过。
- 保留执行过程：初次全量检查撞上 NativeContext 新文件加入时的编译快照不一致，以及旧路由源码断言；修正后重跑。首次集成因 Docker reaper 名称 / 启动冲突失败，串行完整重跑通过。最后仅按 staticcheck 作等价布尔简化，日志包 race 与全量 lint 再次通过。
- 验证边界：同时进行的 gzip / Claude 参数修复随后修改了 `internal/pkg/httputil/body.go`、新增 `request_encoding.go` 并修改 `gateway_claude_native.go`；不把本轮结果视为那些并行变更或之后整个工作区的验收，也未覆盖其后续改动。原始失败 / 成功日志与本次文件哈希见 [验证摘要](request-tracing-validation.json)。
- 仍为未提交、未推送、未部署的代码；没有真实上游请求、封禁验证或订阅资格验证。

- 2026-10-09 提交前复核：在 `01e75d9feeb26c54b47646343a97e2d42c915ba1` 上保存日志实现；28 个已记录文件中，仅原生传输文件因已提交的 CC-20261009-004 gzip 头序改动与原验证哈希不同。日志 / 配置 / 中间件 / 仓储 4 包相关回归再次通过，含原生 HTTP 字节、连接复用与取消检查；结果及本次源码哈希追加到验证摘要。没有为提交重复全量检查，未推送或部署。

- 2026-10-09 提交补记：完整请求日志实现、配置、回归与证据已保存为本地提交 `be3642a6f6c421cb437424b5da1778aaa89d03b7`，包含 `Claude-Change-ID: CC-20261009-002`。实际暂存的 28 个源码 / 测试 / 部署文件与提交前复核哈希完全一致；未纳入遥测归档文档修改。尚未推送、合并或部署。

## CC-20261009-004：对齐参数控制与原生 gzip 请求

- 授权：维护者要求开始对齐 CC-20261009-003 的四类差异；在 `fee18477` / `719ceb35` 上继续。release `b4430850`、上游 `3f1a2ea0` 未更新。保留共存的请求追踪改动，完整检查覆盖当前工作树，不将那些改动列为本轮实现。
- 版本 / 依据：固定未修改官方 Linux x64 CLI 2.1.292 / SDK 0.128.0，SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。沿用独立原生 gzip 捕获，新增五模型 × 四场景共 20 条参数捕获，PCAP 字节一致、丢包 0。
- gzip：入口保留请求独占的原压缩字节及逻辑正文摘要；仅在已验证第一方 messages 路径且最终正文未改时恢复 gzip、原 cch sentinel、长度与 GetBody。模型 / beta / 托管身份改写不能恢复旧正文。Content-Encoding 移至已排序应用头之后；最终四条真实传输路径的压缩字节、正文、完整头值及顺序和 ClientHello 非随机部分均与原生一致。
- 解压边界：读取现有 64 MiB 上限加一，超限显式失败，CRC 损坏不保留可重放数据；防止把只解析了前缀的整个压缩包向上游发送。回归覆盖独立请求、预读 / clone、返回切片修改、正文变化、版本 / origin / 头策略。
- 参数：支持模型的直接 disabled 控制补温度 / effort，清理所有额外 thinking 键；单独显式 temperature 保留模型默认 thinking 和关联字段，显式合法值优先。5.5 既有限制保留，不以模拟成功放宽非法组合。
- 语义边界：CLI 的 EXTRA_BODY 是更晚的覆盖入口，同一个 disabled 对象可能保留不同上下文 / 温度缺省值。普通 API 按直接控制解释，不推断隐藏入口；旧审查 EXTRA_BODY-only 样本的两项缺省值差异仍单列，未宣称已完全消失。原生完整请求可按原生路径保留。
- 验证：原 89 条请求的 356 个组合，逻辑 JSON、实际发送字节、长度、GetBody 全部一致；178 个同认证组合应用头一致。12 条原生托管会话、96 生成、24 计数通过，跨会话混入 0；使用测试存储与上游，不冒充完整真实部署。
- 工程：完整 unit 58 个包、integration 52 个包通过；最后的 gzip 头序改动后 repository 集成包再通过；最终 lint 0 issues，Go 源码在最终 unit 期间保持不变。首轮旧 disabled 温度断言按新证据修正；显式值保留断言未删除。首轮宿主 lint 工具链不匹配，固定 Go 1.27.0 后通过，保留失败记录。
- 文件：httputil 解压 / 原压缩快照、网关 native finalizer、2.1.292 默认参数、原生传输排序、回归与采集样本、transport probe、采集 / 核对脚本、维护说明及 .gitignore。详见 [实现与边界](claude-parameter-wire-alignment.md)、[验证及源码哈希](claude-parameter-wire-validation.json)。
- 提交 / PR：本轮未提交、推送、合并或部署，所在分支关联 PR #5；后续提交使用 `Claude-Change-ID: CC-20261009-004`。原始材料在本机 `claude-capture/parameter-wire-alignment-20261009-c0k0t5v2/`。未访问真实官方模型服务，不代表官方接受、真实签名、订阅或计费验证。
- 提交隔离验证：从实际暂存内容导出独立源码快照，排除并行请求追踪实现，httputil / service / repository 三个包相关回归通过。结构化验证记录保留共享工作树全量检查与独立暂存回归的不同源码哈希，不混用验证范围。
- 2026-10-09 提交补记：对齐实现与关联的 CC-20261008-011 / CC-20261009-003 审查证据已保存为本地提交 `5447bd74bada937d165bf77f211f43895e044340`；三个记录的 trailer 随提交保留。提交前独立暂存快照回归通过，源码与已验证暂存内容一致。尚未推送、合并或部署。


## CC-20261009-005：对齐后压缩、头策略和日志的综合复核

- 授权：维护者要求再次全面对比，并在进行中明确要求提交全部代码。固定审查提交 `c20ac40a`（参数 / gzip `5447bd74`、日志 `be3642a6`）；release `b4430850`，上游 `3f1a2ea0`。生产实现保持不变。
- 版本 / 依据：未修改官方 Linux x64 CLI 2.1.292 / SDK 0.128.0，SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`；固定二进制中的 JS 压缩分支、Docker 断网、独立配置与假凭据。
- 捕获：重跑原 51 组并增加 8 组，共 59 场景、99 请求、8 条压缩请求，PCAP 字节一致且丢包 0。396 个转发组合逻辑 JSON 全部一致，392 个原字节一致；198 个同认证组合中 196 个头值一致。
- 新差异：大型 count_tokens 使用 gzip 时仍被解压发送；JS 分块 gzip 的编码头应在应用头排序中，现有统一后置规则不匹配；账号编码头覆写存在大小写查找 / 重复键问题，可生成头体不匹配。后者的 22 次实际发送全部与 PCAP 核对，不以模拟服务端 400 冒充官方拒绝。
- 日志与传输：更换独立回环代理后完成 32 组真实传输；正文、头值、非随机 ClientHello 均一致，8 个分块 gzip 组合头序不同。16 个日志开启组合正文、偏移、摘要、完整标记与实际字节一致，凭据脱敏、写入失败 0。
- 其他验证：component 6 包 149 个通过事件，service 374 个通过事件，repository 9 个集成测试通过；12 托管会话、96 生成、24 计数通过，跨会话混入 0。次数含子测试，未重跑全仓库 unit / integration / lint 或全部原生算法探针。
- 实验边界：初次 policy 测试漏挂 testdata 后补跑；首版观测器沿用大小写受限 getter，最终改为记录全部变体并以实际网络为准。旧双线程本地 HTTPS 代理发生一次 TLS 解码错误，未单独定责；每连接单线程非阻塞代理复验 32 组通过，保留原始失败材料。未修改生产网络实现以绕过失败。
- 文件：新增边界采集器、编码策略 overlay、带日志的传输探针、单线程回环代理及核对器、复现说明、[报告](claude-post-alignment-audit-20261009.md)和[证据摘要](claude-post-alignment-audit-20261009.json)。此前已明确的 EXTRA_BODY / 普通 API 语义边界保留，不将其重新归为新回归。
- 提交 / PR：维护者已授权提交全部当前改动；本轮审查使用 `Claude-Change-ID: CC-20261009-005`，提交后补记哈希。所在分支关联 PR #5，未推送、合并或部署；原始材料在本机 `claude-capture/post-align-audit-20261009-rkgawtwu/`。不代表真实官方接受、签名、订阅或风控验证。
- 2026-10-09 提交补记：本轮审查工具、报告和证据已保存为本地提交 `08a37c23a25347f8d7e82055cab8066075a28f86`，并按维护者要求纳入此前未提交的文档。该提交不修改生产实现；三项新发现仍待修复。未推送、合并或部署。

## CC-20261009-006：完整修复原生 gzip 计数、分块头序和编码覆写

- 授权：维护者要求完全修复 CC-20261009-005 的三项发现，并沿用此前提交全部改动的要求。在 `37289d73` 上继续，release `b4430850844263e3fd3d2d180f514099bffad04e`、上游 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469` 未更新。
- 版本 / 来源：固定未修改 Linux x64 CLI 2.1.292 / SDK 0.128.0，二进制 SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。只读提取 `chunk-47d8fnm7.js` 的 oos/Sto 封装及 `chunk-9yn9h839.js` 的 aM/dee 分支，保存文件哈希和二进制偏移；重新运行断网 Docker、空配置和假凭据采集。
- 修复：count_tokens 原压缩字节恢复独立于 messages 的 billing / cch 条件，仍需目标 / 版本 / 原生识别与最终正文摘要相等；分块 gzip 用完整固定封装头和同步刷新 / 空终止块识别，在请求 context 中选择应用头排序，运行时 gzip 保持后置；前后端禁止静态 Content-Encoding 覆写，过滤旧配置，清理所有大小写变体并由最终正文确定编码。
- 边界：封装判断仅用于已完整解码并通过 CRC、摘要校验的已知原生请求，不能作为任意 gzip 来源或身份认证。模型 / 身份 / 策略改写不得恢复旧正文，未知版本及自定义 origin 保留原有适用范围。无数据库迁移、新运行时依赖或默认配置开关。
- 原生补证：12 组新场景、20 请求，其中 18 条 gzip，PCAP 原字节全部相等、丢包 0；覆盖级别 1 / 6 / 9、Unicode、count_tokens、mode 1 首轮运行时压缩到下一轮分块复用，以及 mode 2 两轮压缩。18 个压缩样本纳入永久回归，头序预期来自独立捕获。
- 综合转发：结合旧 99 条样本，共 119 条请求、476 个配置组合，逻辑正文 / 实际字节 / 方法 / URL / 长度 / GetBody 全部一致；238 个同认证组合应用头一致。12 托管会话、96 生成、24 计数通过，连续压缩、迁移与迁移后再压缩 / 续聊无跨会话混入；测试存储 / 本地上游与数据库集成证据分开。
- 工程与真实传输：152 组发送的原字节、完整头值 / 头序、ClientHello 非随机部分全部一致；76 组日志原文 / 摘要 / 完整性 / 脱敏通过，写入失败 0。22 次编码策略发送全部为合法 JSON，PCAP 一致、丢包 0。完整 unit 58 个包、integration 52 个包、前端 70 项通过；最终固定快照 lint 0 issues。最后删除一处触发 SA1012 的 nil Context 测试断言，Claude 包复跑通过，生产代码与全量测试时一致。
- 并行边界：全量测试完成后出现 CC-20261009-007 的 Go 1.27.2 / 依赖升级，保留该组工作区改动，本提交采用已暂存 Go 1.27.0 快照；不混用两轮依赖的验收。原共享树 lint 主动终止，隔离首试遇到进程锁，后一次发现上述测试写法并超时；修正后延长上限重跑完整 lint 通过。最终 3,186 个 Go/SQL 文件的差异和完整来源哈希见验证摘要。
- 文件：native finalizer、原生传输头序、claude gzip context、raw header 工具、账号覆写后端与前端验证、永久回归 / gzip 样本、采集与严格头序核对脚本；详见 [修复报告](claude-gzip-complete-fix-20261009.md)及[结构化验证](claude-gzip-complete-fix-20261009.json)。原始材料在本机 `claude-capture/encoding-complete-fix-20261009-_n_icwx2/`。
- 提交 / PR：本轮代码提交使用 `Claude-Change-ID: CC-20261009-006`，提交后补记哈希；所在分支关联 PR #5。未推送、合并或部署，没有真实官方接受、签名、订阅、计费或风控验证。保留 CC-20261009-004 / 005 的历史适用范围。
- 2026-10-09 提交补记：本轮修复、18 个原生压缩样本、回归脚本与验证记录已保存为本地提交 `3b9b984f9432e4eb1218f9f884d521064de8f173`，带 `Claude-Change-ID: CC-20261009-006`。提交中的 41 个实现 / 样本 / 复现输入文件与最终固定快照 SHA-256 一致，采用 Go 1.27.0 依赖。CC-20261009-007 的并行升级仍单独保留，未纳入本提交；未推送、合并或部署。


## CC-20261009-007：修复后端 Go / HTTP/2 安全扫描告警

- 基线：上游 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，release `b4430850844263e3fd3d2d180f514099bffad04e`；在工作分支 `codex/claude-managed-recovery` 的 `37289d73e35fb6d356c163244ed830087344c4c3` 及已有暂存改动上修复，不同步上游或改写历史。
- 原因 / 依据：[backend-security 失败日志](https://github.com/skingford/sub2api/actions/runs/37888055342/job/113682424457) 报告 12 个代码可达漏洞编号；`pluginapi.Serve → plugin.Serve → http2.Framer.WriteContinuation` 是 [GO-2026-6617 / CVE-2026-97032](https://pkg.go.dev/vuln/GO-2026-6617) 的示例调用链，根因是 HTTP/2 服务端 HPACK 编码器并发修改可能导致崩溃。官方 [Go 漏洞数据库](https://vuln.go.dev/ID/GO-2026-6617.json) 指定 Go 1.27.2 与 `golang.org/x/net v0.60.0` 为当前版本分支的修复下限。
- 版本 / 范围：Go 1.27.0 → 1.27.2，`x/net` 0.58.0 → 0.60.0；通过 `go get` / `go mod tidy` 同步其最低版本依赖 `x/crypto`、`x/mod`、`x/sync`、`x/sys`、`x/term`、`x/text`、`x/tools`。Claude CLI 2.1.292 / SDK 0.128.0 的现有兼容目标保持不变；本条升级共用工具链和网络依赖，并迁移 HTTP/2 客户端保活配置；没有新增 Claude 请求、认证或协议实现。
- 工具兼容性：首轮 unit / integration 的 `TestAuthIdentityFoundationSchemas` 失败，原因是 `x/tools v0.49.0` 不能读取 Go 1.27.2 的 V5 导出数据。依据官方 [V5 读取器修复](https://github.com/golang/tools/commit/89ed5c340cb6d4a9437f801cfc718ac5980c938d)，将 `x/tools` 固定为 v0.51.0 后该测试通过；CI 同步升级为含 V5 读取器的 golangci-lint v2.14.0，避免 v2.13 系列内置 v0.49.0 的兼容问题。未删除或弱化 schema 测试。
- HTTP/2 兼容：`x/net v0.60.0` 将旧配置 API 标记为弃用。客户端保活改用 `http.Transport.Protocols` / `HTTP2Config`，保留两种模式原有 PING 超时、代理和 HTTP/1.1 回退，并补充真实 TLS 回退回归。服务端仍使用已修补的兼容适配器，原因是标准库单一 `Server.IdleTimeout` 无法保留当前 HTTP/1 与 H2C 分别配置的空闲超时；该调用和旧 `GoAwayError` 兼容判断仅按位置豁免 SA1019 并注明理由，未关闭安全扫描或全局弃用检查。
- 文件：`backend/go.mod` / `go.sum`、三个 Dockerfile、backend-ci / security-scan / release 工作流的 Go 校验、CI 的 golangci-lint 版本、开发指南、三份 README 和 `.github/claude-validation/README.md` 中现行验证命令；`http_upstream.go` / HTTP2 keepalive 测试、server `http.go` / ingress 测试、Codex models service 及其错误兼容测试。历史实验报告中的 Go 版本和结果保留。
- 最终安全验证：Go 1.27.2、govulncheck v1.8.0，`GOOS=linux GOARCH=amd64 govulncheck ./...` 退出 0：代码可达漏洞 0、导入包漏洞 0；仍有 7 个依赖模块级提示，扫描未发现调用路径，不宣称整个依赖树不存在漏洞。此前 macOS 扫描也通过；GitHub 原失败运行未重跑，最终修复尚未推送。
- 最终工程验证：`GOTOOLCHAIN=go1.27.2 go test -p 2 -tags=unit ./...` 58 个包通过；`GOTOOLCHAIN=go1.27.2 CI=true go test -p 1 -tags=integration ./...` 52 个包通过（使用真实本地 PostgreSQL / Redis 测试容器）。HTTP/2 / 旧错误兼容针对性测试 3 个包通过；最终补充弃用注释后 server unit 再次通过。官方 golangci-lint v2.14.0 二进制经 SHA-256 核验，使用独立缓存及 `--allow-parallel-runners --concurrency=2 --timeout=30m ./...` 完整检查 0 issues；首次进程锁冲突、旧工具导出格式问题和弃用告警均保留为检查过程记录。
- 构建配置验证：`go mod verify`、`go mod tidy -diff`、`git diff --check` 通过；官方 `golang:1.27.2-alpine` 标签存在，workflow 校验、go.mod 与三个构建镜像版本一致。未执行完整 Docker 镜像构建或真实上游请求。
- 提交 / PR：本条工作区修复尚未提交或推送；后续提交应使用 `Claude-Change-ID: CC-20261009-007` 并补记哈希与 PR。并行 gzip 任务已自行提交为 `3b9b984f` / `4dbbd949`；本条没有将其改动纳入安全修复提交，最终验证针对包含这些提交的工作区。未部署；本地测试不代表真实 Claude 上游接受、订阅或计费验证。

- 2026-10-09 交付授权：维护者要求提交全部当前改动、推送并合入 `release`，随后删除本次开发分支。安全修复随 [PR #5](https://github.com/skingford/sub2api/pull/5) 交付，使用 merge commit 保留各项实现与证据提交的原始引用；最终提交后补记哈希。
- 2026-10-09 提交补记：安全修复代码与验证记录提交为 `c142e67ece6383119eddf7413269e9b78576732e`，带 `Claude-Change-ID: CC-20261009-007`；随 [PR #5](https://github.com/skingford/sub2api/pull/5) 合入 `release`。上述 58 包 unit、52 包 integration、0 issues lint 和 Linux govulncheck 结果对应本提交的运行时代码；本次补记仅增加提交引用。

## CC-20261009-008：release 合入后的全面深入对比

- 授权 / 基线：维护者要求再次全面深入对比；固定 `release` 的 `9833384c65ddb574b2054d3c9e0a7dd83d359f5c`，包含 CC-20261009-006 gzip 修复和 CC-20261009-007 Go / HTTP2 升级。上游 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469` 未更新；在 `codex/claude-deep-audit-20261009` 记录审查，不修改生产逻辑。
- 版本 / 依据：Go 1.27.2；固定未修改 Linux x64 CLI 2.1.292 / SDK 0.128.0，二进制 SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。重新定位嵌入模块的压缩门槛、分块封装、拒绝降级和后缀函数，记录来源偏移 / 哈希；运行未修改 CLI、独立原生机器码 oracle 与提取 JS 函数对照，三者分开计数。
- 新发现 F1：API Key 账号改写 UA 或关闭原生保留后，known 分支退出，已解码正文会作为明文发送但仍带 gzip 的 `cch=00000`。逻辑摘要不变未阻止该编码变化；16 条第一方实际发送中 12 条与独立原生 CCH 不一致，4 条模型映射对照正确。未修改 CLI 的自定义 UA 场景也在四个转发配置中复现。真实官方是否拒绝未验证，问题定位在 finalizer 第 115–123 行。
- 新发现 F2：错误转换器丢失 cf-ray，改变原生 CLI 对 403 来源的识别。持续相同拒绝、分块 gzip 开启且普通 SDK 重试为 0 时，原始 cf-ray-only 响应为 1 次请求，生产转换后的响应为 3 次（分块 / 运行时 gzip / 明文）；仅在实验中补回 cf-ray 后恢复 1 次。request-id 控制为 1 / 1，匿名控制为 3 / 3。另 7 个未修改 CLI 回放场景、13 条请求（10 gzip）PCAP 全部一致、丢包 0；这是生产响应转换器与下游 CLI 的两阶段语义验证，不冒充完整线上部署。
- 新发现 F3：标准化错误体时忽略原生可识别的 error 字符串 / reason=bad_json 对象及纯文本 bad json。模拟 gzip 被拒绝、明文可成功，原始两种响应各自使 CLI 经 3 次请求恢复成功，转换后的通用 api_error 使 CLI 首次报错即退出；标准 Anthropic 错误格式控制仍能恢复。另 6 场景 / 14 请求 PCAP 全匹配、丢包 0。
- 原生与转发：85 场景、139 请求，其中 44 条 gzip，全部 PCAP 字节匹配、丢包 0。556 个配置组合逻辑正文一致，552 个发送字节一致；4 个差异为 F1 的自定义 UA。278 个同认证组合头值 274 个一致，另 2 个编码头变化、2 个为空协议版本补缺省值。排除两种头策略场景，其余 137 请求 / 548 组合字节及 274 个同认证应用头一致。
- 算法与传输：593 个 CCH 独立运行时向量全同（新增固定种子 256 个，PCAP 全同）；394 个原 JS 后缀函数向量与 Go 一致。112 组四路径 / 日志开关传输，字节、完整头值 / 头序及 ClientHello 非随机部分全同；56 组日志正文、摘要、完整性和两种假凭据脱敏通过，写入失败 0。
- 恢复与工程：12 会话、96 生成、24 计数，连续压缩和迁移后压缩 / 续聊通过，跨会话混入 0。service 专项 548 个通过事件；补充 38 个合同 / 参数检查和 16 个错误信号观察通过，108 策略结果复跑相同，成功响应头过滤观察通过；两个需 CLI 环境的入口在普通命令跳过，专用联调另跑；repository 19 个顶层测试 / 51 个通过事件，含 PostgreSQL 事务、会话隔离、HTTP2 回退、TLS 校验、连接复用和取消。未重跑全仓库 unit / integration / lint；3,188 个 Go / SQL / 依赖清单文件与固定快照一致。
- 既有边界：OAuth 旧身份策略改变 session ID 后本地 400 是已有明确的保护规则，不列为新缺陷；普通 API 与晚期 EXTRA_BODY / verbose 的八组字段差异继续单列。默认成功响应过滤器还会丢失 request-id 和已测 Anthropic 限流头，显式 additional_allowed 可保留；跨进程压缩锁定影响仅列为源码推断。自定义 origin 不纳入第一方 CCH 接受性结论，也不把其传输退出当作新回归。
- 实验纠正：一处准备脚本相对路径错误改为绝对路径后重跑。连续大明文在宿主 bind mount 捕获中出现 203 / 79 个内核丢包，保留失败材料；改为容器 tmpfs 后重采 20 条原生重算及 16 条 Go 发送，PCAP 全部一致、丢包 0，不将采集缺口判为产品网络故障。
- 文件：新增深层原生场景、策略 / 错误信号 overlay、真实策略发送、下游原生错误回放、独立运行时 PCAP 核对和 tmpfs 采集工具；更新维护说明 / .gitignore，新增 [深入报告](claude-deep-audit-20261009.md)及[结构化摘要](claude-deep-audit-20261009.json)。原始材料位于本机 `claude-capture/deep-audit-20261009-o0w0wdwu/`，不提交完整提取源码、实验二进制、TLS 私钥或原始大正文。
- 提交 / PR：本轮审查使用 `Claude-Change-ID: CC-20261009-008`，提交后补记引用；尚未推送或新建 PR，没有部署。原 PR #5 已合入 release，仅作为被审基线。三项新缺陷尚未修复，不代表真实服务接受、订阅、签名或计费验证。

- 2026-10-09 提交补记：深入审查工具、报告和结构化证据已保存为本地提交 `90db233293115cbc90429274797ac703322a84d2`，带 `Claude-Change-ID: CC-20261009-008`。累计 98 个未修改 CLI 场景 / 166 条请求的最终 PCAP 全部匹配且丢包 0；三项发现仍待修复。被审生产源码保持 release `9833384c6` 不变，未推送或部署。

## CC-20261009-009：修复 gzip CCH 与两类错误恢复信号

- 授权与起点：维护者要求修复 008 的三处协议差异，并授权隔离环境中使用指定中转地址 / 凭据测试。起点 `0f628bcf44ef10d6dde3f7a9a9ecec6d2b0f6b5b`，生产基线 release `9833384c65ddb574b2054d3c9e0a7dd83d359f5c`；上游基线仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。
- 版本与依据：Claude Code 2.1.292 / SDK 0.128.0，复用 008 的固定二进制、反编译来源和独立 CCH oracle；Go 1.27.2。原生压缩错误分类依据 chunk-hy08191v.js 的来源标记、8192 字节探测和 schema 优先级，不以模拟响应推断官方接受规则。
- 行为：原字节 gzip 保留不再依赖最终 UA 的已知版本判断；已知版本改成明文时独立重算 CCH，未知格式的不安全转换 / 改写返回 400。错误响应保留 cf-ray（含空值），将原生认可的非标准 JSON / 纯文本解析错误转换为短标准消息，同时防止 trim / 嵌套提取制造新重试信号。统一短消息还避免 JSON 转义后超过原生探测上限。
- 文件：`internal/pkg/httputil/request_encoding*`、`gateway_claude_native*`、`gateway_claude_compatibility.go`、新增 `gateway_claude_error.go` / `gateway_claude_protocol_fix_test.go`，以及 gzip / parameter 回归、验证脚本和 [修复报告](claude-protocol-fixes-20261009.md) / [结构化记录](claude-protocol-fixes-20261009.json)。
- 协议验证：556 组逻辑正文及原字节比较全部通过；108 个策略观察，16 个真实发送匹配原生 gzip 或独立 CCH oracle，PCAP 零丢包。22 个未修改 CLI 错误回放场景 / 58 条请求（40 条 gzip）均匹配 PCAP；cf-ray 403 不再额外降级重试，三种解析错误及转义扩展均正常恢复。最终服务联调覆盖 12 条对话、96 条生成、24 条计数，跨会话串入 0。
- 真实接口：首个地址在模型列表阶段被 403 拒绝。更换地址 / 新凭据后，模型列表 Bearer 成功；消息接口两种认证、原生 CLI、生产 Forward + HTTPUpstream 两阶段 normal / passthrough 共 5 个短生成用例成功。独立记录真实与合成结果，不把中转返回 200 视为官方 CCH、订阅或完整部署验收；专用网络、代理和本地临时凭据已清理。
- 配置复用：按维护者后续请求，在远端 root 的 Claude Code 2.1.295 配置自定义地址、Bearer 凭据和 `hasCompletedOnboarding`，认证检查及短请求成功。新增无内置凭据的 `configure_claude_router.py`，备份旧配置并以 600 权限原子保存；提供交互式隐藏输入及私有 key-file 两种方式，步骤见验证 README。远端 2.1.295 的配置测试不扩大 2.1.292 算法兼容声明。
- 工程验证：最终全量 unit 58 包、integration 52 包通过，golangci-lint 2.14.0 为 0 issues，详见结构化记录；首轮全量单测出现一次未修改 OpenAI 缓存刷新去重用例失败，同基线隔离重复 100 次通过，保留失败日志及最终复跑记录。lint 使用匹配工具链的 2.14.0。
- 边界与提交：空 anthropic-version 补默认值、身份冲突保护、自定义 origin 和成功响应头过滤边界仍保留。本条使用 `Claude-Change-ID: CC-20261009-009`，提交后补记哈希；没有合并或部署网关代码。保留 008 的历史问题记录，由本条链接修正其当前状态。

- 2026-10-09 提交补记：三项协议修复、最终回归与复用配置脚本已提交为 `133c6b4212a7192a52ffb9ccf5dd9e508c6d87d2`，带 `Claude-Change-ID: CC-20261009-009`。提交内 3190 个 Go / SQL / 模块文件与最终验证清单一致；58 包 unit、52 包 integration、0 issues lint 均通过。尚未推送、合并或部署网关代码；远端 CLI 配置为维护者单独授权的操作。

- 2026-10-09 发布跟踪：维护者授权推送并合入 release；008 / 009 所在分支已推送，关联 [PR #6](https://github.com/skingford/sub2api/pull/6)，目标为 `release`。上文“尚未推送 / 合并”保留为各提交时点的历史记录，合并状态以本 PR 为准；未部署网关代码。

## CC-20261009-011：反编译来源与两个隔离 CLI 版本逐项复核

- 授权 / 基线：维护者要求依据反编译源码与 Docker 隔离 CLI 全面深入逐项核对。固定 release `7ce838ee33663b8b4dc93296a51fad25b2daa49c`，上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`；从 release 建立 `codex/claude-source-runtime-audit-20261009`。原 `codex/claude-router-model-alias` 的 `f5188a2e6` 及其 CC-20261009-010 记录保留在原分支，本轮不改写或合并它。
- 版本 / 来源：Go 1.27.2，未修改 Linux x64 CLI 2.1.292 / 2.1.295，SDK 均为 0.128.0、runtime v26.3.0。前者 SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`；后者 `4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358`，核对固定官方 manifest。重新提取 2,260 / 2,340 个嵌入 JS 模块，两版各 3 个 Zstd 模块只读解压。来源偏移、压缩前后哈希和关键函数锚点留档，不冒充原始 TypeScript。
- 原生转发：两版各 95 个新 CLI 场景 / 149 请求，合计 1,192 个认证 / passthrough 组合，逻辑及发送字节全部一致；每版同认证头值 296/298 一致，两例差异仍是空 anthropic-version 补默认值。方法、路径、长度、GetBody 和认证方式全通过。OAuth passthrough 的配置重复不算独立实现。
- 新版缺口 F1：2.1.295 托管恢复的三模型 × 两种账号六组全部在压缩后续聊失败。自定义 origin 无分类头的推断仅允许 2.1.292；仅在实验 overlay 放开版本后，首次压缩继续而第二次仍被新版保留消息摘要尾文拒绝。实验副本同时适配精确尾文后，六组通过：12 会话、96 生成、24 计数，跨会话混入 0。原始 2.1.292 同序列通过。没有将实验修正写入生产代码。
- 新版缺口 F2：Haiku 5.5 普通 API 转换没有进入已验证模型默认值分支，缺 thinking / 默认 effort / context_management，且补 temperature=1；原生 CLI 直通仍保留。2.1.295 裸 haiku 实际解析为 5.5，2.1.292 为 4.5；完整模型及显式控制逐字段记录。
- 新版缺口 F3：2.1.295 常规请求不选择 2.1.292 原生传输配置。两版各 24 组四路径 / 日志开关实际发送：正文全部一致、零丢包；2.1.292 的头值 / 头序 / ClientHello 全同，2.1.295 的头序及握手均不同，Connection keep-alive 省略，其他应用头值忽略大小写后相同。两版原生握手各 5 条控制均匹配既有黄金摘要，不据此声称官方拒绝或封禁。
- 既有修复复验：两版各 22 个错误回放场景 / 58 请求，原始与生产转换响应的请求次数、编码序列和结束状态相同；cf-ray 和三类 bad-json 及转义扩展恢复没有复发。合计主捕获与错误回放 234 场景 / 414 请求，正式 PCAP 全匹配、零丢包。两版各 108 组策略保留身份冲突、未知 CCH 改写保护和自定义 origin 的明确边界。
- 算法 / 工程：593 个独立原生运行时 CCH 向量与 Go 一致；两版提取后缀函数的 394 个唯一向量与 Go 一致，探针和提取函数分别计数。service 专项 1,663 个通过事件，六个组件包 322 个通过事件，CCH / 成功头策略 595 个通过事件；完整后端 3,190 个源码文件与固定 release 快照一致。没有重跑全仓库 unit / integration / lint。
- 文件 / 复现：新增 [深入报告](claude-source-runtime-audit-20261009.md)和[结构化结果](claude-source-runtime-audit-20261009.json)、只读嵌入源码提取器及版本边界场景，公共采集器增加显式的两个版本哈希白名单；更新验证 README 和 .gitignore 的报告白名单。本机材料位于 `claude-capture/source-runtime-crosscheck-20261009-333q2v1p/`。最终工具另跑两个版本单场景 smoke 和 PCAP，不与主审查重复计数；提取器重建的 4,600 个模块解码字节全部一致。
- 提交 / 边界：当前审查尚未提交、推送、合并或部署；未来提交使用 `Claude-Change-ID: CC-20261009-011` 并补记引用。三项新版兼容缺口未修复，009 的历史修复结论由本条复验补充。全部模型交互使用隔离假凭据和模拟响应，不代表真实服务接受、OAuth 订阅、签名或计费验收。


- 2026-10-09 提交 / 推送补记：维护者授权“提交 推送”后，011 的审查材料与 012 的修复一并保存为 [765a12ec4](https://github.com/skingford/sub2api/commit/765a12ec4199dfeb490b60b0cfcc47db10bdff14)，提交同时带两个 Claude-Change-ID trailer，并已推送至 `origin/codex/claude-source-runtime-audit-20261009`。被审 release 基线不变，未合并或部署；上文未提交状态保留为审查完成时点的记录。

## CC-20261009-012：完整修复 2.1.295 三项兼容缺口

- 授权 / 基线：维护者要求“完全修复”011 的问题；在同一 `codex/claude-source-runtime-audit-20261009` 工作区完成，固定 release `7ce838ee33663b8b4dc93296a51fad25b2daa49c`，上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。保留 011 的原始审查记录，以本条更新当前状态。
- 版本 / 来源：未修改 Linux x64 CLI 2.1.292 / 2.1.295，SDK 0.128.0、runtime v26.3.0，使用 011 中的二进制 SHA-256 和嵌入模块锚点；新增 Haiku 5.5 的 11 场景 / 21 请求及 PCAP 核对。独立 2.1.295 CCH 探针仅替换 JS 入口并禁用入口 bytecode，原生机器码前缀保持不变；605 个向量与 Go 一致，含 12 个真实新版正文及模型 / token / Unicode 改写向量，正式捕获零丢包。
- F1 修复：精确放行 2.1.295 无分类头的压缩识别及已观测保留消息尾文；仍校验已保存摘要、保留历史、system 帧和工具状态。拒绝伪造、追加及重复尾文。迁移归因保留 2.1.295。原生恢复 2.1.292 三模型 × 两种账号通过 12 会话 / 96 生成 / 24 计数；2.1.295 加入 Haiku 5.5 后四模型 × 两种账号通过 16 会话 / 128 生成 / 32 计数，跨会话混入均为 0。
- F2 修复：Haiku 5.5 的 128000 token、adaptive / omitted、medium effort、context_management 和 OAuth beta 顺序与捕获一致；显式 token、temperature、effort、display 保留。普通转换为该模型选择已验证 2.1.295 身份，保证 UA / billing / CCH 一致；count_tokens 不添加生成参数或 billing block。支持配置选择精确 292 / 295 版本，未知版本仍拒绝。补查并修复 Responses / Chat Completions 的旧 thinking 分支和丢弃签名问题；共享模型能力判断，保留 xhigh、opaque thinking / redacted_thinking 及工具回填，模型描述同步 1M 上下文及 effort，未修改价格。
- F3 修复：增加独立的 `claude_2_1_295_linux_x64` profile / 连接池键；仅允许已测 Linux x64、SDK / runtime 组合。保留 gzip 编码、两种 Content-Encoding 头位置、TLS 校验、四种代理路径与日志行为。两版合计 48 组真实传输的正文、完整头值 / 头序、归一化 ClientHello 全同、零丢包；24 组日志字节、摘要、完整性、脱敏通过。未扩大到 295 MacOS、ARM、HTTP/2 或任意未来版本。
- 转发 / 回归：旧捕获 1,192 组合与新增 Haiku 捕获 84 组合，合计 1,276 个生产 Forward 组合正文及 wire 全同。新增捕获、归因、CCH、版本 / 平台、精确摘要、显式参数、OpenAI 兼容入口及签名往返永久回归。既有空版本补值、身份冲突和成功头过滤等显式策略保留。
- 工程检查：最终使用不可变源码快照完成全量 unit / integration / lint；实际结果在完成后补记，并保存于下方结构化结果。准备中两处错误测试假设已纠正；在补齐兼容入口时一次旧集成构建混用修订导致 helper 未定义，保留失败并以冻结快照重跑，不当作产品运行时失败。
- 文件 / 证据：恢复 compaction / count / service、CLI 兼容与默认参数、CCH 最终处理、Haiku 能力表、Responses 适配器、HTTPUpstream / TLS profile 及测试；新增 `testdata/claude_code_2_1_295`、12 个 CCH oracle 样本、Haiku 隔离采集器，扩展双版本运行时 / 传输探针及 README / .gitignore。[修复说明](claude-295-compatibility-fix-20261009.md)、[结构化结果](claude-295-compatibility-fix-20261009.json)；完整材料在本机 `claude-capture/compatibility-fix-20261009-yvk8ocrl/`。
- 提交 / 边界：当前尚未提交、推送、合并或部署；后续提交携带 `Claude-Change-ID: CC-20261009-012` 并补记引用。本轮全部模型交互均为隔离假凭据 / 模拟响应；不代表官方接受、真实 thinking 签名验证、OAuth 订阅或计费验收。

- 最终工程补记：不可变快照的 58 包 unit 全通过；integration 首轮 49 包通过，三个包在 Redis / PostgreSQL / Ryuk 启动阶段因本机 Docker 双栈发布端口选择失败。临时代理仅将这三个包新建 testcontainers 的端口绑定到 IPv4，保留所有原断言及真实数据库交互，复验全部通过，合计 52 包、未解决失败 0；原始命令退出 1 与环境诊断均保留。代理已停止，没有修改 Docker 全局配置或业务容器。golangci-lint 2.14.0 为 0 issues；模块校验、tidy diff 和差异空白检查通过。工作区 3,193 个 Go / SQL / 模块文件与最终快照一致，连同回归 JSON 共 3,332 个文件有 SHA-256 清单。

- 2026-10-09 提交 / 推送补记：本条代码、回归样本及验证材料已提交为 [765a12ec4199dfeb490b60b0cfcc47db10bdff14](https://github.com/skingford/sub2api/commit/765a12ec4199dfeb490b60b0cfcc47db10bdff14)，带 `Claude-Change-ID: CC-20261009-012`，并按维护者授权推送到 `origin/codex/claude-source-runtime-audit-20261009`。通过 Git 提交归档逐文件重新核对，3,332 个源码 / 回归 JSON 与已验证快照完全一致；本轮提交后仅补充追溯记录，没有重跑相同源码的测试。没有新建 PR、合并 release 或部署。


## CC-20261009-013：已推送修复后的再次逐项审查

- 授权 / 基线：维护者要求再次依据反编译源码及 Docker 隔离 CLI 全面深入对比。固定 HEAD `afb0c04a361a10ac17dc55ea00a573f1b415fcc7`，运行时代码为 `765a12ec4199dfeb490b60b0cfcc47db10bdff14`；上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。本条只审查，生产源码未修改，保留 011 / 012 的既有记录。
- 版本 / 来源：未修改 Linux x64 CLI 2.1.292 / 2.1.295，SDK 0.128.0、runtime v26.3.0，复用并重新校验 012 的固定二进制哈希；重新提取 2,260 / 2,340 个嵌入 JS 模块，与此前 4,600 个解码模块逐字节相同。新增定位结构化输出、停止条件及原生 Q6o 摘要归一化；原函数仅独立执行，不把它计作未修改 CLI。
- 新发现 F1：六模型的 Responses.text.format / Chat.response_format 在最终转换中丢失 schema（12 例），Messages 的等价约束全部保留（6 例）；本地模拟请求均 200。原生 CLI --json-schema 使用 StructuredOutput 工具，另以 EXTRA_BODY 验证显式 format 字段，不混淆两种表示或推断真实生成成功。
- 新发现 F2 / F3：六模型 Chat.stop 全部丢失，而 Messages.stop_sequences 保留；显式 max_tokens=64 经 Chat 转换变成 128，而 Messages / Responses 保持 64。三模型未修改 CLI 的 64 上限捕获与 PCAP 一致。前者是中间结构无 stop，后者是通用 Responses 128 下限被套用于最终 Anthropic 目标；两者所在 Chat 转换器与 release 基线相同，不列为 012 新增回归。
- 新发现 F4：Go TrimSpace 与原生 JS trim 对 BOM / NEL 的处理相反，导致保存的摘要与 CLI 续聊包装不同。两版各三场景 / 18 原生请求，空格对照六轮通过，BOM / NEL 各在首次压缩后的第三条请求被生产恢复管理判为历史冲突；handler 源码映射 409。为原生捕获与生产管理器的两阶段回放，不冒充完整部署。该归一化函数在 release 与当前代码相同，补充此前未覆盖的 Unicode 边界。
- 原生 / 传输：主捕获 228 场景 / 382 请求，错误回放 44 场景 / 116 请求，总计 272 场景 / 498 请求，PCAP 全匹配、零丢包。1,528 个生产 Forward 组合中 1,524 个正文 / wire 全同；四例是晚期 EXTRA_BODY 缺 thinking-display-updates beta，触发现有 display=omitted 清理并正确重算 CCH，明确列为策略差异。48 组四路径 / 日志开关的正文、完整头值 / 头序、归一化握手全同，24 组日志完整性与脱敏通过；605 个独立运行时 CCH 向量与 Go 全同。
- 旧修复复验：普通恢复 292 三模型 / 12 会话 / 96 生成 / 24 计数、295 四模型 / 16 会话 / 128 生成 / 32 计数通过，混入 0。Haiku 5.5 默认值、显式 xhigh、签名往返和原生传输保持有效；错误归一化前后实际 CLI 请求次数、编码序列和退出状态全部一致。另列 Sonnet 5.5 默认 effort 及旧 55 模型 display 的跨入口字段差异，不凭客户端结果推断官方语义。
- 工程 / 文件：service 专项 1,690 个通过事件、五组件包 389 个通过事件、CCH 606 个通过事件；后端 3,332 个源码 / 回归 JSON 文件与冻结快照一致。未重跑全仓库 unit / integration / lint，012 的历史工程结果不算本轮重跑。新增五份审查采集器 / Go overlay、README、[报告](claude-postfix-audit-20261009.md)及[结构化结果](claude-postfix-audit-20261009.json)，.gitignore 放行报告；完整本机材料位于 `claude-capture/postfix-audit-20261009-m7wo9q_4/`。
- 提交 / 范围：本轮新审查尚未提交或推送，后续提交使用 `Claude-Change-ID: CC-20261009-013` 并补记引用；四项新发现未修复。所有模型交互为断网、假凭据及本地合成响应，不代表官方接受、真实签名、OAuth 订阅、计费或完整部署验收。


## CC-20261010-001：显式约束、Unicode 摘要与固定验收合同

- 授权 / 责任：维护者要求完整修复 013 的四项问题，并指出此前反复出现差异。此前覆盖偏重原生转发和常规场景，观察型 PASS 被过度概括；本条以固定断言、独立来源及明确策略边界纠正验收方式，不保证所有未知输入或未来版本等价。保留 013 的失败记录。
- 基线 / 版本：基于 `afb0c04a361a10ac17dc55ea00a573f1b415fcc7`，上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`；CLI Linux x64 2.1.292 / 2.1.295，SDK 0.128.0、runtime v26.3.0，沿用 012 / 013 的已核验二进制及模块哈希。工作开始于 2026-10-09，最终记录于 2026-10-10。
- F1：将 Responses / Chat 的 JSON Schema 转为原生 output_config.format，保留完整 schema，effort 分支不再覆盖 format；已知非法或不支持的模式明确返回 400。第一方转换请求声明源码中的 structured-outputs-2025-12-15，策略禁用 / 生效的 header 覆写移除能力则明确拒绝。原生声明、自定义 origin 及旧版 output_format 路径保持原合同。补查嵌套 schema.default.system 可遮蔽 CCH 定位，仅对需要的生成请求调整真实 system 位置，保留数据；三入口与独立原生运行时的完整字节相同。
- F2 / F3：新增面向 Anthropic 的 Chat 转换器，在模型映射后转换；准确传递 stop 字符串 / 数组及正值输出上限，max_completion_tokens 优先。真正 OpenAI Responses 的旧 128 下限保留，Anthropic 目标不再被中间协议抬高。错误类型、非正上限和不支持的 format 在发送前报 400，不静默删除或改成缺省值。
- F4：摘要内部及整体改用 ECMAScript 空白集合，BOM 去除、NEL 保留，摘要来源与历史完整性校验不变。62 个独立 Q6o JS 向量通过；重新运行两版未修改 CLI 的空格 / BOM / NEL 六场景 / 36 请求，PCAP 全匹配、零丢包，生产恢复序列全通过。
- 固定验收：新增六模型 × 两账号 × 三入口的 192 个约束组合，覆盖 schema、schema+effort、1 / 64 token、单 / 多 stop；补充低上限边界、优先级、非法控制、策略冲突、映射顺序及嵌套 schema。新增硬性验收工具，已证实拒绝旧数据、通过修复后的 102 入口观察与 36 摘要请求。192 个导出请求经真实 HTTPUpstream / TLS 发送，在接收端检查约束并通过 PCAP；不是官方模型输出验收。
- 旧行为回归：1,528 个原生转发组合中 1,524 个原字节相同，四例保留缺 display beta 的既有回退及 CCH 重算。普通恢复 292 12 会话 / 96 生成 / 24 计数、295 16 会话 / 128 生成 / 32 计数通过，跨会话混入 0；不以剔除策略差异的方式宣称全同。
- 工程：最终冻结源码的 58 包 unit、52 包 integration 全通过，golangci-lint 2.14.0 为 0 issues；go mod verify、tidy diff、git diff --check 通过。本机 Docker 集成使用仅作用于新 testcontainers 的临时 IPv4 绑定代理，真实 PostgreSQL / Redis 和原断言保留；首轮一次 Redis 内部启动探测超时已记录，最终完整运行退出 0。代理已清理，未改 Docker 全局配置或业务容器。
- 文件 / 证据：新增目的端约束转换、原生格式结构、结构化能力处理、JS trim、生成 billing 定位修复及永久回归 / 独立向量；新增强制验收和真实发送工具、README、[固定合同](claude-validation-contract.md)、[修复报告](claude-constraint-contract-20261010.md)和[结构化记录](claude-constraint-contract-20261010.json)。本机材料在 `claude-capture/constraint-fix-20261009-qvh78u69/`。准备中纠正一处未使用导入和一处将 API Key 头覆写资格误用于 OAuth 的测试假设，保留相应日志。
- 提交 / 范围：当前未提交、推送、合并或部署；后续提交使用 `Claude-Change-ID: CC-20261010-001` 并补记引用。全部模型交互为隔离假凭据 / 合成响应，不声称官方接受、真实签名、订阅或计费验收；默认策略与未测平台范围见固定合同。

- 2026-10-10 提交 / 推送补记：维护者授权提交推送；CC-20261009-013 审查材料和本条修复、回归、固定验收合同已共同提交为 [7e924048b9e67953dbec015bb535bb846cd2972b](https://github.com/skingford/sub2api/commit/7e924048b9e67953dbec015bb535bb846cd2972b)，携带两个对应的 `Claude-Change-ID` trailer。已推送到 `origin/codex/claude-source-runtime-audit-20261009`，并以远端引用核对成功。通过提交归档重新核验，3,338 个后端源码 / 回归 JSON 文件与最终通过验证的快照完全一致；不重复计算此前的 58 包 unit、52 包 integration 和 0 issues lint。013 的未修复、未提交表述及报告中的发布布尔值保留为当时审查快照，四项缺陷的当前修复状态以本条为准。提交后仅补充本追溯记录；没有新建 PR、合并 release 或部署。

## CC-20261010-002：已推送修复后的工具与推理组合复核

- 授权 / 基线：维护者要求再次根据反编译源码和 Docker 隔离 CLI 全面逐项对比。固定已推送的 `465ed3738afb317b08c68eb08da04af7ee58c2ef`，运行时代码 `7e924048b9e67953dbec015bb535bb846cd2972b`，上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。生产代码保持不变，保留 001 的修复与通过范围，不扩大成全部等价。
- 来源 / 版本：未修改 Linux x64 CLI 2.1.292 / 2.1.295，SDK 0.128.0、runtime v26.3.0，SHA-256 沿用并复核 001 的固定值。重新提取 2,260 / 2,340 个内嵌 JS 模块，与此前全部一致；保存 82 个来源锚点及偏移。分发 JS、独立原生机器码探针和未修改 CLI 分别计数，不称为完整原始 TypeScript。
- F1 / F2：Chat / Responses 丢失 `parallel_tool_calls:false`（72 例）和工具级 `strict:true`（48 例），Messages 对照及原生显式发送保留。工具 strict 不属于 001 所述输出格式包装 strict 的省略规则。源码定位为 Responses→Anthropic 未消费 ParallelToolCalls，工具转换和目标结构不保留 Strict；不以本地发送成功证明模型能力或 beta 授权。
- F3 / F4：旧三模型在 high + 1,025 / 4,096 输出上限时被转换成固定 10,240 的 enabled 预算（24 例）；none 被转换成 enabled / 10,240 而非关闭或明确拒绝（12 例）。原生 Haiku 4.5 的 4,096 / 1,025 预算为 4,095 / 1,024，Sonnet / Opus 4.6 用 adaptive；原生关闭控制为 disabled。原生 Haiku 在不高于 1,024 上限时也保留最低 1,024 预算，这一客户端边界单列，不归因于网关。blame 确认旧工具转换、固定预算及非 low 分支早已存在，不列为 001 修复复发。
- 新验收：新增六模型 × 两账号 × 三入口 × 14 控制的 504 观察；44 个支持边界请求明确 400，132 个组合触发四类失败（重叠计数 72 / 48 / 24 / 12）。460 个成功导出请求（首组 388 加新增上限组 72）经生产 HTTPUpstream 实际 TLS 发送，接收字段及 PCAP 相符、丢包 0；独立判定器对当前基线退出 1，观察测试 PASS 不能替代一致性结论。旧 192 约束发送、102 入口观察和两版新采集 36 摘要请求仍通过原固定判定器。
- 原生 / 错误：新主捕获 357 场景 / 517 请求，错误回放 44 场景 / 116 请求，合计 401 场景 / 633 请求全部 PCAP 匹配、丢包 0。错误的原始 / 转换后实际请求数、编码序列、退出状态全同。2,068 个生产 Forward 组合中 2,060 个逻辑及 wire 原字节相同，错误 0；八例均为两版本 `haiku55-updates-low` 的四配置 display-beta 回退与 CCH 重算。相比 013 的四例只是增加 292 对照，没有新增策略。1,034 个同认证头组合 1,030 个相同，四例仍为空版本补默认值；方法、路径、长度、重放及认证方式核验通过。
- 传输 / 算法 / 恢复：48 个两版 / 三类请求 / 四路径 / 日志组合的正文、完整头值 / 头序及归一化 ClientHello 全同，24 个日志正文、摘要、完整性及两种假凭据脱敏通过。605 个既有独立原生 CCH 向量重新跑 Go 全同；三个新生成嵌套 Schema 请求与 295 原生机器码输出和 PCAP 相同，探针原生前缀重新核验不变。普通恢复 292 的 12 会话 / 96 生成 / 24 计数及 295 的 16 会话 / 128 生成 / 32 计数通过，跨会话混入 0；108 个策略观察留档。
- 工程与范围：service 专项 620 顶层测试 / 2,286 通过事件，五组件包 813 通过事件，CCH 606 通过事件（含顶层）通过。普通专项跳过的环境入口分别记录：两项策略 / 错误和扩展恢复另跑，短恢复包装入口未单独重跑。3,338 个后端源码 / SQL / 模块 / 回归 JSON 与冻结快照一致。没有重跑全仓库 unit / integration / lint，不计入 001 的历史结果。18 个 Responses、19 个 Chat、13 个 Anthropic 强类型字段的盘点保留；OpenAI 专属状态、缓存提示、verbosity、service_tier 等没有完整等价承诺。
- 实验与环境：提取器 Zstd 依赖、探针 profile 拼写、OAuth count 假凭据类型在准备阶段纠正；后者导致初次 16 个 count 头差异，正确认证后对应 16 组全部通过，另 32 组保留原通过结果。后期 Docker 宿主共享目录读取卡住，转为 docker exec 标准流传输到测试容器本地文件系统完成余下检查；网络仍为 none，固定二进制 / 源码 / 断言未变，补回回环域名后重新采集，失败实验保留。全部正式材料已导出，容器内临时副本已清除；Docker 删除仍超时，四个本轮 network-none 实验容器待共享目录恢复后清理，没有重启 Docker 或修改业务容器。
- 文件 / 证据：新增工具控制与 token / effort CLI 采集器、504 观察 overlay、实际发送观察器和硬性失败判定器，更新验证 README / 固定合同 / .gitignore，新增 [逐项报告](claude-tool-reasoning-audit-20261010.md)与[结构化结果](claude-tool-reasoning-audit-20261010.json)。本机完整材料在 `claude-capture/full-recheck-20261010-cgpjnej3/`；70 个关键证据哈希留在结构化结果中。完整内嵌源码、二进制、私钥和大正文不提交。
- 提交 / 限制：本轮审查尚未提交、推送、合并或部署，四项新发现未修复；后续提交使用 `Claude-Change-ID: CC-20261010-002` 并补记引用。全部模型交互是隔离假凭据 / 合成响应，不代表官方接受、真实 thinking 签名、订阅、计费或完整部署验收。HTTP/2、未测平台及所有远程开关未覆盖。

## CC-20261010-003：修复工具并行、strict 与旧模型推理约束

- 授权 / 基线：维护者要求完整修复 002 的四类问题。基于 `465ed3738afb317b08c68eb08da04af7ee58c2ef`，上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`；CLI Linux x64 2.1.292 / 2.1.295、SDK 0.128.0、runtime v26.3.0，沿用并核验 002 的固定源码、二进制与原生捕获。保留 002 的失败证据和范围。
- F1：并行开关映射至原生 tool_choice.disable_parallel_tool_use，保留选择方式和工具名称；缺选择时补 auto，none 与无工具保持原生有效形状，未知选择与显式并行控制冲突时报错。true / false 均有断言，不只检查禁止并行。
- F2：目标工具结构和转换器保留 strict。strict=true 的 schema 不经过摊平联合或补空 schema，保留原内容；非 object 根结构及不支持 strict 的 server tool 明确拒绝。第一方普通请求对工具 strict 和输出 Schema 一并声明已测结构化能力，账户过滤或有效头覆写移除能力时明确 400。原生保留与自定义 origin 边界不变；不把字段 / beta 发送当作提供方授权证明。
- F3 / F4：旧 Sonnet / Opus 4.6 显式非 low 推理采用已测 adaptive；其他手动预算按已有限额约束，至少 1,024 且小于 max_tokens。none 明确 disabled，不能再生成 10,240 预算及 effort=none，Schema 同时保留。5.5 的既有处理保持；未知 effort、强制工具与启用推理冲突明确拒绝。较大手动上限的 effort 档位映射保留，不宣称所有缺省预算与 CLI 相同。
- 固定验收：原 504 输入完整保留并进入永久 service 回归。新增八个不可同时满足 high / 最低预算与 64 / 1,024 上限的 Haiku 4.5 转换请求明确 400；连同原 44 个边界共 52 拒绝，其余 452 成功，约束失败 0。没有提高显式上限、关闭所要求的推理或跳过失败输入来凑通过。独立判定器增加 true 开关、strict 能力、准确 thinking 类型与预算下限检查，仍拒绝旧基线证据。
- 实际发送 / 回归：新合同 452 和旧合同 192 个请求经真实 HTTPUpstream / TLS，接收约束与 PCAP 全匹配、丢包 0。旧 102 入口 / 36 摘要合同通过。2,068 个旧原生捕获 Forward 组合中 2,060 原字节相同、错误 0，八个 display-beta 策略差异仍为原场景。两版新跑普通托管恢复：292 为 12 会话 / 96 生成 / 24 计数，295 为 16 / 128 / 32，跨会话混入均为 0。
- CCH：全部 226 个带 billing 的新生成请求，恢复零占位符后由独立 295 原生机器码探针计算，最终完整字节与 Go 相同，PCAP 匹配且丢包 0。原版二进制 SHA-256 和探针原生前缀重新校验；入口修改的探针仍不计作未修改 CLI。
- 工程：冻结 3,341 个源码 / SQL / 模块 / 回归 JSON 文件后运行，工作区匹配。全量 unit 58 包、22,994 个通过事件，失败 0；golangci-lint 2.14.0 为 0 issues；模块校验与 tidy diff 通过。52 个 integration 测试包编译成功，但数据库测试尚未执行；不能借用 001 的历史 52 包结果。准备时纠正一处测试局部变量声明及格式化命令目录，不修改断言规避功能失败。
- 环境 / 剩余验证：Docker 继承 002 的共享目录 / 容器启动故障，协议测试通过标准流把文件放入本轮隔离容器本地文件系统完成。完整数据库测试依赖新 PostgreSQL / Redis 容器；已请求维护者确认重启 Docker，因为会短暂中断 digital-human-postgres、picpak_postgres、picpak_redis 三个既有业务容器。尚未获得确认，没有擅自重启，也未改业务容器。
- 文件 / 证据：新增 `anthropic_tool_reasoning_constraints.go` 及单测、永久 504 组 service 合同和 strict 能力策略测试；更新请求转换、工具类型、结构化能力判断及独立判定 / wire 工具。更新 README、固定合同、.gitignore，新增 [修复报告](claude-tool-reasoning-fix-20261010.md)与[结构化结果](claude-tool-reasoning-fix-20261010.json)。本机材料在 `claude-capture/tool-fix-20261010-k99esgbp/`，源码、原始观察和抓包分开留档。
- 提交 / 限制：本轮尚未提交、推送、合并或部署；后续提交使用 `Claude-Change-ID: CC-20261010-003` 并补记引用。四类代码缺陷及核心协议验收已修复通过，完整数据库 integration 仍待上述环境确认。所有模型交互仍是隔离假凭据 / 合成响应，不代表官方接受、真实签名、订阅或计费验收。

- 2026-10-10 授权与最终验收补记：维护者明确回复“可以重启”。正常停止三个原数据库容器后退出并重启 Docker，恢复同一容器 ID 和 unless-stopped 策略，三者均 healthy，测试结束后复验仍健康。四个卡住的旧实验容器已清除。对同一 3,341 文件源码快照实跑 `go test -p 2 -tags=integration -json ./...`（CI=true），真实 PostgreSQL / Redis、原断言和仅面向新 testcontainers 的临时 IPv4 绑定代理：52 包、13,735 个通过事件、失败 0、退出码 0。测试容器及代理 / socket 已清理，最终只保留原有三个业务容器。结合此前 58 包 unit、0 issues lint、约束及原生协议验证，本轮四项修复验收完成；上述“待确认 / 未执行”保留为初轮状态。本补记只更新环境与验证记录，未修改生产代码，未提交、推送、合并或部署。

- 2026-10-10 提交 / 推送补记：维护者授权提交推送；CC-20261010-002 审查材料与本条修复、永久回归及完整验收记录共同提交为 [fd4d400090fcfdb55dc89700bb7d4332991b232c](https://github.com/skingford/sub2api/commit/fd4d400090fcfdb55dc89700bb7d4332991b232c)，携带两个对应的 `Claude-Change-ID` trailer。已推送至 `origin/codex/claude-source-runtime-audit-20261009`，远端引用核对一致。通过提交归档再次核验，3,341 个后端源码 / SQL / 模块 / 回归 JSON 文件与最终通过验证的快照逐文件 SHA-256 相同，记录保存在本轮证据目录的 `analysis/commit-source-verification.json`；此前 58 包 unit、52 包 integration、0 issues lint 及协议验证对应本提交源码，不重复计作新测试。002 的未修复与待清理状态、003 的初轮待验证状态及报告中的未提交 / 未推送布尔值保留为历史快照，当前修复、环境恢复和交付状态以本条两次补记为准。本次后续提交仅补充追溯记录，没有新建 PR、合并或部署；隔离验证仍不代表真实官方接受、签名、订阅或计费验收。

## CC-20261010-004：已推送工具修复后的内容与响应逐项审查

- 授权 / 基线：维护者要求再次根据提取源码与 Docker 隔离 CLI 全面深入逐项对比。固定 `1004275707dc5b6b9b17ecf8dee27346696fe23d`，运行时代码 `fd4d400090fcfdb55dc89700bb7d4332991b232c`；上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。本条不修改生产逻辑，不同步上游；保留 002 / 003 的历史证据。
- 来源 / 版本：未修改 Linux x64 CLI 2.1.292 / 2.1.295，SDK 0.128.0、runtime v26.3.0，二进制 SHA-256 与 003 的固定值相同。重新提取 2,260 / 2,340 个 JS 模块，4,600 个与此前逐字节一致；不是完整原始 TypeScript。冻结的 3,341 个后端源码 / SQL / 模块 / 回归 JSON 与最终工作区相同。
- 旧修复：504 个工具 / 推理输入维持 452 成功、52 明确拒绝、约束失败 0；192 个显式参数合同、102 个入口观察及两版 36 个摘要请求通过。旧完整 504 样本经增强判定器仍有 180 个失败，不将先前较弱判定器的计数混用；首次选到的 432 项初期样本被完整性门槛拒绝，过程留档。
- 新 F1–F5：Chat developer 降为 user（12 组合）、普通 / 工具结果 URL 图片丢失（48）、旧式 function_call 历史及结果丢失（12）、非法工具参数被忽略 marshal 错误后变成 content:null 仍发送（24）、Responses 工具结果内 input_file 文档丢失（12）。实际生产构建器与 TLS 接收端均复现；原生显式字段对照保留，base64 图片、普通文档及普通工具对照正常。源码 blame 表明相关分支早已存在，不列为 003 的回归。
- 新 F6 / 范围纠正：旧三模型 Responses 入站的 opaque envelope 被丢弃（12 组合）；出站 22 个含签名 / redacted 的源响应，在转换函数与真实 buffered / streaming 处理器的四路径共 88 个比较丢失不透明值。另有六模型 44 个 Chat 组件往返全部丢失签名 / data，包含 5.5。由此明确纠正 [CC-20261009-012](claude-295-compatibility-fix-20261009.md) 的笼统范围：已验证的签名保留仅适用于 5.5 Responses，不包括 Chat 的完整历史往返。保留原记录，不声称这些合成签名有效或已经观察到官方拒绝。
- 新 F7：旧三模型非流式响应先输出 function_call、最后输出缓存的 message，和流式的文本 / 工具顺序不同。另加两版各六个不含 thinking 的 text→tool 最小场景，六个旧模型源响应在转换函数 / buffered 处理器共 12 个比较失败；5.5 对照正常。连同主响应语料共 36 个文本 / 工具顺序差异，部分与 F6 重叠。
- 内容 / 响应分母：新内容矩阵 540 适用输入、528 实际发送、12 已知采样拒绝；153 原始差异中 33 为既有 OAuth system 包装 / redacted prefilter 政策，120 为上述 F1–F6。原生内容 180 请求的显式字段全同。主响应 797 个加最小对照 24 个，合计 821 个独立组装响应、3,284 个四路径比较、100 个有差异；Chat 44 个往返另计。既有空工具结果替代、原生 tool/text 续聊归一化及停止原因映射均单列，不改变原分母凑通过。
- 全量原生 / 真实发送：689 个新跑场景、1,005 条请求 PCAP 全匹配、零丢包；响应专项 108 场景 / 192 条 SSE 返回字节也匹配、零丢包。3,556 个 Forward 组合中 3,532 个逻辑 / wire 全同，16 个已有 Sonnet 5.5 参数保护明确 400、8 个已有 display-beta 回退。成功同认证头 1,770 组中 1,766 相同，四个为空版本补默认值；无新增未解释的原生转发差异。旧 / 新合同共 1,172 次 HTTPUpstream / TLS 发送，PCAP 全同，新内容接收字段仍检出相同 120 个缺口。
- 传输 / 算法 / 恢复：48 组四路径 / 日志开关的正文、完整头值 / 头序及归一化 ClientHello 全同，24 组日志完整性和两种假凭据脱敏通过。264 个新增正文与新建 295 原生机器码 CCH 探针及 PCAP 全同，探针原生前缀保持不变，仍不算未修改 CLI。44 个错误回放场景 / 116 请求的次数、编码序列、退出结果前后相同。292 恢复 12 会话 / 96 生成 / 24 计数，295 为 16 / 128 / 32，跨会话混入 0。
- 工程 / 环境：服务专项 827 个顶层 / 2,563 个通过事件，四组件 unit 1,840 个通过事件；环境跳过与专用另跑逐项记录。没有重跑全仓库 unit / integration / lint。准备阶段纠正 probe 的 internal 导入位置、Go 工作目录及不存在的组件包路径，保留过程。所有本轮实验容器已清理，原三个业务容器均 healthy，本轮未重启 Docker 或修改业务容器。
- 文件 / 证据：新增内容 / 响应原生采集器、540 项及响应处理器观察 overlay、Chat opaque 组件探针、三个独立判定器和响应 PCAP 核对器；扩展 wire 接收字段，更新 README、固定合同及 .gitignore。报告为 [内容与响应审查](claude-content-stream-audit-20261010.md)及[结构化结果](claude-content-stream-audit-20261010.json)，本机材料在 `claude-capture/post-tool-audit-20261010-ku0u3z0l/`，115 个证据哈希及 11 个工具哈希留档。完整提取源码、二进制、私钥和大正文不提交。
- 提交 / 限制：七类缺口尚未修复，三个新增判定器返回 1；当前审查材料未提交、推送、合并或部署。后续提交使用 `Claude-Change-ID: CC-20261010-004` 并补记引用。所有模型交互均为隔离假凭据 / 合成响应；没有真实官方接受、签名有效性、订阅、计费或完整部署验收。其他平台、HTTP/2、全部远程开关和未列协议字段不在通过范围内。

## CC-20261010-005：修复内容丢失与签名 / 响应往返

- 授权 / 基线：维护者要求完整修复 004 的七类问题。基于 `1004275707dc5b6b9b17ecf8dee27346696fe23d`，上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`；保留 004 的失败记录，不同步上游、不改写历史。CLI 固定 Linux x64 2.1.292 / 2.1.295、SDK 0.128.0、runtime v26.3.0，沿用并复核 004 的二进制 SHA-256 与源码证据。
- F1–F5：Chat developer 保留到中间协议并正确进入 Anthropic system；普通 / 工具结果支持 HTTP(S) URL source；旧式 function_call / function 结果配对，并为同名多轮调用生成不同 ID、避开已有 ID；非法或非 object 工具 JSON 明确 400、编码错误不再忽略；工具结果的 input_file 保留为 document。不能转换的明确媒体来源（含 foreign file_id-only）改为报错，原测试输入保留，不以删文件获得成功。
- F6 / F7：Responses 六模型均保留标记过的 signed / redacted envelope；普通 OAuth 有效 redacted data 不再因旧模型门槛丢失，无效签名和显式禁用的已有处理仍验证。非流式按原 block 顺序输出，只合并相邻文本。Chat buffered 补上 signature_delta 累加；两种返回模式使用 `anthropic_content` 字符串承载完整块和实际可见对应值，下一轮恢复原始签名 / data。工具别名还原不修改不透明字节，分片与模型别名有永久回归。
- Chat 兼容合同：客户端必须保留并原样回传 `anthropic_content`；流式在 finish 前发送一次字符串增量，普通无签名响应不增加该字段。正文 / 工具历史冲突、坏编码、无效块或将其交给普通 Chat→OpenAI Responses 转换均明确拒绝。该字段是 Base64 编码而非加密，也不证明签名有效；删除它后无法从文本恢复签名。此限制不能被省略为“所有现有 Chat 客户端无需保留元数据也能完整往返”。详见修复报告。
- 固定内容合同：原 540 输入完整保留并加入永久 service 测试，504 成功、36 明确拒绝（原 12 采样 + 新 24 非法工具 JSON），未解决差异 0。36 个 OAuth system / developer 包装差异保留，必须有实际包装和确认消息才归类；旧 redacted 豁免取消。原 504 工具 / 推理输入仍为 452 成功 / 52 拒绝，192 参数请求、102 入口观察、36 摘要请求通过。
- 返回与历史：同一 821 个捕获响应经非流式 / 流式转换函数及实际 buffered / streaming 处理器，共 3,284 比较全部通过。44 个 Chat 组件往返及 176 个两模式 / 两账号实际处理器到下一轮请求入口的往返，签名 / data 丢失和转换错误均为 0。新增大整数、同名旧式函数、模型 / 工具别名、历史冲突、分片签名和重复完成事件回归；负控制仍拒绝旧内容 123 个未解决差异、旧响应 88 + 12 差异以及旧 Chat 44 个丢失。
- 实际发送 / 原生回放：1,324 个最终生产请求通过真实 HTTPUpstream / TLS，接收端内容与 Chat 不透明历史判定均通过，PCAP 全同、零丢包。复用 004 的已核验原生捕获执行 3,556 个 Forward 组合，分类与原记录相同：3,532 原字节相同，16 已有模型拒绝、8 已有 display-beta 回退；同认证成功头 1,770 组中 1,766 相同，四个为空版本补值。未将语料复放计作重新采集 689 场景。
- 算法 / 传输 / 恢复：662 个生成正文与独立 295 原生机器码 CCH 探针及 PCAP 完整字节相同，丢包 0；重验官方二进制和探针原生前缀，入口修改的探针不算未修改 CLI。48 组传输的正文、完整头值 / 顺序、归一化 ClientHello 全同；24 组日志完整性、摘要、两种假凭据脱敏通过。重新运行未修改 CLI 托管恢复：292 为 12 会话 / 96 生成 / 24 计数，295 为 16 / 128 / 32，跨会话混入 0。
- 最终工程：同一最终 3,347 文件快照上，完整 unit 58 包 / 23,026 通过事件，完整 integration 52 包 / 13,752 通过事件，均失败 0、退出 0；官方 golangci-lint 2.14.0 为 0 issues，模块校验与 tidy diff 通过。工作区逐文件 SHA-256 与最终快照一致。初轮定位并修正一个未再调用的包装函数和一处 strings.Builder 恒 nil 错误返回值的显式处理，分别保留初轮日志，重新冻结后完成最终工程及协议检查，没有降低断言。
- 环境：数据库集成使用真实临时 PostgreSQL / Redis 与仅作用于新 testcontainers 的 IPv4 Docker API 代理；所有实验容器、代理和 socket 已清理，原 digital-human-postgres / picpak_postgres / picpak_redis 三容器保持同一 ID、均 healthy。本轮没有重启 Docker 或修改业务容器。
- 文件 / 证据：17 个后端实现 / 测试 / fixture 文件；追加 Chat 网关历史观察器，更新独立内容判定、README、固定合同与 .gitignore。[修复报告](claude-content-stream-fix-20261010.md)和[结构化结果](claude-content-stream-fix-20261010.json)记录 141 个最终证据哈希及测试二进制哈希。初轮材料在 `claude-capture/content-fix-20261010-iqw0orzj/`，最终材料在其 `verified/` 子目录；完整提取源码、二进制、TLS 私钥和原始大正文不提交。
- 提交 / 限制：本轮七项修复已完成，当前代码及审查材料未提交、推送、合并或部署。后续提交使用 `Claude-Change-ID: CC-20261010-005`（004 审查材料同交付时同时关联）并补记哈希。所有模型交互为隔离假凭据 / 合成响应，未验证真实官方接受、签名有效性、订阅或计费；已有政策、未测平台、HTTP/2 和未列协议字段不扩大为完全等价。

- 2026-10-10 提交 / 推送补记：维护者授权“提交 推送”；CC-20261010-004 审查材料与本条七项修复、永久回归和最终验证记录共同提交为 [6f654aa1d52dd4249ff270df80897d67c4963ec6](https://github.com/skingford/sub2api/commit/6f654aa1d52dd4249ff270df80897d67c4963ec6)，携带两个对应的 `Claude-Change-ID` trailer。已推送至 `origin/codex/claude-source-runtime-audit-20261009`，远端完整提交引用核对一致。通过提交归档再次核验，3,347 个后端源码 / SQL / 模块 / 回归 JSON 文件与最终通过验证的快照逐文件 SHA-256 相同，记录保存在本轮最终证据目录的 `analysis/commit-source-verification.json`；此前 58 包 unit、52 包 integration、0 issues lint、540 项内容合同、3,284 次响应比较、176 次 Chat 处理器往返及 1,324 次 TLS 发送验证对应本提交源码，不重复计作新测试。004 的未修复状态及两份报告中的未提交 / 未推送字段保留为历史快照，当前修复和交付状态以本条及补记为准。Chat 客户端仍须原样回传 `anthropic_content` 才能保留签名历史。本次后续提交仅补充追溯记录，没有新建 PR、合并或部署；隔离验证不代表真实官方接受、签名有效性、订阅或计费验收。

## CC-20261010-006：可选保留 API 调用方内容的 OAuth 转换策略

- 授权 / 基线：维护者要求降低“官方订阅号通过 sub2api 转成 API”路径中网关额外引入的特征，并明确要求开始处理。沿用当前工作分支 `codex/claude-source-runtime-audit-20261009`，代码基线 `a426396d8`；Claude 集成上游基线仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。没有合并 main 或改写历史。
- 版本 / 证据：沿用既有 Linux x64 CLI 2.1.292 / 2.1.295、SDK 0.128.0、runtime v26.3.0 的兼容边界。依据本地生产源码中的固定 system→messages 包装、确认语、工具名映射和缓存重写，以及 004 / 005 的原始内容合同；本轮不新增官方 CLI 抓包或官方行为等价声明。
- 改动：新增默认关闭的 `gateway.claude_oauth_preserve_caller`，环境变量 `GATEWAY_CLAUDE_OAUTH_PRESERVE_CALLER`。启用后调用方 system 原文 / 块顺序 / 缓存属性留在 system，不生成固定确认对话或通用扩充，不替换 OpenCode 身份句，不混淆工具名、不补工具缓存断点、不覆盖已有 messages 断点或 TTL，不做旧日期规范化。总 system 注入开关开启时仍保留既有 billing / 身份前缀，不声称它们是提供方必需条件。
- 范围 / 拒绝：Messages、Chat、Responses 及 count_tokens 使用一致的保留策略；超限缓存及 thinking 上的断点明确 400，不静默删除。原生请求与 API Key 路径不启用该策略。已有参数、能力、会话归属、签名历史与单次拒绝转发规则继续执行。旧自定义 system / 缓存设置在新模式下的优先级及新会话要求见[操作说明](claude-caller-preservation.md)。
- 合同：新增策略测试并在新模式复用 540 个输入；旧模式保留原 540 预期。独立内容判定器读取 `preserve_caller` 标志，新模式不能沿用旧 OAuth 包装豁免；不删除历史失败证据。配置测试覆盖 YAML、环境变量及回退，四种 Compose 增加变量透传。
- 初轮验证：配置及内容场景执行后，新增原生样本断言发现测试侧把具名 JSON 字节类型与 `[]byte` 直接比较；已改成显式 `[]byte` 的逐字节断言，未放宽内容预期。系统 PATH 中 lint 二进制由 Go 1.26 构建，无法检查 Go 1.27.2 项目，改用此前已验证的 golangci-lint 2.14.0。初轮日志保留在本机证据目录，最终结果后补。
- 文件 / 证据：生产配置、统一调用方策略、四入口和 OAuth normalizer；对应 unit / 内容合同、独立判定器、四种 Compose、配置示例、固定合同与操作说明。本机证据：`/Users/kingford/claude-capture/caller-preservation-20261010-j2kAol/`。
- 提交 / 边界：尚未提交、推送、合并或部署，未来提交使用 `Claude-Change-ID: CC-20261010-006` 并补记提交 / PR 引用。全部请求和响应验证使用本地合成数据；没有真实提供方接受、签名有效性、OAuth 订阅或计费验证，也不承诺不可识别。

- 专项验证补记：新模式 540 个输入完整保留，504 个成功 dispatch、36 个既有非法工具参数 / 模型采样拒绝，独立内容判定器差异 0、退出 0。把其中 36 个 system / developer 场景故意改回旧包装后，独立判定器退出 1，36 个均为未豁免失败，已保存负向样本；这不是官方请求样本。系统块、六工具名称与历史、四断点、count_tokens、注入关闭、缓存非法请求发送前拒绝和 403 只发送一次的专项通过，12 份原生样本正文逐字节保持。配置默认 / YAML / 环境变量 / 回退及四种 Compose 变量透传通过。后端 3,350 个源码 / SQL / 模块 / 回归 JSON 文件已冻结并核对无变动，工程检查继续在该源码上运行。

- 最终工程补记：冻结源码上 `GOTOOLCHAIN=go1.27.2 go test -tags=unit -json ./...` 退出 0，58 包、23,068 个通过事件、失败 0；golangci-lint 2.14.0 全量检查退出 0，0 issues。`CI=true go test -p 2 -tags=integration -json ./...` 退出 0，52 包、失败 0，使用新建 PostgreSQL / Redis 测试容器和只为 testcontainers 绑定本机 IPv4 的临时 Docker API 代理；既有业务容器未重启或修改。代理已停止，socket 已删除。完成后 3,350 个后端文件 SHA-256 与冻结清单一致，摘要、日志哈希和命令保存在本机 `validation-summary.json`。本次没有实际官方请求、没有新增传输等价声明，未提交、推送、合并或部署。

## CC-20261010-007：两版 CLI 实际模式与调用方保留政策复核

- 授权 / 基线：维护者明确以编译产物提取源码和 Docker 运行对齐官方 CLI，并要求开始验证。审查 `a426396d8` 加本工作区未提交的 006；上游 Claude 集成基线仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。未修改后端，3,350 个源码 / SQL / 模块 / fixture 与 006 冻结哈希一致；不合并 main，不改写 006 的历史结果。
- 来源：Linux x64 CLI 2.1.292 / 2.1.295，SDK 0.128.0、runtime v26.3.0；二进制 SHA-256 分别为 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3` 和 `4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358`。逐一核对二进制中 2,260 / 2,340 个 JS 模块的压缩区间哈希、解码哈希和已有提取文件，4,600 个全同；不称为原始 TypeScript，也未修改 CLI 二进制。
- 新捕获：两版本 × 10 个 print 场景加两版各一个真实 PTY 场景，共 22 场景、62 条生成 / count 请求，接收字节与 PCAP 全同、零丢包。覆盖默认 / 替换 / 追加 system、默认工具、真实注册的本地 MCP、续聊、恢复、503 重试和 /context。PTY 使用隔离测试 HOME、预写 onboarding / trust、假 OAuth 环境令牌，收到回复后主动 SIGTERM（143）；明确不是真实订阅登录或自然退出成功。
- 模式发现：print 自动标记 sdk-cli，交互入口为 cli；默认、替换与追加 system 的身份 / 缓存布局有实际差异。普通转换的 cli UA 可对应交互入口，不能与 print 样本混成一份默认模板。两版默认四块 system 内容已有差异，旧固定 1,541 字符扩充块的“2.1.x 一致”注释不构成版本依据。
- 006 差异：36 个原生 count 探针作为普通 API 输入时，旧模式 36 / 36 JSON 内容相同，开启 006 后 36 / 36 system 改变。计数路径无条件加前缀不是 CLI 对齐证据；没有实际 token 值差额测量。新模式继续默认关闭，操作文档添加链接跟进，未在本次修改生产策略。
- 工具差异：六条 MCP 转换请求的 36 个原名，旧模式全部改写、新模式全部保留；Chat 的两版本 × 两模式共四条请求中，24 个原先省略 strict 的工具出现 false。归因至 Chat→Responses 的 `defaultStrictFalse` 与后续 Anthropic 转换；不能为修复这个目标而全局破坏 OpenAI Responses 默认语义。条件 diagnostics / beta 及缓存布局差异另列，不将随机 ID、显式实验超时或未请求流式视作缺陷。
- 转发 / 传输：128 个派生输入 × 两种模式产生 256 个观察，全部发送且无运行错误；其中 62 个原生请求 × 两模式的 124 组，逻辑正文原字节、实际出站头值 / 头序全部一致，凭据值单列替换。当前源码编译的生产 HTTPUpstream 发送全部 256 组（112 生成 / 144 计数），PCAP 全同、零丢包；256 个 ClientHello 均匹配已有归一化 Linux pin。输入标记未泄漏为出站 X-Sub2API-* 头。
- 验证 / 限制：已有五组 profile / 版本专项通过，共 24 个通过事件。初轮观察 overlay 闭合括号、fixture 工作目录和仅覆盖 messages 的旧 PCAP 判定器问题已纠正，日志保留；新判定器保留全部 count 样本。未重跑全仓 unit / integration / lint；未读取线上配置，未扩大到其他平台、HTTP/2、TLS 恢复、全部远程开关、真实签名、订阅或计费。
- 文件 / 证据：新增两类 CLI 采集器、Go 观察 overlay、两个独立判定器，更新验证 README、固定合同、006 操作说明、.gitignore；[报告](claude-cli-alignment-audit-20261010.md)与[结构化摘要](claude-cli-alignment-audit-20261010.json)保存分母和哈希。本机材料：`/Users/kingford/claude-capture/cli-alignment-recheck-20261010-zOECmb/`。实验容器已清理，原三业务容器保持同一 ID 且 healthy，未重启 Docker。
- 提交 / 状态：本轮新发现尚未修复，尚未提交、推送、合并或部署；后续提交使用 `Claude-Change-ID: CC-20261010-007` 并补记引用。006 的内容保留合同通过与本轮 CLI 差异同时保留，不以任一结论覆盖另一份证据。

## CC-20261010-008：修复计数 / strict 差异并确立 CLI 自定义 system 基线

- 授权 / 基线：维护者要求修复 007 已确认差异。继续当前分支 `codex/claude-source-runtime-audit-20261009`，基于 `a426396d8` 加尚未提交的 006 / 007；上游 Claude 集成仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，未同步 main，未改写旧记录。
- 来源 / 版本：沿用 007 已核验的 Linux x64 CLI 2.1.292 / 2.1.295、SDK 0.128.0、runtime v26.3.0 和二进制 pin。新增两版真实 PTY 的 replace / empty / append 三种 system 选项，使用隔离 HOME、假 OAuth、断网 Docker 与合成响应；按原捕获工具校验 PCAP。两版 custom 分支的身份和原文块均为 1h 缓存，空 custom 仅身份块缓存，append 仍为完整默认四块。006 的计数一致性假设不再沿用。
- 修复：count_tokens 删除生成前缀包装，保留调用方计数正文；Chat→Anthropic 保留工具 strict 的省略 / false / true，Chat→真正 OpenAI Responses 继续原默认策略。普通 API 对齐模式以交互式 CLI 的自定义 system 分支为基线，默认 / append / SDK 的原生请求继续透传；不复制依赖 CLI 工具环境的完整默认提示词到任意 API 调用。
- 缓存 / 工具：字符串或单一无显式缓存文本块采用实测身份 / 自定义文本的 1h 断点；多块或显式缓存按调用方保留，自动默认不挤占已有断点，超限仍明确拒绝。对齐模式不执行工具名混淆及工具断点注入。配置与四种 Compose 默认改为 true，已有显式 false 保留旧策略回退，切换后新建会话；未修改线上环境或账号。
- 固定验收：保留 007 的 128 输入 / 256 观察，新增独立 `validate_cli_alignment_fix.py`；固定检查原生 124、普通计数 72、Chat 工具 strict 省略 24、自定义 system 布局 30、MCP 名称六组。独立 CLI fixture 只保存本地自定义文本、身份 / 缓存、UA 与捕获哈希，不从网关输出生成预期。修复前真实 TLS 记录检出 68 个失败案例；修复后构造层相同分母全部通过，完整原比较仍记录默认 / append 应用状态等差异，不声称全请求等价。
- 工程 / 文件：修改计数路径、目的端 strict 默认处理、自定义 system 布局与配置默认；增加 strict 三态 / 目标隔离测试和原生 custom fixture / 三入口布局回归，保留已有内容合同。扩展 TTY 采集器，新增独立修复判定器，更新部署示例、README、固定合同及操作文档。[修复说明](claude-cli-alignment-fix-20261010.md)记录策略优先级、回退与边界；后端 3,352 个源码 / SQL / 模块 / 回归 JSON 文件已冻结，完整工程与真实传输结果后补。
- 证据 / 状态：本机 `/Users/kingford/claude-capture/cli-alignment-fix-20261010-VHHR1K/`。007 原 TTY 采集器源码已按其原 SHA-256 保存，旧数据与失败记录保留。尚未提交、推送、合并或部署；后续提交使用 `Claude-Change-ID: CC-20261010-008`。不声称真实签名、OAuth 订阅、计费或官方接受验证。

- 最终验证补记：新增 6 个 TTY 场景 / 12 条生成请求的 PCAP 全匹配、零丢包；同一 128 输入 / 256 观察及真实 TLS 记录均通过固定修复判定器，失败 0，修复前记录仍检出 68 个失败。72 个普通计数保持输入，24 个 Chat 工具 strict 省略正确，30 个自定义 system 布局与新增 CLI fixture 相符，六组 MCP 共 36 个工具名保留；显式 false 回退模式的其他已知差异仍保留在原比较中。
- 传输 / CCH：256 次实际生产 HTTPUpstream 发送（112 生成 / 144 计数）与 PCAP 全同、零丢包，256 个归一化 ClientHello 匹配原 Linux pin；124 个原生转发正文、完整头值 / 头序仍一致，无 X-Sub2API-* 泄漏。112 个带 CCH 正文与新建的独立 295 原生运行时探针完整字节相同，PCAP 校验通过；探针只替换入口，原生前缀一致，明确不计作未修改 CLI。
- 工程 / 交付状态：冻结的 3,352 文件源码上，完整 unit 58 包 / 23,089 通过事件、integration 52 包 / 13,764 通过事件，均失败 0、退出 0；golangci-lint 2.14.0 全量 0 issues。配置、strict 三态 / 两目标、三入口 custom 布局及四种 Compose 检查通过。测试后源码 SHA-256 与冻结清单全同，临时集成代理 / socket 和实验容器已清理，原三个业务容器同 ID 且 healthy。[结构化结果](claude-cli-alignment-fix-20261010.json)保存分母与证据哈希。本轮没有提交、推送、合并或部署；普通 API 采用明确的 custom 分支，不宣称完整默认 / append 应用状态、所有远程开关或真实提供方接受等价。

- 2026-10-10 提交 / 推送补记：维护者授权“提交 推送”；006 的内容保留策略、007 的源码 / Docker 审查及本条最终修复共同提交为 [af2b8d8549e90eb59dae28d5f22181a297878541](https://github.com/skingford/sub2api/commit/af2b8d8549e90eb59dae28d5f22181a297878541)，同时带 006 / 007 / 008 三个 Claude-Change-ID trailer。已推送至 `origin/codex/claude-source-runtime-audit-20261009`。通过 Git 提交归档重新核验，3,352 个后端源码 / SQL / 模块 / 回归 JSON 文件与最终验证清单逐文件 SHA-256 相同，记录在本轮证据目录 `commit-source-verification.json`；没有重复计算此前测试结果。报告中的未提交 / 未推送布尔值及旧状态保留为各阶段快照，当前交付状态以本条为准。本次后续文档提交只补充引用，不修改后端；未创建 PR、合并 release 或部署。

## CC-20261010-009：版本发现与自动生效的已验证配置分离

- 授权 / 基线：维护者要求继续优化版本同步，避免发现未验证的新 CLI 后造成转换拒绝。基于当前分支 `f3d98866a8ec3ad96954edff356ff41305781098`；上游 Claude 集成仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，不合并 main。沿用 2.1.292 / 2.1.295、SDK 0.128.0、runtime v26.3.0 的已测默认 Linux x64 配置，不扩大协议范围。
- 依据：同步服务会将最新稳定 tag 写入 synced，旧运行时直接选用它；转换守卫只允许两个已测版本。故“自动发现未验证版本 → 本地转换拒绝”是代码条件冲突；本轮使用模拟 GitHub release 复现，不声称线上已经发生。
- 修改：新增共享版本准入表，自动选取不高于发现值的最高已验证配置；没有匹配项时保留原环境 / 内置回退。synced 继续记录完整发现值，未更改数据库字段、关闭 / 失败保留与单向发现语义。手动固定仍优先，未验证显式配置仍由原守卫明确拒绝，不静默替换。设置读取与页面显示使用同一选择逻辑。
- 可见性：管理 API 增加只读基础版本和是否已验证字段，页面区分发现与选择，并提示未验证显式配置；保存不回写派生字段，旧后端缺字段时不编造状态。同步日志记录已验证自动候选。基础版本不等于各模型 / 各账号最终传输选择。
- 回归：准入表区间 / 副本、已有未来发现值、缓存失效与进程重建、四入口实际构建为已验证 295、手动未知版本 400 且不发送、每个准入项的 UA / 归因 / CCH / 传输完整性；沿用原同步测试。后端专项、前端组件 / i18n 49 项及类型检查通过，完整后端工程结果后补；没有修改测试输入来把未验证版本当作通过配置。
- 文件 / 证据：`pkg/claude` 准入表、版本选择 / 同步、转换守卫、设置视图 / DTO / 管理响应、对应测试、前端管理页与中英提示。[说明](claude-version-activation.md)；本机 `/Users/kingford/claude-capture/version-activation-20261010-p8HC8o/`，冻结后端 3,355 个源码 / SQL / 模块 / JSON 及五个前端改动文件。没有新 CLI 二进制或抓包；不重复计算 008 的传输 / CCH 结果。
- 提交 / 边界：尚未提交、推送、合并或部署，未来提交使用 `Claude-Change-ID: CC-20261010-009` 并补记引用。未读取或修改线上账号 / 配置，未验证真实提供方接受、订阅资格或计费；模拟发现版本不表示官方当前发布状态。

- 最终验证补记：初轮完整 unit 检出两份管理设置 API 契约缺少新增只读字段；补入精确字段和值后复跑完整 unit，58 包、失败 0、退出 0，未放宽响应比较。集成测试 52 包通过，golangci-lint 2.14.0 为 0 issues；初轮冻结后唯一后端改动是 unit 标签下的 API 契约预期，已用 Go 包元数据确认它不参与 integration / lint 编译，生产与集成测试源码未变。前端补充了响应缺失字段时清除旧版本状态的回归，最终组件 / i18n 共 50 项通过，类型检查通过。
- 证据补记：最终 3,355 个后端文件和五个前端改动文件与最终哈希清单一致，初轮日志、仅 unit 测试文件的差异和最终结果分别留档；临时集成代理 / socket 已清理，原三业务容器同 ID、均 healthy。[结构化结果](claude-version-activation-20261010.json)记录命令结果、分母及日志哈希。没有新 CLI 运行、协议 / TLS 模板改动或真实提供方验证，也没有提交、推送、合并或部署。

- 2026-10-10 本地提交补记：维护者授权“提交”；本条版本发现 / 自动选择修复、管理页状态、回归与验证记录保存为本地提交 `c156466b0443667a598ac49571ef651359f528df`，携带 `Claude-Change-ID: CC-20261010-009`。通过 Git 提交归档重新核验，3,355 个后端文件和五个前端改动文件与最终验证清单逐文件 SHA-256 一致，记录在本轮证据目录 `commit-source-verification.json`；此前测试结果没有重复计作新执行。上文及结构化结果中的未提交状态保留为阶段快照，当前交付状态以本条为准。本次后续文档提交仅补充引用；未推送、新建 PR、合并或部署，没有新增真实提供方验证。

## CC-20261010-010：会话校验、请求配置快照与重试响应可见性

- 授权 / 基线：维护者要求继续修复，并确认按会话连续性、请求重试、配置一致性排查。基于 `e93e3fcd9`；Claude 集成上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，没有同步 main。沿用 CLI 2.1.292 / 2.1.295、SDK 0.128.0 的现有兼容范围；本轮不新增 CLI / 协议版本。
- 依据：本地源码检查及修复前回归发现，路由校验按 UUID 归一化比较，兼容转换却先比较原始字符串且只读第一个头值；Gin 已固定的请求配置仍在恢复前查询可变全局设置；CORS 会话头检查使用子串匹配且覆盖后续字段值，错误响应的重试信号未显式暴露给跨域客户端。没有把这些本地复现描述为线上事故或提供方行为。
- 会话修改：转换阶段复用路由校验器，接受指向同一 UUID 的合法写法，检查全部重复值并在发布会话标识前拒绝冲突 / 非法值。续聊头只要任一值非空，即执行已存在归属校验，避免首值为空漏检；校验与绑定共用判断，不更改账号归属、迁移或签名历史规则。
- 配置修改：优先恢复同一请求在 Context / Gin 中已有的配置快照，再为新请求读取当前设置。请求处理中途修改版本不会使已有请求重新选择或因新值被拒绝；新请求仍执行已验证版本准入。
- 重试 / CORS 修改：按完整 token、忽略大小写扫描所有已有暴露字段，保留中间件的既有值；为实际返回的 request-id、retry-after、retry-after-ms、x-should-retry 和 cf-ray 添加暴露声明。保留 cf-ray 空值存在性、响应脱敏和客户端拥有重试的既有行为；没有加入服务端重试或账号切换。
- 文件：`gateway_claude_compatibility.go`、`gateway_claude_session.go` 及对应会话 / 重试回归。本机证据目录 `/Users/kingford/claude-capture/continuity-fix-20261010-y8frpk18/`，保留修复前失败日志与源码哈希；最终执行结果后补。
- 提交 / 范围：尚未提交、推送、合并或部署，未来提交使用 `Claude-Change-ID: CC-20261010-010` 并补记引用。全部模型响应为本地合成数据；未运行真实浏览器或新 CLI 抓包，没有真实提供方接受、订阅资格或计费验证。

- 调度 / 预检补记：四个 handler 直接使用校验器写入的规范化会话 ID，不再重新取首个原始头值；新增重复续聊头的路由键断言。CORS 预检显式允许回传两种会话头和 prompt ID，沿用原来源 / 凭据策略，四入口各验证允许与拒绝来源。补充 401 / 429 与四入口网络中断的单次发送回归。
- 验证过程补记：最初五组新增回归在旧实现上检出失败，修复后的会话 / 重试专项通过。完整检查首轮因继续补齐 handler 路由而主动停止（unit / lint 均退出 143，不计通过）；随后 CORS 中间件再有修改，最终全量检查将基于更新后的冻结清单执行，旧日志继续保留。

- 最终验收：`GOTOOLCHAIN=go1.27.2 go test -tags=unit -json ./...` 在最终源码上退出 0，58 包、23,155 个通过事件、失败 0；其中新会话 / 快照 / 错误可见性 / 网络中断测试为 7 个顶层组、34 个通过事件，CORS 预检为 1 个顶层组及八个入口 / 来源组合。已有 HTTP 单次发送合同扩至 401 / 403 / 429 / 503 / 529。golangci-lint 2.14.0 最终退出 0、0 issues，`git diff --check` 通过。
- 证据范围：最终 3,356 个后端 Go / SQL / 模块 / JSON 文件与 `final-backend-manifest.json` 逐文件 SHA-256 相同；最终命令、分母、历史停止状态和日志哈希见本机证据目录 `validation-summary.json`。CORS 修改前的完整 unit 58 包结果保留为中间记录，最终完整命令复用未变包的 Go 缓存、重新验证受影响包，不把两次结果累计。此次未重跑数据库 integration、前端测试、真实浏览器或原生 CLI / TLS 抓包；没有将前轮结果表述为本轮执行。改动尚未提交、推送、合并或部署。

- 2026-10-10 提交 / 推送补记：维护者授权“提交推送”；本条修复与回归提交为 [0c10bc9cd812a73eb0234940dcbdf4ca303ec8f8](https://github.com/skingford/sub2api/commit/0c10bc9cd812a73eb0234940dcbdf4ca303ec8f8)，携带 `Claude-Change-ID: CC-20261010-010`，已推送至 `origin/codex/claude-source-runtime-audit-20261009`，远端完整引用已核对一致。CC-20261010-009 的版本选择实现 `c156466b0443667a598ac49571ef651359f528df` 及引用补记 `e93e3fcd9` 同时随分支推送。提交归档中的 3,356 个后端文件与最终验证清单逐文件 SHA-256 一致，记录在本轮证据目录 `commit-source-verification.json`；本次不重复计作测试执行。旧的未提交 / 未推送状态保留为阶段快照，当前交付状态以本条为准。后续文档提交仅补充追溯引用；未新建 PR、合并或部署，没有新增真实提供方验证。

## CC-20261010-011：补齐调度边界、同步并发、日志开销与固定 CI

- 授权 / 基线：维护者要求将四项优化全部补齐。基于 `24a7b8f4f132faa827c04a8f1256ffa572d08eb1`；Claude 集成上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，未同步 main。沿用 CLI 2.1.292 / 2.1.295、SDK 0.128.0 / runtime v26.3.0、默认 Linux x64 既有范围，不增加版本或传输等价声明。
- 会话：010 后续复查发现 metadata 优先调度分支未采用校验器的空白处理；两个 UUID 调度入口统一 TrimSpace，新增 ASCII / Unicode 空白、路由键和持久账号归属回归。保留原输入优先级与命名空间。
- 同步：单独保存成功检查时间，版本不变也更新，旧部署回退旧 UpdatedAt；使用数据库条件更新 / 唯一键仲裁并在冲突后重读比较，防止不同实例覆写更新版本。Start 幂等，Stop 取消正在进行的操作，取消后不继续列表回退。增加新旧值竞争、相同版本重启、失败不记成功、时间异常及生命周期测试；不声称实现集群单次抓取。
- 日志：先进行十二组、各三次本地基准，再将独立 trace 的 JSON 编码移出共享写盘锁并使用固定 envelope 结构；仍同步、不采样、不排队。新增编码 / 锁等待 / 写盘累计统计和关闭时汇总；序号及 trace 失败计数保持顺序，全局失败字段明确为编码时快照。完整字节、轮转、失败和并发回归继续保留。
- 性能范围：32 KiB / 16 并发临时文件组摊销记录耗时（总墙钟 / 请求数）中位数约下降 39%，分配约减半；部分纯编码和小分块组 P95 上升，慢盘背压仍存在。全部分母及不利结果保留在[说明](claude-maintenance-hardening-20261010.md)，不是模型 TTFT 或生产磁盘性能结论。
- CI：固定 142 份仓库样本、官方二进制来源 pin、版本集合与强制 Go 测试；新增独立 job 和结果上传。校验器拒绝文件损坏 / 缺失 / 新增、来源 pin 不一致，以及缺失 / 跳过 / 失败的必需测试；不以空的成功 Go 命令视为验收。该 job 使用已有样本及本机回环测试，不重新运行 CLI / 抓包。
- 文件 / 证据：`gateway_service.go`、同步服务及设置仓储、`requesttrace` 编码 / 统计及基准、`.github/claude-validation` 校验器 / 清单 / 测试、backend-ci、对应回归及维护说明。原始材料在 `/Users/kingford/claude-capture/remaining-hardening-20261010-1ik1jvj7/`；最终工程执行结果后补。
- 提交 / 边界：尚未提交、推送、合并或部署，未来提交使用 `Claude-Change-ID: CC-20261010-011` 并补记引用。未修改业务容器、线上账号、日志完整性配置或 GitHub 分支保护；未执行真实官方接受、订阅、计费、签名或托管 CI 验证。

- 最终工程补记：完整 unit 命令退出 0，58 包 / 23,164 个通过事件；真实 PostgreSQL / Redis 的完整 integration（`-p 1`）退出 0，52 包 / 13,784 个通过事件。CAS 首次创建和条件更新各 16 路竞争均只有一个成功；版本竞争、相同版本重启、停止取消与 metadata 归属回归通过。日志包 `go test -race` 通过，golangci-lint 2.14.0 为 0 issues；未修改原断言或用模拟仓储替代 SQL 集成检查。
- CI / 性能补记：本地执行新 CI 入口，142 份文件哈希全部匹配，21 项强制测试全部执行并通过；验证器三个负例测试组及工作流 YAML 检查通过。十二组日志基准各三次、每次 100 请求 / 八分块和一个结束事件，完整原始输出及不利 P95 结果留档。基准后的 Stats 报告在计时区间外，源输入 / 分块 / writer / 并发数一致；`ns/op` 表示总墙钟 / 请求数，不是单请求时延。
- 初轮失败与清理：并行检查期间既有 WebSocket 大帧测试超时、安全审计短时序断言失败，repository / routes 因 testcontainers 清理辅助容器启动故障失败。未改源码，待其他检查结束后顺序复跑上述完整检查通过；初次重跑代理因 Unix socket 路径过长退出，缩短临时路径后才实际执行测试。所有初轮日志保留，不算作通过。临时 IPv4 代理 / socket、失败创建的两只容器及后续实验容器已清理；原三个业务容器同 ID、均 healthy，没有重启或修改。
- 最终证据：3,357 个后端文件和四个 CI 实现 / 清单文件与冻结 SHA-256 一致，完整命令、分母和日志哈希保存在本机证据目录 `validation-summary.json`。Go 复用未变包缓存，不累计初轮和重跑分母。未重跑前端、原生 CLI / PCAP 或真实提供方验证；GitHub 托管工作流尚未运行。当前仍未提交、推送、合并或部署。
