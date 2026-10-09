# Sub2API · skingford 维护版

基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 的个人维护 fork。
保留上游 AI API 网关能力，重点维护 Claude Code 请求兼容、可复现的协议验证和改动记录。

- **仓库**：[skingford/sub2api](https://github.com/skingford/sub2api)
- **默认维护分支**：[release](https://github.com/skingford/sub2api/tree/release)
- **Claude 改动记录**：[docs/claude-change-log.md](docs/claude-change-log.md)
- **请求验证报告**：[docs/claude-code-request-parity.md](docs/claude-code-request-parity.md)

新增可选的 [Claude 托管会话恢复](docs/claude-managed-recovery.md)：不同用户、分组和 session 隔离，账号不可用时在明确边界创建新上游会话；默认关闭，需配置摘要服务和加密密钥。

## 分支与维护方式

| 分支 | 用途 |
|---|---|
| `main` | 每天检查并快进同步 `Wei-Shaw/sub2api:main`，保留上游提交 |
| `release` | 本 fork 的维护主分支，包含已验证的自定义改动；人工决定何时合并 `main` |
| 主题分支 | 从 `release` 开始开发，后续 PR 提交到 `release` |

定时同步由 [GitHub Actions](https://github.com/skingford/sub2api/actions/workflows/sync-upstream.yml)
在云端执行，计划为每天 **09:00（Asia/Shanghai）**，也可点击 **Run workflow** 手动触发。
无需本地电脑或 Codex 运行。两边相同时不操作；分叉或推送失败时任务失败并保留日志。
工作流只快进更新远端 `main`，`release` 仍由维护者人工合并。
配置、运行限制和人工合并流程见 [开发指南](DEV_GUIDE.md#main-定时同步与-release-人工更新)。

`release` 已启用[分支保护](https://github.com/skingford/sub2api/rules/24660893)：禁止强推和删除。
当前仍允许普通快进推送，尚未要求必需 CI 状态检查。

每次 Claude 相关改动都记录来源版本、修改原因、证据、涉及文件、验证结果和提交引用。
代码提交通过 `Claude-Change-ID` 与记录关联，已发布记录的纠正通过后续条目保留历史。
具体规则见 [开发指南](DEV_GUIDE.md)。

## 当前维护内容

截至 2026-10-07，上游基线为 **v0.2.14 / `3f1a2ea0`**，Claude CLI 最新对照版本为 **2.1.292**。

本 fork 已维护的 Claude 请求兼容改动包括：

- 保留原生消息级控制字段，以及 thinking 活跃时省略 temperature 的行为。
- 保留已知请求关联头；按实际目标 origin 补充缺失的请求 ID。
- 按最终 beta 保留或清理缓存诊断字段，支持调用方显式请求缓存诊断。
- 对缺少对应 beta 的 safeguards 请求返回明确错误。
- 透传原生 503 重试关联头；CLI 版本一致时保留客户端生成的归因内容，避免重算破坏 Unicode 等请求。
- 原生请求默认保留 metadata、会话与身份头，避免账号缓存覆盖；归因后缀按反编译函数的 UTF-16 规则处理。
- 对已验证的 CLI 2.1.292 / Linux、macOS x64，自动选择匹配的 TLS 与 HTTP/1.1 头序；支持直连及 HTTP、HTTPS、SOCKS5 代理。
- 使用官方原生 CLI 的合成抓包验证转发，检查正文、请求头和凭证隔离。

2026-10-08 已通过 [PR #2](https://github.com/skingford/sub2api/pull/2) 合入 `release`，合并提交
[`81b7e5ce`](https://github.com/skingford/sub2api/commit/81b7e5cec78e38c3bbca282cbc82822d54c76005)。

目前包含 2.1.286、2.1.291、2.1.292 的回归样本。2.1.291 与 2.1.292 在相同测试场景下，
排除版本归因和随机标识后，没有发现新的业务字段或 beta 差异。

2026-10-08 又补充 12 条原生请求，覆盖工具回填、多轮、重试、假 OAuth、Unicode 和 token 计数。
旧身份策略的 180 个配置组合继续保留回归；新的原生保真路径会绕过这些身份改写。
四条传输路径已通过正文、头部值和顺序、ClientHello 非随机部分的对照。
已按 2.1.292 原生运行时恢复 `cch` 算法，并在最终正文构建后计算；
[算法与 Docker 对照记录](docs/claude-code-cch-2.1.292.md)。
后续完整性复核见 [剩余差异报告](docs/claude-code-remaining-differences.md)，
修复后的会话、版本和重试约定见 [调用说明](docs/claude-code-gap-fixes.md)。同一 session UUID 在数据库中固定绑定上游账号，续聊、计数和重试保持相同出站 UUID；账号不可用时返回错误，不自动换号。真实服务端验收仍未验证，
非原生兼容转换未宣称完整复刻 CLI。
详细结论及单账号开关见 [请求报告](docs/claude-code-request-parity.md)，复现方法见
[隔离实验说明](.github/claude-validation/README.md)。

验证针对假 Key、本地模拟服务和指定运行模式。它不代表真实订阅 OAuth、官方计费或
服务端来源判断已经得到验证，也不构成“不会封号”的承诺。

## 从维护分支构建

要使用本 fork 的改动，从 `release` 分支构建本地镜像：

```bash
git clone --branch release https://github.com/skingford/sub2api.git
cd sub2api
docker build --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  -t sub2api-skingford:local .
```

准备部署目录中的配置：

```bash
cd deploy
cp -n .env.example .env
mkdir -p data postgres_data redis_data
```

编辑 `.env`，填写 `POSTGRES_PASSWORD`、`REDIS_PASSWORD`、`ADMIN_EMAIL`、`ADMIN_PASSWORD`，
并按配置说明设置持久化密钥。本地访问可设置
`BIND_HOST=127.0.0.1`。在当前目录创建 `docker-compose.override.yml`，指定刚构建的镜像：

```yaml
services:
  sub2api:
    image: sub2api-skingford:local
```

在 `deploy` 目录使用 Docker Compose v2 启动：

```bash
docker compose -f docker-compose.local.yml -f docker-compose.override.yml up -d
docker compose -f docker-compose.local.yml -f docker-compose.override.yml logs -f sub2api
```

数据、初始化、备份和其他部署参数见 [部署说明](deploy/README.md)。
该 Compose 基础文件原本使用上游镜像，因此部署本 fork 时需要上述镜像覆盖。

## 开发与验证

后端使用 Go **1.27.0**；前端使用仓库锁定的 pnpm 依赖。集成测试需要 Docker。

```bash
cd backend
GOTOOLCHAIN=go1.27.0 go test -tags=unit ./...
GOTOOLCHAIN=go1.27.0 go test -tags=integration ./...
golangci-lint run --timeout=30m ./...
```

Claude 请求回归可单独运行：

```bash
GOTOOLCHAIN=go1.27.0 go test -tags=unit ./internal/service \
  -run 'TestClaudeCode2286|TestClaudeCode229[12]|TestAnthropicClientRequestID' -count=1
```

2026-10-08 本地验证结果：

- 完整后端 unit：57 个包通过。
- 完整后端 integration：51 个包通过。
- golangci-lint 2.13.0：0 issues。
- 2.1.291 / 2.1.292 的 8 个转发组合，原始正文逐字节一致。
- 新增 24 个报文转发组合、180 个身份配置组合通过；12 条原生请求的正文与 PCAP 解密字节一致。

运行的是本地与模拟测试，没有进行生产部署或真实官方模型调用。

## 文档与来源

- [维护开发指南](DEV_GUIDE.md)
- [Claude 改动记录](docs/claude-change-log.md)
- [Claude 请求对齐与验证范围](docs/claude-code-request-parity.md)
- [完整请求日志与 Claude 故障分析](docs/request-tracing.md)：默认保存 HTTP 全链路正文、账号与上游错误证据，支持按请求导出与完整性校验。
- [插件开发](docs/PLUGIN_DEVELOPMENT.md)
- [上游中文项目说明](https://github.com/Wei-Shaw/sub2api/blob/main/README_CN.md)
- [上游项目](https://github.com/Wei-Shaw/sub2api)

历史 [PR #1](https://github.com/skingford/sub2api/pull/1) 保留为相对上游基线的草稿差异记录。
当前维护版本以 `release` 为准。

## 许可证与署名

沿用上游的 [GNU Lesser General Public License v3.0 或更新版本](LICENSE)。

上游版权声明：Copyright (c) 2026 Wesley Liddick。
感谢 Wei-Shaw/sub2api 及其贡献者；本 fork 由 skingford 维护。
