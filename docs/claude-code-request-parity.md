# Claude Code 原生请求对齐

Updated: 2026-10-08

本 fork 的上游基线已同步至 v0.2.14 / `3f1a2ea0`。Claude 相关改动编号、提交和验证记录
统一维护在 [Claude 改动记录](claude-change-log.md)。

## 基线

本 fork 以 Claude Code 2.1.286 原生 macOS x64 客户端的六份合成请求为回归基线。
原二进制 SHA-256：
`53e6a936e89519d695230f9cc97943991286b72766674fba11bee845f0a7c047`。

原版 CLI 使用虚构凭证、独立配置目录和测试文件连接本地 HTTP 模拟服务，覆盖：

- 空工具的 auto 请求。
- Read 工具定义及工具结果回填。
- 显式 default 权限模式。
- HTTP 503 前后的重试请求。

样本位于 `backend/internal/service/testdata/claude_code_2_1_286/`。
这些报文验证客户端序列化，不代表服务端接受所有内部 beta，亦不构成订阅、额度或账号资格的证明。

### 2.1.291 新增基线

新增 Linux x64 与 macOS x64 两份官方原生 CLI 报文，版本与 manifest 哈希均已核对。
两份二进制内嵌 Bun 1.4.3（`eecfd55de`）；请求头中的 SDK 版本为 0.128.0。

| 样本 | 隔离与目标 | 正文 |
|---|---|---|
| linux-firstparty | Docker `--network none`，官方域名在容器内映射到 127.0.0.1 | 1083 字节 |
| macos-loopback | 进程沙箱仅允许指定回环 HTTPS 端口 | 928 字节 |

Linux 请求从 PCAP 使用模拟服务器会话密钥解密，24 个头及正文 SHA-256 均与接收端记录一致。
macOS 样本来自 HTTPS 接收端，没有特权网卡抓包。均使用假 Key、空工具、合成系统提示、
`--bare` 和显式 default 权限模式，属于非交互 `sdk-cli` 场景。

样本位于 `backend/internal/service/testdata/claude_code_2_1_291/`。
`.body.json` 保留原始正文；回归检查经过 API Key / OAuth 构建分支后的正文逐字节一致，
以及入站凭证不会泄漏到上游。OAuth 分支的合成凭证测试不代表真实订阅接受情况。

### TLS 密钥日志验证

Linux 与 macOS 原生版均未通过 `SSLKEYLOGFILE` 或 `NODE_OPTIONS=--tls-keylog=…` 导出客户端密钥；
直接传 `--tls-keylog` 均被 CLI 拒绝。Linux 的服务器密钥成功解密原 PCAP；macOS 的 Node.js
对照在同一沙箱导出 5 条与服务器一致的密钥，因此不能把结果归因于日志目录不可写。
这些结论只覆盖所测运行模式和 TLS 1.3 路径，没有连接真实官方 API。

Linux 在关闭非必要流量的设置下仍向模拟器请求了 settings、policy_limits 和 messages。
本次没有外网连接，依靠的是 Docker / 进程沙箱，而非单个环境变量。

### latest 2.1.292 升级对照

2026-10-07 核对官方指针：latest 为 2.1.292，stable 为 2.1.285。本机 CLI 检测时已指向
2.1.292，文件哈希与 manifest 相符；Linux 同版本文件也已下载校验。两者仍内嵌 Bun 1.4.3。

沿用相同模型、提示、权限模式和假 Key，分别在断网容器与 macOS 沙箱完成验证：

- 请求头集合与顺序、beta 列表、正文业务字段没有发现变化；SDK 头仍为 0.128.0。
- 原始报文的版本归因、cch 取值和随机标识会变化，macOS 的临时端口也不同；不能称原始报文字节相同。
- 对比只归一化上述已列明的字段，没有忽略其他业务差异；两种平台均得到相等结果。
- Linux 模型请求仍为 2182 字节完整 HTTP、1083 字节正文；macOS 正文仍为 928 字节。
- 密钥日志实验结果没有变化。首次 Linux 抓包因缓冲未完整采集模型流，校验失败；启用即时采集后重跑并通过对照。

新增样本位于 `testdata/claude_code_2_1_292/`。原生透传沿用客户端自己的版本信息；本次新增
版本回归，不把单个场景的归因字段或默认身份模板推广给其他请求。

