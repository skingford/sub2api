# Claude 源码、隔离 CLI 与转换入口复核（CC-20261010-002）

本轮固定已推送提交 `465ed3738afb317b08c68eb08da04af7ee58c2ef`，运行时代码为
`7e924048b9e67953dbec015bb535bb846cd2972b`，上游基线仍为
`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。生产代码不作修改。

**不完全一致。** 上一轮 Schema、stop、token 上限、Unicode 摘要和嵌套 Schema 修复的固定
断言保持通过；新增工具 / 推理组合检查发现四类兼容转换问题。不能将原生字节保留、普通 API
转换、客户端行为和真实服务接受性合成一个“全部一致”的结论。

## 来源和比较方法

固定官方 Linux x64 CLI，而不是滚动 latest：

| CLI | SHA-256 | 只读提取模块 |
|---|---|---:|
| 2.1.292 | `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3` | 2,260 |
| 2.1.295 | `4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358` | 2,340 |

两版 SDK 均为 0.128.0、runtime v26.3.0。重新提取的全部模块与此前独立提取结果一致；包括
每版三个 Zstd 模块。模块哈希、偏移及请求构建、工具 strict、能力声明、摘要等分支锚点单独保存。
这些是分发包内嵌 JavaScript，不是恢复全部原始 TypeScript。

原生实验使用未修改 CLI、Docker `--network none`、回环域名、本地 TLS、临时配置和假凭据。
响应为合成 SSE。抓包原字节、服务端接收结果、生产 Forward 输出及生产 HTTPUpstream 的
实际发送分别比较。JS 入口替换的原生 CCH 探针单独标记，不计作未修改 CLI。

## 四项新发现

### F1：禁止并行工具调用被丢弃

Chat 和 Responses 显式指定 `parallel_tool_calls:false`，最终 Anthropic 请求却缺少
`tool_choice.disable_parallel_tool_use:true`。无 tool_choice、auto、与 Schema / strict
组合三种情况均复现：六模型 × 两账号 × 两转换入口 × 三情况，共 72 例。

Messages 的等价字段保留。未修改 CLI 经 EXTRA_BODY 发送的原生字段也保留，并经 PCAP
核对。EXTRA_BODY 只证明序列化，不证明所有模型都接受该参数。

原因：`ChatCompletionsToResponses` 保留了指针，`ResponsesToAnthropicRequest` 未消费
`ParallelToolCalls`。这是中间转换丢失显式约束，不属于账号认证或默认策略变化。
位置：`backend/internal/pkg/apicompat/responses_to_anthropic_request.go` 的 tool_choice 转换段。

### F2：工具级 strict=true 被丢弃

Chat `tools[].function.strict:true` / Responses `tools[].strict:true` 转换后消失，
Messages `tools[].strict:true` 保留。单独 strict 与同时指定输出 Schema / 禁止并行两种组合，
六模型 × 两账号 × 两转换入口 × 两情况，共 48 例。

这与上一轮明确不转发的 OpenAI **输出格式包装** name / strict 不同。本项是工具定义上的
显式约束。两版嵌入源码的工具构建分支存在 strict schema 处理，未修改 CLI 的原生发送也保留
显式工具 strict；它们仍不能证明某个真实账号或模型的能力授权。

原因：`convertResponsesToAnthropicTools` 不复制 Strict，目标 `AnthropicTool` 没有对应字段。
后续修复还应验证工具 strict 所需能力声明，不能只增加一个 JSON 字段便声称提供方已经接受。

### F3：高推理强度生成固定预算，超过显式输出上限

旧三模型的 Chat / Responses 指定 `high` 和 1,025 / 4,096 token 上限，最终请求为
`thinking.type=enabled, budget_tokens=10240`，而 max_tokens 保持原上限。三模型 × 两账号 ×
两入口 × 两上限，共 24 例。

对照使用 CLI 的 `CLAUDE_CODE_MAX_OUTPUT_TOKENS` 和 `--effort high`，在请求构建前施加控制：

| 模型与上限 | 两版原生 CLI | Chat / Responses 转换 |
|---|---|---|
| Haiku 4.5，4,096 | enabled，预算 4,095 | enabled，预算 10,240 |
| Haiku 4.5，1,025 | enabled，预算 1,024 | enabled，预算 10,240 |
| Sonnet / Opus 4.6，4,096 | adaptive | enabled，预算 10,240 |

**原生边界不能归因于网关：** Haiku 4.5 的原生 CLI 在 64 / 1,024 上限时也会保留 1,024
的最低预算。本项使用 1,025 / 4,096 控制确认网关额外生成的差异，低上限异常另存证据。
未调用真实提供方，不把模拟 200 写成预算关系合法或官方接受。

原因：旧模型 reasoning 分支直接使用 `defaultThinkingBudget`，没有根据最终上限及模型的
adaptive 能力处理。对应代码位于 `responses_to_anthropic_request.go` 的 126–155 行附近。

### F4：none 推理请求反而启用思考

旧三模型 Chat `reasoning_effort:"none"` / Responses `reasoning.effort:"none"` 没有明确
拒绝，反而发送 `thinking.type=enabled, budget_tokens=10240` 和 `output_config.effort=none`。
三模型 × 两账号 × 两入口，共 12 例。原生关闭思考的控制为 `thinking.type=disabled`。

原因：旧模型分支把任何非 low 字符串都当成需要 enabled thinking，none 又落入默认预算。
5.5 的另行处理不构成旧模型支持证明：Sonnet 5.5 使用 between_tools；Opus / Haiku 5.5 的
该显式请求返回 400。这些差别在矩阵中保留，不以剔除拒绝场景来提高通过率。

## 新矩阵和判定

矩阵固定为六模型 × 两账号 × 三入口 × 14 控制，共 504 例。44 例明确 400（5.5 强制工具选择
及部分 5.5 none 请求），其余 460 例实际发送。新判定器发现 **132 个失败组合**，对应 F1–F4
的 72 / 48 / 24 / 12 个命中；同一组合可同时命中 F1 和 F2，命中数不能直接相加当作案例数。

独立脚本 `validate_tool_constraints.py` 对当前基线明确返回 1；观察测试退出 0 仅表示数据
采集完成。旧 `validate_constraint_contract.py` 和新增失败条件分开保存。未修改生产代码使
其“通过”，也没有将本轮发现改成默认策略来回避失败。

源码 blame 显示工具转换及旧模型预算 / 非 low 分支在早期实现中已经存在；本轮是补齐遗漏
覆盖，不是四项上一轮修复的复发。有限矩阵仍不能穷尽所有任意 JSON 或未来 CLI 版本。

## 比较范围与边界

| 维度 | 本轮方法 | 结论适用范围 |
|---|---|---|
| 原生正文、gzip、分块压缩、count_tokens | 两版重新采集，生产解码和四配置 Forward | 固定捕获与明确策略 |
| 工具、图片、多轮、签名载体、流式结束与缓存用量 | 原生工具场景、服务 / 转换器回归 | 合成响应；不验证真实签名 |
| Schema、stop、token 上限、Unicode 摘要 | 旧固定合同、重新采集及最终发送 | 已列控制保持修复 |
| 工具并行、strict、推理组合 | 新 504 例、原生对照及最终 TLS 字段 | 上述四项不一致 |
| 错误重试与 gzip 降级 | 原始 / 生产转换错误回放给未修改 CLI | 状态、编码序列、请求数与退出状态 |
| 传输与日志 | 两版 × 明文 / gzip / 计数 × 四路径 × 日志开关 | HTTP/1.1、固定 Linux x64 profile |
| CCH | 605 个独立原生既有向量重新跑 Go；三个新生成嵌套 Schema 请求再跑原生机器码 | 算法 / 字节，不是服务端接受 |
| 托管恢复 | 三次压缩、迁移后续聊、双会话、两账号 | 本地生产管理器和未修改 CLI |

另外盘点当前强类型输入结构的 18 个 Responses 字段、19 个 Chat 字段和 13 个 Anthropic
字段。OpenAI 服务端状态、store / include、previous_response_id、缓存提示、verbosity 和
service_tier 等能力未建立一一等价合同；不能将本文理解为完整 OpenAI API 实现。远程开关、
未测平台、HTTP/2、真实订阅、计费、thinking 签名以及提供方生成质量均未验证。

完整本机证据位于 `/Users/kingford/claude-capture/full-recheck-20261010-cgpjnej3/`。
完整提取源码、二进制、TLS 私钥和大正文不提交。

## 最终结果

| 检查 | 结果 |
|---|---|
| 未修改 CLI 主捕获 | 357 场景 / 517 请求 |
| 未修改 CLI 错误回放 | 44 场景 / 116 请求；原始 / 转换后的请求数、编码序列与退出状态一致 |
| 原生总捕获 | **401 场景 / 633 请求，PCAP 全匹配，内核丢包 0** |
| 生产 Forward | 2,068 组合，2,060 个逻辑正文及 wire 原字节相同，错误 0 |
| 同认证应用头 | 1,034 组合中 1,030 个相同；四例为空 anthropic-version 补默认值 |
| 原生传输及日志 | 48 组正文、完整头值 / 头序、归一化握手一致；24 组日志正文 / 摘要 / 完整性和两种假凭据脱敏通过 |
| 旧固定合同 | 192 个永久组合及真实 TLS 发送通过；102 个入口观察与 36 个新摘要请求通过强制判定 |
| 新工具 / 推理合同 | 504 观察，44 个明确 400，132 个失败组合；其余 460 个成功请求实际发送并核验 PCAP，问题在 wire 上仍存在 |
| CCH | 605 个既有独立原生向量的 Go 复验通过；三个新嵌套 Schema 请求与原生机器码输出及 PCAP 一致 |
| 2.1.292 普通托管恢复 | 12 会话 / 96 生成 / 24 计数，跨会话混入 0 |
| 2.1.295 普通托管恢复 | 16 会话 / 128 生成 / 32 计数，跨会话混入 0 |
| 工程专项 | service 620 个顶层测试、2,286 个通过事件；五组件包 813 个通过事件；CCH 606 个通过事件（含顶层测试） |
| 源码冻结 | 3,338 个源码 / SQL / 模块 / 回归 JSON 文件与被测快照一致 |

八个原生正文差异均为 `haiku55-updates-low` 的已记录策略：晚期 EXTRA_BODY 指定 updates，
但缺对应 beta，网关回退 omitted 并重算 CCH。上轮只在 2.1.295 捕获该场景，本轮增加
2.1.292，因此从四个转发配置扩展为八个；不是增加了八项新缺陷。108 个策略观察保留身份冲突、
未知 CCH 改写保护、自定义 origin 等边界，不将它们当作任意请求均应放行。

service 普通专项跳过四个需要专门输入或 CLI 环境的入口；两项策略 / 错误观察和扩展恢复入口
已通过专门命令另跑。短版 `TestClaudeRecoveryNativeCLILab` 包装入口未单独重跑；扩展版复用
其 helper 执行上述连续压缩与迁移场景。没有重跑全仓库 unit、integration 或 lint，不把
CC-20261010-001 的历史工程结果算作
本轮新执行。结构化结果及 70 个关键证据 SHA-256 见
[JSON 记录](claude-tool-reasoning-audit-20261010.json)。

## 实验故障与清理状态

准备时修正了提取器缺少 Zstd 依赖、传输探针 profile 名称，以及 OAuth count 输入没有声明
synthetic_auth 三处实验配置问题。最初 48 组传输中的 32 组通过，16 个 count 组只因错误的
假凭据类型改变头部；保留失败材料，对应 16 组使用正确 OAuth 假凭据后全部通过。

后期 Docker 的宿主共享目录读取卡住，容器内列目录 15 秒无响应；归档复制及新容器启动也
超时，内存诊断仍有约 1.3 GiB 可用。改为通过 docker exec 的标准输入 / 输出传输 tar，
在本次测试容器的本地文件系统继续验证；网络仍为 none，并补上回环域名。其间一次缺少域名
映射的启动失败单独保留，没有计入正式原生样本。原二进制哈希、生产代码和断言未改动。

正式验证已全部完成并导出，容器内的临时副本已删除。Docker 的强制删除仍超时，当前剩余
四个本次实验容器：`hungry_ardinghelli`、`reverent_einstein`、`mystifying_rosalind`、
`serene_neumann`。它们均为本次创建的 network-none 容器；恢复 Docker 共享目录后仍需清理。
没有重启 Docker 或修改已有业务容器。最终清理状态保存于本机 `analysis/cleanup.json`。

本轮仅增加审查工具、失败判定器及记录，四项新发现尚未修复；没有提交、推送、合并或部署。

后续状态见 [CC-20261010-003 修复记录](claude-tool-reasoning-fix-20261010.md)。以上失败数量、
策略和环境状态保留为本次审查基线，不用修复后的结果覆盖。
