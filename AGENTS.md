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
- **远程控制默认禁用边界**：现网部署统一加 `--disable-web-ssh` 关闭远程终端与文件管理。上游原生 `terminal/terminal.go` 断线重连计时器竞态已于 2026-11-25 同步上游 `0be05ea`（`timerGen` 定时世代、404 速停与 `tty` 关闭修复）并叠加 Fork 加固闭环：定时器到期回调与恢复路径的 TOCTOU 竞态已由世代原子校验的 `closeSessionWithGen` 修复、WebSocket 数据帧/控制帧写均已设有限写超时避免 `writeMu` 活性死锁；`agent.startupConfig` 回传已对 `token`/`auto_discovery_key` 脱敏且在途并发受限，`--disable-web-ssh` 下终端拨号至多单次交付 terminate 绝不进入 5 分钟重试、空 `request_id` 直接拒绝。若未来在任意节点开启远程控制，仍须先评估修复 `terminal/terminal_unix.go` 中 `pty.Setsize` 触发 `SetBlocking` 导致后台逃逸 pgid 子进程持有从端时 PTY 主端 FD/`readOutput` 协程泄漏，以及 `server/files.go:305,333-358` 异名同 inode 硬链接复制截断问题（另外容器仅设 `cpuset.cpus.effective` 未设 `cpu.max` 时 `monitoring/unit/cpu.go:250-266` 仍回退整机在线核数）。

## 索引指针
- 项目文档：[readme.md](readme.md)
- 决策与踩坑笔记：[.agents/notes/](.agents/notes/)
- 索引维护：写完笔记刷新索引：`scripts/notes-index.sh`（本地生成 INDEX.md，不入 git）

## 关联仓库与目录
- **上游仓库**：`komari-monitor/komari-agent`（主干基准源）
- **关联实例**：`komari`（本机运行时实例目录，非 git 仓，勿动）
## Release Policy / 发布规范

本节是本仓库发版的强制规则。仓库已有的更具体版本、构建或发布渠道规则优先，但不得降低以下底线。

1. **版本与变更记录**：遵循 Semantic Versioning，正式版本使用 `vX.Y.Z`。破坏性变更升 MAJOR，新功能升 MINOR，兼容修复升 PATCH。任何用户可见行为、运行时/API/包接口、依赖、安全性或分发产物变化，都必须在同一发版提交中更新版本元数据并写入 CHANGELOG/Release Notes；纯文档且不产生可分发产物时可免升版，但须在规则或 Release Notes 中明确说明。
2. **发布门禁**：发版前工作树必须干净，版本号、CHANGELOG 与发布说明必须一致；必须运行仓库规定的测试、构建、lint、类型检查及必要的安全检查，且全部通过。若仓库提供 `release-check`、`make release-check`、版本检查脚本或同等门禁，必须原样通过，不得绕过。
3. **发布顺序**：先提交并推送发布提交到目标分支 → 在该发布提交上创建 Tag（默认使用带注解的 `git tag -a vX.Y.Z -m "vX.Y.Z"`；仅当现有发布工具明确要求轻量 Tag 时可例外）→ 推送 Tag → 创建**非草稿** GitHub Release → 再发布 npm、PyPI、容器镜像等关联分发渠道。不得只推分支或只打 Tag 就宣称发版完成。
4. **GitHub Release 产物**：标题使用 `vX.Y.Z`，正文必须包含实质性变更摘要、升级/破坏性变更说明及验证结果；二进制、安装包、插件包等适用产物必须作为 Release assets 上传，并提供校验和（如适用）。**Tag 本身不等于完整发版，必须同时存在可访问的 GitHub Release。**
5. **发布后验收**：至少执行并确认 `git ls-remote --tags origin refs/tags/vX.Y.Z`、Tag 指向发布提交、`gh release view vX.Y.Z` 可访问且不是 draft；若发布到包仓库/镜像仓库，还必须核验对应版本、摘要或安装链接可用。
6. **不可变性**：已发布 Tag 不得复用、移动或删除，不得 force push 发布分支或 Tag，不得从未提交、未经测试或未通过门禁的树发布。发布后发现问题须通过新的 patch 版本修复，禁止覆盖历史 Release。
7. **例外必须显式化**：归档、已停用、被替代、仅文档或无可分发产物的仓库，必须在项目规则中明确写出“不发新版本”及历史 Tag/Release 的处置方式；例外不能用于绕过上述发布闭环。

### Repository-specific release contract

- 本仓的具体检查命令以 README、CONTRIBUTING、AGENTS 既有条款及 CI 配置为准。
