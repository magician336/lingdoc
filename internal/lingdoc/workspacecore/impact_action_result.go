package workspacecore

import (
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"slices"
	"strconv"
	"time"
)

// ImpactComputationReceipt is written only by the server's actual preflight
// evaluator. There is no endpoint that accepts this record from a caller.
type ImpactComputationReceipt struct {
	ID                   string                `json:"id"`
	Version              int64                 `json:"version"`
	ProjectID            string                `json:"project_id"`
	Status               string                `json:"status"`
	ProjectVersion       int64                 `json:"project_version"`
	ContextRevision      int64                 `json:"context_revision"`
	TemplateCopyVersion  int64                 `json:"template_copy_version"`
	RulesetHash          string                `json:"ruleset_hash"`
	InputHash            string                `json:"input_hash"`
	ComputationBasisHash string                `json:"computation_basis_hash"`
	SourceBasisHash      string                `json:"source_basis_hash"`
	SourceIDs            []string              `json:"source_ids"`
	Evaluations          []template.Evaluation `json:"evaluations"`
	CheckedAt            time.Time             `json:"checked_at"`
}
type impactActionResultTransaction interface {
	VerifyImpactActionResult(Project, ImpactTask, []ObjectVersionRef) error
}
type impactComputationSourceTransaction interface {
	ImpactSourceDigest(string, []string) (string, error)
}

func (t gormTransaction) ImpactSourceDigest(projectID string, ids []string) (string, error) {
	tenant, err := t.ImpactProjectTenantID(projectID)
	if err != nil {
		return "", err
	}
	values := []any{}
	for _, id := range sortedImpactSourceIDs(ids) {
		var chunk types.Chunk
		if err := t.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", id, tenant).Take(&chunk).Error; err != nil {
			return "", storageError(err)
		}
		var knowledge types.Knowledge
		if err := t.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", chunk.KnowledgeID, tenant).Take(&knowledge).Error; err != nil {
			return "", storageError(err)
		}
		var assets []struct {
			ID              string
			AssetRevision   int64
			ProcessingState string
		}
		if err := t.db.Table("lingdoc_project_assets").Select("id, asset_revision, processing_state").Where("project_id = ? AND tenant_id = ? AND knowledge_id = ? AND deleted_at IS NULL", projectID, tenant, knowledge.ID).Order("id").Find(&assets).Error; err != nil {
			return "", err
		}
		if len(assets) == 0 {
			return "", ErrSourceUnavailable
		}
		values = append(values, struct {
			FileHash    string
			FileSize    int64
			ProcessedAt *time.Time
			Assets      any
		}{knowledge.FileHash, knowledge.FileSize, knowledge.ProcessedAt, assets})
		values = append(values, struct {
			ID, KnowledgeID, ContentHash, ParserHash, IndexStatus, ParseStatus, EnableStatus string
			Revision                                                                         int
			Enabled                                                                          bool
		}{chunk.ID, chunk.KnowledgeID, hashImpactValue(chunk.Content), hashImpactValue(chunk.SourceContent), chunk.IndexStatus, knowledge.ParseStatus, knowledge.EnableStatus, chunk.ContentRevision, chunk.IsEnabled})
	}
	return hashImpactValue(values), nil
}

