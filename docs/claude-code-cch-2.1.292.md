# Claude Code 2.1.292：cch 算法与隔离验证

更新：2026-10-08。对应改动 `CC-20261008-003`，接续 `CC-20261008-002`。

## 结论与适用范围

已恢复本次官方原生安装包的 `cch` 计算规则，并接入 Sub2API 的最终请求构建。
Go 实现与原生 Bun 运行时的 81 个本地用例一致；另核对了未修改 CLI 的历史抓包和新一轮抓包。
本结论限定 Claude Code **2.1.292**，没有验证真实账号、官方验收、订阅资格或封禁规则。

`cch` 是请求正文中的五位校验字段。计算它不需要账号私钥；它本身不能授予订阅权限。
`cc_version` 后的三位归因后缀、`cch`、上游返回的 thinking signature 是三种不同字段。

## 二进制依据

| 项目 | macOS x64 | Linux x64 |
|---|---|---|
| CLI 版本 | 2.1.292 | 2.1.292 |
| 完整 SHA-256 | `a9739a215728ce72435885fedb19d1317ee1ccec61e246fbfb3acaf01689c473` | `a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3` |
| seed 指令虚拟地址 | `0x101c0b1b1` | `0x35f1197` |
| seed 常数字节文件偏移 | `0x1c0b1b3` | `0x33f0199` |
| 字段省略函数入口 | `0x101c13fc0` | `0x35f96f0` |

JavaScript bundle 只构造 `cch=00000`。原生函数把 `c`、`c`、`h`、`=`、五个 `0`
逐字节写到栈上，所以在整个二进制里搜索连续字符串，没有直接找到这段机器码。
定位 seed 后，反汇编确认它与正文查找、xxHash64 初始化、字段跳过和五位小写十六进制写回相连。
[地址与证据文件哈希](../backend/internal/service/testdata/claude_code_2_1_292/cch-source-evidence.json) 随代码保存。

