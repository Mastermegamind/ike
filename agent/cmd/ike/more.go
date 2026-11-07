package main

// Commands for email, files, backups, security, packages, servers, API
// tokens, SSH keys and cPanel imports.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

func esc(s string) string { return url.PathEscape(s) }

func acctBase(fs flagSet) (string, error) {
	a, err := fs.need("account")
	return "/accounts/" + esc(a), err
}

// confirm asks before destructive commands unless --yes was given.
func confirm(fs flagSet, what string) error {
	if fs.bools["yes"] {
		return nil
	}
	fmt.Fprintf(os.Stderr, "%s Type yes to continue: ", what)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != "yes" {
		return errors.New("cancelled; nothing was changed")
	}
	return nil
}

// runJob prints the job id, or follows it with --wait.
func (c *client) runJob(fs flagSet, method, path string, body any) error {
	var j map[string]any
	if err := c.do(method, path, body, &j); err != nil {
		return err
	}
	id, _ := j["id"].(string)
	if id == "" {
		c.show(j, nil)
		return nil
	}
	if fs.bools["wait"] {
		return c.waitJob(id)
	}
	fmt.Printf("Started job %s. Follow it with: ike jobs %s --wait\n", id, id)
	return nil
}

func splitAddress(addr string) (local, domain string, err error) {
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" || domain == "" {
		return "", "", fmt.Errorf("%q is not an email address", addr)
	}
	return local, domain, nil
}

// ---------------------------------------------------------------- mail

func mailCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 {
		return errUsage
	}
	base, _ := acctBase(fs)
	needAcct := func() error {
		_, err := fs.need("account")
		return err
	}
	switch pos[0] {
	case "domains":
		path := "/mail/domains"
		if fs.get("account") != "" {
			path = base + "/mail/domains"
		}
		var ds []map[string]any
		if err := c.do("GET", path, nil, &ds); err != nil {
			return err
		}
		c.show(ds, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "DOMAIN\tACCOUNT\tMAILBOXES\tFORWARDERS\tDKIM\tTLS")
			for _, d := range ds {
				m, _ := d["mailboxes"].([]any)
				f, _ := d["aliases"].([]any)
				fmt.Fprintf(w, "%v\t%v\t%d\t%d\t%v\t%v\n", d["domain"], d["account"], len(m), len(f), d["dkim"], d["tls"])
			}
		})
		return nil
	case "enable":
		if len(pos) != 2 || needAcct() != nil {
			return errUsage
		}
		return c.do("POST", base+"/mail/domains", map[string]string{"domain": pos[1]}, nil)
	case "mailboxes":
		if len(pos) != 2 || needAcct() != nil {
			return errUsage
		}
		var d struct {
			Mailboxes []map[string]any `json:"mailboxes"`
		}
		if err := c.do("GET", base+"/mail/domains/"+esc(pos[1]), nil, &d); err != nil {
			return err
		}
		c.show(d.Mailboxes, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "ADDRESS\tNAME\tUSED MB\tQUOTA MB\tON")
			for _, m := range d.Mailboxes {
				used := ""
				if u, ok := m["usage"].(map[string]any); ok {
					used = fmt.Sprintf("%.0f", u["used_mb"])
				}
				fmt.Fprintf(w, "%v@%s\t%v\t%s\t%v\t%v\n", m["local"], pos[1], nv(m["name"]), used, m["quota_mb"], m["enabled"])
			}
		})
		return nil
	case "add":
		if len(pos) != 2 || needAcct() != nil {
			return errUsage
		}
		local, domain, err := splitAddress(pos[1])
		if err != nil {
			return err
		}
		body := map[string]any{"local": local, "name": fs.get("name"), "send_only": fs.bools["send-only"]}
		if q := fs.get("quota"); q != "" {
			n, err := strconv.Atoi(q)
			if err != nil {
				return errors.New("--quota is in MB, e.g. 2048")
			}
			body["quota_mb"] = n
		}
		var r map[string]any
		if err := c.do("POST", base+"/mail/domains/"+esc(domain)+"/mailboxes", body, &r); err != nil {
			return err
		}
		c.show(r, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "Address\t%v\nPassword\t%v\t(shown once)\nIncoming (IMAP)\t%v, port 993, SSL\nOutgoing (SMTP)\t%v, port 465, SSL\n", r["address"], r["password"], r["imap_host"], r["smtp_host"])
		})
		return nil
	case "passwd":
		if len(pos) != 2 || needAcct() != nil {
			return errUsage
		}
		local, domain, err := splitAddress(pos[1])
		if err != nil {
			return err
		}
		pw := os.Getenv("IKE_MAIL_PASSWORD")
		if pw == "" {
			fmt.Fprint(os.Stderr, "New password (10+ characters): ")
			if pw, err = readSecret(); err != nil {
				return err
			}
		}
		return c.mailboxUpdate(base, domain, local, map[string]any{"password": pw})
	case "rm":
		if len(pos) != 2 || needAcct() != nil {
			return errUsage
		}
		local, domain, err := splitAddress(pos[1])
		if err != nil {
			return err
		}
		msg := "Delete mailbox " + pos[1] + " (its messages are kept on disk)?"
		q := ""
		if fs.bools["delete-mail"] {
			msg, q = "Delete mailbox "+pos[1]+" and every message in it?", "?delete_mail=true"
		}
		if err := confirm(fs, msg); err != nil {
			return err
		}
		return c.do("DELETE", base+"/mail/domains/"+esc(domain)+"/mailboxes/"+esc(local)+q, nil, nil)
	case "forward":
		// ike mail forward sales@example.com ada@gmail.com [more...]  (no targets removes it)
		if len(pos) < 2 || needAcct() != nil {
			return errUsage
		}
		local, domain, err := splitAddress(pos[1])
		if err != nil {
			return err
		}
		var d struct {
			Aliases []map[string]any `json:"aliases"`
		}
		if err := c.do("GET", base+"/mail/domains/"+esc(domain), nil, &d); err != nil {
			return err
		}
		out := []map[string]any{}
		for _, a := range d.Aliases {
			if a["from"] != local {
				out = append(out, a)
			}
		}
		if len(pos) > 2 {
			out = append(out, map[string]any{"from": local, "to": pos[2:]})
		}
		return c.do("PUT", base+"/mail/domains/"+esc(domain)+"/aliases", out, nil)
	case "check":
		if len(pos) != 2 || needAcct() != nil {
			return errUsage
		}
		var cs []map[string]any
		if err := c.do("GET", base+"/mail/domains/"+esc(pos[1])+"/checks", nil, &cs); err != nil {
			return err
		}
		c.show(cs, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "CHECK\tSTATUS\tWHAT TO DO")
			for _, x := range cs {
				fmt.Fprintf(w, "%v\t%v\t%v\n", x["record"], x["status"], x["hint"])
			}
		})
		return nil
	case "queue", "log":
		path := "/mail/" + pos[0]
		if fs.get("account") != "" {
			path = base + path
		}
		if s := fs.get("search"); s != "" && pos[0] == "log" {
			path += "?search=" + url.QueryEscape(s)
		}
		if len(pos) == 2 && pos[0] == "queue" { // ike mail queue flush|retry|delete [--id ID]
			p := path + "/" + esc(pos[1])
			if id := fs.get("id"); id != "" {
				p += "?id=" + url.QueryEscape(id)
			}
			return c.do("POST", p, nil, nil)
		}
		var rows []map[string]any
		if err := c.do("GET", path, nil, &rows); err != nil {
			return err
		}
		c.show(rows, func(w *tabwriter.Writer) {
			if pos[0] == "queue" {
				fmt.Fprintln(w, "ID\tFROM\tTO\tWHY IT'S WAITING")
				for _, m := range rows {
					rs, _ := m["recipients"].([]any)
					for _, r := range rs {
						rm, _ := r.(map[string]any)
						fmt.Fprintf(w, "%v\t%v\t%v\t%v\n", m["id"], m["sender"], rm["address"], nv(rm["delay_reason"]))
					}
				}
				return
			}
			fmt.Fprintln(w, "TIME\tFROM\tTO\tSTATUS\tDETAIL")
			for _, d := range rows {
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\n", d["time"], nv(d["from"]), d["to"], d["status"], d["detail"])
			}
		})
		return nil
	}
	return errUsage
}

