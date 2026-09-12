package main

import (
	"bytes"
	"errors"
	"testing"
)

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }
func TestReportWriteFailureReturnsNonzero(t *testing.T) {
	var diagnostics bytes.Buffer
	if code := emitReport(brokenOutput{}, &diagnostics, map[string]bool{"passed": true}, nil); code == 0 {
		t.Fatal("missing JSON report was reported as success")
	}
}
