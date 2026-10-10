# Claude SSE 事件边界对齐

CC-20261010-013，审查基线 `52263439f`，Claude 集成上游仍为
`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。

## 原因与独立证据

Chat / Responses 的流式及缓冲转换共四条路径，原先只在看到 `event:` 后读取紧接着的
一行 `data:`。合法注释、字段换序、多行 JSON 和 CR 分行会让这些路径丢掉正文或用量。
本轮的固定测试先在旧实现运行，28 个组合中 16 个失败，失败日志保留。

已核验的 Linux x64 CLI 2.1.292 / 2.1.295，SDK 0.128.0 / runtime v26.3.0，
内嵌 JavaScript 的 SSE decoder 都按空行提交事件、用换行合并 data、忽略注释，
冒号后只移除一个 ASCII 空格。来源模块及 SHA-256：

| CLI | 模块 / 类 | 模块 SHA-256 |
|---|---|---|
| 2.1.292 | `chunk-9yn9h839.js` / `UE` | `14f68808b28960aa01cb7dc43c6e876964df322deda0242f744ef02b81b55f86` |
| 2.1.295 | `chunk-75cm4bmg.js` / `su` | `2b792485cb95a165feec83c775234f62b730575cd0cfcf181162e264dbe804f8` |

新增 `sse_framing_lab.py`，复用已有固定二进制 pin、假凭据、TLS 模拟器及隔离检查。
两版未修改 CLI 分别运行七种响应：标准格式、注释 / id / retry 插入、data 在 event 前、
多行 JSON、CRLF、单独 CR、逐字节写入。每次必须只发送一个模型请求、退出 0、无超时，
输出准确为 `LOCAL_SSE_中文_🙂`，输入 / 输出 token 分别为 17 / 9。
14 个场景均通过；14 份实际响应与解密 PCAP 的正文 SHA-256 相同，内核丢包为 0。
逐字节写入不保证 TCP 分段边界，Go 组件测试另外用每次只读一个字节的 reader 覆盖拆分。

## 改动与兼容范围

四条转换路径共用 `gateway_anthropic_sse.go` 的事件读取器，按完整事件解析 JSON。
支持 LF / CRLF / CR，以及跨 Read 的 CRLF 和 UTF-8 字节；保留 data 的空白与空行，
event 重复出现时采用最后一个值。

原 `parseAnthropicSSEField` 的测试曾要求把冒号后的所有空格都去掉；共享读取器复用该
函数后，这一条预期改为保留第二个空格，与上面的两版分发源码一致。新增解帧矩阵及
内容 / 用量断言没有改成宽松比较；空 data、制表符、尾空格另有独立解析器断言。

`gateway.max_line_size` 同时约束单行及单个事件累计大小，累计包含被忽略的字段 / 注释，
避免用许多短行无限扩张内存。此大小约束是网关防护策略，可能拒绝过去仅逐行检查时允许
的超大多行事件，不是原生 CLI 的无限大小兼容承诺。每个已提交事件单独计数。

读取中断或超限后，缓冲路径返回 502；流式路径输出对应 OpenAI 格式的 error 并返回 Go
error，保留已接收用量，不再自动输出成功完成或 `[DONE]`。错误已标记提交，底层地址和
任意正文不反射给客户端。尚未结束的事件在读取错误时不会提交。

保留网关对干净 EOF 下缺少最终空行的历史容忍行为，单独测试，不称为 SDK 的严格等价。
原生 Messages 透传、count_tokens、模型参数、TLS profile 和认证策略不属于本轮修改。
上游主动发送的 `event:error` 语义、非法 JSON、缺 message_stop 的完整性以及所有未知
事件类型，尚未纳入本轮一致性声明；不能由合法解帧通过推断所有流式失败已对齐。

## 验证

永久回归包含七种格式 × 四条处理器的 28 项内容 / 用量断言，读取中断 / 超长行 /
累计超限 × 四条处理器的 12 项失败断言，以及解析器字段、空白、分片、EOF 和大小边界。
固定兼容 CI 清单增加四个必需测试，原 142 个证据文件及两个版本 pin 保持不变。

```bash
GOTOOLCHAIN=go1.27.2 go test -C backend -tags=unit ./internal/service \
  -run 'TestClaudeSSEFraming|TestAnthropicSSEReader|TestHandleResponses|TestHandleCC' -count=1
```

实际捕获重放可设置 `CLAUDE_SSE_CAPTURE_ROOT` 为某一版 `sse_framing_lab.py` 的输出目录，
同一 `TestClaudeSSEFramingHandlers` 会读取七份 `responses.json`，保持全部内容 / 用量断言。
缺少场景或响应数量不为一时失败，不自动退回合成输入。

本机证据目录：`/Users/kingford/claude-capture/sse-framing-20261010-VQeu78/`。
最终源码上的完整 unit 58 包 / 23,242 个测试通过事件，失败 0、退出 0；未变包正常复用
Go 测试缓存。两版实际响应重放共 56 个处理器组合通过；固定兼容入口的 32 项必需测试
全部执行通过，142 份固定证据哈希不变。golangci-lint 2.14.0 为 0 issues，验证器三个
负例测试组、gofmt 与 `git diff --check` 通过。

首轮 lint 的两个 strings.Builder 错误检查提示及旧字段函数未使用问题已修复，日志保留；
清理后重新运行完整 unit、固定兼容入口、两版捕获重放和 lint，没有沿用旧源码的结果。
最终八个代码 / 测试 / 采集器 / CI 清单文件的 SHA-256 与检查期间冻结值相同，
命令、结果、源文件与关键证据哈希见本机 `validation-summary.json`。

实验容器已自动清理，三个原有业务容器继续健康运行。本轮未运行数据库 integration、
前端或真实提供方测试。全部上游响应为本地合成数据，不代表真实提供方接受、订阅资格、
签名有效性或计费验证。尚未提交、推送、合并或部署。

交付补记（2026-10-10）：实现及回归已提交为
[891413778](https://github.com/skingford/sub2api/commit/891413778ea59d57c23ef376ffcba9f433e73173)，
并推送到 `origin/codex/claude-source-runtime-audit-20261009`。提交中的八个相关文件与
最终验证 SHA-256 完全一致；前文状态保留为验证快照。尚未核验推送后的托管 CI，
未新建 PR、合并或部署。
