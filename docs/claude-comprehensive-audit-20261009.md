# Claude Code 2.1.292：源码与隔离运行全面复核

日期：2026-10-09（Asia/Shanghai）。记录：CC-20261009-003。

**常规原生转发、已覆盖模型的默认配置和核心校验算法已对齐，但所有输入和发送形式并不完全一致。**
本轮新增确认 gzip 分支及三个普通 API 显式参数分支的差异。上一轮未验收的 5.5 默认值和
托管压缩修复，本轮已纳入验证并通过。

审查对象为 `719ceb3522dfcab8035d2552a6ff620554eeb94b` 的实现，对应后续文档提交
`fee18477636cd23eb99b2c5a49caf7acd2d3d250`。07:50:41 保存的完整后端快照与该提交的
文件集合和内容逐项一致。release 基线 `b4430850`，上游 `3f1a2ea0`，未执行合并或部署。
审查期间后来出现的未提交请求追踪实现不在本轮验收范围；不将旧二进制结果用于它们。

[结构化证据](claude-comprehensive-audit-20261009.json) 保存逐组合结果、参数差异、源码定位、
抓包与日志哈希。源码快照清单 SHA-256：
`7b8d161d8b2d4fa81292dbf800c60202a8a5ac98269f4fe7e39ddc495c9ba537`。

## 1. 方法和覆盖范围

