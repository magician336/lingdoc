// Command acceptance-budget-reconcile performs a single explicitly authorized
// local reconciliation. It neither calls a provider nor exposes an HTTP route.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("acceptance-budget-reconcile", flag.ContinueOnError)
	flags.SetOutput(stderr)
	ledger := flags.String("ledger", "", "explicit absolute calls-ledger JSON path")
	expected := flags.String("expected-ledger-sha256", "", "SHA256 of the complete current ledger bytes")
	number := flags.Int("request", 0, "exact failed request number")
	reason := flags.String("reason", "", "exact operator reason present in the evidence JSON")
	evidencePath := flags.String("evidence", "", "local evidence JSON path; no credentials")
	apply := flags.Bool("apply", false, "explicitly apply this one local reconciliation")
	confirm := flags.Bool("confirm-full-reserve-charge", false, "accept full reserve cost without claiming provider usage")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if !*apply || !*confirm || flags.NArg() != 0 || !filepath.IsAbs(*ledger) || filepath.Ext(*ledger) != ".json" || !filepath.IsAbs(*evidencePath) {
		_, _ = fmt.Fprintln(stderr, "explicit apply, full-reserve confirmation and absolute ledger/evidence paths are required")
		return 2
	}
	data, err := os.ReadFile(*evidencePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "cannot read operator evidence")
		return 1
	}
	var evidence acceptancebudget.EmbeddingHTTPErrorEvidence
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		_, _ = fmt.Fprintln(stderr, "invalid operator evidence")
		return 1
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		_, _ = fmt.Fprintln(stderr, "invalid operator evidence")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	budget := acceptancebudget.New(acceptancebudget.CostOnlyProfile, *ledger)
	err = budget.ReconcileEmbeddingHTTPError(ctx, acceptancebudget.ReconcileEmbeddingHTTPErrorInput{
		ExpectedLedgerSHA256: *expected, RequestNumber: *number, ExpectedStatus: "http_error",
		ExpectedBlockedReason: "http_error", ExpectedReason: *reason, ConfirmFullReserveCharge: *confirm, Evidence: evidence,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "reconciliation refused; ledger retained:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "local full-reserve reconciliation recorded; original http_error and unknown provider usage retained")
	return 0
}