// mailboxUpdate changes some fields of a mailbox and keeps the rest.
func (c *client) mailboxUpdate(base, domain, local string, set map[string]any) error {
	var d struct {
		Mailboxes []map[string]any `json:"mailboxes"`
	}
	if err := c.do("GET", base+"/mail/domains/"+esc(domain), nil, &d); err != nil {
		return err
	}
	for _, m := range d.Mailboxes {
		if m["local"] == local {
			for _, k := range []string{"local", "created_at", "usage"} {
				delete(m, k)
			}
			for k, v := range set {
				m[k] = v
			}
			return c.do("PUT", base+"/mail/domains/"+esc(domain)+"/mailboxes/"+esc(local), m, nil)
		}
	}
	return fmt.Errorf("%s@%s doesn't exist", local, domain)
}

// ---------------------------------------------------------------- files

func filesCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 {
		return errUsage
	}
	base, err := acctBase(fs)
	if err != nil {
		return err
	}
	q := func(p string) string { return "?path=" + url.QueryEscape(p) }
	op := func(name string, body any) error { return c.do("POST", base+"/files/"+name, body, nil) }
	switch pos[0] {
	case "ls":
		p := ""
		if len(pos) > 1 {
			p = pos[1]
		}
		var es []map[string]any
		if err := c.do("GET", base+"/files"+q(p), nil, &es); err != nil {
			return err
		}
		c.show(es, func(w *tabwriter.Writer) {
			for _, e := range es {
				name := fmt.Sprint(e["name"])
				if e["type"] == "dir" {
					name += "/"
				}
				fmt.Fprintf(w, "%v\t%10.0f\t%v\t%s\n", e["mode"], e["size"], e["modified"], name)
			}
		})
		return nil
	case "cat":
		if len(pos) != 2 {
			return errUsage
		}
		var f struct {
			Content string `json:"content"`
		}
		if err := c.do("GET", base+"/files/content"+q(pos[1]), nil, &f); err != nil {
			return err
		}
		fmt.Print(f.Content)
		return nil
	case "get":
		if len(pos) < 2 || len(pos) > 3 {
			return errUsage
		}
		local := filepath.Base(pos[1])
		if len(pos) == 3 {
			local = pos[2]
		}
		f, err := os.Create(local)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := c.do("GET", base+"/files/download"+q(pos[1]), nil, f); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Saved", local)
		return nil
	case "put":
		if len(pos) != 3 {
			return errUsage
		}
		f, err := os.Open(pos[1])
		if err != nil {
			return err
		}
		defer f.Close()
		ow := ""
		if fs.bools["overwrite"] {
			ow = "&overwrite=true"
		}
		return c.do("PUT", base+"/files/upload"+q(pos[2])+ow, f, nil)
	case "mkdir":
		if len(pos) != 2 {
			return errUsage
		}
		return op("mkdir", map[string]any{"path": pos[1]})
	case "mv":
		if len(pos) < 3 {
			return errUsage
		}
		if fs.bools["rename"] {
			return op("move", map[string]any{"from": pos[1:2], "to": pos[2], "rename": true})
		}
		return op("move", map[string]any{"from": pos[1 : len(pos)-1], "to": pos[len(pos)-1]})
	case "cp":
		if len(pos) < 3 {
			return errUsage
		}
		return op("copy", map[string]any{"from": pos[1 : len(pos)-1], "to": pos[len(pos)-1]})
	case "rm":
		if len(pos) < 2 {
			return errUsage
		}
		if err := confirm(fs, fmt.Sprintf("Delete %s?", strings.Join(pos[1:], ", "))); err != nil {
			return err
		}
		return op("delete", map[string]any{"paths": pos[1:]})
	case "chmod":
		if len(pos) < 3 {
			return errUsage
		}
		return op("chmod", map[string]any{"mode": pos[1], "paths": pos[2:], "recursive": fs.bools["recursive"]})
	case "extract":
		if len(pos) != 3 {
			return errUsage
		}
		return op("extract", map[string]any{"path": pos[1], "into": pos[2]})
	case "find":
		p := base + "/files/search?name=" + url.QueryEscape(fs.get("name")) + "&content=" + url.QueryEscape(fs.get("content"))
		if len(pos) > 1 {
			p += "&path=" + url.QueryEscape(pos[1])
		}
		var hs []map[string]any
		if err := c.do("GET", p, nil, &hs); err != nil {
			return err
		}
		c.show(hs, func(w *tabwriter.Writer) {
			for _, h := range hs {
				fmt.Fprintf(w, "%v\t%v\n", h["path"], nv(h["line"]))
			}
		})
		return nil
	case "du":
		p := ""
		if len(pos) > 1 {
			p = pos[1]
		}
		var ss []map[string]any
		if err := c.do("GET", base+"/files/sizes"+q(p), nil, &ss); err != nil {
			return err
		}
		sort.Slice(ss, func(i, j int) bool { return toF(ss[i]["bytes"]) > toF(ss[j]["bytes"]) })
		c.show(ss, func(w *tabwriter.Writer) {
			for _, s := range ss {
				fmt.Fprintf(w, "%s\t%v\n", human(toF(s["bytes"])), s["path"])
			}
		})
		return nil
	case "share":
		if len(pos) != 2 {
			return errUsage
		}
		body := map[string]any{"path": pos[1], "password": fs.get("password")}
		if h := fs.get("hours"); h != "" {
			n, _ := strconv.Atoi(h)
			body["expires_hours"] = n
		}
		var s map[string]any
		if err := c.do("POST", base+"/shares", body, &s); err != nil {
			return err
		}
		link := fmt.Sprint(s["url_path"])
		if !c.local {
			link = strings.TrimSuffix(c.base, "/api/v1") + link
		}
		c.show(s, func(w *tabwriter.Writer) { fmt.Fprintf(w, "Link\t%s\nExpires\t%v\n", link, s["expires_at"]) })
		return nil
	case "snapshot":
		return c.runJob(fs, "POST", base+"/snapshots", nil)
	case "snapshots":
		var ss []map[string]any
		if err := c.do("GET", base+"/snapshots", nil, &ss); err != nil {
			return err
		}
		c.show(ss, func(w *tabwriter.Writer) {
			for _, s := range ss {
				fmt.Fprintf(w, "%v\t%v\n", s["id"], s["time"])
			}
		})
		return nil
	}
	return errUsage
}

