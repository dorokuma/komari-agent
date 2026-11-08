---
status: active
superseded_by: ""
supersedes: ""
模块: "monitoring, server"
---

# 与上游分歧登记 (fork-delta)

## 一句话结论

登记当前仓库与上游 `komari-monitor/komari-agent` 的定制改动及历史分歧项。

## 背景

本仓库从上游 `komari-monitor/komari-agent` fork 并维护（公开 Fork 地址：`dorokuma/komari-agent`），针对特定宿主容器环境（如 LXC）及流量统计场景进行了针对性定制。

基线版本：已于 2026-11-08 由 `1.2.60` 同步至上游 `1.5.11`（worktree `chore/sync-upstream-1.5.11` 合并）。

## 决策

当前与上游已知分歧点如下：

1. **LXC/cgroup v2 CPU 监控支持**
   - 模块：`monitoring/unit`（`cpu.go`, `cpu_cgroup_test.go`）
   - 改动：
     - **容器/配额启用门禁**：仅当存在容器环境特征（marker 文件仅保留 `/.dockerenv` 与 `/run/.containerenv`，移除 `/dev/lxc` 以免 Proxmox/LXC 宿主机因存在 `/dev/lxc/monitor` 等设备目录被误判；`/proc/1/environ` 含 `container=` 且等号右侧非空；或 `/proc/self/cgroup` 的 `0::` 路径经 `isContainerCgroupPath` 判定：先排除 `/` 与 `/init.scope`，再逐段扫描并跳过 `.service` 段（如宿主机的 `/system.slice/docker.service`、`/system.slice/lxc.service`、`/system.slice/lxcfs.service`），任一非 `.service` 段匹配 `lxc`、`lxc.payload.*`、`lxc.monitor.*`、`docker`、`docker-*.scope`、`kubepods`、`kubepods.slice`、`containerd`、`libpod`、`machine-lxc*` 等容器特征即启用，兼顾容器内 systemd 运行的 `/lxc.payload.ct101/init.scope` 与 `/lxc.payload.ct101/system.slice/komari-agent.service` 等嵌套路径）或 `/sys/fs/cgroup/cpu.max` 显式配置了非 `max` 的有限配额，且 `/sys/fs/cgroup/cpu.stat` 可读时，才启用 `cpuUsageFromCgroup`，避免普通 cgroup v2 物理宿主机误入而报 0% 或错误按 `cpu/online` 除算。
     - **基线保护与热更新/回退处理**：在 `cgroupMu` 下记录 `lastCgroupNCPU`；当 `usage < lastCgroupUsage`（计数器重置回退）或 `cpu.max` 配额热更新时重播种采样基线，且计数器回退时置 `haveCgroupPct = false` 并返回 `0, false`；当 `readCgroupUsageUsec()` 或 `cgroupCPULimit()` 瞬时读取失败时作废 `haveCgroupSample`，防止故障恢复后将漫长故障间隔稀释进单次采样。
     - **审查观察项（设计权衡）**：`Cpu()` 每轮询周期执行 `cgroupCPUStatAvailable()`（含 marker 与 `/proc` 轻量读）属每秒一次级微小开销，暂不引入 `sync.Once` 缓存以保持单测路径变量可动态切换。
