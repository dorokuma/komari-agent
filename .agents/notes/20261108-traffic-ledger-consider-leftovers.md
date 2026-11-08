---
status: active # active | superseded
superseded_by: ""
supersedes: ""
模块: "cmd, monitoring, update" # 可选模块: cmd, dnsresolver, monitoring, protocol, server, terminal, update, utils, ws
---

# 2026-11-08 流量账本与网卡过滤 C 档门禁五轮 oracle 审计非阻断观察项

## 一句话结论

登记 `fix/traffic-ledger-and-nic-filter` 分支 2026-11-08 C 档门禁五轮 oracle 审计通过后尚未闭环的 19 条非阻断观察项：首轮 1~6（自更新逃生门、废弃 flag 空操作、旧文件迁移 cwd 优先、原子落盘无父目录 fsync、历史辅助函数保留、`ledgerMu` 持锁落盘），第二轮 7~12（同代际跨月停机恢复归零、`corrupt` 单槽备份覆盖、代际切换空 samples 瞬态基线、切换当拍速率报 0、冷启/新 netns 按内核 uptime 保守播种、`executableDir` 失败 cwd 兜底），第三轮 13~16（`--host-proc` 与独立 netns 的代际标识边界、`state.BootKey==""` 静默采纳当前代际、`IOCounters` 瞬态报错返回 0+err、VPN 出口节点须显式 `--include-nics`），第四轮 17（状态文件本体为软链时按全新账本初始化并在落盘时替换为常规文件），第五轮 18~19（Linux `/proc/uptime` 瞬态不可读窗口保守归零、`1/ns/net` 或 `/proc/1/stat` 瞬态不可读引发 boot_key 子字段抖动按差值处理），其中第 7 条含主代理 `refuted-with-evidence` 反驳取舍记录，第五轮 oracle 给出「无阻断，可放行」结论（零 must-fix / 零 should-fix，附 18~19 两条极端瞬态窄窗观察项），逐条附 `[pattern:<slug>]` 标签、不阻断理由与后续处置条件。

## 背景

C 档门禁首轮 oracle 审计对本轮「流量统计精准化改造（P0 网卡过滤 + P1 单调账本 + P2 默认禁自更新）」给出 1 条 must-fix（M1 代际切换整段累加会把保留 netns 的长跑计数重复计入）、2 条 should-fix（S1 非 Linux/跨平台兜底缺失、S2 `build_all.sh` SemVer tag 正则未锚定尾部）与 6 条非阻断观察项（本笔记 1~6）。M1/S1/S2 已在本轮同分支修复。

C 档终审 reviewer 给出 4 条阻断问题（R-M1 host 网络容器月度冷启按 PID1 年龄误播种宿主长跑计数；R-S2 已消失网卡同名重建沿用陈旧基线；R-S3 `TestShouldIncludeWithSysfs` 前缀/锚点遮蔽；R-S4 原子落盘最终 Rename 失败遗漏临时文件清理）与 6 条非阻断观察项（本笔记 7~12）。R-M1/R-S2/R-S3/R-S4 已在本轮同分支修复；第 7 条为主代理对 oracle 建议的 `refuted-with-evidence` 取舍，附证据留痕。

C 档终审第三轮 oracle 审计给出 2 条 should-fix（S-A `.corrupt` 备份误作用于目录/软链/权限错误路径；S-B 未知 netns 代际切换中未见基线的网卡全值累加）与 4 条非阻断观察项（本笔记 13~16）。S-A/S-B 已在本轮同分支修复；本笔记按门禁纪律仅登记非阻断项。

C 档终审第四轮 oracle 审计给出 1 条 should-fix（SF-R4-1：`systemBootKey`/`kernelUptimeSeconds` 在 Linux 下 boot_id/`/proc/uptime` 瞬态不可读时穿透调用 gopsutil 全局 `/proc` 兜底，跨源 boottime: 标识冲掉 `boot_id|ns=...` 后被 `prevRoot != curRoot` 误判内核重启，整段灌入长跑计数）、`.corrupt` 改名失败后仍打印「preserved as .corrupt」的日志失真观察项与 Linux 瞬态 `boot_id` 抖动观察项，以及 1 条非阻断观察项（本笔记 17）。SF-R4-1、`.corrupt` 改名日志判断与瞬态 `boot_id` 抖动均已在本轮同分支直接改码闭环。

