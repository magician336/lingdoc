package candidateadoption

// 采纳目标 → 写入路径。
//
// 三个采纳目标各自走一条写入路径：
//
//   - 工作副本·选段：只把**确认范围**内的那一段写进工作副本，不建正式版本，
//     不动章节版本指针。
//   - 工作副本·整段：整篇写进工作副本，不建正式版本，不动章节版本指针。
//   - 正式版本·整段：整篇写进工作副本**并**建正式版本，CAS 推进章节版本指针。
//
// 分派按**目标**走，不按动作名走。动作名分支等于把「这个动作允许哪些目标」
// 这张许可位表抄一份到采纳端：许可位一改，采纳端的分支不会跟着变，而且不会
// 有任何编译错误。A29 守护守的就是这件事。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// adoptionWritePath 是一次采纳要走的写入路径。
type adoptionWritePath string

const (
	// pathWorkingCopyPartial 只写工作副本的确认范围。
	pathWorkingCopyPartial adoptionWritePath = "working_copy_partial"
	// pathWorkingCopyWhole 整篇写工作副本。
	pathWorkingCopyWhole adoptionWritePath = "working_copy_whole"
	// pathFormalVersionWhole 建正式章节版本并推进章节版本指针。
	pathFormalVersionWhole adoptionWritePath = "formal_version_whole"
)

// writePathFor 把采纳目标映到写入路径。
//
// 目标之外的取值一律拒：目标是从确认载荷里来的，未知目标没有「写到哪里」的
// 含义，不能猜一个。
func writePathFor(target writingaction.AdoptionTarget) (adoptionWritePath, error) {
	switch target {
	case writingaction.TargetWorkingCopyPartial:
		return pathWorkingCopyPartial, nil
	case writingaction.TargetWorkingCopyWhole:
		return pathWorkingCopyWhole, nil
	case writingaction.TargetFormalVersionWhole:
		return pathFormalVersionWhole, nil
	default:
		return "", ErrInvalidRequest
	}
}

// buildsFormalVersion 说这条路径会不会建正式章节版本。
//
// 只有正式版本路径会。另外两条路径**在代码上就没有建版本的入口**：不是「建了
// 再回滚」，是压根走不到那段代码。这是纵深防御的第二层。
func (p adoptionWritePath) buildsFormalVersion() bool {
	return p == pathFormalVersionWhole
}

// writesFormalVersion 说这次采纳会不会建正式版本。
//
// 判据是**目标**：只有正式版本目标会建版本。工作副本的两个目标在代码上就没有
// 建版本的入口，所以「章节已经有正式版本 ⇒ 必须 replace_existing」这道门
// 不该套在它们身上。
func writesFormalVersion(in AcceptCandidateInput) bool {
	if in.Confirmation == nil {
		return false
	}
	path, err := writePathFor(in.Confirmation.Target)
	if err != nil {
		return false
	}
	return path.buildsFormalVersion()
}

// formalVersionAllowed 复核这个动作允许不允许建正式版本。
//
// 许可位在确认载荷校验里已经查过一次，这里在**写路径**上再查一次：即使许可位
// 被绕过，写路径本身也不给那些动作建正式版本的机会。两层都要有断言——
// 许可位那一层挡的是「请求」，这一层挡的是「写」。
func formalVersionAllowed(action writingaction.Action) bool {
	allowed, err := writingaction.TargetsFor(action)
	if err != nil {
		return false
	}
	return slices.Contains(allowed, writingaction.TargetFormalVersionWhole)
}

// guardFormalVersionPath 是写路径上的第二层复核。
//
// 它在正式版本那条分支里被调用：请求层已经查过许可位，这里再查一次，所以
// 「请求层被绕过」也不会让一个没有建正式版本许可的动作写下正式版本。
func guardFormalVersionPath(in AcceptCandidateInput) error {
	if in.Confirmation == nil {
		return ErrInvalidRequest
	}
	if !formalVersionAllowed(in.Confirmation.WritingAction) {
		return ErrInvalidRequest
	}
	return nil
}

