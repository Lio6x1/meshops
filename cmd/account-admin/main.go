// account-admin 仅供部署机器上的管理员初始化与恢复账号。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/meshops-course/internal/accounts"
	"example.com/meshops-course/internal/platform"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func readPassword(r io.Reader) (string, error) {
	raw, e := io.ReadAll(io.LimitReader(r, 4097))
	if e != nil || len(raw) > 4096 {
		return "", errors.New("password stdin must be one bounded JSON object")
	}
	var b struct {
		Password *string `json:"password"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&b); e != nil || b.Password == nil {
		return "", errors.New("password stdin requires password string")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return "", errors.New("password stdin requires one JSON object")
	}
	return *b.Password, nil
}
func main() {
	if e := run(os.Args[1:], os.Stdin, os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: account-admin status|bootstrap|reset-admin --tenant TENANT [--username USER] [--display-name NAME]")
	}
	action := args[0]
	if action != "status" && action != "bootstrap" && action != "reset-admin" {
		return errors.New("unknown admin action")
	}
	fs := flag.NewFlagSet("account-admin", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tenant := fs.String("tenant", "", "trusted tenant")
	username := fs.String("username", "", "admin username")
	display := fs.String("display-name", "", "admin display name")
	if e := fs.Parse(args[1:]); e != nil || fs.NArg() != 0 {
		return errors.New("invalid admin arguments; passwords are accepted only through stdin JSON")
	}
	if *tenant == "" || (action != "status" && *username == "") {
		return errors.New("tenant and action username required")
	}
	password := ""
	var e error
	if action != "status" {
		password, e = readPassword(in)
		if e != nil {
			return e
		}
	}
	db, e := platform.OpenDB("MESHOPS_MYSQL_DSN")
	if e != nil {
		return e
	}
	defer db.Close()
	s := accounts.NewStore(db)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result any
	switch action {
	case "status":
		a, e := s.Admin(ctx, *tenant)
		if errors.Is(e, accounts.ErrNotFound) {
			result = map[string]any{"initialized": false}
		} else if e != nil {
			return errors.New("account status unavailable; run migrations first")
		} else {
			result = map[string]any{"initialized": true, "username": a.Username}
		}
	case "bootstrap":
		a, created, e := s.Bootstrap(ctx, *tenant, *username, *display, password)
		if e != nil {
			return safeError(e)
		}
		result = map[string]any{"account": a, "created": created}
	case "reset-admin":
		a, e := s.ResetAdmin(ctx, *tenant, *username, password)
		if e != nil {
			return safeError(e)
		}
		result = map[string]any{"account": a}
	}
	return json.NewEncoder(out).Encode(result)
}

// 不把驱动错误、DSN 或用户输入写入命令日志。
func safeError(e error) error {
	for _, known := range []error{accounts.ErrInvalid, accounts.ErrCredentials, accounts.ErrConflict, accounts.ErrForbidden, accounts.ErrNotFound, accounts.ErrBusy} {
		if errors.Is(e, known) {
			return known
		}
	}
	return errors.New("account operation failed")
}
