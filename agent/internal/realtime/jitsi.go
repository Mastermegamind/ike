package realtime

// Jitsi Meet as a one-click server app: the official Docker images (web,
// prosody, jicofo, videobridge) run as one Compose project. The web container
// listens on 127.0.0.1:8000 behind an Ike website (meet.example.com) with a
// certificate; the videobridge takes media on 10000/udp; Ike's TURN server
// (when on) helps callers behind strict firewalls.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/validate"
)

const (
	jitsiDir     = "/var/lib/ike/jitsi"
	jitsiProject = "ike-jitsi"
	jitsiHTTP    = 8000
	jitsiMedia   = 10000
	dockerBinary = "/usr/bin/docker"
)

// JitsiSettings configure Jitsi Meet.
type JitsiSettings struct {
	Enabled bool   `json:"enabled"`
	Domain  string `json:"domain"`
	Account string `json:"account"` // owner of its website
	UseTurn bool   `json:"use_turn"`
	// Lobby makes everyone wait to be let in by a moderator.
	Lobby bool `json:"lobby"`
	// Version is the image tag (default "stable").
	Version   string    `json:"version,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// JitsiInfo is what the panel shows.
type JitsiInfo struct {
	JitsiSettings
	DockerReady bool   `json:"docker_ready"`
	Running     string `json:"running"` // active | inactive | partial
	URL         string `json:"url,omitempty"`
}

func (m *Manager) dockerReady() bool { return m.env.Installed(dockerBinary) }

// JitsiStatus reports Jitsi Meet.
func (m *Manager) JitsiStatus(ctx context.Context) (*JitsiInfo, error) {
	m.mu.Lock()
	st, err := m.load()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	info := &JitsiInfo{JitsiSettings: st.Jitsi, DockerReady: m.dockerReady(), Running: "inactive"}
	if st.Jitsi.Domain != "" {
		info.URL = "https://" + st.Jitsi.Domain
	}
	if st.Jitsi.Enabled && info.DockerReady {
		out, err := m.env.Run.Run(ctx, "docker", "compose", "-p", jitsiProject, "ps", "--status", "running", "-q")
		n := len(strings.Fields(string(out)))
		switch {
		case err != nil || n == 0:
			info.Running = "inactive"
		case n >= 4:
			info.Running = "active"
		default:
			info.Running = "partial"
		}
	}
	return info, nil
}

// SetJitsi saves the settings, writes the Compose project and starts or
// stops it. The website and its certificate are made like LiveKit's.
func (m *Manager) SetJitsi(ctx context.Context, req JitsiSettings) (*JitsiInfo, []string, error) {
	if !m.dockerReady() {
		return nil, nil, fmt.Errorf("Jitsi Meet runs in Docker, which %w; add it with the installer's --docker", kit.ErrNotInstalled)
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if req.Enabled || req.Domain != "" {
		if err := validate.Domain(req.Domain); err != nil {
			return nil, nil, fmt.Errorf("Jitsi domain: %w", err)
		}
		if err := m.env.CheckAccount(req.Account); err != nil {
			return nil, nil, fmt.Errorf("the account that owns Jitsi's website: %w", err)
		}
	}
	if req.Version == "" {
		req.Version = "stable"
	}
	if !imageTagRe.MatchString(req.Version) {
		return nil, nil, fmt.Errorf("the image version is a tag like stable or stable-10184")
	}
	m.mu.Lock()
	st, err := m.load()
	if err != nil {
		m.mu.Unlock()
		return nil, nil, err
	}
	if req.UseTurn && !st.Turn.Enabled {
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("switch the TURN server on first, or untick “use TURN”")
	}
	wasOn := st.Jitsi.Enabled
	req.UpdatedAt = kit.Now()
	st.Jitsi = req
	var notes []string
	if req.Enabled {
		turnSecret := ""
		if req.UseTurn {
			if turnSecret, err = m.unseal(st.Turn.Secret); err != nil {
				m.mu.Unlock()
				return nil, nil, err
			}
		}
		if err := m.writeJitsi(req, st.Turn, turnSecret); err != nil {
			m.mu.Unlock()
			return nil, nil, err
		}
		if err := m.openPort(ctx, fmt.Sprint(jitsiMedia), "udp", "Jitsi media"); err != nil {
			notes = append(notes, "Could not open 10000/udp in the firewall: "+err.Error())
		}
		if _, err := m.env.Run.Run(ctx, "docker", "compose", "-p", jitsiProject, "-f", filepath.Join(jitsiDir, "docker-compose.yml"), "up", "-d", "--pull", "missing"); err != nil {
			m.mu.Unlock()
			return nil, nil, fmt.Errorf("Jitsi Meet didn't start: %w", err)
		}
	} else if wasOn {
		_, _ = m.env.Run.Run(ctx, "docker", "compose", "-p", jitsiProject, "-f", filepath.Join(jitsiDir, "docker-compose.yml"), "down")
		m.closePorts(ctx, []string{fmt.Sprintf("%d/udp", jitsiMedia)})
	}
	if err := m.save(st); err != nil {
		m.mu.Unlock()
		return nil, nil, err
	}
	m.mu.Unlock()
	if req.Enabled && m.ProxySite != nil {
		if note, err := m.ProxySite(ctx, req.Account, req.Domain, jitsiHTTP); err != nil {
			notes = append(notes, "Jitsi's website couldn't be set up: "+err.Error())
		} else if note != "" {
			notes = append(notes, note)
		}
	}
	m.env.Publish("realtime.jitsi.changed", req.Domain, map[string]any{"enabled": req.Enabled})
	info, err := m.JitsiStatus(ctx)
	return info, notes, err
}

var imageTagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,40}$`)

