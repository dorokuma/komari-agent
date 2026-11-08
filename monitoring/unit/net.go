package monitoring

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/net"
)

func ConnectionsCount() (tcpCount, udpCount int, err error) {
	if runtime.GOOS == "linux" {
		return connectionsCountWithProcFallback(procRoot(), gopsutilConnectionsCount)
	}

	return gopsutilConnectionsCount()
}

func connectionsCountWithProcFallback(root string, fallback func() (int, int, error)) (tcpCount, udpCount int, err error) {
	var procErr error
	tcpCount, udpCount, procErr = procNetConnectionsCount(root)
	if procErr == nil {
		return tcpCount, udpCount, nil
	}

	tcpCount, udpCount, err = fallback()
	if err != nil && procErr != nil {
		return 0, 0, fmt.Errorf("proc net fast path failed: %w; gopsutil fallback failed: %w", procErr, err)
	}
	return tcpCount, udpCount, err
}

func gopsutilConnectionsCount() (tcpCount, udpCount int, err error) {
	tcps, err := net.Connections("tcp")
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get TCP connections: %w", err)
	}
	udps, err := net.Connections("udp")
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get UDP connections: %w", err)
	}

	return len(tcps), len(udps), nil
}

func procRoot() string {
	if flags.HostProc != "" {
		return flags.HostProc
	}
	return "/proc"
}

func procNetConnectionsCount(root string) (tcpCount, udpCount int, err error) {
	tcpCount, err = countProcNetFiles(root, "tcp", "tcp6")
	if err != nil {
		return 0, 0, err
	}
	udpCount, err = countProcNetFiles(root, "udp", "udp6")
	if err != nil {
		return 0, 0, err
	}
	return tcpCount, udpCount, nil
}

