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

	"github.com/Mastermegamind/ike/agent/internal/validate"
)

// SSH access modes for an account's Linux user.
const (
	SSHNone  = "none"  // no login at all (default)
	SSHSFTP  = "sftp"  // SFTP only, chrooted to the user's home (sshd Match Group ike-sftp)
	SSHShell = "shell" // full shell, NOT jailed: trusted users only (Match Group ike-ssh)
)

const (
	groupSFTP = "ike-sftp"
	groupSSH  = "ike-ssh"
	maxKeys   = 20
	// Keys live in a root-owned file per user (sshd AuthorizedKeysFile), so hosting
	// users cannot add keys behind the panel's back.
	keysDir = "/etc/ssh/ike/keys"
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
	KeyAccess
	line string
}

// KeyAccess limits what one key can do, on top of the account's SSH mode.
type KeyAccess struct {
	// Access is "" (whatever the account allows), "sftp" (file transfer
	// only) or "sftp-readonly" (download only).
	Access string `json:"access,omitempty"`
	// AllowFrom lists the IP addresses or CIDR ranges the key works from.
	AllowFrom []string `json:"allow_from,omitempty"`
	// Expires is the last day the key works (YYYY-MM-DD).
	Expires string `json:"expires,omitempty"`
}

var expiresRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
var cidrRe = regexp.MustCompile(`^[0-9a-fA-F:.]{2,45}(/\d{1,3})?$`)

func (k KeyAccess) check() error {
	switch k.Access {
	case "", "sftp", "sftp-readonly":
	default:
		return fmt.Errorf("key access is full, sftp or sftp-readonly")
	}
	if len(k.AllowFrom) > 20 {
		return fmt.Errorf("at most 20 addresses per key")
	}
	for _, a := range k.AllowFrom {
		if !cidrRe.MatchString(a) {
			return fmt.Errorf("%q is not an IP address or range", a)
		}
	}
	if k.Expires != "" && !expiresRe.MatchString(k.Expires) {
		return fmt.Errorf("expiry date looks like 2026-12-31")
	}
	return nil
}

