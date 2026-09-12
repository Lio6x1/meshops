package search

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	commonv1 "example.com/meshops-course/gen/common/v1"
)

var ErrInvalidSearch = errors.New("invalid search request or cursor")
var ErrSearchExpired = errors.New("search snapshot expired; start a new search")

type Filter struct {
	Keyword        string `json:"keyword"`
	Status         string `json:"status"`
	TargetEntityID string `json:"target_entity_id"`
	CreatedFrom    string `json:"created_from"`
	CreatedBefore  string `json:"created_before"`
	PageSize       int    `json:"page_size"`
}

type Page struct {
	Tasks      []Document
	NextCursor string
}

type searchCursor struct {
	Version int               `json:"v"`
	Tenant  string            `json:"tenant"`
	Filter  string            `json:"filter"`
	PIT     string            `json:"pit"`
	After   []json.RawMessage `json:"after"`
	Expires int64             `json:"expires"`
}

func normalizeFilter(f Filter) (Filter, string, error) {
	f.Keyword = strings.TrimSpace(f.Keyword)
	if f.PageSize == 0 {
		f.PageSize = 20
	}
	if f.PageSize < 1 || f.PageSize > 100 || len(f.Keyword) > 256 || !utf8.ValidString(f.Keyword) || len(f.TargetEntityID) > 128 || !utf8.ValidString(f.TargetEntityID) {
		return f, "", ErrInvalidSearch
	}
	if f.Status != "" {
		if _, ok := commonv1.TaskStatus_value["TASK_STATUS_"+f.Status]; !ok || f.Status == "UNSPECIFIED" {
			return f, "", ErrInvalidSearch
		}
	}
	var lower, upper time.Time
	for _, entry := range []struct {
		s *string
		t *time.Time
	}{{&f.CreatedFrom, &lower}, {&f.CreatedBefore, &upper}} {
		if *entry.s != "" {
			t, err := time.Parse(time.RFC3339Nano, *entry.s)
			if err != nil {
				return f, "", ErrInvalidSearch
			}
			*entry.t = t
			*entry.s = t.UTC().Format(time.RFC3339Nano)
		}
	}
	if !lower.IsZero() && !upper.IsZero() && !lower.Before(upper) {
		return f, "", ErrInvalidSearch
	}
	b, _ := json.Marshal(f)
	hash := sha256.Sum256(b)
	return f, hex.EncodeToString(hash[:]), nil
}

func encodeSearchCursor(c searchCursor, key []byte) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(b)
	token := base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	if len(token) > 8192 {
		return "", fmt.Errorf("search cursor exceeds size bound")
	}
	return token, nil
}

