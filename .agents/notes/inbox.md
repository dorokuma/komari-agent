# 观察项收件箱（Inbox）

- `[pattern:statefile-symlink-fresh-load] | type:redline | added:2026-11-08`
  `monitoring/unit/traffic_ledger.go`（`:442-459`、`:529-536`、`:676-713`）的 `loadTrafficLedgerLocked` 用 `os.Lstat` 拒绝叶路径符号链接，使路径末端为软链的该次启动不加载目标旧账本而转入冷初始化（按模式播种/迁移）；首次成功落盘经原子 `Rename` 将软链本体替换为常规文件后，后续重启即正常加载该常规文件。运维跨盘重定向流量账本时必须软链父目录而非状态文件本体，避免首次切换丢历史账本并断开原软链。
- `[pattern:cgroup-cpuset-unbounded-quota] | type:debt | added:2026-11-08`
  `monitoring/unit/cpu.go:250-266`（在线核数回退 `:258-266`，`max` 解析 `:274-275`）中 `cgroupCPULimit` 在容器 cgroup v2 `cpu.max` 为 `max` 时直接回退整机在线核数，未计入 `cpuset.cpus.effective` 绑核限核场景；后续若部署仅绑核不设 `cpu.max` 的节点需补该次级回退分母。
- `[pattern:reconnect-timer-generation-race] | type:debt | added:2026-11-08`
  `terminal/terminal.go:103-112,193-228`（上游原生代码，仅未设 `--disable-web-ssh` 启用远程控制时可达；现网节点均已禁用）：断线保留计时器回调已触发并等待 `sessionsMu` 期间若 `acquireSession` 先重连换入新连接，`Stop` 未取消已触发回调，随后 `closeSession` 会误删同一会话并关闭新连接。
- `[pattern:copy-hardlink-self-truncate] | type:debt | added:2026-11-08`
  `server/files.go:305,333-358`（上游原生代码，仅未设 `--disable-web-ssh` 启用远程控制时可达；现网节点均已禁用）：`copyPath` 仅比较路径字符串而 `copyRegularFile` 未做 `os.SameFile` 校验，复制到异名同 inode 硬链接目标时 `O_TRUNC` 会截断清空源文件。
