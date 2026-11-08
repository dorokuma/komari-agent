package monitoring

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari-agent/utils"
	"github.com/shirou/gopsutil/v4/host"
)

const (
	trafficLedgerStateFileName    = "traffic-state.json"
	trafficLedgerSaveInterval     = 30 * time.Second
	trafficLedgerErrLogInterval   = 60 * time.Second
	procClockTicksPerSecond       = 100 // Linux USER_HZ（/proc 内核计数统一按 100Hz）
	boottimeJitterToleranceSec uint64 = 5  // 非 Linux boottime 兜底域 now-uptime 墙钟取整抖动容差
)

// nicCounterSample 单块网卡的原始内核单调计数
type nicCounterSample struct {
	Name string
	Sent uint64
	Recv uint64
}

// nicBaseline 单块网卡的计数基线
type nicBaseline struct {
	Tx uint64 `json:"tx"`
	Rx uint64 `json:"rx"`
}

// trafficLedgerState 持久化账本：周期累计 + 代际标识 + 各网卡基线
type trafficLedgerState struct {
	CycleUp   uint64                 `json:"cycle_up"`
	CycleDown uint64                 `json:"cycle_down"`
	CycleKey  string                 `json:"cycle_key"`
	BootKey   string                 `json:"boot_key"`
	Nics      map[string]nicBaseline `json:"nics"`
}

// ledgerEnv 账本一次步进所需的环境依赖，默认由生产路径装配，单测可注入。
type ledgerEnv struct {
	stateFile         string
	procRoot          string
	sysNetRoot        string
	includeNics       map[string]struct{}
	excludeNics       map[string]struct{}
	exeDir            string
	kernelUptime      int64
	hasKernelUptime   bool
}

// trafficLedger 全生命周期由 ledgerMu 保护的内存账本
type trafficLedger struct {
	stateFile           string
	state               trafficLedgerState
	loaded              bool
	initialized         bool
	pendingLegacyRename string
	haveSample          bool
	lastSampleAt        time.Time
	lastFlush           time.Time
	lastErrLog          time.Time
	roundDeltaUp        uint64
	roundDeltaDown      uint64
}

var (
	ledgerMu     sync.Mutex
	ledger       trafficLedger
	exeDirOnce   sync.Once
	exeDirCached string
)

func defaultLedgerEnv(includeNics, excludeNics map[string]struct{}) ledgerEnv {
	uptime, ok := kernelUptimeSeconds(procRoot())
	return ledgerEnv{
		stateFile:       trafficLedgerStateFile(),
		procRoot:        procRoot(),
		sysNetRoot:      defaultSysNetRoot,
		includeNics:     includeNics,
		excludeNics:     excludeNics,
		exeDir:          executableDir(),
		kernelUptime:    uptime,
		hasKernelUptime: ok,
	}
}

func executableDir() string {
	exeDirOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		exeDirCached = filepath.Dir(exe)
	})
	return exeDirCached
}

// trafficLedgerStateFile 账本状态文件路径：显式 --traffic-state-file 优先，
// 否则固定为 <可执行文件解析软链后所在目录>/traffic-state.json，不回退随机 cwd。
func trafficLedgerStateFile() string {
	if flags.TrafficStateFile != "" {
		return flags.TrafficStateFile
	}
	dir := executableDir()
	if dir == "" {
		return trafficLedgerStateFileName
	}
	return filepath.Join(dir, trafficLedgerStateFileName)
}

// currentCycleKey 当前周期标识：MonthRotate==0 或越界值视为 forever；
// 1..31 按夹紧后的上次重置日生成 month:<day>:<YYYY-MM-DD>。
func currentCycleKey(now time.Time) string {
	day := flags.MonthRotate
	if day < 1 || day > 31 {
		return "forever"
	}
	last := utils.GetLastResetDate(day, now)
	return fmt.Sprintf("month:%d:%s", day, last.Format("2006-01-02"))
}

