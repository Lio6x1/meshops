package bus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// 用内存消息源控制空闲和故障时刻；实际执行生产消费循环，断言提交边界。
func TestCommitBatchFlushesSuccessfulPrefixOnIdle(t *testing.T) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	i, calls := 0, 0
	var committed []int64
	fetch := func(c context.Context) (kafka.Message, error) {
		if i < 7 {
			m := kafka.Message{Topic: "t", Partition: 0, Offset: int64(i)}
			i++
			return m, nil
		}
		<-c.Done()
		return kafka.Message{}, c.Err()
	}
	err := (&Kafka{}).consumeRecords(ctx, fetch, func(offsets map[string]map[int]int64) error {
		committed = append(committed, offsets["t"][0])
		cancel()
		return nil
	}, "g", "t", 0, 0, func(context.Context, []byte) error { calls++; return nil }, true)
	if err != nil || calls != 7 || len(committed) != 1 || committed[0] != 7 {
		t.Fatalf("calls=%d commits=%v err=%v", calls, committed, err)
	}
	if elapsed := time.Since(started); elapsed < 80*time.Millisecond || elapsed > time.Second {
		t.Fatalf("idle batch flush timing %s", elapsed)
	}
}

func TestCommitBatchNeverCrossesFailedRecord(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	i, handled := 0, 0
	var committed []int64
	err := (&Kafka{}).consumeRecords(ctx, func(context.Context) (kafka.Message, error) {
		m := kafka.Message{Topic: "t", Offset: int64(i)}
		i++
		return m, nil
	}, func(offsets map[string]map[int]int64) error {
		committed = append(committed, offsets["t"][0])
		cancel()
		return nil
	}, "g", "t", 0, 0, func(context.Context, []byte) error {
		handled++
		if handled == 4 {
			return errors.New("write failed")
		}
		return nil
	}, true)
	if err != nil || handled != 4 || len(committed) != 1 || committed[0] != 3 {
		t.Fatalf("handled=%d commits=%v err=%v", handled, committed, err)
	}
}

func TestCommitBatchBoundsReplayWindow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	i, handled := 0, 0
	var committed []int64
	err := (&Kafka{}).consumeRecords(ctx, func(context.Context) (kafka.Message, error) {
		m := kafka.Message{Topic: "t", Offset: int64(i)}
		i++
		return m, nil
	}, func(offsets map[string]map[int]int64) error {
		committed = append(committed, offsets["t"][0])
		cancel()
		return nil
	}, "g", "t", 0, 0, func(context.Context, []byte) error { handled++; return nil }, true)
	if err != nil || handled < 2 || handled > 100 || len(committed) != 1 || committed[0] != int64(handled) {
		t.Fatalf("handled=%d commits=%v err=%v", handled, committed, err)
	}
}

func TestCommitBatchCancellationLeavesUncommittedForReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	commits := 0
	err := (&Kafka{}).consumeRecords(ctx, func(context.Context) (kafka.Message, error) { return kafka.Message{Topic: "t", Offset: 0}, nil }, func(map[string]map[int]int64) error { commits++; return nil }, "g", "t", 0, 0, func(context.Context, []byte) error { cancel(); return nil }, true)
	if err != nil || commits != 0 {
		t.Fatalf("commits=%d err=%v", commits, err)
	}
}

func TestCommitBatchRejectsGapWithoutCommittingBeyondIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	i, commits := 0, 0
	err := (&Kafka{}).consumeRecords(ctx, func(context.Context) (kafka.Message, error) {
		m := kafka.Message{Topic: "t", Offset: int64(i * 2)}
		i++
		return m, nil
	}, func(map[string]map[int]int64) error { commits++; return nil }, "g", "t", 0, 0, func(context.Context, []byte) error { return nil }, true)
	var gap *RetentionGap
	if !errors.As(err, &gap) || commits != 0 {
		t.Fatalf("commits=%d err=%v", commits, err)
	}
}

func TestCommitBatchStopsOnLostGeneration(t *testing.T) {
	for _, failure := range []error{kafka.IllegalGeneration, kafka.UnknownMemberId, kafka.RebalanceInProgress, kafka.GroupAuthorizationFailed} {
		t.Run(failure.Error(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			i, commits := 0, 0
			err := (&Kafka{}).consumeRecords(ctx, func(context.Context) (kafka.Message, error) {
				m := kafka.Message{Topic: "t", Offset: int64(i)}
				i++
				return m, nil
			}, func(map[string]map[int]int64) error { commits++; return failure }, "g", "t", 0, 0, func(context.Context, []byte) error { return nil }, true)
			if commits != 1 || i > 100 || (terminalBrokerError(failure) && !errors.Is(err, failure)) || (!terminalBrokerError(failure) && err != nil) {
				t.Fatalf("fetches=%d commits=%d err=%v", i, commits, err)
			}
		})
	}
}

func TestCommitBatchRetriesSameOffsetBeforeFetchingMore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	i := 0
	var offsets []int64
	var fetchedAtCommit []int
	err := (&Kafka{}).consumeRecords(ctx, func(context.Context) (kafka.Message, error) {
		m := kafka.Message{Topic: "t", Offset: int64(i)}
		i++
		return m, nil
	}, func(m map[string]map[int]int64) error {
		offsets = append(offsets, m["t"][0])
		fetchedAtCommit = append(fetchedAtCommit, i)
		if len(offsets) == 1 {
			return kafka.RequestTimedOut
		}
		cancel()
		return nil
	}, "g", "t", 0, 0, func(context.Context, []byte) error { return nil }, true)
	if err != nil || len(offsets) != 2 || offsets[0] != offsets[1] || fetchedAtCommit[0] != fetchedAtCommit[1] {
		t.Fatalf("offsets=%v fetched=%v err=%v", offsets, fetchedAtCommit, err)
	}
}