2. **流量偏移补偿机制 (Traffic Offset)**
   - 模块：`monitoring/unit`（`net.go`, `traffic_offset.go`, `traffic_offset_test.go`）
   - 改动：
     - **饱和加法与路径一致性**：支持从 `/opt/komari/traffic-offset` 载入基线流量偏移，使用 `addUint64Saturating` 进行 `uint64` 饱和加法防止溢出回绕；`net.go` 中 `MonthRotate != 0` 的四处返回路径（含 `GetTotalTrafficBetween` 与 fallback 双失败分支）及 `MonthRotate == 0` 路径统一调用 `applyTrafficOffset`。
     - **严格解析与周期/永久偏移语义**：`readTrafficOffset` 对非空非 `#` 注释行严格校验键值格式、白名单键名（`up`/`down`/`reset_day`/`since`）、`reset_day` 范围 `1..31` 及日期合法性（保存原始日期字符串 `sinceRaw`），遇到损坏行立即拒绝整份文件；当生效重置日 `effectiveResetDay` 在 `1..31` 时要求必须存在 `sinceRaw` 并直接在 `now.Location()` 下解析 `sinceDate` 与 `lastResetDate` 比对周期是否过期，消除跨时区日历日偏移；当 `reset_day=0 && MonthRotate=0`（即 `effectiveResetDay == 0`）时，允许无 `since` 作为永久累计偏移生效。
     - **审查观察项（设计权衡与边界说明）**：
       - 每采集周期直接读盘解析 `/opt/komari/traffic-offset`（文件仅数行、本地虚拟文件系统/页缓存开销极小，保留运行时热修改偏移文件无需重启 agent 的运维便利性）；
       - 偏移文件中的 `reset_day` 优先于面板 `flags.MonthRotate`，运维配置时须与面板月重置日保持一致；
       - `net.go:232-237` 在 `MonthRotate != 0` 且双失败回退 `(0,0)` 时叠加偏移、而 `MonthRotate == 0` 错误路径直返 `(0,0)` 的现状说明（调用方在 `err != nil` 时知悉该边界；该历史分支已在后续统一流量账本改造中收拢为单一路径）；
       - 上游既有 `monitoring/unit/net_test.go`（如 `TestNetworkSpeedWithoutMonthRotate`）存在直接修改全局 `flags.MonthRotate = 1` 且未恢复的历史问题，为避免污染与上游无关的测试文件暂不改动，新增 `traffic_offset_test.go` 单测均通过 `t.Cleanup` 严格保存并恢复全局配置（该测试污染问题已在后续流量账本改造中一并修复）；
       - 保持上游 README 结构以减少同步冲突。
3. **安全凭证清理**
   - 提交 `8cd9214`：移除配置中的 Cloudflare Access 凭证。
4. **提交信息规范**
   - 提交 `02a1ed1`：在 `readme.md` 补充 Conventional Commits 与 commit-msg 校验门禁说明。
