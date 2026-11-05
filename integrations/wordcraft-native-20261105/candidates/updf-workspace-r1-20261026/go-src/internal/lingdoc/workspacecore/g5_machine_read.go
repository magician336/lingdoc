package workspacecore

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

var ErrMachineReadDependency = errors.New("machine_read_dependency_unavailable")

// MachineReadSourcePolicy is a server-composed extension of the existing source
// policy. Client input cannot install a checker or manufacture an authorization.
type MachineReadSourcePolicy interface {
	ValidateMachineRead(context.Context, MachineReadAuthorization) error
}

// Fields are private: only the transaction-bound machine evaluator can issue
// this permit. Grantor is a delegation constraint, never the request principal.
type MachineReadAuthorization struct {
	principal   types.Principal
	tenantID    uint64
	keyScope    types.TenantAPIKeyScope
	projectID   string
	grantorID   string
	grantorRole types.TenantRole
	sourceIDs   []string
	stateHash   string
}

func (a MachineReadAuthorization) Principal() types.Principal    { return a.principal }
func (a MachineReadAuthorization) TenantID() uint64              { return a.tenantID }
func (a MachineReadAuthorization) ProjectID() string             { return a.projectID }
func (a MachineReadAuthorization) GrantorID() string             { return a.grantorID }
func (a MachineReadAuthorization) GrantorRole() types.TenantRole { return a.grantorRole }
func (a MachineReadAuthorization) SourceIDs() []string           { return slices.Clone(a.sourceIDs) }
func (a MachineReadAuthorization) KeyScope() types.TenantAPIKeyScope {
	out := a.keyScope
	out.Capabilities = slices.Clone(out.Capabilities)
	out.KnowledgeBaseIDs = slices.Clone(out.KnowledgeBaseIDs)
	return out
}
func (a MachineReadAuthorization) MatchesContext(ctx context.Context) bool {
	p, tenant, key, err := machineReadIdentity(ctx)
	if err != nil || a.stateHash == "" || a.projectID == "" || a.grantorID == "" ||
		p != a.principal || tenant != a.tenantID || key.KeyID != a.keyScope.KeyID ||
		!key.FullAccess && !key.HasCapability(types.APIKeyCapabilityRetrieve) {
		return false
	}
	for _, kb := range a.keyScope.KnowledgeBaseIDs {
		if !slices.Contains(key.KnowledgeBaseIDs, kb) {
			return false
		}
	}
	return true
}

type MachineProjectSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	ProjectVersion  int64  `json:"project_version"`
	ContextRevision int64  `json:"context_revision"`
}
type MachineChapterView struct {
	ID               string   `json:"id"`
	ProjectID        string   `json:"project_id"`
	Title            string   `json:"title"`
	CurrentVersionID *string  `json:"current_version_id"`
	BodyMarkdown     string   `json:"body_markdown"`
	SourceIDs        []string `json:"source_ids"`
}
type machineReadSnapshot struct {
	authorization MachineReadAuthorization
	summary       MachineProjectSummary
	chapter       MachineChapterView
	grantVersion  int64
}
type machineNativeKey struct {
	ID               uint64
	TenantID         *uint64
	ScopeType        types.APIKeyScopeType
	FullAccess       bool
	Capabilities     types.StringArray
	KnowledgeBaseIDs types.StringArray
	ExpiresAt        *time.Time
	RevokedAt        *time.Time
}

func machineReadIdentity(ctx context.Context) (types.Principal, uint64, types.TenantAPIKeyScope, error) {
	p, known := types.PrincipalFromContext(ctx)
	tenant, tenantKnown := types.TenantIDFromContext(ctx)
	scope, keyKnown := types.TenantAPIKeyScopeFromContext(ctx)
	// The first bounded path accepts native tenant principals only. External
	// user/platform/embed personas remain closed until separately specified.
	if !known || !tenantKnown || !keyKnown || tenant == 0 || scope.KeyID == 0 ||
		scope.IsPlatform() || p.Type != types.PrincipalAPITenant || p.ID != strconv.FormatUint(tenant, 10) {
		return types.Principal{}, 0, types.TenantAPIKeyScope{}, ErrNotFound
	}
	return p, tenant, scope, nil
}

func currentMachineNativeKey(tx Transaction, tenant, keyID uint64) (machineNativeKey, error) {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return machineNativeKey{}, ErrMachineReadDependency
	}
	var key machineNativeKey
	err = db.Table("tenant_api_keys").
		Select("id,tenant_id,scope_type,full_access,capabilities,knowledge_base_ids,expires_at,revoked_at").
		Where("id=? AND tenant_id=?", keyID, tenant).Take(&key).Error
	if err != nil || key.TenantID == nil || *key.TenantID != tenant ||
		key.ScopeType != types.APIKeyScopeTenant || key.RevokedAt != nil ||
		key.ExpiresAt != nil && !time.Now().UTC().Before(*key.ExpiresAt) ||
		!key.FullAccess && !slices.Contains(key.Capabilities, string(types.APIKeyCapabilityRetrieve)) {
		return machineNativeKey{}, ErrNotFound
	}
	return key, nil
}

