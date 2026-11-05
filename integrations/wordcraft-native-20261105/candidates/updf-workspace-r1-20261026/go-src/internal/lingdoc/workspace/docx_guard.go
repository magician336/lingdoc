package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
	adoption "github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	tpl "github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// DOCXGuardInput is a server-produced projection, never an HTTP request DTO.
// The caller must independently verify DOCX bytes and resolve immutable version
// mappings before entering this boundary. No body source markers are trusted.
type DOCXGuardInput struct {
	ProjectID                   string
	Capability                  string
	Scene                       string
	ExpectedTemplateCopyVersion int64
	ExpectedRuleSetHash         string
	SavedVersionID              string
	Chapters                    []DOCXGuardChapter
}
type DOCXGuardChapter struct {
	SectionID        string
	ChapterID        string
	ChapterVersionID string
	Body             string
}
type DOCXGuardProof struct {
	Binding     tpl.VerifiedRuleSetBinding
	Evaluations []tpl.Evaluation
	Sources     []string
}

// Keep actionable rule failures across the HTTP boundary without exposing
// document text, evaluator evidence, database errors or source permissions.
type docxRuleFailure struct {
	RuleID    string               `json:"ruleId"`
	TargetRef string               `json:"targetRef"`
	SectionID string               `json:"sectionId,omitempty"`
	Status    tpl.EvaluationStatus `json:"status"`
	Message   string               `json:"message"`
}

type docxRuleBlockedError struct {
	Failures []docxRuleFailure
}

func (e *docxRuleBlockedError) Error() string {
	return fmt.Sprintf("DOCX_RULE_BLOCKED: %s (%s)", e.Failures[0].RuleID, e.Failures[0].Status)
}

func docxRuleMessage(message string) string {
	switch message {
	case "required chapter is incomplete":
		return "本章正文为空，请填写正文并保存后再确认。"
	case "required chapter is not confirmed":
		return "本章尚未确认，请先完成章节确认。"
	case "review item is undecided":
		return "仍有待处理的复核项，请完成复核后再确认。"
	case "source is unavailable or unauthorized":
		return "引用的来源不可用或无访问权限，请检查来源后再确认。"
	default:
		return "该项规则未通过，请核对要求并重新保存后再试。"
	}
}

