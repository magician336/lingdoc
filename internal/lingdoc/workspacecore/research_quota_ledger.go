package workspacecore

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
)

const researchQuotaWindowKind = "research_quota_window"
const researchQuotaEntryKind = "research_quota_entry"
const researchQuotaHeadKind = "research_quota_head"

var ErrResearchQuotaExhausted = clienterror.New(clienterror.Form{Status: 429, Code: "research_budget_exhausted", Message: "项目当前窗口的累计调研额度不足。", Retryable: false}, "lingdoc: cumulative research quota exhausted")

type ResearchQuotaAmount struct {
	Searches   int64 `json:"searches"`
	Results    int64 `json:"results"`
	ModelCalls int64 `json:"model_calls"`
}

// ResearchQuotaPolicy contains explicit approved window boundaries. The ledger
// neither invents quota values nor guesses whether a reset is daily/monthly.
type ResearchQuotaPolicy struct {
	Version    string              `json:"version"`
	ApprovedBy string              `json:"approved_by"`
	WindowID   string              `json:"window_id"`
	StartsAt   time.Time           `json:"starts_at"`
	EndsAt     time.Time           `json:"ends_at"`
	Limit      ResearchQuotaAmount `json:"limit"`
}

// ResearchQuotaPolicySource returns an already-loaded approved configuration
// snapshot. It must not perform network calls inside the project transaction.
type ResearchQuotaPolicySource interface {
	ResearchQuotaPolicy(context.Context, Actor, string) (ResearchQuotaPolicy, error)
}

type ResearchQuotaWindow struct {
	Policy   ResearchQuotaPolicy `json:"policy"`
	Charged  ResearchQuotaAmount `json:"charged"`
	Measured ResearchQuotaAmount `json:"measured"`
}

type researchQuotaEntry struct {
	ID           string              `json:"id"`
	WindowID     string              `json:"window_id"`
	RequestHash  string              `json:"request_hash"`
	CreatedBy    string              `json:"created_by"`
	Allowance    ResearchQuotaAmount `json:"allowance"`
	Charged      ResearchQuotaAmount `json:"charged"`
	Measured     ResearchQuotaAmount `json:"measured"`
	Closed       bool                `json:"closed"`
	Outcome      string              `json:"outcome,omitempty"`
	InputTokens  int                 `json:"input_tokens,omitempty"`
	OutputTokens int                 `json:"output_tokens,omitempty"`
}

// ResearchQuotaLedger uses the existing authorized project transaction and
// versioned object store, so SQLite/PostgreSQL recovery and atomic audit share
// the same owner as ResearchRun. A nil policy source always fails closed.
type ResearchQuotaLedger struct {
	service  *Service
	policies ResearchQuotaPolicySource
	now      func() time.Time
}

type researchQuotaProjectLocker interface {
	LockResearchQuotaProject(uint64, string) error
}

func NewResearchQuotaLedger(service *Service, policies ResearchQuotaPolicySource) *ResearchQuotaLedger {
	return &ResearchQuotaLedger{service: service, policies: policies, now: time.Now}
}

func quotaObjectID(kind, id string) string {
	return kind + ":" + operationcore.ValueHash(id)
}

func researchPlanHash(task ResearchTask) string {
	task.Version, task.Status, task.Paused = 0, "", false
	task.LatestRunID, task.ApprovedBy = "", ""
	task.CreatedAt, task.UpdatedAt = time.Time{}, time.Time{}
	return operationcore.ValueHash(task)
}

func validQuotaAmount(amount ResearchQuotaAmount) bool {
	return amount.Searches >= 0 && amount.Results >= 0 && amount.ModelCalls >= 0
}

func quotaFits(amount, limit ResearchQuotaAmount) bool {
	return validQuotaAmount(amount) && amount.Searches <= limit.Searches && amount.Results <= limit.Results && amount.ModelCalls <= limit.ModelCalls
}

func quotaAdd(left, right ResearchQuotaAmount) ResearchQuotaAmount {
	return ResearchQuotaAmount{left.Searches + right.Searches, left.Results + right.Results, left.ModelCalls + right.ModelCalls}
}

func quotaSubtract(left, right ResearchQuotaAmount) ResearchQuotaAmount {
	return ResearchQuotaAmount{left.Searches - right.Searches, left.Results - right.Results, left.ModelCalls - right.ModelCalls}
}

