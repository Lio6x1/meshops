// Package simulation stores the demo source control plane separately from entity
// state. A requested mode is not an acknowledgement that a worker applied it.
package simulation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Mode string

const (
	Running Mode = "running"
	Paused  Mode = "paused"
	Offline Mode = "offline"
)

func (m Mode) Valid() bool { return m == Running || m == Paused || m == Offline }

const HeartbeatTTL = 3 * time.Second

// Counters are decimal strings because JavaScript numbers cannot represent all
// int64 values. They reset when a process restarts; pending is read from bbolt.
type Observation struct {
	Applied    Mode      `json:"applied"`
	ObservedAt time.Time `json:"observedAt"`
	Generated  string    `json:"generated"`
	Sent       string    `json:"sent"`
	Pending    string    `json:"pending"`
}
type Status struct {
	Desired     Mode         `json:"desired"`
	Observation *Observation `json:"observation,omitempty"`
	Connected   bool         `json:"connected"`
}
type Store struct{ client redis.UniversalClient }

func NewStore(client redis.UniversalClient) *Store { return &Store{client: client} }
func key(tenant, source, suffix string) string {
	encode := base64.RawURLEncoding.EncodeToString
	return "meshops:simulation:" + encode([]byte(tenant)) + ":" + encode([]byte(source)) + ":" + suffix
}
func modeValue(value string) (Mode, error) {
	if value == "" {
		return Running, nil
	}
	m := Mode(value)
	if !m.Valid() {
		return "", fmt.Errorf("invalid simulation mode %q", value)
	}
	return m, nil
}
func (s *Store) Desired(ctx context.Context, tenant, source string) (Mode, error) {
	value, err := s.client.Get(ctx, key(tenant, source, "desired")).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	return modeValue(value)
}
func (s *Store) SetDesired(ctx context.Context, tenant, source string, mode Mode) error {
	if tenant == "" || source == "" || !mode.Valid() {
		return errors.New("tenant, source and a valid simulation mode are required")
	}
	return s.client.Set(ctx, key(tenant, source, "desired"), string(mode), 0).Err()
}
func (s *Store) Observe(ctx context.Context, tenant, source string, o Observation) error {
	if !o.Applied.Valid() {
		return errors.New("invalid applied simulation mode")
	}
	o.ObservedAt = time.Now().UTC()
	payload, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, key(tenant, source, "observed"), payload, HeartbeatTTL).Err()
}
func (s *Store) Read(ctx context.Context, tenant, source string) (Status, error) {
	// A single MGET reads requested mode and the expiring worker heartbeat together.
	values, err := s.client.MGet(ctx, key(tenant, source, "desired"), key(tenant, source, "observed")).Result()
	if err != nil {
		return Status{}, err
	}
	desired := ""
	if values[0] != nil {
		desired = fmt.Sprint(values[0])
	}
	mode, err := modeValue(desired)
	if err != nil {
		return Status{}, err
	}
	result := Status{Desired: mode}
	if values[1] != nil {
		var observation Observation
		if err = json.Unmarshal([]byte(fmt.Sprint(values[1])), &observation); err != nil {
			return Status{}, err
		}
		if !observation.Applied.Valid() {
			return Status{}, errors.New("invalid observed simulation mode")
		}
		result.Observation = &observation
		result.Connected = true
	}
	return result, nil
}
