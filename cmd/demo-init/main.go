// demo-init owns the isolated Compose demo's credentials and bootstrap ordering.
// It never reads or changes the host course's .local directory or Docker project.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/search"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: demo-init secrets|initialize|exec|canal|codes|probe|invalidate-search|rebuild-search|reset-canal-meta")
	}
	if args[0] == "probe" {
		if len(args) != 2 {
			return errors.New("probe requires a local health URL")
		}
		c := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		r, err := c.Get(args[1])
		if err != nil {
			return errors.New("health probe unavailable")
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			return fmt.Errorf("health probe HTTP %d", r.StatusCode)
		}
		return nil
	}
	dir := os.Getenv("MESHOPS_DEMO_STATE")
	if dir == "" {
		dir = "/state"
	}
	if args[0] == "invalidate-search" {
		return invalidateSearch(dir)
	}
	if args[0] == "reset-canal-meta" {
		if _, err := search.LoadBootstrap(filepath.Join(dir, "search-bootstrap.json")); err != nil {
			return err
		}
		return resetCanalMeta("/home/admin/canal-server/meta")
	}
	if args[0] == "secrets" {
		_, err := ensureSecrets(dir)
		if err == nil {
			dataDir := os.Getenv("MESHOPS_DEMO_DATA")
			if dataDir != "" {
				err = os.MkdirAll(dataDir, 0755)
				if err == nil {
					err = ownSecret(dataDir)
				}
			}
		}
		if err == nil {
			fmt.Println("Demo credentials ready; use demo-stack.ps1 -Action Codes for browser access.")
		}
		return err
	}
	var secrets map[string]string
	var err error
	if args[0] == "exec" {
		secrets, err = readCredentialFile(filepath.Join(dir, "runtime-secrets.json"), runtimeSecretNames())
		if err == nil && len(args) > 1 && filepath.Base(args[1]) == "web-gateway" {
			var browser map[string]string
			browser, err = readCredentialFile(filepath.Join(dir, "web-codes.json"), browserSecretNames())
			for k, v := range browser {
				secrets[k] = v
			}
		}
	} else {
		secrets, err = readSecrets(dir)
	}
	if err != nil {
		return err
	}
	if args[0] == "codes" {
		fmt.Print(accessCodes(secrets))
		return nil
	}
	for k, v := range secrets {
		if args[0] == "canal" {
			continue
		}
		if args[0] == "exec" {
			if strings.HasPrefix(k, "MESHOPS_MYSQL_") || k == "MESHOPS_CANAL_PASSWORD" {
				continue
			}
			if strings.HasPrefix(k, "MESHOPS_WEB_") && (len(args) < 2 || filepath.Base(args[1]) != "web-gateway") {
				continue
			}
		}
		if err = os.Setenv(k, v); err != nil {
			return err
		}
	}
	if args[0] != "canal" {
		if err = os.Setenv("MESHOPS_MYSQL_DSN", "meshops_app:"+secrets["MESHOPS_MYSQL_PASSWORD"]+"@tcp(mysql:3306)/meshops_course?parseTime=true&loc=UTC"); err != nil {
			return err
		}
	}
	switch args[0] {
	case "rebuild-search":
		if os.Getenv("MESHOPS_NETWORK_MODE") != "compose-demo" {
			return errors.New("demo recovery requires compose-demo network mode")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		return rebuildSearchMarker(ctx, dir, func(ctx context.Context) error {
			cmd := exec.CommandContext(ctx, "/app/search-admin", "--es", "http://elasticsearch:9200", "--marker", filepath.Join(dir, "search-bootstrap.json"), "--rebuild")
			cmd.Env = withoutEnv(os.Environ(), "MESHOPS_MYSQL_DSN")
			cmd.Env = append(cmd.Env, "MESHOPS_MYSQL_DSN=root:"+secrets["MESHOPS_MYSQL_ROOT_PASSWORD"]+"@tcp(mysql:3306)/meshops_course?parseTime=true&loc=UTC")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		})
	case "initialize":
		return initialize(dir, secrets)
	case "exec":
		if len(args) < 2 {
			return errors.New("exec requires a program")
		}
		return replaceProcess(args[1:])
	case "canal":
		if len(args) < 2 {
			return errors.New("canal requires the image's original entrypoint")
		}
		marker, err := search.LoadBootstrap(filepath.Join(dir, "search-bootstrap.json"))
		if err != nil {
			return err
		}
		// The image template consumes dotted environment names. Populate them
		// immediately before exec, after validating the durable complete marker.
		values := map[string]string{"canal.instance.master.journal.name": marker.Position.File, "canal.instance.master.position": fmt.Sprint(marker.Position.Offset), "canal.instance.dbPassword": secrets["MESHOPS_CANAL_PASSWORD"]}
		for k, v := range values {
			if err = os.Setenv(k, v); err != nil {
				return err
			}
		}
		return replaceProcess(args[1:])
	default:
		return errors.New("unknown demo-init command")
	}
}

