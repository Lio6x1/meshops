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

func TestBenchmarkProfileCLIValidation(t *testing.T) {
	var diagnostics bytes.Buffer
	for _, args := range [][]string{{"--profile", "unknown"}, {"--mode", "unknown"}, {"--profile", "mixed", "extra"}} {
		if _, err := parseOptions(args, &diagnostics); err == nil {
			t.Fatal("accepted invalid CLI", args)
		}
	}
	options, err := parseOptions(nil, &diagnostics)
	if err != nil || options.profile != "mixed" || options.seconds != 30 || options.mode != "benchmark" {
		t.Fatal("wrong default", options, err)
	}
	options, err = parseOptions([]string{"--profile", "person"}, &diagnostics)
	if err != nil || options.profile != "person" {
		t.Fatal("person comparison unavailable", options, err)
	}
}
