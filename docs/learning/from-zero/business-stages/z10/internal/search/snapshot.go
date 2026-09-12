package search

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

type BinlogPosition struct {
	File   string `json:"file"`
	Offset uint64 `json:"offset"`
}

// Snapshot establishes an InnoDB repeatable read view while writes are locked.
// The lock is released before any ES or checkpoint I/O. saveStart must durably
// save the returned coordinate for Canal; it is not a "bootstrap complete" flag.
// Startup must refuse missing/purged binlogs rather than jump to the current end.
func Snapshot(ctx context.Context, db *sql.DB, index TaskIndex, saveStart func(BinlogPosition) error) error {
	if db == nil || index == nil || saveStart == nil {
		return fmt.Errorf("snapshot dependencies required")
	}
	lock, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	reader, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer reader.Close()
	if _, err = reader.ExecContext(ctx, "SET SESSION time_zone='+00:00'"); err != nil {
		return err
	}
	// A failed UNLOCK cannot return a lock-owning session to database/sql's pool.
	locked := false
	unlock := func() error {
		if !locked {
			return nil
		}
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, e := lock.ExecContext(c, "UNLOCK TABLES")
		if e != nil {
			_ = lock.Raw(func(any) error { return driver.ErrBadConn })
		}
		locked = false
		return e
	}
	defer unlock()
	if _, err = lock.ExecContext(ctx, "FLUSH TABLES WITH READ LOCK"); err != nil {
		_ = lock.Raw(func(any) error { return driver.ErrBadConn })
		return fmt.Errorf("cannot acquire bootstrap read lock")
	}
	locked = true
	var position BinlogPosition
	var doDB, ignoreDB, gtid string
	if err = lock.QueryRowContext(ctx, "SHOW MASTER STATUS").Scan(&position.File, &position.Offset, &doDB, &ignoreDB, &gtid); err != nil {
		return fmt.Errorf("cannot read enabled MySQL binlog position: %w", err)
	}
	tx, err := reader.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The first SELECT, not merely BEGIN, establishes the read view. Keep the
	// global lock until QueryContext has begun this SELECT against InnoDB.
	names := []string{"tenant_id", "task_id", "target_entity_id", "task_type", "status", "status_version", "payload", "created_at", "updated_at", "cancelled_reason", "failure_reason"}
	rows, err := tx.QueryContext(ctx, `SELECT tenant_id,task_id,target_entity_id,task_type,status,CAST(status_version AS CHAR),CAST(payload AS CHAR),CAST(created_at AS CHAR),CAST(updated_at AS CHAR),cancelled_reason,failure_reason FROM tasks ORDER BY task_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if err = unlock(); err != nil {
		return fmt.Errorf("cannot release bootstrap read lock")
	}
	if err = saveStart(position); err != nil {
		return err
	}
	for rows.Next() {
		values := make([]sql.NullString, len(names))
		args := make([]any, len(names))
		for i := range args {
			args[i] = &values[i]
		}
		if err = rows.Scan(args...); err != nil {
			return err
		}
		row := make(map[string]json.RawMessage, len(names))
		for i, name := range names {
			if values[i].Valid {
				row[name], _ = json.Marshal(values[i].String)
			} else {
				row[name] = json.RawMessage("null")
			}
		}
		doc, e := decodeRow(row)
		if e != nil {
			return e
		}
		if _, err = index.Put(ctx, doc); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}