func decodeSearchCursor(token string, key []byte, tenant, filter string) (searchCursor, error) {
	var c searchCursor
	if len(token) > 8192 {
		return c, ErrInvalidSearch
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return c, ErrInvalidSearch
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, ErrInvalidSearch
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, ErrInvalidSearch
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(b)
	if !hmac.Equal(sig, h.Sum(nil)) || json.Unmarshal(b, &c) != nil || c.Version != 1 || c.Tenant != tenant || c.Filter != filter || c.PIT == "" || len(c.After) != 3 {
		return c, ErrInvalidSearch
	}
	if time.Now().Unix() >= c.Expires {
		return c, ErrSearchExpired
	}
	return c, nil
}

func (s *Index) closePIT(pit string) {
	if pit == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _ = s.request(ctx, http.MethodDelete, "/_pit", map[string]any{"id": pit})
}

// Search's tenant argument must come from the authenticated principal, never
// from a public request field. PIT cursors retain a fixed five minute lifetime.
func (s *Index) Search(ctx context.Context, tenant string, f Filter, token string, key []byte) (page Page, err error) {
	if tenant == "" || len(tenant) > 64 || len(key) < 32 {
		return page, ErrInvalidSearch
	}
	f, hash, err := normalizeFilter(f)
	if err != nil {
		return page, err
	}
	if err = s.checkIdentity(ctx); err != nil {
		return page, err
	}
	var cursor searchCursor
	if token != "" {
		cursor, err = decodeSearchCursor(token, key, tenant, hash)
		if err != nil {
			return page, err
		}
	} else {
		code, b, e := s.request(ctx, http.MethodPost, "/"+s.name+"/_pit?keep_alive=5m", nil)
		if e != nil {
			return page, e
		}
		var opened struct {
			ID string `json:"id"`
		}
		if code != 200 || json.Unmarshal(b, &opened) != nil || opened.ID == "" {
			return page, fmt.Errorf("cannot open search snapshot: HTTP %d", code)
		}
		cursor = searchCursor{Version: 1, Tenant: tenant, Filter: hash, PIT: opened.ID, Expires: time.Now().Add(5 * time.Minute).Unix()}
	}
	// Errors and terminal pages close the PIT. Successful intermediate pages
	// transfer its ownership to the next signed cursor; abandoned PITs expire.
	keep := false
	defer func() {
		if !keep {
			s.closePIT(cursor.PIT)
		}
	}()
	filters := []any{map[string]any{"term": map[string]any{"tenant_id": tenant}}}
	for _, field := range []struct{ name, value string }{{"status", f.Status}, {"target_entity_id", f.TargetEntityID}} {
		if field.value != "" {
			filters = append(filters, map[string]any{"term": map[string]any{field.name: field.value}})
		}
	}
	if f.CreatedFrom != "" || f.CreatedBefore != "" {
		bounds := map[string]any{}
		if f.CreatedFrom != "" {
			bounds["gte"] = f.CreatedFrom
		}
		if f.CreatedBefore != "" {
			bounds["lt"] = f.CreatedBefore
		}
		filters = append(filters, map[string]any{"range": map[string]any{"created_at": bounds}})
	}
	boolean := map[string]any{"filter": filters}
	if f.Keyword != "" {
		boolean["must"] = []any{map[string]any{"multi_match": map[string]any{"query": f.Keyword, "fields": []string{"note", "cancelled_reason", "failure_reason"}, "operator": "and"}}}
	}
	remaining := cursor.Expires - time.Now().Unix()
	if remaining <= 0 {
		return page, ErrSearchExpired
	}
	request := map[string]any{"size": f.PageSize + 1, "track_total_hits": false, "query": map[string]any{"bool": boolean}, "pit": map[string]any{"id": cursor.PIT, "keep_alive": fmt.Sprintf("%ds", remaining)}, "sort": []any{map[string]any{"created_at": map[string]any{"order": "desc", "format": "strict_date_optional_time_nanos"}}, map[string]any{"task_id": "asc"}, map[string]any{"_shard_doc": "asc"}}}
	if len(cursor.After) > 0 {
		request["search_after"] = cursor.After
	}
	code, b, err := s.request(ctx, http.MethodPost, "/_search", request)
	if err != nil {
		return page, err
	}
	if code == 404 {
		return page, ErrSearchExpired
	}
	var response struct {
		PIT      string `json:"pit_id"`
		TimedOut bool   `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits *struct {
			Hits []struct {
				Source Document          `json:"_source"`
				Sort   []json.RawMessage `json:"sort"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if code != 200 || json.Unmarshal(b, &response) != nil || response.Hits == nil || response.Hits.Hits == nil || response.TimedOut || response.Shards.Failed > 0 {
		return page, fmt.Errorf("search unavailable or partial response: HTTP %d", code)
	}
	if response.PIT != "" {
		cursor.PIT = response.PIT
	}
	hits := response.Hits.Hits
	if len(hits) > f.PageSize+1 {
		return page, fmt.Errorf("search response exceeds page bound")
	}
	for _, hit := range hits {
		if hit.Source.TenantID != tenant || hit.Source.TaskID == "" || len(hit.Sort) != 3 {
			return page, fmt.Errorf("search returned invalid task identity or sort")
		}
	}
	count := len(hits)
	if count > f.PageSize {
		count = f.PageSize
	}
	page.Tasks = make([]Document, 0, count)
	for _, hit := range hits[:count] {
		page.Tasks = append(page.Tasks, hit.Source)
	}
	if len(hits) > count {
		cursor.After = hits[count-1].Sort
		page.NextCursor, err = encodeSearchCursor(cursor, key)
		if err != nil {
			return Page{}, err
		}
		keep = true
	}
	return page, nil
}
