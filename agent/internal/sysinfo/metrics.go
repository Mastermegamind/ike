package sysinfo

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Point is one 5-minute sample of the whole server.
type Point struct {
	Time       time.Time `json:"t"`
	CPUPercent float64   `json:"cpu"`     // 0-100, all cores together
	MemUsedMB  float64   `json:"mem"`     // total minus available
	SwapUsedMB float64   `json:"swap"`    // swap total minus free
	NetInBps   float64   `json:"net_in"`  // bytes per second received, all interfaces but lo
	NetOutBps  float64   `json:"net_out"` // bytes per second sent
	DiskReadB  float64   `json:"disk_rd"` // bytes per second read from block devices
	DiskWriteB float64   `json:"disk_wr"` // bytes per second written
	Load1      float64   `json:"load1"`
}

// Interface is one network interface's current rates and totals.
type Interface struct {
	Name     string  `json:"name"`
	InBps    float64 `json:"in_bps"`
	OutBps   float64 `json:"out_bps"`
	RxBytes  uint64  `json:"rx_bytes"`
	TxBytes  uint64  `json:"tx_bytes"`
	RxErrors uint64  `json:"rx_errors"`
	TxErrors uint64  `json:"tx_errors"`
}

// Metrics is what GET /v1/server/metrics returns.
type Metrics struct {
	Now         Point       `json:"now"`
	MemTotalMB  float64     `json:"mem_total_mb"`
	SwapTotalMB float64     `json:"swap_total_mb"`
	Interfaces  []Interface `json:"interfaces"`
	History     []Point     `json:"history"` // last 24 h, every 5 minutes
}

type counters struct {
	at        time.Time
	cpuBusy   uint64
	cpuTotal  uint64
	net       map[string][2]uint64 // rx, tx
	diskRead  uint64               // sectors
	diskWrite uint64
}

// Sampler keeps the server's recent history. Proc is "/proc" in production;
// State is where history survives restarts (may be empty).
type Sampler struct {
	Proc  string
	State string

	mu     sync.Mutex
	prev   *counters
	now    Point
	memTot float64
	swpTot float64
	ifaces []Interface
	hist   []Point
	acc    []Point // samples since the last 5-minute point
}

// NewSampler loads saved history.
func NewSampler(proc, state string) *Sampler {
	s := &Sampler{Proc: proc, State: state}
	if state != "" {
		if b, err := os.ReadFile(state); err == nil {
			_ = json.Unmarshal(b, &s.hist)
		}
	}
	return s
}

func (s *Sampler) read() (*counters, map[string]uint64, float64) {
	c := &counters{net: map[string][2]uint64{}}
	if b, err := os.ReadFile(filepath.Join(s.Proc, "stat")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 8 || f[0] != "cpu" {
				continue
			}
			var v [10]uint64
			for i := 1; i < len(f) && i <= 10; i++ {
				v[i-1], _ = strconv.ParseUint(f[i], 10, 64)
			}
			// user nice system idle iowait irq softirq steal (guest is inside user)
			idle := v[3] + v[4]
			for i := 0; i < 8; i++ {
				c.cpuTotal += v[i]
			}
			c.cpuBusy = c.cpuTotal - idle
			break
		}
	}
	errs := map[string]uint64{}
	if f, err := os.Open(filepath.Join(s.Proc, "net/dev")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			name, rest, ok := strings.Cut(sc.Text(), ":")
			if !ok {
				continue
			}
			name = strings.TrimSpace(name)
			fl := strings.Fields(rest)
			if len(fl) < 16 || name == "lo" {
				continue
			}
			rx, _ := strconv.ParseUint(fl[0], 10, 64)
			rxe, _ := strconv.ParseUint(fl[2], 10, 64)
			tx, _ := strconv.ParseUint(fl[8], 10, 64)
			txe, _ := strconv.ParseUint(fl[10], 10, 64)
			c.net[name] = [2]uint64{rx, tx}
			errs[name+"/rx"], errs[name+"/tx"] = rxe, txe
		}
		f.Close()
	}
	if b, err := os.ReadFile(filepath.Join(s.Proc, "diskstats")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 10 || !wholeDisk(f[2]) {
				continue
			}
			r, _ := strconv.ParseUint(f[5], 10, 64)
			w, _ := strconv.ParseUint(f[9], 10, 64)
			c.diskRead += r
			c.diskWrite += w
		}
	}
	load := 0.0
	if b, err := os.ReadFile(filepath.Join(s.Proc, "loadavg")); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			load, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	return c, errs, load
}

// wholeDisk keeps sda, vda, xvda, nvme0n1, md0; skips partitions and loop/ram devices.
func wholeDisk(n string) bool {
	switch {
	case strings.HasPrefix(n, "loop"), strings.HasPrefix(n, "ram"), strings.HasPrefix(n, "dm-"), strings.HasPrefix(n, "sr"):
		return false
	case strings.HasPrefix(n, "nvme"):
		return !strings.Contains(n[4:], "p")
	case strings.HasPrefix(n, "md"):
		return true
	}
	return len(n) > 0 && (n[len(n)-1] < '0' || n[len(n)-1] > '9')
}