## 对齐原则

原生客户端请求中的 system、消息顺序、message-level output_config、工具 schema、
tool_use/tool_result、metadata 和条件 safeguards 应按最终 beta 能力保留。
上游认证继续使用所选账号的凭证；入站 Cookie 与下游 Key 不应被转发。

每个请求的模型、thinking、权限模式、账号类型与功能开关都可能不同，因此不能将
一次抓包的全部 beta、设备标识或安全分类器上下文无条件复制到其他请求。

## 已定位的兼容差异

| 项目 | 2.1.286 样本 | 原有网关行为 | 对齐方向 |
|---|---|---|---|
| 消息级 output_config | 使用 mid-conversation-system + per-turn-control | 仅识别旧 mid-conversation-output-config，删除新控制消息 | 同时识别旧 token 和新能力组合 |
| thinking 与 temperature | adaptive 时未携带 temperature | 普通 OAuth 规范化缺字段时补 1 | thinking 活跃时保留 temperature 缺省；显式值保持不变 |
| safeguards | 仅在对应请求和 beta 存在时发送 | 最终 beta 被过滤后仍可能发送不匹配请求 | 缺 dangerous-tool-use beta 时明确返回 400，不静默删除或生成安全上下文 |
| helper-method | 本次 .create(stream:true) 未携带 | 兼容路径将 streaming 等同于 SDK .stream helper | 不无条件生成 |
| x-client-request-id | 本地模拟请求未携带；源码在第一方路径生成 | 旧兼容路径无条件生成，首轮修改又将缺省生成一并删除 | 按出站 origin 处理：官方 HTTPS 地址缺失时生成，已有值保留，自定义地址不补造 |
| 请求关联头 | 按 tracing / Agent / 压缩场景出现 | 部分官方条件头不在白名单 | 白名单透传已知非认证头，不主动制造值 |
| diagnostics（2.1.291） | 与 cache-diagnosis beta 同时出现，previous_message_id 可为 null | 过滤 beta 后正文仍残留；兼容路径不保留调用方显式诊断 beta | 按最终 beta 保留或清理诊断，兼容路径支持显式请求，不生成缺失字段 |

## 继续追踪源码：能否确认“识别 Sub2API”

### 1. 没有找到按项目名判断的客户端规则

对该版本格式化后的 JavaScript 做 `sub2api|sub_2_api|sub-2-api` 大小写不敏感检索，
未找到匹配。检查本 fork 的 Anthropic messages / count_tokens 构建器、identity service
与 HTTP client，也未发现默认把项目名写入上游请求头或 system 的代码。
这不覆盖用户自定义提示词、账号 header overrides、其他中继或服务端实现。

模型在回答中自称“通过 Sub2API 调用”、HTTP 层拒绝请求、以及账号被归入某种额度，
是不同的现象。模型文本可以受历史消息和系统提示影响，不能单凭一句回答确认服务端检测。
本次没有真实拒绝响应，因此没有将某个字段差异认定为拒绝原因。

### 2. 本地抓包遗漏了第一方发送分支

以下行号对应 Prettier 3.9.9 格式化的固定样本，不适用于其他构建：

| 源文件与函数 | 确认的行为 |
|---|---|
| `chunk-ntpz2rzv.js:1278`，`Us / Ng / Bh` | 未设置 base URL 默认符合第一方条件；显式 URL 按 host 判断是否为 `api.anthropic.com` |
| `chunk-t0f1bt6g.js:18184`，`nUe` | 将 provider 与上述条件组合，用于每次尝试的关联头 |
| `chunk-k5k1ek0j.js:97579`，`PNt` | 符合条件时生成 `x-client-request-id`；`traceparent` 另受 tracing 条件控制 |
| `chunk-zfdndjae.js:30533`，`gw / s9` | fetch 包装层在第一方条件成立且头不存在时补请求 ID |
| `chunk-28mep0c1.js:84`，`YUr` | 归因文本构造器在第一方 / Vertex 分支加入 `cch` 占位字段；自定义地址省略 |

因此，“localhost 没有请求 ID”和“新版全面取消 cch”两个推断都不成立。
这里只确认 `cch` 在构造阶段的条件，不能把占位内容当成直连最终报文或服务端认可的凭据。
本 fork 修正相关注释，没有补造该字段或改变其现有处理。

