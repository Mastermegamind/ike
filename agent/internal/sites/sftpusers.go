package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/kit"
)

// Website SFTP users: extra logins that can only reach one website's folder,
// for a designer or a developer who shouldn't see the rest of the account.
//
// Each is its own Linux user name sharing the account's UID and group, so
// files they upload belong to the account and PHP can still change them.
// OpenSSH chroots them into a root-owned jail,
//
//	/var/lib/ike/sftp/<user>/<domain>    the website folder, bind-mounted
//
// kept mounted by a systemd mount unit. The jail holds nothing else, so the
// shared UID gives no reach beyond that folder. Logins are SFTP only (a
// Match Group block forces internal-sftp), with a key, a password or both.

const (
	groupSFTPSite   = "ike-sftpsite"    // read and write
	groupSFTPSiteRO = "ike-sftpsite-ro" // download only
	sftpJailRoot    = "/var/lib/ike/sftp"
	maxSFTPUsers    = 50
)

// SFTPUser is one website SFTP login.
type SFTPUser struct {
	Name     string    `json:"name"`   // Linux user: <account>_<short name>
	Domain   string    `json:"domain"` // the website it reaches
	Folder   string    `json:"folder"` // inside the website folder: public_html (default) or "" for the whole website
	ReadOnly bool      `json:"read_only,omitempty"`
	Password bool      `json:"password"` // signs in with a password (as well as any keys)
	Keys     []SSHKey  `json:"keys"`
	Expires  string    `json:"expires,omitempty"` // YYYY-MM-DD, last day it works
	Created  time.Time `json:"created"`
	// Host and Port to put in FileZilla and the like (filled in on read).
	Login string `json:"login,omitempty"`
}

// SFTPUserRequest creates or changes a website SFTP user.
type SFTPUserRequest struct {
	Name      string   `json:"name,omitempty"`   // short name, e.g. designer (create only)
	Domain    string   `json:"domain,omitempty"` // create only
	Folder    *string  `json:"folder,omitempty"`
	ReadOnly  *bool    `json:"read_only,omitempty"`
	Password  *string  `json:"password,omitempty"` // "" removes the password; "generate" makes one up
	PublicKey string   `json:"public_key,omitempty"`
	Keys      []string `json:"keys,omitempty"` // replace every key (fingerprints to keep are matched by line)
	Expires   *string  `json:"expires,omitempty"`
}

type sftpStoreUser struct {
	SFTPUser
	KeyLines []string `json:"key_lines"`
}