// proc1StarttimeTicks 解析 <procRoot>/1/stat 的 starttime（第 22 字段）。
// 先定位最后一个 ')' 再切分，防 comm 含空格或括号错位。返回 0 表示不可解析。
func proc1StarttimeTicks(proc string) uint64 {
	data, err := os.ReadFile(filepath.Join(proc, "1", "stat"))
	if err != nil {
		return 0
	}
	s := string(data)
	idx := strings.LastIndexByte(s, ')')
	if idx < 0 || idx+2 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[idx+1:])
	// fields[0] = 第 3 字段 state，第 22 字段 starttime 对应 fields[19]
	if len(fields) < 20 {
		return 0
	}
	v, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// systemBootKey 内核单调代际标识：boot_id + netns 符号链接 + PID1 starttime。
// 宿主机重启变 boot_id，LXC/容器重建全新 netns 变 1/ns/net 链接目标，
// 同 netns 重启（如 docker restart）仅变 PID1 starttime，仅重启 agent 全不变。
// Linux 下严格绑定传入的 proc 根（含 --host-proc/测试 procRoot）：boot_id
// 瞬态不可读直接返回空串（由 stepLocked 保留既有代际标识），绝不穿透调用
// host.BootTime()——后者固定读全局 /proc/stat，既无视 --host-proc 又会用
// boottime: 冲掉 boot_id|ns=... 标识。仅非 Linux 平台回退 gopsutil
// host.BootTime() 生成 boottime:<sec>，仍失败返回空串。
func systemBootKey(proc string) string {
	if runtime.GOOS == "linux" {
		id, err := os.ReadFile(filepath.Join(proc, "sys/kernel/random/boot_id"))
		if err != nil || strings.TrimSpace(string(id)) == "" {
			return ""
		}
		bootID := strings.TrimSpace(string(id))
		netns := ""
		if link, err := os.Readlink(filepath.Join(proc, "1", "ns", "net")); err == nil {
			netns = strings.TrimSpace(link)
		}
		ticks := proc1StarttimeTicks(proc)
		switch {
		case netns != "" && ticks > 0:
			return fmt.Sprintf("%s|ns=%s|t=%d", bootID, netns, ticks)
		case netns != "":
			return fmt.Sprintf("%s|ns=%s", bootID, netns)
		case ticks > 0:
			return fmt.Sprintf("%s|t=%d", bootID, ticks)
		default:
			return bootID
		}
	}
	if bt, err := host.BootTime(); err == nil && bt > 0 {
		return fmt.Sprintf("boottime:%d", bt)
	}
	return ""
}

// parseBootKey 解析代际标识为 (根代际 ID, netns 标识)。
// 兼容历史 "boot_id:ticks" 与新 "boot_id|ns=...|t=..." 及 "boottime:..." 三种格式：
// 先按 '|' 切首段（"boottime:..." 整段保留以保证内核重启可区分），
// 不含 '|' 且非 "boottime:" 前缀的历史格式按首个 ':' 切出 rootID。
func parseBootKey(key string) (rootID, netns string) {
	if idx := strings.IndexByte(key, '|'); idx >= 0 {
		rootID = key[:idx]
		for _, seg := range strings.Split(key[idx+1:], "|") {
			if v, ok := strings.CutPrefix(seg, "ns="); ok {
				netns = v
			}
		}
		return rootID, netns
	}
	if strings.HasPrefix(key, "boottime:") {
		return key, ""
	}
	if idx := strings.IndexByte(key, ':'); idx >= 0 {
		return key[:idx], ""
	}
	return key, ""
}

// boottimeSeconds 解析 boottime:<sec> 形式代际标识的秒数。
func boottimeSeconds(root string) (uint64, bool) {
	v, ok := strings.CutPrefix(root, "boottime:")
	if !ok {
		return 0, false
	}
	s, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return s, true
}

func absDiffU64(a, b uint64) uint64 {
	if a >= b {
		return a - b
	}
	return b - a
}

// bootKeyChanged 判定代际标识是否发生「需要进入代际切换处理」的变化：
// 任一侧为空（瞬态不可读/冷启空串）或相等返回 false；boottime: 域内仅
// 当两侧可解析且绝对差 > boottimeJitterToleranceSec 时才判变化（容忍非
// Linux now-uptime 墙钟取整 ±1~2s 抖动，避免单拍速率归零、频繁落盘与
// 基准漂移）；其余不等一律视为变化，由 countersDefinitelyReset 决定
// 切换路径（整段累加或逐卡差值）。
func bootKeyChanged(prevKey, curKey string) bool {
	if prevKey == "" || curKey == "" || prevKey == curKey {
		return false
	}
	prevBt, prevOk := boottimeSeconds(prevKey)
	curBt, curOk := boottimeSeconds(curKey)
	if prevOk && curOk {
		return absDiffU64(prevBt, curBt) > boottimeJitterToleranceSec
	}
	return true
}

