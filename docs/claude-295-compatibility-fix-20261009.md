# Claude 2.1.295 兼容修复（CC-20261009-012）

本次修复 [CC-20261009-011 审查](claude-source-runtime-audit-20261009.md)发现的三个缺口：
托管 `/compact` 续聊、Haiku 5.5 普通请求转换，以及 2.1.295 原生传输选择。
旧审查结论仍描述修复前的 release，当前状态以本记录及
[结构化结果](claude-295-compatibility-fix-20261009.json)为准。

## 代码变化

### 连续压缩与会话恢复

- 缺少压缩分类头时，正文推断精确允许 2.1.292 和 2.1.295；其他未验证版本不自动加入。
- 接受 2.1.295 `RZt / DZt` 函数生成的精确保留消息尾文；继续验证已保存摘要、原有 system
  控制帧、保留消息和工具闭合状态。伪造摘要、篡改保留回复、任意尾文及重复尾文均有拒绝回归。
- 迁移后重建归因时保留已验证的 2.1.295 版本，避免回退成 2.1.292 后与 UA 不一致。
- 托管模型表增加 Haiku 5.5；隔离原生恢复实验覆盖其连续压缩和迁移。

### Haiku 5.5 参数与转换版本

- 新增实测默认值：128000 max_tokens、adaptive thinking / display=omitted、medium effort、
  相应 context_management；默认不添加 temperature。
- 显式 temperature、low / xhigh effort、token 上限和 display=updates 保留；OAuth beta 顺序
  由 2.1.295 原生样本固定。diagnostics 继续要求调用方提供状态，不凭空生成前一条消息 ID。
- effort 能力表支持 low / medium / high / xhigh / max，并沿用现有 provider / dotted ID 规范化。
- 支持精确的 2.1.292 / 2.1.295 转换配置。默认配置仍为 2.1.292；转换 Haiku 5.5 时选择
  2.1.295，使 UA、billing 版本、CCH 和模型默认值相符。请求内版本继续冻结。
- 依照该模型的已知能力，显式 disabled / enabled thinking、between_tools 和强制工具选择
  返回本地 400；未将这些设置静默改成另一种语义。原生 CLI 的 MAX_THINKING_TOKENS=0
  会省略 thinking，不能等同于调用方显式发送 `{type:"disabled"}`。
- count_tokens 不注入生成字段或额外 billing system block。
- Responses / Chat Completions 共用同一 Haiku 5.5 能力判断，保留 xhigh，生成 adaptive 而非
  旧 enabled / budget 组合；默认输出上限为 128000。响应转换和流式事件保留 opaque thinking /
  redacted_thinking，与工具回填一起往返，不丢弃签名。签名仅透传，测试不声称验证其真实性。
- Codex 模型描述为 Haiku 5.5 提供实测目录中的 1M 上下文及五档 effort，不修改价格或模型别名。

### 请求校验与传输

- 在 2.1.295 的本地副本中仅替换 JS 入口，原生 HTTP / CCH 机器码前缀保持原样。
  593 个既有边界向量及 12 个来自实际 Haiku 5.5 正文的模型 / token / Unicode 改写向量，
  均由该运行时独立生成预期，再与 Go 比较；PCAP 核对零丢包。
- 已验证的 Linux x64 2.1.295 可在最终策略改写后重算 CCH；未知版本或未验证平台仍保留
  不安全改写保护。原生 gzip 原字节保留与校验计算各自处理。
- 增加独立的 `claude_2_1_295_linux_x64` 传输配置及连接池身份；复用已证实相同的握手结构，
  凭据、随机数、会话标识和密钥仍由当前请求 / 连接生成。
- 只在第一方 HTTPS、Linux x64、CLI 2.1.295、SDK 0.128.0、runtime v26.3.0 组合下选择它。
  2.1.295 MacOS、ARM、新版 SDK / runtime、未知 CLI、显式账号模板和自定义 origin 不自动套用。
- 区分运行时 gzip 与 CLI 分块 gzip 的 Content-Encoding 位置。两版传输均保留原生 HTTP/1.1
  头序、keep-alive、代理路径、证书校验和日志正文；profile 名称使不同版本连接池分离。

## 验证与来源

