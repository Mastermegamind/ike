package storage

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
)

// Target is a place to keep backups and files.
type Target struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`     // s3 | b2 | r2 | wasabi | spaces | minio | sftp | local
	Endpoint string `json:"endpoint"` // S3 endpoint URL
	Region   string `json:"region"`
	Bucket   string `json:"bucket"`
	Prefix   string `json:"prefix"` // folder inside the bucket (or path for local/sftp)
	// S3 credentials (secret sealed)
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	PathStyle bool   `json:"path_style,omitempty"`
	// SFTP
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
	User string `json:"user,omitempty"`
	// Account limits a target to one account ("" = server admin's).
	Account   string    `json:"account,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	LastCheck string    `json:"last_check,omitempty"`
}

type Manager struct {
	env   *kit.Env
	keys  *secrets.Keyring
	store kit.Store[[]Target]
}

func New(env *kit.Env, keys *secrets.Keyring) *Manager {
	m := &Manager{env: env, keys: keys}
	m.store.Path = env.P(env.Cfg.StorageFile)
	if keys != nil {
		keys.Register("storage", m.reseal)
	}
	return m
}

func (m *Manager) reseal(open, seal func(string) (string, error)) error {
	_, err := m.store.Update(func(ts *[]Target) error {
		for i := range *ts {
			v, err := open((*ts)[i].SecretKey)
			if err != nil {
				return err
			}
			if (*ts)[i].SecretKey, err = seal(v); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// Presets fill endpoints for known providers.
var Presets = map[string]string{
	"s3":     "https://s3.{region}.amazonaws.com",
	"b2":     "https://s3.{region}.backblazeb2.com",
	"wasabi": "https://s3.{region}.wasabisys.com",
	"spaces": "https://{region}.digitaloceanspaces.com",
	"r2":     "", // https://<account id>.r2.cloudflarestorage.com
	"minio":  "",
}

var (
	nameRe   = regexp.MustCompile(`^[\p{L}\p{N} ._()-]{1,60}$`)
	bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	regionRe = regexp.MustCompile(`^[a-z0-9-]{2,40}$`)
	prefixRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{0,200}$`)
	keyRe    = regexp.MustCompile(`^[A-Za-z0-9/+=._-]{8,128}$`)
	hostRe   = regexp.MustCompile(`^[a-zA-Z0-9.-]{1,253}$`)
	userRe   = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`)
)

// Public hides the secret.
func (t Target) Public() Target {
	if t.SecretKey != "" {
		t.SecretKey = "set"
	}
	return t
}

func (m *Manager) List(account string) ([]Target, error) {
	ts, err := m.store.Load()
	out := []Target{}
	for _, t := range ts {
		if account == "" || t.Account == account {
			out = append(out, t.Public())
		}
	}
	return out, err
}

// Get returns a target with its secret opened (internal use).
func (m *Manager) Get(account, id string) (*Target, error) {
	ts, err := m.store.Load()
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.ID == id && (account == "" || t.Account == account) {
			sk, err := m.keys.Unseal(t.SecretKey)
			if err != nil {
				return nil, err
			}
			t.SecretKey = sk
			return &t, nil
		}
	}
	return nil, fmt.Errorf("storage target %s %w", id, kit.ErrNotFound)
}

func (m *Manager) validate(t *Target) error {
	t.Name = strings.TrimSpace(t.Name)
	if !nameRe.MatchString(t.Name) {
		return fmt.Errorf("give the target a short name")
	}
	t.Prefix = strings.Trim(t.Prefix, "/")
	if !prefixRe.MatchString(t.Prefix) || strings.Contains(t.Prefix, "..") {
		return fmt.Errorf("folder may contain letters, digits, dots, dashes and slashes")
	}
	switch t.Kind {
	case "s3", "b2", "r2", "wasabi", "spaces", "minio":
		if !bucketRe.MatchString(t.Bucket) {
			return fmt.Errorf("bucket names are 3-63 lowercase letters, digits, dots and dashes")
		}
		if t.Region == "" && t.Kind != "r2" && t.Kind != "minio" {
			return fmt.Errorf("region is required (for example us-east-1, eu-central-003)")
		}
		if t.Region == "" {
			t.Region = "auto"
		}
		if !regionRe.MatchString(t.Region) {
			return fmt.Errorf("region looks like us-east-1")
		}
		if t.Endpoint == "" {
			p := Presets[t.Kind]
			if p == "" {
				return fmt.Errorf("enter the endpoint URL")
			}
			t.Endpoint = strings.ReplaceAll(p, "{region}", t.Region)
		}
		u, err := url.Parse(t.Endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" && u.Path != "/" {
			return fmt.Errorf("endpoint must be an https:// URL with no path")
		}
		if t.Kind == "r2" || t.Kind == "minio" {
			t.PathStyle = true
		}
		if !keyRe.MatchString(t.AccessKey) {
			return fmt.Errorf("access key ID looks wrong")
		}
	case "sftp":
		if !hostRe.MatchString(t.Host) || !userRe.MatchString(t.User) {
			return fmt.Errorf("enter the SFTP host and user name")
		}
		if t.Port == 0 {
			t.Port = 22
		}
		if t.Port < 1 || t.Port > 65535 {
			return fmt.Errorf("port must be 1-65535")
		}
		if t.Prefix == "" {
			return fmt.Errorf("enter the folder on the SFTP server")
		}
	case "local":
		if t.Account != "" {
			return fmt.Errorf("only server admins can use local folders")
		}
		if t.Prefix == "" {
			return fmt.Errorf("enter a folder, like var/backups/ike/repo or mnt/backup-disk")
		}
	default:
		return fmt.Errorf("kind must be s3, b2, r2, wasabi, spaces, minio, sftp or local")
	}
	return nil
}

// Save adds or updates a target. An empty secret keeps the stored one.
func (m *Manager) Save(ctx context.Context, account string, t Target) (Target, error) {
	t.Account = account
	if err := m.validate(&t); err != nil {
		return Target{}, err
	}
	secret := t.SecretKey
	if secret != "" && secret != "set" {
		if !keyRe.MatchString(secret) && t.Kind != "sftp" {
			return Target{}, fmt.Errorf("secret key looks wrong")
		}
	}
	var saved Target
	_, err := m.store.Update(func(ts *[]Target) error {
		idx := -1
		for i, o := range *ts {
			if t.ID != "" && o.ID == t.ID {
				if o.Account != account && account != "" {
					return fmt.Errorf("storage target %s %w", t.ID, kit.ErrNotFound)
				}
				idx = i
			}
		}
		if idx < 0 {
			if len(*ts) >= 100 {
				return fmt.Errorf("at most 100 storage targets")
			}
			t.ID, t.CreatedAt = kit.RandomHex(6), kit.Now()
		} else {
			t.CreatedAt = (*ts)[idx].CreatedAt
		}
		switch {
		case secret == "" || secret == "set":
			if idx >= 0 {
				t.SecretKey = (*ts)[idx].SecretKey
			} else if t.Kind != "sftp" && t.Kind != "local" {
				return fmt.Errorf("enter the secret access key")
			}
		default:
			sealed, err := m.keys.Seal(secret)
			if err != nil {
				return err
			}
			t.SecretKey = sealed
		}
		if idx < 0 {
			*ts = append(*ts, t)
		} else {
			(*ts)[idx] = t
		}
		saved = t
		return nil
	})
	if err != nil {
		return Target{}, err
	}
	if saved.Kind == "sftp" {
		_, _ = m.SFTPKey()
	}
	return saved.Public(), nil
}

func (m *Manager) Delete(account, id string) error {
	_, err := m.store.Update(func(ts *[]Target) error {
		for i, o := range *ts {
			if o.ID == id && (account == "" || o.Account == account) {
				*ts = append((*ts)[:i], (*ts)[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("storage target %s %w", id, kit.ErrNotFound)
	})
	return err
}

// S3 returns a client for an S3-compatible target.
func (t *Target) S3() *S3 {
	return &S3{Endpoint: t.Endpoint, Region: t.Region, Bucket: t.Bucket, AccessKey: t.AccessKey, SecretKey: t.SecretKey, PathStyle: t.PathStyle}
}

// IsS3 reports whether the target speaks the S3 API.
func (t *Target) IsS3() bool { return t.Kind != "sftp" && t.Kind != "local" }

// Check proves a target is reachable with its credentials.
func (m *Manager) Check(ctx context.Context, account, id string) error {
	t, err := m.Get(account, id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch {
	case t.IsS3():
		err = t.S3().Check(ctx)
	case t.Kind == "sftp":
		_, err = m.env.Run.Run(ctx, "ssh", "-i", m.sftpKeyPath(), "-p", fmt.Sprint(t.Port), "-o", "BatchMode=yes",
			"-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=10", t.User+"@"+t.Host, "exit")
	case t.Kind == "local":
		_, err = os.Stat(m.env.P("/" + t.Prefix))
		if os.IsNotExist(err) {
			err = os.MkdirAll(m.env.P("/"+t.Prefix), 0o700)
		}
	}
	status := "ok " + kit.Now().Format(time.RFC3339)
	if err != nil {
		status = "error: " + err.Error()
	}
	_, _ = m.store.Update(func(ts *[]Target) error {
		for i := range *ts {
			if (*ts)[i].ID == id {
				(*ts)[i].LastCheck = status
			}
		}
		return nil
	})
	return err
}

// ResticEnv returns the repository location and credentials restic needs
// for a target. sub is a folder inside the target ("server", "acct/shop").
func (m *Manager) ResticEnv(account, id, sub string) (repo string, env []string, err error) {
	t, err := m.Get(account, id)
	if err != nil {
		return "", nil, err
	}
	path := strings.Trim(strings.Join([]string{t.Prefix, sub}, "/"), "/")
	switch {
	case t.IsS3():
		u, _ := url.Parse(t.Endpoint)
		repo = "s3:" + u.Scheme + "://" + u.Host + "/" + t.Bucket + "/" + path
		env = []string{"AWS_ACCESS_KEY_ID=" + t.AccessKey, "AWS_SECRET_ACCESS_KEY=" + t.SecretKey, "AWS_DEFAULT_REGION=" + t.Region}
	case t.Kind == "sftp":
		repo = fmt.Sprintf("sftp:%s@%s:/%s", t.User, t.Host, path)
		env = []string{"IKE_SFTP_ARGS=-i " + m.sftpKeyPath() + " -p " + fmt.Sprint(t.Port) + " -o BatchMode=yes -o StrictHostKeyChecking=accept-new"}
	case t.Kind == "local":
		repo = "/" + path
	}
	return repo, env, nil
}

// ---- SFTP key ----

func (m *Manager) sftpKeyPath() string { return m.env.P("/etc/ike/backup_ed25519") }

// SFTPKey returns this server's public key for SFTP targets, making it once.
func (m *Manager) SFTPKey() (string, error) {
	p := m.sftpKeyPath()
	if b, err := os.ReadFile(p + ".pub"); err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if _, err := m.env.Run.Run(context.Background(), "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "ike-backup@"+m.env.Cfg.Hostname, "-f", p); err != nil {
		return "", err
	}
	b, err := os.ReadFile(p + ".pub")
	return strings.TrimSpace(string(b)), err
}