// kernelDefinitelyRebooted 判定根代际 ID 是否已证实跨越内核重启：仅允许
// 同源根命名空间比较——boottime: 与 boot_id 跨源互转一律返回 false；同为
// boottime: 时需两侧可解析且绝对差 > boottimeJitterToleranceSec（同样容忍
// 非 Linux 墙钟取整抖动，任一解析失败不判重启）；同为 boot_id 域按不等判定。
func kernelDefinitelyRebooted(prevRoot, curRoot string) bool {
	if prevRoot == "" || curRoot == "" {
		return false
	}
	_, prevIsBt := strings.CutPrefix(prevRoot, "boottime:")
	_, curIsBt := strings.CutPrefix(curRoot, "boottime:")
	if prevIsBt != curIsBt {
		// boottime: 与 boot_id 跨源互转：根代际来源不同，绝不判内核重启
		return false
	}
	if prevIsBt {
		prevBt, prevOk := boottimeSeconds(prevRoot)
		curBt, curOk := boottimeSeconds(curRoot)
		if !prevOk || !curOk {
			return false
		}
		return absDiffU64(prevBt, curBt) > boottimeJitterToleranceSec
	}
	return prevRoot != curRoot
}

// countersDefinitelyReset 判定跨代际切换中网卡单调计数是否必然归零：
// 根代际 ID 同源且已证实内核重启（boot_id 变化或 boottime 差超容差），
// 或同一内核下明确新建 netns（如 LXC 重建）时返回 true；boot_id 与
// boottime: 跨源互转、仅 PID1 ticks 变化而 netns 保留或不可读
// （如 docker restart、--net=host 容器重启）返回 false，计数器可能延续。
func countersDefinitelyReset(prevKey, curKey string) bool {
	prevRoot, prevNetns := parseBootKey(prevKey)
	curRoot, curNetns := parseBootKey(curKey)
	if prevRoot == "" || curRoot == "" {
		return false
	}
	if strings.HasPrefix(prevRoot, "boottime:") != strings.HasPrefix(curRoot, "boottime:") {
		// 跨源互转不可证明计数器归零，按逐卡差值处理
		return false
	}
	if kernelDefinitelyRebooted(prevRoot, curRoot) {
		return true
	}
	return prevRoot == curRoot && prevNetns != "" && curNetns != "" && prevNetns != curNetns
}

// kernelUptimeSeconds 内核单调运行时长：Linux 下仅读取 <proc>/uptime 首字段
// 秒数（共享内核场景即宿主 uptime，容器经 lxcfs 虚拟化时为虚拟化值；均不混合
// PID1 时钟域），读取失败直接返回 false，绝不穿透调用 host.Uptime()（后者
// 固定读全局 /proc，无视 --host-proc/测试 procRoot）。仅非 Linux 平台回退
// gopsutil host.Uptime()，仍失败返回 false。
func kernelUptimeSeconds(proc string) (int64, bool) {
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile(filepath.Join(proc, "uptime"))
		if err != nil {
			return 0, false
		}
		fields := strings.Fields(string(data))
		if len(fields) >= 1 {
			if uptime, err := strconv.ParseFloat(fields[0], 64); err == nil && uptime >= 0 {
				return int64(uptime), true
			}
		}
		return 0, false
	}
	if up, err := host.Uptime(); err == nil {
		return int64(up), true
	}
	return 0, false
}

