# 部署说明（当前架构：VPS 自构建，push 即部署，2026-08-16 起）

## 拓扑

- **代码仓**：本地 `~/work/nimbus-news`（唯一 git 仓库，两仓制：nimbus-os 持 datasources/guanfu 模块，经 go.mod replace 引用）
- **运行机**：VPS（`root@45.77.26.44`），部署目录 `/opt/news`
- **服务**：systemd `news-platform.service`（`Type=simple`，ExecStart=`deploy/launch-vps.sh`，TZ=Asia/Shanghai）
- **角色**：VPS 为 `active`；Mac 为 `standby`（同配置，`NIMBUS_RUNTIME_ROLE` 或 `runtime.role` 注入）
- **进程**：`./bin/platform -config config.platform.vps.yaml`，API 仅监听 127.0.0.1:8081

VPS 关键目录：

```
/opt/news/
├── bin/platform              # 部署二进制（VPS 本地构建，owner root）
├── scripts/ → src/nimbus-news/scripts   # git 软链，随源码自动更新
├── deploy/  → src/nimbus-news/deploy    # git 软链（脚本/服务文件）
├── src/nimbus-news/          # git 源码仓（deploy key 认证）
├── src/nimbus-os/            # git 源码仓（datasources/guanfu 模块）
├── venv/                     # python + akshare/ecocal（脚本源依赖）
├── config.platform.vps.yaml  # 含密钥，gitignored，本地为真源
├── .env                      # 密钥真源（不提交、本地无副本）
├── data/ logs/ backups/ tmp/ # 运行数据/日志/备份/构建临时目录
└── .deployed-commit          # 当前已部署的 commit hash
```

## 部署（push 即部署）

```bash
cd ~/work/nimbus-news
git push origin main          # 唯一部署动作
```

VPS cron 每 5 分钟跑 `deploy/vps-build-deploy.sh`：

1. `git fetch` 对比 `.deployed-commit`，无新提交直接退出
2. 有更新 → `git pull` → `go build`（`GOTMPDIR=/opt/news/tmp`，避免 /tmp tmpfs 不足）
3. 原子替换 `bin/platform.new → platform` + `systemctl restart`
4. 轮询 `/api/health/live` 最多 60s；失败自动回滚旧二进制（就绪探针 `/api/health` 不参与判定）
5. 成功则更新 `.deployed-commit`

构建环境：VPS 装 Go 1.26.3（/usr/local/go）；私有仓认证用两个 GitHub
deploy key（nimbus-news / nimbus-os 各一），经 `~/.ssh/config` 的
`github-news` / `github-os` alias 区分。

**代码自动部署，配置不会。** `config.platform.vps.yaml` 与 `.env` 不在 git 里，
模板变更需要按下一节手动合并。

## Mac（launchd，standby）

Mac 是常驻**只读实例**：不摄入、不投递、不生成简报，只提供 `/api/health` 与只读 API，
用于本机调试，以及 VPS 故障时临时 `promote` 接管。配置与 VPS 分开：
Mac 用 `config.platform.yaml`，VPS 用 `config.platform.vps.yaml`。

### 前置

- macOS + Go 1.26+（`go version`）
- Python 3.12（推荐）。`make python-deps PYTHON=python3.12` 可指定解释器；
  默认 `python3` 若版本过新，`ecocal` 等包可能没有预编译 wheel
- 仓库路径固定：plist 渲染时会写入绝对路径，移动仓库后必须重新 `make install-mac`

### 首次部署

```bash
cd ~/work/nimbus-news
make mac-bootstrap          # 生成 config/.env → 建 .venv 装依赖 → 构建 → 注册 launchd
$EDITOR .env                # 填 webhook/API key；角色默认 standby，通常不用改
make mac-status             # 看 launchd 状态 + /api/health/live + /api/health
```

`make mac-bootstrap` 等价于 `make init` + `make python-deps` + `make install-mac`，
已有 `config.platform.yaml`、`.env` 不会被覆盖，可以重复执行。日常 `make install-mac`
也会先跑 `make init`：配置文件缺失时自动按模板生成，已有文件保持不动。

真实配置不入库：`config.platform.yaml`、`config.platform.vps.yaml` 都含部署差异
（后者还含密钥），仓库只保留 `config.platform.yaml.example`；密钥统一写进 `.env`。

### 日常更新

```bash
git pull
make config-diff CONFIG=config.platform.yaml   # 模板有变更时按输出合并
make install-mac            # 构建 + 重新渲染 plist + 重启
```

只改了 `.env` 或 `config.platform.yaml`，不需要重新构建：

```bash
make mac-restart
```

### 命令

| 命令 | 作用 |
|---|---|
| `make mac-bootstrap` | 首次部署：配置模板 + Python 依赖 + 构建 + 注册 launchd |
| `make install-mac` | 日常更新：构建 + 重载 launchd |
| `make mac-restart` | 不构建，只 `kickstart -k` 重启 |
| `make mac-status` | launchd 状态、`/api/health/live`、`/api/health` |
| `make mac-logs` | `tail -f ~/Library/Logs/news-platform.{out,err}.log` |
| `make uninstall-mac` | 卸载 launchd agent |

