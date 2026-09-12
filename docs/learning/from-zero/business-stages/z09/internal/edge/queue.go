// Package edge owns the durable state of the two simulators. A database has one
// process owner; server acknowledgements never advance an unsent local sequence.
package edge

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	commonv1 "example.com/meshops-course/gen/common/v1"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

var metaBucket = []byte("meta")
var pendingBucket = []byte("pending")
var versionsBucket = []byte("entity_versions")

const MaxPending = 100000
const MaxPendingBytes int64 = 1 << 30

type Queue struct {
	db         *bolt.DB
	mu         sync.Mutex
	epoch      string
	sent       int64
	maxEntries int
	maxBytes   int64
}
type QueuedEvent struct {
	Sequence int64
	Event    *commonv1.EntityStateEvent
}
type QueueStats struct {
	Epoch             string `json:"epoch"`
	ConfirmedSequence int64  `json:"confirmedSequence"`
	Pending           int    `json:"pending"`
	PendingBytes      int64  `json:"pendingBytes"`
	FileBytes         int64  `json:"fileBytes"`
}

func key64(n int64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, uint64(n)); return b }
func number(b []byte) int64 {
	if len(b) != 8 {
		return -1
	}
	return int64(binary.BigEndian.Uint64(b))
}
func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func openBolt(path string) (*bolt.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	return bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
}
func OpenQueue(path string) (*Queue, error) {
	info, statErr := os.Stat(path)
	fresh := os.IsNotExist(statErr)
	if statErr != nil && !fresh {
		return nil, statErr
	}
	if !fresh && info.Size() == 0 {
		return nil, errors.New("existing queue is empty/corrupt; preserve it for recovery")
	}
	db, err := openBolt(path)
	if err != nil {
		return nil, err
	}
	q := &Queue{db: db, maxEntries: MaxPending, maxBytes: MaxPendingBytes}
	if fresh {
		err = db.Update(func(tx *bolt.Tx) error {
			for _, name := range [][]byte{metaBucket, pendingBucket, versionsBucket} {
				if _, e := tx.CreateBucket(name); e != nil {
					return e
				}
			}
			m := tx.Bucket(metaBucket)
			if e := m.Put([]byte("epoch"), []byte(randomID())); e != nil {
				return e
			}
			if e := m.Put([]byte("next_sequence"), key64(1)); e != nil {
				return e
			}
			if e := m.Put([]byte("pending_bytes"), key64(0)); e != nil {
				return e
			}
			return m.Put([]byte("confirmed_sequence"), key64(0))
		})
	}
	if err == nil {
		err = db.View(func(tx *bolt.Tx) error {
			if e := validateQueue(tx); e != nil {
				return e
			}
			m := tx.Bucket(metaBucket)
			q.epoch = string(m.Get([]byte("epoch")))
			q.sent = number(m.Get([]byte("confirmed_sequence")))
			return nil
		})
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("queue validation failed; original file preserved: %w", err)
	}
	return q, nil
}
func validateQueue(tx *bolt.Tx) error {
	m, p, v := tx.Bucket(metaBucket), tx.Bucket(pendingBucket), tx.Bucket(versionsBucket)
	if m == nil || p == nil || v == nil {
		return errors.New("missing buckets")
	}
	if len(m.Get([]byte("epoch"))) == 0 {
		return errors.New("missing epoch")
	}
	confirmed, next := number(m.Get([]byte("confirmed_sequence"))), number(m.Get([]byte("next_sequence")))
	if confirmed < 0 || next < 1 {
		return errors.New("invalid watermarks")
	}
	expected := confirmed + 1
	var total int64
	count := 0
	if err := p.ForEach(func(k, b []byte) error {
		if number(k) != expected {
			return fmt.Errorf("sequence gap at %d", expected)
		}
		e := new(commonv1.EntityStateEvent)
		if err := proto.Unmarshal(b, e); err != nil {
			return err
		}
		if e.EntityVersion < 1 {
			return errors.New("invalid queued entity version")
		}
		if number(v.Get([]byte(e.TenantId+":"+e.EntityId))) < e.EntityVersion {
			return errors.New("entity version allocator is behind queued event")
		}
		expected++
		count++
		total += int64(len(b))
		return nil
	}); err != nil {
		return err
	}
	if next != expected {
		return errors.New("next_sequence disagrees with pending tail")
	}
	if count > MaxPending || total > MaxPendingBytes {
		return errors.New("queue exceeds capacity")
	}
	if number(m.Get([]byte("pending_bytes"))) != total {
		return errors.New("pending byte accounting mismatch")
	}
	return v.ForEach(func(k, b []byte) error {
		if len(k) == 0 || number(b) < 1 || number(b) > 9007199254740991 {
			return errors.New("invalid entity version")
		}
		return nil
	})
}
func (q *Queue) Epoch() string { return q.epoch }
func (q *Queue) Close() error  { return q.db.Close() }
func (q *Queue) BindSource(identity string) error {
	return q.db.Update(func(tx *bolt.Tx) error {
		m := tx.Bucket(metaBucket)
		old := m.Get([]byte("source_identity"))
		if old != nil && string(old) != identity {
			return errors.New("queue belongs to a different source/generation")
		}
		return m.Put([]byte("source_identity"), []byte(identity))
	})
}
func (q *Queue) Generate(entityKey string, build func(int64) (*commonv1.EntityStateEvent, error)) (int64, error) {
	var seq int64
	err := q.db.Update(func(tx *bolt.Tx) error {
		v := tx.Bucket(versionsBucket)
		version := number(v.Get([]byte(entityKey)))
		if version < 0 {
			version = 0
		}
		version++
		if version > 9007199254740991 {
			return status.Error(codes.ResourceExhausted, "entity version exhausted")
		}
		event, e := build(version)
		if e != nil {
			return e
		}
		if event == nil || event.EntityVersion != version || entityKey != event.TenantId+":"+event.EntityId {
			return errors.New("builder changed allocated identity/version")
		}
		seq, e = q.enqueue(tx, event)
		if e != nil {
			return e
		}
		return v.Put([]byte(entityKey), key64(version))
	})
	return seq, err
}
func (q *Queue) enqueue(tx *bolt.Tx, event *commonv1.EntityStateEvent) (int64, error) {
	if event == nil || event.EntityVersion < 1 || event.EntityVersion > 9007199254740991 || event.TenantId == "" || event.EntityId == "" {
		return 0, errors.New("invalid event")
	}
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return 0, err
	}
	p := tx.Bucket(pendingBucket)
	m := tx.Bucket(metaBucket)
	size := number(m.Get([]byte("pending_bytes")))
	seq := number(m.Get([]byte("next_sequence")))
	count := seq - number(m.Get([]byte("confirmed_sequence"))) - 1
	if count >= int64(q.maxEntries) || size+int64(len(b)) > q.maxBytes {
		return 0, status.Error(codes.ResourceExhausted, "pending queue capacity reached")
	}
	if seq < 1 || seq == int64(^uint64(0)>>1) {
		return 0, errors.New("invalid/exhausted sequence")
	}
	if err = p.Put(key64(seq), b); err != nil {
		return 0, err
	}
	if err = m.Put([]byte("pending_bytes"), key64(size+int64(len(b)))); err != nil {
		return 0, err
	}
	return seq, m.Put([]byte("next_sequence"), key64(seq+1))
}

