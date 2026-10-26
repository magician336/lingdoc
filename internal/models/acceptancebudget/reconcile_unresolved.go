package acceptancebudget

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// The recovery channel for calls whose OUTCOME could not be confirmed.
//
// # The gap this closes
//
// A reserved call leaves the window in one of two states: the provider
// reported its usage (the call is settled), or it did not (the call is
// UNRESOLVED and the window freezes). #671 gave the frozen window a
// recovery channel for exactly ONE of the unresolved shapes -- the provider's
// HTTP error -- and left the others without any way back:
//
//	outcome_unknown                 the transport failed
//	usage_unknown                   the provider answered, the usage did not arrive
//	in_flight                       the process died between reserve and complete
//	cancel_reconciliation_required  a cancelled reservation needs a human
//
// Every one of them leaves hasUnresolved true and ledger.Blocked true, so
// Reserve answers ErrBlocked for ever: the authorized window is retired by
// the FIRST transport hiccup, and the operator has no legitimate way to
// reopen it. Hand-editing the ledger file is not a way back -- that is
// rewriting the accounting, not reconciling it.
//
// # What the settlement does, and what it never does
//
// The settlement is an EXPLICIT LOCAL OPERATOR OPERATION. It never calls a
// provider, never touches the network, and never loosens a budget limit.
//
//   - It NEVER REFUNDS. A call whose outcome is unconfirmable may well have
//     been executed by the provider, so the settlement charges the FULL
//     reserved upper bound. The reserved token and money counters, and the
//     call count, keep the failed call: nothing the window spent comes back.
//   - It NEVER REWRITES HISTORY. The settled row keeps its status, its token
//     counters, its money columns and its usage status EXACTLY as the
//     failure produced them. The settlement only APPENDS an annotation.
//   - It IS BOUND TO ONE EXACT SNAPSHOT. The operator supplies the SHA256 of
//     the current ledger bytes; if the file does not match, nothing is
//     written at all.
//   - It NEEDS AN EXPLICIT CONFIRMATION. Without the operator's
//     acknowledgement that the full reserved upper bound is charged, nothing
//     is written.
//   - It SETTLES EXACTLY ONE ROW. If another row is still unresolved, the
//     settlement is refused, so the operator cannot settle the wrong one.
//
// # The chat profiles
//
// The chat profiles carry no price basis: their money columns say
// "not_applicable", which means "never computed", never "free". There is no
// amount to charge there, and the settlement does not invent one: the
// annotation records the same conservative full-reserve charge, whose
// amounts are 0 under a basis that computes nothing.
//
// What the chat profile does have is a TOKEN reservation, and that is the
// number the window is governed by. The settlement keeps it counted: the
// failed call's reservation is never given back, so the remaining cumulative
// budget stays provable. See ledgerUsageUnverifiable for the gate that
// consults the settlement.
const (
	// unresolvedCallReconciliationKind names the annotation this channel
	// writes. It is a name of its own: the embedding HTTP error channel
	// keeps its own kind, and the two are never interchangeable.
	unresolvedCallReconciliationKind = "unresolved_call_full_reserve_v1"

	// The ledger statuses this channel can settle.
	//
	// They are the shapes in which the window froze because nobody can say
	// what the call did. "response_received" is not one of them: that call
	// is settled already.
	unresolvedStatusOutcomeUnknown               = "outcome_unknown"
	unresolvedStatusUsageUnknown                 = "usage_unknown"
	unresolvedStatusInFlight                     = "in_flight"
	unresolvedStatusCancelReconciliationRequired = "cancel_reconciliation_required"
	unresolvedStatusLimitViolation               = "limit_violation"
)

// UnresolvedCallEvidence is a local operator attestation tied to one exact
// ledger snapshot and one exact request.
//
// EvidenceSHA256 identifies the reviewed attestation document (the operator's
// "this batch was not delivered" / "this batch was delivered but the usage is
// unknown" proof). It does not claim the provider supplied billing usage:
// the provider's usage stays unknown, and the charge is the reserved upper
// bound.
type UnresolvedCallEvidence struct {
	LedgerSHA256          string `json:"ledger_sha256"`
	RequestNumber         int    `json:"request_number"`
	OriginalStatus        string `json:"original_status"`
	OriginalBlockedReason string `json:"original_blocked_reason"`
	EvidenceSHA256        string `json:"evidence_sha256"`
	Operator              string `json:"operator"`
	Reason                string `json:"reason"`
}

// UnresolvedCallReconciliation is the annotation one settled row carries.
//
// It says WHAT was settled and HOW it was charged. It never says what the
// provider consumed: ProviderTokenUsage is "unknown" because nobody can
// prove it.
type UnresolvedCallReconciliation struct {
	Kind                  string                 `json:"kind"`
	Evidence              UnresolvedCallEvidence `json:"evidence"`
	OriginalRequestSHA256 string                 `json:"original_request_sha256"`
	ChargeBasis           string                 `json:"charge_basis"`
	ProviderTokenUsage    string                 `json:"provider_token_usage"`
	ChargedNanoCNY        int64                  `json:"charged_nano_cny"`
	ChargedNanoUSD        int64                  `json:"charged_nano_usd"`
	ReconciledAt          time.Time              `json:"reconciled_at"`
}

