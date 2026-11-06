// ike is the command-line tool for Ike servers: scripts, CI pipelines and
// people who prefer a terminal.
//
// It talks to the panel's API with an API token (from any computer), or —
// run as root on the server itself — straight to the agent's socket.
//
//	ike login https://panel.example.com:2087          (asks for a token, or --username to sign in)
//	ike accounts
//	ike sites create shop.ng --account shop
//	ike deploy shop.ng --ref main --wait
//	ike dns add shop.ng --account shop --type A --name www --content 203.0.113.10
//	ike apply server.json --plan
//	ike api GET /sites
//
// Licensed AGPL-3.0.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// readSecret reads a line without echoing it when stdin is a terminal.
func readSecret() (string, error) {
	fi, _ := os.Stdin.Stat()
	tty := fi != nil && fi.Mode()&os.ModeCharDevice != 0
	if tty && runtime.GOOS == "windows" {
		// No stty: say so, rather than echo a secret by surprise.
		fmt.Fprint(os.Stderr, "(it will show as you type; or set IKE_PASSWORD / use --token) ")
	} else if tty {
		off := exec.Command("stty", "-echo")
		off.Stdin = os.Stdin
		_ = off.Run()
		defer func() {
			on := exec.Command("stty", "echo")
			on.Stdin = os.Stdin
			_ = on.Run()
			fmt.Fprintln(os.Stderr)
		}()
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

const version = "0.5.0"

// ---------------------------------------------------------------- config

type profile struct {
	URL      string `json:"url"`
	Token    string `json:"token"`
	Insecure bool   `json:"insecure,omitempty"` // self-signed panel certificate
}

type fileConfig struct {
	Current  string              `json:"current"`
	Profiles map[string]*profile `json:"profiles"`
}

func configPath() string {
	if p := os.Getenv("IKE_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "ike", "config.json")
}

func loadConfig() *fileConfig {
	c := &fileConfig{Profiles: map[string]*profile{}}
	if b, err := os.ReadFile(configPath()); err == nil {
		_ = json.Unmarshal(b, c)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]*profile{}
	}
	return c
}

func (c *fileConfig) save() error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

// ---------------------------------------------------------------- client

type client struct {
	base     string // https://panel:2087/api/v1  or  http://agent/v1
	token    string
	http     *http.Client
	local    bool
	asJSON   bool
	insecure bool
	server   string // panel's id of another managed server (--server)
}

var errUsage = errors.New("usage")

func newClient(profileName string, asJSON bool) (*client, error) {
	// Environment wins, for CI: IKE_URL + IKE_TOKEN.
	url, token := os.Getenv("IKE_URL"), os.Getenv("IKE_TOKEN")
	insecure := os.Getenv("IKE_INSECURE") == "1"
	if url == "" {
		cfg := loadConfig()
		name := profileName
		if name == "" {
			name = cfg.Current
		}
		if p, ok := cfg.Profiles[name]; ok {
			url, token, insecure = p.URL, p.Token, p.Insecure
		}
	}
	if url == "" {
		// On the server as root: the agent's socket.
		sock := os.Getenv("IKE_AGENT_SOCKET")
		if sock == "" {
			sock = "/run/ike/agent.sock"
		}
		if _, err := os.Stat(sock); err == nil {
			tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			}}
			return &client{base: "http://agent/v1", http: &http.Client{Transport: tr, Timeout: 10 * time.Minute}, local: true, asJSON: asJSON}, nil
		}
		return nil, errors.New("not signed in. Run: ike login https://your-panel:2087")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the admin chose this for a self-signed panel
	}
	return &client{base: strings.TrimRight(url, "/") + "/api/v1", token: token, http: &http.Client{Transport: tr, Timeout: 10 * time.Minute}, asJSON: asJSON, insecure: insecure}, nil
}

type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return e.Msg }

