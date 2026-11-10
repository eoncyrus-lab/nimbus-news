# 部署说明

## 拓扑

- **代码仓**：`nimbus-news`（唯一 git 仓库；nimbus-os 持 datasources/guanfu 模块，经 go.mod replace 引用）
- **运行实例**：同一时刻只能有一个 `active` 实例（唯一摄入/推送），其余为 `standby`/`admin` 只读。
  当前默认由 Mac 承担 `active`，`deploy/launch-mac.sh` 注入 `NIMBUS_RUNTIME_ROLE=active`。
- **进程**：`./bin/platform -config config.platform.yaml`，API 仅监听 127.0.0.1:8081
- **守护**：本机 launchd（`com.news.platform`）；其他 Linux 主机用 systemd，见下文「通用部署方案」。
- 双活会重复推送：启用另一实例前，先把当前实例的角色在 `.env` 里退回 `standby`/`admin` 并重启。

## Mac（launchd，active 长期推送）

Mac 是常驻的**推送实例**：负责摄入、投递与生成简报。

### 前置

- macOS + Go 1.26+（`go version`）
- Python 3.12（`make python-deps` 默认用 `python3.12`；3.14 上 akshare 等包无预编译 wheel）
- 仓库路径固定：plist 渲染时会写入绝对路径，移动仓库后必须重新 `make install-mac`

### 首次部署

```bash
make mac-bootstrap          # 生成 config/.env → 建 .venv 装依赖 → 构建 → 注册 launchd
$EDITOR .env                # 填 webhook/API key；角色默认 active（长期推送）
make mac-status             # 看 launchd 状态 + /api/health/live + /api/health
```

`make mac-bootstrap` 等价于 `make init` + `make python-deps` + `make install-mac`，
已有 `config.platform.yaml`、`.env` 不会被覆盖，可以重复执行。日常 `make install-mac`
也会先跑 `make init`：配置文件缺失时自动按模板生成，已有文件保持不动。

真实配置不入库：`config.platform.yaml` 含部署差异，仓库只保留
`config.platform.yaml.example`；密钥统一写进 `.env`。

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

- `deploy/launch-mac.sh` 在 `.env` 未设置 `NIMBUS_RUNTIME_ROLE` 时注入 `active`
  （长期推送）。要退回只读，在 `.env` 写 `standby`/`admin` 后 `make mac-restart`。
- 临时接管：`curl -X POST 127.0.0.1:8081/api/runtime/promote`
  （配置了 `NIMBUS_ADMIN_TOKEN` 时带 `Authorization: Bearer <token>`）；
  重启后回到 `.env` 里的角色。
- `NIMBUS_CONFIG` 可覆盖启动配置文件名，默认 `config.platform.yaml`。

### AI 过滤与推送

**未配置 AI 不得影响推送。** `config.platform.yaml.example` 里
`filters.ai_filter.enabled` 默认 `false`，各源 `routing.ai_policy` 统一为 `bypass`；
`ai_policy: required` 的源在 AI 关闭时也按默认路由走，不会静默丢弃。
代码侧保障：LLM 未配置时不挂 AI 过滤器；`enabled=false` 时 `ShouldFilter` 直接放行、
`EvaluateTyped` 返回 `disabled`（不拦截）；LLM 报错、限流、解析失败一律 fail-open。
日志会明确打印「not gated by AI」，不会误报成开启拦截。

确需启用 AI 门禁时：先确认 LLM 可用，把 `enabled` 改为 `true` 并将对应源改为
`ai_policy: required`，再观察 `ai_filter blocked` 日志量后决定是否上线。

### 出网代理

企业微信 webhook 建议直连：代理出口 IP 被多方共用时会触发 `45009 Too many requests`。
配了 `HTTP_PROXY`/`HTTPS_PROXY` 时，把 `qyapi.weixin.qq.com` 写进 `NO_PROXY`。

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

### 源健康（各实例通用）

`GET /api/sources/health` 返回逐源状态；`/api/health` 里带 `sources` 汇总计数。
间隔 ≤5 分钟的源连续 6 次抓取 0 条会降级（`source_health.empty_degraded_threshold`），
用于发现上游停更、RSS 换地址或源内过滤过严——这些情况 Fetch 不会报错。
降级只写状态和日志；`notify_channels` 为空时不推送。

## 通用部署方案（Linux / VPS）

仓库不绑定任何具体主机、IP 或部署目录。迁移到任意 Linux 主机时按下面步骤自建，
把 `<DEPLOY_DIR>`、`<USER>`、`<HOST>` 替换成自己的值。

### 1. 准备

- Go 1.26+、Python 3.12、git；私有仓认证自备（deploy key 或个人令牌）
- 目录建议：源码仓 `<DEPLOY_DIR>/src/nimbus-news`，二进制 `<DEPLOY_DIR>/bin/platform`
- 部署 key 用只读权限；不要把 `.env` 或真实配置提交进仓库

### 2. 配置

```bash
make init                                    # 生成 config.platform.yaml / .env（已存在则跳过）
make python-deps                             # 建 .venv 装 scripts/requirements.txt
$EDITOR .env                                 # 填密钥；NIMBUS_RUNTIME_ROLE 决定本机角色
make config-diff CONFIG=config.platform.yaml # 模板变更后逐项合并
```

真实配置与 `.env` 不入库。模板变更**不会**自动进真实配置，每次拉取后手动合并。

### 3. 构建

```bash
make build-native        # 产出 bin/platform
```