// ReconcileUnresolvedCallInput is one operator's explicit request to settle
// one unresolved call.
type ReconcileUnresolvedCallInput struct {
	// ExpectedLedgerSHA256 is the SHA256 of the COMPLETE current ledger
	// bytes. It is the snapshot binding: the settlement refuses to touch a
	// file whose content is not the one the operator reviewed.
	ExpectedLedgerSHA256 string
	// RequestNumber is the exact request the operator settled.
	RequestNumber int
	// ExpectedStatus is the ledger status the operator expects to find.
	ExpectedStatus string
	// ExpectedBlockedReason is the ledger's block reason the operator
	// expects to find. It is empty for the crash shape ("in_flight"),
	// where the process died before the window was frozen.
	ExpectedBlockedReason string
	// ExpectedReason is the operator's reason, which must be the one in the
	// evidence.
	ExpectedReason string
	// ConfirmFullReserveCharge is the explicit acknowledgement that the
	// full reserved upper bound is charged without a provider usage claim.
	ConfirmFullReserveCharge bool
	// Evidence is the operator's attestation.
	Evidence UnresolvedCallEvidence
}

// ReconcileUnresolvedCall settles ONE call whose outcome could not be
// confirmed, and reopens the window it froze.
//
// It is an explicit local operator operation. No provider/client/HTTP path
// calls it: the budget package imports no network client at all, so neither
// this operation nor any other in it can send a provider request.
//
// It preserves the failed request exactly (including its unknown token
// counters), never refunds either reserved or actual cost, and never loosens
// a budget limit: the settled call keeps its reservation and its place in the
// call count, so a window whose cumulative reservation is already past a
// ceiling is still refused after the settlement.
//
// See ProfileSupportsUnresolvedCallReconciliation for which profiles may use
// it.
func (b *Budget) ReconcileUnresolvedCall(ctx context.Context, input ReconcileUnresolvedCallInput) error {
	if b == nil || !ProfileSupportsUnresolvedCallReconciliation(b.profile) || !filepath.IsAbs(b.ledgerPath) ||
		!input.ConfirmFullReserveCharge || !validSHA256(input.ExpectedLedgerSHA256) ||
		input.RequestNumber < 1 || !settleableUnresolvedStatus(input.ExpectedStatus) ||
		input.ExpectedStatus != input.Evidence.OriginalStatus ||
		input.ExpectedBlockedReason != input.Evidence.OriginalBlockedReason ||
		input.ExpectedReason != input.Evidence.Reason ||
		input.Evidence.LedgerSHA256 != input.ExpectedLedgerSHA256 ||
		input.Evidence.RequestNumber != input.RequestNumber ||
		!validUnresolvedCallEvidence(input.Evidence) {
		return ErrUnavailable
	}
	lockPath := b.ledgerPath + ".lock"
	lock, err := AcquireLock(ctx, lockPath)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = ReleaseLock(lock, lockPath) }()
	data, err := os.ReadFile(b.ledgerPath)
	if err != nil || digestSHA256(data) != input.ExpectedLedgerSHA256 {
		return ErrUnavailable
	}
	ledger, err := decodeLedger(data)
	if err != nil {
		return ErrUnavailable
	}
	// The settled call is the last row: a call that froze the window is the
	// last one the window ever wrote, because the reservation refuses every
	// later call while a row is unresolved.
	if input.RequestNumber != len(ledger.Requests) {
		return ErrUnavailable
	}
	request := &ledger.Requests[input.RequestNumber-1]
	if request.Number != input.RequestNumber || request.Status != input.ExpectedStatus ||
		request.Reconciliation != nil || request.UnresolvedReconciliation != nil {
		return ErrUnavailable
	}
	// The provider's usage was never confirmed. A row whose usage WAS
	// reported is not an unconfirmable outcome: its accounting is final, and
	// settling it would be a different decision than this one.
	if request.UsageStatus == UsageStatusReported {
		return ErrUnavailable
	}
	// The freeze must be attributable to this very row.
	switch {
	case ledger.Blocked:
		if input.ExpectedBlockedReason == "" || ledger.BlockedReason != input.ExpectedBlockedReason {
			return ErrUnavailable
		}
	case input.ExpectedStatus == unresolvedStatusInFlight:
		// The crash shape: the process died between the reservation and the
		// completion, so the window was never frozen at all. The ledger
		// carries no block reason, and the operator must say so.
		if ledger.BlockedReason != "" || input.ExpectedBlockedReason != "" {
			return ErrUnavailable
		}
	default:
		return ErrUnavailable
	}
	// Exactly one row may be settled at a time: settling the wrong one of
	// two unresolved calls is the failure this refuses.
	for index, other := range ledger.Requests {
		if index != input.RequestNumber-1 && requestUnresolved(other) {
			return ErrBlocked
		}
	}
	proof := &UnresolvedCallReconciliation{
		Kind: unresolvedCallReconciliationKind, Evidence: input.Evidence,
		OriginalRequestSHA256: requestSHA256(*request), ChargeBasis: fullReserveChargeBasis,
		ProviderTokenUsage: unknownProviderUsage, ChargedNanoCNY: request.ReservedNanoCNY,
		ChargedNanoUSD: request.ReservedNanoUSD, ReconciledAt: time.Now().UTC(),
	}
	// The annotation is APPENDED. The row's status, counters, money and
	// usage status are not touched, and no reservation is given back.
	request.UnresolvedReconciliation = proof
	ledger.Blocked = false
	ledger.BlockedReason = ""
	// The settled ledger has to be a valid ledger, and nothing may be left
	// unresolved. Both are checked BEFORE the write, so a refusal leaves the
	// file byte for byte as it was.
	if !validLedger(ledger, b.profile) || hasUnresolved(ledger) {
		return ErrBlocked
	}
	if err := writeLedger(b.ledgerPath, ledger); err != nil {
		return ErrUnavailable
	}
	return nil
}