C 档终审第五轮 oracle 审计给出「无阻断，可放行」结论：零 must-fix、零 should-fix，附 2 条极端瞬态窄窗非阻断观察项登记为第 18~19 条，并同步修正第 14 条出处引用（`bootKeyChanged` 空串守卫与 `stepLocked` 常规步进尾部采纳逻辑）。

## 决策（观察项逐条留痕）

### 1. [pattern:explicit-autoupdate-escape-hatch] 显式开启自更新作为切回上游逃生门

- **出处**：`cmd/flags/flag.go`（`DisableAutoUpdate` 默认 `true`）、`cmd/root.go`（`--disable-auto-update` flag、`AGENT_DISABLE_AUTO_UPDATE` 环境变量）。
- **问题**：P2 将自更新默认禁用是本 fork 的明确决策；但「默认关闭」意味着 fork 版本通道（`CurrentVersion` 含 `-fork.<sha>` 预发布后缀）下若未来想切回上游发布轨道，必须显式 `--disable-auto-update=false` 或 `AGENT_DISABLE_AUTO_UPDATE=0` 重新打开自更新。该逃生门本身被保留（非硬编码死锁），属于有意设计而非缺陷。
- **不阻断理由**：开关路径完整、语义对称（env 布尔分支同时支持 `true/1` 与 `false/0`），运维可随时切回；默认禁自更新正是本次改造目标，非待修问题。
- **后续处置条件**：若后续 fork 版本策略变化（如改走独立发布渠道或彻底移除自更新代码路径），在本笔记补充记录并同步 `fork-delta.md` P2 条目；届时评估是否改为编译期常量而非运行时开关。

### 2. [pattern:deprecated-flag-noop] 历史废弃 `-autoUpdate` 保持仅告警无副作用

- **出处**：`cmd/root.go`（预解析 `os.Args` 命中 `-autoUpdate`/`--autoUpdate` 时打印 `WARNING: The -autoUpdate flag is deprecated in version 0.0.9 and later...`）。
- **问题**：上游历史废弃 flag `-autoUpdate` 当前仅打印告警、不再设置任何行为（0.0.9 起 deprecated）。oracle 确认该空操作语义在本轮改动中未被改变，告警文案亦未变。
- **不阻断理由**：保持上游既有语义可减少合并冲突；flag 不绑变量、无副作用，符合上游 0.0.9+ 的 deprecation 约定。
- **后续处置条件**：下次上游同步时若上游彻底移除该分支则跟随移除；若上游改为真报错退出，评估是否与 `DisableAutoUpdate` 默认值交互后跟进。

### 3. [pattern:legacy-migration-cwd-precedence] 旧 `net_static.json` 迁移优先检查 cwd 以对齐旧版相对路径默认行为

- **出处**：`monitoring/unit/traffic_ledger.go` `coldInitLocked`（迁移候选顺序：`"net_static.json"`（相对 cwd）在前、`filepath.Join(env.exeDir, "net_static.json")` 在后）。
- **问题**：旧版 netstatic 以相对路径 `net_static.json` 落盘，实际位置取决于启动时的 cwd；迁移优先检查 cwd 是为了对齐旧版的相对路径默认行为。当 cwd 与可执行目录不同时，两路径各查一遍，先命中者优先并随后改名 `.migrated` 防重入。
- **不阻断理由**：迁移为幂等一次性操作，双路径探测开销可忽略；顺序与旧版「相对路径优先」语义一致，先 cwd 再 exeDir 可覆盖 systemd 等 `WorkingDirectory` 部署下旧文件留在 cwd 的情形。
- **后续处置条件**：若未来部署形态统一为固定 exeDir（例如强制 `--traffic-state-file` 且旧文件位置已知），可裁剪为单一路径；届时更新本条目并在 fork-delta 登记。

### 4. [pattern:atomic-rename-without-dir-fsync] 原子落盘对临时文件 Sync 后 Rename、未额外 fsync 父目录

