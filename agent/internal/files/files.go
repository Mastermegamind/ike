// Package files is the file manager. Every operation on an account's files
// runs as that account's Linux user (runuser), so a symbolic link planted by
// the user can never make Ike read or write a file the user couldn't touch
// themselves.
package files

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

type Manager struct {
	env *kit.Env
}

func New(env *kit.Env) *Manager { return &Manager{env: env} }

// Clean validates a path relative to the account's home ("" is the home) and
// returns its absolute form.
func (m *Manager) Clean(account, rel string) (string, error) {
	if err := m.env.CheckAccount(account); err != nil {
		return "", err
	}
	rel = strings.Trim(rel, "/")
	if len(rel) > 4000 || !utf8.ValidString(rel) {
		return "", fmt.Errorf("path is too long or not valid text")
	}
	for _, r := range rel {
		if r == 0 || unicode.IsControl(r) {
			return "", fmt.Errorf("path contains control characters")
		}
	}
	if rel != "" {
		for _, seg := range strings.Split(rel, "/") {
			if seg == "" || seg == "." || seg == ".." || len(seg) > 255 {
				return "", fmt.Errorf("%q is not a valid path", rel)
			}
		}
	}
	home := m.env.Home(account)
	abs := path.Join(home, rel)
	if abs != home && !strings.HasPrefix(abs, home+"/") {
		return "", fmt.Errorf("path is outside the account")
	}
	return abs, nil
}

// as runs a program as the account user.
func (m *Manager) as(ctx context.Context, account string, stdin io.Reader, stdout io.Writer, timeout time.Duration, name string, args ...string) ([]byte, error) {
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	return m.env.Run.Exec(ctx, runner.Command{
		Name: "runuser", Args: append([]string{"-u", account, "--", name}, args...),
		Env: []string{"HOME=" + m.env.Home(account), "USER=" + account, "LANG=C.UTF-8", "PATH=/usr/bin:/bin"},
		Dir: "/", StdinReader: stdin, Stdout: stdout, Timeout: timeout,
	})
}

// Entry is one item in a folder.
type Entry struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"` // file | dir | link | other
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Mode     string    `json:"mode"` // octal, e.g. 0644
	Target   string    `json:"target,omitempty"`
}

// List returns a folder's entries, folders first.
func (m *Manager) List(ctx context.Context, account, rel string) ([]Entry, error) {
	dir, err := m.Clean(account, rel)
	if err != nil {
		return nil, err
	}
	return m.listDir(ctx, account, dir, rel)
}

