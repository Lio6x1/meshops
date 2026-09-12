// search-admin performs local maintenance; it is not a public RPC service.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"example.com/meshops-course/internal/search"
	"github.com/go-sql-driver/mysql"
	"github.com/segmentio/kafka-go"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	endpoint := flag.String("es", "http://127.0.0.1:19200", "local ES endpoint")
	marker := flag.String("marker", ".local/search-bootstrap.json", "bootstrap marker path")
	credentialsOnly := flag.Bool("credentials-only", false, "provision the dedicated local Canal account without importing tasks")
	rebuild := flag.Bool("rebuild", false, "rebuild the dedicated task projection; use scripts/rebuild-search.ps1")
	invalidateOnly := flag.Bool("invalidate-only", false, "mark Search unavailable before maintenance")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if (*credentialsOnly && (*rebuild || *invalidateOnly)) || (*rebuild && *invalidateOnly) {
		return fmt.Errorf("maintenance modes are mutually exclusive")
	}
	if *invalidateOnly {
		return search.SaveBootstrap(*marker, search.Bootstrap{Version: 1, Database: "meshops_course", Index: search.TaskIndexName})
	}
	if _, err := os.Stat(*marker); err == nil && !*credentialsOnly && !*rebuild {
		return fmt.Errorf("bootstrap marker already exists; validate or repair existing bootstrap before retrying")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot inspect bootstrap marker: %w", err)
	}
	password := os.Getenv("MESHOPS_CANAL_PASSWORD")
	if !regexp.MustCompile(`^[A-Za-z0-9+/]{32}$`).MatchString(password) {
		return fmt.Errorf("load scripts/search-env.ps1 first")
	}
	cfg, err := mysql.ParseDSN(os.Getenv("MESHOPS_MYSQL_DSN"))
	if err != nil || cfg.DBName != "meshops_course" {
		return fmt.Errorf("meshops_course MySQL configuration required")
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("invalid MySQL configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var enabled int
	var format, image string
	if err = db.QueryRowContext(ctx, "SELECT @@log_bin,@@binlog_format,@@binlog_row_image").Scan(&enabled, &format, &image); err != nil {
		return err
	}
	if enabled != 1 || format != "ROW" || image != "FULL" {
		return fmt.Errorf("MySQL requires binlog ROW/FULL")
	}
	// Password alphabet is validated above; no arbitrary SQL text is accepted.
	for _, statement := range []string{
		"CREATE USER IF NOT EXISTS 'meshops_canal'@'%' IDENTIFIED BY '" + password + "'",
		"ALTER USER 'meshops_canal'@'%' IDENTIFIED BY '" + password + "'",
		"GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'meshops_canal'@'%'",
		"GRANT SELECT ON meshops_course.tasks TO 'meshops_canal'@'%'",
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("Canal account provisioning failed")
		}
	}
	if *credentialsOnly {
		return nil
	}
	brokers := strings.Split(os.Getenv("MESHOPS_KAFKA_BROKERS"), ",")
	if len(brokers) == 0 || brokers[0] == "" {
		return fmt.Errorf("Kafka brokers required")
	}
	k := &kafka.Client{Addr: kafka.TCP(brokers...), Timeout: 5 * time.Second}
	created, err := k.CreateTopics(ctx, &kafka.CreateTopicsRequest{Topics: []kafka.TopicConfig{{Topic: search.CDCTopic, NumPartitions: 1, ReplicationFactor: 1, ConfigEntries: []kafka.ConfigEntry{{ConfigName: "retention.ms", ConfigValue: "604800000"}}}}})
	if err != nil {
		return fmt.Errorf("CDC topic creation failed: %w", err)
	}
	if e, ok := created.Errors[search.CDCTopic]; !ok || (e != nil && !errors.Is(e, kafka.TopicAlreadyExists)) {
		return fmt.Errorf("CDC topic was not created")
	}
	metadata, err := k.Metadata(ctx, &kafka.MetadataRequest{Topics: []string{search.CDCTopic}})
	if err != nil || len(metadata.Topics) != 1 || metadata.Topics[0].Error != nil || len(metadata.Topics[0].Partitions) != 1 {
		return fmt.Errorf("CDC topic must have one healthy partition")
	}
	start, err := search.CDCBoundary(ctx, k)
	// Windows stops a process abruptly; its Kafka membership may take a session
	// timeout to expire. Never bypass the idle check with an unfenced reset.
	idleDeadline := time.Now().Add(40 * time.Second)
	for err != nil && ctx.Err() == nil && time.Now().Before(idleDeadline) {
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		start, err = search.CDCBoundary(ctx, k)
	}
	if err != nil {
		return err
	}
	es, err := search.NewIndex(*endpoint, search.TaskIndexName, nil)
	if err != nil {
		return err
	}
	state := search.Bootstrap{Version: 1, Database: "meshops_course", Index: search.TaskIndexName, CDCStart: start}
	if err = search.SaveBootstrap(*marker, state); err != nil {
		return err
	}
	if *rebuild {
		err = es.ResetTaskIndex(ctx)
	} else {
		err = es.Create(ctx)
	}
	if err != nil {
		return err
	}
	uuid, err := es.UUID(ctx)
	if err != nil {
		return err
	}
	state.IndexUUID = uuid
	if err = search.SaveBootstrap(*marker, state); err != nil {
		return err
	}
	if err = search.Snapshot(ctx, db, es, func(p search.BinlogPosition) error { state.Position = p; return search.SaveBootstrap(*marker, state) }); err != nil {
		return fmt.Errorf("snapshot incomplete; marker is not ready: %w", err)
	}
	if err = search.CommitCDCStart(ctx, k, start); err != nil {
		return err
	}
	state.Complete = true
	if err = search.SaveBootstrap(*marker, state); err != nil {
		return err
	}
	fmt.Println("Task snapshot imported; saved Canal start position. Start Canal and Search to consume subsequent changes.")
	return nil
}
