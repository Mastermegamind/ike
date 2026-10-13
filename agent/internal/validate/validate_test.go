package validate

import "testing"

func TestDomain(t *testing.T) {
	good := []string{"example.com", "shop.example.com.ng", "my-site.io", "a1.co", "xn--bcher-kva.example"}
	bad := []string{
		"", "com", "Example.com", "-bad.com", "bad-.com", "exa_mple.com", "example.com.",
		"*.example.com", "example.com/../etc", "example.com;rm -rf /", "1.2.3.4", "ex ample.com",
		"example.c", "example.com\nserver_name evil", "..com",
	}
	for _, d := range good {
		if err := Domain(d); err != nil {
			t.Errorf("Domain(%q) unexpected error: %v", d, err)
		}
	}
	for _, d := range bad {
		if err := Domain(d); err == nil {
			t.Errorf("Domain(%q) expected error", d)
		}
	}
}

func TestUsername(t *testing.T) {
	good := []string{"andre", "shop1", "abc"}
	bad := []string{"", "ab", "1abc", "Andre", "root", "nginx", "a-b-c", "verylongusername12", "a b", "../x"}
	for _, u := range good {
		if err := Username(u); err != nil {
			t.Errorf("Username(%q) unexpected error: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := Username(u); err == nil {
			t.Errorf("Username(%q) expected error", u)
		}
	}
}

func TestPHPVersion(t *testing.T) {
	for _, v := range []string{"7.4", "8.1", "8.5"} {
		if err := PHPVersion(v); err != nil {
			t.Errorf("PHPVersion(%q): %v", v, err)
		}
	}
	for _, v := range []string{"", "8", "8.10", "../8.3", "8.3;id"} {
		if err := PHPVersion(v); err == nil {
			t.Errorf("PHPVersion(%q) expected error", v)
		}
	}
}
