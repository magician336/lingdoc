package workspacecore

import (
	"context"
	"testing"
	"time"
)

// 受控故障注入命中之后的写必须还能完成（#310）。
//
// 缺陷现象：组合根装配了受控故障注入之后，注入命中的那笔事务把后续所有写一起
// 卡住——请求既不返回也不报错。本用例就是那个最小复现：
//
//  1. 在**真实 SQLite** 上装配注入（连接池压到生产同款的一条连接：SQLite 只允许
//     一个写者，生产把连接池限成一条）；
//  2. 触发一次注入（apply 返回注入失败）；
//  3. **紧接着**再做一次写——它必须正常完成。
//
// 如果注入失败的那笔事务没有干净回滚、没有把连接归还连接池，第 3 步就会一直等
// 那唯一一条连接。用例把这一步放进 goroutine 并配超时，于是"挂住"变成一次明确
// 的失败，而不是让整个测试套件卡死。
//
// 这些用例同时钉住回滚读回：注入命中的那一笔整体未生效（项目 revision、模板
// 引用、ImpactTask 集合、业务审计事件都与注入前一致）。

// faultWriteTimeout 是"写必须完成"的判定窗口。生产里的写是毫秒级的，20 秒足够
// 区分"完成"与"挂住"。
const faultWriteTimeout = 20 * time.Second

// faultWriteFixture 落一个真实 SQLite 上可应用的变更集，并把连接池压到生产同款。
func faultWriteFixture(t *testing.T) (*Service, Actor, Project, Chapter, ChangeSet) {
	t.Helper()
	svc, actor, p := extendedImpactStore(t)
	chapter := extendedChapter(t, svc, actor, p)
	change := extendedChange(t, svc, actor, p, chapter)
	pool, err := svc.testDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	// 生产同款：SQLite 分支把连接池限成一条连接
	// （internal/container/container.go 的 sqlite 分支）。注入失败后如果连接
	// 没有归还，紧跟着的写就会一直等它。
	pool.SetMaxOpenConns(1)
	return svc, actor, p, chapter, change
}

// writeWithin 跑一次写并要求它在窗口内完成。挂住时用例会明确失败。
func writeWithin(t *testing.T, what string, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(faultWriteTimeout):
		t.Fatalf("%s 在 %s 内没有完成：注入命中的事务没有干净回滚（连接没有归还连接池）", what, faultWriteTimeout)
		return nil
	}
}