func (s *Sampler) memory() (total, used, swapTotal, swapUsed float64) {
	f, err := os.Open(filepath.Join(s.Proc, "meminfo"))
	if err != nil {
		return
	}
	defer f.Close()
	v := map[string]float64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) >= 2 {
			kb, _ := strconv.ParseFloat(fl[1], 64)
			v[strings.TrimSuffix(fl[0], ":")] = kb / 1024
		}
	}
	return v["MemTotal"], v["MemTotal"] - v["MemAvailable"], v["SwapTotal"], v["SwapTotal"] - v["SwapFree"]
}

// Sample takes one measurement; call it every minute.
func (s *Sampler) Sample(now time.Time) {
	c, errs, load := s.read()
	c.at = now
	memTot, memUsed, swpTot, swpUsed := s.memory()

	s.mu.Lock()
	defer s.mu.Unlock()
	p := Point{Time: now.UTC(), MemUsedMB: r1(memUsed), SwapUsedMB: r1(swpUsed), Load1: load}
	var ifaces []Interface
	prev := s.prev
	dt := 0.0
	if prev != nil {
		dt = now.Sub(prev.at).Seconds()
	}
	if prev != nil && dt > 0 {
		if dTot := float64(c.cpuTotal) - float64(prev.cpuTotal); dTot > 0 {
			p.CPUPercent = r1(100 * (float64(c.cpuBusy) - float64(prev.cpuBusy)) / dTot)
		}
		p.DiskReadB = r1(delta(c.diskRead, prev.diskRead) * 512 / dt)
		p.DiskWriteB = r1(delta(c.diskWrite, prev.diskWrite) * 512 / dt)
	}
	for name, v := range c.net {
		in := Interface{Name: name, RxBytes: v[0], TxBytes: v[1], RxErrors: errs[name+"/rx"], TxErrors: errs[name+"/tx"]}
		if prev != nil && dt > 0 {
			if pv, ok := prev.net[name]; ok {
				in.InBps = r1(delta(v[0], pv[0]) / dt)
				in.OutBps = r1(delta(v[1], pv[1]) / dt)
			}
		}
		p.NetInBps += in.InBps
		p.NetOutBps += in.OutBps
		ifaces = append(ifaces, in)
	}
	sort.Slice(ifaces, func(i, j int) bool { return ifaces[i].Name < ifaces[j].Name })
	s.prev, s.now, s.memTot, s.swpTot, s.ifaces = c, p, r1(memTot), r1(swpTot), ifaces

	s.acc = append(s.acc, p)
	if last := len(s.hist) - 1; last < 0 || now.Sub(s.hist[last].Time) >= 5*time.Minute {
		s.hist = append(s.hist, average(s.acc, now))
		s.acc = nil
		cut := now.Add(-24 * time.Hour)
		for len(s.hist) > 0 && s.hist[0].Time.Before(cut) {
			s.hist = s.hist[1:]
		}
		if s.State != "" {
			if b, err := json.Marshal(s.hist); err == nil {
				tmp := s.State + ".tmp"
				if os.WriteFile(tmp, b, 0o600) == nil {
					_ = os.Rename(tmp, s.State)
				}
			}
		}
	}
}

// average folds the minute samples into one 5-minute point (memory and
// swap are the highest seen, so short spikes still show).
func average(ps []Point, at time.Time) Point {
	out := Point{Time: at.UTC()}
	if len(ps) == 0 {
		return out
	}
	for _, p := range ps {
		out.CPUPercent += p.CPUPercent
		out.NetInBps += p.NetInBps
		out.NetOutBps += p.NetOutBps
		out.DiskReadB += p.DiskReadB
		out.DiskWriteB += p.DiskWriteB
		out.Load1 += p.Load1
		if p.MemUsedMB > out.MemUsedMB {
			out.MemUsedMB = p.MemUsedMB
		}
		if p.SwapUsedMB > out.SwapUsedMB {
			out.SwapUsedMB = p.SwapUsedMB
		}
	}
	n := float64(len(ps))
	out.CPUPercent, out.NetInBps, out.NetOutBps = r1(out.CPUPercent/n), r1(out.NetInBps/n), r1(out.NetOutBps/n)
	out.DiskReadB, out.DiskWriteB, out.Load1 = r1(out.DiskReadB/n), r1(out.DiskWriteB/n), r1(out.Load1/n)
	return out
}

// Metrics returns the latest sample and the 24-hour history.
func (s *Sampler) Metrics() Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := Metrics{Now: s.now, MemTotalMB: s.memTot, SwapTotalMB: s.swpTot,
		Interfaces: append([]Interface{}, s.ifaces...), History: append([]Point{}, s.hist...)}
	return m
}

func delta(a, b uint64) float64 {
	if a < b { // counter reset (interface came back, reboot)
		return 0
	}
	return float64(a - b)
}

func r1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

// Run samples every minute until ctx ends.
func (s *Sampler) Run(ctx context.Context) {
	s.Sample(time.Now())
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.Sample(now)
		}
	}
}
