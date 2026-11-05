package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	adoption "github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/gorm"
)

// DOCXFreezeResult keeps the immutable snapshot separate from the export gate.
// A blocked snapshot is a successful diagnostic record, never a downloadable one.
type DOCXFreezeResult struct {
	Snapshot         delivery.ReleaseSnapshot `json:"snapshot"`
	Replayed         bool                     `json:"replayed"`
	DeliveryEligible bool                     `json:"deliveryEligible"`
}

type DOCXDeliveryService struct {
	runtime   *SourceRuntime
	snapshots delivery.SnapshotStore
	exports   delivery.ExportStore
}

func NewDOCXDeliveryService(runtime *SourceRuntime, snapshots delivery.SnapshotStore, exports delivery.ExportStore) *DOCXDeliveryService {
	if runtime == nil || runtime.db == nil || snapshots == nil || exports == nil {
		return nil
	}
	return &DOCXDeliveryService{runtime: runtime, snapshots: snapshots, exports: exports}
}

func (s *DOCXDeliveryService) projects(db *gorm.DB) (*SourceRuntime, *core.Service) {
	runtime := s.runtime.CloneWithDatabase(db)
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), s.runtime.templates, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(projects)
	runtime.EnableFullWorkflowPolicy()
	return runtime, projects
}

func (s *DOCXDeliveryService) authorize(ctx context.Context, actor Actor, projectID string) error {
	_, projects := s.projects(s.runtime.db)
	return projects.Authorize(ctx, actor, projectID, "read")
}

// FreezeSavedDOCXVersion builds T12 input from the named immutable DOCX version,
// then uses the original ReleaseService and transactional idempotency port.
// Non-latest DOCX versions are valid historical inputs; live access and source
// checks still run. Rule failures are persisted as blocked snapshots.
func (s *DOCXDeliveryService) FreezeSavedDOCXVersion(ctx context.Context, actor Actor, projectID, savedVersionID, key string) (DOCXFreezeResult, error) {
	var result DOCXFreezeResult
	if s == nil || strings.TrimSpace(projectID) == "" || strings.TrimSpace(savedVersionID) == "" || len(strings.TrimSpace(key)) < 8 || len(key) > 128 {
		return result, errors.New("DOCX_FREEZE_INVALID_REQUEST")
	}
	if err := s.authorize(ctx, actor, projectID); err != nil {
		return result, fmt.Errorf("authorize DOCX delivery freeze: %w", err)
	}
	freezes, err := delivery.NewTransactionalSnapshotPort(s.snapshots)
	if err != nil {
		return result, err
	}
	attempt := delivery.FreezeAttempt{ActorID: actor.UserID, ProjectID: projectID, Key: key}
	requestHash := docxBytesHash([]byte(actor.UserID + "\x00" + projectID + "\x00" + savedVersionID))
	replay, found, err := freezes.ReplayFreeze(attempt, requestHash)
	if err != nil {
		return result, fmt.Errorf("replay DOCX delivery freeze: %w", err)
	}
	if found {
		current, err := s.current(ctx, actor, replay.FrozenInput)
		if err != nil {
			return result, err
		}
		replay.IsCurrent = current
		return DOCXFreezeResult{Snapshot: replay, Replayed: true, DeliveryEligible: replay.Check.Status == delivery.CheckPassed}, nil
	}
	input, err := s.buildInput(ctx, actor, projectID, savedVersionID)
	if err != nil {
		return result, err
	}
	releases := delivery.NewReleaseService(s.snapshots, delivery.CurrentnessFunc(func(frozen delivery.DeliveryInput) (bool, error) {
		return s.current(ctx, actor, frozen)
	}))
	snapshot, err := releases.Freeze(input)
	if err != nil {
		return result, fmt.Errorf("evaluate DOCX delivery currentness: %w", err)
	}
	useEvent, governed, err := ruleSetUseEvent(ctx, actor.UserID, input, "release_snapshot", snapshot.ID, string(snapshot.Check.Status))
	if err != nil {
		return result, fmt.Errorf("build DOCX RuleSet use audit: %w", err)
	}
	var recorded delivery.ReleaseSnapshot
	var replayed bool
	if governed {
		audited, ok := freezes.(delivery.RuleSetUseFreezeRecorder)
		if !ok {
			return result, errors.New("DOCX_FREEZE_AUDIT_UNAVAILABLE")
		}
		recorded, replayed, err = audited.RecordFreezeWithRuleSetUse(snapshot, attempt, requestHash, useEvent)
	} else {
		recorded, replayed, err = freezes.RecordFreeze(snapshot, attempt, requestHash)
	}
	if err != nil {
		return result, fmt.Errorf("record DOCX delivery freeze: %w", err)
	}
	return DOCXFreezeResult{Snapshot: recorded, Replayed: replayed, DeliveryEligible: recorded.Check.Status == delivery.CheckPassed}, nil
}

