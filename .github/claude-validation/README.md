# Claude Code 2.1.292 隔离请求验证

使用固定版本的官方 Linux x64 二进制、假凭证和本地 TLS 模拟服务，验证原生请求格式。
覆盖普通请求、Read 工具调用与结果回填、503 重试、两轮对话、OAuth 环境变量分支、
中文与 emoji，以及 `/context` 产生的三条 `count_tokens` 请求。

运行时使用 Docker `--network none`，把 `api.anthropic.com` 映射到容器回环地址。
脚本启动前检查接口、路由和域名解析；不挂载宿主机登录目录。模拟响应不能证明真实
OAuth 订阅资格、额度、计费或官方服务接受情况。

## 采集与复核

从仓库根目录执行。镜像构建需要下载 Debian 工具包，但不会执行 CLI。
二进制下载和模型请求是不同操作；真正运行 CLI 时必须使用下面的断网参数。

```bash
capture_build_dir=$(mktemp -d)
capture_results_dir=$(mktemp -d)
cp .github/claude-validation/Dockerfile .github/claude-validation/*.py "$capture_build_dir/"
curl -fsSL https://downloads.claude.ai/claude-code-releases/2.1.292/linux-x64/claude \
  -o "$capture_build_dir/claude"
docker build -t sub2api-claude-validation:2.1.292 "$capture_build_dir"

docker run --rm --network none \
  --add-host api.anthropic.com:127.0.0.1 \
  --mount "type=bind,src=$capture_results_dir,dst=/work" \
  sub2api-claude-validation:2.1.292

mkdir -p "$capture_results_dir/analysis"
docker run --rm --network none \
  --mount "type=bind,src=$capture_results_dir,dst=/evidence,readonly" \
  --mount "type=bind,src=$capture_results_dir/analysis,dst=/analysis" \
  --entrypoint python3 sub2api-claude-validation:2.1.292 /opt/analyze.py
```

Dockerfile 校验二进制 SHA-256：
`a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。

输出包含原始请求、CLI 输出、PCAP、临时 TLS 会话密钥和运行摘要。
`analysis/capture-validation.json` 核对接收端正文与解密后的 PCAP 原始字节 SHA-256，
并要求内核丢包数为 0。中文正文必须比较 `http.file_data_raw`，Wireshark 的显示文本会替换非 ASCII 字节。
模拟器的 `server.key` 是本地临时证书私钥，不纳入仓库样本。

只重跑某个场景时，在镜像名之后传入场景名称，并使用新的空输出目录，例如 `read-tool` 或 `count-context`。
`analyze.py` 同样接受场景名称参数。不要覆盖已引用的原始证据。

## Sub2API 真实传输组件对照

辅助程序调用生产代码 `repository.NewHTTPUpstream(nil).DoWithTLS`，测试默认传输、旧内置
TLS profile、新原生配置，以及新配置的 HTTP / HTTPS CONNECT / SOCKS5 代理路径。
它重放原始请求，观察传输组件的握手和头部序列化行为；
身份重写和请求构建由 Go 回归测试另行覆盖。没有启动完整部署或访问真实账号。

```bash
transport_results_dir=$(mktemp -d)
(
  cd backend
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=go1.27.0 \
    go build -o "$capture_build_dir/transport-probe" \
    ./internal/repository/testdata/claude_capture_probe.go
)
python3 - "$capture_results_dir/baseline/requests.json" "$capture_build_dir/input.json" <<'PY'
import json, sys
from pathlib import Path
records = json.loads(Path(sys.argv[1]).read_text())
request = next(r for r in records if r['path'].startswith('/v1/messages'))
Path(sys.argv[2]).write_text(json.dumps(request))
PY
docker run --rm --network none \
  --add-host api.anthropic.com:127.0.0.1 \
  --mount "type=bind,src=$transport_results_dir,dst=/work" \
  --mount "type=bind,src=$capture_build_dir/transport-probe,dst=/opt/transport-probe,readonly" \
  --mount "type=bind,src=$capture_build_dir/input.json,dst=/opt/input.json,readonly" \
  --entrypoint python3 sub2api-claude-validation:2.1.292 /opt/transport_lab.py

mkdir -p "$transport_results_dir/analysis"
docker run --rm --network none \
  --mount "type=bind,src=$transport_results_dir,dst=/evidence,readonly" \
  --mount "type=bind,src=$transport_results_dir/analysis,dst=/analysis" \
  --entrypoint python3 sub2api-claude-validation:2.1.292 /opt/analyze.py \
  default fingerprint native native-http-proxy native-https-proxy native-socks5-proxy