// options renders the authorized_keys options for a key on an account in
// the given SSH mode. Every key is "restrict"ed (no forwarding); shell keys
// get a terminal back, file-transfer keys are forced into internal-sftp.
func (k KeyAccess) options(mode string) string {
	var o []string
	start := "-d %d/web"
	if mode == SSHSFTP {
		start = "-d /web" // inside the chroot
	}
	switch {
	case k.Access == "sftp-readonly":
		o = append(o, "restrict", `command="internal-sftp -R `+start+`"`)
	case k.Access == "sftp" || mode == SSHSFTP:
		o = append(o, "restrict", `command="internal-sftp `+start+`"`)
	default:
		o = append(o, "restrict", "pty")
	}
	if len(k.AllowFrom) > 0 {
		o = append(o, `from="`+strings.Join(k.AllowFrom, ",")+`"`)
	}
	if k.Expires != "" {
		o = append(o, `expiry-time="`+strings.ReplaceAll(k.Expires, "-", "")+`2359"`)
	}
	return strings.Join(o, ",")
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

// SetSSHMode switches an account between none, sftp and shell.
func (m *Manager) SetSSHMode(ctx context.Context, account, mode string) (*Account, error) {
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
	if err := validate.Username(account); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.getAccount(account)
	if err != nil {
		return nil, err
	}
	if a.Suspended && mode != SSHNone {
		return nil, fmt.Errorf("account %s is suspended; reactivate it first", account)
	}
	// --groups replaces the supplementary group list; account users have no other groups.
	if _, err := m.run.Run(ctx, "usermod", "--shell", shell, "--groups", groups, account); err != nil {
		return nil, err
	}
	a.SSH = mode
	if err := m.saveAccount(a); err != nil {
		return nil, err
	}
	// Key options depend on the mode (chroot paths, forced SFTP).
	if keys, err := m.readKeys(account); err == nil && len(keys) > 0 {
		if err := m.writeKeys(account, keys); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// ListSSHKeys returns the keys authorized for an account.
func (m *Manager) ListSSHKeys(account string) ([]SSHKey, error) {
	if _, err := m.GetAccount(account); err != nil {
		return nil, err
	}
	return m.readKeys(account)
}

// AddSSHKey authorizes a public key for an account.
func (m *Manager) AddSSHKey(account, publicKey string, acc ...KeyAccess) (SSHKey, error) {
	k, err := ParsePublicKey(publicKey)
	if err != nil {
		return SSHKey{}, err
	}
	if len(acc) > 0 {
		if err := acc[0].check(); err != nil {
			return SSHKey{}, err
		}
		k.KeyAccess = acc[0]
	}
	if _, err := m.GetAccount(account); err != nil {
		return SSHKey{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	keys, err := m.readKeys(account)
	if err != nil {
		return SSHKey{}, err
	}
	for _, e := range keys {
		if e.Fingerprint == k.Fingerprint {
			return SSHKey{}, fmt.Errorf("key %s %w", k.Fingerprint, ErrExists)
		}
	}
	if len(keys) >= maxKeys {
		return SSHKey{}, fmt.Errorf("an account can have at most %d SSH keys", maxKeys)
	}
	return k, m.writeKeys(account, append(keys, k))
}

// SetKeyAccess changes what an existing key may do.
func (m *Manager) SetKeyAccess(account, fingerprint string, acc KeyAccess) (SSHKey, error) {
	if err := acc.check(); err != nil {
		return SSHKey{}, err
	}
	if _, err := m.GetAccount(account); err != nil {
		return SSHKey{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	keys, err := m.readKeys(account)
	if err != nil {
		return SSHKey{}, err
	}
	for i, k := range keys {
		if k.Fingerprint == fingerprint {
			keys[i].KeyAccess = acc
			return keys[i], m.writeKeys(account, keys)
		}
	}
	return SSHKey{}, fmt.Errorf("key %s %w", fingerprint, ErrNotFound)
}

// RemoveSSHKey removes a key by its SHA256 fingerprint.
func (m *Manager) RemoveSSHKey(account, fingerprint string) error {
	if _, err := m.GetAccount(account); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	keys, err := m.readKeys(account)
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
		return fmt.Errorf("key %s %w", fingerprint, ErrNotFound)
	}
	return m.writeKeys(account, out)
}

func (m *Manager) keysFile(usr string) string { return m.p(filepath.Join(keysDir, usr)) }

// keyMetaFile keeps each key's access settings; the authorized_keys file is
// rendered from it.
func (m *Manager) keyMetaFile(usr string) string {
	return m.p(filepath.Join(m.cfg.AccountsDir, usr+".keys.json"))
}

type keyMeta struct {
	Line string `json:"line"`
	KeyAccess
}

func (m *Manager) readKeys(usr string) ([]SSHKey, error) {
	if b, err := os.ReadFile(m.keyMetaFile(usr)); err == nil {
		var meta []keyMeta
		if json.Unmarshal(b, &meta) == nil {
			keys := []SSHKey{}
			for _, km := range meta {
				k, err := ParsePublicKey(km.Line)
				if err != nil || km.KeyAccess.check() != nil {
					continue
				}
				k.KeyAccess = km.KeyAccess
				keys = append(keys, k)
			}
			return keys, nil
		}
	}
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
	mode := SSHShell
	if a, err := m.getAccount(usr); err == nil {
		mode = a.SSH
	}
	meta := make([]keyMeta, 0, len(keys))
	var buf bytes.Buffer
	buf.WriteString("# Managed by Ike. Edit keys in the panel, not here.\n")
	for _, k := range keys {
		meta = append(meta, keyMeta{Line: k.line, KeyAccess: k.KeyAccess})
		buf.WriteString(k.KeyAccess.options(mode) + " " + k.line + "\n")
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeFile(m.keyMetaFile(usr), mb, 0o600); err != nil {
		return err
	}
	// sshd requires the file and its directories to be root-owned and not group/world writable.
	return writeFile(m.keysFile(usr), buf.Bytes(), 0o644)
}

// RewriteAllKeys renders every account's authorized_keys file again, so key
// options follow the current rules after an upgrade.
func (m *Manager) RewriteAllKeys() {
	accts, err := m.Accounts()
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range accts {
		if keys, err := m.readKeys(a.Name); err == nil && len(keys) > 0 {
			_ = m.writeKeys(a.Name, keys)
		}
	}
}
