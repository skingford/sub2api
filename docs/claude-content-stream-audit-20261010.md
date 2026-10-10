# Claude 内容与响应逐项审查（2026-10-10）

对应记录：CC-20261010-004。结论：**仍不完全一致**。上一轮工具 / 推理的四项修复未复发；
这轮扩展消息内容、完整历史和返回内容后，确认七类缺口。生产代码保持审查基线不变。

## 基线与证据方法

- Git 基线 `1004275707dc5b6b9b17ecf8dee27346696fe23d`，运行时代码提交
  `fd4d400090fcfdb55dc89700bb7d4332991b232c`；上游沿用
  `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，没有同步或合并。
- 固定 Linux x64 CLI 2.1.292 / 2.1.295、SDK 0.128.0、runtime v26.3.0。
  二进制 SHA-256 分别为 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`
  和 `4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358`。
- 重新提取 2,260 / 2,340 个内嵌 JS 模块，4,600 个模块与上次提取逐字节相同。
  这是发行版嵌入源码，不是完整原始 TypeScript。原生入口、独立机器码探针和 Go 组件分别计数。
- 冻结并逐文件核验 3,341 个后端 Go / SQL / 模块 / 回归 JSON 文件，最终工作区完全一致。
- CLI 只在 Docker `--network none` 中运行，域名指向回环 TLS 模拟服务、使用假凭据和
  空配置。捕获先落容器 tmpfs；未挂载用户登录目录，没有访问官方模型服务。

## 固定矩阵结果

| 检查 | 实际结果 |
|---|---|
| 既有工具 / 推理合同 | 504 个输入，452 成功、52 明确拒绝，约束失败 0 |
| 既有显式参数合同 | 192 个请求通过；102 入口观察、36 摘要请求通过 |
| 新内容矩阵 | 540 个适用输入，528 发送、12 明确拒绝；153 个原始差异，其中 33 个既有策略差异、120 个未解决内容差异 |
| 新内容原生对照 | 两版各 90 场景，共 180 条请求；显式内容字段均与输入相同，PCAP 全同、零丢包 |
| 实际发送旧 / 新合同 | 192 + 452 + 528 = 1,172 条真实 HTTPUpstream / TLS 发送，PCAP 全同；新内容的接收字段再次检出相同的 120 个缺口 |
| 响应主回放 | 797 个捕获合成响应 × 四条转换 / 处理路径 = 3,188 个比较，88 个比较有差异 |
| 无思考块的最小顺序对照 | 24 个捕获响应 × 四路径 = 96 比较，12 个比较有差异 |
| Chat 不透明历史往返 | 44 个源响应，44 个往返均丢失签名或 redacted data，覆盖六模型；转换本身没有报错 |

六模型为 Sonnet 4.6、Opus 4.6、Haiku 4.5 完整 ID，以及 Sonnet / Opus / Haiku 5.5。
三入口为 Messages / Chat Completions / Responses，两账号类型为 API Key / OAuth。
Chat 没有本项目的 Anthropic 不透明 envelope，两个对应入站控制不硬造 Chat 字段；非法
JSON 工具参数字符串也没有原生 Messages 的等价 JSON 输入。保留适用性清单后，分母为 540。

“四路径”分别为非流式转换函数、流式转换函数、实际网关 buffered 处理器和实际网关
streaming 处理器。后二者直接消费捕获的 SSE；原始响应由独立 Python 组装，不用被测 Go
转换器生成预期。两个响应语料互不重叠，合计 **821 个响应 / 3,284 个比较 / 100 个差异**。
其中签名缺失与顺序差异存在重叠，不能相加为独立问题总数。

## 七类确认缺口

### F1：Chat developer 指令降成 user

`{"messages":[{"role":"developer","content":"指令"},{"role":"user","content":"问题"}]}`
经 Chat 转换后，指令进入 user 内容。对应 Responses developer 与 API Key Messages system
对照保留系统角色。六模型 × 两账号的 12 个 Chat 输入受影响；OAuth 原有 system 包装会
掩盖部分最终角色差别，因此同时核对 API Key 对照和转换分支。

根因：`chatMessageToResponsesItems` 未处理 developer，进入 `default → chatUserToResponses`。
位置：`backend/internal/pkg/apicompat/chatcompletions_to_responses.go`。来源分支已在旧提交
`9d81467937` 存在，未由 CC-20261010-003 引入。

### F2：URL 图片静默消失