// writeJitsi writes the .env (secrets made once and kept) and the Compose file.
func (m *Manager) writeJitsi(j JitsiSettings, t TurnSettings, turnSecret string) error {
	envPath := m.env.P(filepath.Join(jitsiDir, ".env"))
	old := readEnvFile(envPath)
	secret := func(k string) string {
		if v := old[k]; v != "" {
			return v
		}
		return kit.RandomHex(16)
	}
	vars := [][2]string{
		{"JITSI_IMAGE_VERSION", j.Version},
		{"CONFIG", filepath.Join(jitsiDir, "config")},
		{"PUBLIC_URL", "https://" + j.Domain},
		{"HTTP_PORT", fmt.Sprint(jitsiHTTP)},
		{"DISABLE_HTTPS", "1"},
		{"ENABLE_HTTP_REDIRECT", "0"},
		{"ENABLE_LETSENCRYPT", "0"},
		{"ENABLE_XMPP_WEBSOCKET", "1"},
		{"ENABLE_COLIBRI_WEBSOCKET", "1"},
		{"ENABLE_PREJOIN_PAGE", "1"},
		{"ENABLE_LOBBY", boolEnv(j.Lobby)},
		{"ENABLE_AUTH", "0"},
		{"TZ", "Africa/Lagos"},
		{"JVB_PORT", fmt.Sprint(jitsiMedia)},
		{"JVB_ADVERTISE_IPS", m.env.Cfg.PublicIPv4},
		{"JICOFO_AUTH_PASSWORD", secret("JICOFO_AUTH_PASSWORD")},
		{"JVB_AUTH_PASSWORD", secret("JVB_AUTH_PASSWORD")},
		{"JIGASI_XMPP_PASSWORD", secret("JIGASI_XMPP_PASSWORD")},
		{"JIBRI_RECORDER_PASSWORD", secret("JIBRI_RECORDER_PASSWORD")},
		{"JIBRI_XMPP_PASSWORD", secret("JIBRI_XMPP_PASSWORD")},
	}
	if turnSecret != "" && t.Domain != "" {
		vars = append(vars, [2]string{"TURN_CREDENTIALS", turnSecret}, [2]string{"TURN_HOST", t.Domain},
			[2]string{"TURN_PORT", fmt.Sprint(TurnPort)}, [2]string{"TURN_TRANSPORT", "udp"})
		if t.TLS {
			vars = append(vars, [2]string{"TURNS_HOST", t.Domain}, [2]string{"TURNS_PORT", fmt.Sprint(TurnTLSPort)})
		}
	}
	var b strings.Builder
	b.WriteString("# Written by Ike. Change Jitsi Meet in the panel under Server → Real-time media.\n")
	for _, kv := range vars {
		fmt.Fprintf(&b, "%s=%s\n", kv[0], kv[1])
	}
	if err := kit.WriteFile(envPath, []byte(b.String()), 0o600); err != nil {
		return err
	}
	for _, d := range []string{"web", "prosody/config", "prosody/prosody-plugins-custom", "jicofo", "jvb"} {
		if err := os.MkdirAll(m.env.P(filepath.Join(jitsiDir, "config", d)), 0o750); err != nil {
			return err
		}
	}
	return kit.WriteFile(m.env.P(filepath.Join(jitsiDir, "docker-compose.yml")), []byte(jitsiCompose), 0o644)
}

func boolEnv(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(k, "#") {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// jitsiCompose follows docker-jitsi-meet's layout; every service reads the
// .env Ike writes. Only the web port (on localhost) and the media port are
// published.
const jitsiCompose = `# Written by Ike. Change Jitsi Meet in the panel, not here.
services:
  web:
    image: jitsi/web:${JITSI_IMAGE_VERSION:-stable}
    restart: unless-stopped
    ports:
      - "127.0.0.1:${HTTP_PORT}:80"
    volumes:
      - ${CONFIG}/web:/config:Z
    env_file: .env
    networks:
      meet.jitsi:
  prosody:
    image: jitsi/prosody:${JITSI_IMAGE_VERSION:-stable}
    restart: unless-stopped
    expose:
      - "5222"
      - "5347"
      - "5280"
    volumes:
      - ${CONFIG}/prosody/config:/config:Z
      - ${CONFIG}/prosody/prosody-plugins-custom:/prosody-plugins-custom:Z
    env_file: .env
    networks:
      meet.jitsi:
        aliases:
          - xmpp.meet.jitsi
  jicofo:
    image: jitsi/jicofo:${JITSI_IMAGE_VERSION:-stable}
    restart: unless-stopped
    volumes:
      - ${CONFIG}/jicofo:/config:Z
    env_file: .env
    depends_on:
      - prosody
    networks:
      meet.jitsi:
  jvb:
    image: jitsi/jvb:${JITSI_IMAGE_VERSION:-stable}
    restart: unless-stopped
    ports:
      - "${JVB_PORT}:${JVB_PORT}/udp"
    volumes:
      - ${CONFIG}/jvb:/config:Z
    env_file: .env
    depends_on:
      - prosody
    networks:
      meet.jitsi:
networks:
  meet.jitsi:
`
