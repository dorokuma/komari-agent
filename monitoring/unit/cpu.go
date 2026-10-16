package monitoring

import (
	"bufio"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
	"github.com/shirou/gopsutil/v4/cpu"
)

// Overridable in tests. On a host this is the root cgroup; inside LXC/Docker
// it is the container cgroup. Do not walk into the agent service slice.
var (
	cgroupCPUStatPath    = "/sys/fs/cgroup/cpu.stat"
	cgroupCPUMaxPath     = "/sys/fs/cgroup/cpu.max"
	cpuOnlinePath        = "/sys/devices/system/cpu/online"
	proc1EnvironPath     = "/proc/1/environ"
	procSelfCgroupPath   = "/proc/self/cgroup"
	containerMarkerPaths = []string{"/.dockerenv", "/run/.containerenv"}
	isContainerEnv       = detectContainerEnv
)

const cgroupUsageKey = "usage_usec"

var (
	cgroupMu         sync.Mutex
	lastCgroupUsage  uint64
	lastCgroupSample time.Time
	lastCgroupNCPU   float64
	lastCgroupPct    float64
	haveCgroupSample bool
	haveCgroupPct    bool
)

func cgroupCPUStatAvailable() bool {
	if _, err := readCgroupUsageUsec(cgroupCPUStatPath); err != nil {
		return false
	}
	if isContainerEnv != nil && isContainerEnv() {
		return true
	}
	return hasFiniteCPUMax(cgroupCPUMaxPath)
}

func isContainerCgroupSegment(seg string) bool {
	switch seg {
	case "lxc", "docker", "kubepods", "kubepods.slice", "containerd", "libpod":
		return true
	}
	if strings.HasPrefix(seg, "lxc.payload.") ||
		strings.HasPrefix(seg, "lxc.monitor.") ||
		strings.HasPrefix(seg, "machine-lxc") ||
		strings.HasPrefix(seg, "kubepods-") {
		return true
	}
	if strings.HasSuffix(seg, ".scope") &&
		(strings.HasPrefix(seg, "docker-") ||
			strings.HasPrefix(seg, "libpod-") ||
			strings.HasPrefix(seg, "cri-containerd-") ||
			strings.HasPrefix(seg, "crio-")) {
		return true
	}
	return false
}

func isContainerCgroupPath(cgPath string) bool {
	cgPath = strings.TrimSpace(cgPath)
	if cgPath == "" || cgPath == "/" || cgPath == "/init.scope" {
		return false
	}
	for _, seg := range strings.Split(cgPath, "/") {
		if seg == "" || strings.HasSuffix(seg, ".service") {
			continue
		}
		if isContainerCgroupSegment(seg) {
			return true
		}
	}
	return false
}

func detectContainerEnv() bool {
	for _, p := range containerMarkerPaths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	if data, err := os.ReadFile(proc1EnvironPath); err == nil {
		for _, entry := range strings.FieldsFunc(string(data), func(r rune) bool {
			return r == 0 || r == '\n'
		}) {
			entry = strings.TrimSpace(entry)
			if strings.HasPrefix(entry, "container=") && strings.TrimSpace(strings.TrimPrefix(entry, "container=")) != "" {
				return true
			}
		}
	}
	if data, err := os.ReadFile(procSelfCgroupPath); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "0::") {
				cgPath := strings.TrimSpace(strings.TrimPrefix(line, "0::"))
				if isContainerCgroupPath(cgPath) {
					return true
				}
			}
		}
	}
	return false
}

func hasFiniteCPUMax(maxPath string) bool {
	data, err := os.ReadFile(maxPath)
	if err != nil {
		return false
	}
	ncpu, ok, err := parseCPUMax(string(data))
	return err == nil && ok && ncpu > 0
}

var flags = pkg_flags.GlobalConfig

type CpuInfo struct {
	CPUName          string  `json:"cpu_name"`
	CPUArchitecture  string  `json:"cpu_architecture"`
	CPUCores         int     `json:"cpu_cores"`
	CPUPhysicalCores int     `json:"cpu_physical_cores"`
	CPUUsage         float64 `json:"cpu_usage"`
}

func Cpu() CpuInfo {
	cpuinfo := CpuStaticInfo()

	// LXC often freezes /proc/stat idle (host leak). When cgroup v2 CPU
	// accounting is active (container environment or finite cpu.max quota),
	// never fall back to gopsutil — that path reports 100%.
	if cgroupCPUStatAvailable() {
		if pct, ok := cpuUsageFromCgroup(); ok {
			cpuinfo.CPUUsage = pct
		}
		return cpuinfo
	}

	percentages, err := cpu.Percent(0, false)
	if err == nil && len(percentages) > 0 {
		cpuinfo.CPUUsage = percentages[0]
	}

	return cpuinfo
}

func resetCgroupCPUSample() {
	cgroupMu.Lock()
	defer cgroupMu.Unlock()
	haveCgroupSample = false
	haveCgroupPct = false
	lastCgroupUsage = 0
	lastCgroupNCPU = 0
	lastCgroupPct = 0
	lastCgroupSample = time.Time{}
}