- **出处**：`monitoring/unit/traffic_ledger.go` `persistTrafficLedgerAtomic`（`CreateTemp` → `Write` → `tmp.Sync()` → `tmp.Close()` → `os.Rename`，未 `fsync(dir)`）。
- **问题**：崩溃一致性语义上，临时文件已 `Sync` 保证内容落盘，但 `rename` 这一目录项变更未经父目录 fsync，断电崩溃窗口内理论上可能回退到旧版本文件名→旧内容（而非损坏/半写文件）。账本数据本身可从内核计数重新播种，丢失最新一次节流落盘仅少记最多 ~30s 流量。
- **不阻断理由**：崩溃场景下语义安全（要么旧完整版要么新完整版，无半文件）；父目录 fsync 每次落盘多一次磁盘往返，对 30s 节流的本地小文件收益有限；目标部署目录为本地 ext4/xfs 类文件系统而非网络挂载。
- **后续处置条件**：若账本被复用到对崩溃后「最新值」有硬需求的场景（如计费级精确对账），补 `fsync(dirfd)`；跨平台实现差异（Windows 需 `FlushFileBuffers`）需一并评估。

### 5. [pattern:legacy-helper-retained-for-tests] 保留 `getNetworkSpeedFallback`/`shouldInclude` 供历史单测与上游合并兼容

- **出处**：`monitoring/unit/net.go`（`getNetworkSpeedFallback`、`collectNetworkTotals`、`updateNetworkSpeedSample`、`shouldInclude` 薄封装仍保留）、`monitoring/unit/net_test.go`（`TestNetworkSpeedFallback`、`TestShouldInclude` 仍引用）。
- **问题**：账本主路径 `NetworkSpeed` 已不再调用 `getNetworkSpeedFallback`，`shouldInclude` 也仅是 `shouldIncludeWithSysfs` 的默认 sysNetRoot 薄封装。oracle 判定其为「保留给历史单测与未来上游合并回退路径」的死代码而非缺陷。
- **不阻断理由**：函数体小、无额外维护成本；`updateNetworkSpeedSample` 速率采样器在语义上与账本 `roundDelta` 机制互补，保留可在上游同步冲突时快速回退；单测仍直接覆盖。
- **后续处置条件**：上游同步若移除对应回退分支或单测迁移完毕，可一并删除并在 fork-delta 登记；删除前先确认无外部 cmd/ws 调用方。

### 6. [pattern:mutex-held-during-state-sync] `ledgerMu` 下每 30 秒同步约数百字节本地 JSON 文件

- **出处**：`monitoring/unit/traffic_ledger.go` `updateTrafficLedger`/`persistLocked`（`ledgerMu` 全程持有，`persistTrafficLedgerAtomic` 含 `Sync`+`Rename` 磁盘 I/O）。
- **问题**：持锁做同步 I/O 在慢速/网络文件系统上可能阻塞所有 `updateTrafficLedger` 与 `FlushTrafficLedger` 调用者。oracle 评估：状态文件固定于可执行文件同目录（本地文件系统）、单文件仅数百字节、30s 节流一次，实际持锁阻塞时间在毫秒级，不构成瓶颈。
- **不阻断理由**：锁内写保证「基线更新→落盘」对并发调用者的线性一致性；把 I/O 挪出锁外需要引入影子快照或双缓冲，复杂度与收益不匹配；目标部署目录非慢速网络文件系统。
- **后续处置条件**：若未来 `--traffic-state-file` 被指向 NFS/FUSE 类慢速挂载，或落盘负载显著增大（多实例共享、状态体膨胀），重新评估「锁内原子提交、锁外异步落盘」改造，并先补并发压测基准。

### 7. [pattern:cross-cycle-downtime-unsplit-counter] 同代际跨月重置点停机恢复归零重建基线

