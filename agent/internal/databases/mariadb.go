package databases

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/kit"
)

// sqlRows runs a query as the MariaDB root user and returns tab-separated rows.
func (m *Manager) sqlRows(ctx context.Context, sql string) ([][]string, error) {
	out, err := m.env.Run.RunInput(ctx, sql, "mariadb", "--batch", "--skip-column-names", "--raw")
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if l != "" {
			rows = append(rows, strings.Split(l, "\t"))
		}
	}
	return rows, nil
}

// Process is one connection in MariaDB's process list.
type Process struct {
	ID      int64  `json:"id"`
	User    string `json:"user"`
	Host    string `json:"host"`
	DB      string `json:"db"`
	Command string `json:"command"`
	Seconds int    `json:"seconds"`
	State   string `json:"state"`
	Query   string `json:"query"`
}

// Processes lists running queries; account limits it to that account's users.
func (m *Manager) Processes(ctx context.Context, account string) ([]Process, error) {
	if account != "" {
		if err := m.env.CheckAccount(account); err != nil {
			return nil, err
		}
	}
	rows, err := m.sqlRows(ctx, "SELECT ID, USER, HOST, IFNULL(DB,''), COMMAND, TIME, IFNULL(STATE,''), "+
		"REPLACE(REPLACE(LEFT(IFNULL(INFO,''), 1000), '\\t', ' '), '\\n', ' ') FROM information_schema.PROCESSLIST "+
		"WHERE ID <> CONNECTION_ID() ORDER BY TIME DESC;")
	if err != nil {
		return nil, err
	}
	out := []Process{}
	for _, r := range rows {
		if len(r) < 8 {
			continue
		}
		if account != "" && !strings.HasPrefix(r[1], account+"_") {
			continue
		}
		id, _ := strconv.ParseInt(r[0], 10, 64)
		secs, _ := strconv.Atoi(r[5])
		q := r[7]
		if q == "NULL" {
			q = ""
		}
		out = append(out, Process{ID: id, User: r[1], Host: r[2], DB: r[3], Command: r[4], Seconds: secs, State: r[6], Query: q})
	}
	return out, nil
}

// Kill stops a running query (or the whole connection).
func (m *Manager) Kill(ctx context.Context, account string, id int64, connection bool) error {
	procs, err := m.Processes(ctx, account)
	if err != nil {
		return err
	}
	found := false
	for _, p := range procs {
		if p.ID == id {
			found = true
			if p.User == "root" || p.User == "system user" || p.User == "event_scheduler" {
				return fmt.Errorf("system connections can't be stopped here")
			}
		}
	}
	if !found {
		return fmt.Errorf("connection %d %w", id, kit.ErrNotFound)
	}
	stmt := "KILL QUERY %d;"
	if connection {
		stmt = "KILL CONNECTION %d;"
	}
	_, err = m.env.Run.RunInput(ctx, fmt.Sprintf(stmt, id), "mariadb", "--batch")
	return err
}

// ---- slow query log ----

// SlowLogSettings turn MariaDB's slow query log on.
type SlowLogSettings struct {
	Enabled       bool    `json:"enabled"`
	LongQuerySecs float64 `json:"long_query_secs"`
	LogNoIndex    bool    `json:"log_queries_not_using_indexes"`
}

const slowLogFile = "/var/log/mysql/mariadb-slow.log"
const ikeCnf = "/etc/mysql/mariadb.conf.d/90-ike.cnf"

func (m *Manager) SlowLog() (SlowLogSettings, error) {
	s := SlowLogSettings{LongQuerySecs: 2}
	b, err := os.ReadFile(m.env.P(ikeCnf))
	if err != nil {
		return s, nil
	}
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "slow_query_log":
			s.Enabled = v == "1"
		case "long_query_time":
			s.LongQuerySecs, _ = strconv.ParseFloat(v, 64)
		case "log_queries_not_using_indexes":
			s.LogNoIndex = v == "1"
		}
	}
	return s, nil
}