func (l *ResearchQuotaLedger) transaction(ctx context.Context, actor Actor, projectID string, action AuthorizationAction, write func(Transaction, Project) error) error {
	if l == nil || l.service == nil {
		return ErrResearchBudgetUnavailable
	}
	return l.service.repository.Transaction(ctx, TransactionOptions{RetryLocks: true}, func(tx Transaction) error {
		locker, ok := tx.(researchQuotaProjectLocker)
		if !ok {
			return ErrResearchBudgetUnavailable
		}
		if err := locker.LockResearchQuotaProject(actor.TenantID, projectID); err != nil {
			return err
		}
		project, err := l.service.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, action, ""))
		if err != nil {
			return err
		}
		if _, err = agentObjectStore(tx); err != nil {
			return err
		}
		return write(tx, project)
	})
}

func (l *ResearchQuotaLedger) policy(ctx context.Context, actor Actor, projectID string) (ResearchQuotaPolicy, error) {
	if l.policies == nil {
		return ResearchQuotaPolicy{}, ErrResearchBudgetUnavailable
	}
	policy, err := l.policies.ResearchQuotaPolicy(ctx, actor, projectID)
	if err != nil {
		return ResearchQuotaPolicy{}, err
	}
	now := l.now().UTC()
	if strings.TrimSpace(policy.Version) == "" || strings.TrimSpace(policy.ApprovedBy) == "" || strings.TrimSpace(policy.WindowID) == "" ||
		policy.StartsAt.IsZero() || !policy.EndsAt.After(policy.StartsAt) || now.Before(policy.StartsAt) || !now.Before(policy.EndsAt) || !validQuotaAmount(policy.Limit) {
		return ResearchQuotaPolicy{}, ErrResearchBudgetUnavailable
	}
	return policy, nil
}

func (l *ResearchQuotaLedger) Reserve(ctx context.Context, actor Actor, projectID, runID string, task ResearchTask) error {
	return l.transaction(ctx, actor, projectID, AuthorizationResearch, func(tx Transaction, project Project) error {
		run, _, err := loadStoredObject[ResearchRun](tx, projectID, ResearchRunKind, runID)
		if err != nil {
			return err
		}
		if run.ID != runID || run.ProjectID != projectID || run.Status != "running" && run.Status != "paused" {
			return ErrInvalidState
		}
		storedTask, _, err := loadStoredObject[ResearchTask](tx, projectID, ResearchTaskKind, run.TaskID)
		if err != nil {
			return err
		}
		if run.CreatedBy != actor.UserID || run.TaskID != task.ID || storedTask.LatestRunID != runID || researchPlanHash(storedTask) != researchPlanHash(task) || !versionBasisCurrent(project, run.Basis) {
			return ErrVersionConflict
		}
		if len(task.Queries) == 0 || len(task.Queries) > researchTaskMaximumQueries {
			return ErrInvalidRequest
		}
		entry := researchQuotaEntry{ID: quotaObjectID("run", runID), RequestHash: researchPlanHash(task), CreatedBy: actor.UserID,
			Allowance: ResearchQuotaAmount{Searches: int64(len(task.Queries)), Results: int64(len(task.Queries) * researchResultsPerRound)}}
		return l.reserve(ctx, tx, actor, projectID, entry)
	})
}

func (l *ResearchQuotaLedger) ReserveModelCall(ctx context.Context, actor Actor, projectID, turnID string) error {
	return l.transaction(ctx, actor, projectID, AuthorizationAgent, func(tx Transaction, _ Project) error {
		turn, _, err := loadStoredObject[AgentTurn](tx, projectID, AgentTurnKind, turnID)
		if err != nil {
			return err
		}
		if turn.CreatedBy != actor.UserID || turn.Agent != AgentResearch || turn.Status != "running" {
			return ErrInvalidState
		}
		entry := researchQuotaEntry{ID: quotaObjectID("turn", turnID), RequestHash: operationcore.ValueHash(turn), CreatedBy: actor.UserID, Allowance: ResearchQuotaAmount{ModelCalls: 1}}
		return l.reserve(ctx, tx, actor, projectID, entry)
	})
}