var shortNameRe = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}$`)

func (m *Manager) sftpStore(account string) string {
	return filepath.Join(m.cfg.AccountsDir, account+".sftp.json")
}

func (m *Manager) readSFTP(account string) ([]sftpStoreUser, error) {
	var out []sftpStoreUser
	b, err := os.ReadFile(m.p(m.sftpStore(account)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal(b, &out)
}

func (m *Manager) writeSFTP(account string, users []sftpStoreUser) error {
	if len(users) == 0 {
		return removeIfExists(m.p(m.sftpStore(account)))
	}
	return m.writeJSON(m.sftpStore(account), users)
}

func (u sftpStoreUser) view() SFTPUser {
	v := u.SFTPUser
	v.Keys = []SSHKey{}
	for _, l := range u.KeyLines {
		if k, err := ParsePublicKey(l); err == nil {
			v.Keys = append(v.Keys, k)
		}
	}
	return v
}

// ListSFTPUsers returns an account's website SFTP users.
func (m *Manager) ListSFTPUsers(account string) ([]SFTPUser, error) {
	if _, err := m.GetAccount(account); err != nil {
		return nil, err
	}
	us, err := m.readSFTP(account)
	if err != nil {
		return nil, err
	}
	out := make([]SFTPUser, 0, len(us))
	for _, u := range us {
		out = append(out, u.view())
	}
	return out, nil
}

func checkFolder(f string) (string, error) {
	f = strings.Trim(strings.TrimSpace(f), "/")
	if f == "" {
		return "", nil
	}
	clean := path.Clean(f)
	if clean != f || strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") || !regexp.MustCompile(`^[A-Za-z0-9._/-]+$`).MatchString(clean) {
		return "", fmt.Errorf("%q is not a folder inside the website", f)
	}
	return clean, nil
}

func checkExpiry(e string) error {
	if e == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", e)
	if err != nil {
		return fmt.Errorf("expiry must be a date like 2026-12-31")
	}
	if t.Before(time.Now().Truncate(24 * time.Hour)) {
		return fmt.Errorf("the expiry date has already passed")
	}
	return nil
}

// CreateSFTPUser adds a website SFTP user. With neither a password nor a key,
// a password is made up; it is returned once.
func (m *Manager) CreateSFTPUser(ctx context.Context, account string, req SFTPUserRequest) (*SFTPUser, string, error) {
	if !shortNameRe.MatchString(req.Name) {
		return nil, "", fmt.Errorf("the name must be 1-12 lowercase letters or digits, starting with a letter")
	}
	name := account + "_" + req.Name
	folder := "public_html"
	if req.Folder != nil {
		var err error
		if folder, err = checkFolder(*req.Folder); err != nil {
			return nil, "", err
		}
	}
	expires := ""
	if req.Expires != nil {
		expires = *req.Expires
	}
	if err := checkExpiry(expires); err != nil {
		return nil, "", err
	}
	var keyLines []string
	if req.PublicKey != "" {
		k, err := ParsePublicKey(req.PublicKey)
		if err != nil {
			return nil, "", err
		}
		keyLines = append(keyLines, k.line)
	}
	password, made := "", ""
	if req.Password != nil && *req.Password != "" && *req.Password != "generate" {
		password = *req.Password
	} else if (req.Password != nil && *req.Password == "generate") || len(keyLines) == 0 {
		password = kit.Password(16)
		made = password
	}
	if password != "" && (len(password) < 10 || len(password) > 200 || strings.ContainsAny(password, ":\n\r")) {
		return nil, "", fmt.Errorf("the password must be 10-200 characters, without : or line breaks")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.getAccount(account)
	if err != nil {
		return nil, "", err
	}
	if a.Suspended {
		return nil, "", fmt.Errorf("account %s is suspended; reactivate it first", account)
	}
	site, err := m.Get(req.Domain)
	if err != nil {
		return nil, "", err
	}
	if site.Account != account {
		return nil, "", fmt.Errorf("website %s %w", req.Domain, ErrNotFound)
	}
	L := m.layout(site)
	src := L.SiteDir
	if folder != "" {
		src = filepath.Join(L.SiteDir, folder)
	}
	if st, err := os.Lstat(m.p(src)); err != nil || !st.IsDir() {
		return nil, "", fmt.Errorf("%s has no folder %s", req.Domain, folder)
	}
	users, err := m.readSFTP(account)
	if err != nil {
		return nil, "", err
	}
	if len(users) >= maxSFTPUsers {
		return nil, "", fmt.Errorf("at most %d website SFTP users per account", maxSFTPUsers)
	}
	for _, u := range users {
		if u.Name == name {
			return nil, "", fmt.Errorf("SFTP user %s %w", name, ErrExists)
		}
	}
	if _, err := user.Lookup(name); err == nil && !m.cfg.DryRun {
		return nil, "", fmt.Errorf("a Linux user called %s already exists", name)
	}
	readOnly := req.ReadOnly != nil && *req.ReadOnly
	u := sftpStoreUser{SFTPUser: SFTPUser{Name: name, Domain: site.Domain, Folder: folder, ReadOnly: readOnly, Password: password != "",
		Expires: expires, Created: time.Now().UTC()}, KeyLines: keyLines}

	undo := &undoStack{}
	if err := m.addSFTPUser(ctx, account, u, src, password, undo); err != nil {
		undo.run()
		return nil, "", err
	}
	users = append(users, u)
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	if err := m.writeSFTP(account, users); err != nil {
		undo.run()
		return nil, "", err
	}
	v := u.view()
	return &v, made, nil
}

func (m *Manager) accountIDs(account string) (uid, gid string, err error) {
	if m.cfg.DryRun {
		return "1001", "1001", nil // no real users in a dry run
	}
	u, err := user.Lookup(account)
	if err != nil {
		return "", "", fmt.Errorf("Linux user %s: %w", account, err)
	}
	return u.Uid, u.Gid, nil
}

// jailDir is the chroot; Where is the mount point inside it.
func jailDir(name string) string           { return filepath.Join(sftpJailRoot, name) }
func jailMount(name, domain string) string { return filepath.Join(jailDir(name), domain) }

func (m *Manager) addSFTPUser(ctx context.Context, account string, u sftpStoreUser, src, password string, undo *undoStack) error {
	if err := m.ensureSFTPSiteSSH(ctx); err != nil {
		return err
	}
	uid, gid, err := m.accountIDs(account)
	if err != nil {
		return err
	}
	group := groupSFTPSite
	if u.ReadOnly {
		group = groupSFTPSiteRO
	}
	// The jail: root-owned all the way down, as sshd requires for ChrootDirectory.
	for _, d := range []string{sftpJailRoot, jailDir(u.Name), jailMount(u.Name, u.Domain)} {
		if err := os.MkdirAll(m.p(d), 0o755); err != nil {
			return err
		}
		_ = os.Chmod(m.p(d), 0o755)
	}
	undo.push(func() { m.removeJail(u.Name, u.Domain) })
	args := []string{"--non-unique", "--uid", uid, "--gid", gid, "--no-create-home", "--home-dir", "/" + u.Domain,
		"--shell", "/usr/sbin/nologin", "--groups", group, "--comment", "Ike SFTP for " + u.Domain}
	if u.Expires != "" {
		args = append(args, "--expiredate", u.Expires)
	}
	if _, err := m.run.Run(ctx, "useradd", append(args, u.Name)...); err != nil {
		return fmt.Errorf("create the SFTP user: %w", err)
	}
	undo.push(func() { _, _ = m.run.Run(context.Background(), "userdel", "--force", u.Name) })
	if err := m.setSFTPPassword(ctx, u.Name, password); err != nil {
		return err
	}
	if err := m.writeSFTPKeys(u.Name, u.KeyLines); err != nil {
		return err
	}
	undo.push(func() { _ = os.Remove(m.keysFile(u.Name)) })
	return m.mountJail(ctx, u, src)
}

// setSFTPPassword sets a SHA-512 crypt password (on stdin, never in the
// process list), or locks password sign-in when empty.
func (m *Manager) setSFTPPassword(ctx context.Context, name, password string) error {
	if password == "" {
		_, err := m.run.Run(ctx, "usermod", "--lock", name)
		return err
	}
	if _, err := m.run.RunInput(ctx, name+":"+cryptHash(password)+"\n", "chpasswd", "--encrypted"); err != nil {
		return fmt.Errorf("set the password: %w", err)
	}
	_, err := m.run.Run(ctx, "usermod", "--unlock", name)
	return err
}

func (m *Manager) writeSFTPKeys(name string, lines []string) error {
	var b strings.Builder
	b.WriteString("# Managed by Ike: website SFTP user. Edit keys in the panel, not here.\n")
	for _, l := range lines {
		b.WriteString("restrict " + l + "\n")
	}
	return writeFile(m.keysFile(name), []byte(b.String()), 0o644)
}

// mountUnit is systemd's name for the mount at where (systemd-escape --path).
func mountUnit(where string) string {
	var b strings.Builder
	for i, c := range []byte(strings.Trim(where, "/")) {
		switch {
		case c == '/':
			b.WriteByte('-')
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == ':', c == '.' && i > 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	return b.String() + ".mount"
}

func (m *Manager) mountJail(ctx context.Context, u sftpStoreUser, src string) error {
	where := jailMount(u.Name, u.Domain)
	unit := mountUnit(where)
	opts := "bind"
	if u.ReadOnly {
		opts = "bind,ro"
	}
	body := "# Managed by Ike: website SFTP user " + u.Name + ".\n[Unit]\nDescription=Ike SFTP folder for " + u.Name + "\nBefore=ssh.service\n\n" +
		"[Mount]\nWhat=" + src + "\nWhere=" + where + "\nType=none\nOptions=" + opts + "\n\n[Install]\nWantedBy=multi-user.target\n"
	if err := writeFile(m.p(filepath.Join("/etc/systemd/system", unit)), []byte(body), 0o644); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "systemctl", "enable", "--now", unit); err != nil {
		return fmt.Errorf("mount the website folder: %w", err)
	}
	return nil
}

// removeJail unmounts and removes the jail. It never deletes recursively:
// while mounted, the folder IS the website.
func (m *Manager) removeJail(name, domain string) {
	where := jailMount(name, domain)
	unit := mountUnit(where)
	_, _ = m.run.Run(context.Background(), "systemctl", "disable", "--now", unit)
	_ = os.Remove(m.p(filepath.Join("/etc/systemd/system", unit)))
	_, _ = m.run.Run(context.Background(), "systemctl", "daemon-reload")
	_ = os.Remove(m.p(where)) // fails, harmlessly, if still mounted
	_ = os.Remove(m.p(jailDir(name)))
}

// UpdateSFTPUser changes a password, keys, access or expiry.
func (m *Manager) UpdateSFTPUser(ctx context.Context, account, name string, req SFTPUserRequest) (*SFTPUser, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	users, err := m.readSFTP(account)
	if err != nil {
		return nil, "", err
	}
	i := -1
	for j := range users {
		if users[j].Name == name {
			i = j
		}
	}
	if i < 0 {
		return nil, "", fmt.Errorf("SFTP user %s %w", name, ErrNotFound)
	}
	u := users[i]
	made := ""
	if req.Password != nil {
		pw := *req.Password
		if pw == "generate" {
			pw = kit.Password(16)
			made = pw
		}
		if pw == "" && len(u.KeyLines) == 0 && req.PublicKey == "" && len(req.Keys) == 0 {
			return nil, "", fmt.Errorf("add a key before removing the password, or the user can't sign in")
		}
		if pw != "" && (len(pw) < 10 || len(pw) > 200 || strings.ContainsAny(pw, ":\n\r")) {
			return nil, "", fmt.Errorf("the password must be 10-200 characters, without : or line breaks")
		}
		if err := m.setSFTPPassword(ctx, name, pw); err != nil {
			return nil, "", err
		}
		u.Password = pw != ""
	}
	if req.Keys != nil || req.PublicKey != "" {
		lines := u.KeyLines
		if req.Keys != nil {
			lines = nil
			for _, l := range req.Keys {
				k, err := ParsePublicKey(l)
				if err != nil {
					return nil, "", err
				}
				lines = append(lines, k.line)
			}
		}
		if req.PublicKey != "" {
			k, err := ParsePublicKey(req.PublicKey)
			if err != nil {
				return nil, "", err
			}
			lines = append(lines, k.line)
		}
		if len(lines) == 0 && !u.Password {
			return nil, "", fmt.Errorf("keep a key or set a password, or the user can't sign in")
		}
		if err := m.writeSFTPKeys(name, lines); err != nil {
			return nil, "", err
		}
		u.KeyLines = lines
	}
	if req.Expires != nil {
		if err := checkExpiry(*req.Expires); err != nil {
			return nil, "", err
		}
		u.Expires = *req.Expires
		if a, err := m.getAccount(account); err == nil && !a.Suspended {
			if _, err := m.run.Run(ctx, "usermod", "--expiredate", u.Expires, name); err != nil {
				return nil, "", err
			}
		}
	}
	if (req.ReadOnly != nil && *req.ReadOnly != u.ReadOnly) || req.Folder != nil {
		folder := u.Folder
		if req.Folder != nil {
			if folder, err = checkFolder(*req.Folder); err != nil {
				return nil, "", err
			}
		}
		ro := u.ReadOnly
		if req.ReadOnly != nil {
			ro = *req.ReadOnly
		}
		site, err := m.Get(u.Domain)
		if err != nil {
			return nil, "", err
		}
		src := m.layout(site).SiteDir
		if folder != "" {
			src = filepath.Join(src, folder)
		}
		if st, err := os.Lstat(m.p(src)); err != nil || !st.IsDir() {
			return nil, "", fmt.Errorf("%s has no folder %s", u.Domain, folder)
		}
		group := groupSFTPSite
		if ro {
			group = groupSFTPSiteRO
		}
		if _, err := m.run.Run(ctx, "usermod", "--groups", group, name); err != nil {
			return nil, "", err
		}
		u.ReadOnly, u.Folder = ro, folder
		if _, err := m.run.Run(ctx, "systemctl", "stop", mountUnit(jailMount(name, u.Domain))); err != nil {
			return nil, "", err
		}
		if err := m.mountJail(ctx, u, src); err != nil {
			return nil, "", err
		}
	}
	users[i] = u
	if err := m.writeSFTP(account, users); err != nil {
		return nil, "", err
	}
	v := u.view()
	return &v, made, nil
}

// DeleteSFTPUser removes a website SFTP user. The website's files stay.
func (m *Manager) DeleteSFTPUser(ctx context.Context, account, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deleteSFTPUserLocked(ctx, account, name)
}

func (m *Manager) deleteSFTPUserLocked(ctx context.Context, account, name string) error {
	users, err := m.readSFTP(account)
	if err != nil {
		return err
	}
	var keep []sftpStoreUser
	var gone *sftpStoreUser
	for i := range users {
		if users[i].Name == name {
			gone = &users[i]
		} else {
			keep = append(keep, users[i])
		}
	}
	if gone == nil {
		return fmt.Errorf("SFTP user %s %w", name, ErrNotFound)
	}
	// userdel --force: the shared UID always has processes (PHP, apps), which
	// would otherwise block removing the name.
	// End open sessions; sshd names its processes "sshd: <user>@..." / "sshd: <user> [priv]".
	_, _ = m.run.Run(ctx, "pkill", "--full", "^sshd: "+name+"[ @]") // fails when nobody is signed in
	if _, err := m.run.Run(ctx, "userdel", "--force", name); err != nil {
		return err
	}
	_ = os.Remove(m.keysFile(name))
	m.removeJail(name, gone.Domain)
	return m.writeSFTP(account, keep)
}

// deleteSFTPUsersFor removes the SFTP users of one website ("" = all of the account).
func (m *Manager) deleteSFTPUsersFor(ctx context.Context, account, domain string) {
	users, _ := m.readSFTP(account)
	for _, u := range users {
		if domain == "" || u.Domain == domain {
			_ = m.deleteSFTPUserLocked(ctx, account, u.Name)
		}
	}
}

// lockSFTPUsers blocks (suspend) or restores (unsuspend) an account's SFTP users.
func (m *Manager) lockSFTPUsers(ctx context.Context, account string, lock bool) {
	users, _ := m.readSFTP(account)
	for _, u := range users {
		exp := u.Expires
		if lock {
			exp = "1" // 1970-01-02: expired
		}
		_, _ = m.run.Run(ctx, "usermod", "--expiredate", exp, u.Name)
	}
}

// ensureSFTPSiteSSH makes sure sshd knows the website SFTP groups. The
// installer writes this block; servers installed before it get it here.
func (m *Manager) ensureSFTPSiteSSH(ctx context.Context) error {
	for _, g := range []string{groupSFTPSite, groupSFTPSiteRO} {
		if _, err := m.run.Run(ctx, "groupadd", "--force", "--system", g); err != nil {
			return err
		}
	}
	cfg := m.p("/etc/ssh/sshd_config")
	b, err := os.ReadFile(cfg)
	if err != nil {
		if os.IsNotExist(err) && m.cfg.DryRun {
			b = nil
		} else {
			return fmt.Errorf("read the SSH server's settings: %w", err)
		}
	}
	if strings.Contains(string(b), "Match Group "+groupSFTPSite) {
		return nil
	}
	text := strings.TrimRight(string(b), "\n") + "\n\n" + SFTPSiteSSHBlock(keysDir)
	if err := writeFile(cfg, []byte(text), 0o644); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "sshd", "-t"); err != nil {
		_ = writeFile(cfg, b, 0o644)
		return fmt.Errorf("the SSH server refused the new settings: %w", err)
	}
	_, err = m.run.Run(ctx, "systemctl", "reload", "ssh")
	return err
}

// SFTPSiteSSHBlock is the sshd_config block for website SFTP users. It goes
// at the end of the file (Match blocks run to the end of the file).
func SFTPSiteSSHBlock(keys string) string {
	block := func(group, extra string) string {
		return "Match Group " + group + "\n" +
			"    ChrootDirectory " + sftpJailRoot + "/%u\n" +
			"    ForceCommand internal-sftp" + extra + "\n" +
			"    AuthorizedKeysFile " + keys + "/%u\n" +
			"    PasswordAuthentication yes\n" +
			"    KbdInteractiveAuthentication no\n" +
			"    AllowTcpForwarding no\n" +
			"    AllowAgentForwarding no\n" +
			"    X11Forwarding no\n" +
			"    PermitTunnel no\n" +
			"    PermitTTY no\n"
	}
	return "# BEGIN ike-sftpsite (managed: website SFTP users, chrooted to one website)\n" +
		block(groupSFTPSite, "") + block(groupSFTPSiteRO, " -R") + "# END ike-sftpsite\n"
}