// SetSlowLog applies the settings now and keeps them across restarts.
func (m *Manager) SetSlowLog(ctx context.Context, s SlowLogSettings) (SlowLogSettings, error) {
	if s.LongQuerySecs < 0.1 || s.LongQuerySecs > 600 {
		return s, fmt.Errorf("slow means 0.1 to 600 seconds")
	}
	on := map[bool]string{true: "1", false: "0"}
	cnf := fmt.Sprintf("# Written by Ike\n[mariadb]\nslow_query_log = %s\nslow_query_log_file = %s\nlong_query_time = %g\nlog_queries_not_using_indexes = %s\n",
		on[s.Enabled], slowLogFile, s.LongQuerySecs, on[s.LogNoIndex])
	if err := kit.WriteFile(m.env.P(ikeCnf), []byte(cnf), 0o644); err != nil {
		return s, err
	}
	// MariaDB writes the log itself; the folder must be its own.
	_, _ = m.env.Run.Run(ctx, "install", "-d", "-o", "mysql", "-g", "adm", "-m", "2750", "/var/log/mysql")
	sql := fmt.Sprintf("SET GLOBAL slow_query_log_file = '%s'; SET GLOBAL long_query_time = %g; SET GLOBAL log_queries_not_using_indexes = %s; SET GLOBAL slow_query_log = %s;",
		slowLogFile, s.LongQuerySecs, on[s.LogNoIndex], on[s.Enabled])
	_, err := m.env.Run.RunInput(ctx, sql, "mariadb", "--batch")
	return s, err
}

// SlowQuery is one entry of the slow query log.
type SlowQuery struct {
	Time         string  `json:"time"`
	User         string  `json:"user"`
	DB           string  `json:"db"`
	QuerySecs    float64 `json:"query_secs"`
	LockSecs     float64 `json:"lock_secs"`
	RowsSent     int     `json:"rows_sent"`
	RowsExamined int     `json:"rows_examined"`
	Query        string  `json:"query"`
}

var (
	slowUserRe = regexp.MustCompile(`^# User@Host: (\S+?)\[`)
	slowTimeRe = regexp.MustCompile(`^# Time: (.+)$`)
	slowStatRe = regexp.MustCompile(`Query_time: ([\d.]+)\s+Lock_time: ([\d.]+)\s+Rows_sent: (\d+)\s+Rows_examined: (\d+)`)
	slowDBRe   = regexp.MustCompile(`^(?:use|USE) ([^;]+);$`)
	slowDBHdr  = regexp.MustCompile(`Schema: (\S+)`)
)

