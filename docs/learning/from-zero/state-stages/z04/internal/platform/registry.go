package platform

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var idPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

func ValidID(s string, max int) bool { return len(s) > 0 && len(s) <= max && idPattern.MatchString(s) }
func NewID() string                  { return uuid.NewString() }
func Hash(b []byte) string           { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
// Authenticate checks a random machine token, not a user password. SHA-256
// is an identity lookup key; plaintext tokens remain available for local RPC.
func (r *Registry) Authenticate(token string) (Principal, error) {
	h := Hash([]byte(token))
	p, ok := r.Principals[h]
	if !ok {
		return Principal{}, errors.New("invalid credential")
	}
	expected := r.Credentials[Key(p.TenantID, p.ID)]
	if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
		return Principal{}, errors.New("invalid credential")
	}
	return p, nil
}
func (r *Registry) Credential(tenant, id string) (string, error) {
	s := r.Credentials[Key(tenant, id)]
	if s == "" {
		return "", errors.New("credential unavailable")
	}
	return s, nil
}
func (r *Registry) ServiceToken(tenant, role string) (string, error) {
	for _, p := range r.Principals {
		if p.TenantID == tenant && p.Role == role {
			return r.Credential(tenant, p.ID)
		}
	}
	return "", errors.New("service identity unavailable")
}

type manifest struct {
	Tenant  string `yaml:"tenant_id"`
	Sources []struct {
		ID         string            `yaml:"source_id"`
		Adapter    string            `yaml:"adapter"`
		Generation int64             `yaml:"source_generation"`
		Env        string            `yaml:"credential_env"`
		Fixture    string            `yaml:"fixture"`
		Stale      string            `yaml:"stale_after"`
		Rate       int               `yaml:"rate_limit_per_second"`
		Entities   map[string]string `yaml:"entities"`
	} `yaml:"sources"`
	Executors []struct {
		ID       string   `yaml:"executor_id"`
		Entities []string `yaml:"entity_ids"`
		Tasks    []string `yaml:"supported_tasks"`
		Env      string   `yaml:"credential_env"`
	} `yaml:"executors"`
	Actors []struct {
		ID   string `yaml:"id"`
		Role string `yaml:"role"`
		Env  string `yaml:"credential_env"`
	} `yaml:"actors"`
	Catalog []struct {
		Type        string `yaml:"task_type"`
		Description string `yaml:"description"`
		Schema      string `yaml:"parameter_schema_json"`
	} `yaml:"task_catalog"`
}