- **出处**：`monitoring/unit/traffic_ledger.go` `stepLocked` 周期切换分支（跨周期时旧周期累计清零、以当前 `samples` 重设基线）。
- **问题**：agent 停机跨越月度重置点后恢复，`/proc/net/dev` 计数器未归零（同内核同 netns）时，旧月内已跑流量无法与新月流量区分，账本按归零重建基线处理——旧月残余部分在月度口径下被丢弃而非并入新月。第二轮 oracle 曾建议累加 `cur-prev` 跨周期差额。
- **不阻断理由（主代理 `refuted-with-evidence`）**：`/proc/net/dev` 计数器无时间戳，`cur-prev` 无法区分「新周期内新增流量」与「旧周期遗留流量」，累加会把旧月整月未入账流量灌入新账期，与周期隔离语义直接冲突；归零重建基线是有意的保守取舍。
- **后续处置条件**：若引入带时间戳的外部流量源（如 vnstat 记录、`net_static.json` 类历史快照），可重新评估按时间戳切分跨周期差额。

### 8. [pattern:corrupt-backup-single-slot] 损坏状态文件 `.corrupt` 单槽备份二次损坏覆盖

- **出处**：`monitoring/unit/traffic_ledger.go` `updateTrafficLedger` 加载失败分支（`os.Rename(l.stateFile, l.stateFile+".corrupt")`）。
- **问题**：状态文件连续两次损坏时，第二次 Rename 会覆盖上一份 `.corrupt`，仅保留最近一份损坏现场；更早的损坏证据丢失。
- **不阻断理由**：损坏文件仅作排查证据，非数据源（账本可从内核计数重建）；单槽覆盖语义简单可预期，避免 `.corrupt.N` 无限堆积占用磁盘。
- **后续处置条件**：若现场出现需比对多代损坏文件的排查需求，改为追加时间戳后缀或环形保留 N 份。

### 9. [pattern:empty-samples-epoch-switch-baseline] 代际切换恰逢空 `samples` 轮次的瞬态基线边界

- **出处**：`monitoring/unit/traffic_ledger.go` `stepLocked` 周期/代际切换分支（`l.state.Nics = baselineFromSamples(samples)`，`samples` 为空时得到空基线表）。
- **问题**：`/proc/net/dev` 瞬态不可读恰与周期/代际切换同拍时，基线表被置空；下一拍所有网卡按「新出现」只记基线，损失该拍差额。常规步进已做 `len(samples)>0` 保护不清空基线，切换分支未做。
- **不阻断理由**：窗口极小（瞬态读失败恰逢切换当拍）；损失上限为一拍差额，方向为少记而非多灌，符合保守口径。
- **后续处置条件**：若需进一步收紧，可在切换分支对空 `samples` 保留旧基线表（与常规步进一致）。

### 10. [pattern:zero-speed-on-epoch-switch] 周期/代际切换当拍 `roundDelta=0` 实时速率单轮报 0

- **出处**：`monitoring/unit/traffic_ledger.go` `stepLocked` 入口（`l.roundDeltaUp, l.roundDeltaDown = 0, 0`）与切换分支提前 `return`。
- **问题**：周期或代际切换当拍不计算 `roundDelta`，实时速率上报 `0` 一轮，监控曲线出现单点跌落。
- **不阻断理由**：语义正确——切换拍无可信同代际增量，报 0 优于虚报；仅影响单拍，下一拍自动恢复。
- **后续处置条件**：若前端展示对单点 0 敏感，可由上报层做插值/标记，不改账本判定逻辑。

### 11. [pattern:host-uptime-conservative-seeding] 月度冷启与硬件锚点层统一按内核/lxcfs uptime 保守判定

- **出处**：`monitoring/unit/traffic_ledger.go` `coldInitLocked`（`now.Unix()-env.kernelUptime >= lastReset.Unix()` 才播种）、`newNetnsCounterAgeSeconds`（仅同内核+新建 netns+无硬件锚点才按 PID1 代际年龄判定）。
- **问题**：冷启阶段无法区分 `--net=host` 与独立 netns，统一按内核 uptime 判定播种——无 lxcfs 桥接的容器首次装机冷启会从内核装机时刻起算（判定偏保守，新容器首账期可能少记装机后至本周期起点前的本属合法流量）；lxcfs 虚拟化 uptime 的容器则按虚拟化值正常判定。
- **不阻断理由**：保守方向为少记而非误灌，防住 `--net=host`/共享 Pod netns 把宿主数月旧流量灌入新账期；误灌方向不可逆且破坏月度隔离语义。
- **后续处置条件**：若需提升无 lxcfs 新容器首账期精度，可在冷启时探测 netns 创建证据（如 netns inode 与历史 boot_key 对照存储扩展）后重估播种条件。

