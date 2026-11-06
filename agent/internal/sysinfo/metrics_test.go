package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeProc(t *testing.T, dir string, cpuBusy, cpuIdle, rx, tx, rd, wr uint64, swapFree int) {
	t.Helper()
	must := func(name, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("stat", "cpu  "+u(cpuBusy)+" 0 0 "+u(cpuIdle)+" 0 0 0 0 0 0\ncpu0 1 2 3 4\n")
	must("net/dev", "Inter-|   Receive  |  Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n"+
		"    lo: 999999 1 0 0 0 0 0 0 999999 1 0 0 0 0 0 0\n  eth0: "+u(rx)+" 10 2 0 0 0 0 0 "+u(tx)+" 10 1 0 0 0 0 0\n")
	must("diskstats", "   7       0 loop0 1 0 100 0 1 0 100 0 0 0 0\n 253       0 vda 10 0 "+u(rd)+" 0 10 0 "+u(wr)+" 0 0 0 0\n 253       1 vda1 10 0 999999 0 10 0 999999 0 0 0 0\n")
	must("meminfo", "MemTotal:        4096000 kB\nMemAvailable:    1024000 kB\nSwapTotal:       2048000 kB\nSwapFree:        "+u(uint64(swapFree))+" kB\n")
	must("loadavg", "0.52 0.40 0.30 1/200 1234\n")
}

func u(v uint64) string { return itoa(v) }
func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func TestSamplerRates(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "metrics.json")
	s := NewSampler(dir, state)
	t0 := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	writeProc(t, dir, 1000, 9000, 0, 0, 0, 0, 2048000)
	s.Sample(t0)
	// One minute later: 30% busy, 6 MB in, 1.2 MB out, 60 KB read, 120 KB written, 512 MB of swap in use.
	writeProc(t, dir, 1000+1800, 9000+4200, 6_000_000, 1_200_000, 120, 240, 2048000-524288)
	s.Sample(t0.Add(time.Minute))
	m := s.Metrics()
	if m.Now.CPUPercent != 30 {
		t.Fatalf("cpu = %v, want 30", m.Now.CPUPercent)
	}
	if m.Now.NetInBps != 100000 || m.Now.NetOutBps != 20000 {
		t.Fatalf("net = %v in, %v out", m.Now.NetInBps, m.Now.NetOutBps)
	}
	if m.Now.DiskReadB != 1024 || m.Now.DiskWriteB != 2048 {
		t.Fatalf("disk = %v read, %v write (loop devices and partitions must not count)", m.Now.DiskReadB, m.Now.DiskWriteB)
	}
	if m.Now.SwapUsedMB != 512 || m.SwapTotalMB != 2000 || m.Now.MemUsedMB != 3000 {
		t.Fatalf("swap %v/%v, mem %v", m.Now.SwapUsedMB, m.SwapTotalMB, m.Now.MemUsedMB)
	}
	if len(m.Interfaces) != 1 || m.Interfaces[0].Name != "eth0" || m.Interfaces[0].RxErrors != 2 {
		t.Fatalf("interfaces = %+v (lo must be skipped)", m.Interfaces)
	}
	// A counter reset doesn't produce a huge negative-wrapped rate.
	writeProc(t, dir, 1000+1800+600, 9000+4200+5400, 100, 100, 120, 240, 2048000)
	s.Sample(t0.Add(2 * time.Minute))
	if n := s.Metrics().Now.NetInBps; n != 0 {
		t.Fatalf("after reset net in = %v", n)
	}
	// History is saved and survives a restart.
	s.Sample(t0.Add(6 * time.Minute))
	again := NewSampler(dir, state)
	if len(again.Metrics().History) != 2 {
		t.Fatalf("history after restart = %d points", len(again.Metrics().History))
	}
}

func TestWholeDisk(t *testing.T) {
	for n, want := range map[string]bool{"sda": true, "sda1": false, "vda": true, "nvme0n1": true, "nvme0n1p2": false, "loop3": false, "md0": true, "dm-0": false, "xvda": true} {
		if wholeDisk(n) != want {
			t.Errorf("wholeDisk(%q) = %v", n, !want)
		}
	}
}