// newNetnsCounterAgeSeconds 同内核新建独立虚拟容器 netns 场景的代际运行
// 时长：kernelUptime - PID1 starttime 折算秒数。仅用于「同 boot_id、netns
// 明确变化、无硬件锚点」的已证实容器重建场景，不得用于 --net=host 或
// netns 来历不明的冷启/跨周期分支。ticks 不可解析或时钟域错配（lxcfs 已
// 虚拟化 uptime 致 PID1 年龄换算值超过 kernelUptime）时保守按整个
// kernelUptime 计（视为跨周期长跑，不播种）。
func newNetnsCounterAgeSeconds(env ledgerEnv) (int64, bool) {
	if !env.hasKernelUptime {
		return 0, false
	}
	ticks := proc1StarttimeTicks(env.procRoot)
	if ticks > 0 && int64(ticks/procClockTicksPerSecond) <= env.kernelUptime {
		return env.kernelUptime - int64(ticks/procClockTicksPerSecond), true
	}
	return env.kernelUptime, true
}

// legacyNetStaticFile 旧 net_static.json 数据结构（兼容 monitoring/netstatic 落盘格式）
type legacyNetStaticFile struct {
	Interfaces map[string][]struct {
		Timestamp uint64 `json:"timestamp"`
		Tx        uint64 `json:"tx"`
		Rx        uint64 `json:"rx"`
	} `json:"interfaces"`
}

// sumLegacyNetStatic 汇总旧 net_static.json 中 timestamp >= minTs 且通过过滤的
// 网卡 Tx/Rx，作为月度周期初始累计。读不到或解析失败返回 false。
// 过滤走集合级 filterInterfacesWithSysfs，使空集保底（如 OpenVZ venet0）
// 与 NetworkSpeed/InterfaceList 行为完全一致。
func sumLegacyNetStatic(path string, minTs uint64, includeNics, excludeNics map[string]struct{}, sysNetRoot string) (up, down uint64, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return 0, 0, false
	}
	var legacy legacyNetStaticFile
	if err := json.Unmarshal(data, &legacy); err != nil {
		return 0, 0, false
	}
	names := make([]string, 0, len(legacy.Interfaces))
	for name := range legacy.Interfaces {
		names = append(names, name)
	}
	allowed := make(map[string]struct{}, len(names))
	for _, name := range filterInterfacesWithSysfs(names, includeNics, excludeNics, sysNetRoot) {
		allowed[name] = struct{}{}
	}
	found := false
	for name, arr := range legacy.Interfaces {
		if _, ok := allowed[name]; !ok {
			continue
		}
		for _, td := range arr {
			if td.Timestamp >= minTs {
				up = addUint64Saturating(up, td.Tx)
				down = addUint64Saturating(down, td.Rx)
				found = true
			}
		}
	}
	return up, down, found
}

func (l *trafficLedger) persistLocked(now time.Time) {
	if err := persistTrafficLedgerAtomic(l.stateFile, &l.state); err != nil {
		l.maybeLogPersistErr(err, now)
		return
	}
	l.lastFlush = now
	// 新状态落盘成功后重命名旧文件防重入
	if l.pendingLegacyRename != "" {
		_ = os.Rename(l.pendingLegacyRename, l.pendingLegacyRename+".migrated")
		l.pendingLegacyRename = ""
	}
}

func (l *trafficLedger) maybeLogPersistErr(err error, now time.Time) {
	if now.Sub(l.lastErrLog) >= trafficLedgerErrLogInterval {
		l.lastErrLog = now
		log.Printf("[ERROR] traffic ledger persist failed (memory ledger continues): %v", err)
	}
}

