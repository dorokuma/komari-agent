package monitoring

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestConnectionsCount(t *testing.T) {
	tcpCount, udpCount, err := ConnectionsCount()
	if err != nil {
		t.Fatalf("ConnectionsCount failed: %v", err)
	}

	if tcpCount < 0 {
		t.Errorf("Expected non-negative TCP count, got %d", tcpCount)
	}

	if udpCount < 0 {
		t.Errorf("Expected non-negative UDP count, got %d", udpCount)
	}

	t.Logf("TCP connections: %d, UDP connections: %d", tcpCount, udpCount)
}

func TestConnectionsCountCombinesProcAndFallbackErrors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("proc net fast path only runs on linux")
	}

	fallbackErr := errors.New("fallback unavailable")
	fallback := func() (int, int, error) {
		return 0, 0, fallbackErr
	}

	tcpCount, udpCount, err := connectionsCountWithProcFallback(t.TempDir(), fallback)
	if err == nil {
		t.Fatal("expected combined error, got nil")
	}
	if tcpCount != 0 || udpCount != 0 {
		t.Fatalf("expected zero counts on failure, got tcp=%d udp=%d", tcpCount, udpCount)
	}
	if !errors.Is(err, fallbackErr) {
		t.Fatalf("expected combined error to wrap fallback error, got %v", err)
	}

	errText := err.Error()
	for _, want := range []string{"proc net fast path failed", "no proc net files found", "gopsutil fallback failed"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("expected error %q to contain %q", errText, want)
		}
	}
}

func TestParseNics(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]struct{}
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:  "single nic",
			input: "eth0",
			expected: map[string]struct{}{
				"eth0": {},
			},
		},
		{
			name:  "multiple nics",
			input: "eth0,wlan0,enp0s3",
			expected: map[string]struct{}{
				"eth0":   {},
				"wlan0":  {},
				"enp0s3": {},
			},
		},
		{
			name:  "nics with spaces",
			input: " eth0 , wlan0 , enp0s3 ",
			expected: map[string]struct{}{
				"eth0":   {},
				"wlan0":  {},
				"enp0s3": {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseNics(tt.input)

			if tt.expected == nil && result != nil {
				t.Errorf("Expected nil, got %v", result)
				return
			}

			if tt.expected != nil && result == nil {
				t.Errorf("Expected %v, got nil", tt.expected)
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d items, got %d", len(tt.expected), len(result))
				return
			}

			for key := range tt.expected {
				if _, exists := result[key]; !exists {
					t.Errorf("Expected key %s not found in result", key)
				}
			}
		})
	}
}