// Enqueue copies an existing event, preserving its entity version and identity.
func (q *Queue) Enqueue(event *commonv1.EntityStateEvent) (int64, error) {
	var seq int64
	err := q.db.Update(func(tx *bolt.Tx) error {
		var e error
		seq, e = q.enqueue(tx, event)
		if e != nil {
			return e
		}
		v := tx.Bucket(versionsBucket)
		key := []byte(event.TenantId + ":" + event.EntityId)
		if event.EntityVersion > number(v.Get(key)) {
			return v.Put(key, key64(event.EntityVersion))
		}
		return nil
	})
	return seq, err
}
func (q *Queue) Pending(limit int) ([]QueuedEvent, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("batch must be 1..100")
	}
	out := make([]QueuedEvent, 0, limit)
	err := q.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(pendingBucket).Cursor()
		for k, b := c.First(); k != nil && len(out) < limit; k, b = c.Next() {
			event := new(commonv1.EntityStateEvent)
			if e := proto.Unmarshal(b, event); e != nil {
				return e
			}
			out = append(out, QueuedEvent{number(k), event})
		}
		return nil
	})
	return out, err
}

// BeginStream discards the previous connection's in-memory sent boundary.
func (q *Queue) BeginStream() (QueueStats, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	s, e := q.Stats()
	if e == nil {
		q.sent = s.ConfirmedSequence
	}
	return s, e
}
func (q *Queue) MarkSent(epoch string, first, last int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.db.View(func(tx *bolt.Tx) error {
		m := tx.Bucket(metaBucket)
		if epoch != q.epoch || first != q.sent+1 || last < first || last >= number(m.Get([]byte("next_sequence"))) {
			return errors.New("invalid sent interval")
		}
		q.sent = last
		return nil
	})
}
func (q *Queue) Ack(epoch string, sequence int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.db.Update(func(tx *bolt.Tx) error {
		m := tx.Bucket(metaBucket)
		confirmed := number(m.Get([]byte("confirmed_sequence")))
		if epoch != q.epoch || sequence < confirmed || sequence > q.sent {
			return errors.New("invalid epoch, regressing or unsent ACK")
		}
		p := tx.Bucket(pendingBucket)
		size := number(m.Get([]byte("pending_bytes")))
		c := p.Cursor()
		for k, b := c.First(); k != nil && number(k) <= sequence; k, b = c.Next() {
			size -= int64(len(b))
			if err := c.Delete(); err != nil {
				return err
			}
		}
		if err := m.Put([]byte("pending_bytes"), key64(size)); err != nil {
			return err
		}
		return m.Put([]byte("confirmed_sequence"), key64(sequence))
	})
}
func (q *Queue) Stats() (QueueStats, error) {
	s := QueueStats{Epoch: q.epoch}
	err := q.db.View(func(tx *bolt.Tx) error {
		s.ConfirmedSequence = number(tx.Bucket(metaBucket).Get([]byte("confirmed_sequence")))
		return tx.Bucket(pendingBucket).ForEach(func(_, v []byte) error { s.Pending++; s.PendingBytes += int64(len(v)); return nil })
	})
	if f, e := os.Stat(q.db.Path()); e == nil {
		s.FileBytes = f.Size()
	}
	return s, err
}

