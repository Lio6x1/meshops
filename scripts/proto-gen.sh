#!/bin/bash
# ============================================================================
# scripts/proto-gen.sh - 方案A proto 代码生成脚本
#
# 策略：
#   共享类型（common/v1）用 protoc 生成到 gen/；
#   各中心端服务用 goctl 生成 zRPC 骨架，生成后修复 goctl 1.10.x 的两类别名 bug：
#     1. service 的 gen 包别名：v1_xxxv1 → xxxv1
#     2. 引用了共享类型的 logic/client 文件：import common/v1 而代码用 xxxv1 别名
#
# 用法：
#   bash scripts/proto-gen.sh          # 全量生成
#   bash scripts/proto-gen.sh ingest   # 仅重新生成指定服务
# ============================================================================

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROTO_DIR="$ROOT/proto"
GEN_DIR="$ROOT/gen"
APP_DIR="$ROOT/app"

GREEN='\033[0;32m'; NC='\033[0m'
log() { echo -e "${GREEN}[proto-gen]${NC} $1"; }

# ============================================================================
# 修复函数：接受服务目录、正确别名、gen包路径
# ============================================================================
fix_service_aliases() {
  local svc_app_dir="$1"   # app/ingest
  local pkg_alias="$2"     # ingestv1
  local gen_import="$3"    # github.com/Lio6x1/meshops/gen/ingest/v1
  local wrong_alias="v1_${pkg_alias}"   # v1_ingestv1（goctl bug 产生的错误别名）

  # 修复 1：service 自身的 gen 包别名错误（v1_xxxv1 → xxxv1）
  find "$svc_app_dir" -name "*.go" | while read -r f; do
    if grep -q "\"$gen_import\"\|${wrong_alias}\." "$f" 2>/dev/null; then
      sed -i "s|\"$gen_import\"|${pkg_alias} \"$gen_import\"|g" "$f"
      sed -i "s/${wrong_alias}\./${pkg_alias}./g" "$f"
    fi
  done

  # 修复 2：goctl 把 common/v1 import 写进 logic/client 但代码用服务包别名
  find "$svc_app_dir" -name "*.go" | while read -r f; do
    if grep -q '"github.com/Lio6x1/meshops/gen/common/v1"' "$f" 2>/dev/null; then
      sed -i "s|\"github.com/Lio6x1/meshops/gen/common/v1\"|${pkg_alias} \"${gen_import}\"|g" "$f"
      sed -i "s/commonv1\./${pkg_alias}./g" "$f"
    fi
  done
}

# ============================================================================
# 生成共享类型（protoc，不用 goctl）
# common proto 末尾有占位空 service，使 goctl pb/grpc 一致性检查通过
# ============================================================================
gen_common() {
  log "生成共享类型 gen/common/v1/ ..."
  mkdir -p "$GEN_DIR"
  cd "$PROTO_DIR"
  protoc --proto_path=. \
    --go_out="$GEN_DIR"      --go_opt=paths=source_relative \
    --go-grpc_out="$GEN_DIR" --go-grpc_opt=paths=source_relative \
    common/v1/entity.proto common/v1/task.proto
  log "✓ gen/common/v1/ 已生成"
}

# ============================================================================
# 生成单个服务骨架
# ============================================================================
gen_service() {
  local proto_rel="$1"  # e.g. ingest/v1/ingest.proto
  local svc="$2"        # e.g. ingest
  local alias="$3"      # e.g. ingestv1

  log "生成 $svc 服务骨架 ..."
  cd "$PROTO_DIR"
  goctl rpc protoc "$proto_rel" \
    --proto_path=. \
    --go_out="$GEN_DIR"      --go_opt=paths=source_relative \
    --go-grpc_out="$GEN_DIR" --go-grpc_opt=paths=source_relative \
    --zrpc_out="$APP_DIR/$svc" -m

  fix_service_aliases "$APP_DIR/$svc" "$alias" \
    "github.com/Lio6x1/meshops/gen/$svc/v1"
  log "✓ $svc 骨架生成并修复完成"
}

# ============================================================================
# 主流程
# ============================================================================
TARGET="${1:-all}"

case "$TARGET" in
  common)
    gen_common ;;
  ingest)
    gen_service "ingest/v1/ingest.proto"         "ingest"     "ingestv1" ;;
  entity)
    gen_service "entity/v1/entity.proto"         "entity"     "entityv1" ;;
  task)
    gen_service "task/v1/task.proto"             "task"       "taskv1"   ;;
  dispatcher)
    gen_service "dispatcher/v1/dispatcher.proto" "dispatcher" "dispatcherv1" ;;
  all)
    gen_common
    gen_service "ingest/v1/ingest.proto"         "ingest"     "ingestv1"
    gen_service "entity/v1/entity.proto"         "entity"     "entityv1"
    gen_service "task/v1/task.proto"             "task"       "taskv1"
    gen_service "dispatcher/v1/dispatcher.proto" "dispatcher" "dispatcherv1"
    ;;
  *)
    echo "未知目标: $TARGET"; echo "用法: $0 [common|ingest|entity|task|dispatcher|all]"; exit 1 ;;
esac

log ""
log "生成完成，运行 go mod tidy && go build ./... 验证"