func (s *DOCXDeliveryService) buildInput(ctx context.Context, actor Actor, projectID, savedVersionID string) (delivery.DeliveryInput, error) {
	version, err := NewDOCXVersionStore(s.runtime.db).Get(ctx, projectID, savedVersionID)
	if err != nil {
		return delivery.DeliveryInput{}, err
	}
	base, err := adoption.NewSQLiteCandidateAdoptionStore(s.runtime.db).ReadDeliveryInput(ctx, projectID)
	if err != nil {
		return delivery.DeliveryInput{}, fmt.Errorf("read DOCX delivery project binding: %w", err)
	}
	mapped, err := NewDOCXVersionStore(s.runtime.db).GuardChapters(ctx, savedVersionID, nil)
	if err != nil {
		return delivery.DeliveryInput{}, err
	}
	if len(mapped) != len(docxSectionSpecs) {
		return delivery.DeliveryInput{}, errors.New("DOCX_SECTION_MAPPING_INCOMPLETE")
	}
	projected := make(map[string]DOCXProjectedChapter, len(version.Projection.Sections))
	for _, section := range version.Projection.Sections {
		projected[section.SectionID] = section
	}
	base.Chapters = make([]adoption.WorkspaceDeliveryChapter, 0, len(mapped))
	for _, chapter := range mapped {
		section, ok := projected[chapter.SectionID]
		if !ok || section.Body != chapter.Body {
			return delivery.DeliveryInput{}, errors.New("DOCX_SECTION_MAPPING_INVALID")
		}
		var title string
		if err := s.runtime.db.WithContext(ctx).Table("lingdoc_chapters").Select("title").Where("id=? AND project_id=? AND section_id=?", chapter.ChapterID, projectID, chapter.SectionID).Scan(&title).Error; err != nil {
			return delivery.DeliveryInput{}, err
		}
		var row struct {
			SourceIDsJSON      string
			CitationUsagesJSON string
			ReviewItemsJSON    string
		}
		found := s.runtime.db.WithContext(ctx).Table("lingdoc_chapter_versions").Select("source_ids_json,citation_usages_json,review_items_json").Where("id=? AND chapter_id=? AND project_id=? AND body_markdown=?", chapter.ChapterVersionID, chapter.ChapterID, projectID, chapter.Body).Scan(&row)
		if found.Error != nil {
			return delivery.DeliveryInput{}, found.Error
		}
		if found.RowsAffected != 1 {
			return delivery.DeliveryInput{}, errors.New("DOCX_PROJECTION_VERSION_MISMATCH")
		}
		var sourceIDs []string
		var citations []adoption.CitationUsage
		var reviews []adoption.ReviewItem
		if err := json.Unmarshal([]byte(row.SourceIDsJSON), &sourceIDs); err != nil {
			return delivery.DeliveryInput{}, err
		}
		if err := json.Unmarshal([]byte(row.CitationUsagesJSON), &citations); err != nil {
			return delivery.DeliveryInput{}, err
		}
		if err := json.Unmarshal([]byte(row.ReviewItemsJSON), &reviews); err != nil {
			return delivery.DeliveryInput{}, err
		}
		versionID := chapter.ChapterVersionID
		frozen := adoption.WorkspaceDeliveryChapter{ChapterID: chapter.ChapterID, SectionID: chapter.SectionID, Title: title, ChapterVersionID: &versionID, BodyMarkdown: chapter.Body, SourceIDs: sourceIDs, CitationUsages: citations, ReviewItems: reviews}
		confirmation, err := s.confirmationForVersion(ctx, projectID, chapter.ChapterID, chapter.ChapterVersionID, base.SpecRevision, base.TemplateVersion)
		if err != nil {
			return delivery.DeliveryInput{}, err
		}
		frozen.Confirmation = confirmation
		base.Chapters = append(base.Chapters, frozen)
	}
	builder := s.runtime.DeliveryInputBuilder()
	if builder == nil {
		return delivery.DeliveryInput{}, errors.New("DOCX_DELIVERY_BUILDER_MISSING")
	}
	input, err := builder.Build(ctx, actor.UserID, base)
	if err != nil {
		return delivery.DeliveryInput{}, fmt.Errorf("build immutable DOCX delivery input: %w", err)
	}
	input.DOCXSavedVersionID = version.ID
	input.DOCXSHA256 = version.DocSHA256
	input.DOCXManifestHash = version.ManifestHash
	return input, nil
}

