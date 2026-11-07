package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectBuildPackageManagers(t *testing.T) {
	m, _, root := newTestManager(t)
	cases := []struct {
		runtime string
		files   map[string]string
		want    []string
	}{
		{"static", map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`, "package-lock.json": "{}"}, []string{"npm ci", "npm run build"}},
		{"static", map[string]string{"package.json": `{"packageManager":"pnpm@9.12.0+sha512.abc","scripts":{"build":"x"}}`}, []string{"npx --yes pnpm@9.12.0 install --frozen-lockfile", "npx --yes pnpm@9.12.0 run build"}},
		{"node", map[string]string{"package.json": `{}`, "yarn.lock": ""}, []string{"npx --yes yarn@1 install --frozen-lockfile"}},
		{"node", map[string]string{"package.json": `{"packageManager":"yarn@4.5.1","scripts":{"build":"x"}}`, "yarn.lock": "", ".yarnrc.yml": ""}, []string{"npx --yes @yarnpkg/cli-dist@4.5.1 install --immutable", "npx --yes @yarnpkg/cli-dist@4.5.1 run build"}},
		{"node", map[string]string{"package.json": `{"scripts":{"build":"x"}}`, "bun.lock": ""}, []string{"npx --yes bun@1 install --frozen-lockfile", "npx --yes bun@1 run build"}},
		{"node", map[string]string{"package.json": `{"packageManager":"pnpm@latest; rm -rf /"}`}, []string{"npx --yes pnpm@9 install --frozen-lockfile"}},
		{"php", map[string]string{"composer.json": "{}", ".phive/phars.xml": "<phive/>"}, []string{"composer install --no-dev", "phive --no-progress install --copy"}},
		{"python", map[string]string{"uv.lock": "", "pyproject.toml": ""}, []string{"python3 -m venv .venv", "uv sync --frozen --no-dev"}},
		{"python", map[string]string{"poetry.lock": "", "pyproject.toml": ""}, []string{"POETRY_VIRTUALENVS_IN_PROJECT=true poetry install --only main"}},
		{"python", map[string]string{"Pipfile": "", "Pipfile.lock": ""}, []string{"PIPENV_VENV_IN_PROJECT=1 pipenv sync"}},
		{"python", map[string]string{"Pipfile": ""}, []string{"pipenv install --deploy"}},
		{"python", map[string]string{"requirements.txt": ""}, []string{".venv/bin/pip install --no-input -r requirements.txt"}},
		{"java", map[string]string{"mvnw": "", "pom.xml": ""}, []string{"sh ./mvnw -B -q -DskipTests package"}},
		{"java", map[string]string{"build.gradle.kts": ""}, []string{"gradle --no-daemon build -x test"}},
		{"go", map[string]string{"go.mod": ""}, []string{"go mod download", "go build -o app ."}},
		{"dart", map[string]string{"pubspec.yaml": ""}, []string{"dart pub get"}},
	}
	for i, c := range cases {
		rel := filepath.Join("/srv/rel", string(rune('a'+i)))
		for name, body := range c.files {
			p := filepath.Join(root, rel, name)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte(body), 0o644)
		}
		L := layout{Runtime: c.runtime, Version: "8.3"}
		got := strings.Join(m.detectBuild(L, rel), "\n")
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s %v: missing %q in\n%s", c.runtime, c.files, w, got)
			}
		}
		if strings.Contains(got, "rm -rf") {
			t.Errorf("unsafe version passed through: %s", got)
		}
	}
}
