package mcp

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestToolTable writes the tool table for docs/MCP.md when IKE_MCP_DOC is
// a file path: IKE_MCP_DOC=/tmp/tools.md go test -run TestToolTable ./internal/mcp
func TestToolTable(t *testing.T) {
	out := os.Getenv("IKE_MCP_DOC")
	if out == "" {
		t.Skip("set IKE_MCP_DOC to a file to write the table")
	}
	s := &Server{}
	areas := []struct{ id, label string }{{"sites", "Websites"}, {"accounts", "Accounts"}, {"dns", "DNS"}, {"mail", "Email"},
		{"databases", "Databases"}, {"files", "Files"}, {"backups", "Backups"}, {"containers", "Containers"}, {"security", "Security"},
		{"billing", "Packages and billing"}, {"server", "Server"}, {"", "Always available"}}
	all := s.allTools()
	var b strings.Builder
	fmt.Fprintf(&b, "%d tools. Tools marked * need the panel connection.\n\n| Area (`--tools` name) | Read | Change (need `--allow-write`) |\n|---|---|---|\n", len(all))
	seen := 0
	for _, a := range areas {
		var read, change []string
		for _, tl := range all {
			if tl.area != a.id {
				continue
			}
			seen++
			n := "`" + tl.Name + "`"
			if tl.panel {
				n += "*"
			}
			if tl.write {
				change = append(change, n)
			} else {
				read = append(read, n)
			}
		}
		id := a.id
		if id == "" {
			id = "always"
		}
		fmt.Fprintf(&b, "| %s (%s) | %s | %s |\n", a.label, map[bool]string{true: "always", false: "`" + id + "`"}[a.id == ""], strings.Join(read, ", "), strings.Join(change, ", "))
	}
	if seen != len(all) {
		t.Fatalf("%d of %d tools have an area in the table", seen, len(all))
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
