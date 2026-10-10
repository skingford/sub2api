# Claude 订阅 OAuth 转 API：保留调用方内容

> 当前实现见 [CC-20261010-008 修复说明](claude-cli-alignment-fix-20261010.md)：已移除
> 计数前缀注入，修复 Anthropic 目标的 strict 省略，采用实测 CLI 自定义 system 布局，
> 并将新配置默认值改为 true。以下 006 / 007 的启用建议和结果作为历史快照保留。

变更记录：CC-20261010-006。代码基线 `a426396d8`，沿用该基线的 Claude 集成上游
`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`；没有同步当前 main，也没有部署。

> 后续复核：[CC-20261010-007](claude-cli-alignment-audit-20261010.md)确认此模式会给
> 36 个 CLI 计数探针输入额外补入 system，并且尚未还原完整的 CLI system / 缓存布局。
> 006 的通过结果是内容保留合同，不是官方 CLI 等价验证。以 CLI 对齐为目标时，建议
> 保持默认关闭，先修正已确认的计数差异；下文保留原策略的实现与验证记录。

## 适用范围与目的

适用于第三方客户端调用 Messages、Chat Completions、Responses，再通过 Anthropic
OAuth 账号转发的路径，以及对应的 Messages `count_tokens`。原生 Claude Code 保留
路径和 API Key 路径不使用这项策略。

现有兼容策略会把调用方 system 搬进 `[System Instructions]` 用户消息，并插入
`Understood. I will follow these instructions.` assistant 消息，还会生成通用扩充
提示词、工具别名和缓存断点。这是可观察的网关行为，不是已经证实的服务端检测规则。

新模式减少这些网关改写，保留调用方指令层级、工具名称和缓存意图。它不是官方 CLI
运行时，不承诺全部官方行为一致，也没有验证真实 OAuth 订阅、计费或上游接受性。

## 启用与回退

默认关闭，以免现有部署升级时突然改变正在进行的对话。使用包含本次代码的镜像，
在部署 `.env` 中设置以下值并按原有部署方式重建服务容器：

```dotenv
GATEWAY_CLAUDE_OAUTH_PRESERVE_CALLER=true
```

仓库四种 Compose 文件均显式传入这个变量。直接启动后端也可用相同环境变量，或配置：

```yaml
gateway:
  claude_oauth_preserve_caller: true
```

环境变量覆盖配置文件。回退设为 `false`，重启对应服务，并重新开始对话。切换策略会改变
system 前缀及工具命名政策，不应在已有对话中途切换并复用旧的签名、缓存或恢复状态。
本次没有修改真实账号、生产 `.env`、数据库或远程服务。

## 行为与优先级

| 内容 | 启用后的行为 |
|---|---|
| system 字符串 | 转成一个 text block，保留原始文字与空白，不搬进 messages |
| system block 数组 | 保留块顺序、原始 JSON、各块 cache_control 与扩展字段 |
| 固定确认对话 | 不生成；原始 messages 不因 system 包装而增加 user / assistant |
| 通用扩充提示词 | 不生成，不应用旧自定义扩充或 system blocks 模板 |
| 既有归因 / 身份前缀 | system 注入开关开启时仍保留现有 billing 与身份两个 block；未证明这些是服务端必需条件 |
| system 注入关闭 | 不补前缀，保留调用方 system；不会替换 OpenCode 身份句 |
| 工具 | 不执行静态 / 动态名称混淆，也不补 tools 最后一个缓存断点 |
| 缓存 | 不执行 messages 断点重写或强制 1h TTL；超过四个断点或 thinking 带断点时发送前 400 |
| 日期文本 | Messages 路径不执行旧日期规范化 |
| token 计数 | 使用相同的 system 前缀和调用方工具 / 缓存策略；不是实际 token 数相等的声明 |

保留模式优先于旧的自定义 system 模板、messages 缓存重写、1h TTL 注入和日期规范化
设置。总 system 注入开关仍生效。保留模式不是无限制字节透传：协议转换、模型参数约束、
无效内容检查、能力字段处理、账号与会话身份验证、最终序列化和完整性处理仍执行。
跨协议适配器本身能表达的字段范围不因这个开关扩大。

会话 ID、账号归属、拒绝不隐式重试、签名历史回传继续使用已有实现。Chat 客户端仍需
原样回传 `anthropic_content`；这个开关不能恢复客户端已经丢弃的内容。

API 客户端续聊还需要回传响应中的 `X-Sub2API-Session-Id`，独立的新对话使用新的会话。
没有显式会话标识时，现有兼容入口会生成新 UUID；本模式不会按首句或完整历史文本猜测
会话，也不会把多个用户合并到一个固定 session。仅开启服务器开关不能代替客户端的
会话与签名历史管理。

## 验证依据

- `gateway_claude_caller_policy_test.go`：system 层级与原始块、静态和动态工具名、
  四个缓存断点、旧设置优先级、计数路径、400 不发送、注入关闭、原生样本与 403 单次转发。
- `TestClaudeCallerContentContract`：在新模式重用原 540 个内容合同输入，覆盖六模型、
  两种账号、三种入口，OAuth system / developer 必须实际留在 system 中。
- 原 `TestClaudeContentContract` 保留旧模式及其既有包装预期，不覆盖历史失败证据。
- `TestLoadClaudeOAuthPreserveCaller`：默认关闭、YAML、环境变量和显式回退；
  `deploy/tests/docker-compose-gateway-env-test.sh` 检查四种 Compose 的透传。

全部测试凭据、签名和模型回复为本地合成数据。测试结果在变更记录中补记；不把模拟
200 或源码检查当作真实上游接受、签名有效性、订阅资格或防识别验证。

2026-10-10 结果：新模式 540 输入中 504 正常转发、36 按既有约束拒绝，独立内容判定
差异 0；恢复旧包装的 36 个负向样本均被判失败。完整 unit 58 包、integration 52 包
通过，lint 0 issues。原生 12 份样本正文保持，配置与 Compose 检查通过。完整证据在
`/Users/kingford/claude-capture/caller-preservation-20261010-j2kAol/`，关联变更记录
[CC-20261010-006](claude-change-log.md#cc-20261010-006可选保留-api-调用方内容的-oauth-转换策略)。