// workingCopyBodyHash 是工作副本正文的摘要。
//
// 算法写死在这里，客户端算的是同一个值：正文的 UTF-8 字节的 SHA-256，小写
// 十六进制、64 个字符。
//
// 它是**内容摘要**，不是操作请求摘要：后者归 operationcore 一家，判据是
// 「同一份请求」，而这里是「同一份正文」。仓里已有同形状的内容摘要
// （workspacecore 的 g2Hash 等），它们走的也是这一条路。
func workingCopyBodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// validBodyHash 说这个摘要是不是一个形状对的正文摘要。
//
// 形状不对就拒：摘要是声明的一部分，声明看不懂就不能拿它当判据。
func validBodyHash(hash string) bool {
	if len(hash) != sha256.Size*2 {
		return false
	}
	for _, r := range hash {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// currentWorkingCopyState 是服务端当前的工作副本状态。
//
// 没有工作副本时它就是「修订号 0、正文为空」：服务端与客户端对同一件事的
// 说法必须一致，否则「你看到的是哪一份」永远算不清。
func currentWorkingCopyState(current *adoptionWorkingCopyRow) (int64, string) {
	if current == nil {
		return 0, ""
	}
	return current.WorkingCopyRevision, current.BodyMarkdown
}

// checkWorkingCopyDeclaration 复核客户端声明的工作副本状态。
//
// 顺序是有讲究的：
//
//  1. 修订号对不上 ⇒ 别人改过这份草稿，请重新读取（version_conflict）。
//  2. 正文摘要对不上 ⇒ 你看到的与服务端当前的不是同一份：你的编辑器里有
//     没保存的内容，或者内容已经变了（working_copy_dirty）。
//
// 带快照的替换可以在 dirty 的情况下继续：服务端先把当前工作副本正文与替换
// 范围记进审计详情，人工据此可以恢复。修订号那一步不放行——别人改过就是
// 别人改过，快照救不了「你基于哪一版」这件事。
func checkWorkingCopyDeclaration(in AcceptCandidateInput, current *adoptionWorkingCopyRow) error {
	revision, body := currentWorkingCopyState(current)
	if in.ExpectedWorkingCopyRevision != revision {
		return ErrVersionConflict
	}
	if !strings.EqualFold(in.ExpectedBodyHash, workingCopyBodyHash(body)) {
		if strings.TrimSpace(in.SnapshotID) == "" {
			return ErrWorkingCopyDirty
		}
	}
	return nil
}

// spliceWorkingCopyBody 把候选正文拼进工作副本正文的确认范围。
//
// 范围之外一字不动：拼进去的就是确认的那一段，不多写不少写。范围必须仍然
// 落在当前正文里（UTF-16 边界合法，且那一段确实存在）——正文变了，范围就
// 落在别的段落上了，那不是用户确认的那一段。
func spliceWorkingCopyBody(body string, scope AdoptionScope, replacement string) (string, error) {
	start, end, ok := reviewanchor.ByteRange(body, scope.StartUTF16, scope.EndUTF16)
	if !ok {
		return "", ErrVersionConflict
	}
	return body[:start] + replacement + body[end:], nil
}

// mergeAdoptedReviewItems 把候选的待核事项并进工作副本的待核事项。
//
// 语义与工作副本侧的改写应用一致：
//
//   - 已有事项按**新正文**重锚点：引文还在就重新定位，找不到就标成 stale。
//   - 候选事项的锚点按插入位置平移后必须仍然落在新正文里，否则拒。
//   - 重复 ID 一律拒：一个 ID 只能有一条待核事项。
//
// offset 是候选正文被插入的位置（UTF-16 偏移）：整段替换是 0，部分采纳是
// 确认范围的起点。
func mergeAdoptedReviewItems(previous, added []ReviewItem, oldBody, newBody string, offset int, revision int64, version *string) ([]ReviewItem, error) {
	merged := make([]ReviewItem, 0, len(previous)+len(added))
	seen := make(map[string]struct{}, len(previous)+len(added))
	for _, item := range previous {
		rebased := item
		rebased.Anchor = reviewanchor.Rebase(oldBody, newBody, item.Anchor, revision, version)
		seen[item.ID] = struct{}{}
		merged = append(merged, rebased)
	}
	for _, item := range added {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Statement) == "" ||
			strings.TrimSpace(item.OriginCandidateID) == "" {
			return nil, ErrInvalidRequest
		}
		if _, exists := seen[item.ID]; exists {
			return nil, ErrInvalidRequest
		}
		seen[item.ID] = struct{}{}
		next := item
		if item.Anchor != nil {
			selection := reviewanchor.Selection{
				StartUTF16:    item.Anchor.StartUTF16 + offset,
				EndUTF16:      item.Anchor.EndUTF16 + offset,
				QuoteMarkdown: item.Anchor.QuoteMarkdown,
			}
			if !reviewanchor.Valid(newBody, selection) {
				return nil, ErrInvalidRequest
			}
			next.Anchor = reviewanchor.New(selection, revision, version)
		}
		merged = append(merged, next)
	}
	return merged, nil
}

// adoptedCitationUsages 折出工作副本新的引用说明。
//
// 仍在正文里的来源保留它原来的用途与限制说明；新出现的来源先记成「用途未知」——
// 它还没有人工说明，编一个就是撒谎。
func adoptedCitationUsages(previous []CitationUsage, sourceIDs []string) []CitationUsage {
	allowed := make(map[string]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		allowed[id] = struct{}{}
	}
	usages := make([]CitationUsage, 0, len(sourceIDs))
	seen := make(map[string]struct{}, len(sourceIDs))
	for _, usage := range previous {
		if _, ok := allowed[usage.SourceID]; !ok {
			continue
		}
		if _, ok := seen[usage.SourceID]; ok {
			continue
		}
		seen[usage.SourceID] = struct{}{}
		usages = append(usages, usage)
	}
	for _, id := range sourceIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		usages = append(usages, CitationUsage{SourceID: id})
	}
	slices.SortFunc(usages, func(a, b CitationUsage) int { return strings.Compare(a.SourceID, b.SourceID) })
	return usages
}

