package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

const TaskIndexName = "meshops-tasks-v1"
const CDCTopic = "meshops-task-search-cdc-v1"
const CDCGroup = "meshops-task-search-v1"

// Bound 返回绑定到引导记录中实际索引的运行时客户端。重新创建的
// 同名空索引不能被静默当作已恢复的数据。
func (s *Index) Bound(uuid string) *Index { clone := *s; clone.expectedUUID = uuid; return &clone }

func (s *Index) checkIdentity(ctx context.Context) error {
	if s.expectedUUID == "" {
		return nil
	}
	uuid, err := s.UUID(ctx)
	if err != nil {
		return err
	}
	if uuid != s.expectedUUID {
		return fmt.Errorf("task index was replaced; bootstrap recovery required")
	}
	return nil
}

func (s *Index) Check(ctx context.Context) error { return s.checkIdentity(ctx) }

type Bootstrap struct {
	Version   int            `json:"version"`
	Complete  bool           `json:"complete"`
	Database  string         `json:"database"`
	Index     string         `json:"index"`
	IndexUUID string         `json:"index_uuid"`
	Position  BinlogPosition `json:"position"`
	CDCStart  int64          `json:"cdc_start"`
}

func SaveBootstrap(path string, b Bootstrap) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "bootstrap-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func LoadBootstrap(path string) (Bootstrap, error) {
	var b Bootstrap
	data, err := os.ReadFile(path)
	if err != nil {
		return b, fmt.Errorf("search bootstrap marker unavailable")
	}
	if len(data) > 4096 || json.Unmarshal(data, &b) != nil || b.Version != 1 || !b.Complete || b.Database != "meshops_course" || b.Index != TaskIndexName || b.IndexUUID == "" || b.CDCStart < 0 || b.Position.Offset < 4 || !regexp.MustCompile(`^[A-Za-z0-9_-]+\.[0-9]+$`).MatchString(b.Position.File) {
		return b, fmt.Errorf("search bootstrap incomplete or invalid")
	}
	return b, nil
}

func (s *Index) UUID(ctx context.Context) (string, error) {
	code, data, err := s.request(ctx, http.MethodGet, "/"+s.name+"/_settings?filter_path=*.settings.index.uuid", nil)
	if err != nil {
		return "", err
	}
	var response map[string]struct {
		Settings struct {
			Index struct {
				UUID string `json:"uuid"`
			} `json:"index"`
		} `json:"settings"`
	}
	if code != 200 || json.Unmarshal(data, &response) != nil || response[s.name].Settings.Index.UUID == "" {
		return "", fmt.Errorf("task index identity unavailable")
	}
	return response[s.name].Settings.Index.UUID, nil
}
