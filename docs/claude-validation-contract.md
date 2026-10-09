# Claude 兼容验收合同

本文件定义可重复的通过条件。有限测试通过，只能证明这里列出的版本、输入与策略；不能写成
所有客户端、所有参数、所有平台或真实服务全部等价。CC-20261010-001 将之前观察型审查中的
四项遗漏改为固定断言，并保留此前失败记录。

## 固定范围

- 来源：官方 Linux x64 CLI 2.1.292 / 2.1.295，SDK 0.128.0、runtime v26.3.0；使用验证 README
  中固定 SHA-256。不得用滚动 latest 替换已测二进制后沿用旧结论。
- 六模型：Sonnet 4.6、Opus 4.6、Haiku 4.5 完整 ID、Sonnet 5.5、Opus 5.5、Haiku 5.5。
- 三入口：Messages、Chat Completions、Responses；两种账号：API Key、OAuth。
- 生产转发、最终序列化、请求重放和必要的能力头必须一起验证。

## 必须保持的约束

| 调用方意图 | Anthropic 目标的要求 |
|---|---|
| Chat response_format.json_schema / Responses text.format | schema 原内容进入 output_config.format.schema，不允许消失 |
| Schema 与 effort 同时存在 | 两者均保留，默认值或分支返回不能覆盖 format |
| Chat stop 字符串 / 字符串数组 | 准确转为 stop_sequences，保留顺序、Unicode 和转义内容 |
| 显式输出 token 上限 | 正值按原值传递；Chat 不套用真正 OpenAI Responses 目标的 128 下限 |
| 两种 Chat 上限同时出现 | max_completion_tokens 优先于 max_tokens |
| 非法转换参数 / 不支持的 format 模式 | 在发送前明确返回 400，不以默认值代替，也不静默删除 |
| 结构化能力被策略禁用 / 头覆写移除 | 明确拒绝请求，不能发送丢失约束的“成功”请求 |
| 嵌套 Schema 含 system 等名称 | 保留数据，生成的 billing 定位不能被嵌套内容遮蔽 |
| 压缩摘要去空白 | 匹配原生 JS trim 的字符集合，BOM / NEL 不能造成历史冲突 |
| 续聊与签名历史 | 保留摘要来源、历史前缀、保留消息及不透明签名校验，不能为通过测试而跳过 |

这里支持的输出 format 是 text 与 json_schema；json_object 及未知模式在 Anthropic 转换入口
明确拒绝。name / strict 是 OpenAI 包装属性，目标格式传递实际 schema；不改写 schema 内的
required、enum、additionalProperties、引用或注释。具体模型是否接受某个 schema 关键字，
仍由上游能力决定，离线测试不冒充官方接受验证。

## 固定测试入口

1. `TestClaudeExplicitConstraintMatrix`：6 模型 × 2 账号 × 三入口的适用组合，共 192 例；
   包含 schema、schema+effort、1 / 64 token、单 / 多停止词。Responses 没有 stop 合同，不虚造它。
2. apicompat 约束测试：1 / 64 / 127 / 128 / 4096 token、优先级、Schema 与各 reasoning 分支、
   非法参数及真正 OpenAI Responses 的旧下限保持不变。
3. `TestClaudeSummaryMatchesNativeUnicodeVectors`：独立原生 JS 函数生成的 62 个固定向量。
4. 原生 CLI 的两版空格 / BOM / NEL 场景：六场景、36 请求，生产恢复序列必须全部接受。
5. `validate_constraint_contract.py`：读取 102 个 API 入口观察和两版摘要序列，任何已列约束
   丢失、上限变化、场景缺失或续聊错误均退出非零。观察工具的 PASS 本身不算一致。
6. `constraint_wire_lab.py`：实际发送生产导出的 192 例，在接收端检查约束，再与 PCAP 核验。

新版本、新模型、新参数或新的跨参数组合，必须先增加独立来源及对应断言再扩大支持范围。
发现差异时记录最小输入、最终请求、原因、策略归类和修复验证；不能只增加“通过次数”。

## 明确保留的策略差异

- 账号凭据、会话绑定及必要的关联 ID 会按网关职责变化，不以真实凭据值作字节等价条件。
- 空 anthropic-version 补默认值；缺 display beta 时 updates 回退 omitted；对这些变化保留
  原始分母和逐项记录，不删除差异样本后声称全同。
- OpenAI 与 Messages 的既有缺省 effort / display 策略不等同于调用方的显式参数；显式参数
  和默认策略分别验证。字段相同也不证明服务端输出相同。
- Native 原字节保留与普通 API 协议转换分别定义合同。不能通过改写原生请求去“补齐”所有差异。
- 未测的平台、HTTP/2、TLS 恢复、所有远程开关、真实 thinking 签名、订阅及计费，不包含在
  上述通过声明内。