func toF(v any) float64 { f, _ := v.(float64); return f }

func human(b float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}

// ---------------------------------------------------------------- backups

func backupsCmd(c *client, pos []string, fs flagSet) error {
	base := "/backups/plans"
	if a := fs.get("account"); a != "" {
		base = "/accounts/" + esc(a) + "/backups/plans"
	}
	if len(pos) == 0 || pos[0] == "plans" {
		var ps []map[string]any
		if err := c.do("GET", base, nil, &ps); err != nil {
			return err
		}
		c.show(ps, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "ID\tNAME\tWHAT\tSCHEDULE\tLAST RUN\tRESULT")
			for _, p := range ps {
				sch, _ := p["schedule"].(map[string]any)
				last, _ := p["last"].(map[string]any)
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%v\n", p["id"], p["name"], p["scope"], sch["frequency"], nv(last["started_at"]), nv(last["status"]))
			}
		})
		return nil
	}
	if len(pos) < 2 {
		return errUsage
	}
	plan := base + "/" + esc(pos[1])
	switch pos[0] {
	case "run":
		return c.runJob(fs, "POST", plan+"/run", nil)
	case "check":
		return c.runJob(fs, "POST", plan+"/check", nil)
	case "snapshots":
		var ss []map[string]any
		if err := c.do("GET", plan+"/snapshots", nil, &ss); err != nil {
			return err
		}
		c.show(ss, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "ID\tTIME\tSIZE")
			for _, s := range ss {
				fmt.Fprintf(w, "%v\t%v\t%s\n", s["id"], s["time"], human(toF(s["size"])))
			}
		})
		return nil
	case "browse":
		if len(pos) < 3 {
			return errUsage
		}
		p := "/"
		if len(pos) > 3 {
			p = pos[3]
		}
		var ns []map[string]any
		if err := c.do("GET", plan+"/snapshots/"+esc(pos[2])+"/browse?path="+url.QueryEscape(p), nil, &ns); err != nil {
			return err
		}
		c.show(ns, func(w *tabwriter.Writer) {
			for _, n := range ns {
				fmt.Fprintf(w, "%v\t%s\t%v\n", n["type"], human(toF(n["size"])), n["path"])
			}
		})
		return nil
	case "restore":
		// ike backups restore PLAN SNAPSHOT [--path P ...] [--database D ...] [--overwrite] [--yes] [--wait]
		if len(pos) != 3 {
			return errUsage
		}
		body := map[string]any{"snapshot": pos[2], "paths": fs.vals["path"], "databases": fs.vals["database"], "overwrite": fs.bools["overwrite"]}
		what := "Restore from backup " + pos[2]
		if fs.bools["overwrite"] {
			what += ", replacing the current files and databases"
		}
		if err := confirm(fs, what+"?"); err != nil {
			return err
		}
		return c.runJob(fs, "POST", plan+"/restore", body)
	}
	return errUsage
}

// ---------------------------------------------------------------- security

func malwareCmd(c *client, pos []string, fs flagSet) error {
	base, err := acctBase(fs)
	if err != nil {
		return err
	}
	if len(pos) == 0 || pos[0] == "scans" {
		var ss []map[string]any
		if err := c.do("GET", base+"/malware/scans", nil, &ss); err != nil {
			return err
		}
		c.show(ss, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "SCAN\tSTARTED\tFILES\tFOUND\tSTATUS")
			for _, s := range ss {
				f, _ := s["findings"].([]any)
				fmt.Fprintf(w, "%v\t%v\t%v\t%d\t%v\n", s["id"], s["started_at"], s["files"], len(f), s["status"])
				for _, x := range f {
					m, _ := x.(map[string]any)
					fmt.Fprintf(w, "\t  %v\t%v\t%v\t\n", m["severity"], m["signature"], m["path"])
				}
			}
		})
		return nil
	}
	switch pos[0] {
	case "scan":
		return c.runJob(fs, "POST", base+"/malware/scans", map[string]string{"path": fs.get("path")})
	case "quarantine":
		if len(pos) != 2 {
			return errUsage
		}
		return c.do("POST", base+"/malware/scans/"+esc(pos[1])+"/quarantine", map[string]any{"findings": fs.vals["finding"]}, nil)
	}
	return errUsage
}

func wafCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) < 2 {
		return errUsage
	}
	site := "/sites/" + esc(pos[0]) + "/waf"
	if pos[1] == "events" {
		var es []map[string]any
		if err := c.do("GET", site+"/events", nil, &es); err != nil {
			return err
		}
		c.show(es, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "TIME\tFROM\tRULE\tBLOCKED\tREQUEST")
			for _, e := range es {
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\n", e["time"], e["client"], e["rule_id"], e["blocked"], e["uri"])
			}
		})
		return nil
	}
	body := map[string]any{"mode": pos[1], "profile": fs.get("profile")}
	var rules []int
	for _, r := range fs.vals["disable-rule"] {
		n, err := strconv.Atoi(r)
		if err != nil {
			return fmt.Errorf("--disable-rule takes a rule number, e.g. 942100")
		}
		rules = append(rules, n)
	}
	if rules != nil {
		body["disabled_rules"] = rules
	}
	return c.do("PUT", site, body, nil)
}

// ---------------------------------------------------------------- business

func packagesCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 || pos[0] == "list" {
		var ps []map[string]any
		if err := c.do("GET", "/packages", nil, &ps); err != nil {
			return err
		}
		c.show(ps, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "ID\tNAME\tPRICE\tSITES\tDISK MB\tBANDWIDTH GB\tACCOUNTS")
			for _, p := range ps {
				l, _ := p["limits"].(map[string]any)
				price := fmt.Sprintf("%s %.2f/%v", p["currency"], toF(p["price"])/100, p["interval"])
				fmt.Fprintf(w, "%v\t%v\t%s\t%v\t%v\t%v\t%v\n", p["id"], p["name"], price, unl(l["sites"]), unl(l["disk_mb"]), unl(l["bandwidth_gb"]), nv(p["accounts"]))
			}
		})
		return nil
	}
	if pos[0] == "assign" && len(pos) == 3 {
		return c.do("PUT", "/account-packages/"+esc(pos[1]), map[string]string{"package": pos[2]}, nil)
	}
	if pos[0] == "bandwidth" {
		var b struct {
			Month    string           `json:"month"`
			Accounts []map[string]any `json:"accounts"`
		}
		if err := c.do("GET", "/bandwidth", nil, &b); err != nil {
			return err
		}
		c.show(b, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "ACCOUNT\tUSED (%s)\tCAP GB\tSUSPENDED\n", b.Month)
			for _, a := range b.Accounts {
				fmt.Fprintf(w, "%v\t%s\t%v\t%v\n", a["account"], human(toF(a["used_mb"])*1024*1024), unl(a["limit_gb"]), a["suspended_for_bandwidth"])
			}
		})
		return nil
	}
	return errUsage
}

