package panel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/client"
	"github.com/Mastermegamind/ike/agent/internal/cluster"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

// Servers: other Ike servers joined to this panel over mutual TLS.

type remotes struct {
	once    sync.Once
	err     error
	ca      *cluster.CA
	cert    tls.Certificate
	mu      sync.Mutex
	clients map[string]*client.Client
}

const keyAgent ctxKey = 100

func (s *Server) clusterInit() error {
	s.remote.once.Do(func() {
		dir := s.ClusterDir
		if dir == "" {
			s.remote.err = errors.New("multi-server management isn't set up on this panel")
			return
		}
		ca, err := cluster.LoadOrCreateCA(dir)
		if err != nil {
			s.remote.err = err
			return
		}
		certPath, keyPath := filepath.Join(dir, "panel.crt"), filepath.Join(dir, "panel.key")
		if _, err := os.Stat(certPath); err != nil {
			cp, kp, err := ca.IssueLocal(cluster.PanelCN, nil, true, false)
			if err != nil {
				s.remote.err = err
				return
			}
			_ = os.WriteFile(keyPath, kp, 0o600)
			_ = os.WriteFile(certPath, cp, 0o644)
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			s.remote.err = err
			return
		}
		s.remote.ca, s.remote.cert, s.remote.clients = ca, cert, map[string]*client.Client{}
	})
	return s.remote.err
}

// remoteClient talks to one joined server's agent.
func (s *Server) remoteClient(id string) (*client.Client, error) {
	if err := s.clusterInit(); err != nil {
		return nil, err
	}
	var srv *store.RemoteServer
	s.Store.View(func(d *store.Data) {
		for _, x := range d.Servers {
			if x.ID == id {
				c := *x
				srv = &c
			}
		}
	})
	if srv == nil {
		return nil, fmt.Errorf("server %s %w", id, kit.ErrNotFound)
	}
	s.remote.mu.Lock()
	defer s.remote.mu.Unlock()
	if c, ok := s.remote.clients[id+"|"+srv.Address]; ok {
		return c, nil
	}
	tc := cluster.ClientTLS(s.remote.ca, s.remote.cert, id)
	dial := func(ctx context.Context) (net.Conn, error) {
		d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: tc}
		return d.DialContext(ctx, "tcp", srv.Address)
	}
	tr := &http.Transport{TLSClientConfig: tc, DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		MaxIdleConnsPerHost: 4, IdleConnTimeout: time.Minute}
	c := &client.Client{BaseURL: "https://" + srv.Address, HTTP: &http.Client{Transport: tr, Timeout: 6 * time.Minute},
		Stream: &http.Client{Transport: tr}, Dial: dial}
	s.remote.clients[id+"|"+srv.Address] = c
	return c, nil
}

// serverParam reads which server a request is for: the X-Ike-Server header,
// or ?_server= for downloads and terminals (which can't set headers).
func serverParam(r *http.Request) string {
	if v := r.Header.Get("X-Ike-Server"); v != "" {
		return v
	}
	return r.URL.Query().Get("_server")
}

// resolveAgent picks the agent for a request. Only server admins manage
// other servers.
func (s *Server) resolveAgent(r *http.Request, p *store.Person) (*client.Client, error) {
	id := serverParam(r)
	if id == "" || id == "local" {
		return s.Agent, nil
	}
	if p.Role != store.Admin {
		return nil, fmt.Errorf("%w: only server admins manage other servers", errForbidden)
	}
	return s.remoteClient(id)
}

func (s *Server) agentFor(r *http.Request) *client.Client {
	if c, ok := r.Context().Value(keyAgent).(*client.Client); ok && c != nil {
		return c
	}
	return s.Agent
}

