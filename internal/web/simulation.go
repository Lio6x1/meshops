package web

import (
	"context"
	"encoding/json"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/simulation"
	"io"
	"log/slog"
	"net/http"
	"sort"
)

// SimulationControl is opt-in and separate from production entity/task RPCs.
// The store records desired state; only a live simulator publishes applied state.
type SimulationControl interface {
	Read(context.Context, string, string) (simulation.Status, error)
	SetDesired(context.Context, string, string, simulation.Mode) error
}
type simulationSource struct {
	SourceID   string   `json:"sourceId"`
	EntityType string   `json:"entityType"`
	EntityIDs  []string `json:"entityIds"`
	simulation.Status
}

func (s *Server) simulationSources() []platform.Source {
	var sources []platform.Source
	for _, source := range s.cfg.Registry.Sources {
		if source.TenantID == s.cfg.TenantID {
			sources = append(sources, source)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	return sources
}
func (s *Server) simulationHTTP(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Simulation == nil {
		webError(w, 503, "模拟器控制未启用；请按启动手册启用演示模式")
		return
	}
	sources := s.simulationSources()
	if r.Method == http.MethodPut {
		id := r.PathValue("source")
		found := false
		for _, source := range sources {
			if source.ID == id {
				found = true
				break
			}
		}
		if !found {
			webError(w, 404, "该模拟数据源不在当前租户的注册清单中")
			return
		}
		var body struct {
			Mode simulation.Mode `json:"mode"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			webError(w, 400, "需要 mode 字段")
			return
		}
		if decoder.Decode(new(any)) != io.EOF || (body.Mode != simulation.Running && body.Mode != simulation.Paused && body.Mode != simulation.Offline) {
			webError(w, 400, "mode 只支持 running、paused、offline")
			return
		}
		if err := s.cfg.Simulation.SetDesired(r.Context(), s.cfg.TenantID, id, body.Mode); err != nil {
			webError(w, 503, "模拟器控制暂不可用，请稍后刷新状态")
			return
		}
		actor, _ := r.Context().Value(sessionKey{}).(*session)
		slog.Info("simulation desired state changed", "tenant", s.cfg.TenantID, "actor", actor.actor, "source", id, "mode", body.Mode)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"sourceId": id, "desired": body.Mode})
		return
	}
	result := make([]simulationSource, 0, len(sources))
	for _, source := range sources {
		status, err := s.cfg.Simulation.Read(r.Context(), s.cfg.TenantID, source.ID)
		if err != nil {
			webError(w, 503, "无法读取模拟器状态，请检查演示环境 Redis")
			return
		}
		ids := make([]string, 0, len(source.Entities))
		for _, id := range source.Entities {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		result = append(result, simulationSource{source.ID, source.Adapter, ids, status})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"sources": result, "serverTime": s.cfg.Now().UTC()})
}
