package tasks

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SplitSQL 能解析未作修改的历史迁移脚本中的 DELIMITER 指令，
// 不会把 mysql 客户端指令发送给 SQL 服务端。
func SplitSQL(raw string) ([]string, error) {
	var result []string
	var b strings.Builder
	delimiter := ";"
	scan := bufio.NewScanner(strings.NewReader(raw))
	scan.Buffer(make([]byte, 4096), 1024*1024)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if strings.HasPrefix(line, "--") || line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(line), "DELIMITER ") {
			if strings.TrimSpace(b.String()) != "" {
				return nil, fmt.Errorf("DELIMITER within unfinished statement")
			}
			delimiter = strings.TrimSpace(line[len("DELIMITER "):])
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		if strings.HasSuffix(line, delimiter) {
			statement := strings.TrimSpace(b.String())
			result = append(result, strings.TrimSpace(strings.TrimSuffix(statement, delimiter)))
			b.Reset()
		}
	}
	if e := scan.Err(); e != nil {
		return nil, e
	}
	if strings.TrimSpace(b.String()) != "" {
		return nil, fmt.Errorf("unfinished SQL statement")
	}
	return result, nil
}

// Migrate 使用 MySQL 咨询锁串行执行迁移。由于 MySQL DDL 不支持事务，
// 每一步执行前都先写入日志。若中断导致某一步结果不确定，
// 重启时会明确报错，交由运维人员检查。
func Migrate(ctx context.Context, db *sql.DB, dir string) error {
	conn, e := db.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	var locked int
	if e = conn.QueryRowContext(ctx, `SELECT GET_LOCK(CONCAT(DATABASE(),':course-migrate'),10)`).Scan(&locked); e != nil || locked != 1 {
		return fmt.Errorf("cannot acquire migration lock")
	}
	defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK(CONCAT(DATABASE(),':course-migrate'))`)
	if _, e = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS course_migrations(name VARCHAR(128) NOT NULL, step INT NOT NULL, checksum CHAR(64) NOT NULL, finished BOOLEAN NOT NULL DEFAULT FALSE, PRIMARY KEY(name,step))`); e != nil {
		return e
	}
	files, e := filepath.Glob(filepath.Join(dir, "*.sql"))
	if e != nil {
		return e
	}
	sort.Strings(files)
	if len(files) < 3 {
		return fmt.Errorf("expected migrations 001..003 and any sequential additions in %s", dir)
	}
	for i, file := range files {
		if !strings.HasPrefix(filepath.Base(file), fmt.Sprintf("%03d_", i+1)) {
			return fmt.Errorf("migration sequence gap: %s", filepath.Base(file))
		}
	}
	// 仅当用于识别 001/002 的特征表和列存在时，才接纳外部已执行的迁移；
	// 不重写或删除待处理事实。
	for _, file := range files {
		raw, e := os.ReadFile(file)
		if e != nil {
			return e
		}
		statements, e := SplitSQL(string(raw))
		if e != nil {
			return fmt.Errorf("%s: %w", filepath.Base(file), e)
		}
		name := filepath.Base(file)
		hash := digest(raw)
		var entries int
		if e = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM course_migrations WHERE name=?`, name).Scan(&entries); e != nil {
			return e
		}
		adopt := false
		if entries == 0 && strings.HasPrefix(name, "001_") {
			var count int
			e = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('integration_sources','entities','entity_history_samples','tasks','task_status_history','outbox_events','consumer_dedup','task_dispatches')`).Scan(&count)
			if e != nil {
				return e
			}
			if count > 0 && count != 8 {
				return fmt.Errorf("partial legacy001 schema: %d/8 tables; inspect before migration", count)
			}
			adopt = count == 8
		}
		if entries == 0 && strings.HasPrefix(name, "002_") {
			var count int
			e = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND ((table_name='tasks' AND column_name='execution_key') OR (table_name='outbox_events' AND column_name='lease_until') OR (table_name='task_execution_reports' AND column_name='disposition') OR (table_name='task_dispatches' AND column_name='command_kind'))`).Scan(&count)
			if e != nil {
				return e
			}
			if count > 0 && count != 4 {
				return fmt.Errorf("partial legacy002 schema: inspect before migration")
			}
			adopt = count == 4
		}
		if strings.HasPrefix(name, "003_") {
			rows, e := conn.QueryContext(ctx, `SELECT tenant_id,task_id,status_version,COUNT(*) FROM task_status_history GROUP BY tenant_id,task_id,status_version HAVING COUNT(*)>1 LIMIT 20`)
			if e != nil {
				return e
			}
			var conflicts []string
			for rows.Next() {
				var tenant, id string
				var v, n int
				if e = rows.Scan(&tenant, &id, &v, &n); e != nil {
					rows.Close()
					return e
				}
				conflicts = append(conflicts, fmt.Sprintf("%s/%s version=%d rows=%d", tenant, id, v, n))
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if len(conflicts) > 0 {
				return fmt.Errorf("duplicate legacy audits require review: %s", strings.Join(conflicts, ", "))
			}
		}
		for i, statement := range statements {
			var old string
			var finished bool
			e = conn.QueryRowContext(ctx, `SELECT checksum,finished FROM course_migrations WHERE name=? AND step=?`, name, i).Scan(&old, &finished)
			if e == nil {
				if old != hash {
					return fmt.Errorf("migration checksum changed: %s", name)
				}
				if !finished {
					return fmt.Errorf("interrupted migration %s step %d; inspect actual schema and reconcile journal before retry", name, i)
				}
				continue
			}
			if e != sql.ErrNoRows {
				return e
			}
			if _, e = conn.ExecContext(ctx, `INSERT INTO course_migrations(name,step,checksum,finished) VALUES(?,?,?,?)`, name, i, hash, adopt); e != nil {
				return e
			}
			if adopt {
				continue
			}
			if _, e = conn.ExecContext(ctx, statement); e != nil {
				return fmt.Errorf("migration %s step %d: %w", name, i, e)
			}
			if _, e = conn.ExecContext(ctx, `UPDATE course_migrations SET finished=TRUE WHERE name=? AND step=?`, name, i); e != nil {
				return e
			}
		}
	}
	return nil
}
func canonical(v any) string { b, _ := json.Marshal(v); return string(b) }
func bindingValues(reg *platform.Registry) map[string]string {
	out := map[string]string{}
	for key, b := range reg.Bindings {
		b.Tasks = append([]string(nil), b.Tasks...)
		sort.Strings(b.Tasks)
		out["entity:"+key] = canonical(b)
	}
	for key, s := range reg.Sources {
		s.Fixture = ""
		out["source:"+key] = canonical(s)
	}
	return out
}
func Seed(ctx context.Context, db *sql.DB, reg *platform.Registry) error {
	return syncBindings(ctx, db, reg, true, false)
}

// SeedWithEntityExpansion 是显式维护操作，用于演示环境将每个来源的
// 注册实体从一个扩展到五个。只允许追加原始 ID 映射。
// 实体归属、来源配置、凭据、现有任务历史及其他所有
// 已持久化的绑定均保持不变。
func SeedWithEntityExpansion(ctx context.Context, db *sql.DB, reg *platform.Registry) error {
	return syncBindings(ctx, db, reg, true, true)
}

func additiveSourceEntities(old, next string) bool {
	var a, b map[string]json.RawMessage
	if json.Unmarshal([]byte(old), &a) != nil || json.Unmarshal([]byte(next), &b) != nil {
		return false
	}
	var before, after map[string]string
	if json.Unmarshal(a["Entities"], &before) != nil || json.Unmarshal(b["Entities"], &after) != nil || before == nil || after == nil {
		return false
	}
	for rawID, entityID := range before {
		if after[rawID] != entityID {
			return false
		}
	}
	delete(a, "Entities")
	delete(b, "Entities")
	// 比较所有其余字段，包括未来版本新增的字段。
	var left, right any
	if json.Unmarshal([]byte(canonical(a)), &left) != nil || json.Unmarshal([]byte(canonical(b)), &right) != nil {
		return false
	}
	return canonical(left) == canonical(right)
}

func CheckBindings(ctx context.Context, db *sql.DB, reg *platform.Registry) error {
	return syncBindings(ctx, db, reg, false, false)
}
func syncBindings(ctx context.Context, db *sql.DB, reg *platform.Registry, seed, expand bool) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	wanted := bindingValues(reg)
	if expand {
		// 删除整个来源或实体也必须接受逐值的子集检查，不能绕过校验。
		// 共用数据库的其他租户不属于本清单的范围。
		tenants := map[string]bool{}
		for _, source := range reg.Sources {
			tenants[source.TenantID] = true
		}
		for _, binding := range reg.Bindings {
			tenants[binding.TenantID] = true
		}
		for tenant := range tenants {
			sourcePrefix, entityPrefix := "source:"+tenant+":", "entity:"+tenant+":"
			rows, err := tx.QueryContext(ctx, `SELECT binding_key FROM course_bindings WHERE LEFT(binding_key,CHAR_LENGTH(?))=? OR LEFT(binding_key,CHAR_LENGTH(?))=? FOR UPDATE`, sourcePrefix, sourcePrefix, entityPrefix, entityPrefix)
			if err != nil {
				return err
			}
			var missing string
			for rows.Next() {
				var key string
				if err = rows.Scan(&key); err != nil {
					rows.Close()
					return err
				}
				if _, ok := wanted[key]; !ok {
					missing = key
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if missing != "" {
				return fmt.Errorf("entity expansion cannot remove binding: %s", missing)
			}
		}
	}
	keys := make([]string, 0, len(wanted))
	for key := range wanted {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := wanted[key]
		var old string
		e = tx.QueryRowContext(ctx, `SELECT binding_json FROM course_bindings WHERE binding_key=? FOR UPDATE`, key).Scan(&old)
		if e == sql.ErrNoRows && seed {
			_, e = tx.ExecContext(ctx, `INSERT INTO course_bindings(binding_key,binding_json) VALUES(?,?)`, key, value)
		} else if e == nil {
			var a, b any
			json.Unmarshal([]byte(old), &a)
			json.Unmarshal([]byte(value), &b)
			if canonical(a) != canonical(b) {
				if !expand || !strings.HasPrefix(key, "source:") || !additiveSourceEntities(old, value) {
					return fmt.Errorf("binding differs: %s", key)
				}
				_, e = tx.ExecContext(ctx, `UPDATE course_bindings SET binding_json=? WHERE binding_key=?`, value, key)
			}
		}
		if e != nil {
			return fmt.Errorf("binding unavailable %s: %w", key, e)
		}
	}
	for _, s := range reg.Sources {
		token, e := reg.Credential(s.TenantID, s.ID)
		if e != nil {
			return e
		}
		hash := digest([]byte(token))
		var tenant, storedHash, sourceType, types, status string
		var rate int
		e = tx.QueryRowContext(ctx, `SELECT tenant_id,credentials_hash,source_type,COALESCE(allowed_entity_types,'[]'),rate_limit_per_second,status FROM integration_sources WHERE source_id=? FOR UPDATE`, s.ID).Scan(&tenant, &storedHash, &sourceType, &types, &rate, &status)
		if e == sql.ErrNoRows && seed {
			_, e = tx.ExecContext(ctx, `INSERT INTO integration_sources(source_id,tenant_id,source_name,source_type,credentials_hash,allowed_entity_types,rate_limit_per_second) VALUES(?,?,?,'simulator',?,?,?)`, s.ID, s.TenantID, s.ID, hash, canonical([]string{s.Adapter}), s.Rate)
		} else if e == nil {
			var allowed []string
			json.Unmarshal([]byte(types), &allowed)
			if tenant != s.TenantID || storedHash != hash || sourceType != "simulator" || canonical(allowed) != canonical([]string{s.Adapter}) || rate != s.Rate || status != "active" {
				return fmt.Errorf("source binding differs: %s (tenant/credential/type/rate/status)", s.ID)
			}
		}
		if e != nil {
			return fmt.Errorf("source unavailable %s: %w", s.ID, e)
		}
	}
	for _, b := range reg.Bindings {
		var kind, source, executor, catalog string
		var generation int64
		e = tx.QueryRowContext(ctx, `SELECT entity_type,COALESCE(owner_source_id,''),source_generation,COALESCE(executor_id,''),COALESCE(task_catalog,'[]') FROM entities WHERE tenant_id=? AND entity_id=? FOR UPDATE`, b.TenantID, b.EntityID).Scan(&kind, &source, &generation, &executor, &catalog)
		want := append([]string{}, b.Tasks...)
		sort.Strings(want)
		if e == sql.ErrNoRows && seed {
			_, e = tx.ExecContext(ctx, `INSERT INTO entities(tenant_id,entity_id,entity_type,owner_source_id,source_generation,executor_id,task_catalog) VALUES(?,?,?,?,?,?,?)`, b.TenantID, b.EntityID, b.Type, b.SourceID, b.SourceGeneration, b.ExecutorID, canonical(want))
		} else if e == nil {
			var got []string
			json.Unmarshal([]byte(catalog), &got)
			sort.Strings(got)
			if kind != b.Type || source != b.SourceID || generation != b.SourceGeneration || executor != b.ExecutorID || canonical(got) != canonical(want) {
				return fmt.Errorf("entity binding differs: %s/%s (type/source/generation/executor/catalog)", b.TenantID, b.EntityID)
			}
		}
		if e != nil {
			return fmt.Errorf("entity unavailable %s: %w", b.EntityID, e)
		}
	}
	return tx.Commit()
}