func TestShouldInclude(t *testing.T) {
	tests := []struct {
		name        string
		nicName     string
		includeNics map[string]struct{}
		excludeNics map[string]struct{}
		expected    bool
	}{
		{
			name:        "loopback interface should be excluded",
			nicName:     "lo",
			includeNics: nil,
			excludeNics: nil,
			expected:    false,
		},
		{
			name:        "docker interface should be excluded",
			nicName:     "docker0",
			includeNics: nil,
			excludeNics: nil,
			expected:    false,
		},
		{
			name:        "normal interface with no filters",
			nicName:     "eth0",
			includeNics: nil,
			excludeNics: nil,
			expected:    true,
		},
		{
			name:    "interface in include list",
			nicName: "eth0",
			includeNics: map[string]struct{}{
				"eth0": {},
			},
			excludeNics: nil,
			expected:    true,
		},
		{
			name:    "interface not in include list",
			nicName: "wlan0",
			includeNics: map[string]struct{}{
				"eth0": {},
			},
			excludeNics: nil,
			expected:    false,
		},
		{
			name:        "interface in exclude list",
			nicName:     "eth0",
			includeNics: nil,
			excludeNics: map[string]struct{}{
				"eth0": {},
			},
			expected: false,
		},
		{
			name:        "interface not in exclude list",
			nicName:     "wlan0",
			includeNics: nil,
			excludeNics: map[string]struct{}{
				"eth0": {},
			},
			expected: true,
		},
		{
			name:    "loopback in include list should still be excluded",
			nicName: "lo",
			includeNics: map[string]struct{}{
				"lo": {},
			},
			excludeNics: nil,
			expected:    false,
		},
		{
			name:        "interface with wildcard pattern in exclude list",
			nicName:     "tun0",
			includeNics: nil,
			excludeNics: map[string]struct{}{
				"tun*": {},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldInclude(tt.nicName, tt.includeNics, tt.excludeNics)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// writeSysfsStub 在临时目录构造 sysfs 网卡 stub：dirs 中每个名字创建目录，
// files 以 "<nic>/<file>" 为键写入内容。
func writeSysfsStub(t *testing.T, dirs []string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestShouldIncludeWithSysfs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sysfs topology checks only run on linux")
	}

	t.Run("hardware dominant filters non-device nic", func(t *testing.T) {
		// 硬件主导层：enp171s0 带 device；proxy0 为不命中任何默认排除前缀的
		// 中性名、无 device 应被锚点剔除；niclb0 带 device 但 type=772
		// 属回环硬拦截（772 不在 virtualNicSysfsTypes，唯有 isLoopbackNic 能排除）
		root := writeSysfsStub(t, []string{"enp171s0", "proxy0", "niclb0", "lo"}, map[string]string{
			"enp171s0/type":        "1",
			"enp171s0/device/keep": "", // 以目录模拟 device 链接存在
			"proxy0/type":          "1",
			"niclb0/type":          "772",
			"niclb0/device/keep":   "", // 带 device，消除硬件锚点遮蔽
			"lo/type":              "772",
		})
		if !shouldIncludeWithSysfs("enp171s0", nil, nil, root) {
			t.Error("enp171s0 with device should be included")
		}
		if shouldIncludeWithSysfs("proxy0", nil, nil, root) {
			t.Error("proxy0 without device should be excluded under hardware anchor")
		}
		if shouldIncludeWithSysfs("niclb0", nil, nil, root) {
			t.Error("niclb0 with type=772 should be excluded as loopback")
		}
		if shouldIncludeWithSysfs("lo", nil, nil, root) {
			t.Error("lo should be excluded")
		}
		// 生产集合入口：同一份 stub 经集合过滤应只剩 enp171s0
		got := filterInterfacesWithSysfs([]string{"enp171s0", "proxy0", "niclb0"}, nil, nil, root)
		if len(got) != 1 || got[0] != "enp171s0" {
			t.Fatalf("filterInterfacesWithSysfs = %v, want [enp171s0]", got)
		}
		// 显式白名单：第 1 级 type=772 回环硬拦截仍压过第 2 级 --include-nics
		includeLb := map[string]struct{}{"enp171s0": {}, "niclb0": {}}
		if shouldIncludeWithSysfs("niclb0", includeLb, nil, root) {
			t.Error("niclb0 with type=772 should be excluded even in include list")
		}
		gotInclude := filterInterfacesWithSysfs([]string{"enp171s0", "niclb0"}, includeLb, nil, root)
		if len(gotInclude) != 1 || gotInclude[0] != "enp171s0" {
			t.Fatalf("filterInterfacesWithSysfs with include = %v, want [enp171s0]", gotInclude)
		}
	})

	t.Run("include list beats default blacklist", func(t *testing.T) {
		root := writeSysfsStub(t, []string{"vmbr0", "eth0"}, map[string]string{
			"eth0/device/keep": "",
		})
		include := map[string]struct{}{"vmbr0": {}}
		if !shouldIncludeWithSysfs("vmbr0", include, nil, root) {
			t.Error("vmbr0 in include list should be included")
		}
		if shouldIncludeWithSysfs("eth0", include, nil, root) {
			t.Error("eth0 not in include list should be excluded")
		}
	})

	t.Run("exclude list wins over default filter", func(t *testing.T) {
		root := writeSysfsStub(t, []string{"eth0"}, map[string]string{
			"eth0/device/keep": "",
		})
		exclude := map[string]struct{}{"eth*": {}}
		if shouldIncludeWithSysfs("eth0", nil, exclude, root) {
			t.Error("eth0 matched by exclude pattern should be excluded")
		}
	})

	t.Run("tun_flags and virtual devtype and tunnel type excluded", func(t *testing.T) {
		// 纯容器层（eth0 无 device → 无硬件锚点）：排除完全依赖
		// isVirtualNicBySysfs 的 tun_flags/DEVTYPE/type 检查，名称均为不
		// 命中任何默认排除前缀的中性名
		root := writeSysfsStub(t, []string{"eth0", "xnet0", "xdev0", "xenc0"}, map[string]string{
			"eth0/type":     "1",
			"xnet0/tun_flags": "0x1",
			"xdev0/uevent":  "DEVTYPE=wireguard",
			"xenc0/type":    "768",
		})
		if shouldIncludeWithSysfs("xnet0", nil, nil, root) {
			t.Error("tun_flags nic should be excluded")
		}
		if shouldIncludeWithSysfs("xdev0", nil, nil, root) {
			t.Error("DEVTYPE=wireguard nic should be excluded")
		}
		if shouldIncludeWithSysfs("xenc0", nil, nil, root) {
			t.Error("type=768 tunnel nic should be excluded")
		}
		if !shouldIncludeWithSysfs("eth0", nil, nil, root) {
			t.Error("container uplink eth0 should be included")
		}
		got := filterInterfacesWithSysfs([]string{"eth0", "xnet0", "xdev0", "xenc0"}, nil, nil, root)
		if len(got) != 1 || got[0] != "eth0" {
			t.Fatalf("filterInterfacesWithSysfs = %v, want [eth0]", got)
		}
	})

	t.Run("container layer includes single non-device uplink", func(t *testing.T) {
		// 纯容器层：整机无任何带 device 的候选网卡，veth 模式主口 eth0 应保留
		root := writeSysfsStub(t, []string{"eth0", "lo"}, map[string]string{
			"eth0/type": "1",
			"lo/type":   "772",
		})
		if !shouldIncludeWithSysfs("eth0", nil, nil, root) {
			t.Error("container uplink eth0 should be included when no hardware anchor exists")
		}
		got := filterInterfacesWithSysfs([]string{"lo", "eth0"}, nil, nil, root)
		if len(got) != 1 || got[0] != "eth0" {
			t.Fatalf("filterInterfacesWithSysfs = %v, want [eth0]", got)
		}
	})

	t.Run("missing sysfs dir synthetic names included", func(t *testing.T) {
		root := t.TempDir() // 空 sysNetRoot：eth0/wlan0 目录不存在
		if !shouldIncludeWithSysfs("eth0", nil, nil, root) {
			t.Error("synthetic eth0 without sysfs dir should be included")
		}
		if !shouldIncludeWithSysfs("wlan0", nil, nil, root) {
			t.Error("synthetic wlan0 without sysfs dir should be included")
		}
		if shouldIncludeWithSysfs("veth9", nil, nil, root) {
			t.Error("default prefix veth should still be excluded")
		}
	})

	t.Run("empty set fallback keeps openvz style uplink", func(t *testing.T) {
		// 硬件主导层全部过滤为空时回退最小保底集合（venet0 类场景）
		root := writeSysfsStub(t, []string{"venet0", "hw0", "veth1", "lo"}, map[string]string{
			"venet0/type":     "1",
			"hw0/type":        "1",
			"hw0/device/keep": "",
			"veth1/type":      "1",
			"lo/type":         "772",
		})
		_ = root
		// venet0 命中默认前缀被排除；veth1 前缀排除；hw0 无锚点问题但名称不命中前缀、有 device → 自动过滤保留 hw0
		got := filterInterfacesWithSysfs([]string{"lo", "venet0", "veth1", "hw0"}, nil, nil, root)
		if len(got) != 1 || got[0] != "hw0" {
			t.Fatalf("expected hw0 only, got %v", got)
		}
		// 全部命中排除前缀（无 anchor 兜底场景）→ 保底保留 venet0（lo/veth 仍排除）
		root2 := writeSysfsStub(t, []string{"venet0", "veth1", "lo"}, map[string]string{
			"venet0/type": "1",
			"veth1/type":  "1",
			"lo/type":     "772",
		})
		got = filterInterfacesWithSysfs([]string{"lo", "venet0", "veth1"}, nil, nil, root2)
		if len(got) != 1 || got[0] != "venet0" {
			t.Fatalf("fallback should keep venet0 only, got %v", got)
		}
	})
}

func TestNetworkSpeedFallback(t *testing.T) {
	// 测试回退方法
	includeNics := map[string]struct{}{}
	excludeNics := map[string]struct{}{}

	totalUp, totalDown, upSpeed, downSpeed, err := getNetworkSpeedFallback(includeNics, excludeNics)
	if err != nil {
		t.Fatalf("getNetworkSpeedFallback failed: %v", err)
	}

	t.Logf("TotalUp: %d, TotalDown: %d, UpSpeed: %d/s, DownSpeed: %d/s",
		totalUp, totalDown, upSpeed, downSpeed)
}

func TestNetworkSpeedWithoutMonthRotate(t *testing.T) {
	originalMonthRotate := flags.MonthRotate
	originalIncludeNics := flags.IncludeNics
	originalExcludeNics := flags.ExcludeNics
	originalStateFile := flags.TrafficStateFile

	defer func() {
		flags.MonthRotate = originalMonthRotate
		flags.IncludeNics = originalIncludeNics
		flags.ExcludeNics = originalExcludeNics
		flags.TrafficStateFile = originalStateFile
	}()
	resetTrafficLedgerForTest()
	t.Cleanup(resetTrafficLedgerForTest)

	flags.MonthRotate = 0
	flags.IncludeNics = ""
	flags.ExcludeNics = ""
	flags.TrafficStateFile = t.TempDir() + "/traffic-state.json"

	totalUp, totalDown, upSpeed, downSpeed, err := NetworkSpeed()
	if err != nil {
		t.Fatalf("NetworkSpeed failed: %v", err)
	}

	t.Logf("Without MonthRotate - TotalUp: %d, TotalDown: %d, UpSpeed: %d/s, DownSpeed: %d/s",
		totalUp, totalDown, upSpeed, downSpeed)
}

func TestNetworkSpeedWithMonthRotate(t *testing.T) {
	// 保存原始值
	originalMonthRotate := flags.MonthRotate
	originalIncludeNics := flags.IncludeNics
	originalExcludeNics := flags.ExcludeNics
	originalStateFile := flags.TrafficStateFile

	// 恢复原始值
	defer func() {
		flags.MonthRotate = originalMonthRotate
		flags.IncludeNics = originalIncludeNics
		flags.ExcludeNics = originalExcludeNics
		flags.TrafficStateFile = originalStateFile
	}()
	resetTrafficLedgerForTest()
	t.Cleanup(resetTrafficLedgerForTest)

	// 设置测试值 - 启用月重置
	flags.MonthRotate = 1
	flags.IncludeNics = ""
	flags.ExcludeNics = ""
	flags.TrafficStateFile = t.TempDir() + "/traffic-state.json"

	totalUp, totalDown, upSpeed, downSpeed, err := NetworkSpeed()

	// 如果vnstat不可用，可能会回退到原来的方法，这是正常的
	if err != nil {
		t.Fatalf("NetworkSpeed failed: %v", err)
	}

	t.Logf("With MonthRotate - TotalUp: %d, TotalDown: %d, UpSpeed: %d/s, DownSpeed: %d/s",
		totalUp, totalDown, upSpeed, downSpeed)
}

func TestNetworkSpeedWithNicFilters(t *testing.T) {
	// 保存原始值
	originalMonthRotate := flags.MonthRotate
	originalIncludeNics := flags.IncludeNics
	originalExcludeNics := flags.ExcludeNics
	originalStateFile := flags.TrafficStateFile

	// 恢复原始值
	defer func() {
		flags.MonthRotate = originalMonthRotate
		flags.IncludeNics = originalIncludeNics
		flags.ExcludeNics = originalExcludeNics
		flags.TrafficStateFile = originalStateFile
	}()
	resetTrafficLedgerForTest()
	t.Cleanup(resetTrafficLedgerForTest)

	// 测试排除回环接口
	flags.MonthRotate = 0
	flags.IncludeNics = ""
	flags.ExcludeNics = "lo,docker0"
	flags.TrafficStateFile = t.TempDir() + "/traffic-state.json"

	totalUp, totalDown, upSpeed, downSpeed, err := NetworkSpeed()
	if err != nil {
		t.Fatalf("NetworkSpeed with excludeNics failed: %v", err)
	}

	t.Logf("With excludeNics - TotalUp: %d, TotalDown: %d, UpSpeed: %d/s, DownSpeed: %d/s",
		totalUp, totalDown, upSpeed, downSpeed)
}

// writeProcStub 构造伪 proc 根：boot_id、PID1 stat（含可定制 starttime）与 uptime。
func writeProcStub(t *testing.T, bootID string, starttimeTicks uint64, uptimeSeconds float64) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sys/kernel/random"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sys/kernel/random/boot_id"), []byte(bootID+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	// fields[0] 为第3字段 state，第22字段 starttime 位于 fields[19]
	stat := "1 (init) S"
	for i := 0; i < 18; i++ {
		stat += " 0"
	}
	stat += " " + strconv.FormatUint(starttimeTicks, 10)
	if err := os.WriteFile(filepath.Join(root, "1", "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "uptime"), []byte(strconv.FormatFloat(uptimeSeconds, 'f', 2, 64)+" 0.00"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// linkProcNetns 在伪 proc 根上构造 1/ns/net 符号链接（模拟容器 netns 标识）
func linkProcNetns(t *testing.T, procRoot, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(procRoot, "1", "ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(procRoot, "1", "ns", "net")); err != nil {
		t.Fatal(err)
	}
}

func newLedgerTestEnv(t *testing.T, procRoot string, uptime int64, hasUptime bool) ledgerEnv {
	t.Helper()
	sysNet := t.TempDir() // 空 sysfs：eth0 等合成名按“目录不存在”规则纳入
	return ledgerEnv{
		stateFile:       filepath.Join(t.TempDir(), "traffic-state.json"),
		procRoot:        procRoot,
		sysNetRoot:      sysNet,
		exeDir:          t.TempDir(),
		kernelUptime:    uptime,
		hasKernelUptime: hasUptime,
	}
}

func setMonthRotateForTest(t *testing.T, day int) {
	t.Helper()
	old := flags.MonthRotate
	flags.MonthRotate = day
	t.Cleanup(func() { flags.MonthRotate = old })
	resetTrafficLedgerForTest()
	t.Cleanup(resetTrafficLedgerForTest)
}

func readLedgerState(t *testing.T, path string) trafficLedgerState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state file %s unreadable: %v", path, err)
	}
	var st trafficLedgerState
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("state file %s corrupt: %v", path, err)
	}
	return st
}

// ledgerStateSnapshot 读取当前内存账本状态（常规增量走 30s 节流落盘，
// 单测用虚构时间推进，不依赖磁盘同步时机）。
func ledgerStateSnapshot() trafficLedgerState {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	return ledger.state
}

func TestTrafficLedgerColdStartForeverSeedsCurrent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("proc stub boot key only applies on linux")
	}
	setMonthRotateForTest(t, 0)
	proc := writeProcStub(t, "boot-aaaa", 5000, 1000)
	env := newLedgerTestEnv(t, proc, 10, true)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 100, Recv: 200}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if up != 100 || down != 200 {
		t.Fatalf("forever cold start should seed current counters, got up=%d down=%d", up, down)
	}
	st := readLedgerState(t, env.stateFile)
	if st.BootKey != "boot-aaaa|t=5000" {
		t.Fatalf("BootKey = %q, want boot-aaaa|t=5000", st.BootKey)
	}
	if st.CycleKey != "forever" {
		t.Fatalf("CycleKey = %q, want forever", st.CycleKey)
	}
	if st.Nics["eth0"] != (nicBaseline{Tx: 100, Rx: 200}) {
		t.Fatalf("baseline = %+v", st.Nics["eth0"])
	}
}