func (m *Manager) listDir(ctx context.Context, account, dir, rel string) ([]Entry, error) {
	var out bytes.Buffer
	if _, err := m.as(ctx, account, nil, &out, 0, "find", dir, "-mindepth", "1", "-maxdepth", "1",
		"-printf", `%y\t%s\t%T@\t%m\t%l\t%f\0`); err != nil {
		return nil, friendly(err, rel)
	}
	entries := []Entry{}
	for _, rec := range bytes.Split(out.Bytes(), []byte{0}) {
		f := strings.SplitN(string(rec), "\t", 6)
		if len(f) != 6 {
			continue
		}
		e := Entry{Name: f[5], Mode: "0" + f[3], Target: f[4]}
		switch f[0] {
		case "f":
			e.Type = "file"
		case "d":
			e.Type = "dir"
		case "l":
			e.Type = "link"
		default:
			e.Type = "other"
		}
		e.Size, _ = strconv.ParseInt(f[1], 10, 64)
		if sec, err := strconv.ParseFloat(f[2], 64); err == nil {
			e.Modified = time.Unix(int64(sec), 0).UTC()
		}
		entries = append(entries, e)
		if len(entries) >= 20000 {
			break
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if (entries[i].Type == "dir") != (entries[j].Type == "dir") {
			return entries[i].Type == "dir"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

func friendly(err error, rel string) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "No such file"):
		return fmt.Errorf("%s %w", orHome(rel), kit.ErrNotFound)
	case strings.Contains(msg, "Permission denied"):
		return fmt.Errorf("%s: permission denied", orHome(rel))
	case strings.Contains(msg, "File exists"), strings.Contains(msg, "not overwritten"), strings.Contains(msg, "already exists"):
		return fmt.Errorf("%s %w", orHome(rel), kit.ErrExists)
	case strings.Contains(msg, "No space left"), strings.Contains(msg, "Disk quota exceeded"):
		return fmt.Errorf("the account is out of disk space")
	}
	lines := strings.Split(strings.TrimSpace(msg), "\n")
	return fmt.Errorf("%s", lines[len(lines)-1])
}

func orHome(rel string) string {
	if rel == "" {
		return "home folder"
	}
	return rel
}

// MaxEdit is the largest file the editor opens.
const MaxEdit = 2 << 20

// Read streams a file (download) or returns it for the editor.
func (m *Manager) Read(ctx context.Context, account, rel string, w io.Writer) error {
	p, err := m.Clean(account, rel)
	if err != nil {
		return err
	}
	if _, err := m.as(ctx, account, nil, w, time.Hour, "cat", "--", p); err != nil {
		return friendly(err, rel)
	}
	return nil
}

// ReadText returns a text file for the editor (up to 2 MB).
func (m *Manager) ReadText(ctx context.Context, account, rel string) (string, error) {
	var b bytes.Buffer
	p, err := m.Clean(account, rel)
	if err != nil {
		return "", err
	}
	if _, err := m.as(ctx, account, nil, &b, 0, "head", "-c", strconv.Itoa(MaxEdit+1), "--", p); err != nil {
		return "", friendly(err, rel)
	}
	if b.Len() > MaxEdit {
		return "", fmt.Errorf("%s is larger than 2 MB; download it instead", rel)
	}
	if !utf8.Valid(b.Bytes()) || bytes.IndexByte(b.Bytes(), 0) >= 0 {
		return "", fmt.Errorf("%s isn't a text file", rel)
	}
	return b.String(), nil
}

// Write saves a file atomically: written to a temporary name next to it,
// then moved over the old one.
func (m *Manager) Write(ctx context.Context, account, rel string, r io.Reader, overwrite bool) error {
	p, err := m.Clean(account, rel)
	if err != nil {
		return err
	}
	if rel == "" {
		return fmt.Errorf("choose a file name")
	}
	tmp := path.Join(path.Dir(p), ".ike-upload-"+kit.RandomHex(6))
	if _, err := m.as(ctx, account, r, nil, 6*time.Hour, "dd", "of="+tmp, "bs=1M", "status=none"); err != nil {
		_, _ = m.as(context.Background(), account, nil, nil, 0, "rm", "-f", "--", tmp)
		return friendly(err, rel)
	}
	mv := []string{"-T", "--", tmp, p}
	if !overwrite {
		mv = append([]string{"-n"}, mv...)
	}
	if _, err := m.as(ctx, account, nil, nil, 0, "mv", mv...); err != nil {
		_, _ = m.as(context.Background(), account, nil, nil, 0, "rm", "-f", "--", tmp)
		return friendly(err, rel)
	}
	if !overwrite {
		// mv -n succeeds silently when the target exists; the temp file is left
		if _, err := m.as(ctx, account, nil, nil, 0, "test", "-e", tmp); err == nil {
			_, _ = m.as(context.Background(), account, nil, nil, 0, "rm", "-f", "--", tmp)
			return fmt.Errorf("%s %w", rel, kit.ErrExists)
		}
	}
	return nil
}

// Mkdir creates a folder (and parents).
func (m *Manager) Mkdir(ctx context.Context, account, rel string) error {
	p, err := m.Clean(account, rel)
	if err != nil || rel == "" {
		return orErr(err, "choose a folder name")
	}
	_, err = m.as(ctx, account, nil, nil, 0, "mkdir", "-p", "--", p)
	return friendlyIf(err, rel)
}

func orErr(err error, msg string) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("%s", msg)
}

func friendlyIf(err error, rel string) error {
	if err == nil {
		return nil
	}
	return friendly(err, rel)
}

// Move renames or moves items into another folder. Existing names are not
// overwritten.
func (m *Manager) Move(ctx context.Context, account string, from []string, to string, rename bool) error {
	dst, err := m.Clean(account, to)
	if err != nil {
		return err
	}
	srcs, err := m.cleanAll(account, from)
	if err != nil {
		return err
	}
	if rename {
		if len(srcs) != 1 || to == "" {
			return fmt.Errorf("rename takes one item and a new name")
		}
		_, err = m.as(ctx, account, nil, nil, 0, "mv", "-n", "-T", "--", srcs[0], dst)
		return friendlyIf(err, to)
	}
	args := append([]string{"-n", "-t", dst, "--"}, srcs...)
	_, err = m.as(ctx, account, nil, nil, 10*time.Minute, "mv", args...)
	return friendlyIf(err, to)
}

// Copy copies items into a folder, keeping modes and times.
func (m *Manager) Copy(ctx context.Context, account string, from []string, to string) error {
	dst, err := m.Clean(account, to)
	if err != nil {
		return err
	}
	srcs, err := m.cleanAll(account, from)
	if err != nil {
		return err
	}
	args := append([]string{"-a", "-n", "-t", dst, "--"}, srcs...)
	_, err = m.as(ctx, account, nil, nil, time.Hour, "cp", args...)
	return friendlyIf(err, to)
}

