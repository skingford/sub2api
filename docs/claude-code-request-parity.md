# Claude Code 2.1.286 请求对齐

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
| TLS / HTTP 传输层 | 本次 recorder 核对 JSON 与应用头，没有捕获真实上游 TLS / HTTP2 链路 | 不能声称已验证完整网络指纹一致 |

这些差异不证明具体的封号、额度或客户端识别规则。本次修复限于可验证的请求兼容问题；
账号权限、订阅资格和服务端策略仍由上游决定。

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
GOTOOLCHAIN=go1.27.0 go test -tags=unit ./internal/service -run 'TestClaudeCode2286|TestAnthropicClientRequestID' -count=1
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
  自定义地址、域名混淆、已有值、空值、唯一性及四个构建器。最终执行结果见 PR。
- `go test -tags=unit ./...`：全部通过。
- `go test -tags=integration ./...`：全部通过，包含 Redis 与 PostgreSQL 容器测试。
- golangci-lint 2.13.0（由 Go 1.27.0 构建）：0 issues。

本机首次集成检查曾卡在 `docker-credential-desktop`，使用独立的临时匿名配置拉取
公开测试镜像后通过；没有修改默认 Docker 配置。复现该处理方式：

```bash
test_docker_config=$(mktemp -d)
printf '%s\n' '{"auths":{"https://index.docker.io/v1/":{}}}' > "$test_docker_config/config.json"
DOCKER_CONFIG="$test_docker_config" CI=true GOTOOLCHAIN=go1.27.0 \
  go test -tags=integration ./...
```

上述命令仍在 `backend` 目录执行。本次没有进行生产部署或真实 Anthropic 请求。
