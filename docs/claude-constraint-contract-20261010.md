# 显式约束与摘要一致性修复（CC-20261010-001）

本次修复 [CC-20261009-013](claude-postfix-audit-20261009.md) 的四项问题，并将对应观察升级为
[固定验收合同](claude-validation-contract.md)。工作开始于 2026-10-09，完成验证记录于 2026-10-10。
当前源码基于 `afb0c04a361a10ac17dc55ea00a573f1b415fcc7`，上游仍为
`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。

## 为什么此前反复出现差异

这些缺口原先就存在。之前的对照偏重原生转发和常规续聊，没有把跨 API 的显式约束、参数组合、
Unicode 摘要和嵌套 Schema 固定为失败即阻止验收的断言。观察脚本的 PASS 只表示成功收集数据，
也曾被过度概括成“完全一致”。本次保留旧失败证据，并以明确范围及强制断言替代这种结论。

有限验证仍不能证明所有未知输入、未来 CLI 或真实服务等价。新增支持必须先扩大合同和独立证据，
再扩大一致性声明；有意保留的策略差异继续显式列出。

## 修复行为

### F1：JSON Schema 与 effort 同时保留

- Responses.text.format 和 Chat.response_format.json_schema 转为 Anthropic 的
  output_config.format。完整 schema 作为原始 JSON 传递，不删除 required、enum、
  additionalProperties、引用或注释内容。
- OutputConfig 同时容纳 format 与 effort；各 thinking / reasoning 分支修改 effort 时不再
  用一个新对象覆盖 Schema，Sonnet between_tools 提前返回路径也保留约束。
- 支持 text、json_schema；缺失 / 非对象 schema、json_object 和未知模式，在转换入口明确
  返回 400，不继续发送一个没有约束的成功请求。
- 第一方、非原生转换的 schema 请求声明两版固定 bundle 中的
  `structured-outputs-2025-12-15`。策略禁止或生效的 header override 移除该能力时明确拒绝。
  保留旧 `output_format` / 2025-11-13 兼容路径；不改原生 CLI 的能力声明，也不对自定义 origin
  擅自加入第一方 beta。
- 补查嵌套 schema.default.system 数组会遮蔽原生 CCH 的首个 system 定位：仅对网关生成的
  兼容请求，在必要时把真实 system 放到首位，保留其他字段值及原始子 JSON。原生输入不重排。
  三个入口的嵌套 Schema 已增加回归及独立原生运行时校验。

CLI 的 --json-schema 在已捕获场景中可使用 StructuredOutput 工具；这里是 OpenAI 到原生 API
format 的转换，不把两种实现形式写成逐字节相同，也不以模拟响应宣称真实模型已经遵守 Schema。

### F2 / F3：面向 Anthropic 的 Chat 约束转换

新增目的明确的 `ChatCompletionsToAnthropicRequest`：在模型映射后转换，保留 stop 为原生
stop_sequences，保留显式正值 token 上限，max_completion_tokens 优先。
真正发送给 OpenAI Responses 的旧 token floor 不受影响；不会因为用了中间数据结构就把
Anthropic 目标的 1 / 64 token 限制抬到 128。

停止词支持字符串或字符串数组，保留 Unicode、转义及顺序；错误类型、本应为正值的零 / 负数
上限明确拒绝。模型别名映射在转换前完成，避免把客户端别名误当成 OpenAI 模型而删除采样参数。
转换失败对六模型统一返回客户端 400，不能只在 5.5 模型上报告错误。

### F4：匹配原生 JavaScript 的摘要空白规则

摘要标签内部及最终文本均使用 ECMAScript 空白集合：去掉 BOM，保留 NEL；不再直接使用
Go TrimSpace。62 个独立原生 JS 函数向量覆盖全部相关空白、非空白对照、多标签和空摘要。
摘要来源、历史前缀、保留消息及签名保护未删除。

## 固定验收与结果

固定 CLI：Linux x64 2.1.292 / 2.1.295，SDK 0.128.0、runtime v26.3.0，沿用前次已核验二进制
SHA-256。原生摘要实验使用未修改 CLI、Docker network-none、临时配置、假凭据、本地 TLS
和模拟 SSE。工程验证在不可变源码快照内运行，不混用编辑中的修订。

- 192 个永久断言组合：六模型 × 两种账号 × 三入口，检查 Schema、Schema+effort、1 / 64
  token、单 / 多停止词；不为 Responses 虚构 stop 合同。
- 原审查的 102 例入口观察转换为硬性验收；已验证该工具会拒绝旧结果，修复结果必须通过。
- 两版重新采集空格 / BOM / NEL 六场景、36 请求，PCAP 字节及零丢包检查完成；生产恢复
  序列必须全部通过，而不是只看 CLI 对模拟器退出 0。
- 实际发送生产导出的 192 个请求，在接收端逐一检查约束并与 PCAP 比较；这仍是本地组件
  联调，不是官方模型生成验收。
- 嵌套 Schema 的三个入口，另用保留原生机器码的 2.1.295 CCH oracle 比较最终字节。
- 既有 1,528 个原生转发组合和普通连续压缩 / 账号迁移重新回归；不将有意保留的四例 display
  beta 清理差异从统计中抹掉。

详细数值和最终工程结果见 [结构化记录](claude-constraint-contract-20261010.json)。
本机完整材料：`/Users/kingford/claude-capture/constraint-fix-20261009-qvh78u69/`。

最终冻结源码结果：**58 包 unit、52 包 integration 全部通过，golangci-lint 2.14.0 为 0 issues**。
192 个实际 TLS 发送请求与 PCAP 全匹配、丢包 0；两版 Unicode 摘要的 36 个请求均通过恢复回放；
三个嵌套 Schema 的完整字节与独立原生 CCH oracle 相同。
原生既有 1,528 个转发组合仍为 1,524 个原字节相同、四个明确 display-beta 策略差异。
普通恢复仍通过 292 的 12 会话 / 96 生成 / 24 计数，以及 295 的 16 会话 / 128 生成 / 32 计数，
跨会话混入均为 0。模块校验、tidy diff 和差异空白检查通过。

本机 Docker 仍有双栈动态端口探测问题；数据库集成检查使用仅面向本次新建 testcontainers 的
临时 IPv4 端口绑定代理，不修改全局 Docker 配置、业务容器、生产代码或原断言。
首轮一次 Redis 内部就绪探测超时保留在日志中，最终结果单独记录。

## 留痕与边界

新增目的端转换器、结构化能力处理、固定约束测试、原生 Unicode 向量、实际发送验证器及
失败闸门；保留 013 的历史发现，以本条更新修复状态。暂未提交、推送、合并或部署。

输入约束保留、原生字节转发、默认策略与真实提供方接受是四种不同结论。既有账号凭据处理、
缺失版本补值、缺 display beta 的回退、不同入口的已记录默认策略保持原合同。
本次没有验证真实订阅、计费、thinking 签名、所有远程开关、HTTP/2 或未测平台。
