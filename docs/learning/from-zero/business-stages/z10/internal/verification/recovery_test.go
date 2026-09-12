package verification

import (
	"errors"
	"testing"
	"time"
)

func TestStopUncertaintyStillRestoresDependency(t *testing.T) {
	stopped := false
	cause := errors.New("CLI response lost after stop")
	err := recoverDependency(func() error { stopped = true; return cause }, func() error { stopped = false; return nil }, func(func() error) error { t.Fatal("probe ran after uncertain stop"); return nil })
	if !errors.Is(err, cause) || stopped {
		t.Fatal("uncertain stop left dependency unavailable", err, stopped)
	}
}
func TestRestoreErrorsAreNotLost(t *testing.T) {
	primary, recovery := errors.New("probe failed"), errors.New("restore failed")
	err := recoverDependency(func() error { return nil }, func() error { return recovery }, func(func() error) error { return primary })
	if !errors.Is(err, primary) || !errors.Is(err, recovery) {
		t.Fatal("lost fault/recovery error", err)
	}
	calls := 0
	err = recoverDependency(func() error { return nil }, func() error { calls++; return nil }, func(restore func() error) error { return restore() })
	if err != nil || calls != 1 {
		t.Fatal("successful recovery should run once", calls, err)
	}
}
func TestAllSupportedBenchmarkDurationsLeaveDrainBudget(t *testing.T) {
	for _, seconds := range []int{5, 30, 180, 300} {
		for _, rate := range []int{100, 500} {
			budget := phaseBudget(rate, rate*seconds, false)
			if budget < time.Duration(seconds)*time.Second+time.Minute {
				t.Fatalf("%ds load has no bounded drain budget: %s", seconds, budget)
			}
		}
	}
}
