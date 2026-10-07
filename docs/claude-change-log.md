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