### 12. [pattern:exe-dir-empty-cwd-fallback] `executableDir()` 解析失败时回退裸文件名的极端兜底

- **出处**：`monitoring/unit/traffic_ledger.go` `executableDir`（`os.Executable` 失败时 `exeDirCached` 为空）、`trafficLedgerStateFile`（`dir == ""` 时返回裸 `traffic-state.json` 即相对 cwd）。
- **问题**：`os.Executable` 失败的极端情形下，状态文件落回「相对 cwd」的旧版语义，与「不回退随机 cwd」的设计目标相悖。
- **不阻断理由**：`os.Executable` 失败概率极低；失败时进程整体环境已异常，落盘位置属次要问题；保留兜底胜过直接 panic 或禁用账本。
- **后续处置条件**：若需彻底杜绝 cwd 依赖，可在 `dir == ""` 时禁用持久化（仅内存账本）或显式要求 `--traffic-state-file`。

### 13. [pattern:host-proc-isolated-netns-bootkey] `--host-proc` 搭配独立 netns 容器时代际标识取自宿主 `/proc` 的适用边界

- **出处**：`monitoring/unit/net.go` `procRoot()`（`flags.HostProc` 非空时代际标识、连接数等 `/proc` 读取均走宿主挂载点）、`monitoring/unit/traffic_ledger.go` `systemBootKey`（boot_id、`1/ns/net`、PID1 starttime 全部按 procRoot 取值）。
- **问题**：`host_proc`/`HOST_PROC` 的设计语义是容器内挂载宿主 `/proc` 以监控宿主进程；此时 `systemBootKey` 读到的 `1/ns/net` 与计数器所属的网络命名空间不同源（读的是宿主 PID1 的 netns，计数器是容器 netns 的）。在 `--net=host`/共享 netns 场景下二者一致、语义正确；但独立 netns 容器配置 `host_proc` 后，容器重建（netns 变化、计数器归零）不会被代际标识感知，归零后的同值区间可能漏记流量。
- **不阻断理由**：属配置适用边界而非代码缺陷——`host_proc` 本就面向宿主机/共享网络命名空间监控形态；独立 netns 容器无配置动机；已在 `fork-delta.md` P0/P1 条目明示该边界。
- **后续处置条件**：若后续出现独立 netns + `host_proc` 的合法部署形态（如仅需宿主进程计数但流量按容器口径统计），评估将流量账本 procRoot 与进程监控 procRoot 拆分，或在冷启时比对 netns inode 后 WARN。

### 14. [pattern:empty-bootkey-recovery-adopts-current] `state.BootKey==""` 恢复可读后静默采纳当前 `curBootKey` 的窄窗口取舍

- **出处**：`monitoring/unit/traffic_ledger.go` `bootKeyChanged`（`prevKey == "" || curKey == ""` 返回 `false`）与 `stepLocked` 常规步进尾部（`if curBootKey != "" && l.state.BootKey == ""` 采纳当前 `curBootKey`）。
- **问题**：`/proc` 瞬态不可读时 `curBootKey` 为空串，账本按同代际 `cur-prev` 保守处理并保留既有 `BootKey` 不冲掉——但冷启后落盘的 `BootKey` 若为空（如持久化后 `/proc` 短暂异常又恢复可读），`state.BootKey==""` 状态下不做代际切换判定，恢复可读后静默把当前 `curBootKey` 写入 `BootKey`。该窄窗口内若发生真实重启（计数器归零），归零后同值区间按 `cur<prev` 计 0，会漏记窗口内已跑流量。
- **不阻断理由**：窗口极窄（仅在 `BootKey` 首次可写为空且后续恰好跨重启恢复可读时存在）；方向为少记而非误灌；把 `BootKey==""` 视为未知代际切换并整段累加当前值，反而会在常规瞬态不可读场景把长跑计数灌入账本，代价更大。
- **后续处置条件**：若现场出现 `BootKey` 持久空串 + 重启漏记的排查案例，评估对 `BootKey==""` 且 `curBootKey!=""` 的恢复拍按「保守同代际」处理并在下一拍重采基线，或引入状态文件 `boot_key` 缺失标志位与冷启空串区分。

