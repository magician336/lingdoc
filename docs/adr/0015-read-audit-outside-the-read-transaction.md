# 15. 读类审计写在读的事务之外

**状态**：已采纳（2026-10-27，由 Issue [#387](https://github.com/magician336/lingdoc/issues/387)（PF-3 残留）与 Spec [#130](https://github.com/magician336/lingdoc/issues/130) 产生）

**修订关系**：本决策**修订** [ADR 0011](0011-audit-failure-alert-on-its-own-transaction.md) 末尾「PostgreSQL 上读类审计失败会让读一并失败另见 #387」这条待办——它给出该缺口的处置。ADR 0011 的告警事务归属、触发边界与接线点**不变**。

## 背景

Spec 要求「普通读取的审计写入失败采用 fail-open：继续读取，并发一条运维告警」

[29-Spec](../06-灵档产品开发规划/29-Spec-G5审计与权限可见性.md):32。ADR 0011 解决了**告警写在哪个事务**，但没有解决**审计写在哪个事务**。

读类审计原先与业务读共用调用方事务：
`readWithAudit` 在 `repository.Transaction` 内部调用 `recordAuditInTransaction`
（`internal/lingdoc/workspacecore/service_facade.go`），GORM 分支直接
`tx.(auditTransaction).RecordAudit(event)`。

- **SQLite**：`RAISE(ABORT)` 只撤销失败语句，事务仍可用 ⇒ fail-open 成立；
- **PostgreSQL**（生产方言）：任一语句失败即 aborted，`COMMIT` 变成 rollback
  ⇒ **读本身失败**（实测：`commit unexpectedly resulted in rollback`）。

丢弃审计函数的返回值救不了这一点：被污染的是事务，不是返回值。于是
「审计 sink 故障 ⇒ 读还能用」在 SQLite 成立、在生产库不成立——同一条承诺在两种
方言上给出两种行为，而这正是最容易在评审里漏掉的一类偏差。

`machine.read` 的读类审计早已是正确形状：业务读结束后，在**独立事务**里写审计
（`internal/lingdoc/workspacecore/machine_read_grant.go` 的 `auditMachineRead`）。

## 决策

### 决策一：读类审计在业务读的事务**结束之后**，用独立事务写入

`readWithAudit` 只负责业务读；事务返回后，`recordReadAudit` 另开一个事务写审计行。
PostgreSQL 上审计失败只 abort 它自己的事务，业务读已完成；SQLite 上也不会死锁
（既有约束：单连接下业务事务未结束时另开事务会阻塞到超时）。

### 决策二：读与审计**不再原子**，读的结论是权威，审计是尽力而为

读不改状态，因此「读成功、审计行缺失」是可接受的降级；反过来的「审计坏了就读不了」
不可接受——那是把可观测性故障升级成业务故障。

丢失窗口写清楚：进程若在业务读完成与审计事务提交之间崩溃，这次读不会留下审计行。
这不是新风险——审计 sink 失败时本来就会丢行——区别只是它不再连带业务失败。

### 决策三：只有**读类**入口走这条接缝；写路径不变

`Authorize` / `GetProject` / `AuthorizeRequest` 走 `readWithAudit`。
写路径继续 `recordAuditInTransaction`，审计失败即操作失败（fail-closed），
与 ADR 0011 决策二一致。`AuthorizeResourceBatch` 保持既有 fail-closed 语义：
它的文档明确「调用方不得基于没有人记录的决策行动」，不属本决策范围。

### 决策四：不用 `SAVEPOINT`

PG 支持、SQLite 不支持，会引入方言分支；而且「事件已经发生但保存点回滚」仍要
在应用层决定读与审计谁赢。独立事务把这个选择只写一次。

### 决策五：审计失败仍按 ADR 0011 告警

`recordReadAudit` 的错误不返回给调用方，交给 `EmitAuditFailureAlert` 发布运行事件。
告警自己的事务仍与审计事务分离，理由见 ADR 0011 决策一。

## 后果

- **守护测试**：`TestAuditFailurePublishesRuntimeEventAndAlertOnPostgres`
  （`internal/lingdoc/workspacecore/audit_failure_alert_test.go`）在 PostgreSQL 上装
  `BEFORE INSERT` 触发器让审计写入必失败，断言 `Authorize` 与 `GetProject` 仍成功、
  告警仍产生。无 `LINGDOC_TEST_POSTGRES_DSN` 时该用例显式 `Skip("NOT RUN: …")`，
  不冒充通过。
- **SQLite 守护**：`TestAuditFailurePublishesRuntimeEventAndAlert` 同一注入下继续通过，
  两种方言的读行为一致。
- **权限判定不变**：`TestAuditFailureDoesNotChangeTheAuthorizationDecision`
  钉住「越权仍拒绝」；本决策只改审计事务归属，不改判定。
- **审计与读的顺序变化**：先读后审计。需要「先审计后行动」的写路径不在本接缝。
- 术语：本决策里的「读类审计」指 `Authorize` / `GetProject` / `AuthorizeRequest`
  记录的授权判定审计；「写类审计」指与业务写同事务、失败即回滚的审计。