// assertFaultRollbackUnchanged 断言注入命中的那一笔整体未生效。
func assertFaultRollbackUnchanged(t *testing.T, svc *Service, actor Actor, projectID string, before Project, tasksBefore int, auditsBefore int) {
	t.Helper()
	after, err := svc.GetProject(context.Background(), actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CurrentContextRevision != before.CurrentContextRevision || after.ProjectVersion != before.ProjectVersion || after.SpecRevision != before.SpecRevision {
		t.Fatalf("失败的应用留下了半完成状态\nbefore=%+v\nafter=%+v", before, after)
	}
	if after.TemplateCopyVersion != before.TemplateCopyVersion || after.TemplateID != before.TemplateID || after.TemplateVersion != before.TemplateVersion {
		t.Fatalf("失败的应用改动了模板引用: before=%+v after=%+v", before, after)
	}
	if got := countImpactTasks(t, svc, actor, projectID); got != tasksBefore {
		t.Fatalf("失败的应用留下了 ImpactTask: %d -> %d", tasksBefore, got)
	}
	if got := countApplyAudits(t, svc, actor, projectID); got != auditsBefore {
		t.Fatalf("失败的应用留下了业务审计事件: %d -> %d", auditsBefore, got)
	}
}

// TestInjectedImpactTaskWriteLeavesTheNextWriteRunnable 是 #310 的核心回归：
// 注入命中之后紧接着的一次写必须正常完成。
func TestInjectedImpactTaskWriteLeavesTheNextWriteRunnable(t *testing.T) {
	svc, actor, p, chapter, change := faultWriteFixture(t)
	svc.InstallFaultInjection(FaultInjection{Point: FaultPointImpactTaskWrite, TenantID: actor.TenantID, Reason: "合成注入：ImpactTask 写入不可用"})
	ctx := context.Background()
	before, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasksBefore := countImpactTasks(t, svc, actor, p.ID)
	auditsBefore := countApplyAudits(t, svc, actor, p.ID)

	if _, _, _, err := svc.ApplyChangeSet(ctx, actor, p.ID, change.ID, "issue310-impact-apply"); err == nil || !IsInjectedFault(err) {
		t.Fatalf("注入的任务写入失败没有让 apply 失败: %v", err)
	}
	// 先读回：注入命中的那一笔整体未生效。
	assertFaultRollbackUnchanged(t, svc, actor, p.ID, before, tasksBefore, auditsBefore)

	// 紧跟着的第一笔写：建项目（票面记录的最小复现就是它）。它要的是同一条
	// 连接——如果注入那笔事务把连接扣住了，它就会挂在这里。
	if err := writeWithin(t, "注入命中之后的建项目", func() error {
		_, _, _, err := svc.CreateProject(ctx, actor, "issue310-impact-project", CreateProjectInput{Name: "注入之后的项目", TemplateID: "template-demo"})
		return err
	}); err != nil {
		t.Fatalf("注入命中之后的建项目没有完成: %v", err)
	}
	// 再写同一个项目的一笔：证明被扣住的不是"只有新项目"这条路径。
	if err := writeWithin(t, "注入命中之后的章节版本", func() error {
		_, _, _, err := svc.SaveChapter(ctx, actor, p.ID, chapter.ID, "issue310-impact-chapter", SaveChapterInput{
			ExpectedChapterVersionID: chapter.CurrentVersionID,
			ExpectedSpecRevision:     before.SpecRevision,
			BodyMarkdown:             "合成注入之后的章节版本",
			SourceIDs:                []string{},
		})
		return err
	}); err != nil {
		t.Fatalf("注入命中之后的章节版本没有完成: %v", err)
	}
}

// TestInjectedAuditWriteLeavesTheNextWriteRunnable 覆盖另一个注入点：业务审计
// 是 apply 事务的最后一次写入，失败时回滚要连同 revision 推进与任务行一起撤销。
func TestInjectedAuditWriteLeavesTheNextWriteRunnable(t *testing.T) {
	svc, actor, p, chapter, change := faultWriteFixture(t)
	svc.InstallFaultInjection(FaultInjection{Point: FaultPointAuditWrite, TenantID: actor.TenantID, Action: "project.change_set.apply", Reason: "合成注入：业务审计写入不可用"})
	ctx := context.Background()
	before, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasksBefore := countImpactTasks(t, svc, actor, p.ID)
	auditsBefore := countApplyAudits(t, svc, actor, p.ID)

	if _, _, _, err := svc.ApplyChangeSet(ctx, actor, p.ID, change.ID, "issue310-audit-apply"); err == nil || !IsInjectedFault(err) {
		t.Fatalf("注入的审计写入失败没有让 apply 失败: %v", err)
	}
	assertFaultRollbackUnchanged(t, svc, actor, p.ID, before, tasksBefore, auditsBefore)

	if err := writeWithin(t, "注入命中之后的建项目", func() error {
		_, _, _, err := svc.CreateProject(ctx, actor, "issue310-audit-project", CreateProjectInput{Name: "注入之后的项目", TemplateID: "template-demo"})
		return err
	}); err != nil {
		t.Fatalf("注入命中之后的建项目没有完成: %v", err)
	}
	if err := writeWithin(t, "注入命中之后的章节版本", func() error {
		_, _, _, err := svc.SaveChapter(ctx, actor, p.ID, chapter.ID, "issue310-audit-chapter", SaveChapterInput{
			ExpectedChapterVersionID: chapter.CurrentVersionID,
			ExpectedSpecRevision:     before.SpecRevision,
			BodyMarkdown:             "合成注入之后的章节版本",
			SourceIDs:                []string{},
		})
		return err
	}); err != nil {
		t.Fatalf("注入命中之后的章节版本没有完成: %v", err)
	}
}

// TestInjectedAuthorizationLeavesTheNextWriteAnswered 覆盖授权类注入：这个注入点
// 让该租户的每一类动作都失败，所以紧跟着的写**不会成功**——但它必须**有答复**，
// 而不是挂住。挂住说明事务或连接没有干净归还。
func TestInjectedAuthorizationLeavesTheNextWriteAnswered(t *testing.T) {
	svc, actor, p, _, change := faultWriteFixture(t)
	// 授权类注入按动作限定到 manage：读与建项目仍然可用，只有 apply（需要 manage）
	// 会失败。这样"注入命中之后的写"仍然能完成，而 apply 之后紧跟着的读也能读回。
	fault := FaultInjection{Point: FaultPointAuthorization, TenantID: actor.TenantID, Action: "manage", Reason: "合成注入：授权失效"}
	svc.authorizer = NewFaultInjectingAuthorizationPolicy(svc.authorizer, fault)
	ctx := context.Background()
	before, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasksBefore := countImpactTasks(t, svc, actor, p.ID)
	auditsBefore := countApplyAudits(t, svc, actor, p.ID)

	if _, _, _, err := svc.ApplyChangeSet(ctx, actor, p.ID, change.ID, "issue310-auth-apply"); err == nil || !IsInjectedFault(err) {
		t.Fatalf("注入的授权失效没有让 apply 失败: %v", err)
	}
	assertFaultRollbackUnchanged(t, svc, actor, p.ID, before, tasksBefore, auditsBefore)

	// 紧跟着的写必须完成（授权失效只点名 manage，建项目这一类写仍然可用）。
	if err := writeWithin(t, "注入命中之后的建项目", func() error {
		_, _, _, err := svc.CreateProject(ctx, actor, "issue310-auth-project", CreateProjectInput{Name: "注入之后的项目", TemplateID: "template-demo"})
		return err
	}); err != nil {
		t.Fatalf("注入命中之后的建项目没有完成: %v", err)
	}
	// 读回同样必须有答复。
	if _, err := svc.GetProject(ctx, actor, p.ID); err != nil {
		t.Fatalf("注入命中之后的读回失败: %v", err)
	}
}

// recordingReadTransaction 是一个"项目读会被记下来、并且读不出来"的事务替身。
//
// 它用来证明两件事：受控失败的实现**不读**存储；以及即便项目读不出来，注入仍然
// 会命中（旧实现把读不到项目当成"不在作用域"，注入会静默地不生效）。
type recordingReadTransaction struct {
	Transaction
	reads int
}

func (tx *recordingReadTransaction) Project(uint64, string) (Project, error) {
	tx.reads++
	return Project{}, ErrNotFound
}

// TestInjectedFaultWriterPerformsNoStorageAccess 钉住 #310 的关键性质：受控失败
// 的实现**不碰存储**。
//
// 旧实现用「在同一笔写事务里读项目行」来判定注入作用域。那次读发生在事务已经
// 推进 revision 之后，也就是事务已经握住写锁的时候；读一旦等锁，注入就从"一次
// 干净的失败"变成"一个卡住的请求"，而读的错误又被当成"不在作用域"吞掉——注入
// 会静默地不生效。作用域改由写自己的租户判定，故障缝隙因此完全不做存储访问。
func TestInjectedFaultWriterPerformsNoStorageAccess(t *testing.T) {
	fault := FaultInjection{Point: FaultPointImpactTaskWrite, TenantID: 42, Reason: "合成注入"}
	tx := &recordingReadTransaction{}
	writer := faultImpactTaskWriter{fault: fault, inner: transactionImpactTaskWriter{}}
	err := writer.WriteImpactTask(tx, 42, ImpactTask{ProjectID: "p-1"})
	if err == nil || !IsInjectedFault(err) {
		t.Fatalf("作用域租户命中时没有返回注入失败: %v", err)
	}
	if tx.reads != 0 {
		t.Fatalf("注入失败实现读取了存储 %d 次：故障缝隙必须在不触碰存储的前提下失败", tx.reads)
	}
	// 另一个租户的写仍然走真实写入路径。
	other := &recordingReadTransaction{}
	otherWriter := faultImpactTaskWriter{fault: fault, inner: transactionImpactTaskWriter{}}
	if err := otherWriter.WriteImpactTask(other, 43, ImpactTask{ProjectID: "p-1"}); err == nil {
		t.Fatal("非演示租户的写没有走到真实写入路径")
	}
	if other.reads != 0 {
		t.Fatalf("非演示租户的写也读了存储 %d 次", other.reads)
	}

	auditTx := &recordingReadTransaction{}
	auditWriter := faultBusinessAuditWriter{fault: fault, inner: transactionBusinessAuditWriter{}}
	if err := auditWriter.WriteBusinessAudit(auditTx, AuditEvent{TenantID: 42, Action: "project.change_set.apply"}); err == nil || !IsInjectedFault(err) {
		t.Fatalf("作用域租户命中时审计写入没有返回注入失败: %v", err)
	}
	if auditTx.reads != 0 {
		t.Fatalf("注入失败实现读取了存储 %d 次：故障缝隙必须在不触碰存储的前提下失败", auditTx.reads)
	}
}