// stripServerParam removes ?_server= before forwarding.
func stripServerParam(raw string) string {
	if !strings.Contains(raw, "_server=") {
		return raw
	}
	var keep []string
	for _, kv := range strings.Split(raw, "&") {
		if !strings.HasPrefix(kv, "_server=") {
			keep = append(keep, kv)
		}
	}
	return strings.Join(keep, "&")
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

var serverNameRe = regexp.MustCompile(`^[\p{L}\p{N} ._()-]{1,60}$`)
var addrRe = regexp.MustCompile(`^[A-Za-z0-9.:\[\]-]{1,255}$`)

// ---- admin endpoints ----

type serverView struct {
	store.RemoteServer
	Local bool `json:"local"`
}

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	var list []store.RemoteServer
	s.Store.View(func(d *store.Data) {
		for _, x := range d.Servers {
			list = append(list, *x)
		}
	})
	host, _ := os.Hostname()
	out := []serverView{{RemoteServer: store.RemoteServer{ID: "local", Name: "This server", Hostname: host, Status: "online"}, Local: true}}
	var wg sync.WaitGroup
	results := make([]serverView, len(list))
	for i := range list {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := serverView{RemoteServer: list[i]}
			v.Status = "offline"
			if c, err := s.remoteClient(v.ID); err == nil {
				ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
				var h struct {
					Version string `json:"version"`
				}
				if c.Get(ctx, "/v1/health", &h) == nil {
					v.Status, v.Version, v.LastSeen = "online", h.Version, time.Now().UTC()
				}
				cancel()
			}
			results[i] = v
		}(i)
	}
	wg.Wait()
	_ = s.Store.Update(func(d *store.Data) error {
		for _, v := range results {
			for _, x := range d.Servers {
				if x.ID == v.ID && v.Status == "online" {
					x.LastSeen, x.Version, x.Status = v.LastSeen, v.Version, "online"
				}
			}
		}
		return nil
	})
	out = append(out, results...)
	writeJSON(w, 200, out)
}

func (s *Server) createJoinToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Group   string `json:"group"`
		Address string `json:"address"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !serverNameRe.MatchString(req.Name) || (req.Group != "" && !serverNameRe.MatchString(req.Group)) {
		fail(w, 400, "Give the server a short name (and group).")
		return
	}
	if req.Address != "" && !addrRe.MatchString(req.Address) {
		fail(w, 400, "Address looks like 203.0.113.5 or server.example.com:2089.")
		return
	}
	if err := s.clusterInit(); err != nil {
		failErr(w, err)
		return
	}
	token := kit.RandomHex(24)
	exp := time.Now().Add(2 * time.Hour).UTC()
	_ = s.Store.Update(func(d *store.Data) error {
		keep := d.JoinTokens[:0]
		for _, t := range d.JoinTokens {
			if t.Expires.After(time.Now()) {
				keep = append(keep, t)
			}
		}
		d.JoinTokens = append(keep, store.JoinToken{Hash: hashToken(token), Name: req.Name, Group: req.Group, Address: req.Address, Expires: exp})
		return nil
	})
	panelURL := s.adminURL(r)
	fp := s.CertFingerprint
	writeJSON(w, 201, map[string]any{
		"token": token, "fingerprint": fp, "expires": exp, "panel_url": panelURL,
		"command": fmt.Sprintf("sudo ike-agent join %s %s %s", panelURL, token, fp),
		"install": fmt.Sprintf("curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash -s -- --join %s --join-token %s --join-fingerprint %s", panelURL, token, fp),
	})
}

// adminURL is where joining servers reach this panel (the admin port).
func (s *Server) adminURL(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if s.AdminPort == "" {
		return "https://" + net.JoinHostPort(host, "2087")
	}
	return "https://" + net.JoinHostPort(host, s.AdminPort)
}

// join is called by a new server (public, token-protected).
func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.webhookLimiter.allow("join:" + ip) {
		fail(w, 429, "Too many attempts.")
		return
	}
	var req struct {
		Token     string   `json:"token"`
		CSR       string   `json:"csr"`
		Hostname  string   `json:"hostname"`
		Addresses []string `json:"addresses"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.clusterInit(); err != nil {
		failErr(w, err)
		return
	}
	var tok *store.JoinToken
	_ = s.Store.Update(func(d *store.Data) error {
		for i, t := range d.JoinTokens {
			if t.Hash == hashToken(req.Token) && t.Expires.After(time.Now()) {
				c := t
				tok = &c
				d.JoinTokens = append(d.JoinTokens[:i], d.JoinTokens[i+1:]...)
				break
			}
		}
		return nil
	})
	if tok == nil {
		fail(w, 403, "This join token is wrong or expired. Make a new one in the panel.")
		return
	}
	id := kit.RandomHex(5)
	hosts := []string{ip}
	for _, a := range req.Addresses {
		if net.ParseIP(a) != nil {
			hosts = append(hosts, a)
		}
	}
	if validHostname(req.Hostname) {
		hosts = append(hosts, req.Hostname)
	}
	cert, err := s.remote.ca.Sign([]byte(req.CSR), cluster.ServerCN+id, hosts, false, true, 3650)
	if err != nil {
		fail(w, 400, "The certificate request was invalid.")
		return
	}
	addr := tok.Address
	if addr == "" {
		addr = ip
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(strings.Trim(addr, "[]"), "2089")
	}
	_ = s.Store.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, &store.RemoteServer{ID: id, Name: tok.Name, Group: tok.Group, Address: addr, Hostname: req.Hostname, AddedAt: time.Now().UTC()})
		return nil
	})
	panelIP := ""
	if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		panelIP, _, _ = net.SplitHostPort(la.String())
	}
	writeJSON(w, 200, map[string]string{"server_id": id, "cert": string(cert), "ca": string(s.remote.ca.PEM), "panel_ip": panelIP})
}