func (c *client) do(method, path string, body any, out any) error {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case io.Reader:
		rd = b
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.server != "" {
		req.Header.Set("X-Ike-Server", c.server)
	}
	req.Header.Set("X-Ike-Actor", "cli")
	req.Header.Set("X-Ike-Trigger", "api")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &apiError{resp.StatusCode, e.Error}
	}
	if out == nil {
		return nil
	}
	if w, ok := out.(io.Writer); ok {
		_, err = w.Write(data)
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// show prints v as JSON with --json, or calls the table printer.
func (c *client) show(v any, table func(w *tabwriter.Writer)) {
	if c.asJSON || table == nil {
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(b))
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	table(w)
	_ = w.Flush()
}

// waitJob follows a background job until it ends, printing its log.
func (c *client) waitJob(id string) error {
	seen := 0
	for {
		var j struct {
			Status string   `json:"status"`
			Error  string   `json:"error"`
			Log    []string `json:"log"`
		}
		if err := c.do("GET", "/jobs/"+url.PathEscape(id), nil, &j); err != nil {
			return err
		}
		for ; seen < len(j.Log); seen++ {
			fmt.Println(j.Log[seen])
		}
		switch j.Status {
		case "done":
			return nil
		case "failed":
			return errors.New(j.Error)
		}
		time.Sleep(1500 * time.Millisecond)
	}
}

// nv prints a missing value as empty instead of <nil>.
func nv(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// ---------------------------------------------------------------- main

func main() {
	err := run(os.Args[1:])
	if err == nil {
		return
	}
	if errors.Is(err, errUsage) {
		usage()
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, "ike:", err)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == 401 {
		fmt.Fprintln(os.Stderr, "Sign in again with: ike login <panel address>")
	}
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `ike — command-line tool for Ike hosting servers

Sign in
  ike login URL [--token T | --username U] [--insecure] [--profile NAME]
  ike logout [--profile NAME]          ike profiles          ike whoami

Hosting
  ike accounts [create NAME | delete NAME | suspend NAME [--reason R] | unsuspend NAME]
  ike sites [--account A]
  ike sites create DOMAIN --account A [--runtime php|static|node|python] [--version V]
  ike sites delete DOMAIN [--remove-files]       ike sites show DOMAIN
  ike logs DOMAIN [--kind access|error|php|app] [--lines N]
  ike deploy DOMAIN [--ref REF] [--wait]         ike deployments DOMAIN
  ike rollback DOMAIN DEPLOYMENT_ID
  ike ssl DOMAIN [--wildcard] [--wait]
  ike protect DOMAIN                           password-protected paths and users
  ike protect DOMAIN user NAME [--password P]  add a sign-in user, or a new password
  ike protect DOMAIN path /admin/ [--realm R] [--user U ...] [--allow IP ...]
  ike protect DOMAIN unpath PATH | rm-user NAME

DNS
  ike dns zones [--account A]
  ike dns records ZONE --account A
  ike dns add ZONE --account A --type T --name N --content C [--ttl S] [--priority P]
  ike dns rm ZONE RECORD_ID --account A
  ike dns export ZONE --account A

Databases
  ike db list --account A       ike db create NAME --account A       ike db delete NAME --account A

Email
  ike mail domains [--account A]               ike mail enable DOMAIN --account A
  ike mail mailboxes DOMAIN --account A
  ike mail add USER@DOMAIN --account A [--name N] [--quota MB] [--send-only]
  ike mail passwd USER@DOMAIN --account A      ike mail rm USER@DOMAIN --account A [--delete-mail]
  ike mail forward USER@DOMAIN [TO ...] --account A   (no TO removes the forwarder)
  ike mail check DOMAIN --account A            deliverability: MX, SPF, DKIM, DMARC, PTR
  ike mail queue [flush|retry|delete [--id ID]] [--account A]
  ike mail log [--search TEXT] [--account A]

Files (paths are inside the account's home folder)
  ike files ls [PATH] --account A              ike files cat PATH --account A
  ike files get PATH [LOCAL] --account A       ike files put LOCAL PATH --account A [--overwrite]
  ike files mkdir|rm|mv|cp|chmod|extract ... --account A
  ike files find [PATH] --name N | --content TEXT --account A
  ike files du [PATH] --account A              ike files share PATH --account A [--hours H] [--password P]
  ike files snapshot --account A [--wait]      ike files snapshots --account A

Backups (leave out --account for the server's plans)
  ike backups [--account A]                    ike backups run|check PLAN [--wait]
  ike backups snapshots PLAN                   ike backups browse PLAN SNAPSHOT [PATH]
  ike backups restore PLAN SNAPSHOT [--path P ...] [--database D ...] [--overwrite] [--wait]

Security
  ike malware [scans] --account A              ike malware scan --account A [--path P] [--wait]
  ike malware quarantine SCAN_ID --account A [--finding ID ...]
  ike waf DOMAIN off|detect|block [--profile wordpress] [--disable-rule 942100 ...]
  ike waf DOMAIN events
  ike firewall                                 (ufw, nftables or firewalld: whichever the server uses)
  ike firewall open PORT/PROTO [--from ADDRESS/RANGE] [--note TEXT]
  ike firewall close PORT/PROTO [--from ADDRESS/RANGE]
  ike firewall block ADDRESS/RANGE [--note TEXT]    ike firewall unblock ADDRESS/RANGE
  ike firewall bans                            ike firewall unban JAIL IP

Access
  ike tokens                                   ike tokens revoke ID
  ike tokens create NAME --username YOU [--scope AREA=read|write ...] [--account A ...]
                    [--ip ADDRESS/RANGE ...] [--days N] [--read-only]
      areas: sites dns mail databases files backups containers security billing accounts server
  ike keys --account A
  ike keys add --account A --key-file ~/.ssh/id_ed25519.pub [--access shell|sftp|sftp-readonly]
               [--from ADDRESS ...] [--expires YYYY-MM-DD]
  ike keys set FINGERPRINT --account A --access ... [--from ...] [--expires ...]
  ike keys rm FINGERPRINT --account A
  ike sftp --account A                         website SFTP users (each sees one website)
  ike sftp add NAME --account A --domain D [--folder public_html] [--read-only] [--key-file F] [--expires DATE]
  ike sftp passwd USER --account A             ike sftp rm USER --account A

Developers (people who work in some of your accounts, in chosen areas)
  ike developers                               ike developers pause|resume|rm USERNAME
  ike developers invite USERNAME --email E --account A --area sites=write [--area files=write ...]
  ike developers set USERNAME [--account A ...] [--area AREA=read|write ...]
      areas: sites files databases containers dns mail backups security

Reseller and billing
  ike packages                                 ike packages assign ACCOUNT PACKAGE_ID
  ike packages bandwidth

Migration
  ike cpanel [list]                            ike cpanel upload backup-cpuser.tar.gz
  ike cpanel show FILE                         ike cpanel restore FILE [--as ACCOUNT] [--skip mail,dns] [--point-dns] [--wait]

Server
  ike servers                                  every server this panel manages
  ike metrics                                  CPU, memory, swap, network and disk right now
  ike status                    ike services          ike service NAME start|stop|restart
  ike jobs JOB_ID [--wait]      ike events
  ike apply FILE.json [--plan] [--var key=value ...] [--wait]
  ike api METHOD PATH [JSON]    raw API call; PATH like /sites or /accounts/shop

Every command takes --json for machine-readable output, --profile NAME, and
--server NAME to act on another server the panel manages. Destructive commands
ask first; --yes skips the question. A token only does what it was given:
limited tokens are refused outside their areas, accounts and addresses.
In CI, set IKE_URL and IKE_TOKEN instead of signing in. As root on the server,
ike uses the agent's socket directly when you're not signed in.
`)
}

// flags parses --name value pairs anywhere in args; returns positionals.
type flagSet struct {
	vals  map[string][]string
	bools map[string]bool
}

func parseFlags(args []string, boolNames ...string) ([]string, flagSet) {
	fs := flagSet{vals: map[string][]string{}, bools: map[string]bool{}}
	isBool := map[string]bool{"json": true}
	for _, b := range boolNames {
		isBool[b] = true
	}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") || a == "--" {
			pos = append(pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if isBool[name] {
			fs.bools[name] = !hasVal || val == "true" || val == "1"
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				fs.vals[name] = append(fs.vals[name], "")
				continue
			}
			i++
			val = args[i]
		}
		fs.vals[name] = append(fs.vals[name], val)
	}
	return pos, fs
}

