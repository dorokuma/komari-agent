---
status: active
superseded_by: ""
supersedes: ""
模块: "server"
---

# make check 跳过上游实网连通性探针

[pattern:upstream-live-network-test]

## 一句话结论

`make check` 中 `go test` 使用 `-skip '^Test(ICMP|TCP|HTTP)Ping$'` 跳过 `server/task_test.go` 的 3 个上游手动连通性探针，其余确定性单测全部保留运行。

## 背景

上游 `server/task_test.go` 中 `TestICMPPing` / `TestTCPPing` / `TestHTTPPing` 硬编码了维护者私用外网节点（`v4/v6-sh-cm.oojj.de`、`2409:8c1e:8f80:2:6a::`、`117.185.125.154`）。本机环境下：`oojj.de` 域名不可解析、无 IPv6 连通性、`117.185.125.154` HTTP 返回非 200（同 IP TCP ping 通过），导致 `go test ./...` 稳定 FAIL，`make check` 无法退 0。本仓为上游 Fork，测试源码不可修改。

## 决策

按 /root/.dev-baseline/README.md §2「内部跑什么由项目决定」，在不触碰上游代码的前提下，用 `go test -skip` 仅跳过这 3 个实网探针（含其子测试）；`cmd`、`monitoring/unit`、`terminal`、`update`、`utils` 等全部确定性单测照常执行。

## 被放弃的方案（必填）

- **修改/删除上游 `task_test.go`**：破坏上游基线，任务与 Fork 定位均不允许。
- **跳过整个 `server` 包**：会连带跳过该包其它本地确定性测试（`task_exec_test.go`、`files_test.go` 等），缩窄检查覆盖。
- **不动、接受 FAIL**：`make check` 无法退 0，L1 基线无法收敛。

## 来源

- 2026-11-09 L1 基线收敛任务（分支 `chore/baseline-l1-make-check`）环境性失败复现与对照实验。
