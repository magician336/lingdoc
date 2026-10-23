package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// These aliases keep the generation ledger readable while ensuring the
	// acceptance profile has one source of truth in acceptancebudget.
	generationAcceptanceProfileEnv = acceptancebudget.ProfileEnv
	generationAcceptanceLedgerEnv  = acceptancebudget.LedgerEnv
	generationAcceptanceProfile    = acceptancebudget.Profile
	generationAcceptanceModel      = acceptancebudget.DeepSeekModel
	generationAcceptanceInputMax   = acceptancebudget.InputMax
	generationAcceptanceOutputMax  = acceptancebudget.OutputMax
	generationAcceptanceCallsMax   = acceptancebudget.MaxCalls
	generationAcceptancePerCall    = acceptancebudget.DeepSeekPerCallNanoUSD
	generationAcceptanceTotal      = acceptancebudget.DeepSeekTotalNanoUSD
	generationPromptOverhead       = 1024
	deepSeekInputNanoUSDPerToken   = acceptancebudget.DeepSeekInputNanoUSDPerToken
	deepSeekOutputNanoUSDPerToken  = acceptancebudget.DeepSeekOutputNanoUSDPerToken
)

var (
	errAcceptanceLedgerBusy    = errors.New("acceptance ledger busy")
	errAcceptanceLedgerIO      = errors.New("acceptance ledger unavailable")
	errAcceptanceLedgerBlocked = errors.New("acceptance ledger requires reconciliation")
	errAcceptanceBudgetSpent   = errors.New("acceptance request budget exhausted")
	errAcceptanceInputTooLarge = errors.New("acceptance input exceeds the round limit")
	errAcceptanceWrongModel    = errors.New("acceptance model does not match the profile")
)

type generationAcceptanceBudget struct {
	profile    string
	ledgerPath string
	total      *acceptancebudget.Budget
}

type generationAcceptanceLedger struct {
	SchemaVersion int    `json:"schema_version"`
	Profile       string `json:"profile"`
	Model         string `json:"model"`
	PricingBasis  string `json:"pricing_basis"`
	// CostBasis names what the money columns of this ledger MEAN. A profile
	// without a price basis writes "not_applicable", and its 0 in the money
	// columns reads as "nothing was priced" instead of "the call was free".
	CostBasis     string                        `json:"cost_basis,omitempty"`
	InputMax      int                           `json:"input_max_tokens"`
	OutputMax     int                           `json:"output_max_tokens"`
	CallsMax      int                           `json:"calls_max"`
	CNYBackstop   int                           `json:"human_backstop_cny"`
	ReserveMax    int64                         `json:"reserve_max_nano_usd"`
	Reserved      int64                         `json:"reserved_nano_usd"`
	Actual        int64                         `json:"actual_nano_usd"`
	Blocked       bool                          `json:"blocked"`
	BlockedReason string                        `json:"blocked_reason,omitempty"`
	Requests      []generationAcceptanceRequest `json:"requests"`
}

