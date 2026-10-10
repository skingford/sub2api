# CLI 对齐差异修复

对应 CC-20261010-008，跟进 [007 审查](claude-cli-alignment-audit-20261010.md)。代码基线
`a426396d8` 加同工作区的 006 / 007；Claude 集成上游仍为
`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`，没有同步 main。

## 本轮修复

| 已确认问题 | 修改 |
|---|---|
| 保留模式给计数探针补 system | count_tokens 不再调用生成请求的 system 包装器；缺省 system 保持缺省，调用方给出的 system 保持原值 |
| Chat 省略 strict 变 false | Anthropic 目标保留 nil / false / true 三态；真正 OpenAI Responses 目标继续使用原默认规则，覆盖 tools 与旧 functions 两入口 |
| 默认路径仍混淆工具名 | 对齐模式改为新配置默认，工具原名、调用历史和工具选择不经静态 / 动态混淆；false 仅用于显式回退旧策略 |
| 缺少明确的 system 分支 | 普通 API 明确映射交互式 CLI 的自定义 system 分支；标记为 cli，按新增的两版 TTY 抓包构建身份 / 自定义文本缓存布局 |

不是把所有 CLI 模式折叠成一个模板。普通 API 的 system 是调用方指令，不能无条件混入
依赖 CLI 本地工具、环境和状态的完整默认提示词。原生 CLI 的默认、替换、追加和 SDK
请求继续走原生透传，保留自己的入口与内容。

## 自定义 system 的证据与边界

额外使用未修改 Linux x64 CLI 2.1.292 / 2.1.295，在真实 PTY 中采集
`--system-prompt <text>`、`--system-prompt ""`、`--append-system-prompt <text>`：

- 空自定义 system：归因块加官方 CLI 身份块；身份块带 `ephemeral / 1h`。
- 非空自定义 system：归因、身份、自定义原文；身份与原文都带 `ephemeral / 1h`。
- append：仍使用默认四块布局，不能冒充 replace。API 默认不隐式选择 append。

两版本的上述自定义布局相同。`testdata/claude_cli_custom_system_profiles.json` 保存独立
抓包的身份 / 自定义块、UA、二进制 pin 和原始正文哈希；测试预期没有从网关输出生成。
归因块中的会话 / prompt 计数等应用状态不在这个布局等价断言内。

API 未提供 system 时，明确采用“空自定义 system”的语义；字符串或适配器产生的单个
无显式缓存 text block 采用已测自定义布局。多 block 或显式 cache_control 是 API 扩展，
按调用方原样保留；不把它们声称为单一 CLI 参数的完整等价。
自动缓存断点只在四断点限额内添加，不挤掉调用方断点；原有非法 / 超限输入仍明确拒绝。

相较旧版本，默认生成的 system 缓存由手选的 5m 模板改为对应 CLI 自定义分支的 1h。
调用方显式缓存设置继续优先；总 system 注入开关关闭时不生成前缀。

## 启用和回退

新配置和四种 Compose 的缺省值均为：

```dotenv
GATEWAY_CLAUDE_OAUTH_PRESERVE_CALLER=true
```

或直接后端配置：

```yaml
gateway:
  claude_oauth_preserve_caller: true
```

环境变量覆盖 YAML。已经显式设置为 false 的部署保持旧策略；移除覆盖或改为 true 才
启用对齐模式。false 保留旧模板、包装和工具别名作为回退，仍有 007 记录的已知差异。
切换后应新建会话，避免把旧别名 / system / 缓存历史与新策略混用。没有自动修改生产
配置、已有账号或线上会话，也没有部署。

对齐模式优先于旧的自定义扩充块、messages 缓存改写、强制 TTL 和日期规范化设置；
明确的调用方参数、已有身份归属检查、签名历史与错误处理继续保留。
续聊客户端仍需回传 `X-Sub2API-Session-Id`；Chat 仍需保留 `anthropic_content`。

## 固定验收

保留 007 的 128 个输入、256 个观察及所有历史差异。新增独立判定器
`validate_cli_alignment_fix.py` 固定检查：

- 124 组原生请求内容保留；完整字节与头序另由传输判定器核验。
- 72 个普通计数输入（两种策略）不改变计数正文。
- 24 个 Chat 工具保持 strict 省略；组件测试另覆盖显式 false / true 和 OpenAI 目标。
- 默认对齐模式的 30 个生成请求，身份 / 自定义 system 布局与独立 CLI 自定义样本一致。
- 六条 MCP 请求保留 36 个工具原名。

同一判定器在修复前实际 TLS 记录上检出 68 个失败案例，退出 1；没有用修改旧输入或
隐藏回退模式差异来得到通过。完整默认 / append 提示词、状态相关 diagnostics、随机
身份与调用方选择的流式 / 超时差异，继续在原比较报告中显示，不计作“全请求一致”。

本轮源码与原始验证材料保存在
`/Users/kingford/claude-capture/cli-alignment-fix-20261010-VHHR1K/`。
所有 CLI / TLS / CCH 实验均使用断网 Docker、假 OAuth
和合成响应；真实提供方接受、订阅资格、签名有效性和计费仍未验证。

## 最终执行结果

- 新增六个 TTY 场景、12 条请求，PCAP 相同，零丢包。
- 原 256 组观察及实际 TLS 记录均通过固定修复合同；旧记录仍有 68 个失败，验证负控制有效。
- 全部 256 组发送与 PCAP 相同，归一化 ClientHello 匹配既有 pin；124 组原生正文、头值和
  头序保持，无网关头泄漏。112 个 CCH 正文与独立原生运行时计算一致，探针入口修改单列。
- 完整 unit 58 包、integration 52 包通过，lint 0 issues；配置 / Compose 检查通过。
- 测试前后 3,352 个后端文件哈希一致，实验容器和集成代理已清理，原业务容器保持健康。

详细分母和哈希见[结构化结果](claude-cli-alignment-fix-20261010.json)。代码尚未提交或部署。