func validHostname(h string) bool {
	return h != "" && len(h) < 254 && regexp.MustCompile(`^[a-zA-Z0-9.-]+$`).MatchString(h)
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Group   string `json:"group"`
		Address string `json:"address"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !serverNameRe.MatchString(req.Name) || (req.Group != "" && !serverNameRe.MatchString(req.Group)) || (req.Address != "" && !addrRe.MatchString(req.Address)) {
		fail(w, 400, "Name, group or address has unusual characters.")
		return
	}
	id := r.PathValue("id")
	var out *store.RemoteServer
	err := s.Store.Update(func(d *store.Data) error {
		for _, x := range d.Servers {
			if x.ID == id {
				x.Name, x.Group = req.Name, req.Group
				if req.Address != "" {
					x.Address = req.Address
				}
				c := *x
				out = &c
				return nil
			}
		}
		return fmt.Errorf("server %s %w", id, kit.ErrNotFound)
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.Store.Update(func(d *store.Data) error {
		for i, x := range d.Servers {
			if x.ID == id {
				d.Servers = append(d.Servers[:i], d.Servers[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("server %s %w", id, kit.ErrNotFound)
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"removed": id})
}

// fanout sends one agent request to several servers (a group or a list) and
// returns each answer, for actions across a fleet.
func (s *Server) fanout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Servers []string        `json:"servers"` // IDs; "local" for this server
		Group   string          `json:"group"`
		Method  string          `json:"method"`
		Path    string          `json:"path"` // /v1/...
		Body    json.RawMessage `json:"body"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !strings.HasPrefix(req.Path, "/v1/") || strings.Contains(req.Path, "..") {
		fail(w, 400, "Path must be an agent path like /v1/updates/apply.")
		return
	}
	switch req.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		fail(w, 400, "Method must be GET, POST, PUT, PATCH or DELETE.")
		return
	}
	ids := req.Servers
	if req.Group != "" {
		s.Store.View(func(d *store.Data) {
			for _, x := range d.Servers {
				if x.Group == req.Group {
					ids = append(ids, x.ID)
				}
			}
		})
	}
	if len(ids) == 0 || len(ids) > 200 {
		fail(w, 400, "Choose 1-200 servers.")
		return
	}
	type result struct {
		Server string          `json:"server"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body,omitempty"`
		Error  string          `json:"error,omitempty"`
	}
	results := make([]result, len(ids))
	var wg sync.WaitGroup
	p := personFrom(r)
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			res := result{Server: id}
			c := s.Agent
			if id != "local" {
				var err error
				if c, err = s.remoteClient(id); err != nil {
					res.Error = err.Error()
					results[i] = res
					return
				}
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			defer cancel()
			var body io.Reader
			if len(req.Body) > 0 {
				body = bytes.NewReader(req.Body)
			}
			hr, _ := http.NewRequestWithContext(ctx, req.Method, c.BaseURL+req.Path, body)
			hr.Header.Set("Content-Type", "application/json")
			s.actorHeaders(hr, p)
			resp, err := c.HTTP.Do(hr)
			if err != nil {
				res.Error = err.Error()
			} else {
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
				resp.Body.Close()
				res.Status = resp.StatusCode
				if json.Valid(b) {
					res.Body = b
				}
			}
			results[i] = res
		}(i, id)
	}
	wg.Wait()
	writeJSON(w, 200, results)
}

