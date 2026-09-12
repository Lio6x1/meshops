// Package testsupport 提供破坏性故障操作，其作用范围仅限于
// 专用参考 Compose 项目中可丢弃的集成测试主题。
package testsupport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func TruncateKafka(ctx context.Context, topic string, partition int, offset int64) error {
	if !strings.HasPrefix(topic, "retention_it_") && !strings.HasPrefix(topic, "state_it_") {
		return fmt.Errorf("refusing non-test topic")
	}
	compose, err := filepath.Abs("../../docker-compose.yml")
	if err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "partitions": []any{map[string]any{"topic": topic, "partition": partition, "offset": offset}}})
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", compose, "exec", "-T", "kafka", "/opt/kafka/bin/kafka-delete-records.sh", "--bootstrap-server", "kafka:29092", "--offset-json-file", "/dev/stdin")
	cmd.Stdin = bytes.NewReader(raw)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("DeleteRecords: %w: %s", err, output)
	}
	// CLI 即使以零状态码退出，也可能输出分区错误；调用者还需验证
	// broker 更新后的低水位，绝不能仅凭退出状态判断截断成功。
	return nil
}
