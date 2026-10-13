package sites

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UsagePoint is one sample of an account's resource use.
type UsagePoint struct {
	Time       time.Time `json:"t"`
	CPUPercent float64   `json:"cpu"` // 100 = one core
	MemoryMB   float64   `json:"mem"`
}

// AccountUsage is what one account is using right now plus its recent history.
type AccountUsage struct {
	Account          string       `json:"account"`
	CPUPercent       float64      `json:"cpu_percent"`
	MemoryMB         float64      `json:"memory_mb"`
	Processes        int          `json:"processes"`
	DiskMB           float64      `json:"disk_mb"`
	BandwidthTodayMB float64      `json:"bandwidth_today_mb"`
	Limits           Limits       `json:"limits"`
	History          []UsagePoint `json:"history"` // last 24 h, every 5 min
}

const clkTck = 100 // USER_HZ on Linux

type usageState struct {
	mu        sync.Mutex
	prevTicks map[int]uint64 // uid -> total CPU ticks at last sample
	prevAt    time.Time
	current   map[string]*AccountUsage
	disk      map[string]float64
	diskAt    time.Time
	logOff    map[string]int64 // access log path -> bytes read
	bwDay     string
	bw        map[string]float64 // account -> bytes today
}

var usage = &usageState{prevTicks: map[int]uint64{}, current: map[string]*AccountUsage{}, disk: map[string]float64{},
	logOff: map[string]int64{}, bw: map[string]float64{}}

// procTotals sums CPU ticks, RSS and process count per uid from /proc.
func procTotals(proc string) (ticks map[int]uint64, rssKB map[int]uint64, procs map[int]int) {
	ticks, rssKB, procs = map[int]uint64{}, map[int]uint64{}, map[int]int{}
	entries, _ := os.ReadDir(proc)
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		status, err := os.ReadFile(filepath.Join(proc, e.Name(), "status"))
		if err != nil {
			continue
		}
		uid, rss := -1, uint64(0)
		for _, l := range strings.Split(string(status), "\n") {
			f := strings.Fields(l)
			if len(f) < 2 {
				continue
			}
			switch f[0] {
			case "Uid:":
				uid, _ = strconv.Atoi(f[1])
			case "VmRSS:":
				rss, _ = strconv.ParseUint(f[1], 10, 64)
			}
		}
		if uid < 1000 {
			continue // system users
		}
		stat, err := os.ReadFile(filepath.Join(proc, e.Name(), "stat"))
		if err != nil {
			continue
		}
		// Fields after the ")" that closes the command name: utime is 14th, stime 15th overall.
		s := string(stat)
		if i := strings.LastIndex(s, ")"); i >= 0 {
			f := strings.Fields(s[i+1:])
			if len(f) > 12 {
				u, _ := strconv.ParseUint(f[11], 10, 64)
				st, _ := strconv.ParseUint(f[12], 10, 64)
				ticks[uid] += u + st
			}
		}
		rssKB[uid] += rss
		procs[uid]++
	}
	return
}

// bytes field of a combined log line: `"GET / HTTP/1.1" 200 5120 "-"`
var logBytesRe = regexp.MustCompile(`" \d{3} (\d+) `)

// readNewBytes adds up response sizes appended to an access log since last time.
func readNewBytes(path string, off int64) (sum float64, newOff int64) {
	f, err := os.Open(path)
	if err != nil {
		return 0, off
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, off
	}
	if fi.Size() < off { // rotated
		off = 0
	}
	if fi.Size()-off > 64<<20 { // first run on a huge log: count only the recent part
		off = fi.Size() - 64<<20
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return 0, off
	}
	r := bufio.NewReaderSize(f, 1<<16)
	n := off
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 && strings.HasSuffix(line, "\n") {
			n += int64(len(line))
			if m := logBytesRe.FindStringSubmatch(line); m != nil {
				b, _ := strconv.ParseFloat(m[1], 64)
				sum += b
			}
		}
		if err != nil {
			break
		}
	}
	return sum, n
}

// SampleUsage takes one measurement. RunUsageSampler calls it every minute.
func (m *Manager) SampleUsage(ctx context.Context, now time.Time) {
	accts, _ := m.Accounts()
	sites, _ := m.List()
	ticks, rss, procs := procTotals("/proc")

	usage.mu.Lock()
	defer usage.mu.Unlock()
	elapsed := now.Sub(usage.prevAt).Seconds()
	day := now.Format("2006-01-02")
	if day != usage.bwDay {
		usage.bwDay, usage.bw = day, map[string]float64{}
	}
	for _, s := range sites {
		p := m.p(filepath.Join("/var/log/ike", s.Domain, "access.log"))
		add, off := readNewBytes(p, usage.logOff[p])
		usage.logOff[p] = off
		usage.bw[s.Account] += add
	}
	refreshDisk := now.Sub(usage.diskAt) > 30*time.Minute
	if refreshDisk {
		usage.diskAt = now
	}
	for _, a := range accts {
		cur := usage.current[a.Name]
		if cur == nil {
			cur = &AccountUsage{Account: a.Name}
			usage.current[a.Name] = cur
		}
		cur.Limits = a.Limits
		uid := -1
		if u, err := user.Lookup(a.Name); err == nil {
			uid, _ = strconv.Atoi(u.Uid)
		}
		if uid >= 0 {
			if prev, ok := usage.prevTicks[uid]; ok && elapsed > 0 && ticks[uid] >= prev {
				cur.CPUPercent = float64(ticks[uid]-prev) / clkTck / elapsed * 100
			}
			usage.prevTicks[uid] = ticks[uid]
			cur.MemoryMB = float64(rss[uid]) / 1024
			cur.Processes = procs[uid]
		}
		cur.BandwidthTodayMB = usage.bw[a.Name] / (1 << 20)
		if refreshDisk {
			if out, err := m.run.Run(ctx, "du", "-sb", "--one-file-system", m.accountLayout(a.Name).Home); err == nil {
				if f := strings.Fields(string(out)); len(f) > 0 {
					b, _ := strconv.ParseFloat(f[0], 64)
					usage.disk[a.Name] = b / (1 << 20)
				}
			}
		}
		cur.DiskMB = usage.disk[a.Name]
		last := len(cur.History) - 1
		if last < 0 || now.Sub(cur.History[last].Time) >= 5*time.Minute {
			cur.History = append(cur.History, UsagePoint{Time: now.UTC(), CPUPercent: round2(cur.CPUPercent), MemoryMB: round2(cur.MemoryMB)})
			if len(cur.History) > 288 {
				cur.History = cur.History[len(cur.History)-288:]
			}
		}
	}
	usage.prevAt = now
}

// Usage returns the latest measurements for every account.
func (m *Manager) Usage() []AccountUsage {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	accts, _ := m.Accounts()
	out := make([]AccountUsage, 0, len(accts))
	for _, a := range accts {
		if c := usage.current[a.Name]; c != nil {
			cp := *c
			cp.History = append([]UsagePoint(nil), c.History...)
			out = append(out, cp)
		} else {
			out = append(out, AccountUsage{Account: a.Name, Limits: a.Limits, History: []UsagePoint{}})
		}
	}
	return out
}

// RunUsageSampler samples every minute until ctx ends.
func (m *Manager) RunUsageSampler(ctx context.Context) {
	m.SampleUsage(ctx, time.Now())
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.SampleUsage(ctx, now)
		}
	}
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
