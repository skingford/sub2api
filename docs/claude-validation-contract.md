# Claude 兼容验收合同

本文件定义可重复的通过条件。有限测试通过，只能证明这里列出的版本、输入与策略；不能写成
所有客户端、所有参数、所有平台或真实服务全部等价。CC-20261010-001 将之前观察型审查中的
四项遗漏改为固定断言，并保留此前失败记录。

## 固定范围

- 来源：官方 Linux x64 CLI 2.1.292 / 2.1.295，SDK 0.128.0、runtime v26.3.0；使用验证 README
  中固定 SHA-256。不得用滚动 latest 替换已测二进制后沿用旧结论。
- 六模型：Sonnet 4.6、Opus 4.6、Haiku 4.5 完整 ID、Sonnet 5.5、Opus 5.5、Haiku 5.5。
- 三入口：Messages、Chat Completions、Responses；两种账号：API Key、OAuth。
- 生产转发、最终序列化、请求重放和必要的能力头必须一起验证。

## 必须保持的约束

| 调用方意图 | Anthropic 目标的要求 |
|---|---|
| Chat response_format.json_schema / Responses text.format | schema 原内容进入 output_config.format.schema，不允许消失 |
| Schema 与 effort 同时存在 | 两者均保留，默认值或分支返回不能覆盖 format |
| Chat stop 字符串 / 字符串数组 | 准确转为 stop_sequences，保留顺序、Unicode 和转义内容 |
| 显式输出 token 上限 | 正值按原值传递；Chat 不套用真正 OpenAI Responses 目标的 128 下限 |
| 两种 Chat 上限同时出现 | max_completion_tokens 优先于 max_tokens |
| 非法转换参数 / 不支持的 format 模式 | 在发送前明确返回 400，不以默认值代替，也不静默删除 |
| 结构化能力被策略禁用 / 头覆写移除 | 明确拒绝请求，不能发送丢失约束的“成功”请求 |
| 嵌套 Schema 含 system 等名称 | 保留数据，生成的 billing 定位不能被嵌套内容遮蔽 |
| 压缩摘要去空白 | 匹配原生 JS trim 的字符集合，BOM / NEL 不能造成历史冲突 |
| 续聊与签名历史 | 保留摘要来源、历史前缀、保留消息及不透明签名校验，不能为通过测试而跳过 |

这里支持的输出 format 是 text 与 json_schema；json_object 及未知模式在 Anthropic 转换入口
明确拒绝。name / strict 是 OpenAI 包装属性，目标格式传递实际 schema；不改写 schema 内的
required、enum、additionalProperties、引用或注释。具体模型是否接受某个 schema 关键字，
仍由上游能力决定，离线测试不冒充官方接受验证。

## 固定测试入口

1. `TestClaudeExplicitConstraintMatrix`：6 模型 × 2 账号 × 三入口的适用组合，共 192 例；
   包含 schema、schema+effort、1 / 64 token、单 / 多停止词。Responses 没有 stop 合同，不虚造它。
2. apicompat 约束测试：1 / 64 / 127 / 128 / 4096 token、优先级、Schema 与各 reasoning 分支、
   非法参数及真正 OpenAI Responses 的旧下限保持不变。
3. `TestClaudeSummaryMatchesNativeUnicodeVectors`：独立原生 JS 函数生成的 62 个固定向量。
4. 原生 CLI 的两版空格 / BOM / NEL 场景：六场景、36 请求，生产恢复序列必须全部接受。
5. `validate_constraint_contract.py`：读取 102 个 API 入口观察和两版摘要序列，任何已列约束
   丢失、上限变化、场景缺失或续聊错误均退出非零。观察工具的 PASS 本身不算一致。
6. `constraint_wire_lab.py`：实际发送生产导出的 192 例，在接收端检查约束，再与 PCAP 核验。

新版本、新模型、新参数或新的跨参数组合，必须先增加独立来源及对应断言再扩大支持范围。
发现差异时记录最小输入、最终请求、原因、策略归类和修复验证；不能只增加“通过次数”。

## 明确保留的策略差异

- 账号凭据、会话绑定及必要的关联 ID 会按网关职责变化，不以真实凭据值作字节等价条件。
- 空 anthropic-version 补默认值；缺 display beta 时 updates 回退 omitted；对这些变化保留
  原始分母和逐项记录，不删除差异样本后声称全同。
- OpenAI 与 Messages 的既有缺省 effort / display 策略不等同于调用方的显式参数；显式参数
  和默认策略分别验证。字段相同也不证明服务端输出相同。