```

本次模拟服务仅提供 HTTP/1.1，因此结果不覆盖 HTTP/2、真实出口 IP、真实代理链路或所有平台。
握手特征有差异不等于已证明服务端检测、拒绝或封禁规则。

新配置在 2026-10-08 的四条路径上通过了原生样本的正文、头部值和顺序对比。
ClientHello 排除随机数、session ID 和临时公钥后，其余 1499 字节握手结构一致；
对应 SHA-256 已写入 `TestClaudeNativeClientHelloGolden`，在本地 TCP 接收端自动验证，
无需外部服务。macOS x64 通过仅允许指定回环端口的进程沙箱与本地 CONNECT 模拟器补充验证，
messages 和三条 count_tokens 的握手也匹配此摘要。

## 自动回归

```bash
cd backend
GOTOOLCHAIN=go1.27.0 go test -tags=unit ./internal/service \
  -run 'TestClaudeCode2292|TestSyncBillingHeaderVersion|TestAnthropicClientRequestID' -count=1
```

样本与来源记录在
[`testdata/claude_code_2_1_292`](../../backend/internal/service/testdata/claude_code_2_1_292)。
修复和实验结论见 [请求对齐报告](../../docs/claude-code-request-parity.md)
及 [Claude 改动记录](../../docs/claude-change-log.md)。

## cch 原生运行时对照

完整依据与边界见 [cch 2.1.292 报告](../../docs/claude-code-cch-2.1.292.md)。
下面在官方 Linux x64 二进制的实验副本里，仅替换 JavaScript 入口并关闭该入口 bytecode。
原生 HTTP 和哈希代码保持原样。产物是运行时探针，不能作为未修改 CLI 的产品行为样本。

从仓库根目录执行，`claude_binary` 指向上文已校验的官方 Linux x64 文件；镜像复用上文构建结果。

```bash
claude_binary=/absolute/path/to/official/linux-x64/claude
cch_probe_dir=$(mktemp -d)/probe
python3 .github/claude-validation/prepare_cch_probe.py "$claude_binary" "$cch_probe_dir"

docker run --rm --network none \
  --add-host api.anthropic.com:127.0.0.1 \
  --mount "type=bind,src=$cch_probe_dir,dst=/work" \
  --mount "type=bind,src=$cch_probe_dir/claude-runtime-probe,dst=/opt/claude-runtime-probe,readonly" \
  --mount "type=bind,src=$PWD/.github/claude-validation/cch_runtime_lab.py,dst=/opt/cch_runtime_lab.py,readonly" \
  --entrypoint python3 sub2api-claude-validation:2.1.292 /opt/cch_runtime_lab.py

docker run --rm --network none \
  --mount "type=bind,src=$cch_probe_dir,dst=/work" \
  --mount "type=bind,src=$PWD/.github/claude-validation/verify_cch_capture.py,dst=/opt/verify_cch_capture.py,readonly" \
  --entrypoint python3 sub2api-claude-validation:2.1.292 /opt/verify_cch_capture.py

(cd backend && GOTOOLCHAIN=go1.27.0 go test -tags=unit ./internal/pkg/claude ./internal/service \
  -run 'TestCCH2292|TestClaudeNative|TestClaudeCode2292' -count=1)
```

核对器要求 81 条接收正文与已提交向量、PCAP 原始字节全部一致且内核丢包为 0。
`runtime-probe-provenance.json` 记录二进制哈希和入口修改位置。目录内私钥只属于本地模拟服务，
不应提交；探针只在断网容器运行。

## 扩展流程审查

`extended_audit.py` 复用镜像内的 `lab.py` / `validation.py`，补充并行 Read、内联 PNG、
合成 thinking + 工具、`/compact`、`--resume`、sonnet 别名、额外 metadata 和 JSON schema 请求。
另有六种 API Key / OAuth、普通 / verbose / stream-json 模式对照。全部使用合成材料。
默认运行扩展场景及模式对照，也可以在脚本后显式列出场景；输出目录必须为空。
后续修复增加 multi-turn、count-context、oauth-count-context，compact 现在执行两次连续压缩。
`CLAUDE_AUDIT_MODEL` 可指定固定模型进行同条件对照；不会传递宿主机凭证或登录配置。

```bash
audit_repo="$PWD"
audit_output=$(mktemp -d)
docker run --rm --network none --add-host api.anthropic.com:127.0.0.1 \
  --mount "type=bind,src=$audit_output,dst=/work" \
  --mount "type=bind,src=$audit_repo/.github/claude-validation/extended_audit.py,dst=/opt/extended_audit.py,readonly" \
  --entrypoint python3 sub2api-claude-validation:2.1.292 /opt/extended_audit.py