func secretNames() []string {
	names := []string{"MESHOPS_CURSOR_KEY", "MESHOPS_OPERATOR_TOKEN", "MESHOPS_ADMIN_TOKEN", "MESHOPS_TASK_TOKEN", "MESHOPS_DISPATCHER_TOKEN", "MESHOPS_WEB_OPERATOR_CODE", "MESHOPS_WEB_ADMIN_CODE", "MESHOPS_CANAL_PASSWORD", "MESHOPS_MYSQL_ROOT_PASSWORD", "MESHOPS_MYSQL_PASSWORD"}
	for _, kind := range []string{"PERSON", "DRONE", "VEHICLE", "ROBOT", "SENSOR", "FACILITY"} {
		names = append(names, "MESHOPS_"+kind+"_SOURCE_TOKEN")
	}
	for _, kind := range []string{"PERSON", "DRONE", "VEHICLE", "ROBOT"} {
		names = append(names, "MESHOPS_"+kind+"_EXECUTOR_TOKEN")
	}
	sort.Strings(names)
	return names
}

func readSecrets(dir string) (map[string]string, error) {
	return readCredentialFile(filepath.Join(dir, "secrets.json"), secretNames())
}

func readCredentialFile(path string, names []string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("demo credentials unavailable; run demo-stack.ps1 -Action Up")
	}
	if len(data) > 16384 {
		return nil, errors.New("demo credentials file too large")
	}
	var values map[string]string
	if json.Unmarshal(data, &values) != nil {
		return nil, errors.New("demo credentials malformed; never regenerate existing credentials automatically")
	}
	seen := map[string]bool{}
	for _, name := range names {
		v := values[name]
		size := 32
		if name == "MESHOPS_CANAL_PASSWORD" {
			size = 24
		}
		decoded, e := base64.StdEncoding.DecodeString(v)
		if e != nil || len(decoded) != size || seen[v] {
			return nil, fmt.Errorf("demo credential %s missing, invalid, or duplicated; restore original secrets", name)
		}
		seen[v] = true
	}
	if len(values) != len(names) {
		return nil, errors.New("unexpected demo credential fields")
	}
	return values, nil
}