func unl(v any) string {
	if toF(v) == 0 {
		return "unlimited"
	}
	return fmt.Sprint(v)
}

func serversCmd(c *client) error {
	var ss []map[string]any
	if err := c.do("GET", "/servers", nil, &ss); err != nil {
		return err
	}
	c.show(ss, func(w *tabwriter.Writer) {
		fmt.Fprintln(w, "ID\tNAME\tGROUP\tADDRESS\tVERSION\tSTATUS")
		for _, s := range ss {
			fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%v\n", s["id"], s["name"], nv(s["group"]), s["address"], nv(s["version"]), nv(s["status"]))
		}
	})
	return nil
}

// resolveServer turns --server NAME into the id the panel expects.
func (c *client) resolveServer(name string) error {
	if name == "" || name == "local" {
		return nil
	}
	var ss []map[string]any
	if err := c.do("GET", "/servers", nil, &ss); err != nil {
		return err
	}
	for _, s := range ss {
		if s["id"] == name || s["name"] == name {
			c.server = fmt.Sprint(s["id"])
			return nil
		}
	}
	return fmt.Errorf("no server called %q; see: ike servers", name)
}

// ---------------------------------------------------------------- tokens

func tokensCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 || pos[0] == "list" {
		var ts []map[string]any
		if err := c.do("GET", "/tokens", nil, &ts); err != nil {
			return err
		}
		c.show(ts, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "ID\tNAME\tACCESS\tLAST USED\tEXPIRES")
			for _, t := range ts {
				fmt.Fprintf(w, "%v\t%v\t%s\t%v\t%v\n", t["id"], t["name"], tokenAccess(t), nv(t["last_used"]), nv(t["expires"]))
			}
		})
		return nil
	}
	switch pos[0] {
	case "revoke":
		if len(pos) != 2 {
			return errUsage
		}
		return c.do("DELETE", "/tokens/"+esc(pos[1]), nil, nil)
	case "create":
		return createToken(c, pos, fs)
	}
	return errUsage
}

// tokenAccess describes what a token may do in a few words.
func tokenAccess(t map[string]any) string {
	var parts []string
	if t["read_only"] == true {
		parts = append(parts, "read only")
	}
	if t["limited"] == true {
		sc, _ := t["scopes"].(map[string]any)
		keys := make([]string, 0, len(sc))
		for k := range sc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var s []string
		for _, k := range keys {
			s = append(s, k+"="+fmt.Sprint(sc[k]))
		}
		if len(s) == 0 {
			s = []string{"nothing"}
		}
		parts = append(parts, strings.Join(s, ","))
	} else if t["read_only"] != true {
		parts = append(parts, "full")
	}
	if as, _ := t["accounts"].([]any); len(as) > 0 {
		parts = append(parts, "accounts "+joinAny(as))
	}
	if ips, _ := t["allow_ips"].([]any); len(ips) > 0 {
		parts = append(parts, "from "+joinAny(ips))
	}
	return strings.Join(parts, "; ")
}

func joinAny(v []any) string {
	s := make([]string, len(v))
	for i, x := range v {
		s[i] = fmt.Sprint(x)
	}
	return strings.Join(s, ",")
}

// createToken signs in with a password (the panel only makes tokens for a
// signed-in person, never for another token) and creates a token.
//
//	ike tokens create NAME --username U [--scope dns=write ...] [--account A ...]
//	                       [--ip CIDR ...] [--days N] [--read-only]
func createToken(c *client, pos []string, fs flagSet) error {
	if len(pos) != 2 || c.local {
		return errors.New("usage: ike tokens create NAME --username YOU [--scope AREA=read|write ...] (needs a panel profile)")
	}
	user, err := fs.need("username")
	if err != nil {
		return err
	}
	body := map[string]any{"name": pos[1], "read_only": fs.bools["read-only"]}
	if sc := fs.vals["scope"]; len(sc) > 0 {
		scopes := map[string]string{}
		for _, s := range sc {
			area, level, ok := strings.Cut(s, "=")
			if !ok {
				return fmt.Errorf("--scope %q: use AREA=read or AREA=write, e.g. --scope dns=write", s)
			}
			scopes[area] = level
		}
		body["limited"], body["scopes"] = true, scopes
	}
	if a := fs.vals["account"]; len(a) > 0 {
		body["accounts"] = a
	}
	if ip := fs.vals["ip"]; len(ip) > 0 {
		body["allow_ips"] = ip
	}
	if d := fs.get("days"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil {
			return errors.New("--days is a number of days")
		}
		body["days"] = n
	}
	post, err := passwordSession(strings.TrimSuffix(c.base, "/api/v1"), c.insecure, user, fs.get("code"))
	if err != nil {
		return err
	}
	var tok map[string]any
	code, err := post("/tokens", body, &tok)
	if err != nil {
		return err
	}
	if code != 201 {
		return fmt.Errorf("could not create the token: %v", tok["error"])
	}
	c.show(tok, func(w *tabwriter.Writer) {
		fmt.Fprintf(w, "Token\t%v\nAccess\t%s\n\nThe token is shown once. Use it with: IKE_URL=%s IKE_TOKEN=… ike …\n", tok["token"], tokenAccess(tok), strings.TrimSuffix(c.base, "/api/v1"))
	})
	return nil
}

type poster func(path string, body any, out any) (int, error)

// passwordSession signs in to the panel like the browser does and returns
// a function that POSTs with that session. The password comes from
// IKE_PASSWORD or is asked for; a 2FA code from --code or asked for.
func passwordSession(panel string, insecure bool, user, code string) (poster, error) {
	pw := os.Getenv("IKE_PASSWORD")
	if pw == "" {
		fmt.Fprint(os.Stderr, "Password: ")
		var err error
		if pw, err = readSecret(); err != nil {
			return nil, err
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the admin chose this
	}
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Transport: tr, Jar: jar, Timeout: time.Minute}
	post := func(path string, body any, out any) (int, error) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", panel+"/api/v1"+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "ike")
		resp, err := hc.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(data, out)
		return resp.StatusCode, nil
	}
	var res map[string]any
	status, err := post("/session", map[string]string{"username": user, "password": pw, "code": code}, &res)
	if err != nil {
		return nil, err
	}
	if status == 401 && res["two_factor_required"] == true {
		fmt.Fprint(os.Stderr, "Authentication code: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if status, err = post("/session", map[string]string{"username": user, "password": pw, "code": strings.TrimSpace(line)}, &res); err != nil {
			return nil, err
		}
	}
	if status != 200 {
		return nil, fmt.Errorf("sign-in failed: %v", res["error"])
	}
	return post, nil
}