func TestTrafficLedgerBootSwitchAccumulatesCurrent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("proc stub boot key only applies on linux")
	}
	setMonthRotateForTest(t, 0)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	// ① 宿主机重启：boot_id 变化 → 计数器必然归零，整段累加
	t.Run("host reboot accumulates full counters", func(t *testing.T) {
		resetTrafficLedgerForTest()
		t.Cleanup(resetTrafficLedgerForTest)
		proc1 := writeProcStub(t, "boot-aaaa", 5000, 1000)
		env := newLedgerTestEnv(t, proc1, 10, true)
		if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 100, Recv: 200}}, now); err != nil {
			t.Fatal(err)
		}
		env.procRoot = writeProcStub(t, "boot-bbbb", 3000, 60)
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 120, Recv: 260}}, now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 220 || down != 460 {
			t.Fatalf("host reboot should accumulate full current counters, got up=%d down=%d", up, down)
		}
		st := readLedgerState(t, env.stateFile)
		if st.BootKey != "boot-bbbb|t=3000" {
			t.Fatalf("BootKey after switch = %q, want boot-bbbb|t=3000", st.BootKey)
		}
	})

	// ② LXC 新建 netns：boot_id 不变但 1/ns/net 标识变化 → 整段累加
	t.Run("new netns accumulates full counters", func(t *testing.T) {
		resetTrafficLedgerForTest()
		t.Cleanup(resetTrafficLedgerForTest)
		proc1 := writeProcStub(t, "boot-aaaa", 5000, 1000)
		linkProcNetns(t, proc1, "net:[1001]")
		env := newLedgerTestEnv(t, proc1, 10, true)
		if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 100, Recv: 200}}, now); err != nil {
			t.Fatal(err)
		}
		proc2 := writeProcStub(t, "boot-aaaa", 9000, 4000)
		linkProcNetns(t, proc2, "net:[1002]")
		env.procRoot = proc2
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 120, Recv: 260}}, now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 220 || down != 460 {
			t.Fatalf("new netns should accumulate full current counters, got up=%d down=%d", up, down)
		}
	})

	// ③a M1 反例：docker restart 保留 netns（仅 PID1 ticks 变化）→ 只累加差值
	t.Run("same netns restart accumulates delta only", func(t *testing.T) {
		resetTrafficLedgerForTest()
		t.Cleanup(resetTrafficLedgerForTest)
		proc1 := writeProcStub(t, "boot-aaaa", 5000, 1000)
		linkProcNetns(t, proc1, "net:[1001]")
		env := newLedgerTestEnv(t, proc1, 10, true)
		if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 200, Recv: 300}}, now); err != nil {
			t.Fatal(err)
		}
		proc2 := writeProcStub(t, "boot-aaaa", 9000, 4000)
		linkProcNetns(t, proc2, "net:[1001]")
		env.procRoot = proc2
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 210, Recv: 320}}, now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 210 || down != 320 {
			t.Fatalf("same netns restart should only add delta, got up=%d down=%d, want 210/320", up, down)
		}
	})

	// ③b M1 反例：netns 不可读（仅 ticks 变化）→ 同样按差值；单网卡实际归零时累加当前值
	t.Run("unknown netns restart accumulates delta and reset", func(t *testing.T) {
		resetTrafficLedgerForTest()
		t.Cleanup(resetTrafficLedgerForTest)
		proc1 := writeProcStub(t, "boot-aaaa", 5000, 1000)
		env := newLedgerTestEnv(t, proc1, 10, true)
		if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 200, Recv: 300}}, now); err != nil {
			t.Fatal(err)
		}
		// ticks 变化但计数器延续：prev=200 -> cur=210 只加 +10
		env.procRoot = writeProcStub(t, "boot-aaaa", 9000, 4000)
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 210, Recv: 320}}, now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 210 || down != 320 {
			t.Fatalf("unknown netns restart should only add delta, got up=%d down=%d, want 210/320", up, down)
		}
		// 再次 ticks 变化且计数器实际归零：cur=30 < prev=210 → 累加当前值 30
		env.procRoot = writeProcStub(t, "boot-aaaa", 12000, 4000)
		up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 30, Recv: 40}}, now.Add(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 240 || down != 360 {
			t.Fatalf("counter reset under same boot should add current value, got up=%d down=%d, want 240/360", up, down)
		}
	})

	// S-B 反例探针：netns 不可读的同内核代际切换中，未见基线的网卡
	// （中途重现/新纳入）一律只记基线，不得把当前计数重复灌入。
	t.Run("unknown netns reappearing nic sets baseline only", func(t *testing.T) {
		resetTrafficLedgerForTest()
		t.Cleanup(resetTrafficLedgerForTest)
		proc1 := writeProcStub(t, "boot-aaaa", 5000, 1000)
		env := newLedgerTestEnv(t, proc1, 10, true)
		// Round1：forever 冷启播种 400/600
		if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 100, Recv: 200},
			{Name: "usb0", Sent: 300, Recv: 400},
		}, now); err != nil {
			t.Fatal(err)
		}
		// Round2：usb0 消失（基线被剔除），eth0 +20/+40 → 420/640
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 120, Recv: 240},
		}, now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 420 || down != 640 {
			t.Fatalf("before epoch switch got up=%d down=%d, want 420/640", up, down)
		}
		// Round3：同 boot_id 仅 ticks 变化（netns 不可读），usb0 重现
		env.procRoot = writeProcStub(t, "boot-aaaa", 9000, 4000)
		up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 130, Recv: 260},
			{Name: "usb0", Sent: 350, Recv: 450},
		}, now.Add(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		// eth0 +10/+20 → 430/660；usb0 只记基线，不得灌入 350/450
		if up != 430 || down != 660 {
			t.Fatalf("reappearing usb0 must only set baseline, got up=%d down=%d, want 430/660", up, down)
		}
		// 基线已按本轮样本重设，下一拍正常累加 usb0 差值
		up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 140, Recv: 270},
			{Name: "usb0", Sent: 360, Recv: 460},
		}, now.Add(3*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 450 || down != 680 {
			t.Fatalf("delta after rebaseline got up=%d down=%d, want 450/680", up, down)
		}
	})
}