### 15. [pattern:iocounters-transient-error-zero-return] `NetworkSpeed` 在 `net.IOCounters` 瞬态报错时返回 `0 + err` 而不污损账本状态

- **出处**：`monitoring/unit/net.go` `NetworkSpeed`（`net.IOCounters(true)` 报错时 `return 0, 0, 0, 0, fmt.Errorf(...)`，不触达 `updateTrafficLedger`）。
- **问题**：gopsutil `IOCounters` 瞬态失败（如 `/proc/net/dev` 短暂不可读）时本轮上报为全 0 加错误，账本步进被跳过——该拍的单调增量不会丢失（计数器仍累积，下一拍 `cur-prev` 补回），但监控曲线出现单点 0 速率，与第 10 条同类单拍跌落。
- **不阻断理由**：跳过步进是正确语义——无样本时不得推进基线或写账本；总量口径由账本维护不受单拍影响；错误向上传递供调用方决定展示策略。
- **后续处置条件**：若上层对瞬态 0 敏感，可在调用方对 err 分支做保持上次速率的展示层处理；账本侧若需提升样本健壮性，评估 `/proc/net/dev` 直读 fast path 与 gopsutil 兜底的双通道。

### 16. [pattern:vpn-uplink-requires-include-nics] 默认排除前缀扩展后纯 VPN 出口节点须显式配置 `--include-nics`

- **出处**：`monitoring/unit/net.go` `defaultExcludedNicPrefixes`（含 `tun`/`utun`/`wg`/`tailscale`/`zt`/`dae`/`mihomo`/`meta`/`sing`/`clash`/`warp`/`ppp` 等隧道前缀）、`filterInterfacesWithSysfs` 空集保底回退。
- **问题**：本轮默认排除前缀大幅扩展后，上行口本身即隧道接口的纯 VPN 出口节点（整机唯一公网口为 `tun0`/`wg0` 等）会被默认自动过滤命中；空集保底虽不至于静默恒 0，但会回退到「仅排除 lo 与桥接/容器前缀」的最小集合，统计口径与运维预期的单上行口不一致。
- **不阻断理由**：空集保底保证不报恒 0 且 WARN 提示配置 `--include-nics`；该类部署形态相对少见且有显式白名单这一正解路径；已在 `fork-delta.md` P0 条目明示。
- **后续处置条件**：若 VPN 出口节点部署增多，在安装文档/`install.sh` 注释中补充 `--include-nics` 配置指引，或为隧道类接口增加「唯一非 lo 上行口」启发式纳入规则（需先评估误纳入内层隧道的风险）。

### 17. [pattern:statefile-symlink-fresh-load] 状态文件本体为符号链接时按全新账本初始化并在首轮落盘替换为常规文件

- **出处**：`monitoring/unit/traffic_ledger.go` `loadTrafficLedgerLocked`（`os.Lstat` + `Mode().IsRegular()` 非即拒）、`persistTrafficLedgerAtomic`（同目录临时文件 `Rename` 替换链接本体）。
- **问题**：`--traffic-state-file` 或默认路径指向符号链接时，`Lstat` 按「非常规文件」拒绝加载——即使软链目标是一份完全合法的账本状态，也按全新账本初始化；首轮原子落盘的 `Rename` 会用常规文件替换链接本体（软链目标的旧内容不动）。配合账本「自有文件独占」语义这是有意取舍，但运维若习惯把状态文件软链到其它盘会发现「每次启动都是新账本」。
- **不阻断理由**：语义安全且可预期——`Rename` 只替换目录项、绝不写穿软链目标，杜绝账本写入穿透到非预期路径；父目录为软链不受影响（`Lstat` 只检路径末端组件），需跨盘重定向时可软链父目录而非文件本体。
- **后续处置条件**：若部署形态确需文件本体软链，评估在 `Lstat` 非软链拒绝分支前增加 `EvalSymlinks` 后跟检并 WARN 的兼容路径，同时同步 `fork-delta.md` P1 条目。

