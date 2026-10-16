package monitoring

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseCPUOnline(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 1},
		{"0-11", 12},
		{"0,2-3,5", 4},
		{"0-0", 1},
	}
	for _, tc := range cases {
		got, err := parseCPUOnline(tc.in)
		if err != nil {
			t.Fatalf("parseCPUOnline(%q) err=%v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("parseCPUOnline(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
	if _, err := parseCPUOnline(""); err == nil {
		t.Fatal("empty online should fail")
	}
	if _, err := parseCPUOnline("3-1"); err == nil {
		t.Fatal("reversed range should fail")
	}
}

func TestParseCPUMax(t *testing.T) {
	ncpu, ok, err := parseCPUMax("max 100000\n")
	if err != nil || ok {
		t.Fatalf("unlimited: ncpu=%v ok=%v err=%v", ncpu, ok, err)
	}
	ncpu, ok, err = parseCPUMax("100000 100000\n")
	if err != nil || !ok || ncpu != 1 {
		t.Fatalf("1 cpu quota: ncpu=%v ok=%v err=%v", ncpu, ok, err)
	}
	ncpu, ok, err = parseCPUMax("50000 100000\n")
	if err != nil || !ok || ncpu != 0.5 {
		t.Fatalf("half cpu: ncpu=%v ok=%v err=%v", ncpu, ok, err)
	}
}

func TestCgroupCPUStatAvailableGate(t *testing.T) {
	dir := t.TempDir()
	stat := filepath.Join(dir, "cpu.stat")
	maxp := filepath.Join(dir, "cpu.max")
	envp := filepath.Join(dir, "environ")
	cgp := filepath.Join(dir, "cgroup")
	marker := filepath.Join(dir, ".dockerenv")

	if err := os.WriteFile(stat, []byte("usage_usec 1000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(maxp, []byte("max 100000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envp, []byte("PATH=/usr/bin\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cgp, []byte("0::/init.scope\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldStat, oldMax := cgroupCPUStatPath, cgroupCPUMaxPath
	oldEnv, oldCg := proc1EnvironPath, procSelfCgroupPath
	oldMarkers, oldHook := containerMarkerPaths, isContainerEnv
	cgroupCPUStatPath, cgroupCPUMaxPath = stat, maxp
	proc1EnvironPath, procSelfCgroupPath = envp, cgp
	containerMarkerPaths = []string{marker}
	isContainerEnv = detectContainerEnv
	t.Cleanup(func() {
		cgroupCPUStatPath, cgroupCPUMaxPath = oldStat, oldMax
		proc1EnvironPath, procSelfCgroupPath = oldEnv, oldCg
		containerMarkerPaths = oldMarkers
		isContainerEnv = oldHook
	})

	// Host root cgroup with unlimited cpu.max must not enter cgroup CPU path
	if cgroupCPUStatAvailable() {
		t.Fatal("host root cgroup without finite quota should not enable cgroup CPU")
	}

	// Empty container= in /proc/1/environ must not enable cgroup CPU path
	if err := os.WriteFile(envp, []byte("container=\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cgroupCPUStatAvailable() {
		t.Fatal("empty container= in environ should not enable cgroup CPU")
	}

	// Host systemd user/system slices and daemon .service units with unlimited cpu.max must not enter cgroup CPU path
	for _, hostCg := range []string{
		"0::/user.slice/user-0.slice/session-1.scope\n",
		"0::/system.slice/komari.service\n",
		"0::/system.slice/docker.service\n",
		"0::/system.slice/lxc.service\n",
	} {
		if err := os.WriteFile(cgp, []byte(hostCg), 0o644); err != nil {
			t.Fatal(err)
		}
		if cgroupCPUStatAvailable() {
			t.Fatalf("host slice %q without finite quota should not enable cgroup CPU", hostCg)
		}
	}

	// Finite cpu.max on host enables cgroup CPU path
	if err := os.WriteFile(maxp, []byte("200000 100000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !cgroupCPUStatAvailable() {
		t.Fatal("finite cpu.max should enable cgroup CPU")
	}

	// Unlimited cpu.max inside container cgroup path enables cgroup CPU path
	if err := os.WriteFile(maxp, []byte("max 100000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, containerCg := range []string{
		"0::/lxc.payload.ct101\n",
		"0::/lxc.payload.ct101/init.scope\n",
		"0::/lxc.payload.ct101/system.slice/komari-agent.service\n",
		"0::/lxc/ct101\n",
		"0::/system.slice/docker-abcdef.scope\n",
	} {
		if err := os.WriteFile(cgp, []byte(containerCg), 0o644); err != nil {
			t.Fatal(err)
		}
		if !cgroupCPUStatAvailable() {
			t.Fatalf("container cgroup path %q should enable cgroup CPU", containerCg)
		}
	}
}

func TestCPUUsageFromCgroup(t *testing.T) {
	dir := t.TempDir()
	stat := filepath.Join(dir, "cpu.stat")
	maxp := filepath.Join(dir, "cpu.max")
	online := filepath.Join(dir, "online")
	if err := os.WriteFile(maxp, []byte("max 100000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(online, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stat, []byte("usage_usec 1000000\nuser_usec 400000\nsystem_usec 600000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldStat, oldMax, oldOnline, oldHook := cgroupCPUStatPath, cgroupCPUMaxPath, cpuOnlinePath, isContainerEnv
	cgroupCPUStatPath, cgroupCPUMaxPath, cpuOnlinePath = stat, maxp, online
	isContainerEnv = func() bool { return true }
	resetCgroupCPUSample()
	t.Cleanup(func() {
		cgroupCPUStatPath, cgroupCPUMaxPath, cpuOnlinePath, isContainerEnv = oldStat, oldMax, oldOnline, oldHook
		resetCgroupCPUSample()
	})

	if _, ok := cpuUsageFromCgroup(); ok {
		t.Fatal("first sample must not report a percent")
	}

	time.Sleep(250 * time.Millisecond)
	// +50000 usec over ~250ms on 1 CPU ≈ 20%
	if err := os.WriteFile(stat, []byte("usage_usec 1050000\nuser_usec 420000\nsystem_usec 630000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pct, ok := cpuUsageFromCgroup()
	if !ok {
		t.Fatal("second sample should report")
	}
	if pct < 5 || pct > 40 {
		t.Fatalf("pct=%v want around 20", pct)
	}

	// Quota hot-reload (1 CPU -> 2 CPUs): reseeds baseline and preserves lastCgroupPct
	time.Sleep(250 * time.Millisecond)
	if err := os.WriteFile(maxp, []byte("200000 100000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stat, []byte("usage_usec 1300000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pctAfterQuotaChange, ok := cpuUsageFromCgroup()
	if !ok || pctAfterQuotaChange != pct {
		t.Fatalf("quota change should reseed and return previous pct=%v, got (%v, %v)", pct, pctAfterQuotaChange, ok)
	}
	if lastCgroupNCPU != 2 || lastCgroupUsage != 1300000 {
		t.Fatalf("quota change did not reseed baseline: ncpu=%v usage=%d", lastCgroupNCPU, lastCgroupUsage)
	}

	// Transient read failure invalidates sample baseline while keeping last valid pct
	if err := os.Remove(stat); err != nil {
		t.Fatal(err)
	}
	pctOnErr, ok := cpuUsageFromCgroup()
	if !ok || pctOnErr != pct {
		t.Fatalf("transient read failure should return cached pct=%v, got (%v, %v)", pct, pctOnErr, ok)
	}
	if haveCgroupSample {
		t.Fatal("transient read failure must invalidate haveCgroupSample")
	}
	// Restore stat: first read after failure reseeds baseline rather than diluting over outage interval
	if err := os.WriteFile(stat, []byte("usage_usec 1400000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pctAfterRestore, ok := cpuUsageFromCgroup()
	if !ok || pctAfterRestore != pct || !haveCgroupSample || lastCgroupUsage != 1400000 {
		t.Fatalf("restore after failure should reseed baseline: pct=%v ok=%v haveSample=%v usage=%d", pctAfterRestore, ok, haveCgroupSample, lastCgroupUsage)
	}

	// Counter rollback (usage < lastCgroupUsage): clears haveCgroupPct and returns (0, false)
	if err := os.WriteFile(stat, []byte("usage_usec 100000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pctRollback, ok := cpuUsageFromCgroup()
	if ok || pctRollback != 0 || haveCgroupPct {
		t.Fatalf("counter rollback must clear haveCgroupPct and return (0, false), got (%v, %v)", pctRollback, ok)
	}
	if lastCgroupUsage != 100000 || !haveCgroupSample {
		t.Fatalf("counter rollback must reseed baseline usage=100000, got %d", lastCgroupUsage)
	}
}
