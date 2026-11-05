package acceptancebudget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func operatorFixture(t *testing.T, status string) (*Budget, ReconcileEmbeddingHTTPErrorInput) {
	t.Helper()
	b := New(CostOnlyProfile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
	prior, err := b.Reserve(context.Background(), DeepSeekModel, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := prior.Complete("response_received", 50, 50, "stop", false); err != nil {
		t.Fatal(err)
	}
	r, err := b.Reserve(context.Background(), QwenEmbedding, 256, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Unknown(status); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("fixture Unknown: %v", err)
	}
	return b, operatorInput(t, b)
}

func operatorInput(t *testing.T, b *Budget) ReconcileEmbeddingHTTPErrorInput {
	t.Helper()
	data, err := os.ReadFile(b.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	var ledger Ledger
	if err := json.Unmarshal(data, &ledger); err != nil {
		t.Fatal(err)
	}
	number := len(ledger.Requests)
	e := EmbeddingHTTPErrorEvidence{
		LedgerSHA256: digestSHA256(data), RequestNumber: number, OriginalStatus: "http_error", OriginalBlockedReason: "http_error",
		HTTPStatus: 403, ResponseEvidenceSHA256: digestSHA256([]byte("synthetic request-specific HTTP403 artifact; no network")),
		Operator: "synthetic-local-operator", Reason: "Reviewed synthetic HTTP403 response; accept full reserve with provider usage unknown",
	}
	return ReconcileEmbeddingHTTPErrorInput{ExpectedLedgerSHA256: e.LedgerSHA256, RequestNumber: number,
		ExpectedStatus: "http_error", ExpectedBlockedReason: "http_error", ExpectedReason: e.Reason, ConfirmFullReserveCharge: true, Evidence: e}
}

func mustLedgerBytes(t *testing.T, b *Budget) []byte {
	t.Helper()
	data, err := os.ReadFile(b.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertLedgerUnchanged(t *testing.T, b *Budget, before []byte) {
	t.Helper()
	if !bytes.Equal(before, mustLedgerBytes(t, b)) {
		t.Fatal("refused operation changed ledger bytes")
	}
}

func TestOperatorReconcileEmbeddingHTTPErrorPreservesHistoryAndReopens(t *testing.T) {
	b, input := operatorFixture(t, "http_error")
	before, err := readLedger(b.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	originalBytes := mustLedgerBytes(t, b)
	if _, err := b.Reserve(context.Background(), QwenEmbedding, 1, 0); !errors.Is(err, ErrBlocked) {
		t.Fatalf("old blocked ledger permitted call: %v", err)
	}
	assertLedgerUnchanged(t, b, originalBytes)
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	after, err := readLedger(b.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Blocked || hasUnresolved(after) || !validLedger(after, CostOnlyProfile) {
		t.Fatal("complete reconciliation did not settle block")
	}
	if before.ActualNanoCNY != after.ActualNanoCNY || before.ReservedNanoCNY != after.ReservedNanoCNY ||
		before.ActualNanoUSD != after.ActualNanoUSD || before.ReservedNanoUSD != after.ReservedNanoUSD {
		t.Fatal("reconciliation changed money")
	}
	if !reflect.DeepEqual(before.Requests[:1], after.Requests[:1]) {
		t.Fatal("earlier history changed")
	}
	original := before.Requests[1]
	settled := after.Requests[1]
	proof := settled.Reconciliation
	settled.Reconciliation = nil
	if !reflect.DeepEqual(original, settled) || proof == nil || proof.Evidence.LedgerSHA256 != input.ExpectedLedgerSHA256 ||
		proof.ProviderTokenUsage != "unknown" || proof.ChargedNanoCNY != original.ReservedNanoCNY || settled.Status != "http_error" {
		t.Fatal("original failure/unknown usage/full charge not preserved")
	}
	// Restart/readback uses no in-memory operator state.
	reopened := New(CostOnlyProfile, b.ledgerPath)
	r, err := reopened.Reserve(context.Background(), QwenEmbedding, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CancelNoCall(); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ReconcileEmbeddingHTTPError(context.Background(), input); err == nil {
		t.Fatal("duplicate old-SHA reconciliation accepted")
	}
	input = operatorInput(t, reopened)
	if err := reopened.ReconcileEmbeddingHTTPError(context.Background(), input); err == nil {
		t.Fatal("duplicate with refreshed SHA accepted")
	}
}

func TestOperatorReconcileRejectsMismatchedEvidenceWithoutWrites(t *testing.T) {
	cases := map[string]func(*ReconcileEmbeddingHTTPErrorInput){
		"wrong whole-ledger SHA": func(i *ReconcileEmbeddingHTTPErrorInput) {
			i.ExpectedLedgerSHA256 = strings.Repeat("0", 64)
			i.Evidence.LedgerSHA256 = i.ExpectedLedgerSHA256
		},
		"wrong request":               func(i *ReconcileEmbeddingHTTPErrorInput) { i.RequestNumber = 1; i.Evidence.RequestNumber = 1 },
		"cross-request evidence":      func(i *ReconcileEmbeddingHTTPErrorInput) { i.Evidence.RequestNumber = 1 },
		"wrong evidence snapshot":     func(i *ReconcileEmbeddingHTTPErrorInput) { i.Evidence.LedgerSHA256 = strings.Repeat("1", 64) },
		"wrong status":                func(i *ReconcileEmbeddingHTTPErrorInput) { i.ExpectedStatus = "response_received" },
		"wrong evidence status":       func(i *ReconcileEmbeddingHTTPErrorInput) { i.Evidence.OriginalStatus = "outcome_unknown" },
		"wrong block reason":          func(i *ReconcileEmbeddingHTTPErrorInput) { i.ExpectedBlockedReason = "usage_unknown" },
		"wrong evidence block reason": func(i *ReconcileEmbeddingHTTPErrorInput) { i.Evidence.OriginalBlockedReason = "usage_unknown" },
		"wrong operator reason":       func(i *ReconcileEmbeddingHTTPErrorInput) { i.ExpectedReason = "different reason" },
		"missing operator":            func(i *ReconcileEmbeddingHTTPErrorInput) { i.Evidence.Operator = "" },
		"missing reason":              func(i *ReconcileEmbeddingHTTPErrorInput) { i.ExpectedReason = ""; i.Evidence.Reason = "" },
		"missing response digest":     func(i *ReconcileEmbeddingHTTPErrorInput) { i.Evidence.ResponseEvidenceSHA256 = "" },
		"success response":            func(i *ReconcileEmbeddingHTTPErrorInput) { i.Evidence.HTTPStatus = 200 },
		"unknown response":            func(i *ReconcileEmbeddingHTTPErrorInput) { i.Evidence.HTTPStatus = 0 },
		"no full charge confirmation": func(i *ReconcileEmbeddingHTTPErrorInput) { i.ConfirmFullReserveCharge = false },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b, input := operatorFixture(t, "http_error")
			before := mustLedgerBytes(t, b)
			mutate(&input)
			if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err == nil {
				t.Fatal("invalid evidence accepted")
			}
			assertLedgerUnchanged(t, b, before)
		})
	}
}

func TestOperatorReconcileNeverResolvesUnknownOutcomes(t *testing.T) {
	for _, status := range []string{"in_flight", "outcome_unknown", "usage_unknown", "limit_violation", "cancel_reconciliation_required", "incomplete", "response_received", "unknown_status"} {
		t.Run(status, func(t *testing.T) {
			b, input := operatorFixture(t, status)
			before := mustLedgerBytes(t, b)
			if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err == nil {
				t.Fatal("unsupported outcome reconciled")
			}
			assertLedgerUnchanged(t, b, before)
		})
	}
	for _, profile := range []string{Profile, "unknown-profile"} {
		t.Run(profile, func(t *testing.T) {
			b, input := operatorFixture(t, "http_error")
			before := mustLedgerBytes(t, b)
			if err := New(profile, b.ledgerPath).ReconcileEmbeddingHTTPError(context.Background(), input); err == nil {
				t.Fatal("unsupported profile reconciled")
			}
			assertLedgerUnchanged(t, b, before)
		})
	}
}

func TestOperatorReconcileRejectsInconsistentLedgerWithoutRepairingIt(t *testing.T) {
	cases := map[string]func(*Ledger){
		"wrong total":                    func(l *Ledger) { l.ActualNanoCNY-- },
		"wrong per-request reserve":      func(l *Ledger) { l.Requests[1].ReservedNanoCNY--; l.ReservedNanoCNY-- },
		"partial charge":                 func(l *Ledger) { l.Requests[1].ActualNanoCNY--; l.ActualNanoCNY-- },
		"token cost mismatch in history": func(l *Ledger) { l.Requests[0].InputTokens-- },
		"zero token upper bound":         func(l *Ledger) { l.Requests[1].InputUpperBound = 0 },
		"other block reason":             func(l *Ledger) { l.BlockedReason = "manual stop" },
		"not blocked":                    func(l *Ledger) { l.Blocked = false },
		"wrong model":                    func(l *Ledger) { l.Requests[1].Model = DeepSeekModel },
		"changed budget":                 func(l *Ledger) { l.MaxReserveNanoCNY = 20_000_000_001 },
		"other unresolved request":       func(l *Ledger) { l.Requests[0].Status = "outcome_unknown" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b, _ := operatorFixture(t, "http_error")
			ledger, err := readLedger(b.ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&ledger)
			if err := writeLedger(b.ledgerPath, ledger); err != nil {
				t.Fatal(err)
			}
			input := operatorInput(t, b)
			before := mustLedgerBytes(t, b)
			if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err == nil {
				t.Fatal("inconsistent ledger accepted")
			}
			assertLedgerUnchanged(t, b, before)
		})
	}
	for _, extra := range []string{`,"unsupported_history":true}`, `,"blocked":true}`} {
		t.Run(extra, func(t *testing.T) {
			b, _ := operatorFixture(t, "http_error")
			data := bytes.TrimSpace(mustLedgerBytes(t, b))
			data = append(data[:len(data)-1], []byte(extra)...)
			if err := os.WriteFile(b.ledgerPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			input := operatorInput(t, b)
			before := mustLedgerBytes(t, b)
			if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err == nil {
				t.Fatal("unknown/duplicate JSON history accepted")
			}
			assertLedgerUnchanged(t, b, before)
		})
	}
}

func TestOperatorReconcileReadbackRejectsIncompleteOrCrossRequestProof(t *testing.T) {
	cases := map[string]func(*Request){
		"deleted proof":                func(r *Request) { r.Reconciliation = nil },
		"wrong request":                func(r *Request) { r.Reconciliation.Evidence.RequestNumber = 1 },
		"wrong request digest":         func(r *Request) { r.Reconciliation.OriginalRequestSHA256 = strings.Repeat("0", 64) },
		"missing ledger digest":        func(r *Request) { r.Reconciliation.Evidence.LedgerSHA256 = "" },
		"missing response digest":      func(r *Request) { r.Reconciliation.Evidence.ResponseEvidenceSHA256 = "" },
		"missing reason":               func(r *Request) { r.Reconciliation.Evidence.Reason = "" },
		"missing operator":             func(r *Request) { r.Reconciliation.Evidence.Operator = "" },
		"unknown kind":                 func(r *Request) { r.Reconciliation.Kind = "manual-unblock" },
		"provider usage falsely known": func(r *Request) { r.Reconciliation.ProviderTokenUsage = "known" },
		"discount charge":              func(r *Request) { r.Reconciliation.ChargedNanoCNY-- },
		"no decision time":             func(r *Request) { r.Reconciliation.ReconciledAt = time.Time{} },
		"status rewritten as success":  func(r *Request) { r.Status = "response_received" },
		"unknown tokens changed":       func(r *Request) { r.InputTokens-- },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b, input := operatorFixture(t, "http_error")
			if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			ledger, err := readLedger(b.ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&ledger.Requests[1])
			if err := writeLedger(b.ledgerPath, ledger); err != nil {
				t.Fatal(err)
			}
			before := mustLedgerBytes(t, b)
			if _, err := New(CostOnlyProfile, b.ledgerPath).Reserve(context.Background(), QwenEmbedding, 1, 0); err == nil {
				t.Fatal("incomplete persisted proof unlocked budget")
			}
			assertLedgerUnchanged(t, b, before)
		})
	}
}

func TestOperatorReconcileLocksSnapshotAndOnlyOneConcurrentWriterSucceeds(t *testing.T) {
	b, input := operatorFixture(t, "http_error")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- New(CostOnlyProfile, b.ledgerPath).ReconcileEmbeddingHTTPError(context.Background(), input)
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent successful writers=%d", success)
	}
	ledger, err := readLedger(b.ledgerPath)
	if err != nil || len(ledger.Requests) != 2 {
		t.Fatalf("bad concurrent result: %v", err)
	}
	before := mustLedgerBytes(t, b)
	input = operatorInput(t, b)
	lock, err := AcquireLock(context.Background(), b.ledgerPath+".lock")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := b.ReconcileEmbeddingHTTPError(ctx, input); err == nil {
		t.Fatal("operator bypassed active reservation lock")
	}
	assertLedgerUnchanged(t, b, before)
	if err := ReleaseLock(lock, b.ledgerPath+".lock"); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorReconcileStillEnforcesStrictTwentyYuanCap(t *testing.T) {
	b, input := operatorFixture(t, "http_error")
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	for count := 0; count < 200; count++ {
		r, err := b.Reserve(context.Background(), DeepSeekModel, InputMax, OutputMax)
		if errors.Is(err, ErrCallsSpent) {
			ledger, err := readLedger(b.ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			if ledger.ReservedNanoCNY >= 20_000_000_000 || ledger.ActualNanoCNY != ledger.ReservedNanoCNY-750_000 || ledger.Requests[1].Status != "http_error" {
				t.Fatal("money/history changed or cap exceeded")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Complete("response_received", InputMax, OutputMax, "stop", false); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("20-yuan cap not reached")
}

func TestOperatorReconcileRejectsCopiedProofInLegacyProfile(t *testing.T) {
	b, input := operatorFixture(t, "http_error")
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	ledger, err := readLedger(b.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	ledger.Profile = Profile
	ledger.MaxCalls, ledger.MaxReserveNanoCNY, _ = ProfileLimits(Profile)
	if err := writeLedger(b.ledgerPath, ledger); err != nil {
		t.Fatal(err)
	}
	before := mustLedgerBytes(t, b)
	if validLedger(ledger, Profile) || !hasUnresolved(ledger) {
		t.Fatal("legacy profile accepted cost-only reconciliation")
	}
	if _, err := readLedger(b.ledgerPath); err == nil {
		t.Fatal("reader accepted proof copied to wrong profile")
	}
	if _, err := New(CostOnlyProfile, b.ledgerPath).Reserve(context.Background(), QwenEmbedding, 1, 0); err == nil {
		t.Fatal("profile migration laundered copied proof")
	}
	assertLedgerUnchanged(t, b, before)
}
