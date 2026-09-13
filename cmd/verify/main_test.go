package main

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }
func TestReportWriteFailureReturnsNonzero(t *testing.T) {
	var diagnostics bytes.Buffer
	if code := emitReport(brokenOutput{}, &diagnostics, map[string]bool{"passed": true}, nil); code == 0 {
		t.Fatal("missing JSON report was reported as success")
	}
}

func TestScaleCLI(t *testing.T) {
	var diagnostics bytes.Buffer
	defaults, err := parseOptions(nil, &diagnostics)
	if err != nil || defaults.entities != 10000 || defaults.rates != "100,500" || defaults.timeout != 15*time.Minute {
		t.Fatal(defaults, err)
	}
	for _, args := range [][]string{{"--entities", "1000001"}, {"--entities", "11"}, {"--rates", "0"}, {"--rates", "100,100"}, {"--rates", "nan"}, {"--timeout", "0s"}, {"--warmup-timeout", "0s"}} {
		if _, err := parseOptions(args, &diagnostics); err == nil {
			t.Fatal(args)
		}
	}
	o, err := parseOptions([]string{"--entities", "1000000", "--rates", "500,16667", "--timeout", "2h", "--warmup-timeout", "90m"}, &diagnostics)
	if err != nil || o.entities != 1000000 || o.rates != "500,16667" {
		t.Fatal(o, err)
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
