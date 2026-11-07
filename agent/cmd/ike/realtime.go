package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// yesNo reads --flag yes|no, keeping cur when it isn't given.
func yesNo(fs flagSet, name string, cur bool) (bool, error) {
	switch strings.ToLower(fs.get(name)) {
	case "":
		return cur, nil
	case "yes", "on", "true", "1":
		return true, nil
	case "no", "off", "false", "0":
		return false, nil
	}
	return cur, fmt.Errorf("--%s is yes or no", name)
}

func intFlag(fs flagSet, name string, cur int) (int, error) {
	v := fs.get(name)
	if v == "" {
		return cur, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return cur, fmt.Errorf("--%s must be a number", name)
	}
	return n, nil
}

func toBool(v any) bool { b, _ := v.(bool); return b }

// expiryCmd lists what runs out.
//
//	ike expiry [--kind domain|ssl|hosting] [--account A]
//	ike expiry check DOMAIN
//	ike expiry hosting ACCOUNT YYYY-MM-DD|none
func expiryCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) > 0 {
		switch pos[0] {
		case "check":
			if len(pos) < 2 {
				return errors.New("usage: ike expiry check DOMAIN")
			}
			var out map[string]any
			if err := c.do("POST", "/expiry/domains/"+esc(pos[1])+"/refresh", nil, &out); err != nil {
				return err
			}
			c.show(out, func(w *tabwriter.Writer) {
				fmt.Fprintf(w, "%v\texpires %v\t%v\n", out["domain"], nv(out["expires"]), nv(out["registrar"]))
				if e, _ := out["error"].(string); e != "" {
					fmt.Fprintf(w, "\t%s\n", e)
				}
			})
			return nil
		case "hosting":
			if len(pos) < 3 {
				return errors.New("usage: ike expiry hosting ACCOUNT YYYY-MM-DD|none")
			}
			date := pos[2]
			if date == "none" {
				date = ""
			}
			return c.do("PUT", "/expiry/hosting/"+esc(pos[1]), map[string]string{"expires": date}, nil)
		default:
			return fmt.Errorf("unknown expiry command %q", pos[0])
		}
	}
	q := []string{}
	if k := fs.get("kind"); k != "" {
		q = append(q, "kind="+esc(k))
	}
	if a := fs.get("account"); a != "" {
		q = append(q, "account="+esc(a))
	}
	path := "/expiry"
	if len(q) > 0 {
		path += "?" + strings.Join(q, "&")
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := c.do("GET", path, nil, &out); err != nil {
		return err
	}
	c.show(out, func(w *tabwriter.Writer) {
		fmt.Fprintln(w, "KIND\tNAME\tACCOUNT\tEXPIRES\tLEFT\tNOTE")
		for _, it := range out.Items {
			left := "unknown"
			switch it["state"] {
			case "expired":
				left = "EXPIRED"
			case "unknown":
			default:
				left = fmt.Sprintf("%.0f days", toF(it["days_left"]))
			}
			exp := ""
			if s, _ := it["expires"].(string); s != "" {
				if t, err := time.Parse(time.RFC3339, s); err == nil {
					exp = t.Format("2006-01-02")
				}
			}
			fmt.Fprintf(w, "%v\t%v\t%v\t%s\t%s\t%v\n", it["kind"], it["name"], it["account"], exp, left, nv(it["detail"]))
		}
	})
	return nil
}