func (l *ResearchQuotaLedger) reserve(ctx context.Context, tx Transaction, actor Actor, projectID string, entry researchQuotaEntry) error {
	policy, err := l.policy(ctx, actor, projectID)
	if err != nil {
		return err
	}
	previous, _, err := loadStoredObject[researchQuotaEntry](tx, projectID, researchQuotaEntryKind, entry.ID)
	if err == nil {
		if previous.RequestHash != entry.RequestHash || previous.CreatedBy != actor.UserID || previous.WindowID != policy.WindowID || previous.Closed {
			return ErrVersionConflict
		}
		window, _, readErr := loadStoredObject[ResearchQuotaWindow](tx, projectID, researchQuotaWindowKind, quotaObjectID("window", previous.WindowID))
		if readErr != nil {
			return readErr
		}
		if operationcore.ValueHash(window.Policy) != operationcore.ValueHash(policy) {
			return ErrVersionConflict
		}
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	window, old, err := loadStoredObject[ResearchQuotaWindow](tx, projectID, researchQuotaWindowKind, quotaObjectID("window", policy.WindowID))
	if errors.Is(err, ErrNotFound) {
		window = ResearchQuotaWindow{Policy: policy}
	} else if err != nil {
		return err
	}
	if operationcore.ValueHash(window.Policy) != operationcore.ValueHash(policy) {
		return ErrVersionConflict
	}
	// Subtract before adding so even a configured MaxInt64 limit cannot overflow.
	if !quotaFits(window.Charged, policy.Limit) || !quotaFits(entry.Allowance, quotaSubtract(policy.Limit, window.Charged)) {
		return ErrResearchQuotaExhausted
	}
	head, headOld, err := loadStoredObject[ResearchQuotaPolicy](tx, projectID, researchQuotaHeadKind, "project")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil && head.WindowID != policy.WindowID && policy.StartsAt.Before(head.EndsAt) {
		return ErrVersionConflict
	}
	if err == nil && head.WindowID == policy.WindowID && operationcore.ValueHash(head) != operationcore.ValueHash(policy) {
		return ErrVersionConflict
	}
	if head.WindowID != policy.WindowID {
		if err = storeStoredObject(tx, headOld, projectID, researchQuotaHeadKind, "project", headOld.Version+1, policy); err != nil {
			return err
		}
	}
	entry.WindowID, entry.Charged = policy.WindowID, entry.Allowance
	window.Charged = quotaAdd(window.Charged, entry.Charged)
	if err = storeStoredObject(tx, old, projectID, researchQuotaWindowKind, quotaObjectID("window", policy.WindowID), old.Version+1, window); err != nil {
		return err
	}
	if err = storeStoredObject(tx, StoredObject{}, projectID, researchQuotaEntryKind, entry.ID, 1, entry); err != nil {
		return err
	}
	return recordAgentAuditEvent(tx, actor, projectID, "g2.research_budget.reserved", entry.ID, "", map[string]any{"window_id": policy.WindowID, "policy_version": policy.Version, "allowance": entry.Allowance})
}

func (l *ResearchQuotaLedger) Settle(ctx context.Context, actor Actor, projectID, runID string, usage ResearchUsageLedger) error {
	return l.transaction(ctx, actor, projectID, AuthorizationResearch, func(tx Transaction, _ Project) error {
		run, _, err := loadStoredObject[ResearchRun](tx, projectID, ResearchRunKind, runID)
		if err != nil {
			return err
		}
		if run.CreatedBy != actor.UserID {
			return ErrVersionConflict
		}
		normalizeResearchUsage(&run)
		if usage.PolicyVersion != researchRunBudgetPolicyVersion || usage.SearchesUsed != run.Usage.SearchesUsed || usage.ResultsUsed != run.Usage.ResultsUsed || usage.ModelCallsUsed != run.Usage.ModelCallsUsed {
			return ErrResearchBudgetSettlementUnknown
		}
		measured := ResearchQuotaAmount{int64(usage.SearchesUsed), int64(usage.ResultsUsed), int64(usage.ModelCallsUsed)}
		terminal := run.Status != "running" && run.Status != "paused"
		return l.settle(tx, actor, projectID, quotaObjectID("run", runID), measured, terminal, "", 0, 0)
	})
}

func (l *ResearchQuotaLedger) SettleModelCall(ctx context.Context, actor Actor, projectID, turnID string, usage ResearchModelCallUsage) error {
	return l.transaction(ctx, actor, projectID, AuthorizationAgent, func(tx Transaction, _ Project) error {
		turn, _, err := loadStoredObject[AgentTurn](tx, projectID, AgentTurnKind, turnID)
		if err != nil {
			return err
		}
		if turn.CreatedBy != actor.UserID || turn.Agent != AgentResearch {
			return ErrVersionConflict
		}
		if usage.ModelCallsUsed != 1 || usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.Outcome != "succeeded" && usage.Outcome != "failed" {
			return ErrInvalidRequest
		}
		return l.settle(tx, actor, projectID, quotaObjectID("turn", turnID), ResearchQuotaAmount{ModelCalls: 1}, true, usage.Outcome, usage.InputTokens, usage.OutputTokens)
	})
}

func (l *ResearchQuotaLedger) settle(tx Transaction, actor Actor, projectID, entryID string, measured ResearchQuotaAmount, closed bool, outcome string, inputTokens, outputTokens int) error {
	entry, old, err := loadStoredObject[researchQuotaEntry](tx, projectID, researchQuotaEntryKind, entryID)
	if err != nil {
		return err
	}
	if entry.CreatedBy != actor.UserID || !quotaFits(measured, entry.Allowance) || !quotaFits(entry.Measured, measured) {
		return ErrVersionConflict
	}
	if entry.Closed && (!closed || entry.Measured != measured || entry.Outcome != outcome || entry.InputTokens != inputTokens || entry.OutputTokens != outputTokens) {
		return ErrVersionConflict
	}
	if entry.Measured == measured && entry.Closed == closed && entry.Outcome == outcome && entry.InputTokens == inputTokens && entry.OutputTokens == outputTokens {
		return nil
	}
	window, windowOld, err := loadStoredObject[ResearchQuotaWindow](tx, projectID, researchQuotaWindowKind, quotaObjectID("window", entry.WindowID))
	if err != nil {
		return err
	}
	nextCharge := entry.Allowance
	if closed {
		nextCharge = measured
	}
	window.Measured = quotaAdd(window.Measured, quotaSubtract(measured, entry.Measured))
	window.Charged = quotaAdd(quotaSubtract(window.Charged, entry.Charged), nextCharge)
	if !quotaFits(window.Measured, window.Charged) || !quotaFits(window.Charged, window.Policy.Limit) {
		return ErrResearchBudgetSettlementUnknown
	}
	entry.Charged, entry.Measured, entry.Closed = nextCharge, measured, closed
	entry.Outcome, entry.InputTokens, entry.OutputTokens = outcome, inputTokens, outputTokens
	if err = storeStoredObject(tx, windowOld, projectID, researchQuotaWindowKind, windowOld.ID, windowOld.Version+1, window); err != nil {
		return err
	}
	if err = storeStoredObject(tx, old, projectID, researchQuotaEntryKind, entryID, old.Version+1, entry); err != nil {
		return err
	}
	return recordAgentAuditEvent(tx, actor, projectID, "g2.research_budget.settled", entry.ID, "", map[string]any{"window_id": entry.WindowID, "measured": measured, "closed": closed})
}

// Snapshot is an authorized read, not a reset/reconciliation side effect.
func (l *ResearchQuotaLedger) Snapshot(ctx context.Context, actor Actor, projectID string) (ResearchQuotaWindow, error) {
	var out ResearchQuotaWindow
	if l == nil || l.service == nil {
		return out, ErrResearchBudgetUnavailable
	}
	err := l.service.readWithCapability(ctx, actor, projectID, "read", func(tx Transaction, _ Project) error {
		policy, err := l.policy(ctx, actor, projectID)
		if err != nil {
			return err
		}
		out, _, err = loadStoredObject[ResearchQuotaWindow](tx, projectID, researchQuotaWindowKind, quotaObjectID("window", policy.WindowID))
		if errors.Is(err, ErrNotFound) {
			out = ResearchQuotaWindow{Policy: policy}
			return nil
		}
		if err == nil && operationcore.ValueHash(out.Policy) != operationcore.ValueHash(policy) {
			return ErrVersionConflict
		}
		return err
	})
	return out, err
}

var _ ProjectResearchBudgetLedger = (*ResearchQuotaLedger)(nil)
