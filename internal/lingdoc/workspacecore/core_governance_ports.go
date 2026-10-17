package workspacecore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"gorm.io/gorm"
)

// SQL is exposed only to trusted composition adapters, never to transports.
func NewTransactionAdapter(db *gorm.DB) Transaction { return gormTransaction{db: db} }
func TransactionDatabase(tx Transaction) (*gorm.DB, error) {
	t, ok := tx.(gormTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	return t.db, nil
}
func (s *Service) AuthorizeInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID, capability string) (Project, error) {
	request, err := authorizationRequestFromCapability(actor, projectID, capability)
	if err != nil {
		return Project{}, err
	}
	if projectID == "" {
		return Project{}, s.authorizeTenant(ctx, tx, request)
	}
	return s.authorizeProject(ctx, tx, request)
}

type CatalogTemplateDefinition struct {
	Definition                                               Template
	BindingPolicies                                          []governance.BindingPolicy
	SourcePolicyHash                                         string
	RuleSetSnapshot                                          *template.RuleSet
	SourceRuleSetID, SourceRuleSetVersion, SourceRuleSetHash string
}
type TemplateCatalogResolver interface {
	Resolve(context.Context, Transaction, Actor, string, string) (CatalogTemplateDefinition, error)
}

func (s *Service) SetTemplateCatalogResolver(resolver TemplateCatalogResolver) { s.catalog = resolver }
func (s *Service) resolveTemplate(ctx context.Context, tx Transaction, actor Actor, id, version string) (CatalogTemplateDefinition, error) {
	if s.catalog != nil {
		value, err := s.catalog.Resolve(ctx, tx, actor, id, version)
		if err == nil {
			return value, nil
		}
		if id != template.DemoTemplateID || !errors.Is(err, governance.ErrNotFound) {
			return CatalogTemplateDefinition{}, err
		}
	}
	// Existing injected exact-version readers remain valid for legacy callers;
	// production supplies the actor/tenant catalog resolver above.
	definition, err := s.templates.Get(id, version)
	return CatalogTemplateDefinition{Definition: definition}, err
}
func makeCatalogTemplateCopy(actor Actor, pid string, version int64, value CatalogTemplateDefinition, status string) (*ProjectTemplateCopy, error) {
	copy, err := projectTemplateCopy(actor, pid, version, value.Definition, status)
	if err != nil {
		return nil, err
	}
	if value.RuleSetSnapshot != nil {
		if err := template.CanBindRuleSet(*value.RuleSetSnapshot); err != nil {
			return nil, ErrInvalidState
		}
		copy.RuleSetSnapshot = value.RuleSetSnapshot
		copy.SourceRuleSetID = value.SourceRuleSetID
		copy.SourceRuleSetVersion = value.SourceRuleSetVersion
		copy.SourceRuleSetHash = value.SourceRuleSetHash
		if copy.Definition.RuleSetSnapshot == nil && copy.Definition.RuleSetRef == nil {
			copy.RulesetHash = value.RuleSetSnapshot.RulesetHash
		}
	}
	copy.BindingPolicies = value.BindingPolicies
	copy.SourcePolicyHash = value.SourcePolicyHash
	if copy.SourcePolicyHash != "" {
		hash, err := governance.HashBindingPolicies(copy.BindingPolicies)
		if err != nil || hash != copy.SourcePolicyHash {
			return nil, ErrInvalidState
		}
	}
	if validateProjectTemplateCopy(copy) != nil {
		return nil, ErrInvalidState
	}
	return copy, nil
}
func hydrateTemplateBinding(db *gorm.DB, copy *ProjectTemplateCopy) error {
	if copy == nil || !db.Migrator().HasTable(&g5RecordRow{}) {
		return nil
	}
	var value ProjectTemplateCopy
	_, err := (gormTransaction{db}).G5Get(copy.ProjectID, "template_binding", copy.ID, &value)
	if err == ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	copy.BindingPolicies = value.BindingPolicies
	copy.SourcePolicyHash = value.SourcePolicyHash
	copy.RuleSetSnapshot = value.RuleSetSnapshot
	copy.SourceRuleSetID = value.SourceRuleSetID
	copy.SourceRuleSetVersion = value.SourceRuleSetVersion
	copy.SourceRuleSetHash = value.SourceRuleSetHash
	return nil
}
func saveTemplateBinding(tx gormTransaction, copy ProjectTemplateCopy) error {
	if copy.RuleSetSnapshot == nil && copy.SourcePolicyHash == "" {
		return nil
	}
	return tx.G5Put(copy.ProjectID, "template_binding", copy.ID, 0, ProjectTemplateCopy{BindingPolicies: copy.BindingPolicies, SourcePolicyHash: copy.SourcePolicyHash, RuleSetSnapshot: copy.RuleSetSnapshot, SourceRuleSetID: copy.SourceRuleSetID, SourceRuleSetVersion: copy.SourceRuleSetVersion, SourceRuleSetHash: copy.SourceRuleSetHash})
}
func sanitizedSpecAudit(fields map[string]SpecField) map[string]any {
	out := map[string]any{}
	for id, field := range fields {
		sum := sha256.Sum256([]byte(field.Value))
		item := map[string]any{"origin": field.Origin, "status": field.Status, "value_hash": hex.EncodeToString(sum[:]), "modified_by": field.ModifiedBy, "modified_at": field.ModifiedAt, "modified_project_version": field.ModifiedProjectVersion}
		if field.Provenance != nil {
			item["source_type"] = field.Provenance.SourceType
			item["source_id"] = field.Provenance.SourceID
			item["source_version"] = field.Provenance.SourceVersion
			item["needs_review"] = field.Provenance.NeedsReview
		}
		out[id] = item
	}
	return out
}
func sanitizedChangeFields(fields []ChangeFieldDelta) []map[string]any {
	out := []map[string]any{}
	for _, f := range fields {
		old := sha256.Sum256([]byte(f.OldValue))
		next := sha256.Sum256([]byte(f.NewValue))
		out = append(out, map[string]any{"key": f.Key, "old_hash": hex.EncodeToString(old[:]), "new_hash": hex.EncodeToString(next[:])})
	}
	return out
}
func requiresGenericCriticalAudit(op string) bool {
	switch op {
	case "saveMembers", "transferOwner", "requestOwnerTransfer", "acceptOwnerTransfer", "commitWorkingCopy", "saveChapter":
		return true
	}
	return false
}

func (s *Service) SetG2ActionExecutor(executor G2ActionExecutor) { s.g2Runtime.Actions = executor }