Chat `image_url.url=https://content-audit.invalid/synthetic.png` 与 Responses `input_image`
中的相同 URL 在转换后消失；只剩文本。普通图片和工具结果媒体两类场景共 48 个转换输入
受影响。内联 base64 图片对照保留，原生 CLI 显式发送 URL source 也保留。

根因：`dataURIToAnthropicImageSource` 只接受 `data:...;base64,...`；调用方遇到 nil 直接
跳过，没有转换 URL source，也没有明确拒绝。不把模拟端保留 URL 当成官方下载成功。
位置：`backend/internal/pkg/apicompat/responses_to_anthropic_request.go`。

### F3：旧式 function_call 历史与结果丢失

Chat 的 assistant `function_call` 加 function 角色结果，最终只剩前面的用户消息，12 个
组合均模拟成功。普通 `tool_calls` / tool 结果对照正常。

根因：`chatAssistantToResponses` 只输出 `ToolCalls`，没有输出 `FunctionCall`；后续
`normalizeAnthropicToolPairing` 将没有对应调用的旧式结果丢掉。位置同 F1。

### F4：非法工具参数被转成 null 后发送

Chat / Responses 的工具参数字符串 `{"value":` 不能解析为 JSON，24 个输入仍发出请求、
模拟返回 200；最终存在 `{"role":"assistant","content":null}`，对应工具结果也被删除。
实际 TLS 接收和 PCAP 复现相同正文。

根因：`function_call` 分支将字符串直接置入 `json.RawMessage`，随后忽略
`json.Marshal([]AnthropicContentBlock{block})` 的错误。应校验并明确拒绝非法输入，不能把
观察工具的 PASS 或模拟 200 解释成合法请求。位置同 F2。

### F5：Responses 工具结果内的文档丢失

`function_call_output.output` 同时含 `input_text` 与 `input_file.file_data` 时，12 个
Responses 组合仅保留文本；Messages 的 document 块保留，普通 user 文档对照也保留。
Chat 对照使用已有的后续 user 消息携带媒体，不假定 Chat tool 角色支持文档数组。

根因：`responsesFunctionOutputToAnthropicContent` 处理文本和图片，没有处理 input_file。
合成 PDF 仅用于验证字段传输，没有测试真实文档解码或模型阅读。位置同 F2。

### F6：不透明思考历史没有完整往返

- Responses 入站：旧三模型的 `anthropic-thinking-v1:` envelope 被静默忽略，12 个
  signed / redacted 输入丢失不透明块；5.5 对照保留。
- Responses 出站：旧三模型的 signature / redacted data 丢失。主响应语料中 22 个源响应
  在四路径均复现，共 88 个比较；实际 buffered 与 streaming 处理器同样受影响。
- Chat：44 个捕获的含不透明块响应经过生产响应转换和下一轮请求转换后，签名 / data
  全部丢失，**5.5 也包括在内**。这是组件往返证据，不声称真实模型已经拒绝这些历史。

根因分别是入站和出站的 `RequiresSignedThinking` 门槛只覆盖 5.5，以及
`ResponsesToChatCompletions` 只保留 reasoning summary，不携带 encrypted_content。
相关文件为 `responses_to_anthropic_request.go`、`anthropic_to_responses_response.go`、
`responses_to_chatcompletions.go`、`chatcompletions_to_responses.go`。

**纠正 CC-20261009-012 的范围表述**：该条关于“不丢弃签名”的说明只能适用于已验证的
5.5 Responses 路径，不能泛化到 Chat 的完整工具历史往返。原报告保留，本条提供后续证据。
所有签名均为合成不透明值，未验证官方签名有效性。

### F7：旧模型非流式响应移动文本与工具块

最小输入只含 `text → tool_use`，不含 thinking。旧三模型的非流式 Responses 返回变为
`function_call → message`，流式仍为 `message → function_call`。两版六个旧模型源响应在
转换函数和实际 buffered 处理器中共出现 12 个差异；5.5 对照正常。

根因：非 5.5 分支把 text 缓存在 `msgParts`，循环结束后才追加 message，而 tool_use 已
先加入 output。主语料另有 24 个同类内容顺序差异，合计 36 个比较；这些与 F6 部分重叠。
位置：`backend/internal/pkg/apicompat/anthropic_to_responses_response.go`。