func invalidateCgroupSample() (float64, bool) {
	cgroupMu.Lock()
	defer cgroupMu.Unlock()
	haveCgroupSample = false
	if haveCgroupPct {
		return lastCgroupPct, true
	}
	return 0, false
}

func cpuUsageFromCgroup() (float64, bool) {
	usage, err := readCgroupUsageUsec(cgroupCPUStatPath)
	if err != nil {
		return invalidateCgroupSample()
	}
	ncpu, err := cgroupCPULimit(cgroupCPUMaxPath, cpuOnlinePath)
	if err != nil || ncpu <= 0 {
		return invalidateCgroupSample()
	}

	now := time.Now()
	cgroupMu.Lock()
	defer cgroupMu.Unlock()
	if !haveCgroupSample || usage < lastCgroupUsage || math.Abs(ncpu-lastCgroupNCPU) > 1e-9 {
		if usage < lastCgroupUsage {
			lastCgroupPct = 0
			haveCgroupPct = false
		}
		lastCgroupUsage = usage
		lastCgroupSample = now
		lastCgroupNCPU = ncpu
		haveCgroupSample = true
		if haveCgroupPct {
			return lastCgroupPct, true
		}
		return 0, false
	}
	wall := now.Sub(lastCgroupSample)
	if wall < 200*time.Millisecond {
		if haveCgroupPct {
			return lastCgroupPct, true
		}
		return 0, false
	}
	prev := lastCgroupUsage
	lastCgroupUsage = usage
	lastCgroupSample = now
	lastCgroupNCPU = ncpu
	pct := float64(usage-prev) / float64(wall.Microseconds()) / ncpu * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	lastCgroupPct = pct
	haveCgroupPct = true
	return pct, true
}

func readCgroupUsageUsec(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 || fields[0] != cgroupUsageKey {
			continue
		}
		return strconv.ParseUint(fields[1], 10, 64)
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, os.ErrNotExist
}

func cgroupCPULimit(maxPath, onlinePath string) (float64, error) {
	if data, err := os.ReadFile(maxPath); err == nil {
		if ncpu, ok, err := parseCPUMax(string(data)); err != nil {
			return 0, err
		} else if ok {
			return ncpu, nil
		}
	}
	data, err := os.ReadFile(onlinePath)
	if err != nil {
		return 0, err
	}
	n, err := parseCPUOnline(string(data))
	if err != nil {
		return 0, err
	}
	return float64(n), nil
}

func parseCPUMax(s string) (float64, bool, error) {
	fields := strings.Fields(s)
	if len(fields) < 1 {
		return 0, false, os.ErrInvalid
	}
	if fields[0] == "max" {
		return 0, false, nil
	}
	if len(fields) < 2 {
		return 0, false, os.ErrInvalid
	}
	quota, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false, err
	}
	period, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, false, err
	}
	if period <= 0 || quota < 0 {
		return 0, false, os.ErrInvalid
	}
	return quota / period, true, nil
}

func parseCPUOnline(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, os.ErrInvalid
	}
	n := 0
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i := strings.IndexByte(part, '-'); i >= 0 {
			lo, err := strconv.Atoi(part[:i])
			if err != nil {
				return 0, err
			}
			hi, err := strconv.Atoi(part[i+1:])
			if err != nil {
				return 0, err
			}
			if hi < lo {
				return 0, os.ErrInvalid
			}
			n += hi - lo + 1
			continue
		}
		if _, err := strconv.Atoi(part); err != nil {
			return 0, err
		}
		n++
	}
	if n <= 0 {
		return 0, os.ErrInvalid
	}
	return n, nil
}

func CpuStaticInfo() CpuInfo {
	cpuinfo := CpuInfo{
		CPUName:          "Unknown",
		CPUArchitecture:  runtime.GOARCH,
		CPUCores:         1,
		CPUPhysicalCores: 0, // 为兼容旧版 agent，0 表示未上报或未知，避免与实际核心数混淆
		CPUUsage:         0.0,
	}

	// 优先使用 gopsutil 获取 CPU 信息，避免触发 lscpu 在部分内核上的 lockdown 日志刷屏。
	info, err := cpu.Info()
	if err == nil && len(info) > 0 {
		cpuinfo.CPUName = strings.TrimSpace(info[0].ModelName)
		if cpuinfo.CPUName == "" {
			if info[0].VendorID != "" || info[0].Family != "" {
				cpuinfo.CPUName = strings.TrimSpace(info[0].VendorID + " " + info[0].Family)
			}
		}
	}

	if cpuinfo.CPUName == "Unknown" {
		name, err := readCPUNameFromProc()
		if err == nil && name != "" {
			cpuinfo.CPUName = strings.TrimSpace(name)
		}
	}

	cores, err := cpu.Counts(true)
	if err == nil && cores > 0 {
		cpuinfo.CPUCores = cores
	}

	physicalCores, err := cpu.Counts(false)
	if err == nil && physicalCores > 0 {
		cpuinfo.CPUPhysicalCores = physicalCores
	}

	return cpuinfo
}

// readCPUNameFromProc 从 /proc/cpuinfo 读取 CPU 名称
func readCPUNameFromProc() (string, error) {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Model\t") || strings.HasPrefix(line, "Hardware\t") || strings.HasPrefix(line, "Processor\t") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}

	return "", scanner.Err()
}
