package files

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

// TestRealFiles runs the file manager against a real Linux user "shop" with
// home /home/shop. IKE_REAL_FILES=1 go test ./internal/files -run Real
func TestRealFiles(t *testing.T) {
	if os.Getenv("IKE_REAL_FILES") == "" {
		t.Skip("set IKE_REAL_FILES=1 (needs user shop)")
	}
	cfg := config.Default()
	cfg.Root = "/"
	m := New(&kit.Env{Cfg: cfg, Run: runner.Exec{}})
	ctx := context.Background()
	run := runner.Exec{}
	_, _ = run.Run(ctx, "rm", "-rf", "/home/shop/fm")
	_, _ = run.Run(ctx, "install", "-d", "-o", "shop", "-g", "shop", "/home/shop/fm")
	if err := m.Mkdir(ctx, "shop", "fm/site/assets"); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(ctx, "shop", "fm/site/index.php", strings.NewReader("<?php echo 'hello world';"), false); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(ctx, "shop", "fm/site/index.php", strings.NewReader("x"), false); err == nil {
		t.Fatal("overwrote without asking")
	}
	if err := m.Write(ctx, "shop", "fm/site/index.php", strings.NewReader("<?php echo 'hello there';"), true); err != nil {
		t.Fatal(err)
	}
	txt, err := m.ReadText(ctx, "shop", "fm/site/index.php")
	if err != nil || txt != "<?php echo 'hello there';" {
		t.Fatalf("%q %v", txt, err)
	}
	ents, err := m.List(ctx, "shop", "fm/site")
	if err != nil || len(ents) != 2 || ents[0].Name != "assets" || ents[0].Type != "dir" || ents[1].Size != 25 {
		t.Fatalf("%+v %v", ents, err)
	}
	// a symlink to a root-only file can't be read through Ike
	_ = os.Symlink("/etc/shadow", "/home/shop/fm/shadow")
	var b bytes.Buffer
	if err := m.Read(ctx, "shop", "fm/shadow", &b); err == nil || strings.Contains(b.String(), "root:") {
		t.Fatalf("read /etc/shadow through a link: %v", err)
	}
	if _, err := m.Clean("shop", "fm/../../etc"); err == nil {
		t.Fatal(".. accepted")
	}
	if err := m.Archive(ctx, "shop", "fm", []string{"site"}, "backup", "zip"); err != nil {
		t.Fatal(err)
	}
	if err := m.Archive(ctx, "shop", "fm", []string{"site"}, "backup", "tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := m.Extract(ctx, "shop", "fm/backup.zip", "fm/unzipped"); err != nil {
		t.Fatal(err)
	}
	if err := m.Extract(ctx, "shop", "fm/backup.tar.gz", "fm/untarred"); err != nil {
		t.Fatal(err)
	}
	if txt, _ := m.ReadText(ctx, "shop", "fm/untarred/site/index.php"); !strings.Contains(txt, "hello there") {
		t.Fatal("tar round trip")
	}
	if err := m.Copy(ctx, "shop", []string{"fm/site/index.php"}, "fm/unzipped"); err != nil {
		t.Fatal(err)
	}
	if err := m.Move(ctx, "shop", []string{"fm/unzipped"}, "fm/renamed", true); err != nil {
		t.Fatal(err)
	}
	if err := m.Chmod(ctx, "shop", []string{"fm/renamed/index.php"}, "0600", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Chmod(ctx, "shop", []string{"fm/renamed/index.php"}, "4755", false); err == nil {
		t.Fatal("setuid accepted")
	}
	hits, err := m.Search(ctx, "shop", "fm", "index", "")
	if err != nil || len(hits) < 3 {
		t.Fatalf("name search %+v %v", hits, err)
	}
	hits, err = m.Search(ctx, "shop", "fm", "", "HELLO THERE")
	if err != nil || len(hits) < 2 || !strings.Contains(hits[0].Line, "hello there") {
		t.Fatalf("content search %+v %v", hits, err)
	}
	sizes, err := m.FolderSizes(ctx, "shop", "fm")
	if err != nil || len(sizes) < 3 || sizes[0].Path != "fm" {
		t.Fatalf("sizes %+v %v", sizes, err)
	}
	b.Reset()
	if err := m.DownloadFolder(ctx, "shop", "fm/site", &b); err != nil || b.Len() < 100 {
		t.Fatalf("folder download %v", err)
	}
	if err := m.Delete(ctx, "shop", []string{"fm/renamed", "fm/untarred"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "shop", []string{"web"}); err == nil {
		t.Fatal("deleted web/")
	}
	st, _ := os.Stat("/home/shop/fm/site/index.php")
	if st == nil {
		t.Fatal("file missing")
	}
	_, _ = run.Run(ctx, "rm", "-rf", "/home/shop/fm")
}
