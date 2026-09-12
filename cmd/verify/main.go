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
	options, e := parseOptions(os.Args[1:], os.Stderr)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
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
	if options.mode == "faults" {
		report, e := verification.Faults(ctx, root)
		return emitReport(os.Stdout, os.Stderr, report, e)
	}
	report, e := verification.BenchmarkWithProfile(ctx, root, options.seconds, options.profile)
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

// 在启动进程或创建临时数据库之前解析并拒绝无效选项。
// 故障探针仍使用独立的人员样例，不随压测类型配置变化。
type options struct {
	mode, profile string
	seconds       int
}

func parseOptions(args []string, diagnostics io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	fs.StringVar(&o.mode, "mode", "benchmark", "verification mode: benchmark or faults")
	fs.IntVar(&o.seconds, "seconds", 30, "seconds at each offered load")
	fs.StringVar(&o.profile, "profile", "mixed", "benchmark entity profile: mixed or person (not used by faults)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() != 0 {
		return o, fmt.Errorf("unexpected positional arguments")
	}
	if o.mode != "benchmark" && o.mode != "faults" {
		return o, fmt.Errorf("unknown mode")
	}
	if err := verification.ValidateBenchmarkProfile(o.profile); err != nil {
		return o, err
	}
	if o.mode == "benchmark" && (o.seconds < 5 || o.seconds > 300) {
		return o, fmt.Errorf("duration must be 5..300 seconds")
	}
	return o, nil
}
