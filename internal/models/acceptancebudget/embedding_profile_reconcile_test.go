package acceptancebudget

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The controlled embedding profile must be able to RECOVER from a provider
// HTTP error.
//
// Before #671, one 4xx froze the window for good: the only operator
// reconciliation was hard-wired to the cost-only profile, so the authorized
// budget (four calls, eight thousand tokens) was retired by the FIRST
// failure, with no way back. These tests pin the recovery and the
// conservative invariants it has to keep.

// embeddingWindowFixture builds a controlled embedding window whose LAST call
// ended in a provider HTTP error, together with the operator evidence for
// that exact ledger snapshot.
func embeddingWindowFixture(t *testing.T, httpStatus int) (*Budget, ReconcileEmbeddingHTTPErrorInput) {
	t.Helper()
	b := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
	// One completed call first, so the ledger has history besides the failure.
	done, err := b.Reserve(context.Background(), QwenEmbedding, 128, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := done.Complete("response_received", 100, 0, "", false); err != nil {
		t.Fatal(err)
	}
	// The failed call: the provider answered with an HTTP error, so its usage
	// is unknown and the window freezes.
	failed, err := b.Reserve(context.Background(), QwenEmbedding, 256, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.Unknown("http_error"); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("fixture Unknown: %v", err)
	}
	return b, embeddingWindowEvidence(t, b, httpStatus)
}

// embeddingWindowEvidence builds the operator evidence for one exact ledger
// snapshot, the way the CLI's evidence file does.
func embeddingWindowEvidence(t *testing.T, b *Budget, httpStatus int) ReconcileEmbeddingHTTPErrorInput {
	t.Helper()
	data := mustLedgerBytes(t, b)
	var ledger Ledger
	if err := json.Unmarshal(data, &ledger); err != nil {
		t.Fatal(err)
	}
	number := len(ledger.Requests)
	e := EmbeddingHTTPErrorEvidence{
		LedgerSHA256: digestSHA256(data), RequestNumber: number,
		OriginalStatus: "http_error", OriginalBlockedReason: "http_error",
		HTTPStatus:             httpStatus,
		ResponseEvidenceSHA256: digestSHA256([]byte("synthetic request-specific HTTP error artifact; no network")),
		Operator:               "synthetic-local-operator",
		Reason:                 "Reviewed synthetic HTTP error response; accept the full reserve with provider usage unknown",
	}
	return ReconcileEmbeddingHTTPErrorInput{
		ExpectedLedgerSHA256: e.LedgerSHA256, RequestNumber: number,
		ExpectedStatus: "http_error", ExpectedBlockedReason: "http_error",
		ExpectedReason: e.Reason, ConfirmFullReserveCharge: true, Evidence: e,
	}
}

func TestTheControlledEmbeddingWindowRecoversFromAProviderHTTPError(t *testing.T) {
	for _, httpStatus := range []int{400, 403, 429, 500} {
		t.Run(strconv.Itoa(httpStatus), func(t *testing.T) {
			b, input := embeddingWindowFixture(t, httpStatus)

			// Before the recovery the window is frozen: every later
			// reservation is refused.
			if _, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0); !errors.Is(err, ErrBlocked) {
				t.Fatalf("the frozen window reserved anyway: %v", err)
			}
			before, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
			if err != nil {
				t.Fatal(err)
			}
			failedBefore := before.Requests[input.RequestNumber-1]

			if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
				t.Fatalf("the recovery was refused: %v", err)
			}

			after, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
			if err != nil {
				t.Fatal(err)
			}
			if after.Blocked || after.BlockedReason != "" {
				t.Fatalf("the window is still frozen after the recovery: %q", after.BlockedReason)
			}
			// The failed request is preserved exactly: status, counters and
			// money are the ones the provider failure produced.
			failedAfter := after.Requests[input.RequestNumber-1]
			if failedAfter.Status != failedBefore.Status ||
				failedAfter.InputTokens != failedBefore.InputTokens ||
				failedAfter.InputUpperBound != failedBefore.InputUpperBound ||
				failedAfter.OutputTokens != failedBefore.OutputTokens ||
				failedAfter.UsageStatus != failedBefore.UsageStatus ||
				failedAfter.ReservedNanoCNY != failedBefore.ReservedNanoCNY ||
				failedAfter.ActualNanoCNY != failedBefore.ActualNanoCNY {
				t.Fatalf("the recovery rewrote the failed request:\nbefore %+v\nafter  %+v", failedBefore, failedAfter)
			}
			// The provider's usage stays unknown and the charge is the full
			// reserved upper bound -- the conservative reading.
			proof := failedAfter.Reconciliation
			if proof == nil {
				t.Fatal("the recovery left no record of itself")
			}
			if proof.Kind != embeddingHTTPErrorReconciliationKind ||
				proof.ProviderTokenUsage != unknownProviderUsage ||
				proof.ChargeBasis != fullReserveChargeBasis ||
				proof.ChargedNanoCNY != failedBefore.ReservedNanoCNY {
				t.Fatalf("the recovery proof does not carry the conservative charge: %+v", proof)
			}
			if proof.Evidence.HTTPStatus != httpStatus || proof.Evidence.Operator != input.Evidence.Operator ||
				proof.Evidence.RequestNumber != input.RequestNumber {
				t.Fatalf("the recovery proof does not carry the operator evidence: %+v", proof.Evidence)
			}
			// Nothing is refunded: the failed call and its money stay spent.
			if len(after.Requests) != len(before.Requests) ||
				after.ReservedNanoCNY != before.ReservedNanoCNY ||
				after.ActualNanoCNY != before.ActualNanoCNY ||
				after.ReservedTokens != before.ReservedTokens {
				t.Fatalf("the recovery refunded something: before %+v after %+v", before, after)
			}
			// And the window can be used again.
			next, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0)
			if err != nil {
				t.Fatalf("the recovered window still refuses reservations: %v", err)
			}
			if err := next.CancelNoCall(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTheRecoveredEmbeddingWindowGivesBackNoCall(t *testing.T) {
	// The embedding profile allows four calls. The failed call counts, so the
	// recovery must not hand that call back: the window is exhausted.
	b := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
	for i := 0; i < 3; i++ {
		done, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := done.Complete("response_received", 50, 0, "", false); err != nil {
			t.Fatal(err)
		}
	}
	failed, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.Unknown("http_error"); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("fixture Unknown: %v", err)
	}
	input := embeddingWindowEvidence(t, b, 403)
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
		t.Fatalf("the recovery was refused: %v", err)
	}
	if _, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("the recovered window reserved past its call count: %v", err)
	}
}