// settleableUnresolvedStatus reports whether one ledger status is an
// unconfirmable outcome this channel may settle.
func settleableUnresolvedStatus(status string) bool {
	switch status {
	case unresolvedStatusOutcomeUnknown, unresolvedStatusUsageUnknown, unresolvedStatusInFlight,
		unresolvedStatusCancelReconciliationRequired, unresolvedStatusLimitViolation:
		return true
	default:
		return false
	}
}

// validUnresolvedCallEvidence accepts an attestation that names one exact
// snapshot and one exact request.
func validUnresolvedCallEvidence(e UnresolvedCallEvidence) bool {
	if !validSHA256(e.LedgerSHA256) || e.RequestNumber < 1 || !settleableUnresolvedStatus(e.OriginalStatus) ||
		!validSHA256(e.EvidenceSHA256) || !validText(e.Operator, 128) || !validText(e.Reason, 2048) {
		return false
	}
	// The block reason is the ledger's own, and it is empty only for the
	// crash shape: a window that never froze has no reason to quote.
	if e.OriginalStatus == unresolvedStatusInFlight {
		return e.OriginalBlockedReason == ""
	}
	return e.OriginalBlockedReason == e.OriginalStatus
}

// validUnresolvedCallReconciliation reports whether one row's annotation is
// a complete and honest settlement of that row.
//
// It is checked on every read, so an annotation that was tampered with (or
// that does not match the row it sits on) leaves the row UNRESOLVED: the
// window stays frozen rather than opening on a proof nobody can check.
func validUnresolvedCallReconciliation(request Request) bool {
	r := request.UnresolvedReconciliation
	if r == nil || !settleableUnresolvedStatus(request.Status) || request.UsageStatus == UsageStatusReported {
		return false
	}
	if r.Kind != unresolvedCallReconciliationKind || !validUnresolvedCallEvidence(r.Evidence) ||
		r.Evidence.RequestNumber != request.Number || r.Evidence.OriginalStatus != request.Status {
		return false
	}
	if !validSHA256(r.OriginalRequestSHA256) || r.OriginalRequestSHA256 != requestSHA256(request) {
		return false
	}
	// The charge is the FULL reserved upper bound and the provider's usage
	// stays unknown. A discounted charge is not a settlement.
	if r.ChargeBasis != fullReserveChargeBasis || r.ProviderTokenUsage != unknownProviderUsage ||
		r.ChargedNanoCNY != request.ReservedNanoCNY || r.ChargedNanoUSD != request.ReservedNanoUSD {
		return false
	}
	return !r.ReconciledAt.IsZero() && !r.ReconciledAt.Before(request.CreatedAt)
}

// ProfileSupportsUnresolvedCallReconciliation reports whether an operator may
// settle an unconfirmable call on one profile's ledger.
//
// It answers a question about the PROFILE, not about a call: a profile whose
// calls can end in an unconfirmable outcome must be able to recover from one
// of them happening, otherwise the authorized window is retired by the FIRST
// transport failure with no way back (ticket #675).
//
// The settlement it authorises is a LOCAL LEDGER OPERATION with conservative
// invariants: the failed request is preserved exactly, the full reserved
// upper bound is charged, the provider's usage stays "unknown", the failed
// call keeps its reservation and its place in the call count, and nothing is
// refunded. It never calls a provider, never switches a profile, and never
// loosens a budget limit.
//
// It is deliberately NOT a general "unblock anything" switch: it settles the
// calls whose OUTCOME nobody can confirm, and refuses a row whose usage the
// provider did report -- that row's accounting is final, and reopening a
// confirmed outcome is a different decision than this one.
func ProfileSupportsUnresolvedCallReconciliation(profile string) bool {
	return profileConfigOf(profile).unresolvedCallReconcilable
}
