# 完整请求日志与 Claude 故障分析

此 fork 默认开启独立的网关 HTTP 详细日志。不采样，`max_body_bytes: 0` 时不截断已读取或写出的正文。升级到包含此改动的二进制 / 镜像并重启后生效；仅修改工作区不会让现有服务开始记录。

## 保存哪些证据

| 记录 | 内容 |
| --- | --- |
| `request.start` / `request.end` | 独立 `trace_id`、请求 / 客户端关联 ID、时间、路由、IP、协议、状态、用户 / Key / 分组 / 最终账号 ID、模型、取消与结束状态 |
| `client.request` | 网关业务代码实际读取的客户端原始正文，位于模型映射、协议适配和 Claude 身份改写之前 |
| `upstream.request` | 共享 HTTPUpstream 每次 RoundTrip 的最终方法、脱敏 URL / 请求头、账号 ID、代理地址、并发设置、传输 profile；正文按传输读取的字节记录 |
| `upstream.native_headers` | Claude 原生传输处理后的应用头、保留的大小写及显式排序字段，含 beta、UA、SDK、会话头；Host / Content-Length 也可从请求记录关联 |
| `upstream.response` | 状态、全部脱敏响应头、协议、长度、实际读取的响应正文，包括错误、SSE、request-id、cf-ray、retry-after 与限流信息 |
| `upstream.response_decoded` | 网关显式解压时另存解压后的正文；同时保留压缩数据。标准库自动解压的响应标记 `transport_uncompressed` |
| `client.response` | 实际写给客户端的正文，包括协议转换、错误改写和 SSE 心跳；记录写入错误 |
| `claude.account` / `claude.forward_end` | Claude 消息 / count_tokens 的账号类型、发送前调度快照、代理 ID、过期 / 限流 / 冷却 / 会话窗口；可用时保存模型映射、上游请求 ID、token 用量、首 token 时间、断连与错误 |
| 传输事件 | 连接复用、套接字本地 / 远端地址、首字节、请求写入错误；标准库支持时记录 DNS、连接与 TLS 结果。Claude 原生传输桥接连接 / 首字节 / 写入回调，自定义 TLS 不伪造握手状态 |

正文按最多 32 KiB 一片写入 JSONL，`base64` 保留所有字节（含二进制、UTF-8、SSE 换行、thinking / 工具结果、图片附件）。每条 body stream 结束时保存字节数、已记录字节数、SHA-256、`complete`、`truncated`、结束原因和读取错误。所有记录都有 `trace_id`、请求内序号 `seq`、进程实例 ID、UTC 时间和相对耗时。响应头 `X-Sub2api-Trace-ID` 可直接用于导出。

`complete` 只表示该应用层字节流的读取 / 写入完成，不证明模型消息在语义上成功。403 / 429 / SSE error 都会保留，成功请求也会保留，便于比较异常发生前后的变化。

## 位置、容量与配置

默认目录：`${DATA_DIR:-/app/data}/logs/request-traces/`，当前文件为 `requests.jsonl`，历史文件为 `requests-时间戳.jsonl`。Docker 使用已有 `/app/data` 持久卷；`docker-compose.local.yml` 下通常对应宿主机 `deploy/data/logs/request-traces/`。普通启动可用环境变量设置可写目录。

```yaml
gateway:
  request_trace:
    enabled: true
    directory: ""          # 空值使用上述默认目录
    max_size_mb: 100         # 单文件轮转大小，单位 MiB
    max_backups: 100         # 最多保留 100 个历史文件，另有当前文件
    max_age_days: 30         # 与容量条件共同作用，先达到者先清理
    max_body_bytes: 0        # 每条流的正文记录上限；0 = 不截断
```

对应环境变量为 `GATEWAY_REQUEST_TRACE_ENABLED`、`GATEWAY_REQUEST_TRACE_DIRECTORY`、`GATEWAY_REQUEST_TRACE_MAX_SIZE_MB`、`GATEWAY_REQUEST_TRACE_MAX_BACKUPS`、`GATEWAY_REQUEST_TRACE_MAX_AGE_DAYS`、`GATEWAY_REQUEST_TRACE_MAX_BODY_BYTES`，四份 Compose 配置均已透传。设 `GATEWAY_REQUEST_TRACE_ENABLED=false` 可关闭。配置修改需要重启。

默认容量约 10 GiB 加一个当前文件；这不是保证保存 30 天。正文的 base64 有体积开销，长上下文、附件、入站 / 出站及压缩 / 解压副本会进一步增加写入量。轮转清理是异步的，边界附近可短暂多一个文件。发生问题后尽快保留整个目录的副本，避免证据被轮转删除；单次长请求可能横跨多个文件。

