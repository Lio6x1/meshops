package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDerivedCredentialsSeparateRuntimeBrowserAndBootstrap(t *testing.T) {
	dir := t.TempDir()
	full, err := ensureSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"runtime-secrets.json", "web-codes.json"} {
		raw, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		var values map[string]string
		if e = json.Unmarshal(raw, &values); e != nil {
			t.Fatal(e)
		}
		if values["MESHOPS_MYSQL_ROOT_PASSWORD"] != "" || values["MESHOPS_CANAL_PASSWORD"] != "" {
			t.Fatal("bootstrap secret in", name)
		}
		if name == "runtime-secrets.json" {
			if values["MESHOPS_WEB_OPERATOR_CODE"] != "" || values["MESHOPS_WEB_ADMIN_CODE"] != "" || values["MESHOPS_MYSQL_PASSWORD"] != full["MESHOPS_MYSQL_PASSWORD"] {
				t.Fatal("wrong runtime scope")
			}
		} else if len(values) != 2 || values["MESHOPS_WEB_OPERATOR_CODE"] != full["MESHOPS_WEB_OPERATOR_CODE"] {
			t.Fatal("wrong browser scope")
		}
	}
	// Ordinary exec must not depend on full/bootstrap or browser files existing.
	if err = os.Remove(filepath.Join(dir, "secrets.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, "web-codes.json")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MESHOPS_DEMO_STATE", dir)
	for _, name := range secretNames() {
		t.Setenv(name, "")
	}
	t.Setenv("MESHOPS_MYSQL_DSN", "")
	_ = run([]string{"exec", "/not-existing/entity"})
	if os.Getenv("MESHOPS_OPERATOR_TOKEN") != full["MESHOPS_OPERATOR_TOKEN"] {
		t.Fatal("ordinary process still requires full secrets")
	}
}

func TestDerivedCredentialMismatchIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	if _, err := ensureSecrets(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "runtime-secrets.json")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unexpected"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureSecrets(dir); err == nil {
		t.Fatal("derived credential mismatch accepted")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "unexpected" {
		t.Fatal("derived credentials overwritten")
	}
}