// SlowQueries parses the newest entries; account limits to its own users.
// sort is "time" (newest first) or "duration" (slowest first).
func (m *Manager) SlowQueries(account, sortBy string, limit int) ([]SlowQuery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	f, err := os.Open(m.env.P(slowLogFile))
	if os.IsNotExist(err) {
		return []SlowQuery{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, _ := f.Stat()
	if st.Size() > 16<<20 {
		_, _ = f.Seek(st.Size()-16<<20, 0)
	}
	var out []SlowQuery
	var cur *SlowQuery
	var lastTime string
	flush := func() {
		if cur != nil && cur.Query != "" && (account == "" || strings.HasPrefix(cur.User, account+"_")) {
			cur.Query = strings.TrimSpace(cur.Query)
			if len(cur.Query) > 2000 {
				cur.Query = cur.Query[:2000] + "…"
			}
			out = append(out, *cur)
		}
		cur = nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case slowTimeRe.MatchString(l):
			flush()
			lastTime = slowTimeRe.FindStringSubmatch(l)[1]
		case slowUserRe.MatchString(l):
			flush()
			cur = &SlowQuery{Time: lastTime, User: slowUserRe.FindStringSubmatch(l)[1]}
		case cur == nil:
		case strings.HasPrefix(l, "# "):
			if mm := slowStatRe.FindStringSubmatch(l); mm != nil {
				cur.QuerySecs, _ = strconv.ParseFloat(mm[1], 64)
				cur.LockSecs, _ = strconv.ParseFloat(mm[2], 64)
				cur.RowsSent, _ = strconv.Atoi(mm[3])
				cur.RowsExamined, _ = strconv.Atoi(mm[4])
			}
			if mm := slowDBHdr.FindStringSubmatch(l); mm != nil {
				cur.DB = mm[1]
			}
		case strings.HasPrefix(l, "SET timestamp="):
		case slowDBRe.MatchString(l):
			cur.DB = slowDBRe.FindStringSubmatch(l)[1]
		default:
			cur.Query += l + "\n"
		}
	}
	flush()
	if sortBy == "duration" {
		sort.SliceStable(out, func(i, j int) bool { return out[i].QuerySecs > out[j].QuerySecs })
	} else {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []SlowQuery{}
	}
	return out, nil
}

// ---- replication (MariaDB GTID, Redis replicaof) ----

// ReplicationSource prepares this server as a MariaDB primary: binary log,
// a unique server ID and a replication user limited to the replica's address.
type ReplicationSource struct {
	ReplicaIP string `json:"replica_ip"`
	ServerID  int    `json:"server_id"`
}

// ReplicationGrant is what the replica needs to connect.
type ReplicationGrant struct {
	User     string `json:"user"`
	Password string `json:"password"`
	ServerID int    `json:"server_id"`
	Restart  bool   `json:"restart_needed"`
}

var ipRe = regexp.MustCompile(`^[0-9a-fA-F.:]{3,45}$`)

func (m *Manager) EnableSource(ctx context.Context, s ReplicationSource) (*ReplicationGrant, error) {
	if !ipRe.MatchString(s.ReplicaIP) {
		return nil, fmt.Errorf("replica address must be an IP address")
	}
	if s.ServerID < 1 || s.ServerID > 4294967295 {
		return nil, fmt.Errorf("server ID must be a positive number unique in the cluster")
	}
	cnf := fmt.Sprintf("# Written by Ike: replication primary\n[mariadb]\nserver_id = %d\nlog_bin = mariadb-bin\nbinlog_format = ROW\ngtid_strict_mode = 1\nlog_slave_updates = 1\nbind_address = 0.0.0.0\nexpire_logs_days = 7\n", s.ServerID)
	restart, err := m.writeCnf("/etc/mysql/mariadb.conf.d/91-ike-replication.cnf", cnf)
	if err != nil {
		return nil, err
	}
	user, pass := "ike_repl", kit.RandomHex(16)
	sql := fmt.Sprintf("CREATE USER IF NOT EXISTS '%[1]s'@'%[2]s' IDENTIFIED BY '%[3]s'; ALTER USER '%[1]s'@'%[2]s' IDENTIFIED BY '%[3]s' REQUIRE SSL; "+
		"GRANT REPLICATION SLAVE, REPLICATION CLIENT, SELECT, RELOAD, SHOW VIEW, EVENT, TRIGGER, LOCK TABLES ON *.* TO '%[1]s'@'%[2]s';", user, s.ReplicaIP, pass)
	if _, err := m.env.Run.RunInput(ctx, sql, "mariadb", "--batch"); err != nil {
		return nil, err
	}
	if restart {
		if _, err := m.env.Run.Run(ctx, "systemctl", "restart", "mariadb"); err != nil {
			return nil, err
		}
	}
	m.env.Publish("database.replication.source", s.ReplicaIP, nil)
	return &ReplicationGrant{User: user, Password: pass, ServerID: s.ServerID, Restart: restart}, nil
}

func (m *Manager) writeCnf(path, body string) (bool, error) {
	old, _ := os.ReadFile(m.env.P(path))
	if string(old) == body {
		return false, nil
	}
	return true, kit.WriteFile(m.env.P(path), []byte(body), 0o644)
}

// ReplicaRequest turns this server into a replica of a primary.
type ReplicaRequest struct {
	PrimaryHost string `json:"primary_host"`
	User        string `json:"user"`
	Password    string `json:"password"`
	ServerID    int    `json:"server_id"`
	// CopyData copies every database from the primary first (replaces local data).
	CopyData bool `json:"copy_data"`
}

var userRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

func (m *Manager) StartReplica(ctx context.Context, r ReplicaRequest, log func(string, ...any)) error {
	if !ipRe.MatchString(r.PrimaryHost) {
		return fmt.Errorf("primary address must be an IP address")
	}
	if !userRe.MatchString(r.User) || len(r.Password) < 16 || strings.ContainsAny(r.Password, `'\"`) {
		return fmt.Errorf("replication user or password looks wrong")
	}
	if r.ServerID < 1 {
		return fmt.Errorf("server ID must be a positive number unique in the cluster")
	}
	cnf := fmt.Sprintf("# Written by Ike: replica\n[mariadb]\nserver_id = %d\nlog_bin = mariadb-bin\nbinlog_format = ROW\ngtid_strict_mode = 1\nlog_slave_updates = 1\nread_only = 1\nrelay_log = relay-bin\n", r.ServerID)
	restart, err := m.writeCnf("/etc/mysql/mariadb.conf.d/91-ike-replication.cnf", cnf)
	if err != nil {
		return err
	}
	if restart {
		log("Restarting MariaDB with replication settings")
		if _, err := m.env.Run.Run(ctx, "systemctl", "restart", "mariadb"); err != nil {
			return err
		}
	}
	if r.CopyData {
		log("Copying every database from %s (this replaces the data here)", r.PrimaryHost)
		// dump on the primary through the network, load here; --gtid records the position
		pr, pw := ioPipe()
		errc := make(chan error, 1)
		go func() {
			_, err := m.env.Run.Exec(ctx, cmdOut("mariadb-dump", []string{"--host=" + r.PrimaryHost, "--user=" + r.User, "--ssl",
				"--all-databases", "--single-transaction", "--gtid", "--master-data=1", "--routines", "--triggers", "--events"},
				[]string{"MYSQL_PWD=" + r.Password, "PATH=/usr/bin:/bin"}, pw))
			pw.CloseWithError(err)
			errc <- err
		}()
		if _, err := m.env.Run.Exec(ctx, cmdIn("mariadb", []string{"--batch"}, pr)); err != nil {
			return fmt.Errorf("loading the copy: %w", err)
		}
		if err := <-errc; err != nil {
			return fmt.Errorf("copying from the primary: %w", err)
		}
	}
	sql := fmt.Sprintf("STOP SLAVE; CHANGE MASTER TO MASTER_HOST='%s', MASTER_USER='%s', MASTER_PASSWORD='%s', MASTER_SSL=1, MASTER_USE_GTID=slave_pos, MASTER_CONNECT_RETRY=10; START SLAVE;",
		r.PrimaryHost, r.User, r.Password)
	if _, err := m.env.Run.RunInput(ctx, sql, "mariadb", "--batch"); err != nil {
		return err
	}
	log("Replication started")
	m.env.Publish("database.replication.replica", r.PrimaryHost, nil)
	return nil
}

// ReplicaStatus is the replica side's health.
type ReplicaStatus struct {
	Configured    bool   `json:"configured"`
	IORunning     bool   `json:"io_running"`
	SQLRunning    bool   `json:"sql_running"`
	SecondsBehind int    `json:"seconds_behind"`
	PrimaryHost   string `json:"primary_host"`
	GTID          string `json:"gtid"`
	LastError     string `json:"last_error,omitempty"`
	ReadOnly      bool   `json:"read_only"`
	BinaryLogging bool   `json:"binary_logging"`
}

func (m *Manager) ReplicationStatus(ctx context.Context) (*ReplicaStatus, error) {
	out, err := m.env.Run.RunInput(ctx, "SHOW SLAVE STATUS\\G SELECT CONCAT('ReadOnly: ', @@read_only); SELECT CONCAT('LogBin: ', @@log_bin);", "mariadb", "--batch", "--skip-column-names")
	if err != nil {
		return nil, err
	}
	st := &ReplicaStatus{SecondsBehind: -1}
	for _, l := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ": ")
		if !ok {
			continue
		}
		switch k {
		case "Master_Host":
			st.Configured, st.PrimaryHost = true, v
		case "Slave_IO_Running":
			st.IORunning = v == "Yes"
		case "Slave_SQL_Running":
			st.SQLRunning = v == "Yes"
		case "Seconds_Behind_Master":
			if n, err := strconv.Atoi(v); err == nil {
				st.SecondsBehind = n
			}
		case "Gtid_IO_Pos":
			st.GTID = v
		case "Last_Error", "Last_IO_Error", "Last_SQL_Error":
			if v != "" {
				st.LastError = v
			}
		case "ReadOnly":
			st.ReadOnly = v == "1"
		case "LogBin":
			st.BinaryLogging = v == "1"
		}
	}
	return st, nil
}

