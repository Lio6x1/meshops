package state_test

import (
	"context"
	"database/sql"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/tasks"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"os"
	"strconv"
	"testing"
	"time"
)

// An explicitly supplied administrator DSN creates a new, isolated database.
// Cleanup only drops that generated database; never the configured default DB.
func TestMain(m *testing.M) {
	adminDSN := os.Getenv("MESHOPS_TEST_MYSQL_ADMIN_DSN")
	if adminDSN == "" {
		os.Exit(m.Run())
	}
	admin, err := platform.OpenDB("MESHOPS_TEST_MYSQL_ADMIN_DSN")
	if err != nil {
		fmt.Fprintln(os.Stderr, "state integration admin connection failed")
		os.Exit(1)
	}
	database := "state_it_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err = admin.ExecContext(ctx, "CREATE DATABASE "+database+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		fmt.Fprintln(os.Stderr, "state integration database creation failed:", err)
		admin.Close()
		os.Exit(1)
	}
	cfg, err := mysql.ParseDSN(adminDSN)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid integration DSN")
		admin.Close()
		os.Exit(1)
	}
	cfg.DBName = database
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	dsn := cfg.FormatDSN()
	db, err := sql.Open("mysql", dsn)
	if err == nil {
		err = tasks.Migrate(ctx, db, "../../migrations")
	}
	if db != nil {
		db.Close()
	}
	exitCode := 1
	if err != nil {
		fmt.Fprintln(os.Stderr, "state integration migration failed:", err)
	} else {
		os.Setenv("MESHOPS_TEST_MYSQL_DSN", dsn)
		exitCode = m.Run()
	}
	cleanup, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if _, err = admin.ExecContext(cleanup, "DROP DATABASE "+database); err != nil {
		fmt.Fprintln(os.Stderr, "state integration generated database cleanup failed:", database)
		exitCode = 1
	}
	cleanupCancel()
	admin.Close()
	os.Exit(exitCode)
}
