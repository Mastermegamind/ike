package files

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/kit"
)

// Share is a public download link for a file or folder: /s/<token> on the
// panel's address. It can expire, need a password, and stop after a number
// of downloads.
type Share struct {
	Token        string    `json:"token"`
	Account      string    `json:"account"`
	Path         string    `json:"path"` // relative to the home
	Folder       bool      `json:"folder"`
	ExpiresAt    time.Time `json:"expires_at"`
	PasswordHash string    `json:"password_hash,omitempty"`
	PasswordSalt string    `json:"password_salt,omitempty"`
	MaxDownloads int       `json:"max_downloads"` // 0 = unlimited
	Downloads    int       `json:"downloads"`
	CreatedBy    string    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// PublicShare is what the owner sees.
type PublicShare struct {
	Share
	HasPassword bool   `json:"has_password"`
	URLPath     string `json:"url_path"`
}

var shareMu sync.Mutex

func (m *Manager) shareFile() string {
	return m.env.P(filepath.Join(m.env.Cfg.ShareDir, "shares.json"))
}

func (m *Manager) loadShares() ([]Share, error) {
	var s []Share
	err := kit.ReadJSON(m.shareFile(), &s)
	if err == kit.ErrNotFound {
		err = nil
	}
	return s, err
}

func hashSharePassword(salt, pw string) string {
	h := sha256.New()
	// many rounds so a stolen share file is slow to brute-force
	sum := []byte(salt + pw)
	for i := 0; i < 100000; i++ {
		h.Reset()
		h.Write(sum)
		h.Write([]byte(pw))
		sum = h.Sum(nil)
	}
	return hex.EncodeToString(sum)
}

func (s Share) public() PublicShare {
	p := PublicShare{Share: s, HasPassword: s.PasswordHash != "", URLPath: "/s/" + s.Token}
	p.PasswordHash, p.PasswordSalt = "", ""
	return p
}

// ShareRequest creates a link.
type ShareRequest struct {
	Path         string `json:"path"`
	ExpiresHours int    `json:"expires_hours"` // default 72, at most 30 days
	Password     string `json:"password"`
	MaxDownloads int    `json:"max_downloads"`
}

func (m *Manager) CreateShare(ctx context.Context, account, actor string, r ShareRequest) (*PublicShare, error) {
	abs, err := m.Clean(account, r.Path)
	if err != nil {
		return nil, err
	}
	if r.Path == "" {
		return nil, fmt.Errorf("the whole home folder can't be shared")
	}
	if r.ExpiresHours == 0 {
		r.ExpiresHours = 72
	}
	if r.ExpiresHours < 1 || r.ExpiresHours > 720 {
		return nil, fmt.Errorf("links last 1 hour to 30 days")
	}
	if r.MaxDownloads < 0 || r.MaxDownloads > 10000 {
		return nil, fmt.Errorf("download limit must be 0 (none) to 10000")
	}
	if r.Password != "" && (len(r.Password) < 6 || len(r.Password) > 200) {
		return nil, fmt.Errorf("password must be 6-200 characters")
	}
	// must exist and be readable by the account
	folder := false
	if _, err := m.as(ctx, account, nil, nil, 0, "test", "-r", abs); err != nil {
		return nil, fmt.Errorf("%s %w", r.Path, kit.ErrNotFound)
	}
	if _, err := m.as(ctx, account, nil, nil, 0, "test", "-d", abs); err == nil {
		folder = true
	}
	s := Share{Token: kit.RandomHex(16), Account: account, Path: strings.Trim(r.Path, "/"), Folder: folder,
		ExpiresAt: kit.Now().Add(time.Duration(r.ExpiresHours) * time.Hour), MaxDownloads: r.MaxDownloads, CreatedBy: actor, CreatedAt: kit.Now()}
	if r.Password != "" {
		s.PasswordSalt = kit.RandomHex(8)
		s.PasswordHash = hashSharePassword(s.PasswordSalt, r.Password)
	}
	shareMu.Lock()
	defer shareMu.Unlock()
	all, err := m.loadShares()
	if err != nil {
		return nil, err
	}
	// drop expired ones while here
	keep := all[:0]
	n := 0
	for _, o := range all {
		if o.ExpiresAt.After(kit.Now()) {
			keep = append(keep, o)
			if o.Account == account {
				n++
			}
		}
	}
	if n >= 200 {
		return nil, fmt.Errorf("an account can have at most 200 active links")
	}
	keep = append(keep, s)
	if err := kit.WriteJSON(m.shareFile(), keep); err != nil {
		return nil, err
	}
	p := s.public()
	return &p, nil
}

func (m *Manager) Shares(account string) ([]PublicShare, error) {
	all, err := m.loadShares()
	out := []PublicShare{}
	for _, s := range all {
		if (account == "" || s.Account == account) && s.ExpiresAt.After(kit.Now()) {
			out = append(out, s.public())
		}
	}
	return out, err
}

var tokenRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (m *Manager) DeleteShare(account, token string) error {
	shareMu.Lock()
	defer shareMu.Unlock()
	all, err := m.loadShares()
	if err != nil {
		return err
	}
	for i, s := range all {
		if s.Token == token && (account == "" || s.Account == account) {
			all = append(all[:i], all[i+1:]...)
			return kit.WriteJSON(m.shareFile(), all)
		}
	}
	return fmt.Errorf("link %w", kit.ErrNotFound)
}

// ShareInfo is what a visitor sees before downloading.
type ShareInfo struct {
	Name          string    `json:"name"`
	Folder        bool      `json:"folder"`
	NeedsPassword bool      `json:"needs_password"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// ErrShareGone means the link expired, hit its download limit or never existed.
var ErrShareGone = fmt.Errorf("this link has expired or doesn't exist %w", kit.ErrNotFound)

// ErrSharePassword means the password was missing or wrong.
var ErrSharePassword = fmt.Errorf("wrong password %w", kit.ErrForbidden)

func (m *Manager) findShare(token string) (*Share, error) {
	if !tokenRe.MatchString(token) {
		return nil, ErrShareGone
	}
	all, err := m.loadShares()
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		if s.Token == token {
			if !s.ExpiresAt.After(kit.Now()) || (s.MaxDownloads > 0 && s.Downloads >= s.MaxDownloads) {
				return nil, ErrShareGone
			}
			return &s, nil
		}
	}
	return nil, ErrShareGone
}

func (m *Manager) ShareInfo(token string) (*ShareInfo, error) {
	s, err := m.findShare(token)
	if err != nil {
		return nil, err
	}
	name := path.Base(s.Path)
	if s.Folder {
		name += ".tar.gz"
	}
	return &ShareInfo{Name: name, Folder: s.Folder, NeedsPassword: s.PasswordHash != "", ExpiresAt: s.ExpiresAt}, nil
}

// ServeShare checks the password, counts the download and streams the file
// (or the folder as .tar.gz) to w. setName is called with the file name
// before any data is written.
func (m *Manager) ServeShare(ctx context.Context, token, password string, setName func(string), w io.Writer) error {
	shareMu.Lock()
	s, err := m.findShare(token)
	if err != nil {
		shareMu.Unlock()
		return err
	}
	if s.PasswordHash != "" {
		got := hashSharePassword(s.PasswordSalt, password)
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.PasswordHash)) != 1 {
			shareMu.Unlock()
			return ErrSharePassword
		}
	}
	all, _ := m.loadShares()
	for i := range all {
		if all[i].Token == token {
			all[i].Downloads++
		}
	}
	_ = kit.WriteJSON(m.shareFile(), all)
	shareMu.Unlock()
	name := path.Base(s.Path)
	if s.Folder {
		setName(name + ".tar.gz")
		return m.DownloadFolder(ctx, s.Account, s.Path, w)
	}
	setName(name)
	return m.Read(ctx, s.Account, s.Path, w)
}