func TestTrafficLedgerCycleSwitchResets(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("proc stub only applies on linux")
	}
	setMonthRotateForTest(t, 13)
	proc := writeProcStub(t, "boot-aaaa", 5000, 1000)
	// 实例跨周期长跑（uptime 远大于距 lastReset 的时长）→ 冷启动周期累计为 0
	env := newLedgerTestEnv(t, proc, 30*24*3600, true)
	now1 := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) // lastReset=2026-10-13

	up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 1000, Recv: 2000}}, now1)
	if err != nil {
		t.Fatal(err)
	}
	if up != 0 || down != 0 {
		t.Fatalf("long-run monthly cold start should start at 0, got up=%d down=%d", up, down)
	}

	// 常规增量
	up, down, upSp, downSp, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 1300, Recv: 2800}}, now1.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if up != 300 || down != 800 {
		t.Fatalf("regular delta got up=%d down=%d", up, down)
	}
	if upSp != 30 || downSp != 80 {
		t.Fatalf("speed = %d/%d, want 30/80", upSp, downSp)
	}

	// 周期切换：2026-11-14 起 CycleKey 变为 month:13:2026-11-13 → 归零并重设基线
	up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 1400, Recv: 3000}}, time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if up != 0 || down != 0 {
		t.Fatalf("cycle switch should reset totals, got up=%d down=%d", up, down)
	}
	st := readLedgerState(t, env.stateFile)
	if st.CycleKey != "month:13:2026-11-13" {
		t.Fatalf("CycleKey = %q, want month:13:2026-11-13", st.CycleKey)
	}
	if st.Nics["eth0"] != (nicBaseline{Tx: 1400, Rx: 3000}) {
		t.Fatalf("baseline after cycle switch = %+v", st.Nics["eth0"])
	}
	if st.BootKey != "boot-aaaa|t=5000" {
		t.Fatalf("BootKey after cycle switch = %q, want boot-aaaa|t=5000", st.BootKey)
	}

	// 跨重置点重启（实例启动晚于新周期起点）：周期+代际同时切换 →
	// 播种当前计数，防止新周期已跑流量被清零
	proc2 := writeProcStub(t, "boot-bbbb", 9000, 4000)
	env.procRoot = proc2
	env.kernelUptime = 100 // 内核启动晚于新周期起点 2026-12-13：播种当前计数
	now2 := time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)
	up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 400, Recv: 900}}, now2)
	if err != nil {
		t.Fatal(err)
	}
	if up != 400 || down != 900 {
		t.Fatalf("cross-reset reboot should seed current counters, got up=%d down=%d", up, down)
	}
	st = readLedgerState(t, env.stateFile)
	if st.CycleKey != "month:13:2026-12-13" {
		t.Fatalf("CycleKey = %q, want month:13:2026-12-13", st.CycleKey)
	}
	if st.BootKey != "boot-bbbb|t=9000" {
		t.Fatalf("BootKey after reboot = %q, want boot-bbbb|t=9000", st.BootKey)
	}

	// 新周期内常规增量继续累加（验证上一分支的基线重设正确）
	up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 450, Recv: 950}}, now2.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if up != 450 || down != 950 {
		t.Fatalf("delta after cross-reset reboot got up=%d down=%d, want 450/950", up, down)
	}

	// 重置点之前的重启（实例启动早于新周期起点）+ 周期切换 → 仍归零
	// now3 进入下一周期（lastReset=2027-01-13），实例启动于 ~2026-12-05 早于该起点
	env.procRoot = writeProcStub(t, "boot-cccc", 7000, 2000)
	env.kernelUptime = 40 * 24 * 3600
	now3 := time.Date(2027, 1, 14, 12, 0, 0, 0, time.UTC)
	up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 60, Recv: 70}}, now3)
	if err != nil {
		t.Fatal(err)
	}
	if up != 0 || down != 0 {
		t.Fatalf("pre-reset reboot cycle switch should still zero totals, got up=%d down=%d", up, down)
	}
	st = readLedgerState(t, env.stateFile)
	if st.CycleKey != "month:13:2027-01-13" {
		t.Fatalf("CycleKey = %q, want month:13:2027-01-13", st.CycleKey)
	}
	if st.BootKey != "boot-cccc|t=7000" {
		t.Fatalf("BootKey after pre-reset reboot = %q, want boot-cccc|t=7000", st.BootKey)
	}

	// 当前周期先累计非零流量，再跨周期且同内核仅 PID1 ticks 变化
	// （docker restart，eth0 计数延续单调增长）→ 新周期须彻底归零，
	// 旧周期残留与长跑计数都不得灌入
	up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 90, Recv: 150}}, now3.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if up != 30 || down != 80 {
		t.Fatalf("delta within cycle got up=%d down=%d, want 30/80", up, down)
	}
	env.procRoot = writeProcStub(t, "boot-cccc", 8000, 2000) // 同 boot_id 仅 ticks 变化
	env.kernelUptime = 100 // 内核启动晚于新周期起点 2027-02-13：但同内核同 netns，仍归零
	now4 := time.Date(2027, 2, 14, 12, 0, 0, 0, time.UTC)
	up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 120, Recv: 180}}, now4)
	if err != nil {
		t.Fatal(err)
	}
	if up != 0 || down != 0 {
		t.Fatalf("same-boot cycle switch should zero stale totals, got up=%d down=%d", up, down)
	}
	st = readLedgerState(t, env.stateFile)
	if st.CycleKey != "month:13:2027-02-13" {
		t.Fatalf("CycleKey = %q, want month:13:2027-02-13", st.CycleKey)
	}

	// 切换回永久累计：CycleKey→forever 时播种当前计数而非清零
	flags.MonthRotate = 0
	up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 80, Recv: 120}}, now4.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if up != 80 || down != 120 {
		t.Fatalf("switch to forever should seed current counters, got up=%d down=%d", up, down)
	}
	st = readLedgerState(t, env.stateFile)
	if st.CycleKey != "forever" {
		t.Fatalf("CycleKey = %q, want forever", st.CycleKey)
	}
}