// StopReplica stops replicating and makes the server writable (promotion).
func (m *Manager) StopReplica(ctx context.Context, promote bool) error {
	sql := "STOP SLAVE;"
	if promote {
		sql += " RESET SLAVE ALL; SET GLOBAL read_only = 0;"
		_ = os.Remove(m.env.P("/etc/mysql/mariadb.conf.d/91-ike-replication.cnf"))
	}
	_, err := m.env.Run.RunInput(ctx, sql, "mariadb", "--batch")
	if err == nil && promote {
		m.env.Publish("database.replication.promoted", m.env.Cfg.Hostname, nil)
	}
	return err
}

// RedisReplicaOf points the server-wide Redis at a primary ("" stops it).
func (m *Manager) RedisReplicaOf(ctx context.Context, host string, port int, password string) error {
	if m.env.Cfg.Redis == "" {
		return fmt.Errorf("Redis %w", kit.ErrNotInstalled)
	}
	cli := "redis-cli"
	if m.env.Cfg.Redis == "valkey" {
		cli = "valkey-cli"
	}
	env := []string{"PATH=/usr/bin:/bin"}
	if host == "" {
		_, err := m.env.Run.Exec(ctx, runnerCmd(cli, []string{"REPLICAOF", "NO", "ONE"}, env))
		return err
	}
	if !ipRe.MatchString(host) || port < 1 || port > 65535 {
		return fmt.Errorf("primary must be an IP address and port")
	}
	if password != "" {
		if strings.ContainsAny(password, " \n\r") {
			return fmt.Errorf("password can't contain spaces")
		}
		if _, err := m.env.Run.Exec(ctx, runnerCmd(cli, []string{"CONFIG", "SET", "masterauth", password}, env)); err != nil {
			return err
		}
	}
	_, err := m.env.Run.Exec(ctx, runnerCmd(cli, []string{"REPLICAOF", host, strconv.Itoa(port)}, env))
	if err == nil {
		_, _ = m.env.Run.Exec(ctx, runnerCmd(cli, []string{"CONFIG", "REWRITE"}, env))
	}
	return err
}