// realtimeCmd manages TURN and LiveKit.
//
//	ike realtime
//	ike realtime turn on --domain D [--tls yes|no] [--accounts yes|no]    ike realtime turn off
//	ike realtime turn login [--user U] [--hours N]
//	ike realtime livekit on --domain D --account A [--turn yes|no] [--accounts yes|no]
//	ike realtime livekit off
//	ike realtime keys add NAME [--account A]      ike realtime keys rm KEY
//	ike realtime token ROOM IDENTITY [--key K] [--minutes N]
func realtimeCmd(c *client, pos []string, fs flagSet) error {
	var st struct {
		Turn    map[string]any `json:"turn"`
		LiveKit map[string]any `json:"livekit"`
	}
	if err := c.do("GET", "/realtime", nil, &st); err != nil {
		return err
	}
	if len(pos) == 0 {
		c.show(st, func(w *tabwriter.Writer) {
			for _, x := range []struct {
				name string
				m    map[string]any
			}{{"TURN", st.Turn}, {"LiveKit", st.LiveKit}} {
				state := "not installed"
				if toBool(x.m["installed"]) {
					state = "off"
					if toBool(x.m["enabled"]) {
						state = nv(x.m["running"])
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%v\n", x.name, state, nv(x.m["domain"]))
			}
			if uris, _ := st.Turn["uris"].([]any); len(uris) > 0 {
				fmt.Fprintf(w, "TURN addresses\t%s\t\n", joinAny(uris))
			}
			if u, _ := st.LiveKit["url"].(string); u != "" && toBool(st.LiveKit["enabled"]) {
				fmt.Fprintf(w, "LiveKit URL\t%s\t\n", u)
			}
			if keys, _ := st.LiveKit["keys"].([]any); len(keys) > 0 {
				fmt.Fprintln(w, "\nKEY\tNAME\tACCOUNT")
				for _, k := range keys {
					km, _ := k.(map[string]any)
					fmt.Fprintf(w, "%v\t%v\t%v\n", km["key"], km["name"], nv(km["account"]))
				}
			}
		})
		return nil
	}
	switch pos[0] {
	case "turn":
		sub := ""
		if len(pos) > 1 {
			sub = pos[1]
		}
		t := st.Turn
		switch sub {
		case "on", "off", "set":
			body := map[string]any{"enabled": sub != "off", "domain": nv(t["domain"]), "tls": toBool(t["tls"]),
				"user_quota": int(toF(t["user_quota"])), "total_quota": int(toF(t["total_quota"])), "max_kbps": int(toF(t["max_kbps"])),
				"allow_accounts": toBool(t["allow_accounts"])}
			if sub == "set" {
				body["enabled"] = toBool(t["enabled"])
			}
			if d := fs.get("domain"); d != "" {
				body["domain"] = d
			}
			var err error
			for flag, key := range map[string]string{"tls": "tls", "accounts": "allow_accounts"} {
				if body[key], err = yesNo(fs, flag, body[key].(bool)); err != nil {
					return err
				}
			}
			for flag, key := range map[string]string{"user-quota": "user_quota", "total-quota": "total_quota", "max-kbps": "max_kbps"} {
				if body[key], err = intFlag(fs, flag, body[key].(int)); err != nil {
					return err
				}
			}
			if body["domain"] == "" && body["enabled"] == true {
				return errors.New("--domain is required (a name pointing at this server, e.g. turn.example.com)")
			}
			return c.realtimeChange("PUT", "/realtime/turn", body)
		case "login":
			hours, err := intFlag(fs, "hours", 12)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("POST", "/realtime/turn/credentials", map[string]any{"user": fs.get("user"), "ttl_hours": hours}, &out); err != nil {
				return err
			}
			c.show(out, func(w *tabwriter.Writer) {
				fmt.Fprintf(w, "Username\t%v\nCredential\t%v\nExpires\t%v\n", out["username"], out["credential"], out["expires"])
				if u, _ := out["uris"].([]any); len(u) > 0 {
					fmt.Fprintf(w, "Addresses\t%s\n", joinAny(u))
				}
			})
			return nil
		case "rotate-secret":
			if err := confirm(fs, "Logins signed with the current TURN secret stop working."); err != nil {
				return err
			}
			return c.realtimeChange("POST", "/realtime/turn/secret", nil)
		}
		return errors.New("usage: ike realtime turn on|off|set|login|rotate-secret")
	case "livekit":
		sub := ""
		if len(pos) > 1 {
			sub = pos[1]
		}
		if sub != "on" && sub != "off" && sub != "set" {
			return errors.New("usage: ike realtime livekit on|off|set --domain D --account A [--turn yes|no] [--accounts yes|no]")
		}
		l := st.LiveKit
		body := map[string]any{"enabled": sub != "off", "domain": nv(l["domain"]), "account": nv(l["account"]), "use_turn": toBool(l["use_turn"]),
			"empty_timeout": int(toF(l["empty_timeout"])), "max_participants": int(toF(l["max_participants"])),
			"webhook_urls": l["webhook_urls"], "allow_accounts": toBool(l["allow_accounts"])}
		if sub == "set" {
			body["enabled"] = toBool(l["enabled"])
		}
		for _, f := range []string{"domain", "account"} {
			if v := fs.get(f); v != "" {
				body[f] = v
			}
		}
		if hooks := fs.vals["webhook"]; len(hooks) > 0 {
			body["webhook_urls"] = hooks
		}
		var err error
		for flag, key := range map[string]string{"turn": "use_turn", "accounts": "allow_accounts"} {
			if body[key], err = yesNo(fs, flag, body[key].(bool)); err != nil {
				return err
			}
		}
		if body["max_participants"], err = intFlag(fs, "max-participants", body["max_participants"].(int)); err != nil {
			return err
		}
		var out struct {
			NewKey map[string]any `json:"new_key"`
		}
		if err := c.do("PUT", "/realtime/livekit", body, &out); err != nil {
			return err
		}
		if out.NewKey != nil {
			fmt.Printf("API key     %v\nAPI secret  %v\n(the secret isn't shown again)\n", out.NewKey["key"], out.NewKey["secret"])
		}
		return nil
	case "keys":
		if len(pos) >= 3 && pos[1] == "add" {
			var k map[string]any
			if err := c.do("POST", "/realtime/livekit/keys", map[string]string{"name": pos[2], "account": fs.get("account")}, &k); err != nil {
				return err
			}
			c.show(k, func(w *tabwriter.Writer) {
				fmt.Fprintf(w, "API key\t%v\nAPI secret\t%v\n\t(the secret isn't shown again)\n", k["key"], k["secret"])
			})
			return nil
		}
		if len(pos) >= 3 && pos[1] == "rm" {
			if err := confirm(fs, "Apps using "+pos[2]+" stop working and LiveKit restarts."); err != nil {
				return err
			}
			return c.do("DELETE", "/realtime/livekit/keys/"+esc(pos[2]), nil, nil)
		}
		return errors.New("usage: ike realtime keys add NAME [--account A] | rm KEY")
	case "token":
		if len(pos) < 3 {
			return errors.New("usage: ike realtime token ROOM IDENTITY [--key K] [--minutes N]")
		}
		mins, err := intFlag(fs, "minutes", 60)
		if err != nil {
			return err
		}
		var out map[string]any
		if err := c.do("POST", "/realtime/livekit/token", map[string]any{"room": pos[1], "identity": pos[2], "key": fs.get("key"), "ttl_minutes": mins}, &out); err != nil {
			return err
		}
		c.show(out, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "URL\t%v\nToken\t%v\nExpires\t%v\n", out["url"], out["token"], out["expires"])
		})
		return nil
	}
	return fmt.Errorf("unknown realtime command %q", pos[0])
}