mkdir -p "$audit_output/analysis"
docker run --rm --network none \
  --mount "type=bind,src=$audit_output,dst=/evidence,readonly" \
  --mount "type=bind,src=$audit_output/analysis,dst=/analysis" \
  --entrypoint python3 sub2api-claude-validation:2.1.292 /opt/analyze.py \
  parallel-read image-read thinking-tool compact resume alias-sonnet extra-metadata structured-output \
  api-json oauth-json oauth-stream api-arg oauth-arg api-stdin multi-turn count-context oauth-count-context

python3 - "$audit_repo" "$audit_output" <<'PY'
import json, sys
from pathlib import Path
repo, output = map(Path, sys.argv[1:])
(output / 'overlay.json').write_text(json.dumps({'Replace': {
    str(repo / 'backend/internal/service/extended_cli_audit_test.go'):
    str(repo / '.github/claude-validation/extended_audit_test.go')
}}))
PY
(
  cd backend
  CLAUDE_EXTENDED_AUDIT_INPUT="$audit_output" \
  CLAUDE_EXTENDED_AUDIT_OUTPUT="$audit_output/analysis/production-audit.json" \
  GOTOOLCHAIN=go1.27.0 go test -overlay="$audit_output/overlay.json" -tags=unit \
    ./internal/service -run '^TestExtendedNative' -count=1 -v
)
```

overlay 不改生产源码。审查测试采集差异，PASS 不等于没有差异；务必阅读输出 JSON。
托管服务使用测试存储；CLI 到模拟器、原始捕获到生产组件是两阶段验证，不冒充完整部署联调。
`--json-schema` 实验不会由模拟器保证 schema 输出，thinking 签名也仅为合成值。
结果见 [扩展复核报告](../../docs/claude-code-extended-audit.md)。

## 修复后的连续压缩与迁移联调

永久回归读取 `backend/internal/service/testdata/claude_code_2_1_292/alignment/` 的原生样本，
检查 49 条报文 × 4 个转发配置，并验证托管历史、模型配置和能力策略。执行：

```bash
(cd backend && GOTOOLCHAIN=go1.27.0 go test -tags=unit ./internal/service -run '^TestClaudeAlignment' -count=1)
```

实时 CLI 联调使用生产恢复 / Forward 服务和测试存储 / 上游。它覆盖 Sonnet 4.6、Sonnet / Opus 5.5，
两种上游认证，各两条独立对话；每条连续压缩两次后迁移账号，再次压缩并续聊，检查会话隔离和额外 metadata。
PostgreSQL 的原子状态和摘要工作器失效由 `TestClaudeRecoveryCompactionCommitInvalidatesOldSummaryAndRestore` 单独覆盖。

```bash
alignment_repo="$PWD"
alignment_output=$(mktemp -d)
(
  cd backend
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=go1.27.0 \
    go test -tags=unit -c -o "$alignment_output/service-alignment.test" ./internal/service
)
mkdir "$alignment_output/results"
docker run --rm --network none --add-host api.anthropic.com:127.0.0.1 \
  --mount "type=bind,src=$alignment_output/results,dst=/work" \
  --mount "type=bind,src=$alignment_output/service-alignment.test,dst=/opt/service-alignment.test,readonly" \
  --mount "type=bind,src=$alignment_repo/.github/claude-validation/run_alignment_lab.py,dst=/opt/run_alignment_lab.py,readonly" \
  --entrypoint python3 sub2api-claude-validation:2.1.292 /opt/run_alignment_lab.py
```

运行器验证断网和固定 CLI 哈希。实时服务实验使用自定义回环地址，不将它当作 TLS / PCAP 抓包；
固定官方 origin 的 TLS 捕获继续使用上面的 `extended_audit.py`。修复说明见
[扩展对齐修复](../../docs/claude-alignment-fixes.md)。

## 请求参数与算法复核

`request_recheck_test.go` 遍历输入目录下所有 `*/requests.json`，通过生产客户端校验器建立
与 handler 相同的 context，然后调用 Forward / ForwardCountTokens。计数请求通常没有
metadata 和 billing；不能省略这一步后把误分类导致的头变化当作生产差异。

将它通过 Go overlay 映射为 `backend/internal/service/native_request_recheck_test.go`，
设定 `CLAUDE_EXTENDED_AUDIT_INPUT` 为隔离抓包根目录，
`CLAUDE_REQUEST_RECHECK_OUTPUT` 为新 JSON 输出路径，执行：

```bash
GOTOOLCHAIN=go1.27.0 go test -overlay=/absolute/path/to/overlay.json -tags=unit \
  ./internal/service -run '^TestNativeRequestRecheck$' -count=1 -json
