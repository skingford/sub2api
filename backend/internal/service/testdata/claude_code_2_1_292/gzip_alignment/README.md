# CLI 2.1.292 gzip 回归样本

来源：CC-20261009-006，未修改 Linux x64 CLI，SDK 0.128.0，断网 Docker、空配置、假凭据。
CLI SHA-256：`a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3`。
采集器：`.github/claude-validation/gzip_fix_lab.py`；2026-10-09 本地独立抓包。
完整原始材料：`/Users/kingford/claude-capture/encoding-complete-fix-20261009-_n_icwx2/`。

保留实际 gzip 和原始头顺序；逻辑 JSON 由 gzip 解出并核对 decoded_sha256，避免重复存储大型合成提示。
application_encoding 来自原生头序的位置，不从待测 Go 实现计算。多轮场景在轮间等待两秒，覆盖 mode 1
首轮运行时压缩和后续分块复用，以及 mode 2 的两轮分块压缩；级别 1、6、9 均覆盖。
这些样本不是官方服务接受或订阅验证。
