# Claude 错误响应与浏览器协议头对齐

CC-20261010-012，基于 `145c308385d43240c12da6f09920325ddcd9ef76`。
Claude 集成上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，沿用
CLI 2.1.292 / 2.1.295、SDK 0.128.0 / runtime v26.3.0 的既有范围。

## 已确认的差异

此前 Messages 的专用错误路径会保留重试头并调用限流记账；Chat / Responses 在
调用方拥有重试的分支只保留状态，没有同样的头转发与记账。此前“不重复发送”的测试
不足以覆盖这些响应差异。本轮先加入四入口回归，在旧实现上观察到失败。

Messages 的错误正文读取失败会直接返回 Go error；count_tokens 更早在读取正文时
返回通用错误，可能丢失已收到的上游状态和头。另有浏览器预检不允许协议版本 / beta 头。
这些是本地源码及模拟传输复现，未声称线上发生过或代表官方服务判断。

## 统一处理与边界

适用范围是已有 `claudeCallerOwnsRetries` 识别的原生 Claude、OAuth 兼容转换及托管恢复
操作。Messages、count_tokens、Chat、Responses 共用错误读取、记账、重试头和 Ops
记录；普通非原生 API Key 的原有 failover 分支保留，并有负向回归防止误改。

- 上游 HTTP 状态原样保留，每个操作只发送一次，限流处理调用一次。
- 仅转发实际存在的 request-id、x-request-id、retry-after、retry-after-ms、x-should-retry
  和 cf-ray，并向已获 CORS 许可的客户端暴露。cf-ray 的空值存在性保留，不转发 Set-Cookie。
- 保留三种下游 JSON 格式：Messages / count_tokens 的 Anthropic error、Chat 的
  error.type、Responses 的 error.code；两个 OpenAI 入口沿用 server_error 字段值。
- count_tokens 在成功响应读取器之前处理上述错误，成功响应的大小限制和解析保持原规则。原生 API Key 直通的独立计数路径也接入；
  完整正文明确表示计数接口不存在时，保留原有 404 / 本地估算回退和不记上游故障的语义。
- 错误正文沿用默认 512 KiB 上限，以及现有显式日志配置扩大上限的规则；额外读最多一个
  字节区分恰好达到上限和真实超限。其他提供方的通用读取器行为不变。
- 正文读取中断或超限时，保留已经确认的状态 / 响应头，返回通用 incomplete 错误提示。
  限流逻辑仍能使用状态和头，但不根据残缺 JSON 推断模型资格、签名或压缩恢复信号。
  Ops 记录 error_body_read_failed / error_body_limit_exceeded，详细原因不反射任意正文。
  超限错误采用明确的网关防护行为，不声称与 CLI 对任意超大响应完全等价。
- 完整错误正文继续使用已有的原生错误分类与脱敏规则。错误响应标记为已提交，避免
  后续兜底追加 SSE 或再发起请求。

CORS 白名单补入 Anthropic-Version、Anthropic-Beta，以及已经存在于网关转发白名单中的
Anthropic-Dangerous-Direct-Browser-Access。来源、凭据和鉴权策略保持原规则；预检测试
覆盖四入口的允许来源与拒绝来源，不等同于真实浏览器测试。

## 固定回归

新增四入口 × 完整 / 中断 / 超限的 12 组请求，断言状态、头、暴露字段、记账、关闭正文、
发送次数和下游 JSON 形状。另覆盖完整与中断错误不得混淆恢复信号、两条原生 API Key
路径、读取边界和非原生 API Key failover。

固定 CI 清单增加这些测试与 CORS 预检组；原 142 个样本及两版来源 pin 均未修改。
验证入口仍为 `.github/claude-validation/run_contract_ci.py`，本轮强制测试数增至 28。

证据目录：`/Users/kingford/claude-capture/error-parity-20261010-n3xgol05/`。
完整 unit 58 包 / 23,192 个通过事件，失败 0。随后 lint 检出一处私有超限错误字符串
首字母大小写问题，仅修正该字符串；接口文本、分类和控制流未变。回退这一处文本即可
与完整 unit 源码哈希相同，没有其他源码漂移。最终源码上的 28 项强制兼容检查全部
重新执行通过，142 份样本哈希一致，golangci-lint 2.14.0 为 0 issues；验证器三个负例
测试组通过。完整 unit 未在这处纯文本修正后重复执行。

初轮失败、计数直通分支修复和原生测试上下文修正均保留记录；样本和断言未放宽。
最终 3,358 个后端文件、四个 CI 实现 / 清单文件与最终冻结 SHA-256 一致，
命令、源码范围及证据哈希见本机 `validation-summary.json`。
本轮未重跑数据库 integration、前端、真实浏览器、CLI / PCAP 或真实提供方验证。
尚未提交、推送、合并或部署，GitHub 托管 CI 尚未运行。
