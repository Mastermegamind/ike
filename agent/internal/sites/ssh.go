package sites

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SSH access modes for a site's Linux user.
const (
	SSHNone  = "none"  // no login at all (default)
	SSHSFTP  = "sftp"  // SFTP only, chrooted to the user's home (sshd Match Group hp-sftp)
	SSHShell = "shell" // full shell, NOT jailed: trusted users only (Match Group hp-ssh)
)

const (
	groupSFTP = "hp-sftp"
	groupSSH  = "hp-ssh"
	maxKeys   = 20
	// Keys live in a root-owned file per user (sshd AuthorizedKeysFile), so hosting
	// users cannot add keys behind the panel's back.
	keysDir = "/etc/ssh/hostpanel/keys"
)

var (
	keyTypes = map[string]bool{
		"ssh-ed25519": true, "ssh-rsa": true,
		"ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true, "ecdsa-sha2-nistp521": true,
		"sk-ssh-ed25519@openssh.com": true, "sk-ecdsa-sha2-nistp256@openssh.com": true,
	}
	commentRe = regexp.MustCompile(`^[A-Za-z0-9@._ -]{0,100}$`)
)

// SSHKey is one authorized public key.
type SSHKey struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment"`
	line        string
}

// ParsePublicKey validates a single OpenSSH public key line ("type base64 [comment]").
// Key options such as command="..." or from="..." are rejected.
func ParsePublicKey(s string) (SSHKey, error) {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, "\r\n") {
		return SSHKey{}, errors.New("public key must be a single line")
	}
	parts := strings.Fields(s)
	if len(parts) < 2 {
		return SSHKey{}, errors.New("public key must look like: ssh-ed25519 AAAA... comment")
	}
	typ, b64 := parts[0], parts[1]
	if !keyTypes[typ] {
		return SSHKey{}, fmt.Errorf("unsupported or invalid key type %q (options like command= are not allowed)", typ)
	}
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(blob) < 4 {
		return SSHKey{}, errors.New("public key data is not valid base64")
	}
	// The blob starts with the key type as an SSH string; it must match.
	n := binary.BigEndian.Uint32(blob[:4])
	if int(n) != len(typ) || len(blob) < 4+int(n) || string(blob[4:4+n]) != typ {
		return SSHKey{}, errors.New("public key data does not match its type")
	}
	if len(blob) < 32 || len(blob) > 16384 {
		return SSHKey{}, errors.New("public key has an implausible size")
	}
	comment := strings.Join(parts[2:], " ")
	if !commentRe.MatchString(comment) {
		return SSHKey{}, errors.New("key comment may only contain letters, digits, spaces and @._-")
	}
	sum := sha256.Sum256(blob)
	fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	line := typ + " " + b64
	if comment != "" {
		line += " " + comment
	}
	return SSHKey{Type: typ, Fingerprint: fp, Comment: comment, line: line}, nil
}

// SetSSHMode switches a site's user between none, sftp and shell.
func (m *Manager) SetSSHMode(ctx context.Context, domain, mode string) (*Site, error) {
	shell, groups := "/usr/sbin/nologin", ""
	switch mode {
	case SSHNone:
	case SSHSFTP:
		groups = groupSFTP
	case SSHShell:
		shell, groups = "/bin/bash", groupSSH
	default:
		return nil, fmt.Errorf("ssh mode must be %q, %q or %q", SSHNone, SSHSFTP, SSHShell)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}

	// --groups replaces the supplementary group list; site users have no other groups.
	if _, err := m.run.Run(ctx, "usermod", "--shell", shell, "--groups", groups, site.User); err != nil {
		return nil, err
	}
	site.SSH = mode
	if err := m.saveSite(site); err != nil {
		return nil, err
	}
	return site, nil
}

// ListSSHKeys returns the keys authorized for a site's user.
func (m *Manager) ListSSHKeys(domain string) ([]SSHKey, error) {
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	return m.readKeys(site.User)
}

// AddSSHKey authorizes a public key for a site's user.
func (m *Manager) AddSSHKey(domain, publicKey string) (SSHKey, error) {
	k, err := ParsePublicKey(publicKey)
	if err != nil {
		return SSHKey{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return SSHKey{}, err
	}
	keys, err := m.readKeys(site.User)
	if err != nil {
		return SSHKey{}, err
	}
	for _, e := range keys {
		if e.Fingerprint == k.Fingerprint {
			return SSHKey{}, fmt.Errorf("key %s %w", k.Fingerprint, ErrExists)
		}
	}
	if len(keys) >= maxKeys {
		return SSHKey{}, fmt.Errorf("a site can have at most %d SSH keys", maxKeys)
	}
	return k, m.writeKeys(site.User, append(keys, k))
}

// RemoveSSHKey removes a key by its SHA256 fingerprint.
func (m *Manager) RemoveSSHKey(domain, fingerprint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return err
	}
	keys, err := m.readKeys(site.User)
	if err != nil {
		return err
	}
	out := keys[:0]
	found := false
	for _, k := range keys {
		if k.Fingerprint == fingerprint {
			found = true
			continue
		}
		out = append(out, k)
	}
	if !found {
		return fmt.Errorf("key %s: %w", fingerprint, ErrNotFound)
	}
	return m.writeKeys(site.User, out)
}

func (m *Manager) keysFile(usr string) string { return m.p(filepath.Join(keysDir, usr)) }

func (m *Manager) readKeys(usr string) ([]SSHKey, error) {
	b, err := os.ReadFile(m.keysFile(usr))
	if errors.Is(err, os.ErrNotExist) {
		return []SSHKey{}, nil
	}
	if err != nil {
		return nil, err
	}
	keys := []SSHKey{}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, err := ParsePublicKey(line)
		if err != nil {
			continue // never expose or keep a line we would not have written
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (m *Manager) writeKeys(usr string, keys []SSHKey) error {
	var buf bytes.Buffer
	buf.WriteString("# Managed by HostPanel. Edit keys in the panel, not here.\n")
	for _, k := range keys {
		buf.WriteString(k.line + "\n")
	}
	// sshd requires the file and its directories to be root-owned and not group/world writable.
	return writeFile(m.keysFile(usr), buf.Bytes(), 0o644)
}

func (m *Manager) saveSite(s *Site) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return writeFile(m.p(filepath.Join(m.cfg.StateDir, s.Domain+".json")), b, 0o600)
}