目录权限为 `0700`、文件 `0600`。启动时无法建立私有日志文件会报错并停止启动，避免误以为已经记录；运行中磁盘写入失败不会中断推理，但会在运维日志报错，并在后续事件累计 `trace_write_failures` / `recorder_write_failures`。没有采样或异步队列丢弃；逐片同步写文件会增加磁盘 I/O 与延迟，未进行生产负载压力验证。每个服务进程 / 副本必须使用独立日志目录，不能共同轮转同一个文件。

认证头、API Key、Cookie、代理凭据和敏感 URL 参数脱敏，保留字段存在性；不序列化整个 Account / Credentials / Extra。**对话、工具内容、附件、身份 metadata 和模型回复按原文保存**，其中用户自己写入的秘密也会保留。日志不进入普通控制台 / 运维日志数据库，也不自动上传。

## 查找与导出

使用仓库内的 `deploy/export-request-trace.py`，只依赖 Python 3 标准库。建议对完整目录副本分析，避免扫描期间轮转。

```bash
# 按账号查找，包括中间上游状态（例如最终成功之前曾出现 403）
python3 deploy/export-request-trace.py /path/to/request-traces --account-id 123
python3 deploy/export-request-trace.py /path/to/request-traces --account-id 123 --status 403

# 按服务端 / 客户端关联 ID 查找
python3 deploy/export-request-trace.py /path/to/request-traces --request-id REQUEST_ID

# 将某次请求的事件时间线、各阶段正文及校验结果导出到新目录
python3 deploy/export-request-trace.py /path/to/request-traces \
  --trace-id TRACE_UUID --output /private/path/incident-TRACE_UUID
```

导出包含 `events.jsonl`、`manifest.json` 和形如 `0001-upstream.request.bin`、`0001-upstream.response_decoded.bin`、`0000-client.response.bin` 的原始正文。JSON / SSE 正文可直接作为文本查看；压缩 / 图片正文保持二进制。工具不覆盖已有目录，导出权限同样为目录 `0700`、文件 `0600`。

先看 `manifest.json`：只有事件序号连续、有请求开始和结束、无写入失败、所有正文偏移 / 字节数 / SHA-256 一致且未截断时，才标记 `verified_complete: true`。缺失旧轮转文件、截断、半行 JSON、请求仍在运行、提前关闭或进程中断会留下不完整标记；不要把缺失材料推断成“上游没有返回”。

分析 Claude 账号异常时，先用账号 ID 和时间定位首次异常及此前正常请求，再比较 session / account_uuid / device_id、客户端和上游版本、模型 / beta / thinking / 工具参数、代理 / 连接、并发与限流窗口、上游 request-id 及完整错误体。原文在各阶段正文中，可对照网关是否发生了身份 / 模型 / 内容转换；现有运维错误和用量日志可通过 request_id / client_request_id 联查。

## 覆盖边界

- 覆盖注册的网关 HTTP 路由（含 Claude Messages、count_tokens、Chat Completions / Responses 适配入口与别名），以及它们携带追踪上下文调用共享 HTTPUpstream 的请求。管理面登录、OAuth 回调、后台刷新 / 摘要任务、使用其他专用客户端的上游调用不在这个完整出站捕获范围。
- WebSocket 记录 HTTP 入口和接管事件，不捕获接管后的帧。应用层重试 / 故障转移和重定向会分配不同 RoundTrip 编号；传输库内部连接重试不能据此精确计数。
- 不主动预读或排空请求 / 响应。认证拒绝、请求体限额、断连、上游读取上限、提前关闭后尚未读取的字节不会补抓，会记录为部分材料。保持原有请求限制和取消行为。
- 这是应用层证据，不是 PCAP、TLS 私钥日志或完整网络抓包。套接字远端可能是代理，不能据此得知出口 NAT 的公网 IP。未捕获 CLI 绕过中转访问官方的遥测 / 配置流量。
- 日志可以帮助定位关联条件；服务端未公开的封禁判定不能从本地日志唯一确定。403 不自动等同封号，模拟测试也不代表真实服务接受、订阅资格、计费或风控验证。

变更及验证记录见 [CC-20261009-002](claude-change-log.md#cc-20261009-002完整网关请求证据日志)。

本轮检查结果与日志实现源码哈希见 [验证摘要](request-tracing-validation.json)；并行协议修复不包含在本轮验收范围。
