package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"gorm.io/gorm"
)

// 交付链 T12→T13 的验收。这条链此前一段都没接上：delivery 包在包外零 importer，
// T12 的 WorkspaceDeliveryInput 零消费者，于是 T13 的检查与冻结没有任何生产调用者。
// 下面的用例走的就是那条链：T12 的库 → 交付输入 → 冻结来源 → T13 的检查与冻结。

const (
	deliveryActorID = "reader"
	// 与 seedBoundSource 落下的那一条引用同名：链路两端的 fixture 是同一份。
	deliverySourceID  = "source-1"
	deliveryChapterID = "chapter-1"
	deliveryVersionID = "version-1"
	deliveryReviewID  = "review-1"
	// 正文里的 [[source:…]] 标记必须与 source_ids 完全一致，否则 T13 判 citation_mismatch
	// ——那会盖住本用例真正要看的结论。
	deliveryBody = "研究问题：演示资料包含虚构记录 [[source:source-1]]，不能当作真实结论。"
)

// deliveryFixture 是一次链路测试的工作区：项目、章节、确认记录都按 T13 能读完
// 的那一套落库。projectVersion 与 specRevision 要与调用方声称的一致（契约里
// expected_project_version 就是这个版本号）。
const (
	deliveryProjectVersion = 8
	deliverySpecRevision   = 1
)

// newDeliveryReleaseService 把链路装起来。成员校验在这里放行：本用例测的是
// T12→T13，成员判定归 T07（容器里由 candidateAdoptionWorkspaceAuthorizer 承担）。
func newDeliveryReleaseService(t *testing.T, handler *Handler) *DeliveryReleaseService {
	t.Helper()
	inputs := &candidateadoption.DeliveryInputService{
		Reader:     candidateadoption.NewSQLiteCandidateAdoptionStore(handler.db),
		Authorizer: deliveryTestAuthorizer{},
	}
	builder := handler.DeliveryInputBuilder()
	if builder == nil {
		t.Fatal("装配处拿不到交付输入构建器")
	}
	service := NewDeliveryReleaseService(inputs, builder, delivery.NewMemorySnapshotStore())
	if service == nil {
		t.Fatal("交付链装配不齐：T12→T13 仍是断的")
	}
	return service
}

type deliveryTestAuthorizer struct{}

func (deliveryTestAuthorizer) AuthorizeProject(context.Context, string, string) error { return nil }

// deliveryChapter 是一个章节及其不可变版本与确认记录。确认里的资料版本必须与
// 章节冻结来源完全一致——T13 拿这条等式判 confirmation_asset_versions_mismatch。
type deliveryChapter struct {
	id, sectionID, title, versionID string
	body                            string
	sourceIDs                       []string
	items                           []candidateadoption.ReviewItem
	assetVersions                   []candidateadoption.AssetVersion
	decisions                       []candidateadoption.ReviewDecision
}

// deliveryQuestionChapter 是那条带引用的章节；deliveryMethodChapter 是它的陪衬：
// 演示模板把 question 与 method 两节都标成必填，缺一节 T13 就报 required_chapter_missing，
// 盖住本用例真正要看的结论。它与发布样例里的 ch-method 同形——有版本、有确认、没有引用。
func deliveryQuestionChapter(assetID string, revision int, body string, sourceIDs []string) deliveryChapter {
	return deliveryChapter{
		id: deliveryChapterID, sectionID: "question", title: "研究问题", versionID: deliveryVersionID,
		body: body, sourceIDs: sourceIDs,
		items:         []candidateadoption.ReviewItem{{ID: deliveryReviewID, Statement: "100条合成记录不能证明真实研究结论。", OriginCandidateID: "candidate-1"}},
		assetVersions: []candidateadoption.AssetVersion{{AssetID: assetID, AssetRevision: revision}},
		decisions:     []candidateadoption.ReviewDecision{{ReviewItemID: deliveryReviewID, Disposition: "retained_warning", Reason: "仅作内部演示，保留该项并随文件明确列出。"}},
	}
}

func deliveryMethodChapter() deliveryChapter {
	return deliveryChapter{
		id: "chapter-2", sectionID: "method", title: "研究方案", versionID: "version-2",
		body: "研究方案：使用合成记录检验软件流程，结果仅用于内部演示。",
	}
}

