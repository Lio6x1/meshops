package main

import (
	"context"
	"encoding/json"
	"example.com/meshops-course/internal/verification"
	"flag"
	"fmt"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stat"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() { os.Exit(run()) }
func run() int {
	stat.DisableLog()
	logx.DisableStat()
	mode := flag.String("mode", "benchmark", "verification mode")
	seconds := flag.Int("seconds", 30, "seconds at each offered load")
	flag.Parse()
	if flag.NArg() != 0 {
		return 2
	}
	root, e := os.Getwd()
	if e != nil {
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if *mode == "faults" {
		report, e := verification.Faults(ctx, root)
		return emitReport(os.Stdout, os.Stderr, report, e)
	}
	if *mode != "benchmark" {
		fmt.Fprintln(os.Stderr, "unknown mode")
		return 2
	}
	report, e := verification.Benchmark(ctx, root, *seconds)
	return emitReport(os.Stdout, os.Stderr, report, e)
}
func emitReport(out, diagnostics io.Writer, report any, err error) int {
	if writeErr := json.NewEncoder(out).Encode(report); writeErr != nil {
		fmt.Fprintln(diagnostics, "write verification report:", writeErr)
		return 1
	}
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	return 0
}