func machineReadEnvelope(key machineNativeKey, claim types.TenantAPIKeyScope) (types.TenantAPIKeyScope, error) {
	if claim.KeyID != key.ID || claim.IsPlatform() ||
		!claim.FullAccess && !claim.HasCapability(types.APIKeyCapabilityRetrieve) {
		return types.TenantAPIKeyScope{}, ErrNotFound
	}
	allowedKBs := types.StringArray{}
	for _, kb := range key.KnowledgeBaseIDs {
		if slices.Contains(claim.KnowledgeBaseIDs, kb) && !slices.Contains(allowedKBs, kb) {
			allowedKBs = append(allowedKBs, kb)
		}
	}
	// A read grant never carries full-access authority into downstream ports.
	return types.TenantAPIKeyScope{KeyID: key.ID, ScopeType: types.APIKeyScopeTenant,
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityRetrieve)},
		KnowledgeBaseIDs: allowedKBs}, nil
}

func (s *Service) machineReadSnapshot(ctx context.Context, projectID, chapterID string) (machineReadSnapshot, error) {
	var out machineReadSnapshot
	if s.mode != AuthorizationModeEnforce || strings.TrimSpace(projectID) != projectID || projectID == "" {
		return out, ErrNotFound
	}
	principal, tenant, claim, err := machineReadIdentity(ctx)
	if err != nil {
		return out, err
	}
	err = s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		key, err := currentMachineNativeKey(tx, tenant, claim.KeyID)
		if err != nil {
			return err
		}
		envelope, err := machineReadEnvelope(key, claim)
		if err != nil {
			return err
		}
		store, ok := tx.(g5Transaction)
		if !ok {
			return ErrMachineReadDependency
		}
		var grant ProjectAPIKeyGrant
		version, err := store.G5Get(projectID, "api_key_grant", hashImpactValue(key.ID), &grant)
		if err != nil || !grant.Active || grant.KeyID != key.ID || grant.CreatedBy == "" ||
			!slices.Contains(grant.Capabilities, "read") {
			return ErrNotFound
		}
		p, err := tx.Project(tenant, projectID)
		if err != nil || p.DiscardedAt != nil {
			return ErrNotFound
		}
		// This Actor is only an authoritative membership lookup subject. It is
		// never put in Caller/Principal/context or used as the machine actor.
		grantor := Actor{TenantID: tenant, UserID: grant.CreatedBy}
		role, err := currentTenantRole(tx, grantor)
		if err != nil || !role.HasPermission(types.TenantRoleContributor) {
			return ErrNotFound
		}
		m, present := projectMember(p, grant.CreatedBy)
		if !present || m.Status != "" && m.Status != "active" ||
			governanceRole(m) != "owner" && governanceRole(m) != "admin" {
			return ErrNotFound
		}
		db, err := TransactionDatabase(tx)
		if err != nil {
			return ErrMachineReadDependency
		}
		var activeUser int64
		if err := db.Table("users").Where("id=? AND is_active=? AND deleted_at IS NULL",
			grant.CreatedBy, true).Count(&activeUser).Error; err != nil {
			return ErrMachineReadDependency
		}
		if activeUser != 1 {
			return ErrNotFound
		}
		out.summary = MachineProjectSummary{ID: p.ID, Name: p.Name, Status: p.Status,
			ProjectVersion: p.ProjectVersion, ContextRevision: p.CurrentContextRevision}
		sourceIDs := []string{}
		if chapterID != "" {
			chapter, err := tx.Chapter(projectID, chapterID)
			if err != nil || p.Status != "active" {
				return ErrNotFound
			}
			sourceIDs = sortedImpactSourceIDs(chapter.SourceIDs)
			sourceIDs = append(sourceIDs, sourcemarker.IDs(chapter.BodyMarkdown)...)
			sourceIDs = sortedImpactSourceIDs(sourceIDs)
			out.chapter = MachineChapterView{ID: chapter.ID, ProjectID: projectID, Title: chapter.Title,
				CurrentVersionID: chapter.CurrentVersionID, BodyMarkdown: chapter.BodyMarkdown, SourceIDs: sourceIDs}
		}
		facts := []any{}
		for _, sourceID := range sourceIDs {
			if len(envelope.KnowledgeBaseIDs) == 0 {
				return ErrNotFound
			}
			var chunk types.Chunk
			if err := db.Where("id=? AND deleted_at IS NULL", sourceID).Take(&chunk).Error; err != nil {
				return ErrNotFound
			}
			var knowledge types.Knowledge
			if err := db.Where("id=? AND deleted_at IS NULL", chunk.KnowledgeID).Take(&knowledge).Error; err != nil {
				return ErrNotFound
			}
			if chunk.TenantID != knowledge.TenantID || !chunk.IsEnabled ||
				!slices.Contains(envelope.KnowledgeBaseIDs, knowledge.KnowledgeBaseID) {
				return ErrNotFound
			}
			var asset evidence.ProjectAsset
			if err := db.Where("project_id=? AND knowledge_id=? AND deleted_at IS NULL",
				projectID, knowledge.ID).Take(&asset).Error; err != nil {
				return ErrNotFound
			}
			if asset.TenantID != knowledge.TenantID || asset.KnowledgeBaseID != knowledge.KnowledgeBaseID {
				return ErrNotFound
			}
			policy, _, basis, err := ReadProjectAssetPolicy(tx, p, asset.ID)
			if err != nil {
				return ErrNotFound
			}
			allowed := policy.Scope == "project" || policy.Scope == "team" ||
				policy.Scope == "personal" && policy.CreatedBy == grant.CreatedBy
			if len(policy.AllowedUserIDs) > 0 {
				allowed = allowed && slices.Contains(policy.AllowedUserIDs, grant.CreatedBy)
			}
			if !allowed {
				return ErrNotFound
			}
			facts = append(facts, struct {
				Chunk                               types.Chunk
				FileHash, ParseStatus, EnableStatus string
				FileSize                            int64
				ProcessedAt                         *time.Time
				Asset                               evidence.ProjectAsset
				Policy                              ProjectAssetPolicy
				Basis                               string
			}{chunk, knowledge.FileHash, knowledge.ParseStatus, knowledge.EnableStatus,
				knowledge.FileSize, knowledge.ProcessedAt, asset, policy, basis})
		}
		out.grantVersion = version
		auth := MachineReadAuthorization{principal: principal, tenantID: tenant, projectID: projectID,
			grantorID: grant.CreatedBy, grantorRole: role, sourceIDs: sourceIDs,
			keyScope: envelope}
		auth.stateHash = hashImpactValue(struct {
			Key          machineNativeKey
			Envelope     types.TenantAPIKeyScope
			Grant        ProjectAPIKeyGrant
			GrantVersion int64
			Member       Member
			Role         types.TenantRole
			Summary      MachineProjectSummary
			Chapter      MachineChapterView
			Facts        []any
		}{key, envelope, grant, version, m, role, out.summary, out.chapter, facts})
		out.authorization = auth
		return nil
	})
	return out, err
}