// GuardDOCX performs production project authorization, source-ledger review,
// and the full bound RuleSet evaluator on the SAME supplied GORM transaction.
// The callback must write confirmation/freeze/audit using tx, not another DB.
// A check error never reaches the writer. This does not expose a bypass route.
func (r *SourceRuntime) GuardDOCX(ctx context.Context, actor Actor, in DOCXGuardInput,
	write func(*gorm.DB, DOCXGuardProof) error) error {
	if r == nil || r.db == nil || r.kbShares == nil || write == nil || len(in.Chapters) == 0 {
		return errors.New("DOCX_GUARD_DEPENDENCY_MISSING")
	}
	caller := types.CallerFromContext(ctx)
	if caller.UserID != actor.UserID || caller.TenantID != actor.TenantID {
		return errors.New("DOCX_IDENTITY_MISMATCH")
	}
	if in.Scene != "write_check" && in.Scene != "preflight" {
		return errors.New("DOCX_SCENE_INVALID")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		runtime := r.CloneWithDatabase(tx)
		projects := core.NewServiceWithSources(core.NewGORMRepository(tx), r.templates, runtime.WorkspaceSourcePolicy())
		runtime.ConnectProjects(projects)
		runtime.EnableFullWorkflowPolicy()
		if err := projects.Authorize(ctx, actor, in.ProjectID, in.Capability); err != nil {
			return fmt.Errorf("docx authorize: %w", err)
		}
		project, err := projects.GetProject(ctx, actor, in.ProjectID)
		if err != nil {
			return fmt.Errorf("docx project: %w", err)
		}
		if project.TemplateCopy == nil || project.TemplateCopy.Version != in.ExpectedTemplateCopyVersion || project.TemplateCopy.RulesetHash != in.ExpectedRuleSetHash {
			return errors.New("DOCX_BINDING_VERSION_CHANGED")
		}
		definition := project.TemplateCopy.Definition
		binding, err := tpl.VerifyRuleSetBinding(definition)
		if err != nil {
			return err
		}
		if binding.EffectiveHash != in.ExpectedRuleSetHash {
			return errors.New("DOCX_BINDING_HASH_MISMATCH")
		}
		projection := tpl.EvaluationInput{ProjectID: in.ProjectID, TargetVersion: fmt.Sprintf("copy/%d", project.TemplateCopy.Version), Fields: project.Spec}
		seen := map[string]bool{}
		ids := map[string]bool{}
		for _, chapter := range in.Chapters {
			if chapter.SectionID == "" || seen[chapter.SectionID] {
				return errors.New("DOCX_SECTION_MAPPING_INVALID")
			}
			seen[chapter.SectionID] = true
			var row struct {
				SourceIDsJSON string
				SectionID     string
				BodyMarkdown  string
			}
			result := tx.Table("lingdoc_chapter_versions AS v").Select("v.source_ids_json, c.section_id, v.body_markdown").Joins("JOIN lingdoc_chapters c ON c.id=v.chapter_id AND c.project_id=v.project_id").Where("v.id=? AND v.chapter_id=? AND v.project_id=?", chapter.ChapterVersionID, chapter.ChapterID, in.ProjectID).Scan(&row)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 || row.SectionID != chapter.SectionID || row.BodyMarkdown != chapter.Body {
				return errors.New("DOCX_PROJECTION_VERSION_MISMATCH")
			}
			var sources []string
			if err := json.Unmarshal([]byte(row.SourceIDsJSON), &sources); err != nil {
				return err
			}
			for _, id := range sources {
				if id == "" {
					return errors.New("DOCX_SOURCE_ID_INVALID")
				}
				ids[id] = true
			}
			var record struct{ DetailsJSON string }
			found := tx.Table("lingdoc_chapter_confirmations").Select("details_json").Where("chapter_id=? AND chapter_version_id=? AND valid=1", chapter.ChapterID, chapter.ChapterVersionID).Order("created_at DESC,id DESC").Limit(1).Scan(&record)
			if found.Error != nil {
				return found.Error
			}
			var confirmation adoption.Confirmation
			confirmed := false
			decisions := map[string]bool{}
			if found.RowsAffected == 1 {
				if err := json.Unmarshal([]byte(record.DetailsJSON), &confirmation); err != nil {
					return err
				}
				confirmed = confirmation.Valid && confirmation.ChapterID == chapter.ChapterID && confirmation.ChapterVersionID == chapter.ChapterVersionID && int64(confirmation.SpecRevision) == project.SpecRevision && confirmation.TemplateVersion == project.TemplateVersion
				if confirmed {
					for _, d := range confirmation.ReviewDecisions {
						decisions[d.ReviewItemID] = (d.Disposition == "resolved" || d.Disposition == "retained_warning") && strings.TrimSpace(d.Reason) != ""
					}
				}
			}
			var version struct{ ReviewItemsJSON string }
			if err := tx.Table("lingdoc_chapter_versions").Select("review_items_json").Where("id=?", chapter.ChapterVersionID).Scan(&version).Error; err != nil {
				return err
			}
			var items []adoption.ReviewItem
			if err := json.Unmarshal([]byte(version.ReviewItemsJSON), &items); err != nil {
				return err
			}
			for _, item := range items {
				projection.ReviewItems = append(projection.ReviewItems, tpl.ReviewItemInput{ID: item.ID, Version: chapter.ChapterVersionID, Decided: decisions[item.ID]})
			}
			projection.Chapters = append(projection.Chapters, tpl.ChapterInput{ID: chapter.SectionID, Version: chapter.ChapterVersionID, Body: chapter.Body, Confirmed: confirmed})
		}
		sourceIDs := make([]string, 0, len(ids))
		for id := range ids {
			sourceIDs = append(sourceIDs, id)
		}
		sort.Strings(sourceIDs)
		verdicts, err := runtime.sourceRevalidator().Review(ctx, in.ProjectID, evidence.Actor{UserID: actor.UserID, TenantID: fmt.Sprint(actor.TenantID)}, sourceIDs)
		if err != nil {
			return err
		}
		if len(verdicts) != len(sourceIDs) {
			return errors.New("DOCX_SOURCE_REVIEW_INCOMPLETE")
		}
		for _, v := range verdicts {
			if !v.Usable {
				return errors.New("DOCX_SOURCE_UNAVAILABLE")
			}
			projection.Sources = append(projection.Sources, tpl.SourceInput{ID: v.SourceID, Version: fmt.Sprint(v.Source.AssetRevision), Available: true})
		}
		results, err := tpl.EvaluateSceneWithBinding(definition, binding, in.Scene, projection)
		if err != nil {
			return err
		}
		// Keep the complete bound template hash intact, but enforce chapter
		// results only for this server-resolved operation scope. Full-volume
		// preflight supplies all six chapters and therefore retains every result.
		scoped := make([]tpl.Evaluation, 0, len(results))
		for _, result := range results {
			if strings.HasPrefix(result.TargetRef, "chapter/") && !seen[strings.TrimPrefix(result.TargetRef, "chapter/")] {
				continue
			}
			scoped = append(scoped, result)
		}
		results = scoped
		var failures []docxRuleFailure
		for _, result := range results {
			if result.Severity == tpl.SeverityBlocking && result.Status != tpl.EvaluationPass && result.Status != tpl.EvaluationNotApplicable {
				sectionID := ""
				if strings.HasPrefix(result.TargetRef, "chapter/") {
					sectionID = strings.TrimPrefix(result.TargetRef, "chapter/")
				} else if len(in.Chapters) == 1 {
					sectionID = in.Chapters[0].SectionID
				}
				failures = append(failures, docxRuleFailure{RuleID: result.RuleID, TargetRef: result.TargetRef,
					SectionID: sectionID, Status: result.Status, Message: docxRuleMessage(result.Message)})
			}
		}
		if len(failures) > 0 {
			return &docxRuleBlockedError{Failures: failures}
		}
		return write(tx, DOCXGuardProof{Binding: binding, Evaluations: results, Sources: sourceIDs})
	})
}