// DOCXDeliveryStatus is rebuilt from durable records on every read. The browser
// may display a previous response, but it cannot use that response as proof of
// confirmation, freeze eligibility, or snapshot currentness after a reload.
type DOCXDeliveryStatus struct {
	Version          DOCXSavedVersion           `json:"version"`
	SpecRevision     int                        `json:"specRevision"`
	Chapters         []DOCXDeliveryChapterState `json:"chapters"`
	Snapshot         *delivery.ReleaseSnapshot  `json:"snapshot,omitempty"`
	DeliveryEligible bool                       `json:"deliveryEligible"`
}

type DOCXDeliveryChapterState struct {
	SectionID        string                 `json:"sectionId"`
	ChapterID        string                 `json:"chapterId"`
	ChapterVersionID string                 `json:"chapterVersionId"`
	Title            string                 `json:"title"`
	Body             string                 `json:"bodyText"`
	Confirmed        bool                   `json:"confirmed"`
	Confirmation     *delivery.Confirmation `json:"confirmation,omitempty"`
}

func (s *DOCXDeliveryService) Status(ctx context.Context, actor Actor, projectID, savedVersionID string) (DOCXDeliveryStatus, error) {
	var status DOCXDeliveryStatus
	if s == nil || strings.TrimSpace(projectID) == "" || strings.TrimSpace(savedVersionID) == "" {
		return status, errors.New("DOCX_DELIVERY_STATUS_INVALID_REQUEST")
	}
	if err := s.authorize(ctx, actor, projectID); err != nil {
		return status, err
	}
	input, err := s.buildInput(ctx, actor, projectID, savedVersionID)
	if err != nil {
		return status, err
	}
	version, err := NewDOCXVersionStore(s.runtime.db).Get(ctx, projectID, savedVersionID)
	if err != nil {
		return status, err
	}
	status.Version = version
	status.SpecRevision = input.SpecRevision
	status.Chapters = make([]DOCXDeliveryChapterState, 0, len(input.Chapters))
	for _, chapter := range input.Chapters {
		chapterVersionID := ""
		if chapter.ChapterVersionID != nil {
			chapterVersionID = *chapter.ChapterVersionID
		}
		status.Chapters = append(status.Chapters, DOCXDeliveryChapterState{
			SectionID: chapter.SectionID, ChapterID: chapter.ChapterID,
			ChapterVersionID: chapterVersionID, Title: chapter.Title, Body: chapter.BodyMarkdown,
			Confirmed: chapter.Confirmation != nil, Confirmation: chapter.Confirmation,
		})
	}
	lister, ok := s.snapshots.(delivery.SnapshotLister)
	if !ok {
		return DOCXDeliveryStatus{}, errors.New("DOCX_DELIVERY_SNAPSHOT_LIST_UNAVAILABLE")
	}
	snapshots, err := lister.ListSnapshots(projectID)
	if err != nil {
		return DOCXDeliveryStatus{}, err
	}
	for _, snapshot := range snapshots {
		if snapshot.FrozenInput.DOCXSavedVersionID != savedVersionID {
			continue
		}
		current, err := s.current(ctx, actor, snapshot.FrozenInput)
		if err != nil {
			return DOCXDeliveryStatus{}, err
		}
		snapshot.IsCurrent = current
		status.Snapshot = &snapshot
		status.DeliveryEligible = current && snapshot.Check.Status == delivery.CheckPassed
		break
	}
	return status, nil
}

func (s *DOCXDeliveryService) confirmationForVersion(ctx context.Context, projectID, chapterID, versionID string, specRevision int, templateVersion string) (*adoption.Confirmation, error) {
	var rows []struct{ DetailsJSON string }
	if err := s.runtime.db.WithContext(ctx).Table("lingdoc_chapter_confirmations AS confirmation").Select("confirmation.details_json").Joins("JOIN lingdoc_chapters AS chapter ON chapter.id=confirmation.chapter_id").Where("chapter.project_id=? AND confirmation.chapter_id=? AND confirmation.chapter_version_id=? AND confirmation.valid=1", projectID, chapterID, versionID).Order("confirmation.created_at DESC,confirmation.id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		var confirmation adoption.Confirmation
		if err := json.Unmarshal([]byte(row.DetailsJSON), &confirmation); err != nil {
			return nil, err
		}
		if confirmation.Valid && confirmation.ChapterID == chapterID && confirmation.ChapterVersionID == versionID && confirmation.SpecRevision == specRevision && confirmation.TemplateVersion == templateVersion {
			return &confirmation, nil
		}
	}
	return nil, nil
}