// Delete removes items for good. The home folder itself and the top-level
// folders Ike manages can't be deleted.
func (m *Manager) Delete(ctx context.Context, account string, items []string) error {
	srcs, err := m.cleanAll(account, items)
	if err != nil {
		return err
	}
	home := m.env.Home(account)
	for _, s := range srcs {
		rel := strings.TrimPrefix(s, home+"/")
		if s == home || rel == "web" || rel == "tmp" || rel == ".ssh" || (strings.Count(rel, "/") == 1 && strings.HasPrefix(rel, "web/")) {
			return fmt.Errorf("%s is managed by Ike; delete the website instead", rel)
		}
	}
	args := append([]string{"-rf", "--one-file-system", "--"}, srcs...)
	_, err = m.as(ctx, account, nil, nil, time.Hour, "rm", args...)
	return friendlyIf(err, "")
}

func (m *Manager) cleanAll(account string, rels []string) ([]string, error) {
	if len(rels) == 0 || len(rels) > 1000 {
		return nil, fmt.Errorf("choose 1-1000 items")
	}
	out := make([]string, 0, len(rels))
	for _, r := range rels {
		p, err := m.Clean(account, r)
		if err != nil {
			return nil, err
		}
		if r == "" {
			return nil, fmt.Errorf("the home folder itself can't be used here")
		}
		out = append(out, p)
	}
	return out, nil
}

// Chmod changes permissions. Mode is octal like 0644; setuid/setgid are refused.
func (m *Manager) Chmod(ctx context.Context, account string, items []string, mode string, recursive bool) error {
	n, err := strconv.ParseUint(mode, 8, 32)
	if err != nil || n > 0o1777 {
		return fmt.Errorf("mode must be octal like 0644 or 0755")
	}
	srcs, err := m.cleanAll(account, items)
	if err != nil {
		return err
	}
	args := []string{}
	if recursive {
		args = append(args, "-R")
	}
	args = append(append(args, fmt.Sprintf("%04o", n), "--"), srcs...)
	_, err = m.as(ctx, account, nil, nil, 10*time.Minute, "chmod", args...)
	return friendlyIf(err, "")
}

// Archive packs items into a .zip or .tar.gz in the same folder.
func (m *Manager) Archive(ctx context.Context, account, dir string, items []string, name, format string) error {
	base, err := m.Clean(account, dir)
	if err != nil {
		return err
	}
	if strings.ContainsAny(name, "/\x00") || name == "" || len(name) > 200 {
		return fmt.Errorf("archive name must be a plain file name")
	}
	for _, it := range items {
		if strings.Contains(it, "/") || it == "" || it == "." || it == ".." {
			return fmt.Errorf("items must be names inside the folder")
		}
	}
	if len(items) == 0 {
		return fmt.Errorf("choose what to pack")
	}
	dest := path.Join(base, name)
	switch format {
	case "zip":
		if !strings.HasSuffix(name, ".zip") {
			dest += ".zip"
		}
		// zip runs in the folder so paths inside the archive are relative
		_, err = m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: append([]string{"-u", account, "--", "zip", "-r", "-q", "-y", dest, "--"}, items...),
			Dir: base, Env: []string{"HOME=" + m.env.Home(account), "PATH=/usr/bin:/bin"}, Timeout: 2 * time.Hour})
	case "tar.gz", "":
		if !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".tgz") {
			dest += ".tar.gz"
		}
		_, err = m.as(ctx, account, nil, nil, 2*time.Hour, "tar", append([]string{"-czf", dest, "-C", base, "--"}, items...)...)
	default:
		return fmt.Errorf("format is zip or tar.gz")
	}
	return friendlyIf(err, dir)
}

// Extract unpacks a .zip, .tar, .tar.gz, .tgz, .tar.bz2 or .tar.xz into a
// folder. Existing files are not overwritten.
func (m *Manager) Extract(ctx context.Context, account, rel, into string) error {
	p, err := m.Clean(account, rel)
	if err != nil {
		return err
	}
	dst, err := m.Clean(account, into)
	if err != nil {
		return err
	}
	if _, err := m.as(ctx, account, nil, nil, 0, "mkdir", "-p", "--", dst); err != nil {
		return friendly(err, into)
	}
	lower := strings.ToLower(rel)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		_, err = m.as(ctx, account, nil, nil, 2*time.Hour, "unzip", "-q", "-n", "--", p, "-d", dst)
	case strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"),
		strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tar.xz"):
		_, err = m.as(ctx, account, nil, nil, 2*time.Hour, "tar", "-xf", p, "-C", dst, "--no-same-owner", "--no-same-permissions", "--keep-old-files", "--warning=no-existing-file")
		if err != nil && strings.Contains(err.Error(), "exit status 2") && !strings.Contains(err.Error(), "Error is not recoverable") {
			err = nil // some files existed and were kept
		}
	default:
		return fmt.Errorf("only .zip and .tar archives can be extracted")
	}
	return friendlyIf(err, rel)
}