func TestTrafficLedgerRegularDeltaAndReset(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("proc stub only applies on linux")
	}
	setMonthRotateForTest(t, 0)
	proc := writeProcStub(t, "boot-aaaa", 5000, 1000)
	env := newLedgerTestEnv(t, proc, 10, true)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 100, Recv: 200}}, now); err != nil {
		t.Fatal(err)
	}
	// 常规增量：+50/+80；新出现网卡 wlan0 首观测只记基线
	up, down, upSp, downSp, err := updateTrafficLedger(env, []nicCounterSample{
		{Name: "eth0", Sent: 150, Recv: 280},
		{Name: "wlan0", Sent: 500, Recv: 600},
	}, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if up != 150 || down != 280 {
		t.Fatalf("got up=%d down=%d, want 150/280", up, down)
	}
	if upSp != 10 || downSp != 16 {
		t.Fatalf("speed = %d/%d, want 10/16", upSp, downSp)
	}
	// 同代际网卡驱动重置（cur < prev）：增量按 0，基线更新
	up, _, _, _, err = updateTrafficLedger(env, []nicCounterSample{
		{Name: "eth0", Sent: 30, Recv: 300},
		{Name: "wlan0", Sent: 520, Recv: 620},
	}, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if up != 170 {
		t.Fatalf("driver reset should contribute 0 delta (wlan0 +20), got up=%d", up)
	}
	st := ledgerStateSnapshot()
	if st.Nics["eth0"].Tx != 30 {
		t.Fatalf("baseline not updated after driver reset: %+v", st.Nics["eth0"])
	}

	// R-S2 回归：已消失网卡基线随当拍剔除，同名重建首拍不沿用陈旧基线
	t.Run("vanished nic reappears with fresh baseline", func(t *testing.T) {
		resetTrafficLedgerForTest()
		t.Cleanup(resetTrafficLedgerForTest)
		env := newLedgerTestEnv(t, writeProcStub(t, "boot-aaaa", 5000, 1000), 10, true)
		now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

		if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 10, Recv: 20},
			{Name: "usb0", Sent: 100, Recv: 200},
		}, now); err != nil {
			t.Fatal(err)
		}
		// usb0 消失：基线被剔除，eth0 +5/+5
		if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 15, Recv: 25},
		}, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		st := ledgerStateSnapshot()
		if _, ok := st.Nics["usb0"]; ok {
			t.Fatalf("vanished usb0 baseline should be evicted: %+v", st.Nics)
		}
		// usb0 同名重建：仅 eth0 +5/+5，不得出现 usb0 的 150/200 伪增量
		// （forever 冷启已播种 110/220，此时应为 120/230）
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 20, Recv: 30},
			{Name: "usb0", Sent: 250, Recv: 400},
		}, now.Add(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 120 || down != 230 {
			t.Fatalf("reappeared usb0 should not seed stale-baseline delta, got up=%d down=%d, want 120/230", up, down)
		}
		// 随后正常累加：eth0 +5/+5，usb0 +30/+50
		up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 25, Recv: 35},
			{Name: "usb0", Sent: 280, Recv: 450},
		}, now.Add(3*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 155 || down != 285 {
			t.Fatalf("got up=%d down=%d, want 155/285", up, down)
		}
	})
}

