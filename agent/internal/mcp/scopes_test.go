package mcp

import (
	"os"
	"testing"
)

func TestToolFiltering(t *testing.T) {
	count := func(s *Server) (areas map[string]int, writes int) {
		areas = map[string]int{}
		for _, tl := range s.toolList() {
			areas[tl.area]++
			if tl.write {
				writes++
			}
		}
		return
	}
	// Local socket, read-only: no writes and no panel tools.
	a, w := count(&Server{})
	if w != 0 || a["billing"] == 0 {
		t.Fatalf("read-only socket: %v writes=%d", a, w)
	}
	for _, tl := range (&Server{AllowWrite: true}).toolList() {
		if tl.panel {
			t.Fatalf("%s needs the panel but was offered on the socket", tl.Name)
		}
	}
	// Limited token: DNS write, mail read.
	s := &Server{AllowWrite: true, Panel: true, Scopes: map[string]string{"dns": "write", "mail": "read"}}
	a, _ = count(s)
	for area := range a {
		if area != "dns" && area != "mail" && area != "" {
			t.Fatalf("limited token offered %s tools", area)
		}
	}
	for _, tl := range s.toolList() {
		if tl.area == "mail" && tl.write {
			t.Fatalf("mail is read-only but %s was offered", tl.Name)
		}
	}
	if s.hidden(tool{Name: "x", area: "dns", write: true}) != "" {
		t.Fatal("dns writes should be allowed")
	}
	// Read-only token hides writes even with --allow-write.
	if _, w := count(&Server{AllowWrite: true, Panel: true, ReadOnlyToken: true}); w != 0 {
		t.Fatalf("read-only token offered %d write tools", w)
	}
	// --tools.
	a, _ = count(&Server{Groups: map[string]bool{"": true, "files": true}})
	if a["files"] == 0 || len(a) > 2 {
		t.Fatalf("--tools files: %v", a)
	}
	// Every tool has a known area and a unique name.
	seen := map[string]bool{}
	known := map[string]bool{"": true}
	for _, ar := range Areas {
		known[ar] = true
	}
	for _, tl := range (&Server{}).allTools() {
		if seen[tl.Name] {
			t.Fatalf("duplicate tool %s", tl.Name)
		}
		seen[tl.Name] = true
		if !known[tl.area] {
			t.Fatalf("%s has unknown area %q", tl.Name, tl.area)
		}
	}
}

// IKE_PRINT_TOOLS=1 prints every tool as "area|name|write|panel" (for docs/MCP.md).
func TestPrintTools(t *testing.T) {
	if os.Getenv("IKE_PRINT_TOOLS") == "" {
		t.Skip()
	}
	for _, tl := range (&Server{}).allTools() {
		t.Logf("TOOL|%s|%s|%v|%v", tl.area, tl.Name, tl.write, tl.panel)
	}
}