固定官方 Linux x64 CLI **2.1.292 / SDK 0.128.0**，二进制 SHA-256：
`a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。

- **静态依据**：重新从同一二进制只读提取 2,260 个内嵌 JavaScript 模块，沿请求构造、
  身份、默认参数、能力、压缩、重试等关键分支核对；重新反汇编 Linux cch seed 和扫描函数。
  这不是恢复原始 TypeScript，也不表示人工审阅了每个模块。
- **未修改 CLI**：51 个场景，89 条请求，其中 77 条生成、12 条计数。CLI 均在
  `--network none` 容器内，用假凭证、独立 HOME / 配置、回环 TLS 模拟器运行。
- **生产转发**：同一捕获输入经过 API Key / OAuth × passthrough 开关，共 356 个组合。
  使用真实入口正文解压函数、CLI 校验器及 Forward / ForwardCountTokens；存储和上游响应使用测试端口。
  OAuth 的两个 passthrough 配置不是两个不同的发送实现。
- **生产恢复服务**：未修改 CLI 实时调用恢复与 Forward 服务，3 模型 × 2 认证 × 2 会话，
  12 条会话、96 次生成和 24 次计数，覆盖连续压缩、迁移、迁移后再次压缩。
- **持久性**：另用 PostgreSQL 容器运行 7 个集成测试，验证事务、租约、摘要失效、
  墓碑、原子账号归属和隔离；不把测试存储冒充数据库验证。

原生捕获的 89 条请求全部通过 PCAP 原始字节对照，内核丢包 0。gzip 的压缩字节和
解压后的 JSON 分别验证，没有用 Wireshark 解压显示文本冒充原始发送字节。

## 2. 总体结果

| 项目 | 结果 | 解释 |
|---|---|---|
| 未压缩原生请求 | 87 条 × 4 = **348/348 正文逐字节一致** | 包含 tools、图片回填、thinking、metadata、计数、重试、resume 和压缩控制帧 |
| gzip 原生请求 | 2 条 × 4 = **8 个组合正文变化** | 入口解压、删除 Content-Encoding，并重算 cch，详见下节 |
| 方法 / URL / 长度 / GetBody | **356/356 通过** | 长度与 GetBody 对应最终实际发送的正文，不要求等于压缩输入长度 |
| 同认证方式的应用头 | **174/178 完全一致** | 其余 4 个是 gzip 输入删除 Content-Encoding；凭证、Host、Connection 等单独处理 |
| 跨认证应用头 | 150 个组合增加 OAuth beta | 属于认证路线变化；凭证本身按目标账号替换 |
| 五模型普通默认值 | Sonnet 4.6、Opus 4.6、Haiku 4.5、Sonnet 5.5、Opus 5.5 对齐 | 普通输出匹配普通输出；verbose 的 updates 差别单独说明 |
| cch 算法 | **337/337 原生运行时对照一致** | 81 个既有向量 + 固定种子新增 256 个边界样本；压缩分支的触发条件另论 |
| 归因后缀 | **138/138 原函数向量一致** | Linux 提取的 Ak 函数在 Node 执行，结果与生产 Go 函数比较 |
| 原生 TLS | **60/60 ClientHello 非随机内容一致** | 排除随机数、session ID、临时公钥；完整 1499 字节结构，不只比较 JA3 |
| 实际传输 | **4/4 路径一致** | 直连、HTTP CONNECT、HTTPS CONNECT、SOCKS5 的正文、完整头值、头序及非随机 ClientHello |
| 托管恢复 | **12 条会话全部完成，跨会话混入 0** | 96 次生成、24 次计数；持久性另在 PostgreSQL 验证 |

本轮算法探针仅修改实验副本的 JavaScript 入口，原生 HTTP / 哈希机器码保持不变。
这些探针与 89 条未修改 CLI 请求分开计数。337 条探针的接收字节亦通过独立 PCAP 对照，
丢包 0。归因的 138 个独立向量通过既有十向量测试分批执行，末批补齐两个重复项，
共 140 次调用，不把补齐项计入独立向量数。

## 3. 当前仍存在的差异

### A. gzip 是解压转换，不能算原样转发

开启 `CLAUDE_CODE_GZIP_REQUEST_BODIES=1`、使用足够大的正文后，未修改 CLI 发出：

- `Content-Encoding: gzip`；
- gzip 数据解压后的 billing 仍为 `cch=00000`。

生产入口 `ReadRequestBodyWithPrealloc` 解压并删除 Content-Encoding / Content-Length，
随后 `finalizeNativeClaudeRequest` 按最终未压缩正文重新计算 cch。最终 JSON 与输入解压
JSON 的哈希不同，压缩字节当然也不同；这解释了 8 个正文差异和 4 个同认证头差异。

该结果来自成功 gzip 请求及收到合成 415 的 gzip 请求。415 样本没有自动改成未压缩
重发，CLI 退出 1，不能据此宣称 gzip 降级已经验证。

这证明发送形式不同，不证明官方服务拒绝。若维护目标是语义转换，需明确记录这条边界；
若目标是原始报文保真，则需另行设计压缩传递。**不能只把解压后正文的 cch 强改回 00000**
来追求表面相同，因为那不是本轮验证过的未压缩原生路径。

依据：`chunk-hy08191v.js` 的 `ovt`、`chunk-9yn9h839.js` 的压缩路径；
[入口解压](../backend/internal/pkg/httputil/body.go)、
[最终校验](../backend/internal/service/gateway_claude_native.go)。

### B. 显式关闭 thinking 的缺省字段不同

同一 Sonnet 4.6：原生 `MAX_THINKING_TOKENS=0` 发出 `thinking={"type":"disabled"}`，
仍带 `temperature=1`、`output_config.effort=high`。普通 API 传相同 disabled 控制时，
网关不补这两个字段。

参数省略与显式赋值的差异已经实测；本轮没有证明真实服务端最终行为是否相同。
这不影响已有原生请求的保留路径。

### C. disabled thinking 的额外字段清理不同

通过 CLI 的 `CLAUDE_CODE_EXTRA_BODY` 输入：

```json
{"thinking":{"type":"disabled","display":"updates","budget_tokens":2048}}
```

原生构造末尾的 `_ur` 清理为 `{"type":"disabled"}`。普通 API 转换则保留 display 和
budget_tokens，并因 display=updates 添加相应 beta；不是同一规范化算法。
CLI 此时已构造的 effort / context_management 与网关也不同。

这是明确的输入清理差异。模拟器返回成功不表示该 disabled 组合会被官方接受。
若需要兼容 CLI，宜为 disabled thinking 明确清理或拒绝规则，并补专门回归。

### D. 显式 temperature 与自动 thinking 的组合策略不同

CLI 的 `CLAUDE_CODE_EXTRA_BODY={"temperature":0.4}` 在已构造参数上覆盖 temperature，
保留 adaptive thinking、high effort 和上下文编辑。普通 API 显式传 temperature=0.4 时，
网关为保留调用方采样控制，不自动注入 thinking，也不补对应 effort / 上下文编辑。

这是输入接口及合并顺序不同导致的策略差别，不能直接把它描述成转发丢参。
若要复制 CLI 的这一分支，需要明确选择相同的构造语义，而不是对所有 API 输入强加 thinking。

B / C / D 的网关依据：[模型默认值](../backend/internal/service/gateway_claude_2292_defaults.go)、
[正文规范化](../backend/internal/service/gateway_claude_oauth_body.go)。逐字段值保存在结构化记录的
`generic_parameter_comparisons` 中。

## 4. 重试和模式不能只看一个头

本轮分别注入 400、401、403、408、409、429、503、529，以及 x-should-retry true / false：

- 普通 400 和 403：各发一次，CLI 退出 1。
- 401、408、409、429、503、529：本实验的第一次拒绝后各发第二次并成功；
  这不等于真实认证刷新或权限恢复验证。
- 400 配 `x-should-retry=true`：两次；503 overloaded_error 配 false 在本场景也出现两次。
- 上述两次发送的正文和 prompt ID 保持，x-client-request-id 改变，
  **x-stainless-retry-count 两次都为 0**。

因此 SDK retry-count 或一个重试头不足以描述 CLI 全部控制流。源码中同时有 SDK 的
shouldRetry / retryRequest 和 CLI 层的错误分类及请求循环。网关的当前约定是单次发送，
保留错误和重试信号，由调用方控制后续；本轮相关生产回归通过。没有声称 Go 内部复制了
CLI 的全部退避、能力自愈或非流式 fallback 控制器。

普通输出的 thinking.display=omitted 与 verbose / stream-json 的 updates 也属于正常
模式差别；不把五个 verbose 样本与普通转换的这项不同算成五个缺陷。

## 5. 源码和算法证据

结构化记录保存模块 SHA-256、字节偏移与关键锚点。主要对应关系：

| 原生依据 | 对应核对内容 |
|---|---|
| `chunk-9yn9h839.js`：`_ne`、`Ak` | 过滤逻辑 isMeta 消息；UTF-16 下标 4/7/20、salt、SHA-256 三位后缀 |
| `chunk-9cgz7jx3.js` | billing、entrypoint、prompt / turn 标识的条件构造 |
| `chunk-b8z199dv.js`：`pne`、`Gso` | 模型上限、thinking 开关、普通与 verbose 显示分支 |
| `chunk-az075kqs.js`：`wN` | effort 环境变量与 unset / auto 分支 |
| `chunk-br6ckz11.js`：构造器、`_ur`、`BZ` | 最终参数合并、disabled 清理、缓存条件、压缩标记、能力自愈 |
| `chunk-9yn9h839.js` 与 `chunk-28613mzz.js` | SDK 头、SDK 重试与 CLI 错误分类 |
| `chunk-hy08191v.js` | gzip 开关、适用条件、失败分类 |
| Linux `0x35f1197`、`0x35f96f0` | cch seed `0x4D659218E32A3268`、原始字节扫描；与 337 条运行时样本交叉验证 |

随机边界样本固定种子 2921009，覆盖字段顺序、空格、嵌套同名键、转义、Unicode、
不同数字与 fallback 布局。cch 是请求校验值，不能替代服务端 thinking signature。
原生归因的逻辑消息选择发生在 CLI 序列化之前，普通 API 未必拥有同样的本地逻辑消息。

## 6. 工程检查、纠正和未覆盖部分

- component 专项 148 个通过事件、service 专项 351 个通过事件、数据库 7 个测试通过。
  数量包含子测试。service 运行未提供 CLI 路径时跳过两个实验入口；扩展实时入口另在
  指定 CLI 的断网容器成功运行，基础旧实验入口未单独重跑。
- 算法、抓包、最终转发审查、四条代理发送和实时恢复均完成；未重跑整个仓库的全量
  unit / integration / lint，不把旧全量检查算成本轮结果。
- 捕获器最初 47 组后遇到目录重名，另建目录补抓缺失 4 组；已修正执行顺序。
- 最初 effort-low 把参数放在 `--` 后，导致成为提示文本；已独立重抓并替换最终集合中的
  这一组，旧结果保留但不参与最终结论。
- gzip PCAP 需要读取编码实体的原始字段；验证器已修正。传输镜像缺代理 helper 的首次
  运行失败，显式挂载仓库 helper 后重跑完成。以上均是实验接线问题，不冒充产品缺陷。

尚未覆盖真实官方接受、订阅 / 计费、真实 OAuth 刷新、真实签名、HTTP/2、TLS 会话恢复、
ARM64、其他 CLI 版本、所有 feature flag / thread / speed 分支、远程代理与全部 MCP / 工具。
JSON schema 场景只验证已出现请求的保留，不证明模拟回答满足完整结构化输出流程。
测试上游与测试存储、单独数据库测试不等于一个带真实鉴权的完整部署端到端验收。

原始证据：`/Users/kingford/claude-capture/comprehensive-20261009-qlx_n3xe/`。
复现入口见 [验证 README](../.github/claude-validation/README.md#源码驱动的全面复核)。
本轮只新增审查工具、证据和报告，未修改生产实现、提交、推送、合并或部署。