// TestTrafficLedgerKernelUptimeSeeding R-M1 回归：冷启与跨周期播种只信
// 内核/lxcfs uptime 与已证实的新建虚拟 netns 代际年龄，不信任 --net=host
// 容器 PID1 年龄。
func TestTrafficLedgerKernelUptimeSeeding(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("proc stub only applies on linux")
	}
	setMonthRotateForTest(t, 13)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) // lastReset=2026-11-13

	// kernelUptimeSeconds 直读 /proc/uptime 首字段，不再减 PID1 starttime
	t.Run("kernelUptimeSeconds reads /proc/uptime directly", func(t *testing.T) {
		// 宿主长跑 60 天、容器 PID1 仅 5 秒前启动的时钟域混合 stub
		proc := writeProcStub(t, "boot-aaaa", 518399500, 5184000)
		up, ok := kernelUptimeSeconds(proc)
		if !ok || up != 5184000 {
			t.Fatalf("kernelUptimeSeconds = %d, %v; want 5184000, true", up, ok)
		}
	})

	// --net=host 容器冷启：PID1 才 5 秒，但内核已跑 60 天且 uptime 未虚拟化，
	// 不得误播种宿主长跑计数
	t.Run("host-netns cold start does not seed", func(t *testing.T) {
		resetTrafficLedgerForTest()
		t.Cleanup(resetTrafficLedgerForTest)
		proc := writeProcStub(t, "boot-aaaa", 518399500, 5184000)
		env := newLedgerTestEnv(t, proc, 5184000, true)
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 500_000_000, Recv: 900_000_000},
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		if up != 0 || down != 0 {
			t.Fatalf("host-netns cold start must not seed host-longrun counters, got up=%d down=%d", up, down)
		}
	})

	// 长跑宿主同 boot_id 新建虚拟 netns：代际年龄 120s 晚于新周期起点 → 播种
	newNetnsCase := func(t *testing.T, withAnchor bool) (uint64, uint64) {
		t.Helper()
		resetTrafficLedgerForTest()
		t.Cleanup(resetTrafficLedgerForTest)
		procOld := writeProcStub(t, "boot-aaaa", 518300000, 5184000)
		linkProcNetns(t, procOld, "net:[1001]")
		env := newLedgerTestEnv(t, procOld, 5184000, true)
		if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 300, Recv: 400},
		}, now.Add(-4*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if withAnchor {
			// 宿主机硬件锚点存在：无法证明是独立虚拟 netns，保守归零
			env.sysNetRoot = writeSysfsStub(t, []string{"enp171s0"}, map[string]string{
				"enp171s0/type":        "1",
				"enp171s0/device/keep": "",
			})
		}
		procNew := writeProcStub(t, "boot-aaaa", 518388000, 5184000) // PID1 于 120 秒前启动
		linkProcNetns(t, procNew, "net:[1002]")
		env.procRoot = procNew
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{
			{Name: "eth0", Sent: 400, Recv: 900},
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		return up, down
	}

	t.Run("new virtual netns without anchor seeds", func(t *testing.T) {
		up, down := newNetnsCase(t, false)
		if up != 400 || down != 900 {
			t.Fatalf("new netns young generation should seed counters, got up=%d down=%d, want 400/900", up, down)
		}
	})

	t.Run("new netns masked by hardware anchor stays zero", func(t *testing.T) {
		up, down := newNetnsCase(t, true)
		if up != 0 || down != 0 {
			t.Fatalf("hardware anchor should force conservative zero, got up=%d down=%d", up, down)
		}
	})
}

