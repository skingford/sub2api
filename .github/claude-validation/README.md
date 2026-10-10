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
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=go1.27.2 \
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
GOTOOLCHAIN=go1.27.2 go test -tags=unit ./internal/service \
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

(cd backend && GOTOOLCHAIN=go1.27.2 go test -tags=unit ./internal/pkg/claude ./internal/service \
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
  GOTOOLCHAIN=go1.27.2 go test -overlay="$audit_output/overlay.json" -tags=unit \
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
(cd backend && GOTOOLCHAIN=go1.27.2 go test -tags=unit ./internal/service -run '^TestClaudeAlignment' -count=1)
```

实时 CLI 联调使用生产恢复 / Forward 服务和测试存储 / 上游。它覆盖 Sonnet 4.6、Sonnet / Opus 5.5，
两种上游认证，各两条独立对话；每条连续压缩两次后迁移账号，再次压缩并续聊，检查会话隔离和额外 metadata。
PostgreSQL 的原子状态和摘要工作器失效由 `TestClaudeRecoveryCompactionCommitInvalidatesOldSummaryAndRestore` 单独覆盖。

```bash
alignment_repo="$PWD"
alignment_output=$(mktemp -d)
(
  cd backend
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=go1.27.2 \
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
GOTOOLCHAIN=go1.27.2 go test -overlay=/absolute/path/to/overlay.json -tags=unit \
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

## 对齐后的日志与压缩边界复核

`post_alignment_lab.py` 复用 comprehensive / extended 采集器，添加大系统提示、分块 gzip、
压缩级别、Unicode、大 count_tokens 及 top_p / top_k 场景。大提示写入隔离目录中的文件，
使用 CLI 的 system-prompt-file 参数，避免把超长参数塞入进程命令行。
仍需固定 CLI 哈希、空输出目录、Docker `--network none` 及官方域名回环映射。

将 `post_alignment_audit_test.go` 通过 overlay 注入 service 测试包，设
`CLAUDE_ENCODING_POLICY_AUDIT` 输出路径，以及可选 `CLAUDE_ENCODING_POLICY_EXPORT` 导出目录，
运行 `TestPostAlignmentEncodingPolicyAudit`。它记录完整大小写键变体；getter 的返回值不等于
最终网络头，测试端口的 200 也不代表最终编码合法，需继续跑实际传输。

`trace_transport_probe.go` 通过 overlay 替换编译路径
`backend/internal/repository/testdata/claude_capture_probe.go`，仅在测试主函数初始化生产 recorder，
不替换生产 HTTP 实现。`post_trace_transport_lab.py` 使用 `proxy_audit_lab.py` 的单线程 TLS relay，
对普通、运行时 gzip、分块 gzip、Unicode gzip 做四路径 × 日志开关的 32 组传输。
用 `verify_post_trace.py RESULTS INPUTS OUTPUT_JSON` 核对 PCAP 和日志；头序差异保留在输出，
不能仅根据退出码认定完全一致。

`encoding_policy_lab.py` 读取生产构建器导出的六类配置请求，用严格本地 gzip / JSON 解码器观察
头体一致性，重复 identity 覆盖以检查大小写键冲突。用 `verify_encoding_policy.py RESULTS`
核对全部发送字节。后者需要同目录的 `verify_post_trace.py` 和 `verify_native_hello.py`。

结果及失败实验边界见 [对齐后综合复核](../../docs/claude-post-alignment-audit-20261009.md)。

## gzip 完整修复复验

`gzip_fix_lab.py` 复用 post_alignment / comprehensive / extended 采集器，运行 12 个新场景；
与三个依赖一同挂载到 `/opt`。mode 1 / 2 分别覆盖压缩级别 1、6、9 的两轮输入，轮间等待两秒，
让 mode 1 的异步分块缓存完成构建。所有捕获仍需 `verify_comprehensive.py` 核对 PCAP。

`TestClaudeNativeGzipVariants` 是永久回归，读取 `gzip_alignment/*.json` 的独立捕获，覆盖
API Key / OAuth 及 passthrough 配置，检查原字节、长度、重试正文和请求上下文中的头序选择。
设置 `CLAUDE_GZIP_FIX_EXPORT` 可导出实际生产构建器请求。原生完整 JSON 由捕获 gzip 解出，
另核对 decoded_sha256；application_encoding 的预期来自原始头序，不由 Go 算法生成。

`post_trace_transport_lab.py` 现在遍历 `/inputs/*.json`，每个输入跑四条路径 × 日志开关。
传入生产导出；另以独立原生捕获的头 / 正文作为核对基线。修复验收必须用严格头序模式：

```bash
python3 /opt/verify_post_trace.py RESULTS NATIVE_BASELINES OUTPUT_JSON --strict-order
```

编码策略 overlay 会断言旧 Content-Encoding 配置被过滤；实际网络仍用 encoding_policy_lab
与 verify_encoding_policy 复验，不能以 getter 或 mock 返回码替代。修复记录见
[gzip 完整修复](../../docs/claude-gzip-complete-fix-20261009.md)。

## release 合入后的深入审查

先对当前提交执行 `git archive` 保存不可变快照，并记录 go.mod / go.sum 与源码哈希。
`deep_native_lab.py` 和 comprehensive / extended / post_alignment 一同挂载到 `/opt`，在新的空
`/work` 与 network-none 中运行；另跑 comprehensive 51 场景和 gzip_fix 12 场景。新增 22 场景覆盖
字符 / 字节门槛、Unicode、分块降级 / 拒绝、压缩级别及自定义头。

将 `deep_policy_audit_test.go`、`request_recheck_test.go`、`comprehensive_audit_test.go` 用 overlay
映射到快照的 service 包，固定 `GOTOOLCHAIN=go1.27.2` 编译 Linux 测试二进制。
`CLAUDE_DEEP_POLICY_OUTPUT` 指向空结果目录，执行 `TestDeepClaudePolicyBoundaries`，得到
108 个策略观察和生产请求导出。PASS 只表示采集完成；需读取 CCH、编码、错误及发送次数。

`deep_policy_wire_lab.py` 将第一方明文导出通过真实 transport 发送，与 `/oracle/records.json`
中独立原生运行时的重算结果比较；它固定返回本地 200，不模拟官方拒绝。
对压缩来源正文，独立 oracle 的输入先仅将原 cch 值恢复为 00000，其他原字节保持不变。
用 `verify_deep_runtime.py ROOT` 核对算法 oracle 的 PCAP；新随机向量不要求预存黄金值，
已有 `expected_body_sha256` 则仍必须校验，二者分别计数。

大明文连续发送时，使用 Docker `--tmpfs /work:rw,size=256m`、将空宿主输出目录挂到 `/output`，
可选把输入案例挂到 `/seed/cases.json`。以 `python3 /opt/ram_capture.py /opt/cch_runtime_lab.py`
或 `/opt/deep_policy_wire_lab.py` 启动，结束后导出全部材料；原始丢包采集保留，不能算作字节复验成功。
实际传输仍用 `verify_post_trace.py --strict-order` 和 `verify_encoding_policy.py` 独立核对。

结果见 [深入审查](../../docs/claude-deep-audit-20261009.md)。

`TestDeepClaudeErrorOriginSignals` 使用生产错误转换器，将三种来源标记（cf-ray / request-id / 无标记）的
原始与转换后响应导出到 `CLAUDE_DEEP_ERROR_OUTPUT`。把导出目录挂到 `/error-inputs`，在新的 tmpfs
实验目录运行 `deep_error_replay_lab.py`，观察未修改 CLI 收到持续 403 时的真实请求次数与编码变化。
普通 SDK 重试固定为 0；`cf-ray-restored` 仅在实验响应里补回 cf-ray，作为单变量因果对照，不是生产修复。
全量运行 7 场景，也可传入场景名；仍需用 verify_comprehensive 核对 PCAP 和零丢包。

错误体恢复分支可设置 `CLAUDE_DEEP_ERROR_KIND=bad-json-object|bad-json-text|bad-json-standard`
分别导出三组 400。回放时设置 `CLAUDE_LAB_PLAIN_SUCCESS=1`，并选择 request-id-upstream /
request-id-downstream：gzip 始终返回导出的错误，明文由既有 SSE 模拟器返回成功，比较实际 CLI 是否恢复。
标准 Anthropic 格式是对照，所有结果仍需 PCAP 核验；不代表真实官方接受。
`deep_response_header_audit_test.go` 可通过 overlay 放入 util/responseheaders 包，设
`CLAUDE_HEADER_POLICY_OUTPUT`，观察默认和显式 additional_allowed 下的成功响应头过滤。

## 三处协议差异修复复验

修复后的 `protocol_fix_wire_lab.py` 使用相同的 16 个第一方策略导出：gzip 请求逐字节对照
`/fixtures` 中原生 runtime / blocks 捕获，明文请求逐字节对照 `/oracle/records.json` 中独立
原生运行时的 CCH 结果。挂载生产请求到 `/inputs`、新版传输探针到 `/opt/trace-transport-probe`，
通过 `ram_capture.py` 在断网 tmpfs 中运行，再用 `verify_encoding_policy.py` 验证 PCAP。
保留旧 `deep_policy_wire_lab.py`，用于复现 CC-20261009-008 的历史明文缺陷。

错误回放同时选择 cf-ray 和 request-id 的 upstream / downstream 场景。持续 403 的有标记场景
应各发一次，无标记对照各发三次；三种 bad-json 400 及 `bad-json-escaped` 转义扩展用例，
在启用明文成功模拟响应时均应各发三次并
正常结束。SDK 重试保持关闭，分别比较请求编码序列与 CLI 退出状态，不能只检查 Go 测试 PASS。

真实中转接口测试必须单独运行。`protocol_live_export_test.go` 通过 overlay 加入 service 包，
设置 `CLAUDE_LIVE_REQUEST_EXPORT`，导出生产 Forward 的 normal / passthrough 合成短请求。
将 `live_transport_probe.go` overlay 到 repository/testdata/claude_capture_probe.go 编译 Linux
探针，使用生产 HTTPUpstream 发送；这属于两阶段组件联调，不是完整部署或官方源站验收。

客户端仅接入 Docker internal 网络；另一个 CONNECT 代理同时接入该网络和出口网络，使用
`restricted_egress_proxy.py`，其 `ALLOWED_HOST` 必须等于用户授权的主机。探针设置相同的
`SUB2API_LIVE_AUTHORIZED_HOST`，只挂载只读 `/run/secrets/relay-key` 与合成输入，不挂载用户
登录目录。代理别名为 `egress`，监听 8080，不发布宿主端口、不解密 TLS 或记录认证头。
探针只请求 `/v1/messages`，没有自动重试，输入限制为 64 输出 token；凭据不写入参数或报告。
测试结束后清理专用代理、网络及临时凭据文件。真实返回结果与上述模拟 / 抓包结果分开记录。

## 原生 CLI 配置中转地址并跳过登录引导

已在远端 Claude Code 2.1.295 验证。使用该 SSH 用户的全局配置，不需要浏览器登录。
这里的“跳过登录”是使用中转 API 凭据并完成首次引导标记。

远端已安装脚本时，直接运行，按提示输入 API Key（输入不会显示）：

```bash
ssh -t sub2api 'python3 ~/.local/bin/configure-claude-router.py'
```

换地址时添加 `--base-url https://你的中转地址`。配置其他主机时，先从本仓库安装脚本：

```bash
ssh sub2api 'mkdir -p ~/.local/bin'
scp .github/claude-validation/configure_claude_router.py sub2api:.local/bin/configure-claude-router.py
ssh sub2api 'chmod 700 ~/.local/bin/configure-claude-router.py'
ssh -t sub2api 'python3 ~/.local/bin/configure-claude-router.py'
```

脚本保留其他配置，先备份到 `~/.claude/config-backups/`，再原子更新两个文件，权限均为 `600`。
自动化时可用 `--key-file /安全位置/key`，不要把密钥直接放在命令参数或仓库文件中。

`~/.claude/settings.json` 中的关键配置如下（示例值必须替换）：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://anyrouter.top",
    "ANTHROPIC_AUTH_TOKEN": "替换为你的API_KEY"
  }
}
```

这里使用 `ANTHROPIC_AUTH_TOKEN` 让 CLI 发送 Bearer 认证，已用该中转实际验证；避免在同一配置中
同时设置 `ANTHROPIC_API_KEY`。`~/.claude.json` 保留原有内容并添加：

```json
{
  "hasCompletedOnboarding": true
}
```

验证认证，再发一个短请求；示例模型已在该接口验证，不会修改默认模型：

```bash
ssh sub2api 'claude auth status'
ssh sub2api 'claude -p "Reply with exactly OK." --model claude-haiku-4-5-20251001 --tools "" --no-session-persistence'
```

认证状态应显示 `loggedIn: true`，短请求返回 `OK`。修改配置后重新启动已有的 Claude 会话。

## 两个固定版本的源码与运行时复核

`comprehensive_lab.py` 及复用它的 deep / gzip / error 采集器，默认仍校验 2.1.292。
显式设置 `CLAUDE_LAB_CLI_VERSION=2.1.295`，并只读挂载已核验的官方 2.1.295 Linux x64
二进制到 `/opt/claude`，才允许测试新版；不接受任意版本或自动跳过哈希校验。
2.1.295 SHA-256 为 `4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358`。
Dockerfile 仍固定构建 2.1.292，挂载只影响实验容器。

`version_edges_lab.py` 增加十个别名和 Haiku 5.5 参数场景。示例：

```bash
cli_binary=/absolute/path/to/verified/2.1.295/claude
version_results=$(mktemp -d)
docker run --rm --network none --add-host api.anthropic.com:127.0.0.1 \
  --tmpfs /work:rw,size=256m \
  --mount "type=bind,src=$version_results,dst=/output" \
  --mount "type=bind,src=$cli_binary,dst=/opt/claude,readonly" \
  --mount "type=bind,src=$PWD/.github/claude-validation,dst=/audit,readonly" \
  -e PYTHONPATH=/audit:/opt -e CLAUDE_LAB_CLI_VERSION=2.1.295 \
  --entrypoint python3 claude-validation:2.1.292-20261008 \
  /audit/ram_capture.py /audit/version_edges_lab.py