// ---------------------------------------------------------------- SSH keys

func keysCmd(c *client, pos []string, fs flagSet) error {
	base, err := acctBase(fs)
	if err != nil {
		return err
	}
	keys := base + "/ssh/keys"
	access := func() (map[string]any, error) {
		a := fs.get("access")
		switch a {
		case "shell", "full":
			a = ""
		case "", "sftp", "sftp-readonly":
		default:
			return nil, errors.New("--access is shell, sftp or sftp-readonly")
		}
		body := map[string]any{"access": a, "expires": fs.get("expires")}
		if f := fs.vals["from"]; len(f) > 0 {
			body["allow_from"] = f
		}
		return body, nil
	}
	if len(pos) == 0 || pos[0] == "list" {
		var ks []map[string]any
		if err := c.do("GET", keys, nil, &ks); err != nil {
			return err
		}
		c.show(ks, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "FINGERPRINT\tCOMMENT\tACCESS\tFROM\tEXPIRES")
			for _, k := range ks {
				acc := fmt.Sprint(nv(k["access"]))
				if acc == "" {
					acc = "account default"
				}
				from := ""
				if f, ok := k["allow_from"].([]any); ok {
					from = joinAny(f)
				}
				fmt.Fprintf(w, "%v\t%v\t%s\t%s\t%v\n", k["fingerprint"], nv(k["comment"]), acc, from, nv(k["expires"]))
			}
		})
		return nil
	}
	switch pos[0] {
	case "add":
		key := fs.get("key")
		if f := fs.get("key-file"); f != "" {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			key = strings.TrimSpace(string(b))
		}
		if key == "" {
			return errors.New("give the public key with --key \"ssh-ed25519 AAAA… you@laptop\" or --key-file ~/.ssh/id_ed25519.pub")
		}
		body, err := access()
		if err != nil {
			return err
		}
		body["public_key"] = key
		return c.do("POST", keys, body, nil)
	case "set":
		if len(pos) != 2 {
			return errUsage
		}
		body, err := access()
		if err != nil {
			return err
		}
		return c.do("PUT", keys+"?fingerprint="+url.QueryEscape(pos[1]), body, nil)
	case "rm":
		if len(pos) != 2 {
			return errUsage
		}
		return c.do("DELETE", keys+"?fingerprint="+url.QueryEscape(pos[1]), nil, nil)
	}
	return errUsage
}

// ---------------------------------------------------------------- cPanel

func cpanelCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 || pos[0] == "list" {
		var fsList []map[string]any
		if err := c.do("GET", "/imports", nil, &fsList); err != nil {
			return err
		}
		c.show(fsList, func(w *tabwriter.Writer) {
			for _, f := range fsList {
				fmt.Fprintf(w, "%v\t%.0f MB\t%v\n", f["file"], toF(f["size_mb"]), nv(f["uploaded"]))
			}
		})
		return nil
	}
	if len(pos) != 2 {
		return errUsage
	}
	switch pos[0] {
	case "upload":
		f, err := os.Open(pos[1])
		if err != nil {
			return err
		}
		defer f.Close()
		var plan map[string]any
		fmt.Fprintln(os.Stderr, "Uploading", filepath.Base(pos[1]), "…")
		if err := c.do("PUT", "/imports/cpanel/"+esc(filepath.Base(pos[1]))+"/upload", f, &plan); err != nil {
			return err
		}
		c.show(plan, nil)
		return nil
	case "show":
		var plan map[string]any
		if err := c.do("GET", "/imports/cpanel/"+esc(pos[1]), nil, &plan); err != nil {
			return err
		}
		c.show(plan, nil)
		return nil
	case "restore":
		body := map[string]any{"account": fs.get("as"), "point_to_this_server": fs.bools["point-dns"]}
		if s := fs.get("skip"); s != "" {
			body["skip"] = strings.Split(s, ",")
		}
		return c.runJob(fs, "POST", "/imports/cpanel/"+esc(pos[1])+"/restore", body)
	}
	return errUsage
}

// metricsCmd prints the server's current CPU, memory, swap, network and disk.
func metricsCmd(c *client) error {
	var m struct {
		RawNow      map[string]float64 `json:"now"`
		MemTotalMB  float64            `json:"mem_total_mb"`
		SwapTotalMB float64            `json:"swap_total_mb"`
		Interfaces  []map[string]any   `json:"interfaces"`
	}
	if err := c.do("GET", "/server/metrics", nil, &m); err != nil {
		return err
	}
	n := m.RawNow
	bits := func(bps float64) string {
		b := bps * 8
		switch {
		case b >= 1e9:
			return fmt.Sprintf("%.1f Gbit/s", b/1e9)
		case b >= 1e6:
			return fmt.Sprintf("%.1f Mbit/s", b/1e6)
		}
		return fmt.Sprintf("%.0f kbit/s", b/1e3)
	}
	c.show(m, func(w *tabwriter.Writer) {
		fmt.Fprintf(w, "CPU\t%.0f%%\tload %.2f\n", n["cpu"], n["load1"])
		fmt.Fprintf(w, "Memory\t%.0f MB\tof %.0f MB\n", n["mem"], m.MemTotalMB)
		if m.SwapTotalMB > 0 {
			fmt.Fprintf(w, "Swap\t%.0f MB\tof %.0f MB\n", n["swap"], m.SwapTotalMB)
		} else {
			fmt.Fprintf(w, "Swap\tnone\t\n")
		}
		fmt.Fprintf(w, "Network\t%s in\t%s out\n", bits(n["net_in"]), bits(n["net_out"]))
		fmt.Fprintf(w, "Disk\t%s read\t%s written\n", human(n["disk_rd"])+"/s", human(n["disk_wr"])+"/s")
		for _, i := range m.Interfaces {
			fmt.Fprintf(w, "  %v\t%s in\t%s out\n", i["name"], bits(toF(i["in_bps"])), bits(toF(i["out_bps"])))
		}
	})
	return nil
}

