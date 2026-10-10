# 会话、版本同步、日志与 CI 加固

CC-20261010-011，基于 `24a7b8f4f132faa827c04a8f1256ffa572d08eb1`。
Claude 集成上游仍为 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`；本轮没有升级
CLI 2.1.292 / 2.1.295、SDK 0.128.0、runtime v26.3.0 或扩大 Linux x64 协议范围。

## 会话调度

010 已统一请求头校验与调度输入；本轮补齐 metadata 优先分支。JSON metadata 中的
session_id 两侧空白原先会被校验器接受，随后直接交给 uuid.Parse 的调度分支却返回空键。
现在两个调度入口均使用与校验一致的 TrimSpace，保留原 metadata 优先级和缓存键命名。
回归同时断言规范 UUID、路由键及持久账号归属，不仅检查是否返回成功。

## 版本同步

新增内部设置 `claude_code_version_last_checked_at`，记录成功获取有效版本并完成版本
持久化检查的 UTC 时间；即使版本未变化也记录。首次升级没有该字段时，继续参考旧版本
行的 UpdatedAt。无效 / 未来时间不用于跳过检查；网络或持久化失败不会标记成功。
不需要新增表或迁移，也不改变手动版本优先、自动启用仅限已验证配置的规则。

版本写入采用数据库 compare-and-swap：UPDATE 条件匹配旧值，缺失键由唯一约束及
ON CONFLICT DO NOTHING 仲裁；竞争失败后重新读取并比较版本，最多尝试八次。
竞争或数据库错误会记录失败，不能退回普通 Set 覆盖新值。设置仓储缺少原子能力时明确
停止写入。多个进程仍可能同时查询 GitHub；此项防止版本回退，不声称实现了集群单次调度。

Start 幂等，Stop 取消正在运行的 GitHub / 数据库上下文并等待工作协程退出；停止后不再
启动，取消的主查询不继续发起列表回退。保留关闭同步、草稿 / 预发布过滤等已有语义。

## 完整日志的开销

日志继续同步写入，不引入异步队列、采样或静默丢弃。每条 trace 使用独立锁保持事件次序，
JSON 编码移出共享写盘锁；写盘仍串行。用固定结构替代 envelope map，减少排序与分配。
并发编码意味着每个等待写入的 trace 可持有一条已编码事件；正文仍按 32 KiB 分块，
并发越高所需内存越多，并不消除磁盘背压。

`Recorder.Stats()` 返回累计事件数、失败数、实际写入字节和编码 / 共享写盘锁等待 / 写盘耗时；
Close 时输出不含正文的运行汇总。累计耗时跨并发请求求和，可以超过墙钟时间。
日志 JSON 字段及 schema=1 保持兼容，字段顺序可能变化；`recorder_write_failures` 现在
是事件编码时的全局快照，`trace_write_failures` 及同 trace 序号仍按事件顺序准确记录。

可复现基准：

```bash
cd backend
go test ./internal/pkg/requesttrace -run '^$' \
  -bench BenchmarkRequestTraceStreaming -benchtime=100x -count=3
