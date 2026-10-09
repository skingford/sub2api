# Claude Code 2.1.292：参数与 gzip 发送对齐

日期：2026-10-09。记录 CC-20261009-004，接续 [全面审查 CC-20261009-003](claude-comprehensive-audit-20261009.md)。
基线 `fee18477`（协议实现 `719ceb35`），release `b4430850`，上游 `3f1a2ea0`。

## 本次行为

| 差异 | 当前处理 |
|---|---|
| 原生 gzip 被解压重写 | 已验证 2.1.292 第一方 messages 路径，若最终逻辑正文未变，保留原 gzip 字节、Content-Encoding 和 cch=00000 |
| gzip 请求头顺序 | Content-Encoding 放在已排序应用头之后、Connection 之前，匹配未修改 CLI 抓包 |
| 显式关闭 thinking | 已验证且允许 disabled 的模型补 temperature=1；支持 effort 的模型仍补缺省 effort；显式值优先 |
| disabled 的额外字段 | 规范化为仅保留 type=disabled，不自动带 display updates beta |
| 显式 temperature | 单独指定 temperature 不再取消模型默认 thinking，保留对应 effort 和上下文编辑 |

没有修改上游 thinking 签名、账号归属或重试策略。已有 5.5 参数约束仍然生效；
CLI 的 EXTRA_BODY 能拼出某种 JSON，不能据模拟器成功就判断官方服务支持该组合。

## gzip 与正文策略

入口仍先解压以便完成模型权限、身份、计费与请求校验。原压缩数据只保存在当前请求上下文中，
以解压正文 SHA-256 约束恢复条件；不使用账号级、会话级或全局正文缓存。

- middleware 预读后回填 PrereadBody，不会丢失原压缩数据。
- 返回的压缩切片为独立副本；Body、ContentLength 和 GetBody 使用同一份不可变发送字节。
- 恢复限定为已经验证的原生版本、官方 HTTPS origin、messages 接口及 gzip sentinel 布局。
  原生保留开关、自定义目标、未知版本和显式编码策略不会被覆盖。
- 模型映射、beta 清理、托管身份 / 恢复前缀等若确实改变正文，旧压缩包不再符合摘要，
  因此沿用最终未压缩正文与 cch 计算。不能为了字节相等把旧模型或旧会话内容送回去。
- 计费、日志和恢复服务继续使用逻辑 JSON；HTTP 客户端收到的是正确的实际发送字节。

为避免把只验证过的解压前缀对应的整个压缩包发出，解压器现在读取上限加一字节，
超过既有 64 MiB 上限返回 MaxBytesError；CRC 损坏同样拒绝。gzip、zstd、deflate 共用此边界检查。

## 参数语义：直接控制与后置覆盖

新增五模型 × 四场景的 20 条未修改 CLI 请求，分开测试：

1. `MAX_THINKING_TOKENS=0`；
2. 仅通过 `CLAUDE_CODE_EXTRA_BODY` 覆盖 disabled thinking；
3. 同时关闭 thinking 并使用 EXTRA_BODY 添加无效的 display / budget；
4. 通过 EXTRA_BODY 设置 temperature=0.4。

实验明确显示：**相同的最终 thinking 对象，不一定来自相同的 CLI 构造流程。**
EXTRA_BODY 在缺省上下文等字段已经构造后再覆盖，直接关闭 thinking 则发生得更早。

普通 API 的顶层 `thinking` 按直接控制解释，补齐与该控制相符的默认值，并清理 disabled
中的额外字段。不根据几个无效键猜测客户端用了 EXTRA_BODY，也不向 API 中引入隐式模式开关。
显式 context_management、temperature、effort 等合法值继续保留。

因此，旧审查中 **仅 EXTRA_BODY 后置覆盖 disabled** 的样本与普通 API 仍有温度 / 上下文
默认字段差别；这是输入语义边界，不能宣称该场景也已逐字段相等。直接控制及其清理行为，
已分别与对应原生场景匹配。要保留 CLI 后置覆盖的完整结果，应转发 CLI 已构造的原生请求。

5.5 的原生“关闭 thinking”会省略 thinking，而不是发送 disabled；普通 API 的显式 disabled
仍按原有 5.5 约束拒绝。本次也没有放宽 Sonnet 5.5 的非缺省 temperature 限制。
这些组合在模拟器中被响应，不构成真实上游接受的证据。

## 验证

- 原有 51 个场景、89 条原生请求重放：**356/356 逻辑正文、原发送字节、最终长度、GetBody 一致**；
  **178/178 同认证组合的应用头一致**，跨认证必需的 OAuth beta 单列。
- 生产构建器导出的 gzip 请求经真实传输组件发送，直连、HTTP CONNECT、HTTPS CONNECT、
  SOCKS5 四条路径的压缩字节、解压 JSON、完整头值和头序、ClientHello 非随机部分均与原生一致；
  PCAP 校验通过、丢包 0。首次核对发现 Content-Encoding 排序差异，修复后新建目录复验通过。
- 新增 20 条参数捕获的 PCAP 字节一致，丢包 0。永久回归验证支持的直接控制与温度路径，
  并保留不支持组合的本地拒绝断言。
- 原生 CLI 直接调用生产恢复 / Forward 服务：12 条会话、96 次生成、24 次计数，
  连续压缩、迁移及迁移后压缩通过，跨会话混入 0。此实验的存储与上游是测试实现。
- 增加原压缩数据不可跨请求复用、返回切片不可修改缓存、预读 / clone、损坏 CRC、
  超限解压、正文改变不恢复旧包、版本 / origin / 头策略，以及原生 gzip 头序黄金回归。

完整 unit **58 个包**、完整 integration **52 个包**通过；gzip 头序最后补正后，repository
集成包再次通过；最终全量 lint **0 issues**。最终 unit 启动后没有 Go 源码变动。
完整工程检查及最终源文件哈希见 [验证记录](claude-parameter-wire-validation.json)。
提交前从暂存区另建独立快照，排除并行的请求追踪实现；httputil / service / repository
三个包的相关回归再次通过。两种源码范围和对应哈希分别记录。
首轮 unit 的旧“disabled 省略温度”断言依据新原生证据修正；显式温度保留断言全部保留。
首轮 lint 使用了不匹配的宿主 Go 环境；显式固定 GOTOOLCHAIN=go1.27.0 后通过。
这些初次记录均保存，没有覆盖为成功记录。

全部 CLI 实验使用固定官方 Linux x64 2.1.292 / SDK 0.128.0、假凭证、独立配置、
Docker `--network none` 与回环服务。二进制 SHA-256：
`a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。
原始材料：`/Users/kingford/claude-capture/parameter-wire-alignment-20261009-c0k0t5v2/`。
未执行真实官方模型请求、推送、合并或部署。