docker run --rm --network none \
  --mount "type=bind,src=$version_results,dst=/evidence" \
  --mount "type=bind,src=$PWD/.github/claude-validation,dst=/audit,readonly" \
  --entrypoint python3 claude-validation:2.1.292-20261008 \
  /audit/verify_comprehensive.py /evidence /evidence/pcap-verification.json
```

`extract_cli_sources.py BINARY NEW_OUTPUT_DIRECTORY` 只读提取两版的嵌入 JS 并记录来源偏移 / 哈希。
2.1.295 的三个 Zstd 模块要求 Python 3.14 的 `compression.zstd` 或已安装的 `zstandard`。
它不执行源码、不恢复原始 TypeScript、不覆盖已有目录。不要提交完整提取代码或二进制。

新增审查结果见 [源码与运行时报告](../../docs/claude-source-runtime-audit-20261009.md)。
2.1.295 原生正文保留不表示模型转换、托管压缩和 TLS 全部兼容。报告中的压缩实验 overlay
仅用于原因对照，未修改生产代码。错误回放的非零 CLI 退出、未知版本改写保护及默认头过滤
均须逐项解释，不能只以测试进程 PASS / FAIL 推断一致性。

## 2.1.295 修复回归

[CC-20261009-012](../../docs/claude-295-compatibility-fix-20261009.md)修复新版托管压缩、
Haiku 5.5 转换及原生传输配置。2.1.295 传输资格限定 Linux x64、SDK 0.128.0 和 runtime v26.3.0；
不因版本号较新而自动接受其他组合。默认转换版本仍为 2.1.292，Haiku 5.5 使用已验证的 2.1.295。

`haiku295_lab.py` 必须显式设置 `CLAUDE_LAB_CLI_VERSION=2.1.295`，运行 11 个模型 / 认证 /
参数 / 压缩 / 计数场景，沿用上一节的断网容器、固定二进制、tmpfs 和 PCAP 校验。
生产转发回放仍使用 `request_recheck_test.go` / `comprehensive_audit_test.go`；普通调用方要求
`thinking.display=updates` 时需显式传入该字段，不能把原生 verbose 行为当作所有请求的默认值。

`TestClaudeAlignmentNativeCLILab` 支持通过 `CLAUDE_RECOVERY_LAB_MODELS` 传入逗号分隔的已验证
模型列表；2.1.295 可额外加入 `claude-haiku-5-5`，默认保留旧三模型实验。连续三次压缩、账号迁移、
两种认证及两条隔离会话均由该入口执行。未知模型会失败，不能用跳过断言替代支持。

`prepare_cch_probe.py` 现支持两个固定 SHA-256。2.1.295 的原生运行时 oracle 与 2.1.292 算法
相同，但入口替换后的探针仍不是未修改 CLI。新增 12 个实际新版正文及改写向量保存于
`backend/internal/pkg/claude/testdata/cch-2.1.295.json`，不以 Go 的输出生成预期。
传输探针 auto 模式读取生产选择的 2.1.292 / 2.1.295 profile，最后必须用
`verify_post_trace.py --strict-order` 同时验证正文、完整头值 / 头序、握手、日志及零丢包。

## 修复提交后的深入审查

CC-20261009-013 固定已推送的 `afb0c04a3`，结果见
[逐项报告](../../docs/claude-postfix-audit-20261009.md)。生产代码保持不变；新发现按后续记录修复。

- `postfix_edges_lab.py`：18 个三模型 schema、stop、缓存及 thinking / effort / token 组合；
- `token_limit_lab.py`：三个原生模型实际发送 64 输出 token 上限；
- `summary_edges_lab.py`：仅调整模拟压缩回复中摘要首尾的空格、BOM、NEL，CLI 二进制不变。

三个采集器均复用 comprehensive 的版本哈希检查，沿用 network-none / tmpfs 方式并用
`verify_comprehensive.py` 校验 PCAP。`--json-schema` 在本次环境走 StructuredOutput 工具，
不能假定它总是 output_config.format；晚期 EXTRA_BODY 的字段还可能缺少对应 beta。

将两个新 Go 文件通过 overlay 放入冻结快照的 `backend/internal/service/`：

```bash
# 六模型 × 三入口的默认值及显式约束观察，共 102 例（Responses 没有 stop 合同）。
CLAUDE_POSTFIX_CONTROLS_OUTPUT=/output/entrypoints.json \
  /opt/service.test -test.run='^TestPostfixEntrypointControls$' -test.v