func (f flagSet) get(n string) string {
	if v := f.vals[n]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

func (f flagSet) need(n string) (string, error) {
	v := f.get(n)
	if v == "" {
		return "", fmt.Errorf("--%s is required", n)
	}
	return v, nil
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		return errUsage
	}
	cmd, rest := args[0], args[1:]
	pos, fs := parseFlags(rest, "wait", "plan", "remove-files", "insecure", "wildcard", "with-sites",
		"yes", "overwrite", "delete-mail", "send-only", "rename", "recursive", "read-only", "point-dns")
	if cmd == "version" || cmd == "--version" {
		fmt.Println("ike", version)
		return nil
	}
	switch cmd {
	case "login":
		return login(pos, fs)
	case "logout":
		cfg := loadConfig()
		name := fs.get("profile")
		if name == "" {
			name = cfg.Current
		}
		delete(cfg.Profiles, name)
		if cfg.Current == name {
			cfg.Current = ""
		}
		return cfg.save()
	case "profiles":
		cfg := loadConfig()
		var names []string
		for n := range cfg.Profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			mark := " "
			if n == cfg.Current {
				mark = "*"
			}
			fmt.Printf("%s %-12s %s\n", mark, n, cfg.Profiles[n].URL)
		}
		return nil
	case "use":
		if len(pos) != 1 {
			return errUsage
		}
		cfg := loadConfig()
		if _, ok := cfg.Profiles[pos[0]]; !ok {
			return fmt.Errorf("no profile %s", pos[0])
		}
		cfg.Current = pos[0]
		return cfg.save()
	}
	c, err := newClient(fs.get("profile"), fs.bools["json"])
	if err != nil {
		return err
	}
	if srv := fs.get("server"); srv != "" && cmd != "servers" {
		if c.local {
			return errors.New("--server works through the panel; sign in with: ike login https://your-panel:2087")
		}
		if err := c.resolveServer(srv); err != nil {
			return err
		}
	}
	switch cmd {
	case "whoami":
		if c.local {
			fmt.Println("root on this server (agent socket)")
			return nil
		}
		var me map[string]any
		if err := c.do("GET", "/session", nil, &me); err != nil {
			return err
		}
		c.show(me, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "Signed in as\t%v (%v)\n", me["username"], me["role"])
			if t, ok := me["token"].(map[string]any); ok {
				fmt.Fprintf(w, "API token\t%v\nAccess\t%s\n", t["name"], tokenAccess(t))
				if e := nv(t["expires"]); e != "" {
					fmt.Fprintf(w, "Expires\t%s\n", e)
				}
			}
		})
		return nil
	case "status":
		var sys map[string]any
		if err := c.do("GET", "/system", nil, &sys); err != nil {
			return err
		}
		c.show(sys, nil)
		return nil
	case "services":
		var svcs []map[string]any
		if err := c.do("GET", "/services", nil, &svcs); err != nil {
			return err
		}
		c.show(svcs, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "SERVICE\tSTATE")
			for _, s := range svcs {
				fmt.Fprintf(w, "%v\t%v\n", s["name"], s["active"])
			}
		})
		return nil
	case "service":
		if len(pos) != 2 {
			return errUsage
		}
		return c.do("POST", "/services/"+url.PathEscape(pos[0])+"/"+url.PathEscape(pos[1]), nil, nil)
	case "accounts":
		return accounts(c, pos, fs)
	case "sites":
		return sitesCmd(c, pos, fs)
	case "logs":
		if len(pos) != 1 {
			return errUsage
		}
		kind := fs.get("kind")
		if kind == "" {
			kind = "error"
		}
		lines := fs.get("lines")
		if lines == "" {
			lines = "100"
		}
		var out struct {
			Lines []string `json:"lines"`
		}
		var raw json.RawMessage
		if err := c.do("GET", "/sites/"+url.PathEscape(pos[0])+"/logs/"+url.PathEscape(kind)+"?lines="+url.QueryEscape(lines), nil, &raw); err != nil {
			return err
		}
		if c.asJSON {
			fmt.Println(string(raw))
			return nil
		}
		if json.Unmarshal(raw, &out) == nil && out.Lines != nil {
			fmt.Println(strings.Join(out.Lines, "\n"))
			return nil
		}
		var l []string
		if json.Unmarshal(raw, &l) == nil {
			fmt.Println(strings.Join(l, "\n"))
			return nil
		}
		fmt.Println(string(raw))
		return nil
	case "deploy":
		return deploy(c, pos, fs)
	case "deployments":
		if len(pos) != 1 {
			return errUsage
		}
		var deps []map[string]any
		if err := c.do("GET", "/sites/"+url.PathEscape(pos[0])+"/deployments", nil, &deps); err != nil {
			return err
		}
		c.show(deps, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "ID\tSTATUS\tREF\tCOMMIT\tQUEUED")
			for _, d := range deps {
				fmt.Fprintf(w, "%v\t%v\t%v\t%.8s\t%v\n", d["id"], d["status"], nv(d["ref"]), nv(d["commit"]), d["queued_at"])
			}
		})
		return nil
	case "rollback":
		if len(pos) != 2 {
			return errUsage
		}
		var d map[string]any
		if err := c.do("POST", "/sites/"+url.PathEscape(pos[0])+"/deployments/"+url.PathEscape(pos[1])+"/rollback", nil, &d); err != nil {
			return err
		}
		fmt.Printf("Rolling back %s to %s (deployment %v)\n", pos[0], pos[1], d["id"])
		return nil
	case "ssl":
		if len(pos) != 1 {
			return errUsage
		}
		var j struct {
			ID string `json:"id"`
		}
		if err := c.do("POST", "/sites/"+url.PathEscape(pos[0])+"/ssl", map[string]any{"include_aliases": true, "wildcard": fs.bools["wildcard"]}, &j); err != nil {
			return err
		}
		if fs.bools["wait"] {
			return c.waitJob(j.ID)
		}
		fmt.Println("Started job", j.ID, "— follow it with: ike jobs", j.ID, "--wait")
		return nil
	case "dns":
		return dnsCmd(c, pos, fs)
	case "db":
		return dbCmd(c, pos, fs)
	case "jobs":
		if len(pos) != 1 {
			return errUsage
		}
		if fs.bools["wait"] {
			return c.waitJob(pos[0])
		}
		var j map[string]any
		if err := c.do("GET", "/jobs/"+url.PathEscape(pos[0]), nil, &j); err != nil {
			return err
		}
		c.show(j, nil)
		return nil
	case "events":
		var evs []map[string]any
		path := "/events?limit=50"
		if !c.local {
			path = "/notifications"
		}
		if err := c.do("GET", path, nil, &evs); err != nil {
			return err
		}
		c.show(evs, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "TIME\tLEVEL\tEVENT\tSUBJECT")
			for _, e := range evs {
				fmt.Fprintf(w, "%.19v\t%v\t%v\t%v\n", e["time"], e["level"], e["type"], e["subject"])
			}
		})
		return nil
	case "apply":
		return apply(c, pos, fs)
	case "api":
		return rawAPI(c, pos)
	case "mail":
		return mailCmd(c, pos, fs)
	case "files":
		return filesCmd(c, pos, fs)
	case "backups":
		return backupsCmd(c, pos, fs)
	case "malware":
		return malwareCmd(c, pos, fs)
	case "waf":
		return wafCmd(c, pos, fs)
	case "firewall", "fw":
		return firewallCmd(c, pos, fs)
	case "packages":
		return packagesCmd(c, pos, fs)
	case "servers":
		return serversCmd(c)
	case "metrics":
		return metricsCmd(c)
	case "protect":
		return protectCmd(c, pos, fs)
	case "sftp":
		return sftpCmd(c, pos, fs)
	case "developers", "team":
		return teamCmd(c, pos, fs)
	case "tokens":
		return tokensCmd(c, pos, fs)
	case "keys":
		return keysCmd(c, pos, fs)
	case "cpanel":
		return cpanelCmd(c, pos, fs)
	}
	return errUsage
}