### 4. 守护进程

用 systemd 托管（`Type=simple`），要点：

- `WorkingDirectory=<DEPLOY_DIR>`，`ExecStart=<DEPLOY_DIR>/bin/platform -config <DEPLOY_DIR>/config.platform.yaml`
- 由进程环境或启动包装脚本 `source .env`，固定 `TZ=Asia/Shanghai`
- `Restart=always`；stdout/stderr 落到 `<DEPLOY_DIR>/logs/`
- PATH 前置 `<DEPLOY_DIR>/.venv/bin`，让脚本源 subprocess 用带依赖的 python

**先确认角色**：本机设 `NIMBUS_RUNTIME_ROLE=active` 前，先把原 active 实例改为 `standby`，
否则双活重复推送。

### 5. 更新与验证

```bash
git pull && make build-native && <重启守护进程>
curl -s 127.0.0.1:8081/api/health/live     # 存活（进程级）
curl -s 127.0.0.1:8081/api/health | jq .sources  # 就绪（digest 积压会 503，别拿它判版本好坏）
curl -s 127.0.0.1:8081/api/sources/health
```

自动部署（可选）：在主机上放一个定时任务，`git fetch` 对比已部署 commit，
有更新才构建 → 原子替换二进制 → 重启 → 轮询 `/api/health/live`，失败回滚上一个二进制。
`config.platform.yaml` 与 `.env` 不随代码自动更新，需人工合并。

### 6. 备份

`POST /api/store/snapshot`（带 `NIMBUS_ADMIN_TOKEN`）生成进程内一致性快照，
运行中不要直接拷 `data/platform.db`（可能拿到撕裂副本）。定期归档、保留 N 份即可。

## 配置变更流程（模板入库，实例配置不入库）

仓库只保留 `config.platform.yaml.example`。真实配置含部署差异和密钥，留在各自机器：

| 实例 | 配置文件 | 维护方式 |
|---|---|---|
| 任意实例 | `config.platform.yaml` | `make init` 从模板生成；密钥写 `.env` |

模板改了**不会**自动进真实配置。每次拉取模板变更后：

```bash
git pull
make config-diff CONFIG=config.platform.yaml
```

`make config-diff` 逐项列出“模板有、真实配置缺失或不同”的键，密钥字段
（webhook/token/key/secret/password）自动跳过，不会打印明文。按输出合并：

- 新增源、路由、配额、`source_health` 阈值：一般直接照抄
- 本地刻意不同的项（`force`、额外源、渠道开关、连接串）：保留
- 合并后校验 YAML，再重启对应实例

> **严禁提交** config/*.yaml 与 .env。`make config-diff` 只在本地跑，不要把输出贴到公开渠道。

## 配置

- 运行配置：`config.platform.yaml`（渠道、源调度、脚本源参数；gitignored）
- 密钥在 `.env`，启动时 source 进环境；脚本源 subprocess 同样继承
- 结构真源：`config.platform.yaml.example`（渠道用 `${VAR}` 引用 `.env`）
- source-dump 用配置：`make dump-config` 从模板生成 `config.dump.yaml`
- 常用密钥：`WECHAT_WEBHOOK`、`DISCORD_PUSH_WEBHOOK`、`FRED_API_KEY`（宏观报告）、
  `GLM_API_KEY`（翻译/简评）、`NIMBUS_ADMIN_TOKEN`（管理 API）

数据源全览见 nimbus-os 仓 `docs/references/news-sources.md`（news 仓源码为准，文档随源变更在 nimbus-os 更新）。

## 本地开发

```bash
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
- `bin/`、`.venv/`、`logs/`、`backups/`、`scripts/__pycache__/`

## Digest topics v4 显式迁移

该迁移不会随平台启动自动执行。默认命令是只读 dry-run，输出仅含计数、
守恒 hash、marker 状态和预计更新数，不包含新闻正文。

先停止平台后复制数据库，或使用平台 owner-mediated snapshot 生成已验证副本，再演练：

```bash
cp data/platform.db data/platform.topics-v4.rehearsal.db
go run ./cmd/digest-migrate \
  --db data/platform.topics-v4.rehearsal.db \
  --config config.platform.yaml > /tmp/topics-v4-dry-run.json

BEFORE_HASH=$(jq -r .before_hash /tmp/topics-v4-dry-run.json)
go run ./cmd/digest-migrate \
  --db data/platform.topics-v4.rehearsal.db \
  --config config.platform.yaml \
  --mode apply \
  --expected-before-hash "$BEFORE_HASH"
```

确认 `before_count == after_count`、`before_hash == after_hash`、
`applied == true` 和 `marker_present == true`。再次 apply 应返回
`already_marked == true` 且不改数据。

生产执行必须先停止平台并保留原库备份；不要对仍由平台持有的 BoltDB 执行：

```bash
cp data/platform.db data/platform.pre-topics-v4.db
go run ./cmd/digest-migrate --db data/platform.db --config config.platform.yaml \
  > /tmp/topics-v4-production-dry-run.json
BEFORE_HASH=$(jq -r .before_hash /tmp/topics-v4-production-dry-run.json)
go run ./cmd/digest-migrate --db data/platform.db --config config.platform.yaml \
  --mode apply --expected-before-hash "$BEFORE_HASH"
```

hash 不匹配时 apply 会在写入前失败。需要回滚时，保持平台停止并用
`data/platform.pre-topics-v4.db` 替换目标库，再启动和验收；不要尝试手工删除 marker。