// protectCmd manages a website's password protection.
//
//	ike protect DOMAIN                                   paths and users
//	ike protect DOMAIN user NAME [--password P]          add a user or change the password
//	ike protect DOMAIN rm-user NAME
//	ike protect DOMAIN path /admin/ [--realm R] [--user U ...] [--allow IP ...]
//	ike protect DOMAIN unpath /admin/
func protectCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 {
		return errUsage
	}
	base := "/sites/" + esc(pos[0]) + "/protection"
	type path struct {
		ID       string   `json:"id,omitempty"`
		Path     string   `json:"path"`
		Realm    string   `json:"realm"`
		Users    []string `json:"users,omitempty"`
		AllowIPs []string `json:"allow_ips,omitempty"`
	}
	var cur struct {
		Paths []path           `json:"paths"`
		Users []map[string]any `json:"users"`
	}
	if err := c.do("GET", base, nil, &cur); err != nil {
		return err
	}
	if len(pos) == 1 {
		c.show(cur, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "PATH\tSIGN-IN MESSAGE\tUSERS\tNO PASSWORD FROM")
			for _, p := range cur.Paths {
				u := strings.Join(p.Users, ",")
				if u == "" {
					u = "every user"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Path, p.Realm, u, strings.Join(p.AllowIPs, ","))
			}
			names := []string{}
			for _, u := range cur.Users {
				names = append(names, fmt.Sprint(u["name"]))
			}
			fmt.Fprintf(w, "\nUsers: %s\n", strings.Join(names, ", "))
		})
		return nil
	}
	if len(pos) != 3 {
		return errUsage
	}
	switch pos[1] {
	case "user":
		var r map[string]any
		if err := c.do("PUT", base+"/users/"+esc(pos[2]), map[string]string{"password": fs.get("password")}, &r); err != nil {
			return err
		}
		if pw, ok := r["password"]; ok {
			fmt.Printf("Password for %s: %v (shown once)\n", pos[2], pw)
		}
		return nil
	case "rm-user":
		return c.do("DELETE", base+"/users/"+esc(pos[2]), nil, nil)
	case "path", "unpath":
		var next []path
		for _, p := range cur.Paths {
			if p.Path != pos[2] {
				next = append(next, p)
			}
		}
		if pos[1] == "path" {
			next = append(next, path{Path: pos[2], Realm: fs.get("realm"), Users: fs.vals["user"], AllowIPs: fs.vals["allow"]})
		}
		if next == nil {
			next = []path{}
		}
		return c.do("PUT", base+"/paths", next, nil)
	}
	return errUsage
}

// sftpCmd manages website SFTP users.
//
//	ike sftp --account A
//	ike sftp add NAME --account A --domain D [--folder F] [--read-only] [--key-file F] [--password P] [--expires DATE]
//	ike sftp passwd USER --account A          a new password, shown once
//	ike sftp rm USER --account A
func sftpCmd(c *client, pos []string, fs flagSet) error {
	base, err := acctBase(fs)
	if err != nil {
		return err
	}
	base += "/sftp-users"
	if len(pos) == 0 {
		var us []map[string]any
		if err := c.do("GET", base, nil, &us); err != nil {
			return err
		}
		c.show(us, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "USER\tWEBSITE\tFOLDER\tACCESS\tEXPIRES")
			for _, u := range us {
				acc := "read-write"
				if u["read_only"] == true {
					acc = "download only"
				}
				folder := fmt.Sprint(u["folder"])
				if folder == "" {
					folder = "(whole website)"
				}
				fmt.Fprintf(w, "%v\t%v\t%s\t%s\t%v\n", u["name"], u["domain"], folder, acc, nv(u["expires"]))
			}
		})
		return nil
	}
	if len(pos) != 2 {
		return errUsage
	}
	printPw := func(user any, pw any) {
		if s, _ := pw.(string); s != "" {
			fmt.Printf("SFTP login for %v: password %s (shown once), port 22\n", user, s)
		}
	}
	switch pos[0] {
	case "add":
		domain, err := fs.need("domain")
		if err != nil {
			return err
		}
		body := map[string]any{"name": pos[1], "domain": domain, "read_only": fs.bools["read-only"], "expires": fs.get("expires")}
		if _, ok := fs.vals["folder"]; ok {
			body["folder"] = fs.get("folder")
		}
		if p := fs.get("password"); p != "" {
			body["password"] = p
		}
		if f := fs.get("key-file"); f != "" {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			body["public_key"] = strings.TrimSpace(string(b))
		}
		var r map[string]any
		if err := c.do("POST", base, body, &r); err != nil {
			return err
		}
		u, _ := r["user"].(map[string]any)
		fmt.Printf("Added %v for %v.\n", u["name"], u["domain"])
		printPw(u["name"], r["password"])
		return nil
	case "passwd":
		var r map[string]any
		if err := c.do("PATCH", base+"/"+esc(pos[1]), map[string]string{"password": "generate"}, &r); err != nil {
			return err
		}
		printPw(pos[1], r["password"])
		return nil
	case "rm":
		if err := confirm(fs, "Remove SFTP user "+pos[1]+"? (The website's files stay.)"); err != nil {
			return err
		}
		return c.do("DELETE", base+"/"+esc(pos[1]), nil, nil)
	}
	return errUsage
}

// teamCmd manages developers.
//
//	ike developers
//	ike developers invite USERNAME --email E --account A [--account B] --area sites=write [--area files=write ...]
//	ike developers set USERNAME [--account A ...] [--area AREA=read|write ...]
//	ike developers pause|resume|rm USERNAME
func teamCmd(c *client, pos []string, fs flagSet) error {
	areas := func() (map[string]string, error) {
		out := map[string]string{}
		for _, a := range fs.vals["area"] {
			k, v, ok := strings.Cut(a, "=")
			if !ok {
				return nil, fmt.Errorf("--area %q: use AREA=read or AREA=write, e.g. --area sites=write", a)
			}
			out[k] = v
		}
		return out, nil
	}
	if len(pos) == 0 {
		var ps []map[string]any
		if err := c.do("GET", "/people", nil, &ps); err != nil {
			return err
		}
		var devs []map[string]any
		for _, p := range ps {
			if p["role"] == "developer" {
				devs = append(devs, p)
			}
		}
		c.show(devs, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "DEVELOPER\tACCOUNTS\tCAN DO\tPAUSED")
			for _, p := range devs {
				ar, _ := p["areas"].(map[string]any)
				keys := make([]string, 0, len(ar))
				for k := range ar {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				var parts []string
				for _, k := range keys {
					parts = append(parts, k+"="+fmt.Sprint(ar[k]))
				}
				accts, _ := p["accounts"].([]any)
				fmt.Fprintf(w, "%v\t%s\t%s\t%v\n", p["username"], joinAny(accts), strings.Join(parts, ","), p["disabled"])
			}
		})
		return nil
	}
	if len(pos) != 2 {
		return errUsage
	}
	user := esc(pos[1])
	switch pos[0] {
	case "invite":
		email, err := fs.need("email")
		if err != nil {
			return err
		}
		ar, err := areas()
		if err != nil {
			return err
		}
		pw := generatePassword()
		if err := c.do("POST", "/people", map[string]any{"username": pos[1], "email": email, "role": "developer", "password": pw,
			"accounts": fs.vals["account"], "areas": ar, "name": fs.get("name")}, nil); err != nil {
			return err
		}
		fmt.Printf("Invited %s. Password: %s (shown once; send it privately). They'll set up two-factor sign-in.\n", pos[1], pw)
		return nil
	case "set":
		body := map[string]any{}
		if a := fs.vals["account"]; len(a) > 0 {
			body["accounts"] = a
		}
		if len(fs.vals["area"]) > 0 {
			ar, err := areas()
			if err != nil {
				return err
			}
			body["areas"] = ar
		}
		return c.do("PATCH", "/people/"+user, body, nil)
	case "pause", "resume":
		return c.do("PATCH", "/people/"+user, map[string]bool{"disabled": pos[0] == "pause"}, nil)
	case "rm":
		if err := confirm(fs, "Remove developer "+pos[1]+"? They're signed out and their tokens stop working."); err != nil {
			return err
		}
		return c.do("DELETE", "/people/"+user, nil, nil)
	}
	return errUsage
}