2026-10-07 补证：2.1.291 的 Linux 隔离测试保留官方 origin，解密后的实际出站归因文本中
出现了非占位的 cch 字段。它仍是合成请求的原生输出，不能推断生产服务端如何校验。
回归样本将其作为不透明正文保留；本轮没有实现归因字段的生成或重算。

本轮将请求 ID 补全放在四个 Anthropic messages / count_tokens 请求构建入口，
与 SDK helper-method 分开。网关限定为 HTTPS、无 URL userinfo、默认 443 端口的
`api.anthropic.com`；这一范围比 CLI 内部 host 判断更严格。内部环境覆盖和 AWS
分支没有移植。请求 ID 仅用于关联请求，不是官方客户端认证或订阅资格证明。

### 3. 已发现但不能归因于服务端检测的差异

| 位置 | 静态证据 | 可以得出的结论 |
|---|---|---|
| `internal/pkg/claude/constants.go` | 内置 CLI 基线 2.1.258、SDK 0.94.0、Node v24.3.0；固定样本为 2.1.286、0.127.0、v26.3.0 | 兼容默认值不是本次样本的完整运行环境；macOS x64 抓包也不能代表所有平台 |
| `claude_code_version_sync_service.go` | 自动同步 CLI 发布版本号，其他 SDK / 运行时默认值独立维护 | 版本号更新不等于实际升级了整套客户端 |
| `gateway_upstream_request.go` | OAuth mimic 路径跳过入站头，使用默认集合；metadata 和 session 还可能按配置改写 | 原生透传与兼容转换的出站结果需要分别审查 |
| `gateway_billing_block.go` | Go 按 UTF-8 字节索引取样；CLI JavaScript 字符串索引语义不同 | 非 ASCII 文本的计算结果存在差异，不能宣称所有内容字节对齐 |
| TLS / HTTP 传输层 | 2026-10-08 本地隔离对照发现默认传输、内置 TLS profile 均与原生 CLI 不同，详见下文 | 请求正文相同不意味着握手、头部顺序或完整链路相同 |

这些差异不证明具体的封号、额度或客户端识别规则。本次修复限于可验证的请求兼容问题；
账号权限、订阅资格和服务端策略仍由上游决定。

2.1.291 的新样本进一步确认原生转发应保留客户端携带的 SDK / 运行时信息。现有兼容模式的
默认身份模板不等于这两份样本，不能把单个版本的 Linux 或 macOS 默认值套到所有请求上。
本轮的“对齐”以原生报文保真和诊断能力联动为验收范围。

## 配置与边界

- Anthropic API Key 账号可使用已有 `anthropic_passthrough` 路径。
- OAuth 的统一指纹、metadata 重写、会话 ID 屏蔽和账号级 header overrides 仍受现有配置控制。
  若运营方主动开启重写，出站相应字段会按账号配置变化，不能同时要求这些字段字节不变。
- 管理员过滤 beta 或覆盖 anthropic-beta 时，最终出站 beta 是正文能力处理的依据。
- safeguards 可以含本机目录、用户名、规则和 Git 上下文；仅转发调用方已经提供的内容，
  不从网关主机收集或补造这些数据。
- 不复制测试中的设备 / 会话 ID、模型默认值或本机架构到所有用户请求。
- 不接入额外遥测、反馈或设备登记端点，不改变计费、限流、账号选择与权限校验。

## 验证

```bash
cd backend
GOTOOLCHAIN=go1.27.0 go test -tags=unit ./internal/service -run 'TestClaudeCode2286|TestClaudeCode229[12]|TestAnthropicClientRequestID' -count=1
GOTOOLCHAIN=go1.27.0 go test -tags=unit ./...
GOTOOLCHAIN=go1.27.0 go test -tags=integration ./...
golangci-lint run --timeout=30m ./...
```

单元测试使用本地 recorder / mock，不调用真实 Anthropic 服务。
集成测试需要 Docker，使用项目测试容器；CI 使用 Go 1.27.0 和 golangci-lint 2.13。

### 2026-10-03 验证结果

