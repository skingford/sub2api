# Claude 内容与响应修复（2026-10-10）

对应记录：CC-20261010-005，跟进 [CC-20261010-004](claude-content-stream-audit-20261010.md)。
**七项问题均已修复，最终固定合同、实际发送及工程检查通过。**
基线 `1004275707dc5b6b9b17ecf8dee27346696fe23d`，上游仍为
`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。CLI 固定 Linux x64 2.1.292 / 2.1.295，
SDK 0.128.0、runtime v26.3.0；沿用并复核 004 的二进制及来源证据，不扩大版本范围。

## 修复内容

| 问题 | 修复后的行为 |
|---|---|
| F1 developer 角色降级 | Chat 保留 developer 角色进入中间协议，Anthropic 目标转入 system；OAuth 的既有 system 包装仍单列 |
| F2 URL 图片丢失 | 普通输入和工具结果支持 HTTP(S) URL source，保留 URL；网关不下载图片 |
| F3 旧式函数历史丢失 | assistant function_call 与 function 结果成对转换；为连续同名调用生成不同 ID，避免后一个结果覆盖前一个 |
| F4 非法工具参数仍发送 | 参数必须是合法 JSON object；解析 / 编码失败明确 400，不再生成 content:null 或发送残缺历史 |
| F5 工具结果文档丢失 | Responses function_call_output 中的 input_file 转为 document，保留 source 和顺序 |
| F6 签名 / redacted data 丢失 | Responses 跨六模型保留标记过的不透明块；Chat 的两种返回模式和下一轮请求通过专用历史字段完整回传 |
| F7 非流式响应改变顺序 | 按原始 block 顺序输出；仅合并相邻文本，遇到 thinking / tool_use 先结束当前文本项 |

同时补上 Chat buffered SSE 的 signature_delta 累加，并保留模型别名下的签名。
普通 OAuth 请求中的有效 redacted data 在启用思考时也予以保留；已有的无效签名处理、
显式禁用思考和原生请求保留边界继续接受回归检查。

## Chat 的历史回传合同

Chat Completions 标准字段没有原生 signed / redacted block 的位置与载荷。收到这些内容时，
响应的 `choices[0].message.anthropic_content` 新增一个字符串；流式在 finish chunk 之前
发送一次 `choices[0].delta.anthropic_content`。它使用 `anthropic-history-v1:` 前缀和
Base64 JSON，保存完整块及客户端可见内容的对应值。它不是加密，也不是签名有效性证明。

客户端应保存该字段，并在下一轮 assistant 消息中与原 content / tool_calls 一起原样回传。
流式客户端保留这一次字符串增量即可，不需要合并嵌套块数组。不回传这个字段，网关无法
从普通文本恢复原始签名；这类客户端应使用能保留原生块的 Messages 或 Responses 接口。
不含不透明块的普通 Chat 响应不附加此字段。

有冲突的正文 / 工具调用、损坏的历史编码、不支持的块类型、缺少签名的 thinking，均明确
拒绝。普通 Chat→OpenAI Responses 转换入口也会拒绝该 Anthropic 历史字段；普通
OpenAI encrypted_content 不会被当作
Anthropic 历史。工具名还原同时处理历史中的普通块，保持签名和 redacted data 的原始字节；
记录流式实际可见内容，避免跨分片别名造成回传冲突。reasoning_content 是显示文本，不能
替代不透明块。

## 输入策略与回归范围

保留原 540 个内容输入。修复后 24 个非法工具 JSON 请求改为发送前 400，加上原 12 个
Sonnet 5.5 非默认采样拒绝，共 36 个拒绝；504 个有效输入继续发送。不是跳过失败输入。
36 个 system / developer 的 OAuth 包装差异仍保留在原始比较中，必须实际具有既有包装
和确认消息才能归入该政策；原来错误的 Chat developer 普通 user 文本仍会被判失败。
旧 redacted prefilter 的三个差异已经修复，不再列为政策豁免。

无法转换的明确媒体源（包括只有异方 file_id 的文档）现在拒绝并要求提供 file_data，
不再悄悄删掉文件。原 file_id 测试输入保留，预期从静默删除改为明确报错。普通 base64
图片 / 文档、URL 图片、工具文本、空结果替代和 Unicode 对照均纳入回归。

永久测试增加 540 项 service 合同、六模型的签名 / redacted 往返、流式分片、重复完成事件、
模型与工具别名、历史冲突、大整数参数及连续同名旧式函数历史。旧 504 工具 / 推理合同、
192 参数合同和 Unicode 摘要合同继续独立执行。

## 验证记录

最终源码冻结为 3,347 个后端 Go / SQL / 模块 / 回归 JSON 文件。
初轮全量 unit 58 包通过；独立响应预验 797 个响应 / 3,188 比较通过，实际 Chat 处理器
176 个两模式 / 两账号回传通过。初轮 lint 找到一个拆分转换入口后未再调用的私有包装
函数；完整 lint 随后要求显式处理 strings.Builder 的恒 nil 错误返回值。两处修正均保留
初轮记录，并在最终 3,347 文件快照上重新运行工程与协议检查，不混用不同源码结果。

最终验证使用 `verified/snapshot/`，工作区 3,347 个文件与快照逐文件 SHA-256 一致。
全部结果均有 [结构化记录](claude-content-stream-fix-20261010.json)：

| 验证 | 最终结果 |
|---|---|
| 内容合同 | 原 540 输入完整保留；504 成功、36 明确拒绝，未解决内容差异 0；36 个 OAuth 包装差异单列 |
| 工具 / 推理与旧约束 | 原 504 组、192 个请求、102 入口观察、36 摘要请求全部按合同通过 |
| Responses 返回 | 821 个原始响应 × 四路径，共 3,284 比较，差异 0 |
| Chat 往返 | 44 个组件往返、176 个两模式 / 两账号实际处理器往返，签名 / data 丢失和转换错误均为 0 |
| 实际发送 | 504 内容 + 452 工具 + 192 旧约束 + 176 Chat = 1,324 请求，接收端字段判定与 PCAP 均通过、零丢包 |
| CCH | 662 个生成正文与独立 295 原生运行时完整字节相同，PCAP 通过、零丢包 |
| 原生 Forward 回放 | 3,556 个组合的结果与原审查分类一致：3,532 字节相同、16 个已有模型拒绝、8 个已有 display-beta 回退；头差异仍仅四个空版本补值 |
| 真实传输 | 48 组正文 / 完整头值与顺序 / 归一化 ClientHello 对照通过；24 组日志及凭据脱敏通过 |
| 新跑 CLI 托管恢复 | 292：12 会话 / 96 生成 / 24 计数；295：16 / 128 / 32；跨会话混入 0 |
| 全量 unit | 58 包、23,026 个通过事件，失败 0 |
| 全量 integration | 52 包、13,752 个通过事件，失败 0、退出 0，使用真实测试 PostgreSQL / Redis |
| lint / 模块 | golangci-lint 2.14.0：0 issues；go mod verify、tidy diff、diff 空白检查通过 |

负控制仍拒绝旧数据：内容旧 540 输入检出 123 个未解决差异（新规则不再豁免旧 redacted
丢失），原响应主语料 88 / 最小语料 12 个差异，Chat 44 个旧往返全部失败。
原始审查的 120 / 153 数字与分类保留，不覆盖为新版判定器数字。

原生请求与返回语料复用 004 的已核验捕获；本轮重新运行生产构建器 / 响应处理器、实际
TLS 发送及两版未修改 CLI 的托管恢复。独立 CCH 探针只替换 JS 入口，机器码前缀重新核验
相同，不计作未修改 CLI。没有把复用语料计成再次采集的 689 个场景。

最终测试容器、临时 IPv4 Docker 代理及 socket 已清理；原三个业务容器均 healthy，
本轮没有重启 Docker 或修改业务容器。当前修复与审查材料尚未提交、推送或部署。

本机证据目录：`/Users/kingford/claude-capture/content-fix-20261010-iqw0orzj/`。
生产请求构建、真实 TLS 发送、模拟端接收及原生 CLI / 机器码对照分开记录。全部凭据和
模型回复均为隔离合成材料；没有验证真实官方接受、签名有效性、订阅权限或计费。