// writeWorkingCopy 把新的工作副本写回去。
//
// 四元 CAS：基线版本 + 研究条件版本 + 工作副本修订号（章节当前版本在读的时候
// 已经对过）。任何一项对不上 ⇒ 零写入，不会出现半写。
func writeWorkingCopy(tx *gorm.DB, previous adoptionWorkingCopyRow, next adoptionWorkingCopyRow) error {
	query := tx.Model(&adoptionWorkingCopyRow{}).
		Where("project_id = ? AND chapter_id = ? AND working_copy_revision = ? AND spec_revision = ?",
			previous.ProjectID, previous.ChapterID, previous.WorkingCopyRevision, previous.SpecRevision)
	if previous.BaseChapterVersionID == nil {
		query = query.Where("base_chapter_version_id IS NULL")
	} else {
		query = query.Where("base_chapter_version_id = ?", *previous.BaseChapterVersionID)
	}
	result := query.Updates(map[string]any{
		"spec_revision":         next.SpecRevision,
		"working_copy_revision": next.WorkingCopyRevision,
		"body_markdown":         next.BodyMarkdown,
		"source_ids_json":       next.SourceIDsJSON,
		"citation_usages_json":  next.CitationUsagesJSON,
		"review_items_json":     next.ReviewItemsJSON,
		"updated_at":            next.UpdatedAt,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}

// acceptIntoWorkingCopy 走「只写工作副本」的那两条路径。
//
// 这条路径**不建正式版本、不动章节版本指针、不推进项目版本**：正式版本是用户
// 提交工作副本时才产生的东西，采纳草稿不越这一步。
func (s *SQLiteCandidateAdoptionStore) acceptIntoWorkingCopy(
	tx *gorm.DB,
	in AcceptCandidateInput,
	candidate Candidate,
	current Chapter,
	path adoptionWritePath,
	workingCopy *adoptionWorkingCopyRow,
	auditEnabled bool,
	result *AcceptResult,
) error {
	if workingCopy == nil {
		// 没有工作副本就没有草稿可写：这不是「建一个」，是「无可写的对象」。
		return ErrVersionConflict
	}
	// 草稿的基线必须还是章节的当前版本。基线变了，之前看到的正文就不再是同一份。
	if !sameOptionalString(workingCopy.BaseChapterVersionID, current.CurrentVersionID) {
		return ErrVersionConflict
	}
	oldBody := workingCopy.BodyMarkdown
	newBody := candidate.BodyMarkdown
	offset := 0
	if path == pathWorkingCopyPartial {
		if in.Confirmation == nil || in.Confirmation.Scope == nil {
			return ErrInvalidRequest
		}
		spliced, err := spliceWorkingCopyBody(oldBody, *in.Confirmation.Scope, candidate.BodyMarkdown)
		if err != nil {
			return err
		}
		newBody = spliced
		offset = in.Confirmation.Scope.StartUTF16
	}
	if len(newBody) > 200000 {
		return ErrInvalidRequest
	}
	if err := sourcemarker.Validate(newBody); err != nil {
		return ErrInvalidRequest
	}
	sourceIDs := normalizeIDs(sourcemarker.IDs(newBody))
	var previousItems []ReviewItem
	if workingCopy.ReviewItemsJSON != "" {
		if err := json.Unmarshal([]byte(workingCopy.ReviewItemsJSON), &previousItems); err != nil {
			return err
		}
	}
	var previousUsages []CitationUsage
	if workingCopy.CitationUsagesJSON != "" {
		if err := json.Unmarshal([]byte(workingCopy.CitationUsagesJSON), &previousUsages); err != nil {
			return err
		}
	}
	nextRevision := workingCopy.WorkingCopyRevision + 1
	reviewItems, err := mergeAdoptedReviewItems(previousItems, candidate.ReviewItems, oldBody, newBody, offset, nextRevision, workingCopy.BaseChapterVersionID)
	if err != nil {
		return err
	}
	usages := adoptedCitationUsages(previousUsages, sourceIDs)
	sourceJSON, err := json.Marshal(sourceIDs)
	if err != nil {
		return err
	}
	usageJSON, err := json.Marshal(usages)
	if err != nil {
		return err
	}
	reviewJSON, err := json.Marshal(reviewItems)
	if err != nil {
		return err
	}
	next := *workingCopy
	next.SpecRevision = workingCopy.SpecRevision
	next.WorkingCopyRevision = nextRevision
	next.BodyMarkdown = newBody
	next.SourceIDsJSON = string(sourceJSON)
	next.CitationUsagesJSON = string(usageJSON)
	next.ReviewItemsJSON = string(reviewJSON)
	next.UpdatedAt = time.Now().UTC()
	if err := writeWorkingCopy(tx, *workingCopy, next); err != nil {
		return err
	}
	// 正式版本一个字不动：章节指针、章节版本行数、项目版本都保持原样。
	result.Chapter = current
	if err := recordWorkingCopyAdoptionAudit(tx, auditEnabled, in, candidate, path, oldBody, newBody); err != nil {
		return err
	}
	return recordAdoptionIdempotency(tx, in, requestHash(in), result, current.CurrentVersionID)
}

// recordWorkingCopyAdoptionAudit 把这次「只写工作副本」的采纳写进项目审计。
//
// 带快照的替换在这里留痕：替换前的工作副本正文、替换范围与快照标识都记下来，
// 人工据此可以把内容恢复回去。这不是一张可恢复快照表（那要新迁移），它是
// **审计留痕**：能追、能人工恢复，不能自动回滚。
func recordWorkingCopyAdoptionAudit(tx *gorm.DB, auditEnabled bool, in AcceptCandidateInput, candidate Candidate, path adoptionWritePath, previousBody, nextBody string) error {
	if !auditEnabled {
		return nil
	}
	details := map[string]any{
		"candidate_id":          candidate.ID,
		"chapter_id":            in.ChapterID,
		"spec_revision":         in.ExpectedSpecRevision,
		"adoption_target":       string(path),
		"writes_formal_version": false,
	}
	if confirmation := in.Confirmation; confirmation != nil {
		details["writing_action"] = string(confirmation.WritingAction)
		details["confirmed_at"] = confirmation.ConfirmedAt.UTC().Format(time.RFC3339Nano)
		details["confirmed_by"] = confirmation.ConfirmedBy
		details["origin"] = string(confirmation.Origin)
		if confirmation.Scope != nil {
			details["scope"] = map[string]any{
				"start_utf16": confirmation.Scope.StartUTF16,
				"end_utf16":   confirmation.Scope.EndUTF16,
			}
		}
	}
	if strings.TrimSpace(in.SnapshotID) != "" {
		details["snapshot_id"] = in.SnapshotID
		details["snapshot_body_hash"] = workingCopyBodyHash(previousBody)
		details["snapshot_body_markdown"] = previousBody
		details["snapshot_replaced_body_hash"] = workingCopyBodyHash(nextBody)
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return tx.Create(&projectAuditRow{
		ID: uuid.NewString(), ProjectID: in.ProjectID, ActorID: in.ActorID,
		Action: "project.candidate.adopt", Target: in.ChapterID,
		DetailsJSON: string(encoded), CreatedAt: time.Now().UTC(),
	}).Error
}