// CompactQueue requires exclusive bbolt ownership. It keeps the original backup,
// and restores its name if replacing the closed database fails.
func CompactQueue(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	q, err := OpenQueue(path)
	if err != nil {
		return "", err
	}
	defer q.Close()
	tmp := path + ".compact-" + randomID()
	backup := path + ".backup-" + randomID()
	dst, err := bolt.Open(tmp, 0600, nil)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	err = bolt.Compact(dst, q.db, 16<<20)
	if err == nil {
		err = dst.View(validateQueue)
	}
	if err == nil {
		err = q.db.View(func(src *bolt.Tx) error {
			return dst.View(func(target *bolt.Tx) error {
				for _, name := range [][]byte{metaBucket, pendingBucket, versionsBucket} {
					a, b := src.Bucket(name), target.Bucket(name)
					ac, bc := a.Cursor(), b.Cursor()
					ak, av := ac.First()
					bk, bv := bc.First()
					for ak != nil || bk != nil {
						if !bytes.Equal(ak, bk) || !bytes.Equal(av, bv) {
							return errors.New("compaction changed durable contents")
						}
						ak, av = ac.Next()
						bk, bv = bc.Next()
					}
				}
				return nil
			})
		})
	}
	closeErr := dst.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = q.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(path, backup); err != nil {
		return "", err
	}
	if err = os.Rename(tmp, path); err != nil {
		restore := os.Rename(backup, path)
		return "", fmt.Errorf("replace: %v; restore: %v", err, restore)
	}
	return backup, nil
}