### 18. [pattern:transient-uptime-unreadable-conservative-zero] Linux 下 `/proc/uptime` 瞬态不可读窗口冷启与跨周期内核重启保守归零

- **出处**：`monitoring/unit/traffic_ledger.go` `kernelUptimeSeconds`（Linux 读取 `<proc>/uptime` 失败返回 `0, false`）、`coldInitLocked` 与 `stepLocked` 跨周期内核重启分支（`env.hasKernelUptime` 守卫）。
- **问题**：SF-R4-1(a) 移除 Linux 穿透 `host.Uptime()` 后，若 `<proc>/uptime` 在月度冷启或跨周期内核重启当拍瞬态不可读，`hasKernelUptime=false` 使账本跳过当前计数播种而从 `0, 0` 起算。
- **不阻断理由**：窗口极窄（内核伪文件本地内存读且恰逢冷启/跨周期重启当拍），且方向为保守少记而非误灌长跑旧账期流量。
- **后续处置条件**：若现场观测到容器挂载重配导致 `<proc>/uptime` 瞬态不可读丢首拍播种，可在未启用 `--host-proc` 时允许同源 `/proc` 二次重试。

### 19. [pattern:transient-subtoken-flap-delta-only] Linux 下 `1/ns/net` 或 `/proc/1/stat` 瞬态不可读引发子字段抖动按差值处理

- **出处**：`monitoring/unit/traffic_ledger.go` `systemBootKey`（按可读子字段动态拼接 `boot_id|ns=...|t=...`）与 `stepLocked` `!countersDefinitelyReset` 分支。
- **问题**：若 `boot_id` 可读而 `1/ns/net` 或 `/proc/1/stat` 单拍瞬态不可读，`curBootKey` 在 `<id>|ns=...|t=...` 与 `<id>` 间往返变化两次；`countersDefinitelyReset` 均判 `false` 走逐卡差值（零多计、总累计连续），但两拍 `roundDelta` 速率报 0、多触发两次落盘，且若第二次切回恰与真实 `netns` 重建同拍重合则因中间态 `prevNetns==""` 保守少记重建初值。
- **不阻断理由**：方向安全（绝不多计），触发概率极低，单拍 0 速率与第 10 条同类。
- **后续处置条件**：若需进一步平滑，可在 `stepLocked` 对「同 `boot_id` 且新 key 仅为旧 key 丢失 `ns=`/`t=` 子段」的降级拍保留旧完整 `BootKey` 不改写。

## 被放弃的方案（必填）

- **方案 A**：在三轮审计中将非阻断观察项一并改代码闭环。放弃原因：均为非阻断项且多为有意的兼容/部署/保守口径权衡，改动会扩大待审 diff 范围、稀释 must-fix/should-fix 修复的可核验性。
- **方案 B**：口头留痕不落盘。放弃原因：违反门禁审查建议项可追溯纪律与 AGENTS.md「决策留痕」铁律，后续上游同步时易遗忘这些权衡的成因。

## 来源

- 主代理组织的 2026-11-08 C 档门禁首轮 oracle 审计结论（分支 `fix/traffic-ledger-and-nic-filter`，基线 `feat/lxc-cgroup-cpu-and-traffic-offset @ 91e8bfc`）。
- 主代理组织的 2026-11-08 C 档终审 reviewer 阻断项审计与同分支 R-M1/R-S2/R-S3/R-S4 修复（第 7 条含主代理 `refuted-with-evidence` 反驳记录）。
- 主代理组织的 2026-11-08 C 档终审第三轮 oracle 审计结论与同分支 S-A/S-B 修复（13~16 条非阻断观察项登记）。
- 主代理组织的 2026-11-08 C 档终审第四轮 oracle 审计结论与同分支 SF-R4-1 / `.corrupt` 改名日志 / 瞬态 `boot_id` 抖动修复（第 17 条非阻断观察项登记）。
- 主代理组织的 2026-11-08 C 档终审第五轮 oracle 审计「无阻断，可放行」结论（第 18~19 条非阻断观察项登记，并同步第 14 条出处引用）。
