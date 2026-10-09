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
