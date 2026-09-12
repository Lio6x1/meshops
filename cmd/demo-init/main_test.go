package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSecretsPersistAndCodesDoNotExposeMachineCredentials(t *testing.T) {
	dir := t.TempDir()
	first, err := ensureSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ensureSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range first {
		if second[key] != value {
			t.Fatalf("secret changed on restart: %s", key)
		}
		if len(value) < 32 {
			t.Fatalf("short secret %s", key)
		}
	}
	if !regexp.MustCompile(`^[A-Za-z0-9+/]{32}$`).MatchString(first["MESHOPS_CANAL_PASSWORD"]) {
		t.Fatal("Canal password length/alphabet incompatible")
	}
	if first["MESHOPS_WEB_OPERATOR_CODE"] == first["MESHOPS_OPERATOR_TOKEN"] {
		t.Fatal("web code reuses machine token")
	}
	visible := accessCodes(first)
	for key, value := range first {
		if !strings.Contains(key, "WEB_") && strings.Contains(visible, value) {
			t.Fatalf("machine credential leaked: %s", key)
		}
	}
	if !strings.Contains(visible, first["MESHOPS_WEB_OPERATOR_CODE"]) || !strings.Contains(visible, first["MESHOPS_WEB_ADMIN_CODE"]) {
		t.Fatal("missing access codes")
	}
}

func TestCorruptSecretsAreNeverRegenerated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.json")
	bad := []byte(`{"MESHOPS_OPERATOR_TOKEN":"incomplete"}`)
	if err := os.WriteFile(path, bad, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureSecrets(dir); err == nil {
		t.Fatal("incomplete secrets accepted")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(bad) {
		t.Fatal("existing secrets overwritten")
	}
}

func TestMissingDerivedMySQLFileReusesOriginalSecret(t *testing.T) {
	dir := t.TempDir()
	values, err := ensureSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mysql-root-password")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = ensureSecrets(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != values["MESHOPS_MYSQL_ROOT_PASSWORD"] {
		t.Fatal("derived password file did not recover from durable secrets", err)
	}
}

func TestWrongDerivedMySQLFileFailsWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	if _, err := ensureSecrets(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mysql-root-password")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unexpected"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureSecrets(dir); err == nil {
		t.Fatal("inconsistent root password accepted")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "unexpected" {
		t.Fatal("root password replaced")
	}
}

func TestRuntimeExecDoesNotReceiveBootstrapOrBrowserSecrets(t *testing.T) {
	dir := t.TempDir()
	if _, err := ensureSecrets(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MESHOPS_DEMO_STATE", dir)
	for _, name := range secretNames() {
		t.Setenv(name, "")
	}
	t.Setenv("MESHOPS_MYSQL_DSN", "")
	if err := run([]string{"exec", "/not-existing/entity"}); err == nil {
		t.Fatal("nonexistent program unexpectedly ran")
	}
	for _, name := range []string{"MESHOPS_MYSQL_ROOT_PASSWORD", "MESHOPS_CANAL_PASSWORD", "MESHOPS_WEB_OPERATOR_CODE", "MESHOPS_WEB_ADMIN_CODE"} {
		if os.Getenv(name) != "" {
			t.Fatalf("runtime received unnecessary secret %s", name)
		}
	}
	if os.Getenv("MESHOPS_OPERATOR_TOKEN") == "" || !strings.HasPrefix(os.Getenv("MESHOPS_MYSQL_DSN"), "meshops_app:") {
		t.Fatal("runtime environment missing")
	}
}
