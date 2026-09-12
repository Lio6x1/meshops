package search

import (
	"context"
	"errors"
	"testing"
)

type recordingIndex struct {
	calls  []string
	failAt int
}

func (s *recordingIndex) Put(_ context.Context, d Document) (ApplyResult, error) {
	s.calls = append(s.calls, d.TaskID)
	if len(s.calls) == s.failAt {
		return "", errors.New("ES temporarily unavailable")
	}
	return Applied, nil
}

func TestHandlerOnlyAcknowledgesWholeMessage(t *testing.T) {
	a, b := taskRow(), taskRow()
	b["task_id"] = "task-2"
	index := &recordingIndex{failAt: 2}
	h := &Handler{Database: "meshops_course", Index: index}
	if err := h.Handle(context.Background(), canalMessage(a, b)); err == nil {
		t.Fatal("partial ES write must fail Kafka handler")
	}
	index.failAt = 0
	if err := h.Handle(context.Background(), canalMessage(a, b)); err != nil {
		t.Fatal(err)
	}
	if len(index.calls) != 4 || index.calls[2] != "task-1" {
		t.Fatal("whole record must replay")
	}
	broken := taskRow()
	delete(broken, "tenant_id")
	if err := h.Handle(context.Background(), canalMessage(a, broken)); err == nil {
		t.Fatal("malformed row accepted")
	}
	if len(index.calls) != 4 {
		t.Fatal("validate every row before writing any row")
	}
}