func ensureSecrets(dir string) (map[string]string, error) {
	if _, err := os.Stat(filepath.Join(dir, "secrets.json")); err == nil {
		values, e := readSecrets(dir)
		if e != nil {
			return nil, e
		}
		if e = protectFile(filepath.Join(dir, "secrets.json"), 0, 0, 0600); e != nil {
			return nil, e
		}
		if e = ensureDerivedSecrets(dir, values); e != nil {
			return nil, e
		}
		return values, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, name := range secretNames() {
		size := 32
		if name == "MESHOPS_CANAL_PASSWORD" {
			size = 24
		}
		b := make([]byte, size)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		values[name] = base64.StdEncoding.EncodeToString(b)
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return nil, err
	}
	// O_EXCL avoids replacing secrets if two launchers race. A partial write
	// fails validation on the next run; it must not silently rotate identity.
	f, err := os.OpenFile(filepath.Join(dir, "secrets.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err = protectFile(filepath.Join(dir, "secrets.json"), 0, 0, 0600); err != nil {
		return nil, err
	}
	// MySQL's entrypoint supports PASSWORD_FILE. Its separate file is readable
	// inside this dedicated volume; it is never published as a host port/file.
	if err = ensureDerivedSecrets(dir, values); err != nil {
		return nil, err
	}
	return values, nil
}

func ensureRootPasswordFile(dir, password string) error {
	return ensureDerivedFile(filepath.Join(dir, "mysql-root-password"), []byte(password), 999, 999, 0400)
}

func accessCodes(values map[string]string) string {
	return "Operator access code: " + values["MESHOPS_WEB_OPERATOR_CODE"] + "\nAdministrator access code: " + values["MESHOPS_WEB_ADMIN_CODE"] + "\n"
}

func initialize(dir string, values map[string]string) error {
	if os.Getenv("MESHOPS_NETWORK_MODE") != "compose-demo" {
		return errors.New("demo initialization requires compose-demo network mode")
	}
	rootDSN := "root:" + values["MESHOPS_MYSQL_ROOT_PASSWORD"] + "@tcp(mysql:3306)/meshops_course?parseTime=true&loc=UTC"
	if err := os.Setenv("MESHOPS_DEMO_ADMIN_DSN", rootDSN); err != nil {
		return err
	}
	manifest := os.Getenv("MESHOPS_MANIFEST")
	if manifest == "" {
		manifest = "/app/configs/simulation.yaml"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err := child(ctx, "/app/opctl", "seed", "--manifest", manifest, "--migrations", "/app/migrations", "--token-env", "MESHOPS_ADMIN_TOKEN", "--dsn-env", "MESHOPS_DEMO_ADMIN_DSN"); err != nil {
		return err
	}
	db, err := platform.OpenDB("MESHOPS_DEMO_ADMIN_DSN")
	if err != nil {
		return err
	}
	defer db.Close()
	password := values["MESHOPS_MYSQL_PASSWORD"]
	if !regexp.MustCompile(`^[A-Za-z0-9+/=]+$`).MatchString(password) {
		return errors.New("application credential invalid")
	}
	for _, sql := range []string{"CREATE USER IF NOT EXISTS 'meshops_app'@'%' IDENTIFIED BY '" + password + "'", "ALTER USER 'meshops_app'@'%' IDENTIFIED BY '" + password + "'", "GRANT SELECT,INSERT,UPDATE,DELETE ON meshops_course.* TO 'meshops_app'@'%'"} {
		if _, err = db.ExecContext(ctx, sql); err != nil {
			return errors.New("demo runtime database user provisioning failed")
		}
	}
	marker := filepath.Join(dir, "search-bootstrap.json")
	args := []string{"--es", "http://elasticsearch:9200", "--marker", marker}
	if _, err = os.Stat(marker); err == nil {
		b, e := search.LoadBootstrap(marker)
		if e != nil {
			return e
		}
		es, e := search.NewIndex("http://elasticsearch:9200", b.Index, nil)
		if e != nil {
			return e
		}
		if e = es.Bound(b.IndexUUID).Check(ctx); e != nil {
			return fmt.Errorf("existing search bootstrap requires explicit recovery: %w", e)
		}
		args = append(args, "--credentials-only")
	} else if !os.IsNotExist(err) {
		return err
	}
	// Maintenance child alone receives the privileged DSN; normal exec uses
	// the DML-only account above. Never log either DSN or child environment.
	cmd := exec.CommandContext(ctx, "/app/search-admin", args...)
	cmd.Env = withoutEnv(os.Environ(), "MESHOPS_MYSQL_DSN")
	cmd.Env = append(cmd.Env, "MESHOPS_MYSQL_DSN="+rootDSN)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("search initialization failed: %w", err)
	}
	if _, err = search.LoadBootstrap(marker); err != nil {
		return err
	}
	if err = ownSecret(marker); err != nil {
		return err
	}
	fmt.Println("Demo migration, bindings, topics and search bootstrap ready.")
	return nil
}

func withoutEnv(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, v := range env {
		if !strings.HasPrefix(v, name+"=") {
			out = append(out, v)
		}
	}
	return out
}
func child(ctx context.Context, path string, args ...string) error {
	c := exec.CommandContext(ctx, path, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", filepath.Base(path), err)
	}
	return nil
}
