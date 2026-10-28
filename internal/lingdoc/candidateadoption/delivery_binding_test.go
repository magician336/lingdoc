package candidateadoption

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A source-identity-only sidecar (source-kind persistence, no snapshot and no
// policies) is the record CreateProject writes for a template copy that never
// carried a legacy rule binding. Delivery must read it as "no binding" (#686
// regression: it used to refuse every such project at checks with
// invalid_state), while a half-bound sidecar — policies without a snapshot —
// stays refused.
func TestReadDeliveryBindingTreatsSourceIdentityOnlyAsNoBinding(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	require.NoError(t, store.db.Exec("CREATE TABLE IF NOT EXISTS lingdoc_g5_records (project_id TEXT NOT NULL, kind TEXT NOT NULL, id TEXT NOT NULL, version INTEGER NOT NULL, payload_json TEXT NOT NULL, PRIMARY KEY (project_id, kind, id))").Error)
	seed := func(payload string) {
		t.Helper()
		require.NoError(t, store.db.Exec("DELETE FROM lingdoc_g5_records WHERE project_id = 'p1' AND kind = 'template_binding' AND id = 'c1'").Error)
		require.NoError(t, store.db.Exec("INSERT INTO lingdoc_g5_records (project_id, kind, id, version, payload_json) VALUES ('p1', 'template_binding', 'c1', 1, ?)", payload).Error)
	}
	read := func() (deliveryBindingSidecar, error) {
		return readDeliveryBinding(store.db.Session(&gorm.Session{NewDB: true}), "p1", "c1")
	}

	seed(`{"source_kind":"fixed"}`)
	binding, err := read()
	require.NoError(t, err)
	require.Nil(t, binding.RuleSetSnapshot)
	require.Empty(t, binding.BindingPolicies)

	seed(`{"source_policy_hash":"h","binding_policies":[{"rule_id":"r"}]}`)
	_, err = read()
	require.ErrorIs(t, err, ErrInvalidState)
}