# 输入目录是上述 summary_edges 的完整原生捕获；逐次调用生产恢复管理。
CLAUDE_POSTFIX_SUMMARY_INPUT=/captures \
CLAUDE_POSTFIX_SUMMARY_OUTPUT=/output/summary-sequences.json \
  /opt/service.test -test.run='^TestPostfixSummarySequence$' -test.v
```

这两个入口为观察工具，PASS 不表示协议一致。读取最终请求的 schema / stop / max_tokens，
以及恢复序列中的 error；不得将模拟 200 写成官方接受或忽略有证据的字段差异。

## 显式约束的强制验收

[固定合同](../../docs/claude-validation-contract.md)将 schema / stop / token 上限和 Unicode
摘要从观察改为失败即阻止验收的断言；修复记录为
[CC-20261010-001](../../docs/claude-constraint-contract-20261010.md)。

运行 `TestClaudeExplicitConstraintMatrix` 可设置 `CLAUDE_CONSTRAINT_EXPORT=/output/exports`，
导出 192 个通过实际生产构建器生成的请求。将该目录挂到 `/inputs`，通过 network-none /
tmpfs 运行 `constraint_wire_lab.py`，然后运行 `verify_encoding_policy.py` 核对 PCAP。
该实验在接收端验证 schema、stop 和 max_tokens，并保留真实序列化与两种认证方式。
模拟成功仅代表本地约束核验，不代表官方生成验收。

将修复后的入口观察输出保存为 `entrypoint-controls.json`，两版摘要回放分别保存为
`summary-sequences-292.json`、`summary-sequences-295.json`，执行：

```bash
python3 .github/claude-validation/validate_constraint_contract.py /absolute/path/to/results
```

该命令要求完整的 102 个入口观察及 36 个摘要请求，场景缺失、schema / stop 丢失、显式上限
变化或续聊拒绝均退出非零。已用修复前证据验证失败、用修复后结果验证通过。
永久 Go 回归还覆盖 192 个六模型 / 两账号 / 三入口组合、62 个独立 JS Unicode 向量、
Schema 与 effort 叠加、无效参数、模型映射优先级、策略冲突和嵌套 Schema 的 billing 定位。

不要在完整工程检查运行时继续改被测源码。先冻结快照及哈希，再运行 unit / integration /
lint；最终校验工作区与快照一致。新增范围必须先补合同和来源证据，不能只沿用旧通过次数。

## 工具约束与推理组合复核

CC-20261010-002 固定已推送的 `465ed3738`，使用未修改 2.1.292 / 2.1.295 CLI
重新采集，并将工具控制加入三入口 / 两账号 / 六模型的组合审查。

- `tool_constraints_lab.py`：36 个显式工具 / 格式 / token 组合，通过 EXTRA_BODY
  观察原生序列化；该入口不能证明上游模型接受这些组合。
- `thinking_limits_lab.py`：18 个在构建请求前生效的 CLI token / effort 控制，
  区分原生最低预算与 EXTRA_BODY 后期覆盖；小于等于 1,024 的 Haiku 预算边界单列。
- `tool_constraint_audit_test.go`：通过 Go overlay 加入 service 测试包；设置
  `CLAUDE_TOOL_CONSTRAINT_AUDIT=/output/tools`，运行 `TestClaudeToolConstraintAudit`。
  它输出完整的 504 个观察、显式 400 和成功请求导出，测试进程 PASS 不代表约束相同。
- `tool_constraint_wire_lab.py`：将成功请求导出挂到 `/inputs`，将生产 transport 探针
  挂到 `/opt/trace-transport-probe`，沿用 network-none / tmpfs / ram_capture 工作流。
  接收端保存最终工具与推理字段，再用 `verify_encoding_policy.py` 检查 PCAP。

运行独立判定器：

```bash
python3 .github/claude-validation/validate_tool_constraints.py \
  /absolute/path/to/tools/observations.json /absolute/path/to/tool-constraint-result.json
