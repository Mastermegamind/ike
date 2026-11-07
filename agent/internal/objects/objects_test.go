package objects

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
)

type call struct {
	endpoint string
	query    url.Values
	body     map[string]any
}

type fakeGarage struct {
	calls   []call
	n       int
	buckets map[string]bool
	fail    string
}

func (f *fakeGarage) Call(_ context.Context, method, endpoint string, q url.Values, body, out any) error {
	b, _ := body.(map[string]any)
	f.calls = append(f.calls, call{endpoint, q, b})
	if f.fail == endpoint {
		return errors.New("bucket is not empty")
	}
	f.n++
	switch endpoint {
	case "CreateKey":
		o := out.(*struct {
			ID     string `json:"accessKeyId"`
			Secret string `json:"secretAccessKey"`
		})
		o.ID, o.Secret = "GK"+strings.Repeat("a", f.n), "secret-"+strings.Repeat("s", f.n)
	case "CreateBucket":
		o := out.(*struct {
			ID string `json:"id"`
		})
		o.ID = "bkt" + strings.Repeat("b", f.n)
	case "GetBucketInfo":
		o := out.(*struct {
			Bytes   int64 `json:"bytes"`
			Objects int   `json:"objects"`
		})
		o.Bytes, o.Objects = 5<<20, 12
	}
	return nil
}

func (f *fakeGarage) count(endpoint string) int {
	n := 0
	for _, c := range f.calls {
		if c.endpoint == endpoint {
			n++
		}
	}
	return n
}

type accts map[string]bool

func (a accts) AccountExists(n string) error {
	if !a[n] {
		return kit.ErrNotFound
	}
	return nil
}

func TestBucketsAndKeys(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	keys, _ := secrets.Open(filepath.Join(root, "master.key"))
	m := New(&kit.Env{Cfg: cfg, Run: &runner.DryRun{}, Accounts: accts{"shop": true, "blog": true}}, keys)
	g := &fakeGarage{}
	m.Admin = g
	ctx := context.Background()
	var proxied string
	m.ProxySite = func(_ context.Context, account, domain string, port int) (string, error) {
		proxied = account + " " + domain
		return "", nil
	}
	if _, _, err := m.SetSettings(ctx, Settings{Domain: "s3.host.ng", Account: "shop"}); err != nil || proxied != "shop s3.host.ng" {
		t.Fatalf("%v %q", err, proxied)
	}
	b, cred, err := m.CreateBucket(ctx, "shop", "Media", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "shop-media" || cred == nil || cred.SecretKey == "" || cred.Endpoint != "https://s3.host.ng" || !cred.PathStyle {
		t.Fatalf("%+v %+v", b, cred)
	}
	allow := g.calls[2]
	if allow.endpoint != "AllowBucketKey" || allow.body["accessKeyId"] != cred.AccessKey || allow.body["bucketId"] != b.ID {
		t.Fatalf("%+v", allow)
	}
	quota := g.calls[3]
	if quota.endpoint != "UpdateBucket" || quota.query.Get("id") != b.ID || quota.body["quotas"].(map[string]any)["maxSize"] != int64(1024<<20) {
		t.Fatalf("%+v", quota)
	}
	raw, _ := os.ReadFile(filepath.Join(root, cfg.DataDir, "objects", "shop.json"))
	if strings.Contains(string(raw), cred.SecretKey) {
		t.Fatal("secret stored in plain text")
	}
	// a second bucket reuses the key; no secret this time
	_, cred2, err := m.CreateBucket(ctx, "shop", "backups", 0)
	if err != nil || cred2 != nil || g.count("CreateKey") != 1 {
		t.Fatalf("%+v %v", cred2, err)
	}
	for _, bad := range []string{"UPPER case", "x_y", "../etc", strings.Repeat("z", 60)} {
		if _, _, err := m.CreateBucket(ctx, "shop", bad, 0); err == nil {
			t.Errorf("bucket %q accepted", bad)
		}
	}
	if _, _, err := m.CreateBucket(ctx, "shop", "media", 0); !errors.Is(err, kit.ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	v, err := m.Get(ctx, "shop")
	if err != nil || len(v.Buckets) != 2 || v.Buckets[0].UsedMB != 5 || v.Buckets[0].Objects != 12 || v.AccessKey != cred.AccessKey {
		t.Fatalf("%+v %v", v, err)
	}
	// another account sees nothing of shop's
	if v, _ := m.Get(ctx, "blog"); len(v.Buckets) != 0 || v.AccessKey != "" {
		t.Fatalf("%+v", v)
	}
	// new key: granted on both buckets, then the old one is deleted
	nk, err := m.NewKey(ctx, "shop")
	if err != nil || nk.AccessKey == cred.AccessKey {
		t.Fatalf("%+v %v", nk, err)
	}
	last := g.calls[len(g.calls)-1]
	if last.endpoint != "DeleteKey" || last.query.Get("id") != cred.AccessKey || g.count("AllowBucketKey") != 4 {
		t.Fatalf("%+v %d", last, g.count("AllowBucketKey"))
	}
	// a bucket with objects can't go
	g.fail = "DeleteBucket"
	if err := m.DeleteBucket(ctx, "shop", "shop-media"); err == nil || !strings.Contains(err.Error(), "empty it first") {
		t.Fatalf("got %v", err)
	}
	g.fail = ""
	if err := m.DeleteBucket(ctx, "shop", "shop-media"); err != nil {
		t.Fatal(err)
	}
	// rotating the master key keeps the secret readable
	if _, err := keys.Rotate(); err != nil {
		t.Fatal(err)
	}
	m.DeleteAccount(ctx, "shop")
	if _, err := os.Stat(filepath.Join(root, cfg.DataDir, "objects", "shop.json")); !os.IsNotExist(err) {
		t.Fatal("state left after account deletion")
	}
}
