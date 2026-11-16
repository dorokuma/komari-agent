# Agent 协作指南

档位：L1

## 检查入口
- `make check` 为唯一检查入口，依次执行 `go vet ./...`、`go test -skip '^Test(ICMP|TCP|HTTP)Ping$' ./...`、`go build ./...`；退出码 0 即通过。
- `-skip` 跳过上游 `server/task_test.go` 中硬编码私用外网节点（`oojj.de` / IPv6 / `117.185.125.154`）的 3 个手动连通性探针（见 [.agents/notes/20261109-make-check-skip-upstream-live-ping.md](.agents/notes/20261109-make-check-skip-upstream-live-ping.md)）；其余确定性单测全部保留，不触碰上游测试源码。

## 核心铁律
1. **构建与检查**：全量编译验证也可用 `bash build_all.sh`（多平台矩阵）；日常改动以 `make check` 为准。
2. **决策留痕**：架构决策、踩坑特判、与 upstream 分歧等须记 [.agents/notes/](.agents/notes/)（模板：[.agents/notes/_template.md](.agents/notes/_template.md)）。
3. **仓库安全**：严禁随意修改或提交外部运行时文件；注意当前工作区未提交改动。
4. **远端边界**：`origin` 指向我方自有公开 Fork `dorokuma/komari-agent`（允许推送）；`upstream` 指向上游 `komari-monitor/komari-agent`（只读基准源，已设 `pushurl=DISABLED`，严禁推送）。提交规范、门禁与合并收口等全局规则按 SYSTEM.md §4 执行。

## 红线与部署边界
- **流量账本状态文件禁叶级软链**：`monitoring/unit/traffic_ledger.go`（`:442-459`、`:529-536`、`:676-713`）的 `loadTrafficLedgerLocked` 用 `os.Lstat` 拒绝叶路径符号链接，该次启动转入冷初始化，首次原子 `Rename` 落盘会将软链替换为常规文件。运维跨盘重定向流量账本时必须软链父目录而非状态文件本体。
- **远程控制默认禁用边界**：现网部署统一加 `--disable-web-ssh` 关闭远程终端与文件管理。若未来在任意节点开启远程控制，须先评估修复上游原生 `terminal/terminal.go:103-112,193-228` 断线重连计时器竞态与 `server/files.go:305,333-358` 异名同 inode 硬链接复制截断问题（另外容器仅设 `cpuset.cpus.effective` 未设 `cpu.max` 时 `monitoring/unit/cpu.go:250-266` 仍回退整机在线核数）。

## 索引指针
- 项目文档：[readme.md](readme.md)
- 决策与踩坑笔记：[.agents/notes/](.agents/notes/)
- 索引维护：写完笔记刷新索引：`scripts/notes-index.sh`（本地生成 INDEX.md，不入 git）

## 关联仓库与目录
- **上游仓库**：`komari-monitor/komari-agent`（主干基准源）
- **关联实例**：`komari`（本机运行时实例目录，非 git 仓，勿动）
