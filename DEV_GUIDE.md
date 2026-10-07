# sub2api 项目开发指南

> 本文档记录项目环境配置、常见坑点和注意事项，供 Claude Code 和团队成员参考。

## 一、项目基本信息

| 项目 | 说明 |
|------|------|
| **上游仓库** | Wei-Shaw/sub2api |
| **Fork 仓库** | skingford/sub2api |
| **技术栈** | Go 后端 (Ent ORM + Gin) + Vue3 前端 (pnpm) |
| **数据库** | PostgreSQL 16 + Redis |
| **包管理** | 后端: go modules, 前端: **pnpm**（不是 npm） |

## 二、本地环境配置

### PostgreSQL 16 (Windows 服务)

| 配置项 | 值 |
|--------|-----|
| 端口 | 5432 |
| psql 路径 | `C:\Program Files\PostgreSQL\16\bin\psql.exe` |
| pg_hba.conf | `C:\Program Files\PostgreSQL\16\data\pg_hba.conf` |
| 数据库凭据 | user=`sub2api`, password=`sub2api`, dbname=`sub2api` |
| 超级用户 | user=`postgres`, password=`postgres` |

### Redis

| 配置项 | 值 |
|--------|-----|
| 端口 | 6379 |
| 密码 | 无 |

### 开发工具

```bash
# golangci-lint（CI 用 v2.13，本地建议装同一版以免版本差异带来的噪音）
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13

# pnpm (前端包管理)
npm install -g pnpm
```

## 三、CI/CD 流水线

### GitHub Actions Workflows

| Workflow | 触发条件 | 检查内容 |
|----------|----------|----------|
| **backend-ci.yml** | push, pull_request | 单元测试 + 集成测试 + golangci-lint v2.13 |
| **security-scan.yml** | push, pull_request, 每周一 | govulncheck + gosec + pnpm audit |
| **release.yml** | tag `v*` | 构建发布（PR 不触发） |

### CI 要求

- Go 版本必须是 **1.27.0**：三个 workflow 都用 `go-version-file: backend/go.mod` 取版本，随后硬断言 `go version | grep -q 'go1.27.0'`。升级 Go 时要同时改 `backend/go.mod`、`backend-ci.yml`（两处）、`release.yml`、`security-scan.yml` 里的这句断言，**以及三个 Dockerfile 里的 Go 构建镜像**（`Dockerfile` / `deploy/Dockerfile` 的 `ARG GOLANG_IMAGE`、`backend/Dockerfile` 的 `FROM golang:`）。前者漏了 CI 会在版本校验步骤直接失败；**后者漏了 CI 不会报，而是等到有人用这些 Dockerfile 构建时才失败**（`go.mod requires go >= X (running Y; GOTOOLCHAIN=local)`）。
- 前端使用 `pnpm install --frozen-lockfile`，必须提交 `pnpm-lock.yaml`

### 本地测试命令

```bash
# 后端单元测试
cd backend && go test -tags=unit ./...

# 后端集成测试
cd backend && go test -tags=integration ./...

# 代码质量检查
cd backend && golangci-lint run ./...

# 前端依赖安装（必须用 pnpm）
cd frontend && pnpm install
```

## 四、常见坑点 & 解决方案

### 坑 1：pnpm-lock.yaml 必须同步提交

**问题**：`package.json` 新增依赖后，CI 的 `pnpm install --frozen-lockfile` 失败。

**原因**：上游 CI 使用 pnpm，lock 文件不同步会报错。

**解决**：
```bash
cd frontend
pnpm install  # 更新 pnpm-lock.yaml
git add pnpm-lock.yaml
git commit -m "chore: update pnpm-lock.yaml"
```

---

### 坑 2：npm 和 pnpm 的 node_modules 冲突

**问题**：之前用 npm 装过 `node_modules`，pnpm install 报 `EPERM` 错误。

**解决**：
```bash
cd frontend
rm -rf node_modules  # 或 PowerShell: Remove-Item -Recurse -Force node_modules
pnpm install
```

---

### 坑 3：PowerShell 中 bcrypt hash 的 `$` 被转义

**问题**：bcrypt hash 格式如 `$2a$10$xxx...`，PowerShell 把 `$2a` 当变量解析，导致数据丢失。

