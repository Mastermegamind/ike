package api

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// TestRoutesDocumented fails when a route is missing from docs/API.md.
// IKE_PRINT_ROUTES=1 prints them all instead.
func TestRoutesDocumented(t *testing.T) {
	a := newTestAPI(t, nil)
	routes := append([]string(nil), a.srv.Routes()...)
	sort.Strings(routes)
	if os.Getenv("IKE_PRINT_ROUTES") != "" {
		for _, r := range routes {
			t.Log(r)
		}
		return
	}
	doc, err := os.ReadFile("../../../docs/API.md")
	if err != nil {
		t.Skip("docs/API.md not found")
	}
	text := string(doc)
	var missing []string
	for _, r := range routes {
		method, path, _ := strings.Cut(r, " ")
		path = strings.TrimPrefix(path, "/v1")
		if !strings.Contains(text, "`"+method+" "+path+"`") {
			missing = append(missing, method+" "+path)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d routes are not in docs/API.md:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