func (s *Service) readMachineObject(ctx context.Context, projectID, chapterID string) (out machineReadSnapshot, err error) {
	defer func() { s.auditMachineRead(ctx, out, projectID, chapterID, err) }()
	policy, ok := s.sources.(MachineReadSourcePolicy)
	if !ok || policy == nil {
		return out, ErrMachineReadDependency
	}
	before, err := s.machineReadSnapshot(ctx, projectID, chapterID)
	if err != nil {
		return out, err
	}
	out = before
	// Never hold a pool connection over source policy IO. The real source
	// adapter may refresh asset rows and call live sharing services.
	if err = policy.ValidateMachineRead(ctx, before.authorization); err != nil {
		if errors.Is(err, ErrMachineReadDependency) {
			return out, err
		}
		return out, ErrNotFound
	}
	after, err := s.machineReadSnapshot(ctx, projectID, chapterID)
	if err != nil {
		return out, err
	}
	if before.authorization.stateHash != after.authorization.stateHash {
		return out, ErrVersionConflict
	}
	return after, nil
}

func (s *Service) ReadMachineProjectSummary(ctx context.Context, projectID string) (MachineProjectSummary, error) {
	state, err := s.readMachineObject(ctx, projectID, "")
	if err != nil {
		return MachineProjectSummary{}, err
	}
	return state.summary, nil
}
func (s *Service) ReadMachineChapter(ctx context.Context, projectID, chapterID string) (MachineChapterView, error) {
	if strings.TrimSpace(chapterID) != chapterID || chapterID == "" {
		return MachineChapterView{}, ErrNotFound
	}
	state, err := s.readMachineObject(ctx, projectID, chapterID)
	if err != nil {
		return MachineChapterView{}, err
	}
	return state.chapter, nil
}

func (s *Service) auditMachineRead(ctx context.Context, state machineReadSnapshot, pid, chapterID string, authErr error) {
	p, tenant, key, err := machineReadIdentity(ctx)
	if err != nil {
		return
	}
	// Ordinary read audit is fail-open; it cannot change the permission result.
	_ = s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		audit, ok := tx.(auditTransaction)
		if !ok {
			return ErrMachineReadDependency
		}
		return audit.RecordAudit(AuditEvent{ID: uuid.NewString(), TenantID: tenant, ProjectID: pid,
			ActorID: p.StorageID(), UserID: p.StorageID(), Action: "machine.read", Target: chapterID,
			Decision: auditDecision(authErr), Reason: auditReason(authErr), CreatedAt: time.Now().UTC(),
			Details: map[string]any{"key_id": key.KeyID, "principal_type": p.Type,
				"grantor_id": state.authorization.grantorID, "grant_version": state.grantVersion,
				"decision": auditDecision(authErr)}})
	})
}
