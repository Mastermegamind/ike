// Package config loads the agent configuration written by the installer.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type Config struct {
	// Root is prepended to every managed path. "/" in production; a temp dir in tests.
	Root string `json:"-"`
	// DryRun records commands instead of executing them.
	DryRun bool `json:"-"`

	WebUser       string `json:"web_user"`        // www-data or nginx
	WebGroup      string `json:"web_group"`       // group the web server runs as
	NginxSitesDir string `json:"nginx_sites_dir"` // included from nginx.conf
	HomeBase      string `json:"home_base"`       // usually /home
	StateDir      string `json:"state_dir"`       // site metadata JSON files
	DefaultPHP    string `json:"default_php"`
}

func Default() Config {
	return Config{
		Root:          "/",
		WebUser:       "www-data",
		WebGroup:      "www-data",
		NginxSitesDir: "/etc/nginx/hostpanel/sites",
		HomeBase:      "/home",
		StateDir:      "/var/lib/hostpanel/sites",
		DefaultPHP:    "8.3",
	}
}

// Load reads a JSON config file on top of the defaults. A missing file is not an error.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}
