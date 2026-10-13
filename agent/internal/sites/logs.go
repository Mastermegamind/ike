package sites

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Log kinds a website can have. Web server logs are root-owned under
// /var/log/ike/<domain>; the PHP error log lives in the site's own logs folder.
var logFiles = map[string]func(L layout) (path string, userOwned bool){
	"access":        func(L layout) (string, bool) { return filepath.Join(L.LogDir, "access.log"), false },
	"error":         func(L layout) (string, bool) { return filepath.Join(L.LogDir, "error.log"), false },
	"apache-access": func(L layout) (string, bool) { return filepath.Join(L.LogDir, "apache-access.log"), false },
	"apache-error":  func(L layout) (string, bool) { return filepath.Join(L.LogDir, "apache-error.log"), false },
	"php":           func(L layout) (string, bool) { return filepath.Join(L.SiteDir, "logs", "php-error.log"), true },
}

// LogKinds lists the log names accepted by SiteLog.
func LogKinds() []string {
	return []string{"access", "error", "php", "apache-access", "apache-error", "app"}
}

// SiteLog returns the last lines of one of a website's logs.
func (m *Manager) SiteLog(domain, kind string, lines int) (string, error) {
	site, err := m.Get(domain)
	if err != nil {
		return "", err
	}
	f, ok := logFiles[kind]
	if !ok {
		return "", fmt.Errorf("log must be one of: %s", strings.Join(LogKinds(), ", "))
	}
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	L := m.layout(site)
	path, userOwned := f(L)
	uid := -1
	if userOwned && !m.cfg.DryRun {
		u, err := lookupUID(site.Account)
		if err != nil {
			return "", err
		}
		uid = u
	}
	return tailFile(m.p(path), lines, uid)
}

// tailFile reads the end of a log without following symlinks. When wantUID >= 0
// the file must also be owned by that user, so a hosting user can't plant a
// hard link to a system file in their logs folder and read it through the panel.
func tailFile(path string, lines, wantUID int) (string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		if os.IsNotExist(err) || err == syscall.ENOENT {
			return "", nil // no log yet
		}
		if err == syscall.ELOOP {
			return "", fmt.Errorf("log file is a link; refusing to read it")
		}
		return "", err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("log is not a regular file")
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && wantUID >= 0 && (int(st.Uid) != wantUID || st.Nlink > 1) {
		return "", fmt.Errorf("log file ownership looks wrong; run fix permissions")
	}
	const maxRead = 1 << 20
	start := fi.Size() - maxRead
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxRead))
	if err != nil {
		return "", err
	}
	all := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if start > 0 && len(all) > 0 {
		all = all[1:] // first line is probably cut in half
	}
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}

func lookupUID(name string) (int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}