func LoadRegistry(paths string) (*Registry, error) {
	r := &Registry{Bindings: map[string]Binding{}, Sources: map[string]Source{}, Principals: map[string]Principal{}, Credentials: map[string]string{}}
	if strings.TrimSpace(paths) == "" {
		return nil, errors.New("manifest is required")
	}
	tenants := map[string]bool{}
	sourceIDs := map[string]bool{}
	add := func(p Principal, env string) error {
		if !ValidID(p.ID, 128) {
			return errors.New("invalid principal id")
		}
		token := os.Getenv(env)
		if env == "" || len(token) < 32 {
			return fmt.Errorf("credential_env %s requires at least 32 bytes", env)
		}
		k := Key(p.TenantID, p.ID)
		h := Hash([]byte(token))
		if _, ok := r.Credentials[k]; ok {
			return errors.New("duplicate principal id")
		}
		if _, ok := r.Principals[h]; ok {
			return errors.New("duplicate credential binding")
		}
		r.Credentials[k] = token
		r.Principals[h] = p
		return nil
	}
	for _, path := range strings.Split(paths, ",") {
		path = strings.TrimSpace(path)
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("manifest: %w", err)
		}
		var m manifest
		dec := yaml.NewDecoder(f)
		dec.KnownFields(true)
		err = dec.Decode(&m)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("manifest syntax: %w", err)
		}
		if !ValidID(m.Tenant, 64) || tenants[m.Tenant] {
			return nil, errors.New("invalid/duplicate tenant")
		}
		tenants[m.Tenant] = true
		catalog := map[string]bool{}
		for _, c := range m.Catalog {
			if c.Type != "inspect" || catalog[c.Type] {
				return nil, errors.New("unknown/duplicate task catalog")
			}
			catalog[c.Type] = true
		}
		if len(m.Catalog) > 0 && !catalog["inspect"] {
			return nil, errors.New("inspect catalog required")
		}
		for _, s := range m.Sources {
			if !ValidID(s.ID, 64) || sourceIDs[s.ID] {
				return nil, errors.New("invalid/duplicate source_id")
			}
			sourceIDs[s.ID] = true
			if s.Generation < 1 || s.Generation > MaxEntityVersion {
				return nil, errors.New("source_generation outside range")
			}
			switch s.Adapter {
			case "person", "drone", "vehicle", "robot", "sensor", "facility":
			default:
				return nil, errors.New("unknown adapter")
			}
			stale := 30 * time.Second
			if s.Stale != "" {
				stale, err = time.ParseDuration(s.Stale)
				if err != nil || stale <= 0 {
					return nil, errors.New("invalid stale_after")
				}
			}
			if s.Rate == 0 {
				s.Rate = 100
			}
			if s.Rate < 1 || len(s.Entities) == 0 {
				return nil, errors.New("rate/entities invalid")
			}
			fixture := s.Fixture
			if !filepath.IsAbs(fixture) {
				fixture = filepath.Join(filepath.Dir(path), fixture)
			}
			if _, err = os.Stat(fixture); err != nil {
				return nil, errors.New("source fixture unavailable")
			}
			source := Source{TenantID: m.Tenant, ID: s.ID, Adapter: s.Adapter, Fixture: fixture, Generation: s.Generation, StaleAfter: stale, Rate: s.Rate, Entities: s.Entities}
			p := Principal{ID: s.ID, TenantID: m.Tenant, Role: "source", SourceID: s.ID}
			for raw, id := range s.Entities {
				if !ValidID(raw, 128) || !ValidID(id, 128) {
					return nil, errors.New("invalid entity id")
				}
				k := Key(m.Tenant, id)
				if _, ok := r.Bindings[k]; ok {
					return nil, errors.New("entity has two authoritative sources")
				}
				r.Bindings[k] = Binding{TenantID: m.Tenant, EntityID: id, Type: s.Adapter, SourceID: s.ID, SourceGeneration: s.Generation}
				p.EntityIDs = append(p.EntityIDs, id)
			}
			sort.Strings(p.EntityIDs)
			r.Sources[Key(m.Tenant, s.ID)] = source
			if err = add(p, s.Env); err != nil {
				return nil, err
			}
		}
		for _, ex := range m.Executors {
			if !ValidID(ex.ID, 64) || len(ex.Entities) == 0 {
				return nil, errors.New("invalid executor")
			}
			if len(ex.Tasks) != 1 || ex.Tasks[0] != "inspect" {
				return nil, errors.New("executor must declare inspect")
			}
			seen := map[string]bool{}
			for _, id := range ex.Entities {
				k := Key(m.Tenant, id)
				b, ok := r.Bindings[k]
				if !ok || b.ExecutorID != "" || seen[id] {
					return nil, errors.New("executor binding missing or duplicated")
				}
				seen[id] = true
				if b.Type == "sensor" || b.Type == "facility" {
					return nil, errors.New("state-only entity cannot execute inspect")
				}
				b.ExecutorID = ex.ID
				b.Tasks = []string{"inspect"}
				r.Bindings[k] = b
			}
			if err = add(Principal{ID: ex.ID, TenantID: m.Tenant, Role: "executor", ExecutorID: ex.ID, EntityIDs: ex.Entities}, ex.Env); err != nil {
				return nil, err
			}
		}
		roles := map[string]bool{}
		for _, a := range m.Actors {
			switch a.Role {
			case "operator", "admin", "task_service", "dispatcher_service":
			default:
				return nil, errors.New("unknown actor role")
			}
			if roles[a.Role] {
				return nil, errors.New("duplicate service/actor role")
			}
			roles[a.Role] = true
			if err = add(Principal{ID: a.ID, TenantID: m.Tenant, Role: a.Role}, a.Env); err != nil {
				return nil, err
			}
		}
		for _, role := range []string{"operator"} {
			if !roles[role] {
				return nil, fmt.Errorf("missing actor role %s", role)
			}
		}
	}
	return r, nil
}