### 角色与配置

- `deploy/launch-mac.sh` 在 `.env` 未设置 `NIMBUS_RUNTIME_ROLE` 时注入 `standby`，
  避免 Mac 与 VPS 双活；要改 `admin`/`active` 就写进 `.env` 后 `make mac-restart`。
- 临时接管：`curl -X POST 127.0.0.1:8081/api/runtime/promote`
  （配置了 `NIMBUS_ADMIN_TOKEN` 时带 `Authorization: Bearer <token>`）；
  重启后回到 `.env` 里的角色。
- `NIMBUS_CONFIG` 可覆盖启动配置文件名，默认 `config.platform.yaml`。

### 日志与排障

日志在 `~/Library/Logs/news-platform.out.log` 和 `.err.log`。launchd 的 `KeepAlive`
会在进程退出 15 秒后自动拉起。

```bash
launchctl print gui/$(id -u)/com.news.platform   # 最近退出码/pid
lsof -nP -iTCP:8081 -sTCP:LISTEN                 # 端口占用
```

常见问题：

- `缺少 bin/platform` → `make build-native`
- `缺少 config.platform.yaml` → `make init`
- 脚本源报 `ModuleNotFoundError` → `make python-deps`
- 健康检查一直失败 → 先看 `.err.log`；A 股收盘等脚本源首次运行可能较慢
- 仓库搬家后服务起不来 → plist 里是旧绝对路径，重新 `make install-mac`

### 源健康（Mac / VPS 通用）

`GET /api/sources/health` 返回逐源状态；`/api/health` 里带 `sources` 汇总计数。
间隔 ≤5 分钟的源连续 6 次抓取 0 条会降级（`source_health.empty_degraded_threshold`），
用于发现上游停更、RSS 换地址或源内过滤过严——这些情况 Fetch 不会报错。
降级只写状态和日志；`notify_channels` 为空时不推送。

### 离线备份

`deploy/mac-backup.sh` 通过 ssh 让 VPS 现场生成快照再取回，保留 7 份。
建议加入本机 crontab：

```cron
30 3 * * * /path/to/repo/deploy/mac-backup.sh >> ~/Library/Logs/news-backup.log 2>&1
```

## 配置变更流程（模板入库，实例配置不入库）

仓库只保留 `config.platform.yaml.example`。真实配置含部署差异和密钥，留在各自机器：

| 实例 | 配置文件 | 维护方式 |
|---|---|---|
| Mac | `config.platform.yaml` | `make init` 从模板生成；密钥写 `.env` |
| VPS | `config.platform.vps.yaml` | 本地/远端维护，`sync-vps.sh` 推送；密钥在 `/opt/news/.env` |

模板改了**不会**自动进真实配置。每次拉取模板变更后：

```bash
git pull
make config-diff CONFIG=config.platform.yaml   # VPS 配置同理
```

`make config-diff` 逐项列出“模板有、真实配置缺失或不同”的键，密钥字段
（webhook/token/key/secret/password）自动跳过，不会打印明文。按输出合并：

- 新增源、路由、配额、`source_health` 阈值：一般直接照抄
- 本地刻意不同的项（`force`、额外源、渠道开关、连接串）：保留
- 合并后校验 YAML，再重启对应实例

Mac 重启：

```bash
make mac-restart
```

VPS 更新配置（本地有真源时）：

```bash
scp config.platform.vps.yaml root@45.77.26.44:/opt/news/config.platform.vps.yaml
ssh root@45.77.26.44 'systemctl restart news-platform'
```

新机器/换机后本地可能没有这份真源；先从 VPS 取回再改，否则
`sync-vps.sh` 会因缺文件直接失败：

```bash
scp root@45.77.26.44:/opt/news/config.platform.vps.yaml .
make config-diff CONFIG=config.platform.vps.yaml
```

或走完整同步（会重装 cron、可选重启）：

```bash
./deploy/sync-vps.sh                # 同步 config + .env + cron + 重启
./deploy/sync-vps.sh --skip-restart # 只同步不重启
```

重启后验证：

```bash
curl -s 127.0.0.1:8081/api/health | jq .sources
curl -s 127.0.0.1:8081/api/sources/health
```