// persistTrafficLedgerAtomic 同目录临时文件 + Sync + os.Rename 原子替换
func persistTrafficLedgerAtomic(path string, state *trafficLedgerState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".traffic-state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(e error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return e
	}
	data, err := json.Marshal(state)
	if err != nil {
		return cleanup(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// loadTrafficLedgerLocked 读入状态文件并判定是否为「内容/schema 非法的常规文件」。
// corruptRegular 仅在文件确实存在、确为常规文件（经 Lstat 排除目录/软链等）
// 且 JSON 内容损坏或缺 cycle_key 时返回 true；路径不存在、权限拒绝、
// 非常规文件等一律返回 false，调用方据此决定是否保留 .corrupt 现场。
func loadTrafficLedgerLocked(l *trafficLedger) (corruptRegular bool, err error) {
	fi, err := os.Lstat(l.stateFile)
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, fmt.Errorf("state path is not a regular file: %s", fi.Mode())
	}
	data, err := os.ReadFile(l.stateFile)
	if err != nil {
		return false, err
	}
	var state trafficLedgerState
	if err := json.Unmarshal(data, &state); err != nil {
		return true, err
	}
	if state.CycleKey == "" {
		// 误指非账本 JSON（如空 {}）：同属内容非法，备份后全新初始化
		return true, errors.New("missing cycle_key in ledger state")
	}
	if state.Nics == nil {
		state.Nics = make(map[string]nicBaseline)
	}
	l.state = state
	l.loaded = true
	return false, nil
}

// updateTrafficLedger 步进账本：先判代际（周期/启动），再算增量。
// 返回周期累计（未叠加静态 offset）与基于本轮原始单调正增量的实时速率。
func updateTrafficLedger(env ledgerEnv, samples []nicCounterSample, now time.Time) (totalUp, totalDown, upSpeed, downSpeed uint64, err error) {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()

	l := &ledger
	if !l.initialized {
		l.stateFile = env.stateFile
		corruptRegular, lerr := loadTrafficLedgerLocked(l)
		switch {
		case lerr == nil:
		case corruptRegular:
			// 仅内容/schema 非法的常规文件保留现场：尽力改名 .corrupt
			// 供排查，随后按全新账本初始化。目录、软链、权限拒绝或
			// 路径不存在等非本账本自有常规文件绝不做 os.Rename。
			if rerr := os.Rename(l.stateFile, l.stateFile+".corrupt"); rerr != nil {
				log.Printf("[WARN] failed to load traffic ledger state %s: %v (rename to .corrupt failed: %v); starting fresh", l.stateFile, lerr, rerr)
			} else {
				log.Printf("[WARN] failed to load traffic ledger state %s: %v; preserved as .corrupt and starting fresh", l.stateFile, lerr)
			}
		case errors.Is(lerr, os.ErrNotExist):
			// 首次运行无状态文件，静默冷启动
		default:
			log.Printf("[WARN] failed to load traffic ledger state %s: %v; starting fresh in memory", l.stateFile, lerr)
		}
		l.initialized = true
	}

	l.stepLocked(env, samples, now)

	// 实时速率：本轮原始网卡单调正增量 / 真实采样时间差
	var elapsed float64
	if l.haveSample {
		elapsed = now.Sub(l.lastSampleAt).Seconds()
	}
	l.lastSampleAt = now
	l.haveSample = true
	deltaUp, deltaDown := l.roundDeltaUp, l.roundDeltaDown
	if elapsed > 0 {
		upSpeed = uint64(float64(deltaUp) / elapsed)
		downSpeed = uint64(float64(deltaDown) / elapsed)
	}
	return l.state.CycleUp, l.state.CycleDown, upSpeed, downSpeed, nil
}

// stepLocked 核心步进（调用方必须持有 ledgerMu）：
// 冷启动初始化 → 周期切换 → 代际切换 → 常规增量 → 节流落盘。
func (l *trafficLedger) stepLocked(env ledgerEnv, samples []nicCounterSample, now time.Time) {
	l.roundDeltaUp, l.roundDeltaDown = 0, 0

	if !l.loaded {
		l.coldInitLocked(env, samples, now)
		l.loaded = true
		l.persistLocked(now)
		return
	}

	curCycleKey := currentCycleKey(now)
	curBootKey := systemBootKey(env.procRoot)

	// 周期切换：常规归零重设基线；以下场景改用当前计数播种，防新周期已跑
	// 流量丢失：a) 切回 forever（如月度改永久累计）；b) 内核重启且重启晚于
	// 新周期起点；c) 同内核新建虚拟 netns（无宿主机硬件锚点）且容器代际
	// 启动晚于新周期起点。其余跨周期分支（含同 netns 的 docker restart、
	// --net=host 容器重建等）一律归零，仅把当前计数当基线，绝不把旧周期
	// 长跑计数灌入新周期。
	if l.state.CycleKey != "" && l.state.CycleKey != curCycleKey {
		if curCycleKey == "forever" {
			l.state.CycleUp, l.state.CycleDown = sumSamples(samples)
		} else {
			// 先清零旧周期累计：未归零网卡不再赋值，须彻底抹掉上一周期残留
			l.state.CycleUp, l.state.CycleDown = 0, 0
			day := flags.MonthRotate
			if day >= 1 && day <= 31 && bootKeyChanged(l.state.BootKey, curBootKey) {
				prevRoot, prevNetns := parseBootKey(l.state.BootKey)
				curRoot, curNetns := parseBootKey(curBootKey)
				lastResetUnix := utils.GetLastResetDate(day, now).Unix()
				switch {
				case kernelDefinitelyRebooted(prevRoot, curRoot):
					// 内核重启：仅当内核重启晚于新周期起点时，新周期内才
					// 可能已跑流量，播种当前计数
					if env.hasKernelUptime && now.Unix()-env.kernelUptime >= lastResetUnix {
						l.state.CycleUp, l.state.CycleDown = sumSamples(samples)
					}
				case prevRoot != "" && prevRoot == curRoot &&
					prevNetns != "" && curNetns != "" && prevNetns != curNetns &&
					!hasHardwareAnchor(env.sysNetRoot):
					// 同内核新建独立虚拟容器 netns：按容器代际运行时长
					// （kernelUptime - PID1 年龄）保守判定，仅当代际启动晚于
					// 新周期起点时播种
					if age, ok := newNetnsCounterAgeSeconds(env); ok && now.Unix()-age >= lastResetUnix {
						l.state.CycleUp, l.state.CycleDown = sumSamples(samples)
					}
				default:
					// 同 netns 重启、--net=host 重建、netns 来历不明等：
					// 计数器是否归零不可证，保持 0,0 重设基线
				}
			}
		}
		l.state.Nics = baselineFromSamples(samples)
		l.state.CycleKey = curCycleKey
		if curBootKey != "" && (l.state.BootKey == "" || bootKeyChanged(l.state.BootKey, curBootKey)) {
			l.state.BootKey = curBootKey
		}
		l.persistLocked(now)
		return
	}

	// 重启/容器重建代际切换：
	// 计数器必然归零（内核重启或新建 netns）时整段累加当前计数；
	// 同内核且 netns 保留或不可读（如 docker restart、--net=host 重启）时
	// 计数器可能延续，逐网卡对照基线累加真实差值，防重复计入长跑计数；
	// 未见基线的网卡（中途重现/新纳入）一律只记基线，与其它同内核路径一致。
	if bootKeyChanged(l.state.BootKey, curBootKey) {
		if countersDefinitelyReset(l.state.BootKey, curBootKey) {
			for _, s := range samples {
				l.state.CycleUp = addUint64Saturating(l.state.CycleUp, s.Sent)
				l.state.CycleDown = addUint64Saturating(l.state.CycleDown, s.Recv)
			}
		} else {
			for _, s := range samples {
				prev, seen := l.state.Nics[s.Name]
				if seen {
					if s.Sent >= prev.Tx {
						l.state.CycleUp = addUint64Saturating(l.state.CycleUp, s.Sent-prev.Tx)
					} else {
						// 同内核下计数器实际归零：累加当前值
						l.state.CycleUp = addUint64Saturating(l.state.CycleUp, s.Sent)
					}
					if s.Recv >= prev.Rx {
						l.state.CycleDown = addUint64Saturating(l.state.CycleDown, s.Recv-prev.Rx)
					} else {
						l.state.CycleDown = addUint64Saturating(l.state.CycleDown, s.Recv)
					}
				}
				// 未见基线的网卡（中途重现或新纳入）：仅经 baselineFromSamples
				// 记基线，杜绝与消失网卡基线剔除交互产生历史流量重复入账
			}
		}
		l.state.Nics = baselineFromSamples(samples)
		l.state.BootKey = curBootKey
		l.persistLocked(now)
		return
	}

	// 同代际常规增量：逐网卡基于旧基线累加，并把本轮仍存在的网卡写入
	// nextNics——已消失网卡的陈旧基线随本轮剔除，防同名重建沿用旧基线
	// 产生伪增量。本轮无样本（如 /proc/net/dev 瞬态不可读）时不清空基线。
	if len(samples) > 0 {
		nextNics := make(map[string]nicBaseline, len(samples))
		for _, s := range samples {
			prev, seen := l.state.Nics[s.Name]
			if seen {
				if s.Sent >= prev.Tx {
					d := s.Sent - prev.Tx
					l.state.CycleUp = addUint64Saturating(l.state.CycleUp, d)
					l.roundDeltaUp = addUint64Saturating(l.roundDeltaUp, d)
				}
				// s.Sent < prev.Tx：同代际网卡驱动重置，增量按 0
				if s.Recv >= prev.Rx {
					d := s.Recv - prev.Rx
					l.state.CycleDown = addUint64Saturating(l.state.CycleDown, d)
					l.roundDeltaDown = addUint64Saturating(l.roundDeltaDown, d)
				}
			}
			// 中途新出现的合规网卡：首观测只记基线
			nextNics[s.Name] = nicBaseline{Tx: s.Sent, Rx: s.Recv}
		}
		l.state.Nics = nextNics
	}
	if l.state.CycleKey != curCycleKey {
		l.state.CycleKey = curCycleKey
	}
	// /proc 瞬态不可读时 curBootKey 为空串，不冲掉既有合法代际标识；
	// 仅冷启空串恢复后采纳当前标识（boottime: 容差内抖动不改写初始基准）
	if curBootKey != "" && l.state.BootKey == "" {
		l.state.BootKey = curBootKey
	}
	if l.lastFlush.IsZero() || now.Sub(l.lastFlush) >= trafficLedgerSaveInterval {
		l.persistLocked(now)
	}
}

func baselineFromSamples(samples []nicCounterSample) map[string]nicBaseline {
	nics := make(map[string]nicBaseline, len(samples))
	for _, s := range samples {
		nics[s.Name] = nicBaseline{Tx: s.Sent, Rx: s.Recv}
	}
	return nics
}

// coldInitLocked 状态文件不存在时的首次初始化：
// 月度周期优先幂等迁移旧 net_static.json；否则 forever 模式播种当前计数，
// 月度模式按容器感知运行时长决定播种当前计数或 0。
func (l *trafficLedger) coldInitLocked(env ledgerEnv, samples []nicCounterSample, now time.Time) {
	l.state = trafficLedgerState{
		CycleKey: currentCycleKey(now),
		BootKey:  systemBootKey(env.procRoot),
		Nics:     baselineFromSamples(samples),
	}

	day := flags.MonthRotate
	monthly := day >= 1 && day <= 31

	if monthly {
		lastReset := utils.GetLastResetDate(day, now)
		migrated := false
		// 幂等迁移旧 net_static.json（cwd 或可执行文件同目录）
		for _, legacyPath := range []string{"net_static.json", filepath.Join(env.exeDir, "net_static.json")} {
			up, down, ok := sumLegacyNetStatic(legacyPath, uint64(lastReset.Unix()), env.includeNics, env.excludeNics, env.sysNetRoot)
			if !ok {
				continue
			}
			l.state.CycleUp = up
			l.state.CycleDown = down
			l.pendingLegacyRename = legacyPath
			migrated = true
			break
		}
		if !migrated && env.hasKernelUptime {
			// 仅按内核/lxcfs uptime 保守判定：内核在新周期起点之后才启动
			// （now-kernelUptime >= lastReset）才播种当前计数。冷启阶段容器
			// PID1 年龄无法区分 --net=host 与独立 netns，严禁用于播种。
			if now.Unix()-env.kernelUptime >= lastReset.Unix() {
				l.state.CycleUp, l.state.CycleDown = sumSamples(samples)
			}
			// 跨周期长跑：初始为 0，当前计数仅作基线
		}
	} else {
		// forever：播种当前匹配网卡总计数，防线上流量突降为 0
		l.state.CycleUp, l.state.CycleDown = sumSamples(samples)
	}
}

func sumSamples(samples []nicCounterSample) (sent, recv uint64) {
	for _, s := range samples {
		sent = addUint64Saturating(sent, s.Sent)
		recv = addUint64Saturating(recv, s.Recv)
	}
	return sent, recv
}

// FlushTrafficLedger 退出前强刷盘（供 cmd 信号退出与 update 包 os.Exit 前回调）
func FlushTrafficLedger() {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	l := &ledger
	if !l.initialized || l.stateFile == "" {
		return
	}
	l.persistLocked(time.Now())
}

// resetTrafficLedgerForTest 重置账本全局状态，仅供单测隔离使用
func resetTrafficLedgerForTest() {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	ledger = trafficLedger{}
}