type generationAcceptanceRequest struct {
	Number                   int    `json:"number"`
	Model                    string `json:"model"`
	Status                   string `json:"status"`
	ReservedNanoUSD          int64  `json:"reserved_nano_usd"`
	EstimatedInputUpperBound int    `json:"estimated_input_token_upper_bound"`
	OutputLimit              int    `json:"output_limit_tokens"`
	InputTokens              int    `json:"input_tokens,omitempty"`
	OutputTokens             int    `json:"output_tokens,omitempty"`
	// TotalTokens is the total token count the provider reported. It is 0
	// when the provider reported none.
	TotalTokens int `json:"total_tokens,omitempty"`
	// UsageStatus says whether the token counters came from the provider.
	// "unavailable" means the provider reported no usage: the amount is
	// 不可核实 (unverifiable), the counters are then empty, and an estimate
	// is never written as an actual usage.
	UsageStatus   string    `json:"usage_status,omitempty"`
	ActualNanoUSD int64     `json:"actual_nano_usd,omitempty"`
	FinishReason  string    `json:"finish_reason,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type generationAcceptanceReservation struct {
	budget     *generationAcceptanceBudget
	total      *acceptancebudget.Reservation
	lockPath   string
	lock       *os.File
	requestNum int
	finished   bool
}

func loadGenerationAcceptanceBudget() (*generationAcceptanceBudget, error) {
	profile := activeAcceptanceProfile()
	if profile == "" {
		return nil, nil
	}
	if !acceptancebudget.SupportedProfile(profile) {
		return nil, generation.ErrAcceptanceBudgetUnavailable
	}
	ledgerPath := strings.TrimSpace(os.Getenv(generationAcceptanceLedgerEnv))
	if !filepath.IsAbs(ledgerPath) || filepath.Ext(ledgerPath) != ".json" {
		return nil, generation.ErrAcceptanceBudgetUnavailable
	}
	total, err := acceptancebudget.NewFromEnv(profile)
	if err != nil {
		return nil, generation.ErrAcceptanceBudgetUnavailable
	}
	return &generationAcceptanceBudget{profile: profile, ledgerPath: filepath.Clean(ledgerPath), total: total}, nil
}

// approvedModel returns the model the active profile approves.
//
// The two call sites (the generation adapter and the agent adapter) pass the
// historical constant generationAcceptanceModel, which names the DeepSeek
// model; both call sites belong to another face and are not changed here.
// The profile is therefore the single source of truth for which model is
// approved, and the call site's name is only accepted as proof that the
// controlled path was taken.
//
// The environment is checked as well: a budget must not spend itself under a
// profile it was not built for.
func (b *generationAcceptanceBudget) approvedModel(requested string) (string, error) {
	profile := b.profileName()
	if active := activeAcceptanceProfile(); active != "" && active != profile {
		return "", errAcceptanceLedgerIO
	}
	approved := acceptancebudget.ProfileApprovedModel(profile)
	if requested != generationAcceptanceModel && requested != approved {
		return "", errAcceptanceWrongModel
	}
	return approved, nil
}

func (b *generationAcceptanceBudget) reserve(ctx context.Context, modelName string, inputUpperBound int) (*generationAcceptanceReservation, error) {
	approved, err := b.approvedModel(modelName)
	if err != nil {
		return nil, err
	}
	local, err := b.reserveChat(ctx, approved, inputUpperBound)
	if err != nil {
		return nil, err
	}
	totalBudget := b.total
	if totalBudget == nil {
		totalBudget = acceptancebudget.New(b.profileName(), b.ledgerPath+".calls.json")
	}
	total, err := totalBudget.Reserve(ctx, approved, inputUpperBound, acceptanceOutputMax(b.profileName()))
	if err != nil {
		if cancelErr := local.cancelNoCall(); cancelErr != nil {
			return nil, errAcceptanceLedgerBlocked
		}
		return nil, mapAcceptanceBudgetError(err)
	}
	local.total = total
	return local, nil
}

func (b *generationAcceptanceBudget) reserveChat(ctx context.Context, modelName string, inputUpperBound int) (*generationAcceptanceReservation, error) {
	profile := b.profileName()
	inputMax := acceptanceInputMax(profile)
	outputMax := acceptanceOutputMax(profile)
	if b == nil || inputUpperBound < 0 || inputUpperBound > inputMax {
		return nil, errAcceptanceInputTooLarge
	}
	lockPath := b.ledgerPath + ".lock"
	lock, err := acceptancebudget.AcquireLock(ctx, lockPath)
	if err != nil {
		return nil, errAcceptanceLedgerBusy
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			_ = acceptancebudget.ReleaseLock(lock, lockPath)
		}
	}()
	ledger, err := readAcceptanceLedger(b.ledgerPath, profile)
	if err != nil {
		return nil, errAcceptanceLedgerIO
	}
	if migrated, ok := migrateGenerationAcceptanceLedger(ledger, profile); ok {
		ledger = migrated
		if err := writeAcceptanceLedger(b.ledgerPath, ledger); err != nil {
			return nil, errAcceptanceLedgerIO
		}
	}
	if !validAcceptanceLedger(ledger) {
		return nil, errAcceptanceLedgerIO
	}
	if ledger.Blocked || acceptanceLedgerHasUnknownCall(ledger) {
		return nil, errAcceptanceLedgerBlocked
	}
	if ledger.Profile != profile || modelName != acceptancebudget.ProfileApprovedModel(profile) {
		return nil, errAcceptanceWrongModel
	}
	reservedPerCall := acceptanceReservedPerCall(profile)
	if (ledger.CallsMax > 0 && len(ledger.Requests) >= ledger.CallsMax) ||
		(reservedPerCall > 0 && reservedPerCall > ledger.ReserveMax-ledger.Reserved) {
		return nil, errAcceptanceBudgetSpent
	}
	request := generationAcceptanceRequest{
		Number:                   len(ledger.Requests) + 1,
		Model:                    modelName,
		Status:                   "in_flight",
		ReservedNanoUSD:          reservedPerCall,
		EstimatedInputUpperBound: inputUpperBound,
		OutputLimit:              outputMax,
		CreatedAt:                time.Now().UTC(),
	}
	ledger.Requests = append(ledger.Requests, request)
	ledger.Reserved += reservedPerCall
	if err := writeAcceptanceLedger(b.ledgerPath, ledger); err != nil {
		return nil, errAcceptanceLedgerIO
	}
	releaseOnError = false
	return &generationAcceptanceReservation{budget: b, lockPath: lockPath, lock: lock, requestNum: request.Number}, nil
}

func mapAcceptanceBudgetError(err error) error {
	switch {
	case errors.Is(err, acceptancebudget.ErrBlocked):
		return errAcceptanceLedgerBlocked
	case errors.Is(err, acceptancebudget.ErrCallsSpent):
		return errAcceptanceBudgetSpent
	case errors.Is(err, acceptancebudget.ErrInputTooLarge):
		return errAcceptanceInputTooLarge
	case errors.Is(err, acceptancebudget.ErrWrongModel):
		return errAcceptanceWrongModel
	default:
		return errAcceptanceLedgerIO
	}
}

// complete records the outcome of one call whose provider reported its usage.
func (r *generationAcceptanceReservation) complete(status string, inputTokens, outputTokens int, finishReason string, block bool) error {
	return r.completeUsage(status, acceptancebudget.Usage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
		Reported:     true,
	}, finishReason, block)
}

// unknown records a call whose outcome or usage could not be confirmed.
//
// The token counters stay empty and the record says the usage was
// unavailable. The estimate of the request is never written as an actual
// usage: an estimate that reads as a measurement is a false record, and it
// is the number a reader would later reconcile against the provider.
func (r *generationAcceptanceReservation) unknown(status string) error {
	return r.completeUsage(status, acceptancebudget.Usage{Reported: false}, "", true)
}

// completeUsage records the outcome of one call together with the usage the
// provider reported.
func (r *generationAcceptanceReservation) completeUsage(status string, usage acceptancebudget.Usage, finishReason string, block bool) error {
	if r == nil || r.finished || r.lock == nil {
		return errAcceptanceLedgerIO
	}
	ledger, err := readAcceptanceLedger(r.budget.ledgerPath)
	if err != nil || r.requestNum < 1 || r.requestNum > len(ledger.Requests) {
		if r.total != nil {
			_ = r.total.Unknown("chat_ledger_unavailable")
		}
		r.release()
		return errAcceptanceLedgerIO
	}
	request := &ledger.Requests[r.requestNum-1]
	if request.Number != r.requestNum || request.Status != "in_flight" {
		if r.total != nil {
			_ = r.total.Unknown("chat_ledger_sequence_mismatch")
		}
		r.release()
		return errAcceptanceLedgerIO
	}
	inputTokens, outputTokens := usage.InputTokens, usage.OutputTokens
	totalTokens := usage.TotalTokens
	if totalTokens <= 0 && usage.Reported {
		totalTokens = inputTokens + outputTokens
	}
	if !usage.Reported {
		inputTokens, outputTokens, totalTokens = 0, 0, 0
	}
	if inputTokens < 0 || outputTokens < 0 {
		// An impossible counter is a limit violation, not a price. The money
		// stays 0 under a profile without a price basis.
		block = true
		status = "limit_violation"
	}
	if inputTokens+outputTokens > acceptanceTotalMax(ledger.Profile) {
		// This is accounting, NOT prevention: the request has already been
		// sent, so the call that exceeded the ceiling has already happened.
		// What this can do is mark the record and freeze the ledger.
		//
		// The ceiling is enforced BEFORE the request: the input bound is
		// checked in reserve(), and the output ceiling travels in the request
		// itself (max_completion_tokens). This check only notices what the
		// provider reported.
		block = true
		status = "limit_violation"
	}
	request.Status = status
	request.InputTokens = inputTokens
	request.OutputTokens = outputTokens
	request.TotalTokens = totalTokens
	request.UsageStatus = acceptancebudget.UsageStatusReported
	if !usage.Reported {
		request.UsageStatus = acceptancebudget.UsageStatusUnavailable
	}
	request.FinishReason = finishReason
	request.ActualNanoUSD = actualAcceptanceCost(r.budget.profileName(), inputTokens, outputTokens)
	ledger.Actual += request.ActualNanoUSD
	if block {
		ledger.Blocked = true
		ledger.BlockedReason = status
	}
	if ledger.Actual > ledger.Reserved || ledger.Actual > ledger.ReserveMax {
		ledger.Blocked = true
		ledger.BlockedReason = "actual usage exceeded reservation"
	}
	writeErr := writeAcceptanceLedger(r.budget.ledgerPath, ledger)
	var totalErr error
	if r.total != nil {
		if block {
			totalErr = r.total.CompleteUsage(status, usage, finishReason, true)
		} else {
			totalErr = r.total.CompleteUsage(status, usage, finishReason, false)
		}
	}
	r.release()
	if writeErr != nil {
		return errAcceptanceLedgerIO
	}
	if totalErr != nil {
		if errors.Is(totalErr, acceptancebudget.ErrCallsSpent) {
			if status == "outcome_unknown" || status == "usage_unknown" {
				return errAcceptanceLedgerBlocked
			}
			return errAcceptanceBudgetSpent
		}
		if errors.Is(totalErr, acceptancebudget.ErrBlocked) {
			return errAcceptanceLedgerBlocked
		}
		return errAcceptanceLedgerIO
	}
	if block || ledger.Blocked {
		if status == "outcome_unknown" || status == "usage_unknown" {
			return errAcceptanceLedgerBlocked
		}
		return errAcceptanceBudgetSpent
	}
	return nil
}

func (r *generationAcceptanceReservation) cancelNoCall() error {
	if r == nil || r.finished || r.lock == nil {
		return errAcceptanceLedgerIO
	}
	ledger, err := readAcceptanceLedger(r.budget.ledgerPath)
	if err != nil || r.requestNum != len(ledger.Requests) {
		_ = r.unknown("cancel_reconciliation_required")
		return errAcceptanceLedgerBlocked
	}
	request := ledger.Requests[r.requestNum-1]
	if request.Status != "in_flight" {
		_ = r.unknown("cancel_reconciliation_required")
		return errAcceptanceLedgerBlocked
	}
	ledger.Requests = ledger.Requests[:len(ledger.Requests)-1]
	ledger.Reserved -= request.ReservedNanoUSD
	writeErr := writeAcceptanceLedger(r.budget.ledgerPath, ledger)
	r.release()
	if writeErr != nil {
		return errAcceptanceLedgerIO
	}
	return nil
}

func (r *generationAcceptanceReservation) release() {
	if r == nil || r.finished {
		return
	}
	r.finished = true
	_ = acceptancebudget.ReleaseLock(r.lock, r.lockPath)
	r.lock = nil
}

// selectDeepSeekAcceptanceModel returns the model the active profile approves.
//
// The name is the historical one and it is kept because the two call sites
// (the generation adapter and the agent adapter) belong to another face and
// this change does not touch them. What the function decides is read from the
// active profile: the DeepSeek profile keeps its own rule, and the Qwen
// profile has its own rule, which approves a different provider, a different
// endpoint and a different model.
func selectDeepSeekAcceptanceModel(models []*types.Model) (*types.Model, bool) {
	if activeAcceptanceProfile() == acceptancebudget.QwenProfile {
		return selectQwenAcceptanceModel(models)
	}
	return selectDeepSeekAcceptanceModelOnly(models)
}

func selectDeepSeekAcceptanceModelOnly(models []*types.Model) (*types.Model, bool) {
	var selected *types.Model
	for _, model := range models {
		if model == nil || model.Name != generationAcceptanceModel {
			continue
		}
		if selected != nil {
			return nil, false
		}
		selected = model
	}
	if selected == nil || selected.Type != types.ModelTypeKnowledgeQA || selected.Source != types.ModelSourceRemote ||
		selected.Status != types.ModelStatusActive || selected.Parameters.Provider != "generic" ||
		strings.TrimRight(selected.Parameters.BaseURL, "/") != "https://api.deepseek.com" ||
		selected.Parameters.ExtraConfig[chat.ExtraConfigThinkingControl] != "thinking_type" {
		return nil, false
	}
	return selected, true
}

// acceptanceGenerationChatOptions returns the request options of one
// controlled call.
//
// The completion budget is the profile's output ceiling, and it travels in
// the request: the provider is asked for no more than the profile allows,
// instead of the request being sent and the answer measured afterwards.
func acceptanceGenerationChatOptions() *chat.ChatOptions {
	disabled := false
	return &chat.ChatOptions{
		Temperature:         0.2,
		MaxCompletionTokens: acceptanceOutputMax(activeAcceptanceProfile()),
		Thinking:            &disabled,
		Format:              json.RawMessage(`{"type":"json_object"}`),
	}
}

// acceptanceInputUpperBound returns the input bound of one request.
//
// The bound is the JSON-encoded length of the messages plus a fixed
// overhead. It is an UPPER BOUND on the token count, not a token count: a
// token is at least one byte, so the byte length of a UTF-8 prompt cannot be
// smaller than its token count. The gate is therefore conservative -- it may
// refuse a request that would have fitted, but it cannot let a request
// through that exceeds the ceiling.
//
// What the byte count cannot see is what the provider adds on its own (a
// chat template, a few control tokens). The fixed overhead covers that
// margin. The provider's own reported usage remains the truth, and it is
// reconciled after the response; this bound is what can be enforced BEFORE
// the request.
func acceptanceInputUpperBound(messages []chat.Message) (int, error) {
	encoded, err := json.Marshal(messages)
	if err != nil {
		return 0, err
	}
	return len(encoded) + generationPromptOverhead, nil
}

// actualAcceptanceCost returns the money one call consumed under one profile.
//
// A profile with a price basis computes the amount. A profile without one
// computes nothing: the amount is not known, and a 0 that reads as a price
// is a false statement. The ledger says which of the two it is through its
// cost basis.
func actualAcceptanceCost(profile string, inputTokens, outputTokens int) int64 {
	// The price basis is checked FIRST, before the impossible-counter guard.
	//
	// A profile without a price basis has no amount to compute at all -- not
	// even for a counter that cannot be true. Returning the priced profile's
	// sentinel here would put a DeepSeek-sized number on a row that reserved
	// nothing, which is exactly the "reserved 0 but actual is an amount"
	// combination the cost basis exists to prevent. The impossible counter
	// itself is still caught by the caller as a limit violation.
	if !acceptancebudget.ProfilePriced(profile) {
		return 0
	}
	if inputTokens < 0 || outputTokens < 0 {
		return generationAcceptanceTotal + 1
	}
	return int64(inputTokens)*deepSeekInputNanoUSDPerToken + int64(outputTokens)*deepSeekOutputNanoUSDPerToken
}

// actualDeepSeekCost returns the money one call consumed.
//
// The name is the historical one and it is kept because the two call sites
// (the generation adapter and the agent adapter) belong to another face. The
// amount is computed under the active profile, so a profile without a price
// basis records no amount at all.
func actualDeepSeekCost(inputTokens, outputTokens int) int64 {
	return actualAcceptanceCost(activeAcceptanceProfile(), inputTokens, outputTokens)
}

func acceptanceLedgerHasUnknownCall(ledger generationAcceptanceLedger) bool {
	for _, request := range ledger.Requests {
		if request.Status == "in_flight" || request.Status == "outcome_unknown" {
			return true
		}
	}
	return false
}

func (b *generationAcceptanceBudget) profileName() string {
	if b.profile == "" {
		return generationAcceptanceProfile
	}
	return b.profile
}

func generationBudgetLimits(profile string) (calls int, reserveUSD int64, backstopCNY int) {
	calls, budget, backstopCNY := acceptancebudget.ProfileLimits(profile)
	if profile == acceptancebudget.CostOnlyProfile {
		return calls, budget / acceptancebudget.DeepSeekBudgetFXNanoCNYPerUSD, backstopCNY
	}
	if !acceptancebudget.ProfilePriced(profile) {
		// This profile computes no money: the reserve is 0 and the ledger
		// says "not applicable" instead of an amount.
		return calls, 0, backstopCNY
	}
	return calls, generationAcceptanceTotal, backstopCNY
}

func readAcceptanceLedger(path string, profiles ...string) (generationAcceptanceLedger, error) {
	profile := generationAcceptanceProfile
	if len(profiles) > 0 {
		profile = profiles[0]
	}
	calls, reserveUSD, backstop := generationBudgetLimits(profile)
	inputMax, outputMax, _ := acceptancebudget.ProfileTokenLimits(profile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return generationAcceptanceLedger{
			SchemaVersion: 1,
			Profile:       profile,
			Model:         acceptancebudget.ProfileApprovedModel(profile),
			PricingBasis:  acceptancePricingBasis(profile),
			CostBasis:     acceptancebudget.ProfileCostBasis(profile),
			InputMax:      inputMax,
			OutputMax:     outputMax,
			CallsMax:      calls,
			CNYBackstop:   backstop,
			ReserveMax:    reserveUSD,
			Requests:      []generationAcceptanceRequest{},
		}, nil
	}
	if err != nil {
		return generationAcceptanceLedger{}, err
	}
	var ledger generationAcceptanceLedger
	if err := json.Unmarshal(data, &ledger); err != nil || ledger.SchemaVersion != 1 {
		return generationAcceptanceLedger{}, errAcceptanceLedgerIO
	}
	return ledger, nil
}

// migrateGenerationAcceptanceLedger upgrades only a validated, settled
// six-call ledger. In-flight or unknown outcomes keep their original policy
// and remain blocked for reconciliation.
func migrateGenerationAcceptanceLedger(ledger generationAcceptanceLedger, target string) (generationAcceptanceLedger, bool) {
	if ledger.Profile == target || target != acceptancebudget.CostOnlyProfile ||
		ledger.Profile != generationAcceptanceProfile || ledger.Blocked ||
		acceptanceLedgerHasUnknownCall(ledger) || !validAcceptanceLedger(ledger) {
		return ledger, false
	}
	migrated := ledger
	migrated.Profile = target
	migrated.CallsMax, migrated.ReserveMax, migrated.CNYBackstop = generationBudgetLimits(target)
	migrated.CostBasis = acceptancebudget.ProfileCostBasis(target)
	if !validAcceptanceLedger(migrated) {
		return ledger, false
	}
	return migrated, true
}

func validAcceptanceLedger(ledger generationAcceptanceLedger) bool {
	profile := ledger.Profile
	calls, reserveUSD, backstop := generationBudgetLimits(profile)
	inputMax, outputMax, totalMax := acceptancebudget.ProfileTokenLimits(profile)
	reservedPerCall := acceptanceReservedPerCall(profile)
	if ledger.SchemaVersion != 1 || !acceptancebudget.SupportedProfile(profile) ||
		ledger.Model != acceptancebudget.ProfileApprovedModel(profile) ||
		ledger.InputMax != inputMax || ledger.OutputMax != outputMax ||
		ledger.CallsMax != calls || ledger.CNYBackstop != backstop || ledger.ReserveMax != reserveUSD ||
		!validAcceptanceCostBasis(ledger.CostBasis, profile) ||
		(calls > 0 && len(ledger.Requests) > calls) || ledger.Reserved > reserveUSD || ledger.Reserved != int64(len(ledger.Requests))*reservedPerCall ||
		ledger.Actual < 0 || ledger.Actual > ledger.Reserved {
		return false
	}
	var actualTotal int64
	for i, request := range ledger.Requests {
		if request.Number != i+1 || request.Model != acceptancebudget.ProfileApprovedModel(profile) ||
			request.ReservedNanoUSD != reservedPerCall ||
			request.EstimatedInputUpperBound < 0 || request.EstimatedInputUpperBound > inputMax ||
			request.OutputLimit != outputMax || request.InputTokens < 0 || request.OutputTokens < 0 ||
			request.TotalTokens < 0 || request.InputTokens+request.OutputTokens > totalMax ||
			!validAcceptanceUsageStatus(request.UsageStatus) {
			return false
		}
		switch request.Status {
		case "in_flight", "outcome_unknown", "usage_unknown", "response_received", "incomplete", "limit_violation":
		default:
			return false
		}
		if request.Status == "outcome_unknown" || request.Status == "usage_unknown" || request.Status == "in_flight" || request.Status == "limit_violation" {
			if !ledger.Blocked {
				return false
			}
		}
		actualTotal += request.ActualNanoUSD
	}
	return actualTotal == ledger.Actual
}

// validAcceptanceCostBasis accepts the cost basis of one ledger.
//
// An empty basis is a ledger written before the basis existed; it reads as
// the priced basis those ledgers were written under. A profile without a
// price basis has no legacy ledgers and must carry its own basis.
func validAcceptanceCostBasis(recorded, profile string) bool {
	if recorded == acceptancebudget.ProfileCostBasis(profile) {
		return true
	}
	return profile != acceptancebudget.QwenProfile && recorded == ""
}

// validAcceptanceUsageStatus accepts the usage status of one request.
//
// An empty status is a record written before the status existed; it reads as
// "reported", because those records only ever carried provider numbers.
func validAcceptanceUsageStatus(status string) bool {
	switch status {
	case "", acceptancebudget.UsageStatusReported, acceptancebudget.UsageStatusUnavailable:
		return true
	default:
		return false
	}
}

func writeAcceptanceLedger(path string, ledger generationAcceptanceLedger) error {
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return errAcceptanceLedgerIO
	}
	tmpPath := fmt.Sprintf("%s.tmp-%d-%d", path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return errAcceptanceLedgerIO
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return errAcceptanceLedgerIO
	}
	return nil
}