// ---- database clusters ----

func (s *Server) clientFor(id string) (*client.Client, string, error) {
	if id == "local" {
		return s.Agent, "", nil
	}
	c, err := s.remoteClient(id)
	if err != nil {
		return nil, "", err
	}
	var addr string
	s.Store.View(func(d *store.Data) {
		for _, x := range d.Servers {
			if x.ID == id {
				addr = x.Address
			}
		}
	})
	host, _, _ := net.SplitHostPort(addr)
	return c, host, nil
}

func (s *Server) listClusters(w http.ResponseWriter, r *http.Request) {
	var list []store.DBCluster
	s.Store.View(func(d *store.Data) {
		for _, c := range d.Clusters {
			list = append(list, *c)
		}
	})
	type member struct {
		Server string         `json:"server"`
		Role   string         `json:"role"`
		Status map[string]any `json:"status,omitempty"`
		Error  string         `json:"error,omitempty"`
	}
	type view struct {
		store.DBCluster
		Members []member `json:"members"`
	}
	out := []view{}
	for _, c := range list {
		v := view{DBCluster: c, Members: []member{{Server: c.Primary, Role: "primary"}}}
		for _, rep := range c.Replicas {
			m := member{Server: rep, Role: "replica"}
			if c.Engine == "mariadb" {
				if cl, _, err := s.clientFor(rep); err == nil {
					ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					if err := cl.Get(ctx, "/v1/databases/replication", &m.Status); err != nil {
						m.Error = err.Error()
					}
					cancel()
				} else {
					m.Error = err.Error()
				}
			}
			v.Members = append(v.Members, m)
		}
		out = append(out, v)
	}
	writeJSON(w, 200, out)
}