func countProcNetFiles(root string, names ...string) (int, error) {
	total := 0
	readAny := false
	for _, name := range names {
		count, err := countProcNetFile(filepath.Join(root, "net", name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		total += count
		readAny = true
	}
	if !readAny {
		return 0, fmt.Errorf("no proc net files found under %s", filepath.Join(root, "net"))
	}
	return total, nil
}

func countProcNetFile(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	header := true
	for scanner.Scan() {
		if header {
			header = false
			continue
		}
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count, scanner.Err()
}

// defaultSysNetRoot 是生产环境的 sysfs 网卡根目录；单测可注入临时目录构造 sysfs stub。
const defaultSysNetRoot = "/sys/class/net"

// defaultExcludedNicPrefixes 默认排除的虚拟网卡名前缀（大小写不敏感）。
// 仅当未显式配置 --include-nics 时生效。
var defaultExcludedNicPrefixes = []string{
	"lo", "br", "cni", "docker", "podman", "flannel", "cali", "cilium",
	"weave", "kube", "veth", "virbr", "vmbr", "vnet", "tap", "fwbr", "fwpr",
	"fwln", "tun", "utun", "wg", "tailscale", "zt", "dae", "mihomo", "meta",
	"sing", "clash", "warp", "ppp", "sit", "gre", "ip6tnl", "vxlan", "geneve",
	"ifb", "dummy", "lxc", "lxdbr", "bond", "vlan", "macvlan", "ipvlan", "venet",
}

// minimalFallbackNicPrefixes 集合级空集保底时仅排除的已知桥接/容器对端前缀。
var minimalFallbackNicPrefixes = []string{
	"docker", "podman", "cni", "flannel", "cali", "cilium", "weave", "kube",
	"veth", "br", "virbr", "vmbr", "fwbr", "fwpr", "fwln", "ifb", "dummy",
}

// sysfs uevent DEVTYPE 内生虚拟网卡类型
var virtualNicDevTypes = map[string]struct{}{
	"bridge": {}, "bond": {}, "vlan": {}, "wireguard": {},
	"vxlan": {}, "geneve": {}, "dummy": {}, "ifb": {},
}

// sysfs type (ARPHRD) 隧道/虚拟设备类型
var virtualNicSysfsTypes = map[int]struct{}{
	512:   {}, // ARPHRD_PPP
	768:   {}, // ARPHRD_IPGRE / tunnel
	769:   {}, // ARPHRD_IP6GRE / tunnel6
	776:   {}, // ARPHRD_SIT
	778:   {}, // ARPHRD_IPDDP / GRE tunnel (GRE)
	65534: {}, // ARPHRD_NONE 无物理层（隧道/纯虚拟）
}

var warnEmptyNicFilterOnce sync.Once

func isLoopbackNicName(nicName string) bool {
	if nicName == "lo" {
		return true
	}
	// "lo" 后全为数字（如 "lo0"）同样视为回环
	if strings.HasPrefix(nicName, "lo") {
		for _, r := range nicName[2:] {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// isLoopbackNic 第 1 级回环硬拦截：名称或 sysfs type=772 (ARPHRD_LOOPBACK)。
func isLoopbackNic(nicName, sysNetRoot string) bool {
	if isLoopbackNicName(nicName) {
		return true
	}
	if runtime.GOOS == "linux" && nicSysfsType(sysNetRoot, nicName) == 772 {
		return true
	}
	return false
}

func nicSysfsType(sysNetRoot, name string) int {
	data, err := os.ReadFile(filepath.Join(sysNetRoot, name, "type"))
	if err != nil {
		return -1
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return -1
	}
	return v
}

// isVirtualNicBySysfs 通过 sysfs 特征识别 tun/tap、桥接/bond/vlan/隧道等虚拟网卡。
func isVirtualNicBySysfs(nicDir string) bool {
	if _, err := os.Stat(filepath.Join(nicDir, "tun_flags")); err == nil {
		return true
	}
	if data, err := os.ReadFile(filepath.Join(nicDir, "uevent")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			k, v, found := strings.Cut(strings.TrimSpace(line), "=")
			if !found || strings.TrimSpace(k) != "DEVTYPE" {
				continue
			}
			if _, ok := virtualNicDevTypes[strings.TrimSpace(v)]; ok {
				return true
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(nicDir, "type")); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if _, ok := virtualNicSysfsTypes[v]; ok {
				return true
			}
		}
	}
	return false
}

func hasDefaultExcludedPrefix(nicName string, prefixes []string) bool {
	lower := strings.ToLower(nicName)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// isHardwareCandidateNic 用于硬件锚点探测：具备 device 链接、非回环、
// 不命中默认排除前缀、且无 tun_flags/DEVTYPE/type 虚拟特征。
func isHardwareCandidateNic(sysNetRoot, name string) bool {
	if name == "." || name == ".." || isLoopbackNic(name, sysNetRoot) {
		return false
	}
	nicDir := filepath.Join(sysNetRoot, name)
	stat, err := os.Stat(nicDir)
	if err != nil || !stat.IsDir() {
		return false
	}
	if hasDefaultExcludedPrefix(name, defaultExcludedNicPrefixes) {
		return false
	}
	if isVirtualNicBySysfs(nicDir) {
		return false
	}
	if _, err := os.Stat(filepath.Join(nicDir, "device")); err != nil {
		return false
	}
	return true
}

// hasHardwareAnchor 判断整机是否存在至少一块带 device 的硬件/半虚拟化网卡
// （覆盖物理机 PCI/USB 与 KVM virtio/Xen/Hyper-V）。
// 返回 false 表示处于纯容器层（如 Proxmox LXC，含 veth 与 macvlan/ipvlan 模式）。
func hasHardwareAnchor(sysNetRoot string) bool {
	entries, err := os.ReadDir(sysNetRoot)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if isHardwareCandidateNic(sysNetRoot, entry.Name()) {
			return true
		}
	}
	return false
}

// defaultNicFilter 第 4 级默认自动过滤（仅在未配置 --include-nics 时调用）。
// anchorKnown/anchorHasHardware 允许调用方预扫描一次整机锚点，避免逐网卡重复扫描；
// anchorKnown=false 时按需自扫描。
func defaultNicFilter(nicName, sysNetRoot string, anchorKnown, anchorHasHardware bool) bool {
	// a) 默认排除前缀（大小写不敏感）
	if hasDefaultExcludedPrefix(nicName, defaultExcludedNicPrefixes) {
		return false
	}
	// b) 非 Linux 或 sysfs 无该网卡目录（如单测合成名）：前缀未命中即纳入
	if runtime.GOOS != "linux" {
		return true
	}
	nicDir := filepath.Join(sysNetRoot, nicName)
	stat, err := os.Stat(nicDir)
	if err != nil || !stat.IsDir() {
		return true
	}
	// c) sysfs 拓扑识别
	if isVirtualNicBySysfs(nicDir) {
		return false
	}
	if !anchorKnown {
		anchorHasHardware = hasHardwareAnchor(sysNetRoot)
	}
	if anchorHasHardware {
		// 硬件设备主导层：必须具备 device 才纳入，剔除跨命名空间 veth
		// 代理虚拟口（如改名后无 /device 的 dae0）。
		if _, err := os.Stat(filepath.Join(nicDir, "device")); err != nil {
			return false
		}
	}
	// 纯容器层：通过前缀、tun_flags、DEVTYPE 与 type 检查即纳入，
	// 不误杀 macvlan/ipvlan 容器主口或 IPoIB。
	return true
}

// shouldIncludeWithSysfs 判断网卡是否纳入流量统计，sysNetRoot 参数化供单测注入。
// 优先级：回环硬拦截 > 显式白名单 > 显式黑名单 > 默认自动过滤。
func shouldIncludeWithSysfs(nicName string, includeNics, excludeNics map[string]struct{}, sysNetRoot string) bool {
	// 第 1 级：回环硬拦截（即便显式白名单命中也排除）
	if isLoopbackNic(nicName, sysNetRoot) {
		return false
	}
	// 第 2 级：显式白名单（--include-nics）
	if len(includeNics) > 0 {
		for pattern := range includeNics {
			if matched, _ := filepath.Match(pattern, nicName); matched {
				return true
			}
		}
		return false
	}
	// 第 3 级：显式黑名单（--exclude-nics）
	for pattern := range excludeNics {
		if matched, _ := filepath.Match(pattern, nicName); matched {
			return false
		}
	}
	// 第 4 级：默认自动过滤
	return defaultNicFilter(nicName, sysNetRoot, false, false)
}

func shouldInclude(nicName string, includeNics, excludeNics map[string]struct{}) bool {
	return shouldIncludeWithSysfs(nicName, includeNics, excludeNics, defaultSysNetRoot)
}

// minimalFallbackInclude 集合级空集保底：仅排除 lo、excludeNics 与已知
// 桥接/容器对端前缀，覆盖罕见 OpenVZ venet0 唯一公网口等场景。
func minimalFallbackInclude(nicName string, excludeNics map[string]struct{}, sysNetRoot string) bool {
	if isLoopbackNic(nicName, sysNetRoot) {
		return false
	}
	for pattern := range excludeNics {
		if matched, _ := filepath.Match(pattern, nicName); matched {
			return false
		}
	}
	if hasDefaultExcludedPrefix(nicName, minimalFallbackNicPrefixes) {
		return false
	}
	return true
}

// filterInterfacesWithSysfs 对网卡名称集合应用白名单/黑名单/自动过滤，
// 并在自动过滤结果为空而输入存在非 lo 接口时回退到最小保底集合（绝不静默上报恒 0）。
func filterInterfacesWithSysfs(names []string, includeNics, excludeNics map[string]struct{}, sysNetRoot string) []string {
	// 整机硬件锚点在自动过滤路径预扫描一次，供逐网卡判定复用。
	anchorKnown := false
	anchorHasHardware := false
	if len(includeNics) == 0 && runtime.GOOS == "linux" {
		anchorHasHardware = hasHardwareAnchor(sysNetRoot)
		anchorKnown = true
	}

	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if isLoopbackNic(name, sysNetRoot) {
			continue
		}
		if len(includeNics) > 0 {
			matched := false
			for pattern := range includeNics {
				if ok, _ := filepath.Match(pattern, name); ok {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		} else {
			excluded := false
			for pattern := range excludeNics {
				if ok, _ := filepath.Match(pattern, name); ok {
					excluded = true
					break
				}
			}
			if excluded || !defaultNicFilter(name, sysNetRoot, anchorKnown, anchorHasHardware) {
				continue
			}
		}
		filtered = append(filtered, name)
	}

	// 集合级空集安全保底
	if len(filtered) == 0 && len(includeNics) == 0 {
		hasNonLoopback := false
		for _, name := range names {
			if !isLoopbackNic(name, sysNetRoot) {
				hasNonLoopback = true
				break
			}
		}
		if hasNonLoopback {
			for _, name := range names {
				if minimalFallbackInclude(name, excludeNics, sysNetRoot) {
					filtered = append(filtered, name)
				}
			}
			if len(filtered) > 0 {
				warnEmptyNicFilterOnce.Do(func() {
					log.Printf("[WARN] automatic NIC filter matched nothing; falling back to minimal set %v. Configure --include-nics to pin uplinks.", filtered)
				})
			}
		}
	}
	return filtered
}

// VnstatInterface represents a network interface in vnstat output
type VnstatInterface struct {
	Name    string        `json:"name"`
	Alias   string        `json:"alias"`
	Created VnstatDate    `json:"created"`
	Updated VnstatUpdated `json:"updated"`
	Traffic VnstatTraffic `json:"traffic"`
}

// VnstatDate represents date information
type VnstatDate struct {
	Date      VnstatDateInfo `json:"date"`
	Timestamp int64          `json:"timestamp"`
}

// VnstatUpdated represents updated information
type VnstatUpdated struct {
	Date      VnstatDateInfo `json:"date"`
	Time      VnstatTimeInfo `json:"time"`
	Timestamp int64          `json:"timestamp"`
}

// VnstatDateInfo represents date components
type VnstatDateInfo struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// VnstatTimeInfo represents time components
type VnstatTimeInfo struct {
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
}

// VnstatTraffic represents traffic data from vnstat
type VnstatTraffic struct {
	Total      VnstatTotal        `json:"total"`
	FiveMinute []VnstatTimeEntry  `json:"fiveminute"`
	Hour       []VnstatTimeEntry  `json:"hour"`
	Day        []VnstatTimeEntry  `json:"day"`
	Month      []VnstatMonthEntry `json:"month"`
	Year       []VnstatYearEntry  `json:"year"`
	Top        []VnstatTimeEntry  `json:"top"`
}

// VnstatTotal represents total traffic data
type VnstatTotal struct {
	Rx uint64 `json:"rx"`
	Tx uint64 `json:"tx"`
}

// VnstatTimeEntry represents a time-based traffic entry
type VnstatTimeEntry struct {
	ID        int            `json:"id"`
	Date      VnstatDateInfo `json:"date"`
	Time      VnstatTimeInfo `json:"time,omitempty"`
	Timestamp int64          `json:"timestamp"`
	Rx        uint64         `json:"rx"`
	Tx        uint64         `json:"tx"`
}

// VnstatMonthEntry represents a monthly traffic entry
type VnstatMonthEntry struct {
	ID        int            `json:"id"`
	Date      VnstatDateInfo `json:"date"`
	Timestamp int64          `json:"timestamp"`
	Rx        uint64         `json:"rx"`
	Tx        uint64         `json:"tx"`
}

// VnstatYearEntry represents a yearly traffic entry
type VnstatYearEntry struct {
	ID        int            `json:"id"`
	Date      VnstatDateInfo `json:"date"`
	Timestamp int64          `json:"timestamp"`
	Rx        uint64         `json:"rx"`
	Tx        uint64         `json:"tx"`
}

// VnstatOutput represents the complete vnstat JSON output
type VnstatOutput struct {
	VnstatVersion string            `json:"vnstatversion"`
	JsonVersion   string            `json:"jsonversion"`
	Interfaces    []VnstatInterface `json:"interfaces"`
}

func NetworkSpeed() (totalUp, totalDown, upSpeed, downSpeed uint64, err error) {
	includeNics := parseNics(flags.IncludeNics)
	excludeNics := parseNics(flags.ExcludeNics)

	ioCounters, err := net.IOCounters(true)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("failed to get network IO counters: %w", err)
	}
	if len(ioCounters) == 0 {
		return 0, 0, 0, 0, fmt.Errorf("no network interfaces found")
	}

	names := make([]string, 0, len(ioCounters))
	for _, interfaceStats := range ioCounters {
		names = append(names, interfaceStats.Name)
	}
	filtered := filterInterfacesWithSysfs(names, includeNics, excludeNics, defaultSysNetRoot)
	allowed := make(map[string]struct{}, len(filtered))
	for _, name := range filtered {
		allowed[name] = struct{}{}
	}

	samples := make([]nicCounterSample, 0, len(filtered))
	for _, interfaceStats := range ioCounters {
		if _, ok := allowed[interfaceStats.Name]; !ok {
			continue
		}
		samples = append(samples, nicCounterSample{
			Name: interfaceStats.Name,
			Sent: interfaceStats.BytesSent,
			Recv: interfaceStats.BytesRecv,
		})
	}

	env := defaultLedgerEnv(includeNics, excludeNics)
	totalUp, totalDown, upSpeed, downSpeed, err = updateTrafficLedger(env, samples, time.Now())
	if err != nil {
		return totalUp, totalDown, upSpeed, downSpeed, err
	}
	totalUp, totalDown = applyTrafficOffset(totalUp, totalDown)
	return totalUp, totalDown, upSpeed, downSpeed, nil
}

func getNetworkSpeedFallback(includeNics, excludeNics map[string]struct{}) (totalUp, totalDown, upSpeed, downSpeed uint64, err error) {
	totalUp, totalDown, err = collectNetworkTotals(includeNics, excludeNics)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	upSpeed, downSpeed = updateNetworkSpeedSample(totalUp, totalDown, time.Now())
	return totalUp, totalDown, upSpeed, downSpeed, nil
}

func collectNetworkTotals(includeNics, excludeNics map[string]struct{}) (totalUp, totalDown uint64, err error) {
	ioCounters, err := net.IOCounters(true)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get network IO counters: %w", err)
	}

	if len(ioCounters) == 0 {
		return 0, 0, fmt.Errorf("no network interfaces found")
	}

	names := make([]string, 0, len(ioCounters))
	for _, interfaceStats := range ioCounters {
		names = append(names, interfaceStats.Name)
	}
	allowed := make(map[string]struct{}, len(names))
	for _, name := range filterInterfacesWithSysfs(names, includeNics, excludeNics, defaultSysNetRoot) {
		allowed[name] = struct{}{}
	}
	for _, interfaceStats := range ioCounters {
		if _, ok := allowed[interfaceStats.Name]; ok {
			totalUp += interfaceStats.BytesSent
			totalDown += interfaceStats.BytesRecv
		}
	}

	return totalUp, totalDown, nil
}

type networkSpeedState struct {
	sync.Mutex
	totalUp   uint64
	totalDown uint64
	sampledAt time.Time
}

var networkSpeedSample networkSpeedState

func updateNetworkSpeedSample(totalUp, totalDown uint64, now time.Time) (upSpeed, downSpeed uint64) {
	networkSpeedSample.Lock()
	defer networkSpeedSample.Unlock()

	if networkSpeedSample.sampledAt.IsZero() {
		networkSpeedSample.totalUp = totalUp
		networkSpeedSample.totalDown = totalDown
		networkSpeedSample.sampledAt = now
		return 0, 0
	}

	elapsed := now.Sub(networkSpeedSample.sampledAt).Seconds()
	if elapsed <= 0 {
		return 0, 0
	}

	upDelta := safeCounterDelta(totalUp, networkSpeedSample.totalUp)
	downDelta := safeCounterDelta(totalDown, networkSpeedSample.totalDown)

	networkSpeedSample.totalUp = totalUp
	networkSpeedSample.totalDown = totalDown
	networkSpeedSample.sampledAt = now

	return uint64(float64(upDelta) / elapsed), uint64(float64(downDelta) / elapsed)
}

func safeCounterDelta(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	return 0
}

func parseNics(nics string) map[string]struct{} {
	if nics == "" {
		return nil
	}
	nicSet := make(map[string]struct{})
	for _, nic := range strings.Split(nics, ",") {
		nicSet[strings.TrimSpace(nic)] = struct{}{}
	}
	return nicSet
}

func InterfaceList() ([]string, error) {
	includeNics := parseNics(flags.IncludeNics)
	excludeNics := parseNics(flags.ExcludeNics)

	ioCounters, err := net.IOCounters(true)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ioCounters))
	for _, interfaceStats := range ioCounters {
		names = append(names, interfaceStats.Name)
	}
	return filterInterfacesWithSysfs(names, includeNics, excludeNics, defaultSysNetRoot), nil
}