func login(pos []string, fs flagSet) error {
	if len(pos) != 1 {
		return errors.New("give the panel address, e.g. ike login https://panel.example.com:2087")
	}
	u := strings.TrimRight(pos[0], "/")
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		u = "https://" + u
	}
	name := fs.get("profile")
	if name == "" {
		name = "default"
	}
	p := &profile{URL: u, Insecure: fs.bools["insecure"]}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if p.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the admin chose this
	}
	token := fs.get("token")
	if user := fs.get("username"); user != "" && token == "" {
		post, err := passwordSession(u, p.Insecure, user, fs.get("code"))
		if err != nil {
			return err
		}
		host, _ := os.Hostname()
		var tok map[string]any
		if code, err := post("/tokens", map[string]any{"name": "ike CLI on " + host}, &tok); err != nil || code != 201 {
			return fmt.Errorf("could not create an API token: %v %v", err, tok["error"])
		}
		token, _ = tok["token"].(string)
	}
	if token == "" {
		fmt.Fprint(os.Stderr, "API token (Settings → API tokens in the panel): ")
		t, err := readSecret()
		if err != nil {
			return err
		}
		token = strings.TrimSpace(t)
	}
	p.Token = token
	c := &client{base: u + "/api/v1", token: token, http: &http.Client{Transport: tr, Timeout: time.Minute}}
	var me map[string]any
	if err := c.do("GET", "/session", nil, &me); err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && strings.Contains(err.Error(), "certificate") {
			return fmt.Errorf("%v\nThe panel uses a self-signed certificate; add --insecure, or give the panel a real certificate", err)
		}
		return err
	}
	cfg := loadConfig()
	cfg.Profiles[name], cfg.Current = p, name
	if err := cfg.save(); err != nil {
		return err
	}
	fmt.Printf("Signed in to %s as %v (%v). Saved as profile %q.\n", u, me["username"], me["role"], name)
	return nil
}