检索线索来自 [CLIProxyAPI 的实现记录](https://github.com/router-for-me/CLIProxyAPI/blob/31f4cfab3fc17f0ebc57b491508f451b190b020f/internal/runtime/executor/claude_signing.go)。
本仓库按本机二进制的原始字节扫描行为实现，并用本地运行时输出核验；没有把该项目对其他版本、
服务端计费或识别方式的结论直接用于本报告。

## 精确计算方式

```text
placeholderBody = 最终序列化的正文，其中 billing 的 cch 为 00000
hashView = 按该版本的原生字节扫描规则，从 placeholderBody 选择参与计算的片段
hash = xxHash64(hashView, seed=0x4D659218E32A3268)
cch = hash 的低 20 位，补零为 5 位小写十六进制
outgoingBody = 在 placeholderBody 中只替换对应的 5 个字符
```

### 占位符定位

1. 找到首个原始字节序列 `"system":[`。
2. 从这个位置开始，在最多 **300 字节**内查找首个 `cch=00000`。
3. 找不到时，原生运行时保留正文，不生成值。
4. 边界实验确认 `/v1/messages`、带查询参数的 messages 和 count_tokens 路径会处理；
   `/unrelated`、`/v1/complete` 或缺少 `anthropic-version` 的请求不处理。

这些是本次测试的端点条件，不能扩展成所有 HTTP 方法、URL 和 header 值的完整判定规则。
Sub2API 只在最终目标为官方 HTTPS origin、POST、messages / count_tokens 时启用本实现。

### 参与哈希的正文片段

原生代码扫描原始字节，保留字段顺序、空格、Unicode 编码和数字书写形式。它不会先解析 JSON 再重新序列化。

| 精确字节模式 | 哈希输入中的处理 |
|---|---|
| `"model":"` | 保留键及两侧引号，跳过值直到下一个引号 |
| `"max_tokens":` 后紧接数字 | 跳过该成员及按原生位置规则选定的相邻逗号 |
| `"fallbacks":[` | 跳过数组成员；识别嵌套数组、字符串和反斜杠转义 |
| `"fallback_credit_token":"` | 跳过该字符串成员及相邻逗号 |
| 其余内容 | 按原始字节参与计算 |

省略动作只影响哈希输入，发出的正文仍保留这些字段。扫描也可能匹配嵌套对象中的同名键。
模型值和 credit token 的结束位置采用原生“下一个引号”规则；改变 JSON 空格、转义或成员位置，
可能改变结果。多个被省略成员连续出现在末尾时，还存在保留前置逗号的原生行为。
测试向量覆盖这些边界，实现没有用“更规范”的 JSON 重排替代原生算法。

因此，对整个 JSON 直接使用旧 seed `0x6E52736AC806831E` 的资料不能直接用于此安装包。

## Sub2API 接入位置

四个构建入口均在模型映射、beta 净化、计数请求字段清理、账号头部覆盖完成后处理校验值：

- messages 常规入口，覆盖 API Key / OAuth；
- messages API Key passthrough；
- count_tokens 常规入口，覆盖 API Key / OAuth；
- count_tokens API Key passthrough。

启用条件是已有原生请求分类、最终 User-Agent 为 `2.1.292`、首个 system 块有相同版本的
billing 归因，并且目标为官方 HTTPS origin。没有原生 billing 的普通 API 请求不会被扩充为完整 CLI 请求。

- 已有 `cch`：先恢复占位符，再对最终正文计算；正文未改变时结果与原生抓包相同。
- 自定义 base URL 导致客户端省略 `cch`：在已有 billing 的 `cc_entrypoint` 后恢复占位符，
  然后按第一方发送路径计算。
- 正文实际改变：同步请求 Body、Content-Length 与 GetBody，保证重试读取相同的最终字节。
- 不支持的版本或目标：继续使用上一轮的正文完整性保护。
- 不支持的 billing 布局、无效 cch、超出定位窗口等情况：本地返回 400，避免改到普通文本中的占位符。
- `claude_native_passthrough=false`：沿用旧兼容构造行为，不启用这项原生计算。

实现见 [cch.go](../backend/internal/pkg/claude/cch.go) 和
[最终请求处理](../backend/internal/service/gateway_claude_native.go)。复用仓库已有 xxhash 依赖，本次没有新增依赖。

## 如何独立验证

### 未修改的官方 CLI

继续使用 `.github/claude-validation/validation.py`，官方二进制 SHA 保持原样。
新一轮覆盖基础请求、Read 工具回填、503 重试、两轮对话、假 OAuth、Unicode 和 `/context`。
12 条模型 / 计数请求中有 9 条带 cch，全部重算匹配；三条 count_tokens 没有该字段。
再核对已有 10 条带 cch 的 Linux 2.1.292 样本，全部一致。历史 macOS 沙箱的第一方 messages
样本也匹配 `0b0ea`，三条 count_tokens 没有该字段；
[macOS 核对摘要](../backend/internal/service/testdata/claude_code_2_1_292/cch-macos-verification.json)。
两平台合计核对 20 条带 cch 的未修改 CLI 请求。重试复用相同正文时校验值相同。

新抓包的接收端正文与 PCAP 解密原始字节逐一一致，内核丢包数为零。
摘要见 [未修改 CLI 核对记录](../backend/internal/service/testdata/claude_code_2_1_292/cch-unmodified-cli-verification.json)。

### 原生运行时边界实验

为了提供 CLI 正常参数不容易构造的空格、转义、嵌套键和占位符位置样本，实验在**副本**中替换 JS 入口，
关闭该入口的预编译 bytecode，让自写测试脚本调用同一原生 `fetch`。校验源码没有注入这个副本。

- 官方二进制完整哈希先校验。
- Bun payload 之前的 `89,120,776` 字节逐字节保持一致，包括原生 HTTP 与哈希机器码。
- 记录修改位置、探针脚本哈希、修改后文件哈希。
- 全程 Docker `--network none`，官方域名映射回环，只挂载实验文件，使用固定假凭证。
- 81 个向量涵盖字段排列、嵌套、转义、数值、JSON 空格、Unicode、150 KB 文本、窗口边界和发送条件。
- Go 计算结果与原生接收结果比对完整正文 SHA-256；独立 PCAP 再核对接收端字节。

这些边界样本标为“原生运行时探针”，不会冒充未修改 CLI 的正常产品流程。
可复现脚本与步骤见 [隔离验证 README](../.github/claude-validation/README.md#cch-原生运行时对照)。
向量见 [cch-2.1.292.json](../backend/internal/pkg/claude/testdata/cch-2.1.292.json)。

## 其他参数与算法的核对状态

| 项目 | 当前处理与证据 | 剩余范围 |
|---|---|---|
| `cc_version` 三位后缀 | 提取原函数；UTF-16 索引 4 / 7 / 20、salt、SHA-256 截断，10 个原函数向量通过 | 转换后的 wire messages 不一定等于 CLI 计算时的逻辑首条用户消息，优先保留原生值 |
| `cch` | 本文算法、81 个运行时向量、20 条未修改 CLI 的历史 / 新样本核对 | 其他版本和架构需另取二进制验证 |
| JSON 与 beta 相关参数 | 原生字节保留；必要策略在最终哈希之前执行 | 策略改动后的语义可能不同于调用方原请求 |
| session / device / prompt / request ID | 保留原生字段；绕过原生路径的身份缓存和会话重写 | 独立运行的随机值本来就会不同；非 CLI 身份生成未宣称全面等价 |
| thinking signature / redacted data | 作为上游返回的不透明内容保留 | 无法用 cch 算法生成服务端签名 |
| 503 重试 | 原生捕获验证正文一致、dispatch 头透传；最终 GetBody 回归 | 网关的账号切换、故障重试时间不等于 CLI 的完整调度 |
| TLS / HTTP 头顺序 | 上一轮 Linux / macOS x64、HTTP/1.1 与四条连接路径对照通过 | TLS 会话恢复、HTTP/2、ARM64 未覆盖；握手随机材料每次重新生成 |
| OAuth / 订阅 / 风控 | 使用原账号认证和权限处理 | 隔离模拟器无法验证服务端计费、账号接受或封禁概率 |

本机完整原始实验存档：`/Users/kingford/claude-capture/cch-investigation-20261008/`。
仓库只保存复现脚本、合成向量和核对摘要，不提交二进制副本、临时证书私钥或真实凭证。

## 最终工程检查

- 完整后端 unit：57 个包通过。
- 完整 integration：首次 50 个包通过，repository 包因本地 Redis 测试容器启动超时失败；
  单独重跑该包通过，合计 51 个包通过。保留首次失败日志。
- golangci-lint：0 issues。
- 六个实际请求构建组合通过最终正文 / 长度 / 校验回归，已有原生、旧身份策略及传输测试通过。
- 复现脚本语法和文档链接检查通过；81 条最终探针记录与 PCAP、已提交向量一致，丢包为 0。
