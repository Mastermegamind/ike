package storage

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
)

// Signatures from the AWS documentation ("Signature Calculations for the
// Authorization Header", examples for GET Bucket lifecycle and list objects).
func TestSigV4Vectors(t *testing.T) {
	s := &S3{Endpoint: "https://s3.amazonaws.com", Region: "us-east-1", Bucket: "examplebucket",
		AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		now: func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) }}
	cases := []struct {
		q    url.Values
		want string
	}{
		{url.Values{"lifecycle": {""}}, "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"},
		{url.Values{"max-keys": {"2"}, "prefix": {"J"}}, "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"},
	}
	for _, c := range cases {
		u, err := s.objectURL("", c.q)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("GET", u.String(), nil)
		req.URL = u
		s.sign(req, emptySHA)
		auth := req.Header.Get("Authorization")
		want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + c.want
		if auth != want {
			t.Errorf("%v:\n got %s\nwant %s", c.q, auth, want)
		}
	}
	// keys with spaces and unicode are escaped per segment
	u, _ := (&S3{Endpoint: "https://s3.example.com", Bucket: "b", PathStyle: true}).objectURL("backups/my file ñ.tar", nil)
	if u.EscapedPath() != "/b/backups/my%20file%20%C3%B1.tar" {
		t.Fatal(u.EscapedPath())
	}
}

func TestTargets(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	keys, _ := secrets.Open(filepath.Join(root, "k"))
	m := New(&kit.Env{Cfg: cfg, Run: &runner.DryRun{}}, keys)
	tg, err := m.Save(context.Background(), "", Target{Name: "B2 backups", Kind: "b2", Region: "eu-central-003", Bucket: "ike-backups",
		Prefix: "/srv1/", AccessKey: "0031234567890ab0000000001", SecretKey: "K003abcdefghijklmnopqrstuvwxyz0"})
	if err != nil {
		t.Fatal(err)
	}
	if tg.Endpoint != "https://s3.eu-central-003.backblazeb2.com" || tg.SecretKey != "set" || tg.Prefix != "srv1" {
		t.Fatalf("%+v", tg)
	}
	raw, _ := os.ReadFile(filepath.Join(root, cfg.StorageFile))
	if strings.Contains(string(raw), "K003abc") {
		t.Fatal("secret stored in plain text")
	}
	repo, env, err := m.ResticEnv("", tg.ID, "server")
	if err != nil || repo != "s3:https://s3.eu-central-003.backblazeb2.com/ike-backups/srv1/server" || env[1] != "AWS_SECRET_ACCESS_KEY=K003abcdefghijklmnopqrstuvwxyz0" {
		t.Fatalf("%s %v %v", repo, env, err)
	}
	// update without a secret keeps it
	tg.Name = "B2"
	if _, err := m.Save(context.Background(), "", tg); err != nil {
		t.Fatal(err)
	}
	full, _ := m.Get("", tg.ID)
	if full.SecretKey != "K003abcdefghijklmnopqrstuvwxyz0" {
		t.Fatal("secret lost on update")
	}
	if _, err := m.Get("shop", tg.ID); err == nil {
		t.Fatal("account saw the admin's target")
	}
	if _, err := m.Save(context.Background(), "shop", Target{Name: "x", Kind: "local", Prefix: "etc"}); err == nil {
		t.Fatal("account made a local target")
	}
	if _, err := m.Save(context.Background(), "", Target{Name: "x", Kind: "s3", Region: "us-east-1", Bucket: "b", AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "x"}); err == nil {
		t.Fatal("bad bucket accepted")
	}
}