func accounts(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 || pos[0] == "list" {
		var list []map[string]any
		if err := c.do("GET", "/accounts", nil, &list); err != nil {
			return err
		}
		c.show(list, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "ACCOUNT\tSITES\tSSH\tSUSPENDED")
			for _, a := range list {
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\n", a["name"], a["sites"], a["ssh"], a["suspended"])
			}
		})
		return nil
	}
	if len(pos) != 2 {
		return errUsage
	}
	name := url.PathEscape(pos[1])
	switch pos[0] {
	case "create":
		return c.do("POST", "/accounts", map[string]string{"name": pos[1]}, nil)
	case "delete":
		q := ""
		if fs.bools["with-sites"] {
			q = "?with_sites=true"
		}
		return c.do("DELETE", "/accounts/"+name+q, nil, nil)
	case "suspend":
		return c.do("POST", "/accounts/"+name+"/suspend", map[string]string{"reason": fs.get("reason")}, nil)
	case "unsuspend":
		return c.do("POST", "/accounts/"+name+"/unsuspend", map[string]string{}, nil)
	case "show":
		var a map[string]any
		if err := c.do("GET", "/accounts/"+name, nil, &a); err != nil {
			return err
		}
		c.show(a, nil)
		return nil
	}
	return errUsage
}

func sitesCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 || pos[0] == "list" {
		path := "/sites"
		if a := fs.get("account"); a != "" {
			path += "?account=" + url.QueryEscape(a)
		}
		var list []map[string]any
		if err := c.do("GET", path, nil, &list); err != nil {
			return err
		}
		c.show(list, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "DOMAIN\tACCOUNT\tRUNTIME\tSTATUS\tHTTPS")
			for _, s := range list {
				https := "no"
				if s["ssl"] != nil {
					https = "yes"
				}
				fmt.Fprintf(w, "%v\t%v\t%v %v\t%v\t%s\n", s["domain"], s["account"], s["runtime"], s["version"], s["status"], https)
			}
		})
		return nil
	}
	if len(pos) != 2 {
		return errUsage
	}
	switch pos[0] {
	case "create":
		acct, err := fs.need("account")
		if err != nil {
			return err
		}
		body := map[string]any{"domain": pos[1], "account": acct}
		for _, k := range []string{"runtime", "version", "web_server", "start_command"} {
			if v := fs.get(strings.ReplaceAll(k, "_", "-")); v != "" {
				body[k] = v
			}
		}
		var s map[string]any
		if err := c.do("POST", "/sites", body, &s); err != nil {
			return err
		}
		c.show(s, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "Created %v in %v (files: %v)\n", s["domain"], s["account"], s["doc_root"])
		})
		return nil
	case "delete":
		q := ""
		if fs.bools["remove-files"] {
			q = "?remove_files=true"
		}
		return c.do("DELETE", "/sites/"+url.PathEscape(pos[1])+q, nil, nil)
	case "show":
		var s map[string]any
		if err := c.do("GET", "/sites/"+url.PathEscape(pos[1]), nil, &s); err != nil {
			return err
		}
		c.show(s, nil)
		return nil
	}
	return errUsage
}

