package panel

// Public status pages: the admin and each reseller can publish one page
// listing the websites they choose, with their current state and 90 days of
// uptime from the panel's uptime monitor, in their own brand. No sign-in;
// the owner picks exactly which websites appear (and the names shown).

import (
	"context"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/panel/alerts"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

// statusOwner is whose page a person manages: "" for the admin, or the
// reseller's username.
func statusOwner(p *store.Person) (string, bool) {
	switch p.Role {
	case store.Admin:
		return "", true
	case store.Reseller:
		return p.Username, true
	}
	return "", false
}

// candidateSites are the websites a person may put on their page.
func (s *Server) candidateSites(ctx context.Context, acc access) ([]string, error) {
	var sites []struct {
		Domain  string `json:"domain"`
		Account string `json:"account"`
	}
	if err := s.Agent.Get(ctx, "/v1/sites", &sites); err != nil {
		return nil, err
	}
	out := []string{}
	for _, x := range sites {
		if acc.has(x.Account) {
			out = append(out, x.Domain)
		}
	}
	return out, nil
}

func (s *Server) statusURL(d *store.Data, owner, slug string) string {
	base := s.PublicURL
	if b := d.Branding[owner]; b != nil && len(b.Hostnames) > 0 {
		base = "https://" + b.Hostnames[0]
	}
	return strings.TrimRight(base, "/") + "/status/" + slug
}

// GET /api/v1/status-page
func (s *Server) getStatusPage(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	owner, ok := statusOwner(me)
	if !ok {
		failErr(w, errForbidden)
		return
	}
	cands, err := s.candidateSites(r.Context(), s.accessFor(me))
	if err != nil {
		failErr(w, err)
		return
	}
	var page store.StatusPage
	url := ""
	s.Store.View(func(d *store.Data) {
		if p := d.StatusPages[owner]; p != nil {
			page = *p
			url = s.statusURL(d, owner, p.Slug)
		}
	})
	if page.Entries == nil {
		page.Entries = []store.StatusEntry{}
	}
	writeJSON(w, 200, map[string]any{"page": page, "url": url, "websites": cands})
}

// PUT /api/v1/status-page {"enabled","slug","title","intro","entries":[{"domain","name"}]}
func (s *Server) putStatusPage(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	owner, ok := statusOwner(me)
	if !ok {
		failErr(w, errForbidden)
		return
	}
	var in store.StatusPage
	if !decodeBody(w, r, &in) {
		return
	}
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	if !slugRe.MatchString(in.Slug) {
		fail(w, 400, "The address is 3-40 lowercase letters, digits and dashes, like webafrica.")
		return
	}
	if len(in.Title) > 80 || len(in.Intro) > 300 {
		fail(w, 400, "The title is at most 80 characters and the note at most 300.")
		return
	}
	if len(in.Entries) > 100 {
		fail(w, 400, "At most 100 websites on a page.")
		return
	}
	cands, err := s.candidateSites(r.Context(), s.accessFor(me))
	if err != nil {
		failErr(w, err)
		return
	}
	allowed := map[string]bool{}
	for _, c := range cands {
		allowed[c] = true
	}
	seen := map[string]bool{}
	for i := range in.Entries {
		e := &in.Entries[i]
		e.Domain = strings.ToLower(strings.TrimSpace(e.Domain))
		e.Name = strings.TrimSpace(e.Name)
		if !allowed[e.Domain] {
			fail(w, 400, e.Domain+" isn't one of your websites.")
			return
		}
		if seen[e.Domain] {
			fail(w, 400, e.Domain+" is listed twice.")
			return
		}
		seen[e.Domain] = true
		if len(e.Name) > 60 {
			fail(w, 400, "Names are at most 60 characters.")
			return
		}
	}
	in.UpdatedAt = time.Now().UTC()
	url := ""
	err = s.Store.Update(func(d *store.Data) error {
		for o, p := range d.StatusPages {
			if o != owner && p.Slug == in.Slug {
				return errSlugTaken
			}
		}
		page := in
		d.StatusPages[owner] = &page
		url = s.statusURL(d, owner, in.Slug)
		return nil
	})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"page": in, "url": url})
}