原生 CLI 自身的续聊归一化另列：主响应专项的 36 个工具回填场景中，24 个逐块相同，12 个把
`tool_use → text` 整理成 `text → tool_use`，签名和 redacted data 保留。这不能与这里
两个返回模式对 `text → tool_use` 处理不同混为一谈，也没有证据证明顺序差异必然导致官方拒绝。

## 原生转发、恢复及工程复验

- 全部新跑原生采集加错误回放：**689 场景、1,005 条请求**，请求 PCAP 原字节均相同、
  零丢包。响应专项另核验 108 场景 / 192 条 SSE 的返回原字节，也全部匹配、零丢包。
- 生产 Forward 回放：3,556 个组合，3,532 个逻辑 / wire 字节相同；另外 16 个是 Sonnet
  5.5 非默认采样参数的已有显式 400，8 个是缺 display beta 的已有清理 / CCH 重算。
  没有新增未解释的原生正文差异。同认证且成功的 1,770 个头比较中 1,766 相同，四个
  仍为空 anthropic-version 补默认值。方法、路径、长度、GetBody 和认证方式均按适用性检查。
- 48 组两版 / 三种正文 / 四路径 / 日志开关组合：正文、完整头值 / 顺序、去随机部分的
  ClientHello 一致；24 组日志完整性、摘要和两种假凭据脱敏通过，写入失败 0。
- 264 个新增带 billing 的正文与新建 2.1.295 原生机器码探针逐字节相同，PCAP 匹配、
  零丢包。探针只改 JS 入口和入口 bytecode，原生前缀相同；不计入未修改 CLI 场景数。
- 两版错误归一化前后，44 场景 / 116 请求的次数、编码序列、退出结果一致。
- 托管恢复：292 为 12 会话 / 96 生成 / 24 计数；295 为 16 / 128 / 32。连续压缩、
  迁移后再压缩和续聊通过，跨会话混入 0。
- 服务专项 827 个顶层通过、2,563 个通过事件；四组件包 unit 1,840 个通过事件。
  六个环境入口的跳过逐项记录：扩展恢复、内容、策略、错误及响应观察另跑；短恢复包装
  入口没有单独重跑。没有重跑全仓库 unit / integration / lint，不借用上轮通过次数。

## 既有策略与未覆盖范围

540 项中的 33 个既有策略差异是 OAuth system 包装 30 项和旧模型 redacted prefilter 3 项。
Chat developer 的错误分支另计，不把它掩盖为 system 包装。另保留 24 项转换入口的空工具
结果 `(empty)` 替代，以及 12 项 Sonnet 5.5 非默认采样拒绝。原始差异、政策归类和未解决
差异同时保存，不以删除输入的方式获得通过。

Responses 仍将 `pause_turn`、`model_context_window_exceeded`、`refusal` 映射为 completed；
stop 映射明细独立列出，没有把它当作停止语义完全等价。OpenAI 专属的存储 / previous_response_id、
file_id、service_tier、verbosity、所有 server tools / citations、其他平台、HTTP/2、全部远程
开关、真实账号权限、官方接受和计费均不在本次等价声明内。

## 复现与材料

入口见 [验证 README](../.github/claude-validation/README.md)；机器摘要见
[结构化结果](claude-content-stream-audit-20261010.json)。内容与响应判定器当前均返回 1，
旧参数 / 工具合同判定器返回 0；新增检查不会因观察测试通过而自动改成一致。

完整本机材料：`/Users/kingford/claude-capture/post-tool-audit-20261010-ku0u3z0l/`。
源码偏移 / 哈希、最小输入、最终正文、PCAP、响应、源码 blame 和独立判定分别留档。
完整提取代码、二进制、临时 TLS 私钥及大正文不提交。

准备阶段纠正了 transport probe 位于 internal 导入边界之外、一次 Go 命令工作目录错误、
一次组件命令包含不存在的包路径；保留日志，正确的构建与组件检查通过。判定器根据来源
将已知 OAuth 政策单独分类，并保留最初 153 个差异结果。旧工具负对照首次选到 432 项初期
矩阵，被完整性检查拒绝；随后使用完整 504 项旧证据，增强判定器检出 180 项失败。内容
删除图片的负控制也触发失败。没有为了通过而改生产逻辑或删除输入。

所有本轮实验容器已退出清理，最后只剩原三个业务容器且全部 healthy；本轮没有重启
Docker 或修改业务容器。七类缺口尚未修复，本轮审查材料尚未提交或推送。