5. **流量统计精准化改造（P0 网卡过滤 + P1 单调账本 + P2 默认禁自更新）**
   - 模块：`monitoring/unit`（`net.go`, `traffic_ledger.go`）、`utils/utils.go`、`cmd/flags/flag.go`、`cmd/root.go`、`update/update.go`、`build_all.sh`
   - 改动：
     - **P0 网卡拓扑过滤**：`shouldIncludeWithSysfs` 参数化 sysNetRoot（生产 `/sys/class/net`），四级优先级为回环硬拦截（名称 `lo`/`lo<digits>` 或 sysfs type=772）> 显式 `--include-nics` > 显式 `--exclude-nics` > 默认自动过滤（大小写不敏感前缀表 + sysfs `tun_flags`/`DEVTYPE`/隧道 `type` 识别 + 硬件锚点判定：整机存在任一具备 `device` 的合规硬件/半虚拟化网卡时，无 `device` 网卡一律剔除；纯容器层通过特征检查即纳入，不依赖 ifindex/iflink）。集合级空集安全保底：自动过滤为空但存在非 lo 接口时，回退至仅排除 lo、excludeNics 与已知桥接/容器对端前缀的最小集合，并以 `sync.Once` 打印一次 WARN，绝不静默上报恒 0。默认排除前缀覆盖 `tun`/`utun`/`wg`/`tailscale`/`zt`/`dae`/`mihomo`/`meta`/`sing`/`clash`/`warp`/`ppp` 等 VPN/代理隧道接口：上行口本身即隧道接口的专用 VPN 出口节点须显式 `--include-nics` 固定上行口。上游 `host_proc` 配置项（`HOST_PROC`）经 `procRoot()` 同时决定账本代际标识的 `/proc` 根：其语义面向宿主机进程或共享网络命名空间监控（宿主 `/proc` 与容器共享同一 netns 计数域），独立 netns 容器不应配置，否则容器重建代际无法被感知。
     - **P1 原子持久化单调账本**（`traffic_ledger.go`）：以按网卡单调累加账本取代 `MonthRotate==0` 的裸 `/proc/net/dev` 求和与 `MonthRotate!=0` 的 31 天切片 netstatic（netstatic 包文件保留不删）。周期模型仅保留 `MonthRotate==0`（forever）与 1..31（`month:<day>:<YYYY-MM-DD>`）；代际识别采用结构化标识 `boot_id|ns=<netns>|t=<ticks>`（可读字段动态拼接），宿主机重启变 boot_id、LXC 重建 netns 变 `1/ns/net` 链接目标、同 netns 重启（如 docker restart、`--net=host` 重启）仅变 PID1 starttime；Linux 下 `systemBootKey`/`kernelUptimeSeconds` 严格绑定传入 `<proc>` 根（含 `--host-proc`/测试注入），boot_id 或 `/proc/uptime` 读取失败直接返空/返回 false 而不穿透调用 `host.BootTime()`/`host.Uptime()`（gopsutil 固定读全局 `/proc`，瞬态失败会跨源冲掉 `boot_id|ns=...` 标识），仅非 Linux 平台经 gopsutil 兜底（`boottime:<sec>`）。`parseBootKey`/`countersDefinitelyReset` 区分计数器真归零（同源 `boot_id` 变化、或同内核下明确新建 netns）与 netns 保留性重启：前者将当前计数整段饱和累加，后者逐网卡累加 `cur-prev`（计数延续）、实际归零网卡累加当前值，两类代际切换中未见基线的网卡（中途重现或新纳入）统一只记基线，防把长跑计数重复计入；`kernelDefinitelyRebooted`/`countersDefinitelyReset` 要求同源根命名空间——`boot_id` 与 `boottime:` 跨源互转不判重启（按差值处理），`boottime:` 域内核重启判定含 5 秒抖动容差；周期切换归零重设基线，跨周期同时重启时按 `kernelUptimeSeconds`（内核重启，直读 `/proc/uptime` 首字段）或 `newNetnsCounterAgeSeconds`（同内核新建虚拟 netns 且无硬件锚点，kernelUptime 减 PID1 代际年龄）保守判定代际启动是否晚于新周期起点，晚于才播种当前计数否则归零；同代际按 `cur-prev` 累加（`cur<prev` 按 0），常规步进仅按本轮样本重写基线表，自动剔除已消失网卡的陈旧基线，同名重建首拍不产生伪增量。冷启动：forever 播种当前计数；月度优先幂等迁移旧 `net_static.json`（成功落盘后改名 `.migrated` 防重入），无旧文件时按 `kernelUptimeSeconds`（不混合 PID1 时钟域，非 Linux 回退 `host.Uptime()`）判定内核是否晚于新周期起点，晚于才播种当前计数否则从 0 起算。状态文件默认固定为 `<可执行文件解析软链后目录>/traffic-state.json`（可 `--traffic-state-file`/`AGENT_TRAFFIC_STATE_FILE` 覆盖），同目录临时文件 + Sync + Rename 原子落盘，仅对内容/schema 非法的常规文件（JSON 解析失败或缺 `cycle_key`）改名 `.corrupt` 保留现场后全新初始化，目录/软链/权限拒绝等非常规路径或 I/O 错误一律不 Rename、仅 WARN 后内存冷启——`--traffic-state-file` 须指向常规文件路径：状态文件本体为符号链接时经 `os.Lstat` 校验不作既有账本加载、首轮按全新账本初始化，原子落盘的 `Rename` 将链接本体替换为常规文件（软链目标内容不动）；父目录为软链不受影响，需跨盘重定向时应软链父目录而非文件本体。脏状态 30s 节流、写失败 60s 节流 ERROR 并维持内存账本；信号退出与 `update` 包 `os.Exit(42)` 前经 `update.RegisterPreExitHook` 回调强刷盘。
     - **月末重置日夹紧**：`utils.getActualResetDate` 在 resetDay 超出当月天数时夹紧到月末最后一天零点（不再顺延下月 1 日），消除相邻两月同月双重置。
     - **P2 默认禁自更新与版本注入**：`DisableAutoUpdate` 默认 `true`（GlobalConfig 初始值与 cobra 默认一致），`loadFromEnv` 布尔分支对称支持 true/1 与 false/0。
     - **Fork 仓与版本注入（1.5.11 同步期更新）**：`update.Repo` 已改为 `dorokuma/komari-agent`（自更新检查指向我方 Fork，非上游）；上游 `1.5.11` 将自更新成功退出由 `os.Exit(42)` 改为返回 `ErrRestartRequired` 并由 `cmd` 的 `shutdownCoordinator` 收口，我方适配为两处 `ErrRestartRequired` 返回前调用 `runPreExitHook()`，且 `shutdown.shutdown` 的 netstatic 停止回调前包装 `monitoring.FlushTrafficLedger()`，保证 signal 与自更新重启路径均刷盘。上游 `1.5.11` 移除了 `WarnKomariRunning`，其新增远程文件管理（`server/files.go`/`file_stream.go`）无独立开关，与终端/RCE 同受 `--disable-web-ssh`（`DisableWebSsh`）控制。
     - **Fork 发版 Tag 与 `build_all.sh` 版本号规则（1.5.11 同步期更新）**：发版 tag 约定 `v<upstream>-fork.<N>`（如 `v1.5.11-fork.1`）。`build_all.sh` 版本提取：HEAD 精确匹配 `*-fork.<N>` tag 时 `VERSION=<tag 去 v>+g<sha>`（如 `1.5.11-fork.1+g0123456`）；精确匹配上游纯 SemVer tag 时 `VERSION=<tag>-fork.g<sha>`；无精确 tag 时取最近 tag，匹配 `*-fork.<N>` 得 `<tag>.dev.g<sha>`，匹配纯 SemVer 得 `<tag>-fork.g<sha>`，否则回退 `0.0.1-fork.g<sha>`。SHA 统一加 `g` 前缀以消除纯数字前导零短 SHA 在 SemVer 预发布段非法的问题；以上格式均经 `update.parseVersion` 单测覆盖。
     - **自更新防降级与安装脚本下载源（1.5.11 同步期更新）**：`checkAndUpdateStable` 由 `UpdateSelf`（仅判 `!Equals` 不判版本方向）重构为 `DetectLatest + needUpdate` 防降级保护：`latest <= current`（含相等、仅 `+g<sha>` 构建元数据不同、本地 dev/高版本）直接返回不更新，仅严格 `latest > current` 才解析可执行文件路径并调用 `UpdateTo` 后返回 `ErrRestartRequired`；`go-github-selfupdate` v1.2.3 无预发布检测开关，`v*-fork.<N>` Tag 须以非 prerelease 正式发版才能被 `DetectLatest` 检出。`install.sh`/`install.ps1` 的 Releases API 与下载地址统一指向 `dorokuma/komari-agent`（Go 代码 module import 路径保持 `komari-monitor/komari-agent` 不变）。
     - **安装脚本二进制下载信任边界收紧（1.5.11 同步期安全加固）**：`install.sh` 默认禁用直连失败后自动回退 `ghfast.top` / `gh-proxy.com` / `ghproxy.net` 等未验签的第三方公共加速镜像（`install_no_mirror` 默认 `true`，相应镜像拼接回退分支已移除），默认仅请求 `https://github.com/dorokuma/komari-agent/releases/...`；仅当用户显式传入 `--install-ghproxy <mirror-prefix>` 时才经由其指定的代理前缀下载与解析 Releases API。`--install-no-mirror` 选项解析保留以维持命令行向后兼容（幂等，行为同默认）。`install.ps1` 核查无第三方镜像自动回退，原已仅在显式 `--install-ghproxy` 时走代理，无需改动。
6. **后续合并策略**
   - 已确认决策：长期自维护，不向上游提交 PR；同步方式为定期 merge/rebase 上游后在自维护分支重放定制项（LXC cgroup v2 CPU 监控、`/opt/komari/traffic-offset` 流量补偿、网卡拓扑过滤与单调流量账本、默认禁自更新与 fork 版本标识）；`readme.md` 保持上游 README 结构（允许追加小节）。

## 被放弃的方案（必填）

- **完全覆盖上游而不记录分歧**：会导致后续上游同步 (upstream merge/rebase) 时丢失定制上下文和冲突成因。
- **向上游提 PR**：定制强依赖本部署形态的 LXC/cgroup v2 与本地流量补偿挂载，与上游通用监控场景不一致，且上游 review 周期不可控。

## 来源

- 本地提交历史 `git log` 与工作区 diff。