func generatePassword() string {
	const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

// ---------------------------------------------------------------- firewall

func splitPort(arg string) (port, proto string, err error) {
	port, proto, ok := strings.Cut(arg, "/")
	if !ok {
		proto = "tcp"
	}
	if port == "" || (proto != "tcp" && proto != "udp") {
		return "", "", fmt.Errorf("write the port as 8080/tcp, 53/udp or 6000:6100/udp")
	}
	return port, proto, nil
}

func firewallCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 || pos[0] == "status" || pos[0] == "list" {
		var st struct {
			Backend string           `json:"backend"`
			Ports   []map[string]any `json:"ports"`
			Blocks  []map[string]any `json:"blocks"`
		}
		if err := c.do("GET", "/firewall", nil, &st); err != nil {
			return err
		}
		c.show(st, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "Firewall\t%s\n\n", orDefault(st.Backend, "ufw"))
			fmt.Fprintln(w, "PORT\tFROM\tNOTE\t")
			for _, p := range st.Ports {
				from, _ := p["from"].(string)
				lock := ""
				if p["protected"] == true {
					lock = "always open"
				}
				fmt.Fprintf(w, "%v/%v\t%s\t%v\t%s\n", p["port"], p["proto"], orDefault(from, "anyone"), text(p["comment"]), lock)
			}
			if len(st.Blocks) > 0 {
				fmt.Fprintln(w, "\nBLOCKED\tNOTE\tSINCE\t")
				for _, b := range st.Blocks {
					fmt.Fprintf(w, "%v\t%v\t%v\t\n", b["address"], text(b["comment"]), b["added_at"])
				}
			}
		})
		return nil
	}
	switch pos[0] {
	case "open", "close":
		if len(pos) != 2 {
			return errUsage
		}
		port, proto, err := splitPort(pos[1])
		if err != nil {
			return err
		}
		from := fs.get("from")
		if pos[0] == "open" {
			if err := c.do("POST", "/firewall/ports", map[string]string{"port": port, "proto": proto, "from": from, "comment": fs.get("note")}, nil); err != nil {
				return err
			}
			c.done("Opened %s/%s for %s", port, proto, orDefault(from, "anyone"))
			return nil
		}
		q := "/firewall/ports?port=" + url.QueryEscape(port) + "&proto=" + proto
		if from != "" {
			q += "&from=" + url.QueryEscape(from)
		}
		if err := c.do("DELETE", q, nil, nil); err != nil {
			return err
		}
		c.done("Closed %s/%s", port, proto)
		return nil
	case "block":
		if len(pos) != 2 {
			return errUsage
		}
		if err := c.do("POST", "/firewall/blocks", map[string]string{"address": pos[1], "comment": fs.get("note")}, nil); err != nil {
			return err
		}
		c.done("Blocked %s", pos[1])
		return nil
	case "unblock":
		if len(pos) != 2 {
			return errUsage
		}
		if err := c.do("DELETE", "/firewall/blocks?address="+url.QueryEscape(pos[1]), nil, nil); err != nil {
			return err
		}
		c.done("Unblocked %s", pos[1])
		return nil
	case "bans":
		var bans []map[string]any
		if err := c.do("GET", "/firewall/bans", nil, &bans); err != nil {
			return err
		}
		c.show(bans, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "JAIL\tADDRESS")
			for _, b := range bans {
				fmt.Fprintf(w, "%v\t%v\n", b["jail"], b["ip"])
			}
		})
		return nil
	case "unban":
		if len(pos) != 3 {
			return errUsage
		}
		if err := c.do("DELETE", "/firewall/bans?jail="+url.QueryEscape(pos[1])+"&ip="+url.QueryEscape(pos[2]), nil, nil); err != nil {
			return err
		}
		c.done("Released %s from %s", pos[2], pos[1])
		return nil
	}
	return errUsage
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func text(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// done prints a one-line confirmation (nothing with --json).
func (c *client) done(format string, args ...any) {
	if !c.asJSON {
		fmt.Printf(format+"\n", args...)
	}
}

// ---------------------------------------------------------------- passkeys

func passkeysCmd(c *client, pos []string) error {
	if len(pos) != 0 {
		return fmt.Errorf("passkeys are added, renamed and removed in the panel in a browser (Profile, Passkeys)")
	}
	var keys []map[string]any
	if err := c.do("GET", "/me/passkeys", nil, &keys); err != nil {
		return err
	}
	c.show(keys, func(w *tabwriter.Writer) {
		fmt.Fprintln(w, "PASSKEY\tKIND\tLAST USED\tNOTE")
		for _, k := range keys {
			kind := "one device"
			if k["synced"] == true {
				kind = "synced"
			}
			note := ""
			if k["disabled"] == true {
				note = "turned off: may have been copied"
			}
			fmt.Fprintf(w, "%v\t%s\t%s\t%s\n", k["name"], kind, orDefault(text(k["last_used_at"]), "never"), note)
		}
	})
	return nil
}

// ---------------------------------------------------------------- updates

func (c *client) startJob(method, path string, body any, wait bool, what string) error {
	var j struct {
		ID string `json:"id"`
	}
	if err := c.do(method, path, body, &j); err != nil {
		return err
	}
	if wait {
		return c.waitJob(j.ID)
	}
	c.done("%s started (job %s). Follow it with: ike jobs %s --wait", what, j.ID, j.ID)
	return nil
}

func updateCmd(c *client, pos []string, fs flagSet) error {
	wait := fs.bools["wait"]
	if len(pos) == 0 || pos[0] == "status" {
		var st struct {
			Current string `json:"current"`
			Latest  *struct {
				Latest    string `json:"latest"`
				Available bool   `json:"update_available"`
				Notes     string `json:"notes"`
			} `json:"latest"`
			Settings struct {
				Mode string `json:"mode"`
				Skip string `json:"skip"`
			} `json:"settings"`
			Kept    []string         `json:"kept_versions"`
			History []map[string]any `json:"history"`
		}
		if err := c.do("GET", "/ike", nil, &st); err != nil {
			return err
		}
		c.show(st, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "Ike\t%s\n", st.Current)
			if st.Latest != nil && st.Latest.Available {
				fmt.Fprintf(w, "Newest\t%s (ike update install)\n", st.Latest.Latest)
			} else {
				fmt.Fprintln(w, "Newest\tyou have it")
			}
			fmt.Fprintf(w, "New versions\t%s\n", map[string]string{"notify": "ask first", "auto": "install by itself", "off": "not checked"}[st.Settings.Mode])
			if len(st.Kept) > 0 {
				fmt.Fprintf(w, "Kept for rollback\t%s\n", strings.Join(st.Kept, ", "))
			}
			if len(st.History) > 0 {
				fmt.Fprintln(w, "\nCHANGE\tBY\tRESULT\tWHEN")
				for _, h := range st.History {
					fmt.Fprintf(w, "%v → %v\t%v\t%v %v\t%v\n", h["from"], h["to"], orDefault(text(h["by"]), "-"), h["status"], text(h["reason"]), h["started_at"])
				}
			}
		})
		return nil
	}
	switch pos[0] {
	case "check":
		var rel map[string]any
		if err := c.do("POST", "/ike/check", nil, &rel); err != nil {
			return err
		}
		if rel["update_available"] == true {
			c.done("Ike %v is available (you have %v). Install it with: ike update install", rel["latest"], rel["current"])
		} else {
			c.done("Ike %v is the newest version", rel["current"])
		}
		return nil
	case "install":
		return c.startJob("POST", "/ike/update", nil, wait, "Update")
	case "rollback":
		body := map[string]string{}
		if len(pos) > 1 {
			body["version"] = pos[1]
		}
		return c.startJob("POST", "/ike/rollback", body, wait, "Rollback")
	case "mode":
		if len(pos) != 2 {
			return errUsage
		}
		var cur struct {
			Settings map[string]any `json:"settings"`
		}
		if err := c.do("GET", "/ike", nil, &cur); err != nil {
			return err
		}
		cur.Settings["mode"] = pos[1]
		if err := c.do("PUT", "/ike/settings", cur.Settings, nil); err != nil {
			return err
		}
		c.done("New Ike versions: %s", pos[1])
		return nil
	case "skip":
		if len(pos) != 2 {
			return errUsage
		}
		var cur struct {
			Settings map[string]any `json:"settings"`
		}
		if err := c.do("GET", "/ike", nil, &cur); err != nil {
			return err
		}
		cur.Settings["skip"] = pos[1]
		if err := c.do("PUT", "/ike/settings", cur.Settings, nil); err != nil {
			return err
		}
		c.done("Skipping Ike %s; newer versions are offered again", pos[1])
		return nil
	}
	return errUsage
}