**解决**：将 SQL 写入文件，用 `psql -f` 执行：
```bash
# 错误示范（PowerShell 会吃掉 $）
psql -c "INSERT INTO users ... VALUES ('$2a$10$...')"

# 正确做法
echo "INSERT INTO users ... VALUES ('\$2a\$10\$...')" > temp.sql
psql -U sub2api -h 127.0.0.1 -d sub2api -f temp.sql
```

---

### 坑 4：psql 不支持中文路径

**问题**：`psql -f "D:\中文路径\file.sql"` 报错找不到文件。

**解决**：复制到纯英文路径再执行：
```bash
cp "D:\中文路径\file.sql" "C:\temp.sql"
psql -f "C:\temp.sql"
```

---

### 坑 5：PostgreSQL 密码重置流程

**场景**：忘记 PostgreSQL 密码。

**步骤**：
1. 修改 `C:\Program Files\PostgreSQL\16\data\pg_hba.conf`
   ```
   # 将 scram-sha-256 改为 trust
   host    all    all    127.0.0.1/32    trust
   ```
2. 重启 PostgreSQL 服务
   ```powershell
   Restart-Service postgresql-x64-16
   ```
3. 无密码登录并重置
   ```bash
   psql -U postgres -h 127.0.0.1
   ALTER USER sub2api WITH PASSWORD 'sub2api';
   ALTER USER postgres WITH PASSWORD 'postgres';
   ```
4. 改回 `scram-sha-256` 并重启

---

### 坑 6：Go interface 新增方法后 test stub 必须补全

**问题**：给 interface 新增方法后，编译报错 `does not implement interface (missing method XXX)`。

**原因**：所有测试文件中实现该 interface 的 stub/mock 都必须补上新方法。

**解决**：
```bash
# 搜索所有实现该 interface 的 struct
cd backend
grep -r "type.*Stub.*struct" internal/
grep -r "type.*Mock.*struct" internal/

# 逐一补全新方法
```

---

### 坑 7：Windows 上 psql 连 localhost 的 IPv6 问题

**问题**：psql 连 `localhost` 先尝试 IPv6 (::1)，可能报错后再回退 IPv4。

**建议**：直接用 `127.0.0.1` 代替 `localhost`。

---

### 坑 8：Windows 没有 make 命令

**问题**：CI 里用 `make test-unit`，本地 Windows 没有 make。

**解决**：直接用 Makefile 里的原始命令：
```bash
# 代替 make test-unit
go test -tags=unit ./...

# 代替 make test-integration
go test -tags=integration ./...
```

---

### 坑 9：Ent Schema 修改后必须重新生成

**问题**：修改 `ent/schema/*.go` 后，代码不生效。

**解决**：
```bash
cd backend
go generate ./ent  # 重新生成 ent 代码（json.RawMessage 字段会生成为同类型的 jsontext.Value，属预期）
git add ent/       # 生成的文件也要提交
```

---

### 坑 10：前端测试看似正常，但后端调用失败（模型映射被批量误改）

**典型现象**：
- 前端按钮点测看起来正常；
- 实际通过 API/客户端调用时返回 `Service temporarily unavailable` 或提示无可用账号；
- 常见于 OpenAI 账号（例如 Codex 模型）在批量修改后突然不可用。

**根因**：
- OpenAI 账号编辑页默认不显式展示映射规则，容易让人误以为“没映射也没关系”；
- 但在**批量修改同时选中不同平台账号**（OpenAI + Antigravity/Gemini）时，模型白名单/映射可能被跨平台策略覆盖；
- 结果是 OpenAI 账号的关键模型映射丢失或被改坏，后端选不到可用账号。

**修复方案（按优先级）**：
1. **快速修复（推荐）**：在批量修改中补回正确的透传映射（例如 `gpt-5.3-codex -> gpt-5.3-codex-spark`）。
2. **彻底重建**：删除并重新添加全部相关账号（最稳但成本高）。

**关键经验**：
- 如果某模型已被软件内置默认映射覆盖，通常不需要额外再加透传；
- 但当上游模型更新快于本仓库默认映射时，**手动批量添加透传映射**是最简单、最低风险的临时兜底方案；
- 批量操作前尽量按平台分组，不要混选不同平台账号。

---