type statusErr string

func (e statusErr) Error() string { return string(e) }

const errSlugTaken = statusErr("That address is taken; choose another.")

// publicStatus is what a status page shows.
type publicStatus struct {
	Title, Intro, Brand, Logo, Color string
	PoweredBy                        bool
	AllUp                            bool
	Down                             int
	Updated                          time.Time
	Sites                            []publicSite
}

type publicSite struct {
	Name      string         `json:"name"`
	Up        bool           `json:"up"`
	DownSince time.Time      `json:"down_since,omitzero"`
	Percent   float64        `json:"uptime_percent"`
	Days      []float64      `json:"days"`
	Bars      []statusBarDay `json:"-"`
}

type statusBarDay struct {
	Class, Label string
}

func (s *Server) loadStatus(slug string) (*publicStatus, bool) {
	var page *store.StatusPage
	var brand store.Branding
	s.Store.View(func(d *store.Data) {
		for owner, p := range d.StatusPages {
			if p.Enabled && p.Slug == slug {
				c := *p
				page = &c
				if b := d.Branding[owner]; b != nil { // a reseller without a brand stays neutral
					brand = *b
				}
			}
		}
	})
	if page == nil {
		return nil, false
	}
	out := &publicStatus{Title: page.Title, Intro: page.Intro, Brand: brand.ProductName, Logo: brand.Logo, Color: brand.PrimaryColor,
		PoweredBy: !brand.HidePoweredBy, AllUp: true, Updated: time.Now().UTC()}
	if out.Title == "" {
		out.Title = "Service status"
		if out.Brand != "" {
			out.Title = out.Brand + " status"
		}
	}
	if !colorRe.MatchString(out.Color) {
		out.Color = "#b42332"
	}
	if !logoRe.MatchString(out.Logo) {
		out.Logo = ""
	}
	domains := make([]string, len(page.Entries))
	for i, e := range page.Entries {
		domains[i] = e.Domain
	}
	var ups []alerts.Uptime
	if s.Monitor != nil {
		ups = s.Monitor.Uptimes(domains)
	}
	for i, e := range page.Entries {
		ps := publicSite{Name: e.Name, Up: true, Percent: -1}
		if ps.Name == "" {
			ps.Name = e.Domain
		}
		if i < len(ups) {
			u := ups[i]
			ps.Up, ps.DownSince, ps.Percent, ps.Days = u.Up, u.DownSince, u.Percent, u.Days
		}
		for j, v := range ps.Days {
			day := time.Now().UTC().AddDate(0, 0, j-len(ps.Days)+1).Format("2 Jan")
			b := statusBarDay{Class: "none", Label: day + ": no data"}
			switch {
			case v < 0:
			case v >= 99.9:
				b = statusBarDay{"ok", day + ": up"}
			case v >= 95:
				b = statusBarDay{"warn", day + ": " + trimPct(v) + " up"}
			default:
				b = statusBarDay{"bad", day + ": " + trimPct(v) + " up"}
			}
			ps.Bars = append(ps.Bars, b)
		}
		if !ps.Up {
			out.AllUp = false
			out.Down++
		}
		out.Sites = append(out.Sites, ps)
	}
	return out, true
}

// trimPct writes 99.5 as "99.5%" and 100 as "100%".
func trimPct(v float64) string {
	t := strconv.FormatFloat(v, 'f', 2, 64)
	t = strings.TrimRight(strings.TrimRight(t, "0"), ".")
	return t + "%"
}

