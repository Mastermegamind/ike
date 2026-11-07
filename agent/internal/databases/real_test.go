package databases

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// TestRealPostgresAndMariaDB needs running PostgreSQL and MariaDB servers and
// a Linux user "shop". IKE_REAL_DB=1 go test ./internal/databases -run Real
func TestRealPostgresAndMariaDB(t *testing.T) {
	if os.Getenv("IKE_REAL_DB") == "" {
		t.Skip("set IKE_REAL_DB=1")
	}
	cfg := config.Default()
	cfg.Root, cfg.Postgres, cfg.DataDir = "/", true, t.TempDir()
	m := New(&kit.Env{Cfg: cfg, Run: runner.Exec{}})
	ctx := context.Background()
	run := runner.Exec{}
	// clean up earlier runs
	_ = m.psql(ctx, "", `DROP DATABASE IF EXISTS "shop_app" WITH (FORCE); DROP ROLE IF EXISTS "shop_ro"; DROP ROLE IF EXISTS "shop_app";`)

	cred, err := m.Create(ctx, "shop", CreateRequest{Engine: Postgres, Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cred.URL, "postgresql://shop_app:") {
		t.Fatal(cred.URL)
	}
	dump := "CREATE TABLE products (id serial primary key, name text);\nINSERT INTO products (name) VALUES ('rice'), ('beans');\n"
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(dump))
	_ = zw.Close()
	if err := m.Import(ctx, "shop", Postgres, "shop_app", &gz); err != nil {
		t.Fatal(err)
	}
	// the import's tables belong to the owner, and the temporary role is gone
	out, _ := m.psqlQuery(ctx, "shop_app", "SELECT tableowner FROM pg_tables WHERE tablename='products'; SELECT count(*) FROM pg_roles WHERE rolname LIKE 'ike_imp_%';")
	if strings.TrimSpace(out) != "shop_app\n0" {
		t.Fatalf("owner/temp role: %q", out)
	}
	// a dump that tries to escape is refused
	evil := strings.NewReader("\\! touch /tmp/ike-pwned\nCREATE ROLE evil SUPERUSER;\n")
	if err := m.Import(ctx, "shop", Postgres, "shop_app", evil); err == nil {
		t.Fatal("evil dump succeeded")
	}
	if st, err := os.Stat("/tmp/ike-pwned"); err == nil {
		if sys, ok := st.Sys().(interface{ Uid() uint32 }); ok {
			t.Logf("touched by uid %v", sys)
		}
		owner, _ := run.Run(ctx, "stat", "-c", "%U", "/tmp/ike-pwned")
		_ = os.Remove("/tmp/ike-pwned")
		if strings.TrimSpace(string(owner)) == "root" {
			t.Fatal("psql ran a shell command as root")
		}
	}
	ro, err := m.AddRole(ctx, "shop", Postgres, "shop_app", "ro", "read")
	if err != nil {
		t.Fatal(err)
	}
	checks, err := m.Diagnose(ctx, "shop", DiagnoseRequest{Engine: Postgres, Database: "shop_app", User: "shop_ro", Password: ro.Password})
	if err != nil || checks[len(checks)-1].Title != "Signing in works" {
		t.Fatalf("diagnose: %+v %v", checks, err)
	}
	checks, _ = m.Diagnose(ctx, "shop", DiagnoseRequest{Engine: Postgres, Database: "shop_app", User: "shop_app", Password: "wrong"})
	if checks[len(checks)-1].Title != "Wrong password" {
		t.Fatalf("diagnose wrong password: %+v", checks)
	}
	// read-only role can read, not write
	_, err = run.Exec(ctx, runner.Command{Name: "psql", Args: []string{"-X", "-h", "127.0.0.1", "-U", "shop_ro", "-d", "shop_app", "-c", "INSERT INTO products (name) VALUES ('x')"},
		Env: []string{"PGPASSWORD=" + ro.Password, "PATH=/usr/bin:/bin"}})
	if err == nil {
		t.Fatal("read-only role wrote")
	}
	var exp bytes.Buffer
	if err := m.Export(ctx, "shop", Postgres, "shop_app", &exp); err != nil {
		t.Fatal(err)
	}
	zr, _ := gzip.NewReader(&exp)
	body, _ := io.ReadAll(zr)
	if !strings.Contains(string(body), "rice") || strings.Contains(string(body), "OWNER TO") {
		t.Fatalf("export: %s", body)
	}
	dbs, _ := m.RefreshSizes(ctx, "shop")
	if dbs[0].SizeMB <= 0 {
		t.Fatalf("size %+v", dbs)
	}
	if err := m.Delete(ctx, "shop", Postgres, "shop_app"); err != nil {
		t.Fatal(err)
	}

	// MariaDB: import as a temporary user limited to the one database
	_, _ = run.RunInput(ctx, "DROP DATABASE IF EXISTS shop_wp; DROP DATABASE IF EXISTS shop_other; CREATE DATABASE shop_wp; CREATE DATABASE shop_other;", "mariadb")
	m.MariaDBOf = func(string) []string { return []string{"shop_wp"} }
	if err := m.Import(ctx, "shop", MariaDB, "shop_wp", strings.NewReader("CREATE TABLE posts (id int);\nINSERT INTO posts VALUES (1),(2);\n")); err != nil {
		t.Fatal(err)
	}
	if err := m.Import(ctx, "shop", MariaDB, "shop_wp", strings.NewReader("CREATE TABLE shop_other.stolen (id int);\n")); err == nil {
		t.Fatal("import reached another database")
	}
	if err := m.Import(ctx, "shop", MariaDB, "shop_other", strings.NewReader("SELECT 1;")); err == nil {
		t.Fatal("import into a database the account doesn't own")
	}
	exp.Reset()
	if err := m.Export(ctx, "shop", MariaDB, "shop_wp", &exp); err != nil {
		t.Fatal(err)
	}
	zr, _ = gzip.NewReader(&exp)
	body, _ = io.ReadAll(zr)
	if !strings.Contains(string(body), "INSERT INTO `posts` VALUES") || !strings.Contains(string(body), "(2)") {
		t.Fatalf("mariadb export: %s", body[:min(len(body), 500)])
	}
	if out, _ := run.RunInput(ctx, "SELECT COUNT(*) FROM mysql.user WHERE User LIKE 'ike_imp_%';", "mariadb", "-N"); strings.TrimSpace(string(out)) != "0" {
		t.Fatal("temporary import user left behind")
	}
	if _, err := m.Processes(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetSlowLog(ctx, SlowLogSettings{Enabled: true, LongQuerySecs: 0.2}); err != nil {
		t.Fatal(err)
	}
	_, _ = run.RunInput(ctx, "SELECT SLEEP(0.5);", "mariadb")
	sq, err := m.SlowQueries("", "duration", 10)
	if err != nil || len(sq) == 0 || !strings.Contains(sq[0].Query, "SLEEP") {
		t.Fatalf("slow queries: %+v %v", sq, err)
	}
	st, err := m.ReplicationStatus(ctx)
	if err != nil || st.Configured {
		t.Fatalf("replication: %+v %v", st, err)
	}
	_, _ = run.RunInput(ctx, "DROP DATABASE shop_wp; DROP DATABASE shop_other;", "mariadb")
}
