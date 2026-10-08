package delivery

import (
	"fmt"
	"sort"
	"strconv"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

type TemplateCheckStatus string

const (
	TemplateCheckPassed       TemplateCheckStatus = "passed"
	TemplateCheckBlocked      TemplateCheckStatus = "blocked"
	TemplateCheckNotEvaluated TemplateCheckStatus = "not_evaluated"
)

// TemplateCheckResult is the explainable G4 view of the same rules used by
// Preflight. It does not grant export permission; Prepare always evaluates the
// frozen input again through Evaluate.
type TemplateCheckResult struct {
	ProjectVersion      int                          `json:"project_version"`
	TemplateCopyID      string                       `json:"template_copy_id,omitempty"`
	TemplateCopyVersion int64                        `json:"template_copy_version,omitempty"`
	ContentHash         string                       `json:"content_hash,omitempty"`
	RulesetHash         string                       `json:"ruleset_hash"`
	Status              TemplateCheckStatus          `json:"status"`
	Evaluations         []lingdoctemplate.Evaluation `json:"evaluations"`
}

// EvaluateTemplate executes the allowlisted evaluator on a deterministic
// projection of either the editing input or the immutable frozen input.
func EvaluateTemplate(input DeliveryInput) (TemplateCheckResult, error) {
	targetVersion := fmt.Sprintf("project/%s/version/%d/spec/%d/copy/%d", input.ProjectID, input.ProjectVersion, input.SpecRevision, input.TemplateCopyVersion)
	projection := lingdoctemplate.EvaluationInput{
		ProjectID: input.ProjectID, TargetVersion: targetVersion,
		Fields: cloneSpec(input.Spec), Chapters: []lingdoctemplate.ChapterInput{},
		Sources: []lingdoctemplate.SourceInput{}, ReviewItems: []lingdoctemplate.ReviewItemInput{},
	}
	for _, chapter := range input.Chapters {
		if chapter.SectionID == "" {
			continue
		}
		confirmed := confirmationMatches(input, chapter)
		version := ""
		if chapter.ChapterVersionID != nil {
			version = *chapter.ChapterVersionID
		}
		projection.Chapters = append(projection.Chapters, lingdoctemplate.ChapterInput{
			ID: chapter.SectionID, Version: version, Body: chapter.BodyMarkdown, Confirmed: confirmed,
		})
		decisions := map[string]bool{}
		if chapter.Confirmation != nil {
			for _, decision := range chapter.Confirmation.Decisions {
				if decision.Disposition != "" {
					decisions[decision.ReviewItemID] = true
				}
			}
		}
		for _, item := range chapter.ReviewItems {
			projection.ReviewItems = append(projection.ReviewItems, lingdoctemplate.ReviewItemInput{
				ID: item.ID, Version: version, Decided: decisions[item.ID],
			})
		}
	}
	sort.Slice(projection.Chapters, func(i, j int) bool { return projection.Chapters[i].ID < projection.Chapters[j].ID })
	sort.Slice(projection.ReviewItems, func(i, j int) bool {
		if projection.ReviewItems[i].ID != projection.ReviewItems[j].ID {
			return projection.ReviewItems[i].ID < projection.ReviewItems[j].ID
		}
		return projection.ReviewItems[i].Version < projection.ReviewItems[j].Version
	})
	sources := make(map[string]FrozenSource, len(input.Sources))
	for _, source := range input.Sources {
		sources[source.ID] = source
	}
	allowedAssets := stringSet(input.PolicyAssetIDs)
	assetRevisions := assetVersions(input.AssetVersions)
	referenced := map[string]bool{}
	for _, chapter := range input.Chapters {
		for _, sourceID := range chapter.SourceIDs {
			referenced[sourceID] = true
		}
	}
	sourceIDs := make([]string, 0, len(referenced))
	for id := range referenced {
		sourceIDs = append(sourceIDs, id)
	}
	sort.Strings(sourceIDs)
	for _, id := range sourceIDs {
		source, exists := sources[id]
		available := exists && source.ProjectID == input.ProjectID && allowedAssets[source.AssetID] && assetRevisions[source.AssetID] == source.AssetRevision && source.AssetRevision > 0 && source.Locator != "" && sha256Text(source.QuotedText) == source.QuotedTextHash
		version := ""
		if exists {
			version = strconv.Itoa(source.AssetRevision)
		}
		projection.Sources = append(projection.Sources, lingdoctemplate.SourceInput{ID: id, Version: version, Available: available})
	}

	evaluations, err := lingdoctemplate.Evaluate(input.Template, projection)
	if err != nil {
		return TemplateCheckResult{}, err
	}
	result := TemplateCheckResult{
		ProjectVersion: input.ProjectVersion, TemplateCopyID: input.TemplateCopyID,
		TemplateCopyVersion: input.TemplateCopyVersion, ContentHash: input.TemplateCopyContentHash,
		RulesetHash: input.Template.RulesetHash, Status: TemplateCheckPassed,
		Evaluations: evaluations,
	}
	if len(input.Template.Rules) == 0 {
		result.Status = TemplateCheckNotEvaluated
	}
	for _, evaluation := range evaluations {
		if evaluation.Status == lingdoctemplate.EvaluationIssue && evaluation.Severity == SeverityBlocking {
			result.Status = TemplateCheckBlocked
			break
		}
	}
	return result, nil
}

func confirmationMatches(input DeliveryInput, chapter SnapshotChapter) bool {
	if chapter.Confirmation == nil || chapter.ChapterVersionID == nil {
		return false
	}
	confirmation := chapter.Confirmation
	return confirmation.ID != "" && confirmation.ChapterID == chapter.ChapterID &&
		confirmation.ChapterVersionID == *chapter.ChapterVersionID && confirmation.SpecRevision == input.SpecRevision &&
		confirmation.TemplateVersion == input.Template.Version && confirmation.ActorUserID != "" && !confirmation.CreatedAt.IsZero()
}

func cloneSpec(values map[string]string) map[string]string {
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
