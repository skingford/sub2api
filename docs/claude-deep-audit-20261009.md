# CC-20261009-008：合入 release 后的深入对比

基线：`9833384c65ddb574b2054d3c9e0a7dd83d359f5c`（PR #5 合入后的 release，包含 gzip 修复及 Go / HTTP2 升级）。
工具链为 Go 1.27.2；原生对照固定为未修改 Linux x64 Claude Code 2.1.292 / SDK 0.128.0，
二进制 SHA-256 `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。
上游基线仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。
生产源码先用 git archive 固定，本轮只增加审查工具和记录。

## F1：编码回退后的 CCH 不一致

已解码的原生 gzip 请求，在退出 `finalizeNativeClaudeRequest` 的 known 分支后，会发送未压缩 JSON，
却保留 gzip 使用的 `cch=00000`。逻辑正文摘要没有变化，因此现有完整性检查允许发送；它没有覆盖
“逻辑 JSON 相同，但压缩方式改变后必须重新计算 CCH”的情况。

可复现触发条件：

- 实际仍用 2.1.292 CLI，通过 `ANTHROPIC_CUSTOM_HEADERS` 把 User-Agent 改成 2.1.293。
  新抓包中 CLI 发送 gzip；网关的 API Key / OAuth 四个配置组合全部变成明文，仍带原占位符。
- API Key 上游账号使用允许的 UA 覆写（较新 CLI 版本或自定义 UA），或设置
  `claude_native_passthrough=false`。运行时 gzip、分块 gzip，各两种 passthrough 配置均能复现。

独立原生运行时重算与真实发送的例子：

| 配置（API Key / normal） | 实际发送 | 实际 CCH | 原生重算 CCH |
| --- | --- | --- | --- |
| 运行时 gzip → native-off | 未压缩 JSON | `00000` | `90fb0` |
| 运行时 gzip → ua-newer | 未压缩 JSON | `00000` | `90fb0` |
| 分块 gzip → native-off | 未压缩 JSON | `00000` | `47671` |
| model-map 对照 | 未压缩 JSON | `90fb0` | `90fb0` |

16 条发往第一方地址的生产请求导出，经真实 Go transport 发送：12 条 CCH 与独立原生运行时不同，
4 条模型映射对照正确；全部实际发送字节与 PCAP 一致、丢包 0。局部上游始终返回 200 以记录请求，
没有模拟“官方因 CCH 拒绝”，也没有验证真实官方是否接受这些请求。

定位：[native finalizer](../backend/internal/service/gateway_claude_native.go) 的 known 条件与提前返回
（第 115–123 行），以及入口解压后的正文状态。现有 `TestClaudeNativeGzipHonorsScopeAndHeaderPolicy`
只要求该分支不恢复 gzip、正文与返回值相同，没有检查明文 CCH 是否正确。

修复应将“保留原编码”“能否计算已知版本 CCH”“是否选择原生传输”分开：未改动的原始 gzip 可以
按适用策略保留；要发送已知版本的明文时必须对最终字节计算校验；无法安全生成时应明确拒绝不兼容组合。
本记录不改生产行为，保留 CC-20261009-006 的已验证范围。

## F2：丢失 cf-ray 改变原生 CLI 的错误重试决策

`chunk-hy08191v.js` 的 `X/h/C7r` 用 `request-id` 的 req_ 前缀或 `cf-ray` 的存在判断响应来源。
对于 403，带 cf-ray 的拒绝不会触发压缩降级；缺少这两个来源标记则会尝试其他编码。
[生产错误返回](../backend/internal/service/gateway_claude_compatibility.go)第 180–184 行只转发
request-id / retry-after / retry-after-ms / x-should-retry，丢失 cf-ray。

将生产转换器导出的真实响应交给未修改 CLI 回放，在同一 403 持续返回、600,000 字符系统提示、
分块 gzip 开启、`CLAUDE_CODE_MAX_RETRIES=0` 的条件下：

| 响应标记 | CLI 接收原始错误 | CLI 接收网关转换后的错误 |
| --- | --- | --- |
| 仅 cf-ray | 1 次，分块 gzip | 3 次：分块 gzip → 运行时 gzip → 明文 |
| request-id | 1 次 | 1 次 |
| 无来源标记 | 3 次 | 3 次 |

仅在实验中给转换后的响应补回原 cf-ray，请求数恢复为 1，最终仍是预设的 403。
共 7 个回放场景、13 条真实 CLI API 请求，全部 PCAP 字节相等、丢包 0。
这证明额外请求来自来源标记变化；普通 SDK 重试开关不控制这层压缩回退。

这里没有运行完整网关部署，也没有宣称所有线上 403 都会固定重发三次：先导出生产错误转换器的输出，
再将该响应逐字交给原生 CLI，比较它的实际决策。实际账号冷却等状态可能进一步改变后续响应。
现有“caller owns retries”测试验证了网关单次上游调用及状态码，未覆盖下游 CLI 收到转换响应后的决定。
应在保留消息脱敏和账号隔离的同时，保留此类会影响原生决策的响应标记并增加下游行为回归。

## F3：错误体重建丢失可恢复的 bad_json 语义

原生 `te/h/C7r` 除标准 Anthropic 错误外，还识别
`{"error":"bad json: unexpected EOF","reason":"bad_json"}` 和纯文本 `bad json: unexpected EOF`。
[错误消息提取器](../backend/internal/service/gateway_upstream_response.go)没有处理这两种形式，
[错误返回器](../backend/internal/service/gateway_claude_compatibility.go)第 171–174、190–191 行将它们
统一替换为 `api_error / Claude upstream rejected the request`，原生恢复条件因此消失。

使用相同带 request-id 的 400，在本地模拟“gzip 返回解析错误，明文返回有效 SSE”，其余设置与 F2 相同：

| 上游错误形式 | CLI 收到原始响应 | CLI 收到生产转换后的响应 |
| --- | --- | --- |
| error 字符串 + reason=bad_json | 3 次请求后明文成功，退出 0 | 首次 gzip 报错停止，退出 1 |
| 纯文本 bad json | 3 次请求后明文成功，退出 0 | 首次 gzip 报错停止，退出 1 |
| 标准 error.message 解析错误前缀 | 3 次请求后成功 | 3 次请求后成功 |

另 6 个场景、14 条原生请求（10 gzip）全部通过 PCAP 核对，丢包 0。
这些成功 / 失败均针对上述模拟服务，不代表官方接受；它验证了生产转换器确实改变了原生客户端的恢复结果。
F2 和 F3 应一起处理：保留来源标记后，还需安全保留或映射已知的解析错误类别，同时保持敏感信息脱敏。
仅补上 error 字符串的提取仍不够：包装成标准 error.message 后，原生分支只认已知前缀，
因此需要保留原分类或映射到已验证的标准解析错误表示。

## 不作为新缺陷的差异

- 空 `anthropic-version`：原生自定义头允许发出空值，网关补为 `2023-06-01`。
  这是必要协议头的现有缺省策略，不能计为完全逐头相等，也没有证据证明空值被真实服务接受。
- OAuth 上游关闭原生保留后，默认旧身份策略重写 session ID，四个消息组合在本地返回 400，发送次数 0。
  [已有规则](claude-code-gap-fixes.md)明确会话约束优先，不能用开关绕过身份隔离；这不是本轮新增缺陷。
- 普通 API 与 CLI 的后置 EXTRA_BODY / verbose 入口：23 组参数对照中，8 组保留先前已记录的
  thinking.display、thinking/context_management/output_config 或 temperature 缺省差异。
  原生完整请求与普通 API 转换具有不同输入来源，不能用这些样本证明需要取消合法性约束。
- 成功响应仍受可配置白名单过滤：实际默认过滤器只保留 x-request-id，丢失 request-id 和测试中的
  anthropic-ratelimit-requests-remaining；显式加入 additional_allowed 后两者都能保留。因而不能声称
  成功响应头完全一致。原生 `ne()` 还用 request-id 判断压缩回退后的 API 响应，跨进程关闭状态可能
  受影响，这是源码推断，本轮没有验证跨进程后果；它与 F2 错误分支的固定头列表不同。
- 自定义上游、显式关闭原生 transport、SDK 头改写等按策略退出原生传输。
  四个 custom-origin 明文样本也保留 CCH 占位符，但其目标不属于第一方验证范围，另列观察。

## 覆盖与结果

| 层次 | 本轮结果 |
| --- | --- |
| 未修改 CLI 主矩阵 | 85 场景、139 请求，其中 44 条 gzip；全部 PCAP 原字节一致，丢包 0 |
| 原生错误决策 | 另 13 场景、27 请求（20 条 gzip）；全部 PCAP 相等、丢包 0；主矩阵与错误回放合计 98 场景 / 166 请求 |
| 完整转发 | 556 个配置组合逻辑正文全部一致；552 个实际字节一致，4 个差异均为自定义 UA 的 gzip 回退 |
| 同认证头 | 278 个组合中 274 个完全一致；另 2 个丢失编码头、2 个补齐空协议版本 |
| 其余样本 | 排除上述两个头策略场景，137 请求 / 548 组合的正文与发送字节、274 个同认证组合的应用头全部一致 |
| 原生 CCH | 593 个运行时独立向量全部与 Go 相同；含新增固定种子 256 个，PCAP 全部一致，81 个旧黄金哈希也通过 |
| cc_version 后缀 | 提取原 JS 函数与 Go 的 394 个向量一致；含新 Unicode / 空字符边界，400 次执行含末批填充重复 |
| HTTP/TLS | 14 个输入 × 4 路径 × 日志开关，共 112 组：字节、完整头值、头序、1499 字节 ClientHello 非随机部分全相同 |
| 请求日志 | 56 组完整正文 / 偏移 / 摘要一致，API Key 和 OAuth 假凭据均未泄漏，写入失败 0 |
| 会话恢复 | 12 会话、96 生成、24 计数；连续压缩、迁移后压缩与续聊通过，跨会话混入 0 |
| 专项回归 | service 主套件 548 个通过事件（含子测试），2 个实验入口跳过、专用 CLI 联调另跑；另 38 个合同 / 参数、16 个错误信号观察通过；108 策略结果复跑不变，成功响应过滤器观察通过 |
| 数据库 / 网络 | repository 19 个顶层测试、51 个通过事件，含 PostgreSQL 会话 / 恢复事务、连接复用、取消、证书验证与 HTTP2 回退 |

未重复全仓库 unit / integration / lint；生产代码未变，相关行为以本轮专项和真实传输复验为准。

## 新增边界证据

- 源码 `chunk-hy08191v.js` 的 `ovt/aes/C7r`：普通请求门槛 4,096 个 JS 字符；压缩级别仅 1–9 有效；
  根据响应来源标记和错误文本决定压缩降级。`chunk-47d8fnm7.js` 的 `oos/Sto` 和
  `chunk-9yn9h839.js` 的 `aM/dee`：分块门槛 524,288 个 JS 字符，显式设置应用编码头。
- 实测 4,052 字符请求未压缩，5,208 字符请求为运行时 gzip；501,171 字符仍为运行时 gzip，
  525,234 字符进入分块模式。Unicode 请求 1,201,271 字节 / 467,937 UTF-16 单元仍为运行时 gzip，
  1,501,235 字节 / 584,567 单元进入分块模式，证明不能按字节数判断门槛。
- 连续两次“无法解析 JSON”响应：实际序列为分块 gzip → 运行时 gzip → 未压缩 → 下一轮仍未压缩。
  全部进入转发复验；三种状态及后续锁定状态均进入实际传输矩阵。
- 实验中旧的 host bind mount 抓包在大明文连续发送时丢包：原生运行时 203 包、Go 发送 79 包。
  原始材料保留；改为 tmpfs 采集后导出，20 条原生重算和 16 条 Go 发送重新通过完整 PCAP 核对，丢包 0。
  这是采集缺口，不作为产品网络丢包结论。一次准备脚本相对路径错误已改用绝对路径重跑。

## 可追溯材料与边界

新增 `deep_native_lab.py`、`deep_policy_audit_test.go`、`deep_policy_wire_lab.py`、`deep_error_replay_lab.py`、
`verify_deep_runtime.py`、`ram_capture.py`、`deep_response_header_audit_test.go`；复验步骤见 [.github/claude-validation](../.github/claude-validation/README.md)。
[结构化摘要](claude-deep-audit-20261009.json)保留来源偏移、源码 / 依赖 / 工具和结果哈希、异常样本及算法计数。
完整材料：`/Users/kingford/claude-capture/deep-audit-20261009-o0w0wdwu/`。

所有 CLI 与网络实验使用 Docker network-none、假凭据和回环地址。算法 oracle 只替换实验副本的 JS
入口，保持原生可执行前缀；它与未修改 CLI 抓包分开计数。后缀函数使用提取原函数在 Node 下执行，
不是一次独立 CLI 启动。服务联调使用测试存储 / 本地上游，PostgreSQL 验证单列。
范围为固定 Linux x64 CLI 的 messages / count_tokens 及列出的恢复、传输行为；不证明所有平台、
未来 CLI 版本、全部后台请求、真实签名、订阅资格、计费或官方接受。