- 六份报文分别经过 API Key 和 OAuth 转发，共 12 个完整正文对比用例通过。
- thinking 缺省 / 显式温度、条件请求头、beta 过滤、幂等和 safeguards 拒绝路径通过。
- 第一方 origin 分支补充：11 个原始函数隔离用例通过；请求 ID 回归覆盖官方地址、
  自定义地址、域名混淆、已有值、空值、唯一性及四个构建器，全部通过。
- `go test -tags=unit ./...`：全部通过。
- `go test -tags=integration ./...`：全部通过，包含 Redis 与 PostgreSQL 容器测试。
- golangci-lint 2.13.0（由 Go 1.27.0 构建）：0 issues。

上述全量检查已在本轮 origin 修正后重新执行并通过。

本机首次集成检查曾卡在 `docker-credential-desktop`，使用独立的临时匿名配置拉取
公开测试镜像后通过；没有修改默认 Docker 配置。复现该处理方式：

```bash
test_docker_config=$(mktemp -d)
printf '%s\n' '{"auths":{"https://index.docker.io/v1/":{}}}' > "$test_docker_config/config.json"
DOCKER_CONFIG="$test_docker_config" CI=true GOTOOLCHAIN=go1.27.0 \
  go test -tags=integration ./...
```

上述命令仍在 `backend` 目录执行。本次没有进行生产部署或真实 Anthropic 请求。

### 2026-10-07 验证结果

- 新增两份 2.1.291 样本的 4 个转发组合，原始正文逐字节对比通过。
- 修复前复现诊断 beta 被丢弃及诊断字段残留；修复后针对性回归通过。
- 诊断字段显式 null、缺省不生成、策略过滤、账号覆盖和清理幂等性通过。
- 合并 v0.2.14 后，缓存诊断修复的全量 unit / integration 已通过，lint 0 issues。
- 2.1.292 新增 4 个逐字节转发组合，针对性回归通过；包含最新样本的最终全量 unit / integration 全部通过，lint 0 issues。

### 2026-10-08：启用身份服务及扩展场景验证

本轮从 `release / f6b76203` 开始，上游基线仍为 `3f1a2ea0`。只运行假凭证和隔离模拟服务。
可复现的脚本、Dockerfile 与命令见 [隔离实验说明](../.github/claude-validation/README.md)。

#### 新增原生样本

| 场景 | 请求数量 | 已确认的客户端行为 |
|---|---:|---|
| 普通请求 | 1 | 原生请求头与正文基线 |
| Read 工具 | 2 | 发出工具定义，并将模拟服务指定的本地文件读取结果回填到下一条请求 |
| HTTP 503 重试 | 2 | 正文不变；第二次新增 `anthropic-dispatch-id`，本次 `X-Stainless-Retry-Count` 仍为 `0` |
| 两轮对话 | 2 | 第二轮包含上一轮 assistant 消息、新 user 消息，以及上一请求关联信息 |
| 假 OAuth 环境变量 | 1 | 不使用 `--bare`；客户端生成 Bearer 认证及 OAuth / extended-cache-ttl beta |
| 中文与 emoji | 1 | 保留原生 Unicode 正文和对应归因内容 |
| `/context` | 3 | 原生 `count_tokens` 请求不包含 messages 接口的完整 system / metadata |

官方 Linux x64 2.1.292 文件哈希与上一轮一致。全部 12 条模型或计数请求已用本地 TLS
服务器会话密钥解密 PCAP，原始正文 SHA-256 与接收端记录一致，正式采用的捕获内核丢包数为 0。
两条 Sub2API 传输组件重放请求也通过相同核对。每次 CLI 启动仍出现本地 settings / policy_limits 请求。

采集过程的纠正：Read 首次运行时，可变参数选项吞掉了提示词，加入 `--` 分隔符后重跑；
`/context` 首次捕获丢了 25 个包，增大 tcpdump 缓冲到 16 MiB 后重跑三条请求全部通过。
Unicode 复核使用 tshark 原始字节字段，避免其显示文本的字符替换造成错误结论。

#### 复现并修复的两处问题

1. **重试关联头丢失**：原生 CLI 在 503 后携带 `anthropic-dispatch-id`，网关白名单原先将它删除。
   现在仅在调用方提供时透传，并保持已观察到的小写形式；不默认生成此字段。
