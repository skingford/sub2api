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
| x-client-request-id | 本次请求均未携带 | 兼容路径每次生成一个 UUID | 客户端透传路径保留已有值，不为缺失值无条件生成 |
| 请求关联头 | 按 tracing / Agent / 压缩场景出现 | 部分官方条件头不在白名单 | 白名单透传已知非认证头，不主动制造值 |

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
GOTOOLCHAIN=go1.27.0 go test -tags=unit ./internal/service -run 'TestClaudeCode2286' -count=1
GOTOOLCHAIN=go1.27.0 go test -tags=unit ./...
GOTOOLCHAIN=go1.27.0 go test -tags=integration ./...
golangci-lint run --timeout=30m ./...
```

单元测试使用本地 recorder / mock，不调用真实 Anthropic 服务。
集成测试需要 Docker，使用项目测试容器；CI 使用 Go 1.27.0 和 golangci-lint 2.13。

### 2026-10-03 验证结果

- 六份报文分别经过 API Key 和 OAuth 转发，共 12 个完整正文对比用例通过。
- thinking 缺省 / 显式温度、条件请求头、beta 过滤、幂等和 safeguards 拒绝路径通过。
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
