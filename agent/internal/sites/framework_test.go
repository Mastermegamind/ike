package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/config"
)

func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectFramework(t *testing.T) {
	cases := []struct {
		files          map[string]string
		id, out, start string
		static, spa    bool
	}{
		{map[string]string{"package.json": `{"dependencies":{"next":"15","react":"19"},"scripts":{"build":"next build","start":"next start"}}`}, "next", "", "npm start", false, false},
		{map[string]string{"package.json": `{"dependencies":{"next":"15"}}`, "next.config.mjs": `export default { output: 'export' }`}, "next", "out", "npx next start", true, false},
		{map[string]string{"package.json": `{"devDependencies":{"nuxt":"3"}}`}, "nuxt", "", "node .output/server/index.mjs", false, false},
		{map[string]string{"package.json": `{"devDependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-node":"5"}}`}, "sveltekit", "", "node build", false, false},
		{map[string]string{"package.json": `{"devDependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-static":"3"}}`}, "sveltekit", "build", "node build", true, false},
		{map[string]string{"package.json": `{"dependencies":{"astro":"5"}}`}, "astro", "dist", "", true, false},
		{map[string]string{"package.json": `{"dependencies":{"astro":"5","@astrojs/node":"9"}}`}, "astro", "", "node ./dist/server/entry.mjs", false, false},
		{map[string]string{"package.json": `{"dependencies":{"react":"19"},"devDependencies":{"vite":"6"}}`}, "vite", "dist", "", true, true},
		{map[string]string{"package.json": `{"dependencies":{"react-scripts":"5"}}`}, "cra", "build", "", true, true},
		{map[string]string{"package.json": `{"dependencies":{"@angular/core":"19"}}`, "angular.json": `{"projects":{"shop":{}}}`}, "angular", "dist/shop/browser", "", true, true},
		{map[string]string{"package.json": `{"dependencies":{"@nestjs/core":"11"}}`}, "nest", "", "node dist/main", false, false},
		{map[string]string{"package.json": `{"dependencies":{"express":"5"},"scripts":{"start":"node index.js"}}`}, "express", "", "npm start", false, false},
	}
	for _, c := range cases {
		f := DetectFramework(project(t, c.files))
		if f == nil {
			t.Errorf("%s: not detected", c.id)
			continue
		}
		if f.ID != c.id || f.OutputDir != c.out || f.Start != c.start || f.Static != c.static || f.SPA != c.spa {
			t.Errorf("%s: %+v", c.id, f)
		}
	}
	if f := DetectFramework(project(t, map[string]string{"package.json": `{"dependencies":{"lodash":"4"}}`})); f != nil {
		t.Errorf("plain package detected as %+v", f)
	}
	if f := DetectFramework(project(t, map[string]string{"index.html": "hi"})); f != nil {
		t.Error("no package.json, still detected")
	}
	if f := DetectFramework(project(t, map[string]string{"package.json": `{nope`})); f != nil {
		t.Error("broken package.json detected")
	}
}

func TestFrameworkLayoutAndSPA(t *testing.T) {
	for _, stack := range []string{"nginx", "apache", "caddy", "openlitespeed"} {
		m, _, _ := newTestManager(t, func(c *config.Config) {
			c.WebStack, c.DefaultWebServer = stack, stack
			if stack == "apache" {
				c.ApacheListen = "*:80"
			}
		})
		mustAccount(t, m, "web")
		s, err := m.Create(ctx, CreateRequest{Domain: "spa.test", Account: "web", Runtime: "static"})
		if err != nil {
			t.Fatalf("%s: %v", stack, err)
		}
		// A deployed Vite React app.
		s.Deploy = &DeployState{Type: "native", Current: "r1", Framework: &Framework{ID: "vite", Name: "React (Vite)", OutputDir: "dist", Static: true, SPA: true}}
		L := m.layout(s)
		if !strings.HasSuffix(L.Root, "/current/dist") || !L.SPA {
			t.Fatalf("%s: root %s spa %v", stack, L.Root, L.SPA)
		}
		var conf string
		switch stack {
		case "nginx":
			conf = string(renderBytes(nginxTmpl, L))
			if !strings.Contains(conf, "try_files $uri $uri/ /index.html;") {
				t.Errorf("nginx:\n%s", conf)
			}
		case "apache":
			conf = string(renderBytes(apacheTmpl, L))
			if !strings.Contains(conf, "FallbackResource /index.html") {
				t.Errorf("apache:\n%s", conf)
			}
		case "caddy":
			conf = string(renderBytes(caddyTmpl, L))
			if !strings.Contains(conf, "try_files {path} {path}/ /index.html") {
				t.Errorf("caddy:\n%s", conf)
			}
		case "openlitespeed":
			conf = string(renderBytes(olsVhostTmpl, L))
			if !strings.Contains(conf, "RewriteRule ^ /index.html [L]") {
				t.Errorf("ols:\n%s", conf)
			}
		}
		// A public folder set by hand wins.
		s.Deploy.PublicDir = "public"
		if L := m.layout(s); !strings.HasSuffix(L.Root, "/current/public") || L.SPA {
			t.Errorf("%s: PublicDir ignored: %s %v", stack, L.Root, L.SPA)
		}
		// SPA by hand on a site that isn't deployed.
		if _, err := m.Update(ctx, "spa.test", UpdateRequest{SPA: ptrTrue()}); err != nil {
			t.Fatalf("%s: %v", stack, err)
		}
		got, _ := m.Get("spa.test")
		if !got.SPA || !m.layout(got).SPA {
			t.Errorf("%s: SPA toggle not saved", stack)
		}
	}
	// Node.js: the framework's server starts unless the website set its own command.
	m, _, root := newTestManager(t)
	addRuntime(t, root, "node", "22", "node")
	mustAccount(t, m, "app")
	s, err := m.Create(ctx, CreateRequest{Domain: "next.test", Account: "app", Runtime: "node"})
	if err != nil {
		t.Fatal(err)
	}
	s.Deploy = &DeployState{Type: "native", Current: "r1", Framework: &Framework{ID: "next", Name: "Next.js", Start: "npm start"}}
	if L := m.layout(s); L.StartCommand != "npm start" {
		t.Errorf("start %q", L.StartCommand)
	}
	s.StartCommand = "node custom.js"
	if L := m.layout(s); L.StartCommand != "node custom.js" {
		t.Errorf("own start command replaced: %q", L.StartCommand)
	}
	if _, err := m.Update(ctx, "next.test", UpdateRequest{SPA: ptrTrue()}); err == nil {
		t.Error("SPA accepted on an app")
	}
}

func ptrTrue() *bool { b := true; return &b }
