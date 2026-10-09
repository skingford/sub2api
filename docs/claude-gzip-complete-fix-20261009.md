# CC-20261009-006：修复计数 gzip、分块头序和编码覆写

本记录接续 [CC-20261009-005 审查](claude-post-alignment-audit-20261009.md)，保留其历史结论。
基线 `37289d73`，release `b4430850844263e3fd3d2d180f514099bffad04e`，
上游 `3f1a2ea0a760730e3bc528105c00b4ee4f23e469`。协议限定 Claude Code 2.1.292 / SDK 0.128.0。

## 修复行为

1. **大型 count_tokens**：将原 gzip 恢复条件与 messages 的 billing / cch 判断分开。
   已验证的原生请求、第一方 HTTPS 目标、已知版本及最终逻辑正文 SHA-256 相同才恢复原压缩字节。
   count_tokens 没有 billing 块也能保留 gzip。模型、身份、策略改写后仍不能重放旧正文。
2. **分块 gzip 头序**：原生运行时在应用头排序后追加 Content-Encoding；CLI 自行分块压缩时，
   编码头参与应用头排序。根据固定版本实际封装格式保存请求独占的上下文标记，原生 transport 据此排序。
   保留字节、Content-Length 与 GetBody，不重新压缩。
3. **编码覆写**：前后端禁止静态覆写 Content-Encoding；转发时过滤过去落库的旧配置。
   finalizer 根据最终正文决定编码，清除旧头的所有大小写变体。通用 raw header 写入 / 删除也做全量
   大小写去重；读操作支持非标准 casing 并确定性选择。修改后的 JSON 不会再带错误的 gzip 声明。

## 分块识别依据与范围

只读提取固定官方 Linux x64 CLI，二进制 SHA-256：
`a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。

- `chunk-47d8fnm7.js`，偏移 `207221829`，SHA-256
  `0bfe82c8210f3f477a7e651910e9d5ea744cf914a9e4da1f64495bb3358c8043`：
  `oos/Sto` 用 Z_SYNC_FLUSH 拼接分块，封装头为 `1f8b08000000000000ff`，
  最后一块刷新标记后增加 `0300` 空终止块，再写 CRC / 长度。
- `chunk-9yn9h839.js`，偏移 `209661529`，SHA-256
  `14f68808b28960aa01cb7dc43c6e876964df322deda0242f744ef02b81b55f86`：
  `aM/dee` 在 fetch 之前设置编码头并移除 runtime compress 选项；mode 1 可在后续轮次复用分块。

识别同时要求完整固定头和 trailer 前的 `0000ffff0300`，不单看 OS=255；Unicode 运行时压缩也可能
使用 OS=255。生产调用发生在入口完整解压 / CRC 校验及摘要相等之后，并限定已识别的原生版本。
这只是固定 CLI 两种实现之间的排序选择，不是任意 gzip 编码器的通用来源判定或认证手段。

## 验证

- 新增 12 场景 / 20 原生请求，其中 18 条 gzip：PCAP 原字节一致、丢包 0。
- 连同旧 99 条样本，共 119 请求 / 476 个转发组合：逻辑正文、实际字节、方法、URL、长度、GetBody
  全部一致；238 个同认证组合应用头一致。新样本四种配置共 72 个永久回归。
- 19 个 gzip 输入 × 四条传输路径 × 日志开关，共 152 组：原字节、完整头值、头序、1499 字节
  ClientHello 的非随机部分全部一致。76 组日志原文、偏移、摘要、完整性、凭据脱敏检查通过，写入失败 0。
- 22 次旧编码覆写 / 模型映射的实际发送全部可解码为合法 JSON，PCAP 全部一致，丢包 0；无头体不匹配。
- 12 条托管会话、96 次生成、24 次计数，连续压缩、迁移后再压缩与续聊通过，跨会话混入 0。
- 前端配置验证 70 项通过，修改文件 eslint 通过。完整 unit 58 个包、integration 52 个包通过；最后一处测试写法修正后 Claude 包回归通过，固定快照全量 lint 0 issues。

结构化证据及来源哈希见 [验证摘要](claude-gzip-complete-fix-20261009.json)。
最初前端缺少依赖，按未修改的锁文件安装后重跑；首份 overlay 路径多出 backend 层，测试清单检查发现
遗漏注入测试，修正绝对路径并重建后明确运行两个 audit。两者均为实验接线问题，不计为产品缺陷。

原始材料目录：`/Users/kingford/claude-capture/encoding-complete-fix-20261009-_n_icwx2/`。
全部原生采集和真实 HTTP/TLS 重放均在 Docker `--network none`、官方域名映射回环、空配置和假凭据下运行。
本地解码器响应不代表官方服务接受、订阅资格、签名或计费验证；未部署。

完整测试命令为 `GOTOOLCHAIN=go1.27.0 go test -tags=unit ./... -count=1 -json` 和
`GOTOOLCHAIN=go1.27.0 go test -p 1 -tags=integration ./... -count=1 -json`。
条件跳过的 20 / 16 个测试条目在 JSON 中逐项保留；原生 CLI 联调另在隔离容器运行。

全量测试完成后，工作区出现独立的 CC-20261009-007 Go 1.27.2 / 依赖升级。本轮提交保留原来的
Go 1.27.0 依赖，升级改动和它的记录继续留在工作区；不将本轮结果算作新依赖的验收。
最后从实际暂存内容导出完整快照检查 lint。共享工作树上的旧 lint 已主动终止；隔离检查第一次碰到
runner 锁，后一次报告直接传入 nil Context 的测试断言和超时。删除该断言，复跑 Claude 包后，
`golangci-lint run --allow-serial-runners --timeout 20m` 完整通过（2.13.0，Go 1.27.0，0 issues）。
3,186 个 Go/SQL 文件中，生产代码与全量测试时完全一致，仅这一处测试断言在之后调整；最终哈希已记录。