- Native 原字节保留与普通 API 协议转换分别定义合同。不能通过改写原生请求去“补齐”所有差异。
- 未测的平台、HTTP/2、TLS 恢复、所有远程开关、真实 thinking 签名、订阅及计费，不包含在
  上述通过声明内。

## 2026-10-10 新增检查尚未通过

[CC-20261010-002](claude-tool-reasoning-audit-20261010.md)在已推送的 `465ed3738` 上扩展
工具 / 推理组合，发现此前范围外的四项遗漏：并行工具限制、工具级 strict、高推理预算与
输出上限的组合、旧模型 none 推理语义。旧合同通过不能覆盖这些新失败。

新增入口为 `TestClaudeToolConstraintAudit` 和 `validate_tool_constraints.py`，固定 504 个
组合并对未支持的 44 个请求保留明确 400。当前基线的新判定器应退出 1，修复验收必须在相同
分母下退出 0；不能仅凭观察测试 PASS、更改预期或跳过失败组合认定修复。工具级 strict
与前文输出格式包装的 strict 是不同字段，不能套用后者的省略规则。

## 2026-10-10 工具与推理修复跟进

[CC-20261010-003](claude-tool-reasoning-fix-20261010.md)修复上述四类缺口，504 组场景已加入
永久测试。旧 002 的失败记录保留为原基线结果；当前修复仍保留全部 504 个输入，其中
452 个成功请求必须满足完整约束，52 个组合明确拒绝。

新增的八个拒绝是 Haiku 4.5 的 high + 64 / 1,024 上限（两账号、两转换入口）。手动思考最低
预算为 1,024，无法同时小于该输出上限；因此返回 400，而不是提高上限或悄悄关闭思考。
这项新增输入策略单列，原生请求保留政策不变。不能将“原来 44 个拒绝”误写成修复后的数量。

strict=true 必须保留工具 Schema，不走摊平联合的非严格兼容规则，并携带必要结构化能力。
旧模型 none 必须 disabled；已测 Sonnet / Opus 4.6 非 low 推理用 adaptive，手动预算必须
至少 1,024 且小于最终上限。无法表达的 strict Schema、不支持的 effort 或强制工具 / 推理
冲突均须明确拒绝。未知平台和真实服务接受性仍不包含在本合同中。

## 2026-10-10 内容与响应扩展未通过

[CC-20261010-004](claude-content-stream-audit-20261010.md)在 `100427570` 上重新验证，
旧 504 / 192 项合同及 102 入口 / 36 摘要请求仍通过。新内容矩阵保留六模型、两账号及
三入口的 540 个适用输入：528 个发出请求、12 个已知模型参数拒绝；153 个原始差异中，
33 个是既有 OAuth 政策，120 个是 developer 角色、URL 图片、旧式函数历史、非法工具
JSON、工具结果文档和不透明历史的未解决差异。真实 TLS 接收端得到相同结果。

新增响应检查以独立组装的捕获 SSE 为预期，覆盖转换函数和实际 buffered / streaming
处理器：821 个源响应、3,284 个比较中 100 个有差异。旧模型签名丢失与非流式顺序变化
部分重叠；44 个 Chat 组件历史往返均丢失不透明块，包含 5.5。CC-20261009-012 的签名
保留结论仅限当时已验证的 5.5 Responses 路径，不能推广到 Chat。

`validate_content_contract.py`、`validate_response_content.py`、`validate_chat_opaque.py`
目前应返回 1。修复时保留输入、原始差异及已知政策，核对原生字段、实际发送和客户端
返回内容；不以观察工具 PASS 或模拟 200 代替判定。原生 CLI 自身的工具块排序与网关
返回模式间的顺序差异分别记录。停止原因的跨协议映射也不表示语义完全等价。

## 2026-10-10 内容与响应修复跟进

[CC-20261010-005](claude-content-stream-fix-20261010.md)保留 004 的输入和失败证据，将
540 个输入加入永久 service 合同。24 个非法工具 JSON 输入现在发送前 400，连同原
12 个采样拒绝为 36 个，剩余 504 个输入必须完整保留内容。无法表达的明确媒体来源
返回错误，不能以删除文件或图片替代转换。

OAuth system / developer 包装的 36 个差异单独记录，判定器必须检查实际包装形状；
原 Chat developer 错误分支仍失败。旧 redacted prefilter 豁免已取消，六模型的有效
签名 / redacted data 必须按原值回传。