// seedDeliveryWorkspace 在 T12 的库里落一个可交付的工作区。
// 生产里这些表由 000018/000020 号迁移建（见 candidate_adoption_store.go 的 AutoMigrate 注释）。
func seedDeliveryWorkspace(t *testing.T, db *gorm.DB, chapters ...deliveryChapter) {
	t.Helper()
	if err := candidateadoption.NewSQLiteCandidateAdoptionStore(db).AutoMigrate(context.Background()); err != nil {
		t.Fatalf("migrate lingdoc tables: %v", err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if err := db.Exec(query, args...).Error; err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	createdAt := time.Date(2026, time.September, 26, 9, 0, 0, 0, time.UTC)

	// 项目行由脚手架的 newSourceCurrentnessHandler 落下（id=project-1），这里把它推进到
	// 交付 fixture 的版本：租户 7、模板 template-demo/1，研究条件补齐。
	exec(
		"UPDATE lingdoc_projects SET name = ?, project_version = ?, spec_revision = ?, spec_json = ?, template_id = ?, template_version = ? WHERE id = ?",
		"合成资料演示项目", deliveryProjectVersion, deliverySpecRevision,
		`{"research_subject":"公开的合成调查样本","research_goal":"验证资料到章节的工作流程"}`,
		delivery.DemoTemplateID, delivery.DemoTemplateVersion, "project-1",
	)

	for _, chapter := range chapters {
		exec(
			"INSERT INTO lingdoc_chapters (id, project_id, section_id, title, current_version_id) VALUES (?, ?, ?, ?, ?)",
			chapter.id, "project-1", chapter.sectionID, chapter.title, chapter.versionID,
		)
		// 正文与引用写在不可变版本上：T12 的读取侧正是从这里取 body 与 source_ids。
		sourceIDs, err := json.Marshal(orEmpty(chapter.sourceIDs))
		if err != nil {
			t.Fatal(err)
		}
		reviewItems, err := json.Marshal(orEmpty(chapter.items))
		if err != nil {
			t.Fatal(err)
		}
		exec(
			"INSERT INTO lingdoc_chapter_versions (id, project_id, chapter_id, body_markdown, source_ids_json, review_items_json, spec_revision, confirmation_valid, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			chapter.versionID, "project-1", chapter.id, chapter.body, string(sourceIDs), string(reviewItems),
			deliverySpecRevision, true, deliveryActorID, createdAt,
		)

		// 确认记录：T12 的读取侧要求它与章节版本、研究条件、模板版本都一致才交出来
		// （candidateadoption/delivery_input.go:141）；T13 会把同一组等式再判一遍。
		confirmation, err := json.Marshal(candidateadoption.Confirmation{
			ID: "confirm-" + chapter.id, ChapterID: chapter.id, ChapterVersionID: chapter.versionID,
			SpecRevision:    deliverySpecRevision,
			AssetVersions:   orEmpty(chapter.assetVersions),
			TemplateVersion: delivery.DemoTemplateVersion,
			ActorUserID:     "u-owner",
			CreatedAt:       createdAt,
			Valid:           true,
			ReviewDecisions: orEmpty(chapter.decisions),
		})
		if err != nil {
			t.Fatal(err)
		}
		exec(
			"INSERT INTO lingdoc_chapter_confirmations (id, chapter_id, chapter_version_id, valid, details_json, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			"confirm-"+chapter.id, chapter.id, chapter.versionID, true, string(confirmation), createdAt,
		)
	}
}

// orEmpty 让空集合落成 [] 而不是 null：契约里集合一律是数组，T12 的读取侧也按
// 数组解回来——null 会解成 nil，再被上层当成「这一章没有引用」之外的另一种东西。
func orEmpty[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// frozenChapter 按 ID 取冻结后的章节：T12 的读取侧按 section_id 排序，
// 所以位置不表示任何语义，用例里不能按下标认章节。
func frozenChapter(t *testing.T, input delivery.DeliveryInput, chapterID string) delivery.SnapshotChapter {
	t.Helper()
	for _, chapter := range input.Chapters {
		if chapter.ChapterID == chapterID {
			return chapter
		}
	}
	t.Fatalf("frozen input has no chapter %s", chapterID)
	return delivery.SnapshotChapter{}
}

// currentAssetRevision 取资料此刻的版本：确认记录与冻结来源都要钉住它。
func currentAssetRevision(t *testing.T, handler *Handler, assetID string) int {
	t.Helper()
	asset, err := handler.bindings.CurrentAsset(context.Background(), "project-1", assetID)
	if err != nil {
		t.Fatalf("read current asset: %v", err)
	}
	return asset.AssetRevision
}

func issueCodes(result delivery.CheckResult) []string {
	out := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		out = append(out, issue.Code)
	}
	return out
}

func TestDeliveryLinkTurnsAWorkspaceIntoAPassedRelease(t *testing.T) {
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	service := newDeliveryReleaseService(t, handler)
	ctx := policyContext()

	// 检查：一份完整、冻结且已授权的工作区应当通过——唯一一条 finding 是负责人
	// 选择保留的待核项，它是随文件带走的提示（severity=warning），不阻断。
	result, err := service.Check(ctx, deliveryActorID, "project-1", deliveryProjectVersion)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Status != delivery.CheckPassed {
		t.Fatalf("Check status = %s with %v, want passed", result.Status, result.Issues)
	}
	if codes := issueCodes(result); len(codes) != 1 || codes[0] != "retained_review_item" {
		t.Fatalf("Check issues = %v, want exactly the retained warning", result.Issues)
	}

	snapshot, err := service.Prepare(ctx, deliveryActorID, "project-1", deliveryProjectVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if snapshot.Check.Status != delivery.CheckPassed || !snapshot.IsCurrent {
		t.Fatalf("snapshot check = %s current = %v, want passed/current", snapshot.Check.Status, snapshot.IsCurrent)
	}
	if len(snapshot.SnapshotDigest) != 64 {
		t.Fatalf("snapshot digest = %q, want a sha256 hex", snapshot.SnapshotDigest)
	}

	// 冻结来源：T12 手里只有一串 ID，是这一跳把它补成可核对的记录。
	frozen := snapshot.FrozenInput
	if len(frozen.Sources) != 1 {
		t.Fatalf("frozen sources = %+v, want exactly %s", frozen.Sources, deliverySourceID)
	}
	source := frozen.Sources[0]
	if source.ID != deliverySourceID || source.ProjectID != "project-1" || source.AssetID != assetID {
		t.Fatalf("frozen source = %+v, want %s bound to %s", source, deliverySourceID, assetID)
	}
	if source.AssetRevision != currentAssetRevision(t, handler, assetID) {
		t.Fatalf("frozen revision = %d, want the current one", source.AssetRevision)
	}
	if source.QuotedText != sourcePolicyQuote {
		t.Fatalf("frozen quote = %q, want %q", source.QuotedText, sourcePolicyQuote)
	}
	if source.Locator == "" || source.DisplayTitle == "" {
		t.Fatalf("frozen source lacks locator/display title: %+v", source)
	}
	if want := []delivery.AssetVersion{{AssetID: assetID, Revision: source.AssetRevision}}; len(frozen.AssetVersions) != 1 || frozen.AssetVersions[0] != want[0] {
		t.Fatalf("asset versions = %+v, want %+v", frozen.AssetVersions, want)
	}
	if len(frozen.PolicyAssetIDs) != 1 || frozen.PolicyAssetIDs[0] != assetID {
		t.Fatalf("policy asset ids = %v, want [%s]", frozen.PolicyAssetIDs, assetID)
	}

	// 模板必须是交付侧那份**带 rules** 的：工作区自己的 workspacecore.Template 没有 Rules，
	// 拿它来冻结，T13 会因为没有规则声明把每一次检查都判成 not_evaluated。
	if frozen.Template.Version != delivery.DemoTemplateVersion || len(frozen.Template.Rules) == 0 {
		t.Fatalf("frozen template = %+v, want the delivery template with rules", frozen.Template)
	}
	if result.RulesetHash != delivery.DemoRulesetHash {
		t.Fatalf("ruleset hash = %s, want %s", result.RulesetHash, delivery.DemoRulesetHash)
	}

	// 取回：冻结内容与摘要不再变，is_current 按此刻的工作区重算。
	got, err := service.Get(ctx, deliveryActorID, "project-1", snapshot.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.IsCurrent || got.SnapshotDigest != snapshot.SnapshotDigest {
		t.Fatalf("Get = %+v, want the same digest and current", got)
	}
}

// 引用坏掉时，这一跳的处置与 T11/T12 相反：不整批打回，而是把那条留在章节的
// source_ids 里、不放进冻结来源，让 T13 把结论落成一份可读的报告。冻结输入是
// 诊断的输入，不是准入的门——挡在门外，操作者只看到「不通过」，看不到是哪条坏了。
func TestDeliveryLinkDiagnosesASourceItCannotFreeze(t *testing.T) {
	handler, db, assetID := seedBoundSource(t)
	// 章节引用了两条来源，其中一条所在的块被编辑过：ContentRevision 非零即坐标
	// 不再描述当前正文。
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID),
			deliveryBody+"另有待核实的一条 [[source:source-2]]。", []string{"source-1", "source-2"}),
		deliveryMethodChapter(),
	)
	seedChunk(t, db, "source-2", "knowledge-1", sourcePolicyQuote, 1, 0, len([]rune(sourcePolicyQuote)))

	service := newDeliveryReleaseService(t, handler)
	ctx := policyContext()
	result, err := service.Check(ctx, deliveryActorID, "project-1", deliveryProjectVersion)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Status != delivery.CheckBlocked {
		t.Fatalf("Check status = %s, want blocked", result.Status)
	}
	var found bool
	for _, issue := range result.Issues {
		if issue.Code == "source_invalid" && issue.TargetID == deliveryChapterID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Check issues = %v, want source_invalid on %s", result.Issues, deliveryChapterID)
	}

	// 冻结仍会发生，且快照如实记下「哪一条没冻进去」：章节仍引用 source-2，
	// 而冻结来源里只有 source-1。这一份 blocked 快照就是操作者要的答案。
	snapshot, err := service.Prepare(ctx, deliveryActorID, "project-1", deliveryProjectVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if snapshot.Check.Status != delivery.CheckBlocked {
		t.Fatalf("snapshot check = %s, want blocked", snapshot.Check.Status)
	}
	if len(snapshot.FrozenInput.Sources) != 1 || snapshot.FrozenInput.Sources[0].ID != deliverySourceID {
		t.Fatalf("frozen sources = %+v, want only %s", snapshot.FrozenInput.Sources, deliverySourceID)
	}
	chapter := frozenChapter(t, snapshot.FrozenInput, deliveryChapterID)
	if len(chapter.SourceIDs) != 2 || chapter.SourceIDs[1] != "source-2" {
		t.Fatalf("chapter source ids = %v, want both citations kept", chapter.SourceIDs)
	}
}

// 契约里 /checks 与 /releases 的请求体只有 expected_project_version：调用方是在说
// 「我以为工作区长这样」。对不上就要报冲突，而不是把新内容当成他以为的那份检查下去。
func TestDeliveryLinkRefusesAProjectVersionTheCallerDidNotSee(t *testing.T) {
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	service := newDeliveryReleaseService(t, handler)

	_, err := service.Check(policyContext(), deliveryActorID, "project-1", deliveryProjectVersion-1)
	if !errors.Is(err, candidateadoption.ErrVersionConflict) {
		t.Fatalf("Check = %v, want ErrVersionConflict", err)
	}
	if _, err := service.Prepare(policyContext(), deliveryActorID, "project-1", deliveryProjectVersion-1); !errors.Is(err, candidateadoption.ErrVersionConflict) {
		t.Fatalf("Prepare = %v, want ErrVersionConflict", err)
	}
}

// is_current 是随工作区变化的值，不是冻结那一刻的快照属性：存下来的那个值
// 从写下的一刻就在过期。
func TestDeliveryLinkReportsDriftAfterTheWorkspaceMoves(t *testing.T) {
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	service := newDeliveryReleaseService(t, handler)
	ctx := policyContext()

	snapshot, err := service.Prepare(ctx, deliveryActorID, "project-1", deliveryProjectVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := db.Exec("UPDATE lingdoc_projects SET project_version = project_version + 1 WHERE id = ?", "project-1").Error; err != nil {
		t.Fatalf("move the project on: %v", err)
	}
	got, err := service.Get(ctx, deliveryActorID, "project-1", snapshot.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.IsCurrent {
		t.Fatal("Get reports the snapshot as current after the workspace moved on")
	}
	if got.FrozenInput.ProjectVersion != deliveryProjectVersion {
		t.Fatalf("frozen project version = %d, want the frozen one", got.FrozenInput.ProjectVersion)
	}
}

// 身份不全就不构建：产出一份「来源全被略去」的输入，得到的是一份看似诊断完成、
// 实则什么都没测到的 blocked 快照，比直接拒绝更误导。
func TestDeliveryLinkRequiresAnIdentity(t *testing.T) {
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	service := newDeliveryReleaseService(t, handler)

	_, err := service.Check(context.Background(), deliveryActorID, "project-1", deliveryProjectVersion)
	if !errors.Is(err, candidateadoption.ErrSourceAccessDenied) {
		t.Fatalf("Check = %v, want ErrSourceAccessDenied", err)
	}
}

// 摘要只该取决于内容：同一份工作区连着冻两次，两次的摘要必须一致，
// 否则「冻结」就不再意味着「同一份输入永远得到同一枚摘要」。
func TestDeliveryLinkDigestsTheSameWorkspaceTheSameWay(t *testing.T) {
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	service := newDeliveryReleaseService(t, handler)
	ctx := policyContext()

	first, err := service.Prepare(ctx, deliveryActorID, "project-1", deliveryProjectVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	second, err := service.Prepare(ctx, deliveryActorID, "project-1", deliveryProjectVersion)
	if err != nil {
		t.Fatalf("Prepare again: %v", err)
	}
	if first.SnapshotDigest != second.SnapshotDigest {
		t.Fatalf("digests = %s / %s, want the same", first.SnapshotDigest, second.SnapshotDigest)
	}
	if first.ID == second.ID {
		t.Fatal("two freezes share one snapshot id")
	}
	if strings.TrimSpace(first.ID) == "" {
		t.Fatal("snapshot id is empty")
	}
}