// realtimeChange sends a change and prints any notes the agent returns.
func (c *client) realtimeChange(method, path string, body any) error {
	var out struct {
		Notes []string `json:"notes"`
	}
	if err := c.do(method, path, body, &out); err != nil {
		return err
	}
	for _, n := range out.Notes {
		fmt.Println("note:", n)
	}
	return nil
}

// cacheCmd shows or changes an account's private cache.
//
//	ike cache ACCOUNT
//	ike cache ACCOUNT set [--engine redis|valkey|dragonfly] [--memory MB] [--policy P] [--persist yes|no]
func cacheCmd(c *client, pos []string, fs flagSet) error {
	if len(pos) == 0 {
		return errors.New("usage: ike cache ACCOUNT [set --engine redis|valkey|dragonfly --memory MB]")
	}
	base := "/accounts/" + esc(pos[0])
	var list struct {
		Engines struct {
			Caches []string `json:"caches"`
		} `json:"engines"`
		Redis map[string]any `json:"redis"`
	}
	if err := c.do("GET", base+"/dbs", nil, &list); err != nil {
		return err
	}
	if len(pos) > 1 && pos[1] == "set" {
		cur := list.Redis
		body := map[string]any{"memory_mb": 128, "policy": "allkeys-lru", "persist": false}
		if cur != nil {
			body["memory_mb"], body["policy"], body["persist"] = int(toF(cur["memory_mb"])), cur["policy"], toBool(cur["persist"])
		}
		if e := fs.get("engine"); e != "" {
			body["engine"] = e
		}
		if p := fs.get("policy"); p != "" {
			body["policy"] = p
		}
		var err error
		if body["memory_mb"], err = intFlag(fs, "memory", body["memory_mb"].(int)); err != nil {
			return err
		}
		if body["persist"], err = yesNo(fs, "persist", body["persist"].(bool)); err != nil {
			return err
		}
		if cur != nil && body["engine"] != nil && body["engine"] != nv(cur["engine"]) {
			if err := confirm(fs, "Switching engine starts an empty cache; the current keys are not copied."); err != nil {
				return err
			}
		}
		var out struct {
			Credentials map[string]any `json:"credentials"`
		}
		if err := c.do("PUT", base+"/redis", body, &out); err != nil {
			return err
		}
		if out.Credentials != nil {
			fmt.Printf("Password  %v\nURL       %v\n(the password isn't shown again)\n", out.Credentials["password"], out.Credentials["url"])
		}
		return nil
	}
	var info map[string]any
	if list.Redis != nil {
		_ = c.do("GET", base+"/redis", nil, &info)
	}
	c.show(map[string]any{"engines": list.Engines.Caches, "instance": list.Redis, "status": info}, func(w *tabwriter.Writer) {
		fmt.Fprintf(w, "Engines on this server\t%s\n", strings.Join(list.Engines.Caches, ", "))
		if list.Redis == nil {
			fmt.Fprintln(w, "This account has no cache yet\t(ike cache ACCOUNT set --engine ...)")
			return
		}
		fmt.Fprintf(w, "Engine\t%v\nSocket\t%v\nMemory\t%v MB (%v)\n", nv(info["engine"]), list.Redis["socket"], list.Redis["memory_mb"], list.Redis["policy"])
		if info != nil {
			fmt.Fprintf(w, "Running\t%v\nKeys\t%v\nVersion\t%v\n", info["running"], info["keys"], nv(info["version"]))
		}
	})
	return nil
}