func deploy(c *client, pos []string, fs flagSet) error {
	if len(pos) != 1 {
		return errUsage
	}
	body := map[string]string{}
	if r := fs.get("ref"); r != "" {
		body["ref"] = r
	}
	if img := fs.get("image"); img != "" {
		body["image"] = img
	}
	var dep map[string]any
	if err := c.do("POST", "/sites/"+url.PathEscape(pos[0])+"/deployments", body, &dep); err != nil {
		return err
	}
	id, _ := dep["id"].(string)
	if !fs.bools["wait"] {
		c.show(dep, func(w *tabwriter.Writer) { fmt.Fprintf(w, "Deployment %s queued for %s\n", id, pos[0]) })
		return nil
	}
	fmt.Printf("Deployment %s queued for %s\n", id, pos[0])
	printed := 0
	for {
		var res struct {
			Deployment map[string]any `json:"deployment"`
			Log        string         `json:"log"`
		}
		if err := c.do("GET", "/sites/"+url.PathEscape(pos[0])+"/deployments/"+url.PathEscape(id), nil, &res); err != nil {
			return err
		}
		if len(res.Log) > printed {
			fmt.Print(res.Log[printed:])
			printed = len(res.Log)
		}
		switch res.Deployment["status"] {
		case "live":
			fmt.Printf("\n%s is live (%v)\n", pos[0], res.Deployment["commit"])
			return nil
		case "failed", "cancelled":
			return fmt.Errorf("deployment %s %v: %v", id, res.Deployment["status"], res.Deployment["error"])
		}
		time.Sleep(2 * time.Second)
	}
}

func dnsCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 {
		return errUsage
	}
	acctPath := func() (string, error) {
		a, err := fs.need("account")
		return "/accounts/" + url.PathEscape(a) + "/dns/zones", err
	}
	switch pos[0] {
	case "zones":
		path := "/dns/zones"
		if a := fs.get("account"); a != "" {
			path = "/accounts/" + url.PathEscape(a) + "/dns/zones"
		}
		var zones []map[string]any
		if err := c.do("GET", path, nil, &zones); err != nil {
			return err
		}
		c.show(zones, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "ZONE\tACCOUNT\tSERVED BY\tRECORDS\tDNSSEC")
			for _, z := range zones {
				recs, _ := z["records"].([]any)
				fmt.Fprintf(w, "%v\t%v\t%v\t%d\t%v\n", z["name"], z["account"], z["backend"], len(recs), z["dnssec"])
			}
		})
		return nil
	case "create":
		if len(pos) != 2 {
			return errUsage
		}
		base, err := acctPath()
		if err != nil {
			return err
		}
		return c.do("POST", base, map[string]string{"name": pos[1], "template": fs.get("template"), "backend": fs.get("backend")}, nil)
	case "records":
		if len(pos) != 2 {
			return errUsage
		}
		base, err := acctPath()
		if err != nil {
			return err
		}
		var z struct {
			Records []map[string]any `json:"records"`
		}
		if err := c.do("GET", base+"/"+url.PathEscape(pos[1]), nil, &z); err != nil {
			return err
		}
		c.show(z.Records, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "ID\tNAME\tTYPE\tTTL\tVALUE")
			for _, r := range z.Records {
				v := fmt.Sprint(r["content"])
				if p, ok := r["priority"].(float64); ok && p > 0 {
					v = strconv.Itoa(int(p)) + " " + v
				}
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%s\n", r["id"], r["name"], r["type"], r["ttl"], v)
			}
		})
		return nil
	case "add":
		if len(pos) != 2 {
			return errUsage
		}
		base, err := acctPath()
		if err != nil {
			return err
		}
		rec := map[string]any{"name": fs.get("name"), "type": fs.get("type"), "content": fs.get("content")}
		for _, k := range []string{"ttl", "priority", "weight", "port"} {
			if v := fs.get(k); v != "" {
				n, err := strconv.Atoi(v)
				if err != nil {
					return fmt.Errorf("--%s must be a number", k)
				}
				rec[k] = n
			}
		}
		if rec["type"] == "" || rec["content"] == "" {
			return errors.New("--type and --content are required")
		}
		return c.do("POST", base+"/"+url.PathEscape(pos[1])+"/records", rec, nil)
	case "rm":
		if len(pos) != 3 {
			return errUsage
		}
		base, err := acctPath()
		if err != nil {
			return err
		}
		return c.do("DELETE", base+"/"+url.PathEscape(pos[1])+"/records/"+url.PathEscape(pos[2]), nil, nil)
	case "export":
		if len(pos) != 2 {
			return errUsage
		}
		base, err := acctPath()
		if err != nil {
			return err
		}
		return c.do("GET", base+"/"+url.PathEscape(pos[1])+"/export", nil, os.Stdout)
	case "check":
		if len(pos) != 2 {
			return errUsage
		}
		base, err := acctPath()
		if err != nil {
			return err
		}
		var p map[string]any
		if err := c.do("GET", base+"/"+url.PathEscape(pos[1])+"/check", nil, &p); err != nil {
			return err
		}
		c.show(p, func(w *tabwriter.Writer) { fmt.Fprintln(w, p["explanation"]) })
		return nil
	}
	return errUsage
}

func dbCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 {
		return errUsage
	}
	a, err := fs.need("account")
	if err != nil {
		return err
	}
	base := "/accounts/" + url.PathEscape(a) + "/databases"
	switch pos[0] {
	case "list":
		var dbs []map[string]any
		if err := c.do("GET", base, nil, &dbs); err != nil {
			return err
		}
		c.show(dbs, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "DATABASE\tUSER\tNOTE")
			for _, d := range dbs {
				fmt.Fprintf(w, "%v\t%v\t%v\n", d["name"], d["user"], nv(d["note"]))
			}
		})
		return nil
	case "create":
		if len(pos) != 2 {
			return errUsage
		}
		var d map[string]any
		if err := c.do("POST", base, map[string]string{"name": pos[1], "note": fs.get("note")}, &d); err != nil {
			return err
		}
		c.show(d, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "Database\t%v\nUser\t%v\nPassword\t%v\t(shown once)\nHost\t%v\n", d["name"], d["user"], d["password"], d["host"])
		})
		return nil
	case "delete":
		if len(pos) != 2 {
			return errUsage
		}
		return c.do("DELETE", base+"/"+url.PathEscape(pos[1]), nil, nil)
	}
	return errUsage
}

func apply(c *client, pos []string, fs flagSet) error {
	if len(pos) != 1 {
		return errUsage
	}
	var raw []byte
	var err error
	if pos[0] == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(pos[0])
	}
	if err != nil {
		return err
	}
	if !json.Valid(raw) {
		return fmt.Errorf("%s isn't valid JSON", pos[0])
	}
	vars := map[string]string{}
	for _, kv := range fs.vals["var"] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--var takes key=value")
		}
		vars[k] = v
	}
	body := map[string]any{"spec": json.RawMessage(raw), "variables": vars}
	if fs.bools["plan"] {
		var plan any
		if err := c.do("POST", "/spec/plan", body, &plan); err != nil {
			return err
		}
		c.show(plan, nil)
		return nil
	}
	var j struct {
		ID string `json:"id"`
	}
	if err := c.do("POST", "/spec/apply", body, &j); err != nil {
		return err
	}
	if fs.bools["wait"] {
		return c.waitJob(j.ID)
	}
	fmt.Println("Applying in job", j.ID, "— follow it with: ike jobs", j.ID, "--wait")
	return nil
}

func rawAPI(c *client, pos []string) error {
	if len(pos) < 2 {
		return errUsage
	}
	method := strings.ToUpper(pos[0])
	path := pos[1]
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimPrefix(strings.TrimPrefix(path, "/api/v1"), "/v1")
	var body any
	if len(pos) > 2 {
		if pos[2] == "-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			body = b
		} else {
			body = []byte(pos[2])
		}
	}
	var buf bytes.Buffer
	if err := c.do(method, path, body, &buf); err != nil {
		return err
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, buf.Bytes(), "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Print(buf.String())
	}
	return nil
}