// This same current basis must be persisted alongside the actual evaluation.
// Rechecking catches changed parser/source content even without a project edit.
func ImpactComputationBasisHash(tx Transaction, p Project) (string, error) {
	if p.TemplateCopy == nil {
		return "", ErrInvalidState
	}
	chapters, err := tx.Chapters(p.ID)
	if err != nil {
		return "", err
	}
	slices.SortFunc(chapters, func(a, b Chapter) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	ids := []string{}
	values := []any{}
	for _, ch := range chapters {
		ids = append(ids, ch.SourceIDs...)
		var version *ChapterVersion
		if ch.CurrentVersionID != nil {
			v, e := tx.ChapterVersion(p.ID, ch.ID, *ch.CurrentVersionID)
			if e != nil {
				return "", e
			}
			version = &v
		}
		values = append(values, struct {
			Chapter Chapter
			Version *ChapterVersion
		}{ch, version})
	}
	store, ok := tx.(semanticTransaction)
	if !ok {
		return "", ErrInvalidState
	}
	routes, err := store.RouteNodes(p.ID)
	if err != nil {
		return "", err
	}
	for _, node := range routes {
		for _, ref := range node.Inputs {
			state, e := currentImpactObject(tx, p, ref)
			if e != nil {
				return "", e
			}
			ids = append(ids, state.SourceIDs...)
		}
	}
	sourceStore, ok := tx.(impactComputationSourceTransaction)
	if !ok {
		return "", ErrInvalidState
	}
	sourceHash, err := sourceStore.ImpactSourceDigest(p.ID, ids)
	if err != nil {
		return "", err
	}
	return hashImpactValue(struct {
		ProjectID                                                  string
		ProjectVersion, ContextRevision, SpecRevision, CopyVersion int64
		Spec                                                       map[string]string
		RulesetHash                                                string
		Chapters                                                   []any
		Routes                                                     []RouteNode
		SourceHash                                                 string
	}{p.ID, p.ProjectVersion, p.CurrentContextRevision, p.SpecRevision, p.TemplateCopyVersion, p.Spec, p.TemplateCopy.RulesetHash, values, routes, sourceHash}), nil
}
func readImpactComputation(tx Transaction, p Project, id string) (ImpactComputationReceipt, error) {
	store, ok := tx.(runtimeTransaction)
	if !ok {
		return ImpactComputationReceipt{}, ErrInvalidState
	}
	tenant, err := impactProjectTenantID(tx, p)
	if err != nil {
		return ImpactComputationReceipt{}, err
	}
	var receipt ImpactComputationReceipt
	version, err := store.ReadRuntimeRecord(tenant, p.ID, "preflight_check", id, &receipt)
	if err != nil {
		return receipt, err
	}
	if receipt.ID != id || receipt.ProjectID != p.ID || receipt.Version != version || receipt.InputHash == "" || receipt.ComputationBasisHash == "" || receipt.CheckedAt.IsZero() {
		return receipt, ErrInvalidState
	}
	return receipt, nil
}
func currentImpactResultObject(tx Transaction, p Project, ref ObjectVersionRef) (ImpactObjectState, error) {
	state := ImpactObjectState{Ref: ref, State: "CURRENT", SourceIDs: []string{}, ContextRevision: p.CurrentContextRevision}
	switch ref.Kind {
	case "ResearchRun":
		run, _, err := loadStoredObject[ResearchRun](tx, p.ID, ResearchRunKind, ref.ID)
		if err != nil {
			return state, err
		}
		state.Ref.Version = strconv.FormatInt(run.Version, 10)
		state.ContextRevision = run.Basis.ContextRevision
		state.Confirmed = run.Status == "completed" && versionBasisCurrent(p, run.Basis)
		for _, result := range run.Results {
			if result.SourceID != "" {
				state.SourceIDs = append(state.SourceIDs, result.SourceID)
			}
		}
	case "ComputationReceipt":
		receipt, err := readImpactComputation(tx, p, ref.ID)
		if err != nil {
			return state, err
		}
		state.Ref.Version = strconv.FormatInt(receipt.Version, 10)
		state.ContextRevision = receipt.ContextRevision
		state.SourceIDs = receipt.SourceIDs
		state.Confirmed = receipt.ContextRevision == p.CurrentContextRevision && receipt.ProjectVersion == p.ProjectVersion && slices.Contains([]string{"passed", "blocked"}, receipt.Status)
	default:
		return state, ErrInvalidRequest
	}
	state.SourceIDs = sortedImpactSourceIDs(state.SourceIDs)
	if state.Ref.Version != ref.Version {
		return state, ErrVersionConflict
	}
	return state, nil
}
func verifyImpactActionResult(tx Transaction, p Project, task ImpactTask, refs []ObjectVersionRef) error {
	store, ok := tx.(impactActionResultTransaction)
	if !ok {
		return ErrInvalidState
	}
	return store.VerifyImpactActionResult(p, task, refs)
}
func (t gormTransaction) VerifyImpactActionResult(p Project, task ImpactTask, refs []ObjectVersionRef) error {
	if task.TargetRevision != p.CurrentContextRevision || len(refs) != 1 {
		return ErrInvalidState
	}
	ref := refs[0]
	switch task.Action {
	case "RETRIEVE_CANDIDATES":
		if ref.Kind != "ResearchRun" {
			return ErrInvalidState
		}
		run, _, err := loadStoredObject[ResearchRun](t, p.ID, ResearchRunKind, ref.ID)
		if err != nil {
			return err
		}
		if ref.Version != strconv.FormatInt(run.Version, 10) || run.Status != "completed" || run.FinishedAt == nil || run.CorrelationID == "" || run.CreatedBy == "" || len(run.Rounds) < 2 || !versionBasisCurrent(p, run.Basis) {
			return ErrInvalidState
		}
		research, _, err := loadStoredObject[ResearchTask](t, p.ID, ResearchTaskKind, run.TaskID)
		if err != nil {
			return err
		}
		if research.Scope != "impact:"+task.ID || research.LatestRunID != run.ID || research.Status != "completed" || research.Paused || !versionBasisCurrent(p, research.Basis) {
			return ErrInvalidState
		}
		approvedRow, err := t.ObjectVersion(p.ID, ResearchTaskKind, research.ID, run.TaskVersion)
		if err != nil {
			return err
		}
		var approved ResearchTask
		if err := json.Unmarshal(approvedRow.Payload, &approved); err != nil {
			return err
		}
		if approved.Status != "approved" || approved.ApprovedBy == "" || approved.Scope != "impact:"+task.ID || hashImpactValue(approved.Basis) != hashImpactValue(run.Basis) || hashImpactValue(researchReceiptInput(approved)) != hashImpactValue(researchReceiptInput(research)) {
			return ErrInvalidState
		}
		for _, round := range run.Rounds {
			if round.Status != "completed" || round.Provider == "" || round.RetrievedAt.IsZero() {
				return ErrInvalidState
			}
		}
		for _, asset := range run.Basis.AssetVersions {
			truth, e := t.AssetTruth(p, asset.AssetID)
			if e != nil {
				return e
			}
			if truth.Asset.AssetRevision != int64(asset.AssetRevision) {
				return ErrVersionConflict
			}
		}
		if len(research.AssetIDs) != len(run.Basis.AssetVersions) {
			return ErrInvalidState
		}
		seen := map[string]bool{}
		for _, v := range run.Basis.AssetVersions {
			seen[v.AssetID] = true
		}
		for _, id := range research.AssetIDs {
			if !seen[id] {
				return ErrInvalidState
			}
		}
		// A completed search may honestly return no candidates. This receipt proves
		// the search ran; it does not confirm an evidence card or scientific claim.
		return nil
	case "RECOMPUTE":
		if ref.Kind != "ComputationReceipt" {
			return ErrInvalidState
		}
		receipt, err := readImpactComputation(t, p, ref.ID)
		if err != nil {
			return err
		}
		if ref.Version != strconv.FormatInt(receipt.Version, 10) || p.TemplateCopy == nil || receipt.ProjectVersion != p.ProjectVersion || receipt.ContextRevision != p.CurrentContextRevision || receipt.TemplateCopyVersion != p.TemplateCopyVersion || receipt.RulesetHash != p.TemplateCopy.RulesetHash || !slices.Contains([]string{"passed", "blocked"}, receipt.Status) {
			return ErrVersionConflict
		}
		digest, err := ImpactComputationBasisHash(t, p)
		if err != nil {
			return err
		}
		sourceHash, err := t.ImpactSourceDigest(p.ID, receipt.SourceIDs)
		if err != nil {
			return err
		}
		if receipt.SourceBasisHash == "" || sourceHash != receipt.SourceBasisHash {
			return ErrVersionConflict
		}
		if digest != receipt.ComputationBasisHash {
			return ErrVersionConflict
		}
		activeCount := 0
		matched := task.Target.Kind != "RuleBinding"
		if p.TemplateCopy.RuleSetSnapshot != nil {
			for _, binding := range p.TemplateCopy.RuleSetSnapshot.Bindings {
				if binding.Enabled {
					activeCount++
				}
				if task.Target.Kind == "RuleBinding" && binding.RuleID == task.Target.ID && !binding.Enabled {
					matched = true
				}
			}
		} else {
			activeCount = len(p.TemplateCopy.Definition.Rules)
		}
		if len(receipt.Evaluations) == 0 && activeCount > 0 {
			return ErrInvalidState
		}
		for _, evaluation := range receipt.Evaluations {
			if evaluation.RulesetHash != receipt.RulesetHash || evaluation.EvaluatorVersion != template.EvaluatorVersion || evaluation.RuleID == "" || evaluation.TargetRef == "" || evaluation.TargetVersion == "" || evaluation.Status == template.EvaluationUnknown || !slices.Contains([]template.EvaluationStatus{template.EvaluationPass, template.EvaluationIssue, template.EvaluationNotApplicable}, evaluation.Status) {
				return ErrInvalidState
			}
			if evaluation.RuleID == task.Target.ID {
				matched = true
			}
		}
		if !matched {
			return ErrInvalidState
		}
		return nil
	}
	return ErrInvalidRequest
}

// Receipt source reads are independent of closure success, so current source
// policy is still checked for stale or failed receipt references.
func impactResultSourceIDs(tx Transaction, p Project, ref ObjectVersionRef) ([]string, error) {
	if ref.Kind == "ResearchRun" {
		run, _, err := loadStoredObject[ResearchRun](tx, p.ID, ResearchRunKind, ref.ID)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for _, r := range run.Results {
			ids = append(ids, r.SourceID)
		}
		assetReader, ok := tx.(assetPolicyTransaction)
		if !ok {
			return nil, ErrInvalidState
		}
		for _, asset := range run.Basis.AssetVersions {
			truth, e := assetReader.AssetTruth(p, asset.AssetID)
			if e != nil {
				return nil, e
			}
			ids = append(ids, truth.SourceIDs...)
		}
		return sortedImpactSourceIDs(ids), nil
	}
	receipt, err := readImpactComputation(tx, p, ref.ID)
	return receipt.SourceIDs, err
}

func researchReceiptInput(task ResearchTask) any {
	return struct {
		ChapterID, Goal, QuestionType, Scope, ProviderID, ExpectedOutput string
		Exclusions, Queries, AssetIDs                                    []string
	}{task.ChapterID, task.Goal, task.QuestionType, task.Scope, task.ProviderID, task.ExpectedOutput, task.Exclusions, task.Queries, task.AssetIDs}
}
