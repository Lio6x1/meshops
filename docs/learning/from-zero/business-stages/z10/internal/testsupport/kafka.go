// Package testsupport contains destructive fault operations restricted to
// disposable integration topics in the dedicated reference Compose project.
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
	// The CLI can print partition errors despite exit zero; callers also verify
	// the broker's new low watermark, never infer truncation from exit status.
	return nil
}
