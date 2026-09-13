package tasks

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"example.com/meshops-course/internal/platform"
)

// 模拟数据库明确拒绝 INSERT，验证真实 Seed 的错误边界；不依赖活跃数据库。
func TestBindingInsertFailureReportsSafeBatchMetadata(t *testing.T) {
	cause := errors.New("database rejected secret-value-forbidden")
	connection := &bindingDiagnosticConn{cause: cause}
	db := sql.OpenDB(bindingDiagnosticConnector{connection})
	defer db.Close()
	r := &platform.Registry{Bindings: map[string]platform.Binding{
		"t:private-entity": {TenantID: "t", EntityID: "private-entity"},
	}}
	err := Seed(context.Background(), db, r)
	if err == nil || !errors.Is(err, cause) {
		t.Fatalf("database cause lost: %v", err)
	}
	// 独立写出期望 JSON，按 UTF-8 字节计算参数大小，不包含 SQL/协议开销。
	const expectedJSON = `{"TenantID":"t","EntityID":"private-entity","Type":"","SourceID":"","ExecutorID":"","SourceGeneration":0,"Tasks":null}`
	for _, part := range []string{"operation=insert", "start=0", "batch_rows=1", "insert_offset=0", "rows=1", fmt.Sprintf("parameter_bytes=%d", len("entity:t:private-entity")+len(expectedJSON)), "elapsed=", "cause_type="} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("missing diagnostic %q in %q", part, err)
		}
	}
	for _, secret := range []string{"private-entity", "secret-value-forbidden", "TenantID"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("diagnostic leaked parameter or driver error value: %q", err)
		}
	}
	if !connection.rolledBack {
		t.Fatal("failed batch did not roll back")
	}
}

func TestBindingInsertsBoundBytesAndKeepOversizedRowsWhole(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sizes    []int
		wantRows []int
	}{
		{"byte_budget_and_oversized_row", []int{3 << 20, 3 << 20, 5 << 20, 8}, []int{1, 1, 1, 1}},
		{"existing_row_budget", make([]int, 501), []int{500, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connection := &bindingDiagnosticConn{}
			db := sql.OpenDB(bindingDiagnosticConnector{connection})
			defer db.Close()
			if err := Seed(context.Background(), db, diagnosticRegistry(tc.sizes)); err != nil {
				t.Fatal(err)
			}
			if len(connection.inserts) != len(tc.wantRows) {
				t.Fatalf("INSERT batches=%v, expected rows=%v", connection.inserts, tc.wantRows)
			}
			for i, batch := range connection.inserts {
				if batch.rows != tc.wantRows[i] {
					t.Errorf("batch %d rows=%d expected=%d", i, batch.rows, tc.wantRows[i])
				}
				if batch.bytes > 4<<20 && batch.rows != 1 {
					t.Errorf("oversized combined INSERT: %+v", batch)
				}
			}
			if connection.begins != 1 || !connection.committed || connection.rolledBack {
				t.Fatal("batching changed transaction boundary")
			}
		})
	}
}

func TestBindingLaterByteBatchFailureRollsBackWholeSeed(t *testing.T) {
	cause := errors.New("sensitive-second-insert-failure")
	connection := &bindingDiagnosticConn{cause: cause, failInsertAt: 2}
	db := sql.OpenDB(bindingDiagnosticConnector{connection})
	defer db.Close()
	err := Seed(context.Background(), db, diagnosticRegistry([]int{3 << 20, 3 << 20, 3 << 20}))
	if !errors.Is(err, cause) {
		t.Fatalf("later INSERT failure not returned: %v", err)
	}
	if connection.begins != 1 || !connection.rolledBack || connection.committed || len(connection.inserts) != 2 {
		t.Fatalf("failed byte batch transaction: %+v", connection)
	}
	for _, part := range []string{"start=0", "batch_rows=3", "insert_offset=1", "rows=1", fmt.Sprintf("parameter_bytes=%d", connection.inserts[1].bytes)} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("missing %q in %q", part, err)
		}
	}
	if strings.Contains(err.Error(), "sensitive-second") || strings.Contains(err.Error(), "entity-000001") {
		t.Fatal("diagnostics leaked values")
	}
}

func diagnosticRegistry(sizes []int) *platform.Registry {
	r := &platform.Registry{Bindings: map[string]platform.Binding{}}
	for i, size := range sizes {
		id := fmt.Sprintf("entity-%06d", i)
		r.Bindings["t:"+id] = platform.Binding{TenantID: "t", EntityID: id, Tasks: []string{strings.Repeat("x", size)}}
	}
	return r
}

type bindingDiagnosticConnector struct{ connection *bindingDiagnosticConn }

func (c bindingDiagnosticConnector) Connect(context.Context) (driver.Conn, error) {
	return c.connection, nil
}
func (bindingDiagnosticConnector) Driver() driver.Driver { return bindingDiagnosticDriver{} }

type bindingDiagnosticDriver struct{}

func (bindingDiagnosticDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type bindingDiagnosticConn struct {
	cause        error
	rolledBack   bool
	committed    bool
	begins       int
	failInsertAt int
	inserts      []bindingDiagnosticInsert
}
type bindingDiagnosticInsert struct{ rows, bytes int }

func (*bindingDiagnosticConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*bindingDiagnosticConn) Close() error                { return nil }
func (c *bindingDiagnosticConn) Begin() (driver.Tx, error) { c.begins++; return c, nil }
func (c *bindingDiagnosticConn) Commit() error             { c.committed = true; return nil }
func (c *bindingDiagnosticConn) Rollback() error           { c.rolledBack = true; return nil }
func (*bindingDiagnosticConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return bindingDiagnosticRows{}, nil
}
func (c *bindingDiagnosticConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.HasPrefix(query, "INSERT INTO course_bindings") {
		batch := bindingDiagnosticInsert{rows: len(args) / 2}
		for _, arg := range args {
			batch.bytes += len(arg.Value.(string))
		}
		c.inserts = append(c.inserts, batch)
		if c.cause != nil && (c.failInsertAt == 0 || len(c.inserts) == c.failInsertAt) {
			return nil, c.cause
		}
	}
	return driver.RowsAffected(1), nil
}

type bindingDiagnosticRows struct{}

func (bindingDiagnosticRows) Columns() []string         { return []string{"binding_key", "binding_json"} }
func (bindingDiagnosticRows) Close() error              { return nil }
func (bindingDiagnosticRows) Next([]driver.Value) error { return io.EOF }
