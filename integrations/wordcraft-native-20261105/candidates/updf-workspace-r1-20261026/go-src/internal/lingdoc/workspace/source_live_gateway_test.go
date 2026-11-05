package workspace

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFullSourceLivenessRetainsProjectAndSourceOwnerBoundaries(t *testing.T) {
	f := newDeliveryGuardFixture(t, true)
	assets, err := f.runtime.ListAssets(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Len(t, assets, 1)
	id := assets[0].ID
	for _, mutation := range []struct{ name, change, restore string }{
		{"binding owner mismatch", "UPDATE lingdoc_project_assets SET tenant_id=8 WHERE id=?", "UPDATE lingdoc_project_assets SET tenant_id=7 WHERE id=?"},
		{"binding KB mismatch", "UPDATE lingdoc_project_assets SET knowledge_base_id='wrong-kb' WHERE id=?", "UPDATE lingdoc_project_assets SET knowledge_base_id='kb-runtime' WHERE id=?"},
		{"knowledge owner mismatch", "UPDATE knowledges SET tenant_id=8 WHERE id='knowledge-runtime' AND ?<>''", "UPDATE knowledges SET tenant_id=7 WHERE id='knowledge-runtime' AND ?<>''"},
		{"KB owner mismatch", "UPDATE knowledge_bases SET tenant_id=8 WHERE id='kb-runtime' AND ?<>''", "UPDATE knowledge_bases SET tenant_id=7 WHERE id='kb-runtime' AND ?<>''"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			require.NoError(t, f.db.Exec(mutation.change, id).Error)
			denied, err := f.runtime.ListAssets(f.ctx, f.actor, f.project.ID)
			require.NoError(t, err)
			require.Empty(t, denied)
			require.NoError(t, f.db.Exec(mutation.restore, id).Error)
			restored, err := f.runtime.ListAssets(f.ctx, f.actor, f.project.ID)
			require.NoError(t, err)
			require.Len(t, restored, 1)
		})
	}
	other := f.actor
	other.TenantID++
	resolved, err := f.runtime.gateway.ResolveAllowed(f.ctx, f.project.ID, evidenceActor(other), []string{id})
	require.NoError(t, err)
	require.Empty(t, resolved.Allowed)
}