```

每次请求写八个分块和一个结束事件；以下取三次运行的中位数。file 使用本机临时普通文件，
不包含 fsync、容器磁盘或轮转性能；slow_1ms 是每次 Write 人工等待 1 ms。
first_chunk / chunk_gap 衡量记录器给分块增加的时间，不是模型首字时间或真实 token 间隔。

| 32 KiB、16 并发 | 摊销耗时（总墙钟 / 请求数）：前 → 后 | 首块 P95：前 → 后 | 后续块 P95：前 → 后 | 分配次数：前 → 后 |
|---|---:|---:|---:|---:|
| 丢弃输出，仅测编码与锁 | 538 → 285 µs | 1.425 → 1.834 ms | 1.481 → 2.313 ms | 433 → 221 |
| 临时普通文件 | 978 → 594 µs | 2.078 → 2.078 ms | 5.816 → 2.541 ms | 433 → 226 |
| 每次写入延迟 1 ms | 10.908 → 10.412 ms | 19.987 → 18.919 ms | 20.074 → 19.561 ms | 433 → 219 |

表中 ns/op 是并发运行的总墙钟时间除以请求数，不是单个请求的响应耗时。
普通文件组吞吐改善，但纯编码组尾延迟上升；1 KiB / 16 并发文件组的后续块 P95 也从
1.095 ms 升至 1.214 ms。不能据此承诺所有负载延迟下降。慢写组优化后平均每事件锁等待
仍约 16.6 ms，说明需要为完整日志提供足够磁盘吞吐；本轮不自动改变部署或日志完整性策略。
原始日志和全部十二组结果保存在本机证据目录，未只选取有利结果。

## 固定兼容检查进入 CI

`.github/claude-validation/fixture-manifest.json` 固定 142 个已有样本文件、两版 CLI 二进制
来源 pin，以及强制测试名单。校验文件集合和 SHA-256，任何删除、新增或修改都必须显式
更新证据清单；版本准入表还必须与清单版本集合一致。CI 不自动生成或更新基准。

```bash
python3 -m unittest discover -s .github/claude-validation -p test_contract_ci.py
python3 .github/claude-validation/run_contract_ci.py --output /tmp/claude-contract-results
```

backend-ci 新增独立 `Claude pinned compatibility contract` job，执行 CCH 向量、请求
构造、字段约束、自定义 system 布局、HTTP/1.1 字节 / 顺序及本机 ClientHello 回归。
Go 命令成功仍不足以通过：所有必需测试和包都必须有 pass 事件，缺失、跳过、子测试失败
或包未完成均拒绝。验证器自测包含样本损坏、缺失、新增、pin 不匹配及空测试运行负例。
始终上传本次 JSON 摘要和 Go 日志，供定位失败；尚未更改仓库分支保护规则。

这里复用已有独立采集样本及本地回环传输测试，不下载或执行新 CLI，不重新生成 PCAP，
不访问真实模型服务。依赖安装可能访问 Go 模块源；离线兼容范围不能扩大成真实订阅、
签名有效性、计费或官方接受验证。新增版本仍须先取得独立证据并更新变更记录。

## 验证与交付

证据目录：`/Users/kingford/claude-capture/remaining-hardening-20261010-1ik1jvj7/`。
最终完整 unit 58 包 / 23,164 个通过事件、integration 52 包 / 13,784 个通过事件，
均失败 0、退出 0；日志包 race 检查通过，golangci-lint 2.14.0 为 0 issues。
本地 CI 入口校验 142 份样本并实际执行全部 21 项强制测试，三个验证器负例测试组通过。
真实 PostgreSQL 的首次创建与条件更新各运行 16 路竞争，两轮都只有一个写入者成功。

首轮并行检查期间，既有 WebSocket 大帧用例超时、安全审计短时序断言失败，另有两包
因 testcontainers 清理辅助容器启动故障未完成数据库验证。保留原始失败日志，未修改
源码或放宽断言；等待其他检查结束后，完整 unit 顺序复跑通过，integration 改为 -p 1
重跑通过。第一次重跑的代理还遇到 Unix socket 路径过长，缩短临时路径后才开始测试。
新建实验容器、代理和 socket 均已清理，原三个业务容器同 ID 且健康。

最终 3,357 个后端文件及四个 CI 实现 / 清单文件与冻结 SHA-256 全部一致；
命令、分母、初轮失败、清理状态及证据哈希见本机 `validation-summary.json`。
Go 正常复用未变包的测试缓存，不能把重跑统计累计为额外覆盖。
尚未提交、推送、合并或部署，GitHub 托管 job 尚未运行；没有新 CLI 或真实提供方验证。

交付补记（2026-10-10）：实现、回归、基准和 CI 配置已保存为
[76b913703](https://github.com/skingford/sub2api/commit/76b9137034db76199b2626b89901d2f1c9263b3f)，
并推送至 `origin/codex/claude-source-runtime-audit-20261009`。提交归档与验证清单一致；
前文状态保留为验收快照。尚未合并或部署，推送后的 GitHub 托管 CI 结果尚未核验。
