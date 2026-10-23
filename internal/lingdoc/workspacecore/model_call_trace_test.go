package workspacecore_test

import (
	"context"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

// A row that carries no provider request id still has to carry the basis a
// reader checks instead: which provider answered, which model, which product
// action it belonged to, when, and how much it used.
//
// The basis is not a consolation prize. Without a provider request id it is
// the only thing that makes one call reconcilable against the provider's own
// records, so it has to be there for every row.
func TestTheLedgerRowCarriesTheBasisThatStandsInForAProviderRequestID(t *testing.T) {
	db := ledgerTestDB(t)
	ledger := core.NewGORMModelCallLedger(db)
	require.NotNil(t, ledger)

	require.NoError(t, ledger.RecordModelCall(context.Background(), ledgerCallEntry()))

	rows, err := ledger.ModelCallEntriesBySource(context.Background(), 7,
		core.ModelCallLedgerSourceAgentTurn, "ledger-correlation")
	require.NoError(t, err)
	require.Len(t, rows, 1)

	basis := rows[0].ProviderTraceBasis()
	require.True(t, basis.Complete(),
		"a call row must carry the basis a reader checks instead of a provider request id: %+v", basis)
	require.Equal(t, "generic", basis.Provider)
	require.Equal(t, "ledger-model-id", basis.ModelID)
	require.Equal(t, "real", basis.RunMode)
	require.Equal(t, core.ModelCallLedgerSourceAgentTurn, basis.SourceKind)
	require.Equal(t, "ledger-correlation", basis.SourceID)
	require.False(t, basis.CreatedAt.IsZero())
	require.Equal(t, 100, basis.InputTokens)
	require.Equal(t, 30, basis.OutputTokens)
}

// A row that is missing the model cannot claim a complete basis. The gap has
// to be visible: a reader who is told "the basis is complete" must be able to
// rely on it.
func TestABasisWithoutTheModelIsNotComplete(t *testing.T) {
	entry := ledgerCallEntry()
	entry.ModelID = ""
	require.False(t, entry.ProviderTraceBasis().Complete())

	entry = ledgerCallEntry()
	entry.SourceID = ""
	require.False(t, entry.ProviderTraceBasis().Complete())

	entry = ledgerCallEntry()
	entry.CreatedAt = time.Time{}
	require.False(t, entry.ProviderTraceBasis().Complete())
}
