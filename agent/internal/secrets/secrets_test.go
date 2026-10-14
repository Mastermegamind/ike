package secrets

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSealRotate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "master.key")
	k, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	v, err := k.Seal("cf-token-123")
	if err != nil || !strings.HasPrefix(v, "ike1:") || strings.Contains(v, "cf-token") {
		t.Fatalf("seal: %q %v", v, err)
	}
	stored := v
	k.Register("dns", func(open, seal func(string) (string, error)) error {
		p, err := open(stored)
		if err != nil {
			return err
		}
		stored, err = seal(p)
		return err
	})
	st, err := k.Rotate()
	if err != nil || st.Keys != 1 {
		t.Fatalf("rotate: %+v %v", st, err)
	}
	if stored == v {
		t.Fatal("store was not re-sealed")
	}
	// reopen from disk
	k2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := k2.Unseal(stored)
	if err != nil || got != "cf-token-123" {
		t.Fatalf("unseal: %q %v", got, err)
	}
	if _, err := k2.Unseal(v); err == nil {
		t.Fatal("old value should not open after its key was dropped")
	}
	if plain, _ := k2.Unseal("legacy"); plain != "legacy" {
		t.Fatal("unsealed legacy values pass through")
	}
	// tampering fails
	bad := stored[:len(stored)-2] + "AA"
	if _, err := k2.Unseal(bad); err == nil {
		t.Fatal("tampered value opened")
	}
}
