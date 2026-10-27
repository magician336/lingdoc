package workspacecore

import "context"

// These in-memory catalog doubles represent an immutable source; the real
// production adapter acquires its lifecycle locks in ResolveTemplateSwitch.
func (r ruleSetSourcePolicyCatalog) ResolveTemplateSwitch(ctx context.Context, tx Transaction, actor Actor, id, version string, _ bool) (CatalogTemplateDefinition, error) {
	return r.Resolve(ctx, tx, actor, id, version)
}

func (r resolverWithoutCopyAudit) ResolveTemplateSwitch(ctx context.Context, tx Transaction, actor Actor, id, version string, _ bool) (CatalogTemplateDefinition, error) {
	return r.Resolve(ctx, tx, actor, id, version)
}