### 坑 11：PR 提交前检查清单

提交 PR 前务必本地验证：

- [ ] `go test -tags=unit ./...` 通过
- [ ] `go test -tags=integration ./...` 通过
- [ ] `golangci-lint run ./...` 无新增问题
- [ ] `pnpm-lock.yaml` 已同步（如果改了 package.json）
- [ ] 所有 test stub 补全新接口方法（如果改了 interface）
- [ ] Ent 生成的代码已提交（如果改了 schema）

## 五、常用命令速查

### 数据库操作

```bash
# 连接数据库
psql -U sub2api -h 127.0.0.1 -d sub2api

# 查看所有用户
psql -U postgres -h 127.0.0.1 -c "\du"

# 查看所有数据库
psql -U postgres -h 127.0.0.1 -c "\l"

# 执行 SQL 文件
psql -U sub2api -h 127.0.0.1 -d sub2api -f migration.sql
```

### Git 操作

```bash
# 同步上游
git fetch upstream
git checkout main
git merge upstream/main
git push origin main

# 创建功能分支
git checkout -b feature/xxx

# Rebase 到最新 main
git fetch upstream
git rebase upstream/main
```

### 前端操作

```bash
# 安装依赖（必须用 pnpm）
cd frontend
pnpm install

# 开发服务器
pnpm dev

# 构建
pnpm build
```

### 后端操作

```bash
# 运行服务器
cd backend
go run ./cmd/server/

# 生成 Ent 代码
go generate ./ent

# 运行测试
go test -tags=unit ./...
go test -tags=integration ./...

# Lint 检查
golangci-lint run ./...
```

## 六、项目结构速览

```
sub2api-bmai/
├── backend/
│   ├── cmd/server/          # 主程序入口
│   ├── ent/                 # Ent ORM 生成代码
│   │   └── schema/          # 数据库 Schema 定义
│   ├── internal/
│   │   ├── handler/         # HTTP 处理器
│   │   ├── service/         # 业务逻辑
│   │   ├── repository/      # 数据访问层
│   │   └── server/          # 服务器配置
│   ├── migrations/          # 数据库迁移脚本
│   └── config.yaml          # 配置文件
├── frontend/
│   ├── src/
│   │   ├── api/             # API 调用
│   │   ├── components/      # Vue 组件
│   │   ├── views/           # 页面视图
│   │   ├── types/           # TypeScript 类型
│   │   └── i18n/            # 国际化
│   ├── package.json         # 依赖配置
│   └── pnpm-lock.yaml       # pnpm 锁文件（必须提交）
└── .claude/
    └── CLAUDE.md            # 本文档
```

## 七、参考资源

