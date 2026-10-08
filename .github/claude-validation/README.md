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
