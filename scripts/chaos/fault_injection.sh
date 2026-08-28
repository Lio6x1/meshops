#!/bin/bash

# ============================================================================
# 故障演练脚本 - 依赖故障注入与恢复
# 用途：验证 Kafka/Redis/MySQL/消费者 故障时的系统行为和恢复
# 对应文档：production-readiness-checklist.md 第8节 依赖故障矩阵
# ============================================================================

set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

usage() {
  echo "用法: $0 <scenario>"
  echo ""
  echo "可用场景："
  echo "  kafka-down      Kafka Broker 停机恢复（预期：Ingest 快速失败，不返回虚假 accepted）"
  echo "  redis-down      Redis 停机恢复（预期：查询明确降级，不无限回源 MySQL）"
  echo "  mysql-down      MySQL 停机恢复（预期：任务写失败，状态事件仍进 Kafka）"
  echo "  projector-kill  强杀状态投影器（预期：Lag 增长，恢复后从已提交位点续传并幂等）"
  echo "  dispatcher-kill 强杀任务分发器（预期：任务事件保留，恢复后继续下发）"
  echo "  redis-flush     清空 Redis（预期：从 Kafka 保留窗口重建快照）"
  echo "  all             依次执行全部场景"
  exit 1
}

[ $# -lt 1 ] && usage
SCENARIO=$1

# 观察窗口
OBSERVE_BEFORE=30    # 故障前基线观察
FAULT_DURATION=60    # 故障持续时间
OBSERVE_AFTER=180    # 恢复后观察

log() { echo -e "${GREEN}[$(date +%H:%M:%S)]${NC} $1"; }
warn() { echo -e "${YELLOW}[$(date +%H:%M:%S)]${NC} $1"; }

# ----------------------------------------------------------------------------
# 场景实现
# ----------------------------------------------------------------------------

scenario_kafka_down() {
  log "=== 场景：Kafka Broker 停机恢复 ==="
  warn "预期行为：接入服务拒绝新事件并快速失败，绝不返回虚假 accepted；任务保留在 Outbox"

  log "1. 记录基线（观察 ${OBSERVE_BEFORE}s）..."
  # TODO: 抓取 Prometheus 基线指标

  log "2. 停止 Kafka..."
  docker compose stop kafka

  log "3. 故障期观察（${FAULT_DURATION}s）..."
  warn "   请确认：ingest 返回 KAFKA_UNAVAILABLE，无虚假 accepted"
  warn "   请确认：Outbox 待发布数量增长但不丢失"
  sleep "$FAULT_DURATION"

  log "4. 恢复 Kafka..."
  docker compose start kafka
  sleep 10  # 等待健康

  log "5. 恢复期观察（${OBSERVE_AFTER}s）..."
  warn "   请确认：Outbox 积压被消化，任务事件正常投递"
  warn "   请确认：无重复任务执行（消费者幂等）"

  log "✓ Kafka 故障演练完成"
}

scenario_redis_down() {
  log "=== 场景：Redis 停机恢复 ==="
  warn "预期行为：Kafka 继续保留状态事件；最新状态查询明确降级，不无限回源 MySQL"

  log "1. 记录基线..."
  log "2. 停止 Redis..."
  docker compose stop redis

  log "3. 故障期观察（${FAULT_DURATION}s）..."
  warn "   请确认：查询返回明确降级响应（REDIS_UNAVAILABLE），不打垮 MySQL"
  warn "   请确认：状态事件仍进入 Kafka，投影器 Lag 增长"
  sleep "$FAULT_DURATION"

  log "4. 恢复 Redis..."
  docker compose start redis
  sleep 5

  log "5. 恢复期观察（${OBSERVE_AFTER}s）..."
  warn "   请确认：快照从 Kafka 积压重建，查询恢复正常"

  log "✓ Redis 故障演练完成"
}

scenario_mysql_down() {
  log "=== 场景：MySQL 停机恢复 ==="
  warn "预期行为：任务写快速失败；状态事件仍进 Kafka，历史抽样积压或暂停"

  docker compose stop mysql
  warn "   请确认：任务创建返回 MYSQL_UNAVAILABLE，无虚假成功"
  warn "   请确认：状态事件链路不受影响"
  sleep "$FAULT_DURATION"

  docker compose start mysql
  sleep 10
  warn "   请确认：历史抽样积压恢复写入"

  log "✓ MySQL 故障演练完成"
}

scenario_projector_kill() {
  log "=== 场景：强杀状态投影器 ==="
  warn "预期行为：Kafka 产生 Lag；恢复后从已提交位点续传，版本检查保证幂等"

  # TODO: kill -9 投影器进程
  warn "TODO: 强杀投影器进程（kill -9），模拟处理中崩溃"
  warn "   请确认：崩溃时未提交的 Offset 会被重新消费"
  warn "   请确认：重复消费不破坏 Redis 快照（版本比较幂等）"

  log "✓ 投影器故障演练完成"
}

scenario_dispatcher_kill() {
  log "=== 场景：强杀任务分发器 ==="
  warn "预期行为：任务事件保留；恢复后继续消费，执行端幂等防止重复执行"

  warn "TODO: 强杀分发器进程"
  warn "   请确认：任务不丢失，恢复后继续下发"
  warn "   请确认：task_id+attempt 幂等防止重复执行"

  log "✓ 分发器故障演练完成"
}

scenario_redis_flush() {
  log "=== 场景：清空 Redis 后重建 ==="
  warn "预期行为：从 Kafka 保留窗口重放，在新命名空间重建快照并原子切换"

  log "1. 记录当前实体数量..."
  # docker compose exec redis redis-cli DBSIZE

  log "2. 清空 Redis..."
  docker compose exec -T redis redis-cli FLUSHDB

  log "3. 触发重建（观察 ${OBSERVE_AFTER}s）..."
  warn "   请确认：重建期间查询明确降级"
  warn "   请确认：从 Kafka 重放重建 10万实体，记录恢复时间（目标 <10 分钟）"
  warn "   请确认：重建完成后原子切换命名空间"

  log "✓ Redis 重建演练完成"
}

# ----------------------------------------------------------------------------
# 分发
# ----------------------------------------------------------------------------

case "$SCENARIO" in
  kafka-down)      scenario_kafka_down ;;
  redis-down)      scenario_redis_down ;;
  mysql-down)      scenario_mysql_down ;;
  projector-kill)  scenario_projector_kill ;;
  dispatcher-kill) scenario_dispatcher_kill ;;
  redis-flush)     scenario_redis_flush ;;
  all)
    scenario_kafka_down
    scenario_redis_down
    scenario_mysql_down
    scenario_projector_kill
    scenario_dispatcher_kill
    scenario_redis_flush
    ;;
  *) usage ;;
esac

echo ""
warn "注意：演练脚本负责编排故障注入；指标观察和断言需结合 Grafana/Prometheus 和服务日志完成。"
warn "每次演练必须保存原始指标截图和日志作为验证证据（见 production-readiness-checklist.md）。"