821 个响应的四路径比较、44 个 Chat 组件往返及 176 个实际 Chat 处理器 / 请求入口往返
应全部通过。Chat 客户端需保留新返回的 `anthropic_content` 字符串；流式在结束前收取一次。
字段损坏、正文 / 工具历史冲突或目标协议不符必须明确报错。该字段承载历史，不证明签名
有效，也不能把任意 OpenAI ciphertext 当作 Anthropic 历史。

## 2026-10-10 可选调用方内容保留策略

CC-20261010-006 增加 `gateway.claude_oauth_preserve_caller`，默认关闭。启用后，API 转
OAuth 的 system / developer 应保留在 system；不得再添加固定确认对话、通用扩充提示词、
工具别名或自动缓存断点。既有归因 / 身份前缀仍受总 system 注入开关控制。

`TestClaudeCallerContentContract` 在同一 540 输入上验证新策略，原
`TestClaudeContentContract` 继续验证旧策略。旧模式的 36 个包装差异不再适用于新模式；
新模式中的 instruction 降级必须判失败。非法缓存断点应明确 400，不静默删除。

这是一项内容保留策略，不是新的 CLI 等价声明。版本和传输验证范围不扩大，真实服务
接受性仍未验证；部署、设置优先级与会话切换边界见
[调用方内容保留说明](claude-caller-preservation.md)。

## 2026-10-10 CLI 实际构造对照补记

CC-20261010-007 用两版未修改 CLI 的 print / PTY 场景及本地 MCP，补验 006 的政策
边界。内容合同通过不能代替 CLI 对照：新模式在 36 个原生计数探针作为 API 输入时
均补入额外 system；Chat 的省略 strict 变成显式 false；两种模式仍有 system / 缓存
布局差异。这些发现尚未修复，不能把 006 的 540 项通过扩大为 CLI 等价。

新增 62 条 CLI 捕获的 124 组原生转发保持正文和完整头序，256 组实际 TLS 发送经
包含生成 / 计数两路径的 PCAP 判定通过。具体分母、模式差异与未测范围见
[CLI 对齐复核](claude-cli-alignment-audit-20261010.md)。

## 2026-10-10 已确认差异的修复合同

CC-20261010-008 保留原 128 输入 / 256 观察，要求普通计数输入不注入生成前缀、
Anthropic 目标的 Chat strict 省略不补 false、默认对齐模式不混淆工具名。
普通 API 明确映射 CLI 自定义 system，而不是完整默认 / append 应用模板；身份和自定义
文本的 1h 缓存布局来自新增两版真实 TTY 捕获。多块 / 显式缓存是调用方扩展，保留优先。

新增 `validate_cli_alignment_fix.py` 的固定分母为原生 124、普通计数 72、工具 strict
省略 24、自定义 system 布局 30、六组 MCP 名称。修复前实际传输记录必须失败，修复后
观察和真实传输记录均须通过。完整原始差异报告继续保留，不能把这个范围内通过写成
所有请求、所有入口模式或真实服务全部等价。新配置默认 true，显式 false 为旧策略回退。

## 2026-10-10 SSE 事件边界合同

CC-20261010-013 增加七种 SSE 分帧 × Chat / Responses 流式及缓冲四路径的 28 项
固定内容与用量断言。两版未修改 CLI 的 14 场景及其 PCAP 提供独立对照；
注释、data / event 换序、多行 JSON、CRLF、CR、逐字节读取不得导致内容或用量丢失。

`TestClaudeSSEFramingReadErrors` 要求读取中断、单行超限和多行累计超限明确失败，
不能再产生成功收尾。大小限制和干净 EOF 的旧兼容容忍分别作为网关策略验证，
不扩大为原生 SDK 的完全等价。来源、复现及未覆盖语义见
[SSE 对齐报告](claude-sse-framing-20261010.md)。

## 2026-10-10 SSE 事件语义合同

CC-20261010-014 的 `TestClaudeSSETerminalContract` 固定 11 输入 × 四路径共 44 组。
已知事件的非法 JSON、主动 error、无终止信号且未关闭内容块的 EOF、负索引和开始前
增量应失败，不能 panic 或输出成功收尾；未知事件即使带合法增量正文也必须忽略。
缺 message_stop 或 message_delta 的独立成功对照保留，不能简单地一律拒绝 EOF。

两版未修改 CLI 的 22 场景及 PCAP 是独立来源；其失败后非流式回退由模拟器显式返回
400，网关不据此增加内部重试。完整范围见 [终止语义复核](claude-sse-terminal-20261010.md)。