// DownloadFolder streams a folder as .tar.gz.
func (m *Manager) DownloadFolder(ctx context.Context, account, rel string, w io.Writer) error {
	p, err := m.Clean(account, rel)
	if err != nil {
		return err
	}
	_, err = m.as(ctx, account, nil, w, 6*time.Hour, "tar", "-czf", "-", "-C", path.Dir(p), "--", path.Base(p))
	return friendlyIf(err, rel)
}

// ---- search ----

// Hit is one search result.
type Hit struct {
	Path string `json:"path"` // relative to the home
	Type string `json:"type"`
	Size int64  `json:"size"`
	Line string `json:"line,omitempty"` // content search: first matching line
}

// Search finds files by name (glob-like, case-insensitive) or by content
// (plain text, case-insensitive), inside a folder.
func (m *Manager) Search(ctx context.Context, account, rel, name, content string) ([]Hit, error) {
	dir, err := m.Clean(account, rel)
	if err != nil {
		return nil, err
	}
	home := m.env.Home(account) + "/"
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	hits := []Hit{}
	if content != "" {
		if len(content) > 200 || strings.ContainsAny(content, "\n\x00") {
			return nil, fmt.Errorf("search text must be one line, up to 200 characters")
		}
		var out bytes.Buffer
		_, err := m.as(ctx, account, nil, &out, 30*time.Second, "grep", "-r", "-I", "-i", "-F", "-n", "-m", "1",
			"--exclude-dir=.git", "--exclude-dir=node_modules", "--", content, dir)
		if err != nil && out.Len() == 0 && !strings.Contains(err.Error(), "exit status 1") {
			return nil, friendly(err, rel)
		}
		for _, l := range strings.Split(out.String(), "\n") {
			f := strings.SplitN(l, ":", 3)
			if len(f) < 3 {
				continue
			}
			line := f[2]
			if len(line) > 200 {
				line = line[:200]
			}
			hits = append(hits, Hit{Path: strings.TrimPrefix(f[0], home), Type: "file", Line: strings.TrimSpace(line)})
			if len(hits) >= 500 {
				break
			}
		}
		return hits, nil
	}
	if name == "" || len(name) > 100 || strings.ContainsAny(name, "/\x00") {
		return nil, fmt.Errorf("enter part of a file name")
	}
	pattern := name
	if !strings.ContainsAny(pattern, "*?[") {
		pattern = "*" + pattern + "*"
	}
	var out bytes.Buffer
	_, err = m.as(ctx, account, nil, &out, 30*time.Second, "find", dir, "-iname", pattern, "-printf", `%y\t%s\t%p\0`)
	if err != nil && out.Len() == 0 {
		return nil, friendly(err, rel)
	}
	for _, rec := range bytes.Split(out.Bytes(), []byte{0}) {
		f := strings.SplitN(string(rec), "\t", 3)
		if len(f) != 3 {
			continue
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)
		t := "file"
		if f[0] == "d" {
			t = "dir"
		}
		hits = append(hits, Hit{Path: strings.TrimPrefix(f[2], home), Type: t, Size: size})
		if len(hits) >= 500 {
			break
		}
	}
	return hits, nil
}

// ---- folder sizes ----

// Usage is one folder's disk use.
type Usage struct {
	Path  string `json:"path"` // relative to the home
	Bytes int64  `json:"bytes"`
}

// FolderSizes returns the size of a folder and each folder directly in it,
// largest first.
func (m *Manager) FolderSizes(ctx context.Context, account, rel string) ([]Usage, error) {
	dir, err := m.Clean(account, rel)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	_, err = m.as(ctx, account, nil, &out, 5*time.Minute, "du", "-x", "-d", "1", "-B1", "-0", "--", dir)
	if err != nil && out.Len() == 0 {
		return nil, friendly(err, rel)
	}
	home := m.env.Home(account)
	res := []Usage{}
	for _, rec := range bytes.Split(out.Bytes(), []byte{0}) {
		size, p, ok := strings.Cut(string(rec), "\t")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
		r := strings.TrimPrefix(strings.TrimPrefix(p, home), "/")
		res = append(res, Usage{Path: r, Bytes: n})
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Bytes > res[j].Bytes })
	return res, nil
}

var _ = filepath.Join
