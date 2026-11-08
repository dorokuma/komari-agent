---
status: active # active | superseded
superseded_by: ""
supersedes: ""
模块: "cmd, monitoring, update, utils" # 可选模块: cmd, dnsresolver, monitoring, protocol, server, terminal, update, utils, ws
---

# [pattern:closure-record] fix/traffic-ledger-and-nic-filter 合并收口记录

## 一句话结论

`fix/traffic-ledger-and-nic-filter`（流量统计精准化改造：P0 网卡过滤 + P1 单调账本 + P2 默认禁自更新）已于 2026-11-08 以 `--no-ff` 合入 `feat/lxc-cgroup-cpu-and-traffic-offset`，Merge Commit `0a89b8db7cb90f700dae2c7eb8d5541891a6a025`（短 SHA `0a89b8d`）；因 `origin` 指向上游 `komari-monitor/komari-agent` 无推送权，按 AGENTS.md 第 5 条未执行 `git push`，改以本收口记录落盘完成无远端收口。

## 背景

本分支承载流量统计精准化改造全部改动，C 档门禁全部通过（verifier R6 w6K:pX + oracle R6 w6K:pY + reviewer R6 w6K:pZ + jev gate_scan 全通过），随后由获授权 worker 在主仓执行主分支合并与收口。本仓 `origin` 指向上游 `komari-monitor/komari-agent`，为我方无权推送的远端；按 AGENTS.md「远端边界与收口」条款，无任何允许推送的远端时收口改为向 `.agents/notes/` 落盘 `[pattern:closure-record]` 记录。

## 决策

- **Merge Commit**：`0a89b8db7cb90f700dae2c7eb8d5541891a6a025`（短 SHA `0a89b8d`），`git merge --no-ff`，合并消息 `Merge branch 'fix/traffic-ledger-and-nic-filter' into feat/lxc-cgroup-cpu-and-traffic-offset`。
- **合并分支**：`fix/traffic-ledger-and-nic-filter`，合入前分支 HEAD `eb39020794924eb9a09d99adc76d136d4cd206b2`（短 SHA `eb39020`）。
- **合入目标**：`feat/lxc-cgroup-cpu-and-traffic-offset`，合并前基线 `91e8bfc`（完整 SHA `91e8bfccba171724318c6f5423d9c5d8c3847a08`）。
- **门禁档位**：C 档（verifier R6 w6K:pX + oracle R6 w6K:pY + reviewer R6 w6K:pZ + jev gate_scan 全通过）。
- **远端边界**：`origin` 指向上游 `komari-monitor/komari-agent` 无推送权，按 AGENTS.md 第 5 条未执行 `git push`；本笔记即无远端收口记录。
- **清理动作**：worktree `/root/workspace/.wt/komari-agent/fix-traffic-ledger-and-nic-filter` 已 `git worktree remove`；分支 `fix/traffic-ledger-and-nic-filter` 已 `git branch -d` 删除；`.wt` 登记 JSON `fix-traffic-ledger-and-nic-filter.json` 已更新为 `status: "merged"`、`last_pane: "w6K:p11"`。
- **遗留观察项**：C 档门禁五轮 oracle 审计共 19 条非阻断 `[pattern:...]` 观察项已归档于 `.agents/notes/20261108-traffic-ledger-consider-leftovers.md`（条目 1~19，含第 7 条主代理 `refuted-with-evidence` 取舍记录），清单如下：
  - 1 `[pattern:explicit-autoupdate-escape-hatch]` 显式开启自更新作为切回上游逃生门
  - 2 `[pattern:deprecated-flag-noop]` 历史废弃 `-autoUpdate` 保持仅告警无副作用
  - 3 `[pattern:legacy-migration-cwd-precedence]` 旧 `net_static.json` 迁移优先检查 cwd
  - 4 `[pattern:atomic-rename-without-dir-fsync]` 原子落盘未额外 fsync 父目录
  - 5 `[pattern:legacy-helper-retained-for-tests]` 保留历史辅助函数供单测与上游合并兼容
  - 6 `[pattern:mutex-held-during-state-sync]` `ledgerMu` 下每 30 秒同步本地 JSON
  - 7 `[pattern:cross-cycle-downtime-unsplit-counter]` 同代际跨月停机恢复归零重建基线
  - 8 `[pattern:corrupt-backup-single-slot]` `.corrupt` 单槽备份二次损坏覆盖
  - 9 `[pattern:empty-samples-epoch-switch-baseline]` 代际切换恰逢空 samples 的瞬态基线边界
  - 10 `[pattern:zero-speed-on-epoch-switch]` 周期/代际切换当拍速率报 0
  - 11 `[pattern:host-uptime-conservative-seeding]` 冷启统一按内核/lxcfs uptime 保守判定
  - 12 `[pattern:exe-dir-empty-cwd-fallback]` `executableDir()` 失败回退裸文件名
  - 13 `[pattern:host-proc-isolated-netns-bootkey]` `--host-proc` 搭配独立 netns 的代际标识边界
  - 14 `[pattern:empty-bootkey-recovery-adopts-current]` `BootKey==""` 恢复可读后静默采纳当前代际
  - 15 `[pattern:iocounters-transient-error-zero-return]` `IOCounters` 瞬态报错返回 0+err 不污损账本
  - 16 `[pattern:vpn-uplink-requires-include-nics]` 纯 VPN 出口节点须显式 `--include-nics`
  - 17 `[pattern:statefile-symlink-fresh-load]` 状态文件本体为软链时按全新账本初始化
  - 18 `[pattern:transient-uptime-unreadable-conservative-zero]` Linux `/proc/uptime` 瞬态不可读窗口保守归零
  - 19 `[pattern:transient-subtoken-flap-delta-only]` `1/ns/net`/`/proc/1/stat` 瞬态不可读引发 boot_key 子字段抖动按差值处理
- **遗留事项**：上述 19 条观察项按各自「后续处置条件」在后续任务中重估；无阻断遗留。

## 被放弃的方案（必填）

- **方案 A**：合并后向 `origin`（上游 `komari-monitor/komari-agent`）执行 `git push`。放弃原因：origin 指向上游而非我方远端，按 AGENTS.md 远端边界「绝推不动我方无权的远端」，推送属越权操作；无其它允许推送的远端。
- **方案 B**：合并后在主分支再 `git commit` 收口记录。放弃原因：任务硬边界明确禁止合并后在主分支额外 commit；收口记录按 SYSTEM.md §4 无远端条款以未跟踪笔记文件落盘即可，与既有收口先例（主目录仅用于查看主分支与最后合并）一致。

## 来源

- 主代理 2026-11-08 合并收口任务书 `[MARK-WORKER-MERGE-20261108]`（C 档门禁全绿后的获授权主仓合并任务）。
- 合并执行：主仓 `/root/workspace/komari-agent` 于 `feat/lxc-cgroup-cpu-and-traffic-offset`（合并前 `91e8bfc`）执行 `git merge --no-ff`，生成 Merge Commit `0a89b8d`。
- worktree `/root/workspace/.wt/komari-agent/fix-traffic-ledger-and-nic-filter` 已移除；`.wt` 登记文件 `fix-traffic-ledger-and-nic-filter.json` 已更新 `status: "merged"`。
- 19 条非阻断观察项归档：`.agents/notes/20261108-traffic-ledger-consider-leftovers.md`。
