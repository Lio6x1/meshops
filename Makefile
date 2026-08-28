# ============================================================================
# MeshOps - 多源实体实时协同与可靠任务调度平台
# ============================================================================

.PHONY: help
help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ============================================================================
# 依赖与环境
# ============================================================================

.PHONY: deps
deps: ## 下载 Go 依赖
	go mod download
	go mod verify

.PHONY: tools
tools: ## 安装开发工具
	go install honnef.co/go/tools/cmd/staticcheck@latest
	go install github.com/zeromicro/go-zero/tools/goctl@latest

# ============================================================================
# Protobuf
# ============================================================================

.PHONY: proto
proto: ## 生成所有服务的 Protobuf 代码（共享类型用protoc，服务骨架用goctl）
	bash scripts/proto-gen.sh all
	go mod tidy

.PHONY: proto-%
proto-%: ## 生成指定服务（make proto-ingest / proto-entity / proto-task / proto-dispatcher / proto-common）
	bash scripts/proto-gen.sh $*

.PHONY: proto-lint
proto-lint: ## 检查 Protobuf 规范
	buf lint proto

.PHONY: proto-breaking
proto-breaking: ## 检查 Protobuf 破坏性变更
	buf breaking proto --against '.git#branch=main,subdir=proto'

# ============================================================================
# 构建
# ============================================================================

.PHONY: build
build: ## 构建所有服务
	go build ./app/ingest ./app/entity ./app/task ./app/dispatcher

# ============================================================================
# 测试
# ============================================================================

.PHONY: test
test: ## 运行单元测试
	go test ./... -count=1

.PHONY: test-race
test-race: ## 运行竞态检测
	go test -race ./... -count=1

.PHONY: test-integration
test-integration: ## 运行集成测试（需要 docker compose up）
	go test ./... -tags=integration -count=1 -timeout=10m

.PHONY: cover
cover: ## 生成覆盖率报告
	go test ./... -coverprofile=coverage.out -covermode=atomic
	go tool cover -html=coverage.out -o coverage.html
	@echo "覆盖率报告：coverage.html"

# ============================================================================
# 代码质量
# ============================================================================

.PHONY: vet
vet: ## 运行 go vet
	go vet ./...

.PHONY: lint
lint: ## 运行 staticcheck
	staticcheck ./...

.PHONY: check
check: vet lint test-race ## 运行所有检查（提交前）

# ============================================================================
# 本地环境
# ============================================================================

.PHONY: up
up: ## 启动基础依赖（P0-P2）
	docker compose up -d kafka redis mysql

.PHONY: up-p3
up-p3: ## 启动 P3 环境（含 etcd）
	docker compose --profile p3 up -d

.PHONY: up-all
up-all: ## 启动完整环境（含监控）
	docker compose --profile p3 --profile monitoring up -d

.PHONY: down
down: ## 停止所有服务
	docker compose down

.PHONY: clean
clean: ## 停止并清理数据卷（慎用）
	docker compose down -v

.PHONY: ps
ps: ## 查看服务状态
	docker compose ps

.PHONY: logs
logs: ## 查看日志（用法：make logs SVC=kafka）
	docker compose logs -f $(SVC)

# ============================================================================
# 数据库
# ============================================================================

.PHONY: migrate
migrate: ## 应用数据库迁移
	@echo "应用迁移到本地 MySQL..."
	@for f in migrations/*.sql; do \
		echo "Applying $$f..."; \
		mysql -h 127.0.0.1 -u root -pmeshops_dev_root meshops < $$f; \
	done

# ============================================================================
# 压测
# ============================================================================

.PHONY: bench-steady
bench-steady: ## 稳态压测 5k events/s
	bash scripts/benchmark/steady_state_5k.sh

.PHONY: bench-burst
bench-burst: ## 突发压测 50k events/s
	bash scripts/benchmark/burst_50k.sh