// createCluster sets up replication from a primary to each replica: the
// primary gets a replication user per replica, then each replica copies the
// data and starts following.
func (s *Server) createCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string   `json:"name"`
		Engine   string   `json:"engine"`
		Primary  string   `json:"primary"`
		Replicas []string `json:"replicas"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !serverNameRe.MatchString(req.Name) || len(req.Replicas) == 0 || len(req.Replicas) > 10 {
		fail(w, 400, "Give the cluster a name and 1-10 replicas.")
		return
	}
	if req.Engine != "mariadb" && req.Engine != "redis" {
		fail(w, 400, "Engine is mariadb or redis.")
		return
	}
	primary, primaryHost, err := s.clientFor(req.Primary)
	if err != nil {
		failErr(w, err)
		return
	}
	if primaryHost == "" {
		primaryHost = s.LocalIP
	}
	if primaryHost == "" {
		fail(w, 400, "This server's public address isn't known; use a joined server as the primary.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	p := personFrom(r)
	type step struct {
		Server string `json:"server"`
		Job    string `json:"job,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	var steps []step
	for i, rep := range req.Replicas {
		if rep == req.Primary {
			continue
		}
		rc, repHost, err := s.clientFor(rep)
		if err != nil {
			steps = append(steps, step{Server: rep, Error: err.Error()})
			continue
		}
		if repHost == "" {
			repHost = s.LocalIP
		}
		switch req.Engine {
		case "mariadb":
			var grant struct {
				User     string `json:"user"`
				Password string `json:"password"`
			}
			if err := s.agentCall(ctx, primary, p, "POST", "/v1/databases/replication/source",
				map[string]any{"replica_ip": repHost, "server_id": 1}, &grant); err != nil {
				steps = append(steps, step{Server: rep, Error: "primary: " + err.Error()})
				continue
			}
			var job struct {
				ID string `json:"id"`
			}
			err := s.agentCall(ctx, rc, p, "POST", "/v1/databases/replication/replica", map[string]any{
				"primary_host": primaryHost, "user": grant.User, "password": grant.Password, "server_id": 100 + i, "copy_data": true}, &job)
			st := step{Server: rep, Job: job.ID}
			if err != nil {
				st.Error = err.Error()
			}
			steps = append(steps, st)
		case "redis":
			var res struct {
				Password string `json:"password"`
				Port     int    `json:"port"`
			}
			if err := s.agentCall(ctx, primary, p, "POST", "/v1/databases/redis/primary", map[string]any{"replica_ip": repHost}, &res); err != nil {
				steps = append(steps, step{Server: rep, Error: "primary: " + err.Error()})
				continue
			}
			err := s.agentCall(ctx, rc, p, "POST", "/v1/databases/redis/replicaof", map[string]any{"host": primaryHost, "port": res.Port, "password": res.Password}, nil)
			st := step{Server: rep}
			if err != nil {
				st.Error = err.Error()
			}
			steps = append(steps, st)
		}
	}
	c := &store.DBCluster{ID: kit.RandomHex(5), Name: req.Name, Engine: req.Engine, Primary: req.Primary, Replicas: req.Replicas, CreatedAt: time.Now().UTC()}
	_ = s.Store.Update(func(d *store.Data) error { d.Clusters = append(d.Clusters, c); return nil })
	writeJSON(w, 201, map[string]any{"cluster": c, "steps": steps})
}

// promote turns a replica into the new primary (failover).
func (s *Server) promoteCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Server string `json:"server"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	var c *store.DBCluster
	s.Store.View(func(d *store.Data) {
		for _, x := range d.Clusters {
			if x.ID == id {
				cc := *x
				c = &cc
			}
		}
	})
	if c == nil {
		failErr(w, fmt.Errorf("cluster %s %w", id, kit.ErrNotFound))
		return
	}
	found := false
	for _, rep := range c.Replicas {
		found = found || rep == req.Server
	}
	if !found {
		fail(w, 400, "Choose one of the cluster's replicas.")
		return
	}
	cl, _, err := s.clientFor(req.Server)
	if err != nil {
		failErr(w, err)
		return
	}
	path, body := "/v1/databases/replication/stop", map[string]any{"promote": true}
	if c.Engine == "redis" {
		path, body = "/v1/databases/redis/replicaof", map[string]any{"host": ""}
	}
	if err := s.agentCall(r.Context(), cl, personFrom(r), "POST", path, body, nil); err != nil {
		failErr(w, err)
		return
	}
	_ = s.Store.Update(func(d *store.Data) error {
		for _, x := range d.Clusters {
			if x.ID == id {
				var reps []string
				for _, rep := range x.Replicas {
					if rep != req.Server {
						reps = append(reps, rep)
					}
				}
				x.Replicas = append(reps, x.Primary)
				x.Primary = req.Server
			}
		}
		return nil
	})
	writeJSON(w, 200, map[string]string{"primary": req.Server, "note": "Point your apps at the new primary. The old primary is listed as a replica; re-create replication on it once it is back."})
}

func (s *Server) deleteCluster(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_ = s.Store.Update(func(d *store.Data) error {
		for i, x := range d.Clusters {
			if x.ID == id {
				d.Clusters = append(d.Clusters[:i], d.Clusters[i+1:]...)
			}
		}
		return nil
	})
	writeJSON(w, 200, map[string]string{"removed": id})
}

// agentCall sends one JSON request to an agent as the signed-in person.
func (s *Server) agentCall(ctx context.Context, c *client.Client, p *store.Person, method, path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	s.actorHeaders(req, p)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rb, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	if out != nil {
		return json.Unmarshal(rb, out)
	}
	return nil
}