> **严禁提交** config/*.yaml 与 .env。`make config-diff` 只在本地跑，不要把输出贴到公开渠道。

## VPS 运维 cron

| 任务 | 触发 | 内容 |
|---|---|---|
| 自动构建部署 | 每 5 分钟 | `vps-build-deploy.sh`：新 commit → 构建 → 替换 → 重启 → 健康检查（失败回滚） |
| 数据备份 | 每日 03:15 | `POST /api/store/snapshot`（进程内一致性快照），保留 7 天自动删除 |
| 外部探活 | 每 5 分钟 | `health-probe.sh`：curl `/api/health/live`，**连续 3 次**失败 → 企业微信 webhook 告警（独立于平台进程） |

手动操作：

```bash
ssh root@45.77.26.44
systemctl status news-platform           # 状态
tail -f /opt/news/logs/platform.out.log  # 平台日志
tail -f /opt/news/logs/build-deploy.log  # 构建部署日志
journalctl -u news-platform -e           # systemd 侧日志
crontab -l                               # 三条 cron（构建/备份/探活）
```

## 配置

- VPS 运行配置：`config.platform.vps.yaml`；Mac 运行配置：`config.platform.yaml`
  （渠道、源调度、脚本源参数；两者都 gitignored，缺失时 `make init` 从模板生成）
- 密钥在 `.env`，`launch-vps.sh` / `launch-mac.sh` 启动时 source 进环境；
  脚本源 subprocess 同样继承
- 结构真源：`config.platform.yaml.example`（渠道用 `${VAR}` 引用 `.env`）
- source-dump 用配置：`make dump-config` 从模板生成 `config.dump.yaml`
- 模板变更同步：`make config-diff CONFIG=<真实配置>`，见上文
- 常用密钥：`WECHAT_WEBHOOK`、`DISCORD_PUSH_WEBHOOK`、`FRED_API_KEY`（宏观报告）、
  `GLM_API_KEY`（翻译/简评）、`NIMBUS_ADMIN_TOKEN`（管理 API）、
  `LONGPORT_APP_KEY` / `LONGPORT_APP_SECRET` / `LONGPORT_ACCESS_TOKEN`（港股/A股行情）

数据源全览见 nimbus-os 仓 `docs/references/news-sources.md`（news 仓源码为准，文档随源变更在 nimbus-os 更新）。

## 本地开发

```bash
cd ~/work/nimbus-news
make init                 # 生成 config.platform.yaml / .env（已存在则跳过）
make python-deps          # 建 venv 并安装 scripts 依赖（akshare/ecocal 等，见 scripts/requirements.txt）
make build-native         # 本地 go build ./cmd/platform
go run ./cmd/platform -config config.platform.yaml   # 本地起服务（8081）

make dump-config          # 生成 source-dump 用的 config.dump.yaml
make config-diff CONFIG=config.platform.yaml        # 看模板与本地配置的差异
```

本地 `config.platform.yaml` 同样 gitignored，由 `make init` 从模板生成；密钥只写 `.env`。

## 数据与生成物（不提交）

- `data/*.db`（BoltDB 平台库、market.db）
- `bin/`、`venv/`、`logs/`、`backups/`、`scripts/__pycache__/`

备份/恢复：

```bash
# VPS 上
ls /opt/news/backups/                        # 每日 03:15 的 platform.*.db.tgz
# 恢复：停服务 → 解压覆盖 data/platform.db → 启动
systemctl stop news-platform
tar xzf backups/platform.20260816.db.tgz -C /opt/news   # 恢复备份当天数据
systemctl start news-platform
```

## Digest topics v4 显式迁移

该迁移不会随平台启动自动执行。默认命令是只读 dry-run，输出仅含计数、
守恒 hash、marker 状态和预计更新数，不包含新闻正文。

先停止平台后复制数据库，或使用平台 owner-mediated snapshot 生成已验证副本，再演练：

```bash
cp data/platform.db data/platform.topics-v4.rehearsal.db
go run ./cmd/digest-migrate \
  --db data/platform.topics-v4.rehearsal.db \
  --config config.platform.vps.yaml > /tmp/topics-v4-dry-run.json

BEFORE_HASH=$(jq -r .before_hash /tmp/topics-v4-dry-run.json)
go run ./cmd/digest-migrate \
  --db data/platform.topics-v4.rehearsal.db \
  --config config.platform.vps.yaml \
  --mode apply \
  --expected-before-hash "$BEFORE_HASH"
```

确认 `before_count == after_count`、`before_hash == after_hash`、
`applied == true` 和 `marker_present == true`。再次 apply 应返回
`already_marked == true` 且不改数据。

生产执行必须先停止平台并保留原库备份；不要对仍由平台持有的 BoltDB 执行：

```bash
cp data/platform.db data/platform.pre-topics-v4.db
go run ./cmd/digest-migrate --db data/platform.db --config config.platform.vps.yaml \
  > /tmp/topics-v4-production-dry-run.json
BEFORE_HASH=$(jq -r .before_hash /tmp/topics-v4-production-dry-run.json)
go run ./cmd/digest-migrate --db data/platform.db --config config.platform.vps.yaml \
  --mode apply --expected-before-hash "$BEFORE_HASH"
```

hash 不匹配时 apply 会在写入前失败。需要回滚时，保持平台停止并用
`data/platform.pre-topics-v4.db` 替换目标库，再启动和验收；不要尝试手工删除 marker。