固定 release 基线 `7ce838ee33663b8b4dc93296a51fad25b2daa49c`；上游仍为
`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，没有同步上游。Go 1.27.2。

官方 Linux x64 二进制：

| 版本 | SHA-256 |
|---|---|
| 2.1.292 | `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3` |
| 2.1.295 | `4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358` |

复用审查中的原始捕获作为独立输入，另用未修改 CLI 新采集 Haiku 5.5 的 API / OAuth、
流式显示、effort、token 上限、temperature、连续压缩、多轮和 count_tokens：11 场景、
21 请求，PCAP 全匹配、零丢包。

| 检查 | 结果 |
|---|---|
| 两版原有 1,192 个转发组合 + Haiku 新采集 84 个组合 | 1,276 组逻辑及 wire 正文相同 |
| 原生托管恢复 2.1.292 | 三模型 × 两种账号，12 会话、96 生成、24 计数，跨会话混入 0 |
| 原生托管恢复 2.1.295 | 四模型 × 两种账号，16 会话、128 生成、32 计数，跨会话混入 0 |
| 真实传输，两版 × 普通 / 两类 gzip × 四路径 × 日志开关 | 48 组正文、完整头值、头序、ClientHello 全同，零丢包 |
| 日志 | 24 组字节、摘要、完整性及假凭据脱敏通过 |
| 2.1.295 CCH 独立 oracle | 605 个向量与 Go 一致 |
| 工程检查 | 58 包 unit、52 包 integration、golangci-lint 2.14.0 为 0 issues |

源码及抓包来源分别见旧审查的锚点、新增
`backend/internal/service/testdata/claude_code_2_1_295/provenance.json` 和
`backend/internal/pkg/claude/testdata/cch-2.1.295.json`。完整材料保存在：
`/Users/kingford/claude-capture/compatibility-fix-20261009-yvk8ocrl/`。

新增永久回归覆盖实际捕获、错误参数、未知版本 / 平台、归因版本、转换配置、count_tokens、
摘要和保留历史完整性。HTTP 握手黄金回归覆盖两个 profile，并检查连接池键分离。
隔离联调使用生产恢复 / 转发 / HTTPUpstream，存储和响应由测试实现提供；不是完整线上部署。

准备过程中更正了两处测试假设：OAuth 账号不会应用 API Key 的模型映射配置，count_tokens
也不会凭空注入 billing block。后续补查修复 OpenAI 兼容入口时，正在运行的旧集成构建曾因
依赖先编译、调用方后修改而报告新 helper 未定义；已保留该日志。最终工程检查在不可变源码
快照上重新执行，避免将不同修订混成一次通过结果。

最终检查固定 3,193 个 Go / SQL / 模块文件，连同回归 JSON 共核验 3,332 个文件；结束时
与工作区逐文件 SHA-256 一致。unit 全部 58 包通过；integration 首轮 49 包通过，另三个包
因 Redis / PostgreSQL / Ryuk 的 Docker 发布端口探测失败，没有完成启动。
诊断观察到同一容器 IPv4 / IPv6 使用不同端口，而测试库选取的端口不能从 IPv4 连接。
仅将失败三个包的新 testcontainers 容器临时绑定为 127.0.0.1 后，middleware、repository、
server/routes 全部通过，合计 52 包。所有原断言、生产代码和真实数据库交互保留。
临时 Docker API 代理已停止，未修改 Docker 全局配置或现有业务容器。

正式结果分别见 `analysis/verified-unit.jsonl`、`analysis/verified-integration.jsonl`、
`analysis/integration-ipv4.jsonl` 和 `analysis/verified-lint.log`。保留原始失败，不把首轮
integration 命令写成退出 0。`go mod verify`、`go mod tidy -diff` 和 `git diff --check` 通过。

## 复现命令

```bash
cd backend
GOTOOLCHAIN=go1.27.2 go test -tags=unit ./internal/service ./internal/pkg/claude \
  ./internal/pkg/tlsfingerprint -run 'TestClaude2295|TestCCH2295|TestClaudeNative|TestIsHaiku' -count=1
GOTOOLCHAIN=go1.27.2 go test -p 2 -tags=unit ./...
GOTOOLCHAIN=go1.27.2 CI=true go test -p 1 -tags=integration ./...
```

原生实验继续使用验证 README 中的 network-none 配置。2.1.295 托管实验可设置
`CLAUDE_RECOVERY_LAB_MODELS=claude-sonnet-4-6,claude-sonnet-5-5,claude-opus-5-5,claude-haiku-5-5`，
运行 `TestClaudeAlignmentNativeCLILab`；`haiku295_lab.py` 生成新增场景。
`prepare_cch_probe.py` 现在只接受上述两个固定 SHA-256，不接受任意二进制。

## 交付边界

修复保留既有显式策略：空 anthropic-version 补默认值、账号 / 会话身份冲突保护、
成功响应头过滤，以及普通 API 未指定 verbose / display 时的默认显示方式。
这些不属于本次三项缺陷。2.1.295 原生 MacOS、ARM、HTTP/2、TLS 恢复和真实签名未扩大验证。

没有推送、合并或部署。所有模型交互使用 Docker 断网、假凭据和本地模拟响应；
结果不代表真实官方接受、订阅资格、真实 thinking 签名或计费验收。


## 提交与推送补记（2026-10-09）

维护者授权“提交 推送”后，审查材料和修复代码已保存为 [765a12ec4](https://github.com/skingford/sub2api/commit/765a12ec4199dfeb490b60b0cfcc47db10bdff14)，
并推送至 `origin/codex/claude-source-runtime-audit-20261009`。文中的未提交 / 未推送状态
保留为此前验证时点的记录；本次没有新建 PR、合并或部署。提交内 3,332 个被测文件
与最终验证快照逐字节相同，提交后的补记仅涉及文档。
