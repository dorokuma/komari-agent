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

本仓库从上游 `komari-monitor/komari-agent` fork 并维护，针对特定宿主容器环境（如 LXC）及流量统计场景进行了针对性定制。

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
       - `net.go:232-237` 在 `MonthRotate != 0` 且双失败回退 `(0,0)` 时叠加偏移、而 `MonthRotate == 0` 错误路径直返 `(0,0)` 的现状说明（调用方在 `err != nil` 时知悉该边界）；
       - 上游既有 `monitoring/unit/net_test.go`（如 `TestNetworkSpeedWithoutMonthRotate`）存在直接修改全局 `flags.MonthRotate = 1` 且未恢复的历史问题，为避免污染与上游无关的测试文件暂不改动，新增 `traffic_offset_test.go` 单测均通过 `t.Cleanup` 严格保存并恢复全局配置；
       - 保持上游 README 结构以减少同步冲突。
3. **安全凭证清理**
   - 提交 `8cd9214`：移除配置中的 Cloudflare Access 凭证。
4. **提交信息规范**
   - 提交 `02a1ed1`：在 `readme.md` 补充 Conventional Commits 与 commit-msg 校验门禁说明。
5. **后续合并策略**
   - 已确认决策：长期自维护，不向上游提交 PR；同步方式为定期 merge/rebase 上游后在自维护分支重放定制项（LXC cgroup v2 CPU 监控与 `/opt/komari/traffic-offset` 流量补偿、基线保护与热更新回退处理）；`readme.md` 保持上游原貌以减少同步冲突。

## 被放弃的方案（必填）

- **完全覆盖上游而不记录分歧**：会导致后续上游同步 (upstream merge/rebase) 时丢失定制上下文和冲突成因。
- **向上游提 PR**：定制强依赖本部署形态的 LXC/cgroup v2 与本地流量补偿挂载，与上游通用监控场景不一致，且上游 review 周期不可控。

## 来源

- 本地提交历史 `git log` 与工作区 diff。
