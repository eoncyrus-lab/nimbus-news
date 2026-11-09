<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./assets/hero-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="./assets/hero-light.svg">
    <img src="./assets/hero-dark.svg" width="100%" alt="nimbus-news">
  </picture>
</p>

<p align="center"><sub><code>Go</code> · <code>Vue</code> · <code>LLM</code> · <code>Market Intelligence</code></sub></p>

`nimbus-news` 是一个个人投资信息聚合平台，负责拉取市场新闻、A 股聚合报告、行情快照和 LLM 点评，并通过企业微信、Discord、Telegram 等渠道推送。

## 当前结构

```text
cmd/platform/              # 唯一服务入口
cmd/source-dump/           # 逐源拉取并输出格式快照（核对用，不落库不推送）
internal/source/           # 数据源与时段简报
internal/channel/          # 推送渠道
internal/market/           # 行情适配器与缓存
internal/api/              # 管理后台 API
internal/config/           # YAML 配置加载与热更新
scripts/                   # 定时报告依赖的 Python 脚本（akshare/ecocal）
web/                       # Vue 管理后台
docs/                      # 部署、架构与维护文档
```

## 配置约定

- `config.platform.yaml.example`: 当前 15 源/2 渠道拓扑的无密钥模板
- `config.platform.yaml`: 本地运行配置，已加入 `.gitignore`
- `config.platform.vps.yaml`: VPS 运行配置（含密钥，本地为真源），已加入 `.gitignore`
- `.env.example`: 可提交的环境变量模板
- `.env`: 本地/生产密钥，已加入 `.gitignore`

首次部署：

```bash
make init                 # 生成 config.platform.yaml / .env（已存在则跳过）
```

然后只在 `.env` 里填写密钥。`config.platform.yaml` 保留 `${VAR}` 引用，不要把
webhook、token 或 API key 写成明文。服务启动时会展开这些引用；配置管理 API 的
无关更新会继续保留磁盘上的环境变量占位符。

未配置 `routing` 的源继续使用 legacy `delivery_mode` / `briefing_target`；新配置应优先使用
typed `routing`，不要在同一个源上混用两套字段。

## 双实例角色

同一份配置可以在两台机器上运行，靠 `runtime.role`（或环境变量 `NIMBUS_RUNTIME_ROLE`）
区分职责，避免两侧各自摄入、各自推送：

| 角色 | 行为 |
|---|---|
| `active` | 唯一摄入、决策、投递、生成简报 |
| `standby` | 常驻只读：不启动源、不投递、不生成简报，只提供健康检查与只读 API |
| `admin` | 只读 + 手动 `FetchOnly`（只抓不发），禁止 trigger/push |

切换方式：改角色后重启，或在 standby 上 `curl -X POST 127.0.0.1:8081/api/runtime/promote`
（管理路径需带 `NIMBUS_ADMIN_TOKEN`）。不自动选主，避免双活脑裂。

## 本地开发

```bash
make init                 # 生成 config.platform.yaml / .env
make build-native         # go build -o bin/platform ./cmd/platform
make run                  # 前台运行（默认 127.0.0.1:8081）
make test
make dev-frontend         # Vue 管理后台（localhost:3000）
```

管理 API 只监听 loopback。若设置了 `NIMBUS_ADMIN_TOKEN`，管理路径必须带
`Authorization: Bearer <token>`；本机浏览器 UI（可信 Origin）仍可直接访问。

## 部署

两条路径，都是原生二进制 + 本机守护：

**VPS（systemd，active）**

```bash
git push origin main      # VPS cron 每 5 分钟自构建；构建/重启/存活检查失败自动回滚
./deploy/sync-vps.sh      # 同步 config.platform.vps.yaml + 安装运维 cron
```

**Mac（launchd，standby）**

```bash
make mac-bootstrap        # 首次：生成 config/.env + 装 Python 依赖 + 构建 + 注册 launchd
make install-mac          # 日常更新：构建 + 重载 launchd
make mac-restart          # 只改 .env/config 时重启
make mac-status           # launchd 状态 + 健康探针
make mac-logs             # tail 本机日志
make uninstall-mac
```

Mac 默认以 `standby` 运行（`launch-mac.sh` 在 `.env` 未指定角色时注入），
不摄入、不推送，只提供只读 API；要改成 `admin`/`active` 写进 `.env` 后 `make mac-restart`。
首次部署、日志路径和排障见 [部署说明](docs/deployment.md#maclaunchdstandby)。

部署脚本的健康回滚只检查 `/api/health/live`（进程级）。`/api/health` 是就绪探针，
digest 积压时会返回 503，不能用来判定版本好坏。

## Python 脚本依赖

定时报告会调用 `scripts/` 下的 Python 脚本（akshare/ecocal 等）。先建 venv：

```bash
make python-deps                        # 默认 python3
make python-deps PYTHON=python3.12      # 指定解释器（推荐 3.12）
```

`.venv/bin` 已由启动脚本前置到 `PATH`，脚本源 subprocess 会自动使用该解释器。
`make mac-bootstrap` 会一并安装这些依赖。

## 常用命令

```bash
make help
make build-native
make test
make health
make backup               # 进程内一致性快照（不要直接抄运行中的 Bolt 文件）
make restore FILE=backups/platform_xxx.db
```

## 文档入口

- [部署说明](docs/deployment.md)
- [架构方案（2026-11-09）](docs/architecture-v2-2026-11-09.md)
- [数据源输出格式](docs/source-output-format.md)
- [文档索引](docs/README.md)