// current deliberately does not resolve latest chapters or latest DOCX. The
// snapshot names its immutable body. Live project access, source permissions,
// source revisions and DOCX object integrity are still rechecked.
func (s *DOCXDeliveryService) current(ctx context.Context, actor Actor, input delivery.DeliveryInput) (bool, error) {
	if input.DOCXSavedVersionID == "" || input.DOCXSHA256 == "" || input.DOCXManifestHash == "" {
		return false, errors.New("DOCX_EXPORT_DEPENDENCY_MISSING")
	}
	if err := s.authorize(ctx, actor, input.ProjectID); err != nil {
		return false, err
	}
	version, err := NewDOCXVersionStore(s.runtime.db).Get(ctx, input.ProjectID, input.DOCXSavedVersionID)
	if err != nil {
		return false, err
	}
	if version.DocSHA256 != input.DOCXSHA256 || version.ManifestHash != input.DOCXManifestHash {
		return false, errors.New("DOCX_FILE_INTEGRITY_FAILED")
	}
	builder, ok := s.runtime.DeliveryInputBuilder().(DeliveryCurrentness)
	if !ok {
		return false, errors.New("DOCX_CURRENTNESS_DEPENDENCY_MISSING")
	}
	return builder.Current(ctx, actor.UserID, input)
}

func (s *DOCXDeliveryService) exportService(ctx context.Context, actor Actor) *delivery.ExportService {
	store := NewDOCXVersionStore(s.runtime.db)
	service := delivery.NewExportService(s.snapshots, s.exports, DOCXFrozenRenderer{Store: store}, DOCXFrozenValidator{}, delivery.CurrentnessFunc(func(input delivery.DeliveryInput) (bool, error) {
		return s.current(ctx, actor, input)
	}), delivery.ExportAccessFunc(func(actorUserID, projectID string) error {
		if actorUserID != actor.UserID {
			return core.ErrNotFound
		}
		return s.authorize(ctx, actor, projectID)
	}), s.runtime.audit)
	return service.WithRuleSetUseAuditor(rulesetstore.NewStore(s.runtime.db, rulesetstore.TenantMembershipAuthorizer{}))
}

func (s *DOCXDeliveryService) StartExport(ctx context.Context, actor Actor, projectID, snapshotID, key string) (delivery.ExportArtifact, bool, error) {
	if s == nil {
		return delivery.ExportArtifact{}, false, errors.New("DOCX_EXPORT_DEPENDENCY_MISSING")
	}
	service := s.exportService(ctx, actor)
	if service == nil {
		return delivery.ExportArtifact{}, false, errors.New("DOCX_EXPORT_DEPENDENCY_MISSING")
	}
	return service.StartAs(ctx, delivery.Actor{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role}, projectID, snapshotID, key)
}

func (s *DOCXDeliveryService) Download(ctx context.Context, actor Actor, projectID, exportID string) (delivery.ExportArtifact, []byte, error) {
	if s == nil {
		return delivery.ExportArtifact{}, nil, errors.New("DOCX_EXPORT_DEPENDENCY_MISSING")
	}
	artifact, err := s.exports.GetExport(projectID, exportID)
	if err != nil {
		return delivery.ExportArtifact{}, nil, err
	}
	snapshot, err := s.snapshots.Get(projectID, artifact.SnapshotID)
	if err != nil {
		return delivery.ExportArtifact{}, nil, err
	}
	current, err := s.current(ctx, actor, snapshot.FrozenInput)
	if err != nil {
		return delivery.ExportArtifact{}, nil, err
	}
	if !current {
		return delivery.ExportArtifact{}, nil, delivery.ErrExportStaleInput
	}
	service := s.exportService(ctx, actor)
	if service == nil {
		return delivery.ExportArtifact{}, nil, errors.New("DOCX_EXPORT_DEPENDENCY_MISSING")
	}
	return service.DownloadAs(ctx, delivery.Actor{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role}, projectID, exportID)
}
