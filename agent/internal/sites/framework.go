package sites

// Framework detection for JavaScript projects, so a Git deploy of a Next.js,
// Nuxt, SvelteKit, Astro, Remix, NestJS, Vite (React, Vue, Svelte, Solid),
// Create React App, Angular or Gatsby project needs no settings:
//
//   - static websites serve the build output folder (dist, build, out ...)
//     and, for single-page apps, send unknown paths to index.html so client
//     routing works;
//   - Node.js websites start with the framework's own server, listening on
//     the PORT Ike gives the app.
//
// Anything set by hand (public folder, start command, build command) wins.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Framework is what a project was built with.
type Framework struct {
	ID   string `json:"id"`   // next, nuxt, sveltekit, astro, remix, nest, vite, cra, angular, gatsby, express
	Name string `json:"name"` // shown in the panel
	// OutputDir is the folder a static build writes (served by the web server).
	OutputDir string `json:"output_dir,omitempty"`
	// Start runs the framework's server (Node.js websites).
	Start string `json:"start,omitempty"`
	// SPA: unknown paths are answered with index.html (client-side routing).
	SPA bool `json:"spa,omitempty"`
	// Static: the build is plain files (a static website can serve it).
	Static bool `json:"static,omitempty"`
}

type packageJSON struct {
	Name         string            `json:"name"`
	Scripts      map[string]string `json:"scripts"`
	Dependencies map[string]string `json:"dependencies"`
	DevDeps      map[string]string `json:"devDependencies"`
}

func (p packageJSON) has(dep string) bool {
	_, a := p.Dependencies[dep]
	_, b := p.DevDeps[dep]
	return a || b
}

var nextExportRe = regexp.MustCompile(`output\s*:\s*['"]export['"]`)

// DetectFramework looks at a project folder (a release, or public_html).
// It returns nil when there's no package.json or no known framework.
func DetectFramework(dir string) *Framework {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || len(b) > 1<<20 {
		return nil
	}
	var p packageJSON
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	read := func(names ...string) string {
		for _, n := range names {
			if b, err := os.ReadFile(filepath.Join(dir, n)); err == nil && len(b) < 1<<20 {
				return string(b)
			}
		}
		return ""
	}
	start := func(def string) string {
		if p.Scripts["start"] != "" {
			return "npm start"
		}
		return def
	}
	switch {
	case p.has("next"):
		f := &Framework{ID: "next", Name: "Next.js", Start: start("npx next start")}
		if nextExportRe.MatchString(read("next.config.js", "next.config.mjs", "next.config.ts")) {
			f.OutputDir, f.Static = "out", true
		}
		return f
	case p.has("nuxt"):
		f := &Framework{ID: "nuxt", Name: "Nuxt", Start: "node .output/server/index.mjs"}
		if strings.Contains(p.Scripts["build"], "generate") {
			f.OutputDir, f.Static = ".output/public", true
		}
		return f
	case p.has("@sveltejs/kit"):
		f := &Framework{ID: "sveltekit", Name: "SvelteKit", Start: "node build"}
		if p.has("@sveltejs/adapter-static") {
			f.OutputDir, f.Static = "build", true
		}
		return f
	case p.has("astro"):
		f := &Framework{ID: "astro", Name: "Astro", OutputDir: "dist", Static: true}
		if p.has("@astrojs/node") {
			f.Static, f.OutputDir, f.Start = false, "", "node ./dist/server/entry.mjs"
		}
		return f
	case p.has("@remix-run/serve") || p.has("@remix-run/node") || p.has("@react-router/serve"):
		return &Framework{ID: "remix", Name: "Remix", Start: start("npx remix-serve ./build/server/index.js")}
	case p.has("@nestjs/core"):
		return &Framework{ID: "nest", Name: "NestJS", Start: start("node dist/main")}
	case p.has("gatsby"):
		return &Framework{ID: "gatsby", Name: "Gatsby", OutputDir: "public", Static: true}
	case p.has("@angular/core"):
		out := "dist"
		if name := angularProject(read("angular.json")); name != "" {
			out = "dist/" + name + "/browser"
		}
		return &Framework{ID: "angular", Name: "Angular", OutputDir: out, Static: true, SPA: true}
	case p.has("react-scripts"):
		return &Framework{ID: "cra", Name: "Create React App", OutputDir: "build", Static: true, SPA: true}
	case p.has("vite"):
		name := "Vite"
		for dep, label := range map[string]string{"react": "React", "vue": "Vue", "svelte": "Svelte", "solid-js": "Solid", "preact": "Preact"} {
			if p.has(dep) {
				name = label + " (Vite)"
			}
		}
		return &Framework{ID: "vite", Name: name, OutputDir: "dist", Static: true, SPA: true}
	case p.has("express") || p.has("fastify") || p.has("koa") || p.has("hono"):
		return &Framework{ID: "express", Name: "Node.js server", Start: start("")}
	}
	return nil
}

// angularProject is the default project's name in angular.json.
func angularProject(s string) string {
	var a struct {
		DefaultProject string                     `json:"defaultProject"`
		Projects       map[string]json.RawMessage `json:"projects"`
	}
	if json.Unmarshal([]byte(s), &a) != nil {
		return ""
	}
	if a.DefaultProject != "" {
		return a.DefaultProject
	}
	if len(a.Projects) == 1 {
		for k := range a.Projects {
			return k
		}
	}
	return ""
}

// safeRel accepts a relative folder inside the project.
func safeRel(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && !strings.Contains(p, "..") && !strings.ContainsAny(p, " \t\n\"'{};$")
}
