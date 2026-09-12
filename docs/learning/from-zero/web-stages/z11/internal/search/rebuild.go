package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ResetTaskIndex is maintenance-only. Search and Canal must be stopped, and the
// bootstrap marker invalidated, before calling it. It never touches MySQL.
func (s *Index) ResetTaskIndex(ctx context.Context) error {
	if s.name != TaskIndexName || s.expectedUUID != "" {
		return fmt.Errorf("reset requires the unbound dedicated task index")
	}
	code, data, err := s.request(ctx, http.MethodDelete, "/"+TaskIndexName, nil)
	if err != nil {
		return err
	}
	var result struct {
		Acknowledged bool `json:"acknowledged"`
		Error        struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &result) != nil || !((code == 200 && result.Acknowledged) || (code == 404 && result.Error.Type == "index_not_found_exception")) {
		return fmt.Errorf("task index reset refused: HTTP %d", code)
	}
	return s.Create(ctx)
}