// TestPersistTrafficLedgerAtomicCleansTempOnRenameFailure R-S4 回归：最终
// Rename 失败时必须清理 .traffic-state-*.tmp 临时文件。
func TestPersistTrafficLedgerAtomicCleansTempOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	// 目标指向已存在的非空目录 → os.Rename 必失败
	target := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(target, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := persistTrafficLedgerAtomic(target, &trafficLedgerState{}); err == nil {
		t.Fatal("expected rename onto non-empty dir to fail, got nil")
	}
	leftover, err := filepath.Glob(filepath.Join(dir, ".traffic-state-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftover) != 0 {
		t.Fatalf("temp files leaked after failed rename: %v", leftover)
	}
}

// TestTrafficLedgerCorruptBackupOnlyRegularFile S-A 回归：`.corrupt` 现场
// 备份仅限内容/schema 非法的常规文件；目录、权限错误等非损坏场景绝不
// Rename，不得破坏用户文件系统状态。
func TestTrafficLedgerCorruptBackupOnlyRegularFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("proc stub only applies on linux")
	}
	setMonthRotateForTest(t, 0)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	// stateFile 指向已存在的目录：只 WARN 并内存冷启，绝不 Rename 目录
	t.Run("directory state path is never renamed", func(t *testing.T) {
		resetTrafficLedgerForTest()
		t.Cleanup(resetTrafficLedgerForTest)
		stateDir := filepath.Join(t.TempDir(), "statedir")
		if err := os.MkdirAll(filepath.Join(stateDir, "inner"), 0o755); err != nil {
			t.Fatal(err)
		}
		env := newLedgerTestEnv(t, writeProcStub(t, "boot-aaaa", 5000, 1000), 10, true)
		env.stateFile = stateDir
		if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 10, Recv: 20}}, now); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(stateDir)
		if err != nil || !fi.IsDir() {
			t.Fatalf("state dir must be left intact, stat err=%v", err)
		}
		if _, err := os.Lstat(stateDir + ".corrupt"); !os.IsNotExist(err) {
			t.Fatalf("no .corrupt backup expected for dir state path, stat err=%v", err)
		}
	})

	// stateFile 为内容损坏的常规文件：备份 .corrupt 后全新初始化并落盘
	t.Run("corrupt regular file is backed up and reinitialized", func(t *testing.T) {
		resetTrafficLedgerForTest()
		t.Cleanup(resetTrafficLedgerForTest)
		env := newLedgerTestEnv(t, writeProcStub(t, "boot-aaaa", 5000, 1000), 10, true)
		if err := os.WriteFile(env.stateFile, []byte("{broken"), 0o644); err != nil {
			t.Fatal(err)
		}
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 100, Recv: 200}}, now)
		if err != nil {
			t.Fatal(err)
		}
		if up != 100 || down != 200 {
			t.Fatalf("fresh ledger after corrupt file got up=%d down=%d, want 100/200", up, down)
		}
		corrupt, err := os.ReadFile(env.stateFile + ".corrupt")
		if err != nil {
			t.Fatalf("corrupt backup missing: %v", err)
		}
		if string(corrupt) != "{broken" {
			t.Fatalf("corrupt backup content = %q, want {broken", corrupt)
		}
		st := readLedgerState(t, env.stateFile)
		if st.CycleKey != "forever" {
			t.Fatalf("CycleKey = %q, want forever", st.CycleKey)
		}
	})
}

func TestTrafficLedgerLegacyNetStaticMigration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("proc stub only applies on linux")
	}
	setMonthRotateForTest(t, 13)
	proc := writeProcStub(t, "boot-aaaa", 5000, 1000)
	env := newLedgerTestEnv(t, proc, 10, true)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) // lastReset=2026-10-13

	// 在 exeDir 构造旧 net_static.json：eth0 周期内 500/700 + 过期 900/900，veth0 全部不计
	lastReset := uint64(time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC).Unix())
	legacy := map[string]interface{}{
		"interfaces": map[string][]map[string]uint64{
			"eth0": {
				{"timestamp": lastReset + 100, "tx": 500, "rx": 700},
				{"timestamp": lastReset - 100, "tx": 900, "rx": 900},
			},
			"veth0": {
				{"timestamp": lastReset + 100, "tx": 50, "rx": 50},
			},
		},
	}
	data, _ := json.Marshal(legacy)
	legacyPath := filepath.Join(env.exeDir, "net_static.json")
	if err := os.WriteFile(legacyPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 1234, Recv: 5678}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if up != 500 || down != 700 {
		t.Fatalf("legacy migration got up=%d down=%d, want 500/700", up, down)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy file should be renamed, stat err=%v", err)
	}
	if _, err := os.Stat(legacyPath + ".migrated"); err != nil {
		t.Fatalf("legacy .migrated missing: %v", err)
	}
	st := readLedgerState(t, env.stateFile)
	if st.Nics["eth0"] != (nicBaseline{Tx: 1234, Rx: 5678}) {
		t.Fatalf("baseline = %+v", st.Nics["eth0"])
	}
}

