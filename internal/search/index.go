package search

import (
	"bytes"
	"context"
	"encoding/json"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Index struct {
	endpoint     string
	name         string
	client       *http.Client
	expectedUUID string
}

// NewIndex supports local ES or the explicitly opted-in private demo service. A caller's
// client is copied so redirects cannot forward requests to another destination.
func NewIndex(endpoint, name string, client *http.Client) (*Index, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("ES endpoint must be a local HTTP origin")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || !platform.AllowedESAuthority(u.Hostname(), port) {
		return nil, fmt.Errorf("ES endpoint must use a loopback IP and port")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`).MatchString(name) {
		return nil, fmt.Errorf("invalid task index name")
	}
	c := http.Client{Timeout: 5 * time.Second}
	if client != nil {
		c = *client
	}
	if c.Timeout <= 0 || c.Timeout > 5*time.Second {
		c.Timeout = 5 * time.Second
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Index{endpoint: strings.TrimRight(endpoint, "/"), name: name, client: &c}, nil
}

// request bounds response memory and omits dependency bodies from errors: an
// ES diagnostic may contain original task text or cluster configuration.
func (s *Index) request(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, s.endpoint+path, bytes.NewReader(b))
	if err != nil {
		return 0, nil, fmt.Errorf("invalid ES request")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("ES request failed: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return response.StatusCode, nil, fmt.Errorf("ES response unreadable or too large")
	}
	return response.StatusCode, data, nil
}

type ApplyResult string

const (
	Applied   ApplyResult = "applied"
	Duplicate ApplyResult = "duplicate"
	Stale     ApplyResult = "stale"
)

// Put uses external versioning rather than arrival order. A 409 is not by itself
// proof of a safe duplicate: compare the stored canonical projection as well.
func (s *Index) Put(ctx context.Context, d Document) (ApplyResult, error) {
	if err := s.checkIdentity(ctx); err != nil {
		return "", err
	}
	if d.TenantID == "" || d.TaskID == "" || len(d.ID()) > 512 || d.StatusVersion < 0 || d.StatusVersion > 2147483647 {
		return "", fmt.Errorf("invalid task document identity or version")
	}
	path := "/" + s.name + "/_doc/" + d.ID()
	code, data, err := s.request(ctx, http.MethodPut, path+"?version_type=external&version="+strconv.FormatInt(d.Version(), 10), d)
	if err != nil {
		return "", err
	}
	if code == 200 || code == 201 {
		return Applied, nil
	}
	var failure struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if code != 409 || json.Unmarshal(data, &failure) != nil || failure.Error.Type != "version_conflict_engine_exception" {
		return "", fmt.Errorf("ES index rejected task: HTTP %d", code)
	}
	code, data, err = s.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	var current struct {
		Found   bool     `json:"found"`
		Version int64    `json:"_version"`
		Source  Document `json:"_source"`
	}
	if code != 200 || json.Unmarshal(data, &current) != nil || !current.Found {
		return "", fmt.Errorf("cannot verify ES version conflict")
	}
	if current.Source.TenantID != d.TenantID || current.Source.TaskID != d.TaskID {
		return "", fmt.Errorf("ES document identity conflict")
	}
	if current.Source.Version() != current.Version {
		return "", fmt.Errorf("ES stored version metadata conflict")
	}
	if current.Version > d.Version() {
		return Stale, nil
	}
	if current.Version == d.Version() && current.Source == d {
		return Duplicate, nil
	}
	return "", fmt.Errorf("ES task version/content conflict; repair required")
}

// Create is an explicit provisioning operation. Existing indices are not
// accepted blindly: the caller must verify their schema before marking ready.
func (s *Index) Create(ctx context.Context) error {
	properties := map[string]any{}
	for _, name := range []string{"tenant_id", "task_id", "target_entity_id", "task_type", "status"} {
		properties[name] = map[string]any{"type": "keyword"}
	}
	for _, name := range []string{"note", "cancelled_reason", "failure_reason"} {
		properties[name] = map[string]any{"type": "text", "analyzer": "standard"}
	}
	for _, name := range []string{"created_at", "updated_at"} {
		properties[name] = map[string]any{"type": "date_nanos"}
	}
	properties["status_version"] = map[string]any{"type": "long"}
	code, _, err := s.request(ctx, http.MethodPut, "/"+s.name, map[string]any{
		"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0},
		"mappings": map[string]any{"dynamic": "strict", "properties": properties},
	})
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("task index creation rejected: HTTP %d", code)
	}
	return nil
}
