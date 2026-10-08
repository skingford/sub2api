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