// TestTrafficLedgerConcurrentStepAndFlush 并发压测 ledgerMu 与原子落盘：
// 4 个 goroutine 并发执行 updateTrafficLedger 与 FlushTrafficLedger 共 50 步，
// 断言无 panic 且最终状态文件可正常解析（供 -race -count=3 复跑）。
func TestTrafficLedgerConcurrentStepAndFlush(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("proc stub only applies on linux")
	}
	setMonthRotateForTest(t, 0)
	proc := writeProcStub(t, "boot-aaaa", 5000, 1000)
	env := newLedgerTestEnv(t, proc, 10, true)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	done := make(chan struct{}, 4)
	for g := 0; g < 4; g++ {
		go func(g int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 50; i++ {
				if _, _, _, _, err := updateTrafficLedger(env, []nicCounterSample{
					{Name: "eth0", Sent: uint64(100 + i), Recv: uint64(200 + i)},
				}, now.Add(time.Duration(g*50+i)*time.Millisecond)); err != nil {
					t.Errorf("updateTrafficLedger g%d step %d: %v", g, i, err)
					return
				}
				FlushTrafficLedger()
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		<-done
	}
	FlushTrafficLedger()
	readLedgerState(t, env.stateFile)
}

// TestTrafficLedgerBootKeyDomainAndFlap SF-R4-1 回归：同源根命名空间判定与
// Linux 瞬态 boot_id 不可读抖动。boot_id 与 boottime: 跨源互转绝不判内核
// 重启/计数器归零；boottime: 域内容忍 ±5s 墙钟取整抖动。
func TestTrafficLedgerBootKeyDomainAndFlap(t *testing.T) {
	// a) 根代际域与阈值表驱动断言
	t.Run("boot key domain table", func(t *testing.T) {
		cases := []struct {
			name        string
			prev, cur   string
			rebooted    bool // kernelDefinitelyRebooted
			reset       bool // countersDefinitelyReset
			changed     bool // bootKeyChanged
		}{
			{"boot_id to boottime", "boot-a", "boottime:1700000000", false, false, true},
			{"boottime to boot_id", "boottime:1700000000", "boot-a", false, false, true},
			{"boot_id to boot_id|ns|t", "boot-a", "boot-aaaa|ns=net:[1001]|t=5000", true, true, true},
			{"boottime within tolerance", "boottime:1000", "boottime:1003", false, false, false},
			{"boottime within tolerance reversed", "boottime:1003", "boottime:1000", false, false, false},
			{"boottime beyond tolerance", "boottime:1000", "boottime:1020", true, true, true},
			{"boottime unparsable left", "boottime:bad", "boottime:1000", false, false, true},
			{"boottime unparsable right", "boottime:1000", "boottime:bad", false, false, true},
			{"same boot_id ticks only", "boot-a|t=5000", "boot-a|t=9000", false, false, true},
			{"same key", "boot-a|t=5000", "boot-a|t=5000", false, false, false},
			{"empty prev", "", "boot-a", false, false, false},
			{"empty cur", "boot-a", "", false, false, false},
		}
		for _, tc := range cases {
			if got := kernelDefinitelyRebooted(mustParseRoot(t, tc.prev), mustParseRoot(t, tc.cur)); got != tc.rebooted {
				t.Errorf("%s: kernelDefinitelyRebooted(%q,%q)=%v, want %v", tc.name, tc.prev, tc.cur, got, tc.rebooted)
			}
			if got := countersDefinitelyReset(tc.prev, tc.cur); got != tc.reset {
				t.Errorf("%s: countersDefinitelyReset(%q,%q)=%v, want %v", tc.name, tc.prev, tc.cur, got, tc.reset)
			}
			if got := bootKeyChanged(tc.prev, tc.cur); got != tc.changed {
				t.Errorf("%s: bootKeyChanged(%q,%q)=%v, want %v", tc.name, tc.prev, tc.cur, got, tc.changed)
			}
		}
	})

	if runtime.GOOS != "linux" {
		return // 以下子测试依赖伪 proc stub，仅 Linux 可执行
	}

	// b) Linux 瞬态 boot_id 不可读：保留既有 BootKey，仅计差值，恢复后不双计
	t.Run("transient boot_id unreadable keeps delta accounting", func(t *testing.T) {
		setMonthRotateForTest(t, 13)
		now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) // lastReset=2026-11-13
		proc := writeProcStub(t, "boot-aaaa", 5000, 30*24*3600)
		linkProcNetns(t, proc, "net:[1001]")
		env := newLedgerTestEnv(t, proc, 30*24*3600, true) // 内核长跑 → 冷启 0,0

		// Round1：月度长跑宿主冷启，基线 5000/9000，周期累计 0,0
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 5000, Recv: 9000}}, now)
		if err != nil {
			t.Fatal(err)
		}
		if up != 0 || down != 0 {
			t.Fatalf("long-run cold start got up=%d down=%d, want 0/0", up, down)
		}
		wantBootKey := "boot-aaaa|ns=net:[1001]|t=5000"
		if st := ledgerStateSnapshot(); st.BootKey != wantBootKey {
			t.Fatalf("BootKey = %q, want %q", st.BootKey, wantBootKey)
		}

		// Round2：删除 boot_id 模拟瞬态不可读，输入 5100/9200 —— 仅计差值
		// 100/200，绝不整段灌入 5100，BootKey 保留不被冲掉
		if err := os.Remove(filepath.Join(proc, "sys/kernel/random/boot_id")); err != nil {
			t.Fatal(err)
		}
		up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 5100, Recv: 9200}}, now.Add(10*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 100 || down != 200 {
			t.Fatalf("transient unreadable round got up=%d down=%d, want 100/200", up, down)
		}
		if st := ledgerStateSnapshot(); st.BootKey != wantBootKey {
			t.Fatalf("BootKey after transient unreadable = %q, want %q", st.BootKey, wantBootKey)
		}

		// Round3：恢复 boot_id，输入 5150/9260 —— 同代际差值 150/260，零双倍多计
		if err := os.WriteFile(filepath.Join(proc, "sys/kernel/random/boot_id"), []byte("boot-aaaa\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 5150, Recv: 9260}}, now.Add(20*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 150 || down != 260 {
			t.Fatalf("post-recovery round got up=%d down=%d, want 150/260", up, down)
		}
		if st := ledgerStateSnapshot(); st.BootKey != wantBootKey {
			t.Fatalf("BootKey after recovery = %q, want %q", st.BootKey, wantBootKey)
		}
	})

	// c) 持久化旧 boottime: 代际切入 boot_id 域：同周期只加差值并平滑迁移
	//    BootKey；跨周期（即使年轻 kernelUptime）不误判内核重启播种，归 0,0
	t.Run("boottime to boot_id same cycle adds delta only", func(t *testing.T) {
		setMonthRotateForTest(t, 0)
		now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
		proc := writeProcStub(t, "boot-aaaa", 5000, 1000)
		linkProcNetns(t, proc, "net:[1001]")
		env := newLedgerTestEnv(t, proc, 10, true)
		state := trafficLedgerState{
			CycleUp:   1000,
			CycleDown: 2000,
			CycleKey:  "forever",
			BootKey:   "boottime:1700000000",
			Nics:      map[string]nicBaseline{"eth0": {Tx: 5000, Rx: 9000}},
		}
		data, _ := json.Marshal(state)
		if err := os.WriteFile(env.stateFile, data, 0o644); err != nil {
			t.Fatal(err)
		}
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 5100, Recv: 9200}}, now)
		if err != nil {
			t.Fatal(err)
		}
		if up != 1100 || down != 2200 {
			t.Fatalf("cross-source epoch switch must only add delta, got up=%d down=%d, want 1100/2200", up, down)
		}
		wantBootKey := "boot-aaaa|ns=net:[1001]|t=5000"
		if st := ledgerStateSnapshot(); st.BootKey != wantBootKey {
			t.Fatalf("BootKey did not migrate to boot_id domain, got %q, want %q", st.BootKey, wantBootKey)
		}
		// 迁移后下一拍同代际正常累加
		up, down, _, _, err = updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 5150, Recv: 9260}}, now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if up != 1150 || down != 2260 {
			t.Fatalf("post-migration delta got up=%d down=%d, want 1150/2260", up, down)
		}
	})

	t.Run("boottime to boot_id cross cycle never seeds", func(t *testing.T) {
		setMonthRotateForTest(t, 13)
		now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) // lastReset=2026-11-13
		proc := writeProcStub(t, "boot-aaaa", 5000, 1000)
		linkProcNetns(t, proc, "net:[1001]")
		env := newLedgerTestEnv(t, proc, 10, true) // 年轻 kernelUptime：误判内核重启必播种
		state := trafficLedgerState{
			CycleUp:   1000,
			CycleDown: 2000,
			CycleKey:  "month:13:2026-10-13",
			BootKey:   "boottime:1700000000",
			Nics:      map[string]nicBaseline{"eth0": {Tx: 5000, Rx: 9000}},
		}
		data, _ := json.Marshal(state)
		if err := os.WriteFile(env.stateFile, data, 0o644); err != nil {
			t.Fatal(err)
		}
		up, down, _, _, err := updateTrafficLedger(env, []nicCounterSample{{Name: "eth0", Sent: 5100, Recv: 9200}}, now)
		if err != nil {
			t.Fatal(err)
		}
		if up != 0 || down != 0 {
			t.Fatalf("cross-source cross-cycle must not seed, got up=%d down=%d, want 0/0", up, down)
		}
		wantBootKey := "boot-aaaa|ns=net:[1001]|t=5000"
		if st := ledgerStateSnapshot(); st.BootKey != wantBootKey {
			t.Fatalf("BootKey did not migrate to boot_id domain, got %q, want %q", st.BootKey, wantBootKey)
		}
	})
}

// mustParseRoot 提取表驱动用例的根代际 ID，供 kernelDefinitelyRebooted 断言。
func mustParseRoot(t *testing.T, key string) string {
	t.Helper()
	root, _ := parseBootKey(key)
	return root
}
