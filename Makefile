# ============================================
# nimbus-news - 构建与运维命令
# ============================================
#   make help         查看所有命令
#   make build-native 构建 ./bin/platform
#   make install-mac  构建并注册 launchd（本机）
#   make deploy-vps   同步配置并安装 VPS cron（本地运行）
# ============================================

.PHONY: help init build-native run test vet deploy-vps install-mac uninstall-mac \
        mac-bootstrap mac-restart mac-status mac-logs dump-config \
        backup restore python-deps dev-backend dev-frontend health smoke-longbridge

.DEFAULT_GOAL := help

GREEN  := \033[0;32m
YELLOW := \033[0;33m
BLUE   := \033[0;34m
RED    := \033[0;31m
NC     := \033[0m

CONFIG ?= config.platform.yaml
ENV_FILE ?= .env
PYTHON ?= python3
API_URL ?= http://127.0.0.1:8081
MAC_LABEL := com.news.platform
ADMIN_AUTH := $(if $(NIMBUS_ADMIN_TOKEN),-H "Authorization: Bearer $(NIMBUS_ADMIN_TOKEN)")

BACKUP_DIR := ./backups
TIMESTAMP := $(shell date +%Y%m%d_%H%M%S)

help:
	@echo ""
	@echo "$(BLUE)nimbus-news - 构建与运维$(NC)"
	@echo ""
	@echo "$(GREEN)构建:$(NC)"
	@echo "  make init           - 从模板生成本地配置/环境变量"
	@echo "  make dump-config    - 生成 source-dump 用的 config.dump.yaml"
	@echo "  make build-native   - go build -o bin/platform ./cmd/platform"
	@echo "  make test           - go test ./..."
	@echo "  make vet            - go vet ./..."
	@echo ""
	@echo "$(GREEN)部署:$(NC)"
	@echo "  make mac-bootstrap  - 首次：生成配置 + 装 Python 依赖 + 构建 + 注册 launchd"
	@echo "  make install-mac    - 构建并重载 launchd（日常更新）"
	@echo "  make mac-restart    - 不构建，只重启（改完 .env/config 后）"
	@echo "  make mac-status     - launchd 状态 + 存活/就绪探针"
	@echo "  make mac-logs       - tail 本机平台日志"
	@echo "  make uninstall-mac  - 卸载 launchd agent"
	@echo "  make deploy-vps     - 同步配置 + 安装 VPS cron（本地运行）"
	@echo ""
	@echo "$(GREEN)运维:$(NC)"
	@echo "  make health         - 检查 /api/health/live 与就绪状态"
	@echo "  make backup         - 用进程内一致性快照备份 Bolt 库"
	@echo "  make restore FILE=  - 停服后恢复备份"
	@echo "  make run            - 直接前台运行（调试用）"
	@echo ""

init:
	@if [ ! -f "$(CONFIG)" ]; then cp config.platform.yaml.example "$(CONFIG)"; echo "$(GREEN)✓ 已创建 $(CONFIG)$(NC)"; else echo "$(YELLOW)$(CONFIG) 已存在，跳过$(NC)"; fi
	@if [ ! -f "$(ENV_FILE)" ]; then cp .env.example "$(ENV_FILE)"; echo "$(GREEN)✓ 已创建 $(ENV_FILE)$(NC)"; else echo "$(YELLOW)$(ENV_FILE) 已存在，跳过$(NC)"; fi
	@mkdir -p data

# source-dump 的本地配置从模板生成，避免和 config.platform.yaml.example 漂移。
dump-config:
	@if [ -x .venv/bin/python ]; then .venv/bin/python scripts/make_dump_config.py; else $(PYTHON) scripts/make_dump_config.py; fi

build-native:
	@echo "$(BLUE)>>> go build platform...$(NC)"
	go build -o bin/platform ./cmd/platform

run:
	go run ./cmd/platform -config "$(CONFIG)"

test:
	go test ./...

vet:
	go vet ./...

# 首次部署：配置/密钥模板 → Python 依赖 → 构建 → launchd。
# 已有文件不会被覆盖，可重复执行。
mac-bootstrap: init python-deps build-native
	./deploy/install-launchd.sh

# 日常更新也先跑 init：缺配置时自动从模板生成，已有文件不会被覆盖。
install-mac: init build-native
	./deploy/install-launchd.sh

# 只改 .env 或 config.platform.yaml 时无需重新构建。
mac-restart:
	launchctl kickstart -k gui/$$(id -u)/$(MAC_LABEL)
	@echo "$(GREEN)✓ 已重启 $(MAC_LABEL)$(NC)"

mac-status:
	@echo "launchd:"
	@launchctl print gui/$$(id -u)/$(MAC_LABEL) 2>&1 | grep -E "state = |pid = |last exit code" || echo "  (未加载)"
	@echo "存活:"
	@curl -sf -m 5 $(API_URL)/api/health/live && echo "" || echo " $(RED)✗ 进程无响应$(NC)"
	@echo "就绪:"
	@curl -s -m 5 $(API_URL)/api/health || true
	@echo ""

mac-logs:
	tail -f $(HOME)/Library/Logs/news-platform.out.log $(HOME)/Library/Logs/news-platform.err.log

uninstall-mac:
	./deploy/install-launchd.sh --uninstall

deploy-vps:
	./deploy/sync-vps.sh $(ARGS)

health:
	@echo "存活:"
	@curl -sf -m 5 $(API_URL)/api/health/live || echo " $(RED)✗ 进程无响应$(NC)"
	@echo ""
	@echo "就绪:"
	@curl -s -m 5 $(API_URL)/api/health || true

# 运行中直接 tar db 文件可能拿到撕裂副本，必须走进程内快照接口。
backup:
	@mkdir -p $(BACKUP_DIR)
	@curl -sf -m 60 $(ADMIN_AUTH) -X POST $(API_URL)/api/store/snapshot -o $(BACKUP_DIR)/platform_$(TIMESTAMP).db
	@test -s $(BACKUP_DIR)/platform_$(TIMESTAMP).db && echo "$(GREEN)✓ 快照: $(BACKUP_DIR)/platform_$(TIMESTAMP).db$(NC)"

restore:
	@if [ -z "$(FILE)" ]; then echo "$(RED)请指定备份文件: make restore FILE=backups/platform_xxx.db$(NC)"; exit 1; fi
	@if pgrep -f "bin/platform" >/dev/null; then echo "$(RED)先停止平台进程再恢复$(NC)"; exit 1; fi
	@cp "$(FILE)" data/platform.db
	@echo "$(GREEN)✓ 已恢复 $(FILE)$(NC)"

python-deps:
	$(PYTHON) -m venv .venv
	./.venv/bin/python -m pip install --quiet --upgrade pip
	./.venv/bin/pip install -r scripts/requirements.txt

dev-backend:
	go run ./cmd/platform -config "$(CONFIG)"

dev-frontend:
	@cd web && npm run dev

smoke-longbridge:
	@bash scripts/longbridge_smoke.sh
