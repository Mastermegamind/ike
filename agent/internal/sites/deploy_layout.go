package sites

import (
	"context"
	"path/filepath"
)

// DeployState is how a website is deployed from Git or a container image.
type DeployState struct {
	Type           string           `json:"type"`                      // native | dockerfile | buildpack | image
	Git            *GitSource       `json:"git,omitempty"`             // nil for image deploys
	Image          string           `json:"image,omitempty"`           // image deploys: registry/name:tag
	Strategy       string           `json:"strategy"`                  // restart | blue-green
	PublicDir      string           `json:"public_dir,omitempty"`      // PHP/static: folder served inside the release, e.g. "public"
	SharedPaths    []string         `json:"shared_paths,omitempty"`    // kept between releases (.env, storage, uploads)
	BuildCommand   string           `json:"build_command,omitempty"`   // empty: detected from the project
	ReleaseCommand string           `json:"release_command,omitempty"` // runs in the new release before it goes live (migrations)
	ContainerPort  int              `json:"container_port,omitempty"`  // port the container listens on
	HealthPath     string           `json:"health_path,omitempty"`     // blue-green: checked before switching
	KeepReleases   int              `json:"keep_releases,omitempty"`
	NotifyURL      string           `json:"notify_url,omitempty"` // POSTed after each deployment
	Previews       *PreviewSettings `json:"previews,omitempty"`
	PreviewOf      string           `json:"preview_of,omitempty"` // a preview deployment of this website
	PreviewNumber  int              `json:"preview_number,omitempty"`

	Current string            `json:"current,omitempty"`  // live release id
	Color   string            `json:"color,omitempty"`    // a | b: which app instance is live (blue-green)
	AltPort int               `json:"alt_port,omitempty"` // second port for blue-green
	Colors  map[string]string `json:"colors,omitempty"`   // color → release id it runs
}

// PreviewSettings: a pull/merge request gets its own website.
type PreviewSettings struct {
	Enabled    bool   `json:"enabled"`
	BaseDomain string `json:"base_domain"` // previews live at pr-<n>.<base domain>
}

func (m *Manager) releasesDir(L layout) string { return filepath.Join(L.SiteDir, "releases") }
func (m *Manager) sharedDir(L layout) string   { return filepath.Join(L.SiteDir, "shared") }
func (m *Manager) currentLink(L layout) string { return filepath.Join(L.SiteDir, "current") }

// applyDeployLayout points the web server and app at the live release.
func (m *Manager) applyDeployLayout(s *Site, L *layout) {
	L.Root, L.ActivePort, L.ActiveUnit = L.DocRoot, s.Port, L.UnitName
	d := s.Deploy
	if d == nil || d.Current == "" {
		return
	}
	cur := m.currentLink(*L)
	L.Root = cur
	if d.PublicDir != "" {
		L.Root = filepath.Join(cur, d.PublicDir)
	}
	if L.IsApp {
		L.AppDir = cur
	}
	if d.Color == "b" {
		L.ActivePort, L.ActiveUnit = d.AltPort, L.UnitName+"-b"
		if id := d.Colors["b"]; id != "" && L.IsApp {
			L.AppDir = filepath.Join(m.releasesDir(*L), id)
		}
	} else if d.Strategy == "blue-green" && L.IsApp {
		if id := d.Colors["a"]; id != "" {
			L.AppDir = filepath.Join(m.releasesDir(*L), id)
		}
	}
	if s.Runtime == RuntimeDocker {
		L.ActivePort = s.Port
		if d.Color == "b" {
			L.ActivePort = d.AltPort
		}
	}
}

// cleanupExtras removes what a website had beyond its web server configs:
// certificates, Git keys and mirrors, deployment history and containers.
func (m *Manager) cleanupExtras(ctx context.Context, s *Site, L layout) {
	m.deleteCertificate(ctx, s)
	m.removeDeployState(ctx, s, L)
}