// GET /status/{slug} and /status/{slug}.json (public)
func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	asJSON := strings.HasSuffix(slug, ".json")
	slug = strings.TrimSuffix(slug, ".json")
	st, ok := s.loadStatus(slug)
	w.Header().Set("Cache-Control", "public, max-age=30")
	w.Header().Set("X-Robots-Tag", "noindex")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if asJSON {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeJSON(w, 200, map[string]any{"title": st.Title, "all_up": st.AllUp, "down": st.Down, "updated": st.Updated, "websites": st.Sites})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'")
	_ = statusTmpl.Execute(w, st)
}

var statusTmpl = template.Must(template.New("status").Funcs(template.FuncMap{
	"pct":  func(v float64) string { return trimPct(v) },
	"date": func(t time.Time) string { return t.Format("2 Jan 2006, 15:04 MST") },
	"css":  func(c string) template.CSS { return template.CSS(c) },
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="60"><title>{{.Title}}</title>
<style>
:root{--accent:{{css .Color}};--bg:#f5f6fa;--card:#fff;--text:#1d2033;--muted:#6b6f85;--line:#e4e6ee;--ok:#1f9d55;--warn:#d98a00;--bad:#d03030;--none:#d7d9e3}
@media (prefers-color-scheme:dark){:root{--bg:#12131a;--card:#1b1d27;--text:#eceef6;--muted:#9a9db3;--line:#2a2d3a;--none:#343746}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:760px;margin:0 auto;padding:32px 16px 48px}
header{display:flex;align-items:center;gap:12px;margin-bottom:24px}header img{height:36px;width:auto}h1{font-size:22px;margin:0}
.intro{color:var(--muted);margin:-12px 0 20px}
.banner{border-radius:12px;padding:16px 18px;font-weight:600;color:#fff;background:var(--ok);margin-bottom:20px}.banner.bad{background:var(--bad)}
.card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:6px 18px}
.site{padding:14px 0;border-bottom:1px solid var(--line)}.site:last-child{border-bottom:0}
.row{display:flex;justify-content:space-between;align-items:center;gap:12px}.name{font-weight:600;overflow-wrap:anywhere}
.state{font-size:13px;font-weight:600;white-space:nowrap}.state.ok{color:var(--ok)}.state.bad{color:var(--bad)}
.bars{display:flex;gap:2px;margin-top:10px;height:28px}.bars span{flex:1;border-radius:2px;background:var(--none)}
.bars .ok{background:var(--ok)}.bars .warn{background:var(--warn)}.bars .bad{background:var(--bad)}
.legend{display:flex;justify-content:space-between;color:var(--muted);font-size:12px;margin-top:4px}
footer{color:var(--muted);font-size:13px;margin-top:20px;text-align:center}
@media (max-width:520px){.bars span:nth-child(-n+60){display:none}.legend .from{visibility:hidden}}
</style></head>
<body><main>
<header>{{if .Logo}}<img src="{{.Logo}}" alt="">{{end}}<h1>{{.Title}}</h1></header>
{{if .Intro}}<p class="intro">{{.Intro}}</p>{{end}}
<div class="banner{{if not .AllUp}} bad{{end}}">{{if .AllUp}}All websites are up{{else}}{{.Down}} of {{len .Sites}} websites {{if eq .Down 1}}is{{else}}are{{end}} down{{end}}</div>
<div class="card">
{{range .Sites}}<section class="site">
<div class="row"><span class="name">{{.Name}}</span>{{if .Up}}<span class="state ok">Up</span>{{else}}<span class="state bad">Down since {{date .DownSince}}</span>{{end}}</div>
<div class="bars" role="img" aria-label="Uptime over the last 90 days">{{range .Bars}}<span class="{{.Class}}" title="{{.Label}}"></span>{{end}}</div>
<div class="legend"><span class="from">90 days ago</span><span>{{if ge .Percent 0.0}}{{pct .Percent}} uptime{{else}}No data yet{{end}}</span><span>Today</span></div>
</section>{{else}}<p>No websites on this page yet.</p>{{end}}
</div>
<footer>Updated {{date .Updated}}. Checked every few minutes.{{if .PoweredBy}} Powered by Ike.{{end}}</footer>
</main></body></html>
`))
