#!/bin/bash

# ============================================================================
# 实时订阅扇出压测 - 20/50/200 订阅者
# 用途：验证实时订阅在不同扇出规模下的延迟、队列深度和慢消费者处理
# ============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# 订阅者数量阶梯
SUBSCRIBER_LEVELS=(20 50 200)
# 后台状态更新速率
UPDATE_RATE=2000             # events/s
DURATION_PER_LEVEL=120       # 每档持续 2 分钟

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}======================================${NC}"
echo -e "${GREEN}实时订阅扇出压测${NC}"
echo -e "${GREEN}======================================${NC}"
echo ""
echo "订阅者阶梯: ${SUBSCRIBER_LEVELS[*]}"
echo "状态更新速率: $UPDATE_RATE events/s"
echo "每档持续: $DURATION_PER_LEVEL 秒"
echo ""

mkdir -p "$PROJECT_ROOT/benchmark-results"
RESULT_FILE="$PROJECT_ROOT/benchmark-results/fanout-result-$(date +%Y%m%d-%H%M%S).json"

echo "{" > "$RESULT_FILE"
echo "  \"test_config\": { \"update_rate\": $UPDATE_RATE, \"duration_per_level\": $DURATION_PER_LEVEL }," >> "$RESULT_FILE"
echo "  \"levels\": [" >> "$RESULT_FILE"

for i in "${!SUBSCRIBER_LEVELS[@]}"; do
  LEVEL=${SUBSCRIBER_LEVELS[$i]}
  echo -e "${YELLOW}[档位 $((i+1))/${#SUBSCRIBER_LEVELS[@]}] $LEVEL 个订阅者${NC}"

  # TODO: 启动后台状态更新负载
  # ./bin/gateway-simulator --rate $UPDATE_RATE --entity-pool 10000 --target localhost:50051 &
  # LOAD_PID=$!

  # TODO: 启动 N 个订阅者
  # ./bin/subscriber-loadgen --count $LEVEL --target localhost:50052 --duration $DURATION_PER_LEVEL

  echo "  运行中... (TODO: 实现订阅者负载生成器)"
  # sleep $DURATION_PER_LEVEL

  # kill $LOAD_PID 2>/dev/null || true

  # 记录本档结果
  SEP=","
  if [ $i -eq $((${#SUBSCRIBER_LEVELS[@]} - 1)) ]; then SEP=""; fi
  cat >> "$RESULT_FILE" << EOF
    {
      "subscribers": $LEVEL,
      "visible_p99_ms": "TODO",
      "max_queue_depth": "TODO",
      "merged_updates": "TODO",
      "slow_consumer_drops": "TODO",
      "cpu_percent": "TODO",
      "network_mbps": "TODO"
    }$SEP
EOF

  echo "  ✓ 档位 $LEVEL 完成"
  echo ""
done

echo "  ]," >> "$RESULT_FILE"
echo "  \"timestamp\": \"$(date -Iseconds)\"" >> "$RESULT_FILE"
echo "}" >> "$RESULT_FILE"

echo -e "${GREEN}扇出压测完成，结果：$RESULT_FILE${NC}"
echo -e "${YELLOW}注意：当前为框架模板，需实现订阅者负载生成器${NC}"