func osCmd(c *client, pos []string, fs flagSet) error {
	wait := fs.bools["wait"]
	if len(pos) == 0 || pos[0] == "list" {
		var st struct {
			Packages []map[string]any `json:"packages"`
			Security int              `json:"security_count"`
			Reboot   bool             `json:"reboot_required"`
		}
		if err := c.do("GET", "/updates", nil, &st); err != nil {
			return err
		}
		c.show(st, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "%d waiting, %d security. Install with: ike os install [--security] --wait\n", len(st.Packages), st.Security)
			if st.Reboot {
				fmt.Fprintln(w, "A restart is needed to finish earlier updates.")
			}
			fmt.Fprintln(w, "\nPACKAGE\tFROM\tTO\t")
			for _, p := range st.Packages {
				sec := ""
				if p["security"] == true {
					sec = "security"
				}
				fmt.Fprintf(w, "%v\t%v\t%v\t%s\n", p["name"], p["from"], p["to"], sec)
			}
		})
		return nil
	}
	switch pos[0] {
	case "install":
		return c.startJob("POST", "/updates/apply", map[string]bool{"security_only": fs.bools["security"]}, wait, "Package update")
	case "history":
		var runs []map[string]any
		if err := c.do("GET", "/updates/history", nil, &runs); err != nil {
			return err
		}
		c.show(runs, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "RUN\tKIND\tBY\tCHANGED\tRESULT\tWHEN")
			for _, r := range runs {
				ch, _ := r["changes"].([]any)
				res := text(r["status"])
				if r["rolled_back_by"] != nil {
					res += ", rolled back"
				}
				fmt.Fprintf(w, "%v\t%v\t%v\t%d\t%s\t%v\n", r["id"], r["kind"], orDefault(text(r["by"]), "-"), len(ch), res, r["started_at"])
			}
		})
		return nil
	case "rollback":
		if len(pos) != 2 {
			return errUsage
		}
		return c.startJob("POST", "/updates/runs/"+url.PathEscape(pos[1])+"/rollback", nil, wait, "Rollback")
	case "release":
		if len(pos) == 2 && pos[1] == "upgrade" {
			if !fs.bools["i-have-a-snapshot"] {
				return fmt.Errorf("moving to a new Ubuntu release can't be undone from Ike; take a snapshot at your provider, then add --i-have-a-snapshot")
			}
			return c.startJob("POST", "/updates/release", map[string]bool{"have_snapshot": true}, wait, "Release upgrade")
		}
		var r map[string]any
		if err := c.do("GET", "/updates/release?refresh=1", nil, &r); err != nil {
			return err
		}
		c.show(r, func(w *tabwriter.Writer) {
			if text(r["available"]) == "" {
				fmt.Fprintf(w, "%s is the newest release offered.\n", strings.TrimSpace("Ubuntu "+text(r["current"])))
				return
			}
			fmt.Fprintf(w, "Ubuntu %v is available (this server runs %v).\n\n", r["available"], r["current"])
			checks, _ := r["checks"].([]any)
			for _, x := range checks {
				ch, _ := x.(map[string]any)
				mark := "ok "
				if ch["ok"] != true {
					mark = "no "
				}
				fmt.Fprintf(w, "%s\t%v\t%v\n", mark, ch["name"], text(ch["note"]))
			}
			fmt.Fprintln(w, "\nWhen every check is ok: ike os release upgrade --i-have-a-snapshot --wait")
		})
		return nil
	}
	return errUsage
}

func phpPkgCmd(c *client, kind string, pos []string, fs flagSet) error {
	if len(pos) < 2 || (pos[0] != "install" && pos[0] != "remove") {
		return errUsage
	}
	method := map[string]string{"install": "POST", "remove": "DELETE"}[pos[0]]
	if kind == "pear" {
		return c.startJob(method, "/php/pear?name="+url.QueryEscape(pos[1]), nil, fs.bools["wait"], "PEAR "+pos[1])
	}
	v := fs.get("php")
	if v == "" {
		return fmt.Errorf("say which PHP version with --php 8.3")
	}
	return c.startJob(method, "/php/"+url.PathEscape(v)+"/pecl/"+url.PathEscape(pos[1]), nil, fs.bools["wait"], "PECL "+pos[1])
}

func toolsCmd(c *client) error {
	var tools []map[string]any
	if err := c.do("GET", "/package-tools", nil, &tools); err != nil {
		return err
	}
	c.show(tools, func(w *tabwriter.Writer) {
		fmt.Fprintln(w, "TOOL\tLANGUAGE\tINSTALLED\tA DEPLOY USES IT FOR")
		for _, t := range tools {
			fmt.Fprintf(w, "%v\t%v\t%v\t%v\n", t["name"], t["language"], map[bool]string{true: "yes", false: "no"}[t["installed"] == true], orDefault(text(t["detects"]), text(t["note"])))
		}
	})
	return nil
}