var _ = time.Second

// RedisPrimary opens the server-wide Redis to one replica: a password,
// listening on the public address, and a firewall rule for that address only.
func (m *Manager) RedisPrimary(ctx context.Context, replicaIP string) (string, int, error) {
	if m.env.Cfg.Redis == "" {
		return "", 0, fmt.Errorf("Redis %w", kit.ErrNotInstalled)
	}
	if !ipRe.MatchString(replicaIP) {
		return "", 0, fmt.Errorf("replica address must be an IP address")
	}
	cli := "redis-cli"
	if m.env.Cfg.Redis == "valkey" {
		cli = "valkey-cli"
	}
	var saved struct {
		Password string `json:"password"`
	}
	path := m.env.P("/etc/ike/redis-primary.json")
	_ = kit.ReadJSON(path, &saved)
	env := []string{"PATH=/usr/bin:/bin"}
	if saved.Password != "" {
		env = append(env, "REDISCLI_AUTH="+saved.Password)
	} else {
		saved.Password = kit.RandomHex(24)
		if _, err := m.env.Run.Exec(ctx, runnerCmd(cli, []string{"CONFIG", "SET", "requirepass", saved.Password}, env)); err != nil {
			return "", 0, err
		}
		if err := kit.WriteJSON(path, saved); err != nil {
			return "", 0, err
		}
		env = append(env, "REDISCLI_AUTH="+saved.Password)
	}
	bind := "127.0.0.1 -::1"
	if m.env.Cfg.PublicIPv4 != "" {
		bind += " " + m.env.Cfg.PublicIPv4
	}
	for _, kv := range [][]string{{"bind", bind}, {"masterauth", saved.Password}} {
		if _, err := m.env.Run.Exec(ctx, runnerCmd(cli, append([]string{"CONFIG", "SET"}, kv...), env)); err != nil {
			return "", 0, err
		}
	}
	_, _ = m.env.Run.Exec(ctx, runnerCmd(cli, []string{"CONFIG", "REWRITE"}, env))
	_, _ = m.env.Run.Run(ctx, "ufw", "allow", "from", replicaIP, "to", "any", "port", "6379", "proto", "tcp", "comment", "Redis replica")
	return saved.Password, 6379, nil
}