- [上游仓库](https://github.com/Wei-Shaw/sub2api)
- [Ent 文档](https://entgo.io/docs/getting-started)
- [Vue3 文档](https://vuejs.org/)
- [pnpm 文档](https://pnpm.io/)

## 八、Claude 相关改动必须留痕

本 fork 的 Claude 请求构建、认证转发、beta 能力、模型参数、流式处理、身份字段、
测试样本和对应文档，每次改动都要更新 [Claude 改动记录](docs/claude-change-log.md)。

每条记录使用稳定编号，并写清：

1. 上游基线提交、被测 CLI / SDK 版本，以及适用的认证和运行模式。
2. 修改原因、代码或抓包依据；合成实验、源码推断和真实上游验证分别注明。
3. 涉及文件、行为变化、兼容范围与已知限制。
4. 验证命令和实际结果；未执行的检查标为未验证。
5. 对应提交和 PR。代码提交使用 `Claude-Change-ID` trailer 关联记录；提交后补充哈希。

在同一个 PR 中维护代码、回归样本和记录。保留已发布提交及记录；结论需要纠正时，
新增条目并引用原编号。不要把模拟响应写成官方服务接受、订阅资格或计费验证。
同步上游时记录基线变化；若上游也修改了 Claude 路径，逐项复核并记录合并处理。

## 九、Fork 分支约定

- `main`：定时跟踪 `Wei-Shaw/sub2api` 上游主分支，仅允许快进同步。
- `release`：skingford/sub2api 的默认维护分支，包含已验证的 fork 改动，人工决定何时同步 `main`。
- `codex/*` 或其他主题分支：从 `release` 开始开发，后续常规 PR 以 `release` 为目标。

首次建立 `release` 时保留此前已验证的提交历史。历史 PR #1 保留为相对上游 `main`
的草稿差异记录；维护版本以 `release` 为准，不通过该历史 PR 把 fork 改动写入上游跟踪分支。

### main 定时同步与 release 人工更新

2026-10-07 改为 [GitHub Actions：Sync upstream main](https://github.com/skingford/sub2api/actions/workflows/sync-upstream.yml)，
每天 **09:00（Asia/Shanghai，UTC 01:00）** 在 GitHub 托管 runner 执行，支持手动 **Run workflow**。
原 Codex 任务 `sync-sub2api-main-from-upstream` 已暂停，避免重复执行；不再依赖本地电脑或 Codex。

工作流 [.github/workflows/sync-upstream.yml](.github/workflows/sync-upstream.yml) 放在默认分支 `release`，
仅在本 fork 的 `release` 上运行。同步脚本和本地 Git 回归测试也随 `release` 维护。
`main` 保持上游原始提交，不添加 fork 自定义的工作流或记录提交。

每次执行：

1. 在临时裸仓库中获取 `skingford/sub2api:main` 与 `Wei-Shaw/sub2api:main`，不检出或执行上游代码。
2. 提交相同则结束。仅当 fork 的 `main` 是上游 `main` 的祖先时，普通推送已检查的上游提交到远端 `main`，随后复查远端提交。
3. 分叉、fork 独有提交、推送被拒绝或验证失败时停止并报告；不强推、不重置、不自动制造合并提交。
4. 在 Actions 日志及运行摘要记录前后提交、提交数和变动文件，供人工合并时复核 Claude 路径；保留上游原始提交作为追溯依据。
5. 自动任务不修改 `release`，不创建或合并面向它的 PR，也不发布版本或部署。

默认使用工作流自身的 `GITHUB_TOKEN`，仅为同步 job 声明 `contents: write`，无需先配置个人令牌。
如果 GitHub 因上游工作流文件的变更拒绝该令牌推送，可配置仓库 Actions secret `UPSTREAM_SYNC_TOKEN`：
使用仅授权本 fork、具有 Contents 和 Workflows 写权限的细粒度令牌。脚本会停止并保留原始错误，不会绕过权限或改用强推。

GitHub 定时执行可能延迟；公开仓库连续 60 天无活动时，定时工作流可能被自动停用，
需要在 Actions 页面重新启用。见 [GitHub schedule 说明](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#schedule)。
手动验证命令：

```bash
gh workflow run sync-upstream.yml --repo skingford/sub2api --ref release
gh run list --repo skingford/sub2api --workflow sync-upstream.yml --limit 5
```

GitHub 的 “This branch is not behind … No new commits to fetch” 表示当前没有需要拉取的上游提交。
`main` 随上游前进后，README 的已验证基线和 `release` 代码仍以最近一次人工合并为准。

维护者决定更新 `release` 时，在干净工作区从最新 `origin/release` 创建独立分支：

```bash
git fetch --no-tags origin main release
# Replace YYYYMMDD with the actual date, adding a suffix if the branch already exists.
git switch -c codex/sync-upstream-YYYYMMDD origin/release
git merge --no-ff origin/main
```

检查合并差异，处理冲突并完成相关验证后，再通过以 `release` 为目标的 PR 合并。
更新 `docs/claude-change-log.md` 中的上游基线；涉及 Claude 路径时逐项说明与 fork 改动的交互、
验证结果和提交引用。没有新提交时无需创建同步 PR。

### release 分支保护

GitHub 已启用 [release-maintenance-protection](https://github.com/skingford/sub2api/rules/24660893)，
仅匹配 `refs/heads/release`：禁止删除和非快进强推，不设绕过名单。普通快进推送仍允许，
常规开发继续按上述约定通过面向 `release` 的 PR 维护。

规则快照保存在 [.github/rulesets/release-protection.json](.github/rulesets/release-protection.json)。
该文件不会自动修改 GitHub 设置；变更保护策略时，应更新已有规则并核对服务端的生效规则。
2026-10-07 配置时尚无 release 分支的 CI 运行记录，因此未配置必需状态检查。