```

矩阵不完整、意外报错、既有 schema / cap 回归或新约束不满足都会失败。当前审查基线因
并行限制、工具 strict、预算与 none 推理转换的缺口返回 1；不能将这个失败抹成通过。
既有 `validate_constraint_contract.py` 的通过结果与这个新增范围的失败结果同时保留。
完整结果见 [本轮报告](../../docs/claude-tool-reasoning-audit-20261010.md)。

## 工具与推理约束修复验收

[CC-20261010-003](../../docs/claude-tool-reasoning-fix-20261010.md)将 504 组审查转为永久
`TestClaudeToolConstraintContract`，新增 strict 能力策略、Schema 不被改写、none、并行开关
以及预算临界值测试。使用新的空输出目录运行：

```bash
cd backend
CLAUDE_TOOL_CONSTRAINT_EXPORT=/absolute/path/to/new-output \
  go test -tags=unit ./internal/service \
  -run '^TestClaudeToolConstraintContract$|^TestClaudeStrictToolCannotLoseCapability$' -count=1
```

保留同一 504 分母，八个无法同时满足 high 思考最低预算与低上限的 Haiku 4.5 转换请求现在
明确 400；合计 52 个拒绝、452 个成功导出。判定器还检查 true 开关、strict 能力头和精确的
none / adaptive 行为；旧 002 证据仍失败。不是删除场景或把问题改成无条件通过。

实际发送 `exports/` 时，在 `tool_constraint_wire_lab.py` 的隔离环境中设置
`CLAUDE_TOOL_WIRE_REQUIRE_CONTRACT=1`。接收端将按独立 contract 字段检查 max_tokens、
并行限制、strict 及能力头、工具 / 输出 Schema、thinking 类型和预算，再核对 PCAP。
旧 192 约束、102 入口和 36 摘要请求合同继续单独执行。
