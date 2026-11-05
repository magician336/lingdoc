package acceptancebudget

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

// The evidence cases retain a valid earlier reconciliation plus a new failed
// request, so accepting an alias would really allow Reconcile to rewrite history.
func aliasLedgerFixture(t *testing.T, nestedEvidence bool) (*Budget, ReconcileEmbeddingHTTPErrorInput) {
	t.Helper()
	b, input := operatorFixture(t, "http_error")
	if nestedEvidence {
		if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		r, err := b.Reserve(context.Background(), QwenEmbedding, 256, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Unknown("http_error"); !errors.Is(err, ErrCallsSpent) {
			t.Fatal(err)
		}
		input = operatorInput(t, b)
	}
	return b, input
}

func TestOperatorReconcileRejectsAmbiguousJSONKeysWithoutRewriting(t *testing.T) {
	cases := []struct {
		name, canonical, alias, value string
		evidence                      bool
	}{
		{"top-ascii", "blocked", "Blocked", "true", false},
		{"top-kelvin", "blocked", "blocKed", "true", false},
		{"top-escaped-ascii", "blocked", `\u0042locked`, "true", false},
		{"request-ascii", "status", "Status", `"http_error"`, false},
		{"request-kelvin", "input_tokens", "input_toKens", "256", false},
		{"evidence-ascii", "operator", "Operator", `"synthetic-local-operator"`, true},
		{"evidence-long-s", "original_status", "original_ſtatus", `"http_error"`, true},
	}
	for _, tc := range cases {
		for _, order := range []string{"alias-before", "alias-after", "alias-only"} {
			t.Run(tc.name+"/"+order, func(t *testing.T) {
				b, _ := aliasLedgerFixture(t, tc.evidence)
				canonical := []byte(`"` + tc.canonical + `": ` + tc.value)
				alias := []byte(`"` + tc.alias + `": ` + tc.value)
				data := mustLedgerBytes(t, b)
				if bytes.Count(data, canonical) != 1 {
					t.Fatalf("fixture must locate one exact key: %s", tc.canonical)
				}
				replacement := alias
				if order == "alias-before" {
					replacement = append(append(append([]byte{}, alias...), ','), canonical...)
				}
				if order == "alias-after" {
					replacement = append(append(append([]byte{}, canonical...), ','), alias...)
				}
				data = bytes.Replace(data, canonical, replacement, 1)
				if err := os.WriteFile(b.ledgerPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
				input := operatorInput(t, b) // binds the exact modified bytes, so SHA alone cannot hide the defect.
				if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err == nil {
					t.Error("ambiguous/noncanonical key allowed reconciliation")
				}
				assertLedgerUnchanged(t, b, data)
				if _, err := readLedger(b.ledgerPath); err == nil {
					t.Error("reader accepted noncanonical schema key")
				}
				if _, err := b.Reserve(context.Background(), QwenEmbedding, 1, 0); err == nil {
					t.Error("ambiguous history allowed a new reservation")
				}
				assertLedgerUnchanged(t, b, data)
			})
		}
	}
}

func TestOperatorReconcileCanonicalKeysAllowUnicodeEvidenceValues(t *testing.T) {
	b, input := operatorFixture(t, "http_error")
	input.Evidence.Operator = "合成操作者Kſ"
	input.Evidence.Reason = "核对合成403响应；按全部预留保守占费，供应商usage仍未知。"
	input.ExpectedReason = input.Evidence.Reason
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	ledger, err := readLedger(b.ledgerPath)
	if err != nil || ledger.Requests[1].Reconciliation.Evidence.Operator != input.Evidence.Operator {
		t.Fatalf("canonical keys restricted legitimate Unicode values: %v", err)
	}
}