2. **同版本归因被重算**：生产 `IdentityService` 启用后会同步 billing 版本。
   原实现即使版本已经是 2.1.292，仍重算后缀；中文样本由 `f98` 变为 `004`，
   带 CLI 自动插入提示块的假 OAuth 样本由 `357` 变为 `bc4`。
   现在版本相同时保留原生归因内容，仅在配置确实改变版本时保留原有兼容处理。
   这项修复不实现或声称验证了 cch 的生成算法，也不证明改写其他正文后 cch 仍被官方接受。

修复前有 20 个新增回归用例失败：2 个重试头用例和 18 个同版本归因用例。
修复后，12 条原生报文的 API Key / OAuth 共 24 个转发组合通过；身份配置矩阵共 180 个组合、
每组连续请求两次，通过正文差异、凭证隔离、身份缓存和会话稳定性断言。
完整后端 unit / integration 均通过，golangci-lint 0 issues；正式 token 计数样本更新后，
新增转发与身份配置回归再次通过。本轮没有前端或部署变更。

#### 身份配置矩阵的结论

回归使用生产 `SettingService`、`IdentityService` 和 `GatewayService.Forward` / `ForwardCountTokens`，
以内存实现替代设置存储与身份缓存，以 recorder 替代上游网络。没有加载真实用户配置或凭证。
矩阵包括空缓存、旧版本缓存、同版本但平台 / SDK 不同的缓存。

| 配置 | 本轮样本的正文结果 | 请求头结果 |
|---|---|---|
| 指纹统一关闭，metadata 透传开启 | 逐字节保留 | 保留原生应用头；凭证仍替换为所选上游账号，OAuth beta 按路径补充 |
| 默认配置：指纹统一开启，metadata 透传关闭 | 有 metadata 的请求重写 user_id；其他正文保持 | 应用账号缓存，并让 session 头与正文一致 |
| 仅指纹统一开启 | 正文保留，包括同版本归因 | 同版本的已有缓存可能覆盖客户端平台、SDK 和运行时 |
| 仅 metadata 重写 | 只改 user_id | session 头随正文变化 |
| 另开启会话 ID 固定 | 只改 user_id，重复请求保持会话值 | session 头与重写后的值一致 |

因此，默认 OAuth 配置仍不等于原生请求全字段透传。已有同版本账号缓存中的 SDK `0.94.0`
和 `arm64` 也不会因为新客户端带着 SDK `0.128.0` / `x64` 就自动更新。它是现有账号级配置行为，
本轮没有擅自修改生产设置或重写所有缓存。缺少 metadata 的原生计数请求没有被补造 metadata。

#### 传输层实测差异

使用生产 `repository.NewHTTPUpstream(nil).DoWithTLS` 重放同一份请求，服务器仅提供 HTTP/1.1：

| 客户端 / 传输 | 本轮 JA3 |
|---|---|
| 官方 Linux 原生 CLI 2.1.292 | `1523504b38f0fae0d881d4b6554aac1b` |
| Sub2API 默认传输 | `9b7dcdf3f997f1fb7b4409c94cb7ef36` |
| Sub2API 内置 TLS profile | `44f88fca027f27bab4bb08d4af15f23e` |

JA3 是握手中部分参数的摘要，不是客户端身份证明。内置 profile 相比本轮原生 CLI 多了
extension `65037`，缺少 supported group `4588`；普通传输的差异更多。
两条 Go 传输的 HTTP 头以 Host / User-Agent / Content-Length 开始，原生 CLI 的顺序不同。
正文相等，但不能称完整 HTTP / TLS 报文一致。此结果不覆盖 macOS、HTTP/2、实际出口或所有连接状态。
本轮没有为消除差异而修改 TLS 模板。

#### 仍需区分的范围

- 兼容模式的旧 SDK / 运行时模板及归因构造仍与原生请求不同；仅更新 CLI 版本号不能完成整体对齐。
- 新样本验证假 OAuth 环境变量下的客户端序列化，未验证真实账号登录、订阅权限或官方服务接受情况。
- 图像、长上下文压缩、并行工具、其他模型与交互式权限模式尚未在本轮矩阵中覆盖。
- 追踪依据：[样本来源](../backend/internal/service/testdata/claude_code_2_1_292/validation-provenance.json)、
  [PCAP 与握手核对摘要](../backend/internal/service/testdata/claude_code_2_1_292/validation-transport-summary.json)。