```

输出记录完整正文哈希、方法、路径与 query、Content-Length、GetBody、cch 复算、
认证类型和双向应用头差异。认证值不记录；Host / Connection 等传输负责的字段需单独验证。
`anthropic_passthrough` 对 OAuth 不是另一套发送实现；四组合计数包含这项配置重复。
PASS 表示采集成功，是否一致应读取每条结果，不能只看退出码。

`verify_native_hello.py` 在已有断网镜像中运行，参数依次为捕获根目录、输出 JSON 和场景名。
它仅提取确实承载模型 / 计数请求的连接，清零随机数、session ID 和临时公钥，
将剩余完整 ClientHello 与 Go 的 `TestClaudeNativeClientHelloGolden` 摘要比较。
不把 JA3 相同当作完整握手相同，也不包含 TLS 恢复、HTTP/2 或所有平台。

源码可能同时被其他工作修改时，先保存完整源码快照和 SHA-256 清单，再在快照内执行。
本轮结果及范围见 [2026-10-08 再次复核](../../docs/claude-request-recheck-20261008.md)。

## 源码驱动的全面复核

`comprehensive_lab.py` 使用未修改的固定 CLI，复用 `extended_audit.py` 的隔离和模拟器，
增加五模型、显式 thinking / effort / token 上限、缓存、gzip 和 HTTP 错误分支。
运行方式与扩展审查相同，额外挂载两个 Python 文件到 `/opt`，入口改为
`/opt/comprehensive_lab.py`；必须使用新的空 `/work`、`--network none` 和回环域名映射。
不传场景名时运行全部 51 组，也可以传选中的名称。预设拒绝场景允许 CLI 退出非零，
需读取每组 `case.json`、`summary.json`，不能将它们统称成功生成。

`verify_comprehensive.py CAPTURE_ROOT OUTPUT_JSON` 验证原始发送字节及解码后的 JSON，
支持 gzip 的 PCAP 编码实体字段。它与 `verify_native_hello.py` 均在同一断网镜像运行。

通过 overlay 将以下文件映射到 `backend/internal/service/`，可编译 Linux 测试二进制后
在断网镜像运行，输入目录下每个场景都有 `requests.json` 和 `case.json`：

- `request_recheck_test.go`：实际入口解压、原生识别、完整转发；
- `comprehensive_audit_test.go`：同模型及显式控制的普通 API 参数对照。

分别设置 `CLAUDE_EXTENDED_AUDIT_INPUT`、`CLAUDE_REQUEST_RECHECK_OUTPUT`、
`CLAUDE_GENERIC_AUDIT_OUTPUT`，运行 `TestNativeRequestRecheck|TestComprehensiveGenericParameters`。
采集结果有差异时测试也可能 PASS；逐字段报告才是等价判断依据。

`cch_recheck_test.go` 通过 overlay 映射到 `backend/internal/pkg/claude/`；设置
`CLAUDE_CCH_CASES`、`CLAUDE_CCH_RECORDS` 为运行时探针的输入及实际接收记录，运行
`TestComprehensiveCCHOracle`。新增边界样本必须先由独立原生运行时生成结果，不能由 Go
待测函数生成预期。原函数、源码偏移、独立 PCAP 和来源哈希应一起保存。

本轮结果及实验修正见 [2026-10-09 全面复核](../../docs/claude-comprehensive-audit-20261009.md)。

## 参数与原生 gzip 对齐验证

`parameter_alignment_lab.py` 复用 comprehensive / extended 采集器，运行五模型 × 四场景，
区分直接关闭 thinking、后置 EXTRA_BODY 覆盖、两者组合及显式 temperature。
沿用相同断网镜像、空输出目录和回环域名映射，将三个采集器挂载到 `/opt` 后运行入口。
未修改 CLI 的样本保存在 `testdata/claude_code_2_1_292/parameter_alignment/`，其中
5.5 非法参数样本不意味着生产网关必须放行；相应永久测试保留本地拒绝。

`request_recheck_test.go` 现在同时输出 `body_equal`（解码后的 JSON 字节）和
`wire_body_equal`（实际发送字节）。gzip 的 cch=00000 不属于未压缩哈希函数的运行条件，
不能因强行复算不同就认定原生 gzip 不正确。

设置 `CLAUDE_WIRE_ALIGNMENT_EXPORT` 后运行 `TestClaudeNativeGzipWireParity`，可从生产
构建器导出请求。`claude_capture_probe.go` 的 `wire_body_base64` 字段支持按原压缩字节发送。
用既有 transport_lab 的四个 auto 模式运行，再调用：

```bash
python3 /opt/verify_gzip_wire.py /evidence/transport-final /baseline.json /evidence/analysis/gzip-wire-verification.json
```

核对器需要同目录的 `verify_native_hello.py`。baseline 必须使用未修改 CLI 的独立原始捕获，
不能拿 Go 自己生成的 gzip 当预期。结果和语义边界见 [参数与 gzip 修复说明](../../docs/claude-parameter-wire-alignment.md)。