func TestTheRecoveredEmbeddingWindowStillHonoursTheCumulativeTokenCeiling(t *testing.T) {
	b, input := embeddingWindowFixture(t, 403)
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
		t.Fatalf("the recovery was refused: %v", err)
	}
	// The window used 128 + 256 = 384 tokens of the 8000 the authorization
	// names. A reservation that would push the cumulative reservation past
	// the ceiling is still refused after the recovery.
	if _, err := b.Reserve(context.Background(), QwenEmbedding, 8000, 0); !errors.Is(err, ErrTokenBudgetExhausted) {
		t.Fatalf("the recovered window reserved past the cumulative token ceiling: %v", err)
	}
}

func TestARepeatedRecoveryOfTheSameRequestIsRefused(t *testing.T) {
	b, input := embeddingWindowFixture(t, 403)
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
		t.Fatalf("the recovery was refused: %v", err)
	}
	after := mustLedgerBytes(t, b)

	// The same evidence again: that request already carries a recovery.
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err == nil {
		t.Fatal("the same request was recovered twice")
	}
	assertLedgerUnchanged(t, b, after)

	// And a REFRESHED ledger digest does not open the door either.
	refreshed := embeddingWindowEvidence(t, b, 403)
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), refreshed); err == nil {
		t.Fatal("a refreshed digest let the same request be recovered twice")
	}
	assertLedgerUnchanged(t, b, after)
}

func TestProfilesWithoutTheCapabilityStillRefuseTheRecovery(t *testing.T) {
	for _, profile := range []string{Profile, QwenProfile, QwenProfileV2} {
		if ProfileSupportsEmbeddingHTTPErrorReconciliation(profile) {
			t.Fatalf("profile %q must not claim the embedding recovery capability", profile)
		}
	}
	// The legacy deepseek profile CAN make embedding calls, so it can hold a
	// failed embedding call -- and it must still refuse the recovery.
	b := New(Profile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
	failed, err := b.Reserve(context.Background(), QwenEmbedding, 128, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.Unknown("http_error"); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("fixture Unknown: %v", err)
	}
	input := embeddingWindowEvidence(t, b, 403)
	before := mustLedgerBytes(t, b)
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err == nil {
		t.Fatal("a profile without the capability recovered an embedding HTTP error")
	}
	assertLedgerUnchanged(t, b, before)
}

func TestTheBudgetPackageNeverTalksToAProvider(t *testing.T) {
	// The recovery is a LOCAL ledger operation. The strongest statement
	// available is structural: this package imports no network client at
	// all, so neither the recovery nor any other operation in it can send a
	// provider request -- which is what makes "the recovery triggers no
	// provider request" a property rather than a promise.
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range packages {
		for imported := range pkg.Imports {
			if imported == "net" || imported == "net/http" || strings.HasPrefix(imported, "net/") {
				t.Fatalf("the budget package imports %q; it must stay a local ledger operation", imported)
			}
		}
	}
}

func TestTheEmbeddingProfileCapabilityIsStatedInTheProfileTable(t *testing.T) {
	// The capability lives in the profile table, not in a hard-wired profile
	// name: that is what lets another priced embedding profile opt in
	// without touching the reconciliation code.
	if !ProfileSupportsEmbeddingHTTPErrorReconciliation(QwenEmbeddingProfile) {
		t.Fatal("the controlled embedding profile lost the recovery capability")
	}
	if !ProfileSupportsEmbeddingHTTPErrorReconciliation(CostOnlyProfile) {
		t.Fatal("the cost-only profile lost the recovery capability it always had")
	}
	if ProfileSupportsEmbeddingHTTPErrorReconciliation("a-profile-nobody-declared") {
		t.Fatal("an undeclared profile claims the recovery capability")
	}
}
