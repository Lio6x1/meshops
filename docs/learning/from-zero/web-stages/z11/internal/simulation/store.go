// Package simulation stores the demo source control plane separately from entity
// state. A requested mode is not an acknowledgement that a worker applied it.
package simulation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// Demo sources pre-register five entities. Count selects a stable prefix without
// deleting entity history, credentials, or any durable offline queue entries.
const MaxEntitiesPerSource = 5

// Counters are decimal strings because JavaScript numbers cannot represent all
// int64 values. They reset when a process restarts; pending is read from bbolt.
type Observation struct {
	ActiveCount int       `json:"activeCount"`
	Applied     Mode      `json:"applied"`
	ObservedAt  time.Time `json:"observedAt"`
	Generated   string    `json:"generated"`
	Sent        string    `json:"sent"`
	Pending     string    `json:"pending"`
}
type Status struct {
	Count       int          `json:"count"`
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

func countValue(value any) (int, error) {
	if value == nil {
		return 1, nil
	}
	count, err := strconv.Atoi(fmt.Sprint(value))
	if err != nil || count < 0 || count > MaxEntitiesPerSource {
		return 0, fmt.Errorf("invalid simulation entity count %q", value)
	}
	return count, nil
}

func (s *Store) DesiredCount(ctx context.Context, tenant, source string) (int, error) {
	value, err := s.client.Get(ctx, key(tenant, source, "count")).Result()
	if errors.Is(err, redis.Nil) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	return countValue(value)
}

// SetCount only changes count: concurrent mode updates cannot be overwritten.
func (s *Store) SetCount(ctx context.Context, tenant, source string, count int) error {
	if tenant == "" || source == "" || count < 0 || count > MaxEntitiesPerSource {
		return errors.New("tenant, source and simulation entity count 0..5 are required")
	}
	return s.client.Set(ctx, key(tenant, source, "count"), count, 0).Err()
}
func (s *Store) Observe(ctx context.Context, tenant, source string, o Observation) error {
	if !o.Applied.Valid() {
		return errors.New("invalid applied simulation mode")
	}
	if o.ActiveCount < 0 || o.ActiveCount > MaxEntitiesPerSource {
		return errors.New("invalid active simulation entity count")
	}
	o.ObservedAt = time.Now().UTC()
	payload, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, key(tenant, source, "observed"), payload, HeartbeatTTL).Err()
}
func (s *Store) Read(ctx context.Context, tenant, source string) (Status, error) {
	// One Redis command reads the desired settings and expiring heartbeat together.
	values, err := s.client.MGet(ctx, key(tenant, source, "desired"), key(tenant, source, "observed"), key(tenant, source, "count")).Result()
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
	count, err := countValue(values[2])
	if err != nil {
		return Status{}, err
	}
	result := Status{Desired: mode, Count: count}
	if values[1] != nil {
		var observation Observation
		if err = json.Unmarshal([]byte(fmt.Sprint(values[1])), &observation); err != nil {
			return Status{}, err
		}
		if !observation.Applied.Valid() {
			return Status{}, errors.New("invalid observed simulation mode")
		}
		if observation.ActiveCount < 0 || observation.ActiveCount > MaxEntitiesPerSource {
			return Status{}, errors.New("invalid observed simulation entity count")
		}
		result.Observation = &observation
		result.Connected = true
	}
	return result, nil
}
