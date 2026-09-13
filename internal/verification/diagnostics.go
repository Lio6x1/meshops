package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type diagnosticSample struct {
	At      string            `json:"at"`
	Metrics map[string]string `json:"serviceMetrics"`
	MySQL   map[string]string `json:"mysqlGlobalStatus"`
	Redis   string            `json:"redisMemoryAndStats"`
	Errors  []string          `json:"errors,omitempty"`
}

// 仅抓取指标与运行计数，禁止保存环境或带凭证的配置。
func captureDiagnostics(ctx context.Context, env *Environment) diagnosticSample {
	ctx, stop := context.WithTimeout(ctx, 4*time.Second)
	defer stop()
	sample := diagnosticSample{At: time.Now().UTC().Format(time.RFC3339), Metrics: map[string]string{}, MySQL: map[string]string{}}
	client := &http.Client{Timeout: 2 * time.Second}
	for _, role := range []string{"ingest", "entity"} {
		process := env.Processes[role]
		if process == nil {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+process.Metrics+"/metrics", nil)
		if err != nil {
			sample.Errors = append(sample.Errors, role+": metrics request")
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			sample.Errors = append(sample.Errors, role+": metrics unavailable")
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			sample.Errors = append(sample.Errors, role+": metrics response")
			continue
		}
		sample.Metrics[role] = string(raw)
	}
	if env.DB != nil {
		rows, err := env.DB.QueryContext(ctx, "SHOW GLOBAL STATUS WHERE Variable_name IN ('Threads_running','Threads_connected','Questions','Slow_queries','Innodb_buffer_pool_reads','Innodb_buffer_pool_read_requests','Innodb_rows_inserted','Innodb_rows_updated','Innodb_row_lock_waits','Innodb_row_lock_time','Bytes_received','Bytes_sent')")
		if err != nil {
			sample.Errors = append(sample.Errors, "mysql: status unavailable")
		} else {
			for rows.Next() {
				var key, value string
				if err = rows.Scan(&key, &value); err != nil {
					break
				}
				sample.MySQL[key] = value
			}
			if err != nil || rows.Err() != nil {
				sample.Errors = append(sample.Errors, "mysql: status scan")
			}
			rows.Close()
		}
	}
	if env.Redis != nil {
		value, err := env.Redis.Info(ctx, "memory", "stats").Result()
		if err != nil {
			sample.Errors = append(sample.Errors, "redis: info unavailable")
		} else {
			sample.Redis = value
		}
	}
	return sample
}

func startDiagnostics(ctx context.Context, env *Environment) (func() error, error) {
	file, err := os.OpenFile(filepath.Join(env.Dir, "diagnostics.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var failure error
	go func() {
		defer close(done)
		defer file.Close()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			sample := captureDiagnostics(ctx, env)
			if ctx.Err() != nil {
				return
			}
			if len(sample.Errors) > 0 {
				failure = errors.New("diagnostic sampling incomplete; inspect diagnostics.jsonl")
			}
			if err := json.NewEncoder(file).Encode(sample); err != nil {
				fmt.Fprintln(os.Stderr, "diagnostics write failed")
				failure = err
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() error { cancel(); <-done; return failure }, nil
}
