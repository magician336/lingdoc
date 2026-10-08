package workspacecore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Repository owns atomic commit/rollback and database isolation. Callbacks see
// only domain values. ReadOnly gives a repeatable snapshot; RetryLocks allows
// bounded retries of rolled-back spec transactions, never partial writes.
type Repository interface {
	Transaction(context.Context, TransactionOptions, func(Transaction) error) error
}
type TransactionOptions struct{ ReadOnly, RetryLocks bool }

// Transaction is a storage port, not a second application service. Conditional
// writes return ErrVersionConflict unless the expected state alone changes.
type Transaction interface {
	ActiveMember(Actor) (bool, error)
	Project(uint64, string) (Project, error)
	Projects(Actor, int) ([]Project, error)
	InsertProject(uint64, Project) error
	UpdateProject(uint64, Project, Project) error
	ReplaceCollaborators(string, []string) error
	ReplaceMembers(string, []Member) error
	Chapters(string) ([]Chapter, error)
	Chapter(string, string) (Chapter, error)
	WorkingCopy(string, string) (WorkingCopy, error)
	SaveWorkingCopy(WorkingCopy, WorkingCopy) error
	ChapterVersions(string, string) ([]ChapterVersion, error)
	ChapterVersion(string, string, string) (ChapterVersion, error)
	InsertChapter(Chapter) error
	AppendChapter(Chapter, Chapter, int64) error
	Operation(OperationIdentity) (OperationResult, bool, error)
	SaveOperation(OperationIdentity, OperationResult) error
}

type draftCandidateTransaction interface {
	InsertDraftCandidate(DraftCandidate) error
	DraftCandidates(string) ([]DraftCandidate, error)
}

type auditTransaction interface {
	RecordAudit(AuditEvent) error
	AuditEvents(string) ([]AuditEvent, error)
}

type changeSetTransaction interface {
	InsertChangeSet(ChangeSet) error
	ChangeSet(string, string) (ChangeSet, error)
	ChangeSets(string) ([]ChangeSet, error)
	UpdateChangeSet(ChangeSet, ChangeSet) error
	InvalidateChapterConfirmations(string, []string) error
}

type ProjectAuthorizer interface {
	AuthorizeTenant(Transaction, Actor, string) error
	AuthorizeProject(Transaction, Actor, string, string) (Project, error)
}

type ownerTransferTransaction interface {
	InsertOwnerTransfer(OwnerTransfer) error
	OwnerTransfer(string, string) (OwnerTransfer, error)
	AcceptOwnerTransfer(string, string, int64, time.Time) error
	SetMemberRole(string, string, string) error
	EnsureMember(string, string, string) error
}
type OperationIdentity struct {
	Actor                  Actor
	Operation, Target, Key string
}
type OperationResult struct {
	BodyHash string
	Body     json.RawMessage
	Status   int
}

// Service owns validation, permissions, version expectations and replay order.
// Swapping storage cannot swap or omit these rules.
type Service struct {
	repository Repository
	templates  TemplateReader
	sources    SourcePolicy
	authorizer ProjectAuthorizer
	audit      AuditSink
	mode       AuthorizationMode
}

func NewService(repository Repository, readers ...TemplateReader) *Service {
	reader := TemplateReader(ContractDemoTemplate{})
	if len(readers) > 0 && readers[0] != nil {
		reader = readers[0]
	}
	return NewServiceWithAuthorizer(repository, reader, nil, nil)
}

// NewServiceWithSources retains the main-line citation recheck before replay.
func NewServiceWithSources(repository Repository, reader TemplateReader, sources SourcePolicy) *Service {
	return NewServiceWithAuthorizer(repository, reader, sources, nil)
}

func NewServiceWithAuthorizer(repository Repository, reader TemplateReader, sources SourcePolicy, authorizer ProjectAuthorizer) *Service {
	return NewServiceWithAudit(repository, reader, sources, authorizer, nil)
}

func NewServiceWithAudit(repository Repository, reader TemplateReader, sources SourcePolicy, authorizer ProjectAuthorizer, audit AuditSink) *Service {
	return NewServiceWithAuditMode(repository, reader, sources, authorizer, audit, AuthorizationModeEnforce)
}

func NewServiceWithAuditMode(repository Repository, reader TemplateReader, sources SourcePolicy, authorizer ProjectAuthorizer, audit AuditSink, mode AuthorizationMode) *Service {
	if reader == nil {
		reader = ContractDemoTemplate{}
	}
	if authorizer == nil {
		authorizer = transactionProjectAuthorizer{}
	}
	if mode != AuthorizationModeLog && mode != AuthorizationModeRollback {
		mode = AuthorizationModeEnforce
	}
	return &Service{repository: repository, templates: reader, sources: sources, authorizer: authorizer, audit: audit, mode: mode}
}

func validActor(actor Actor) bool { return actor.TenantID != 0 && actor.UserID != "" }

func legacyAuthorizeTenant(tx Transaction, actor Actor) error {
	if !validActor(actor) {
		return ErrNotFound
	}
	active, err := tx.ActiveMember(actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrNotFound
	}
	return nil
}

func legacyAuthorizeProject(tx Transaction, actor Actor, projectID, capability string) (Project, error) {
	if capability != "read" && capability != "write" && capability != "manage" {
		return Project{}, ErrInvalidRequest
	}
	if !validActor(actor) {
		return Project{}, ErrNotFound
	}
	active, err := tx.ActiveMember(actor)
	if err != nil {
		return Project{}, err
	}
	if !active {
		return Project{}, ErrNotFound
	}
	p, err := tx.Project(actor.TenantID, projectID)
	if err != nil {
		return Project{}, err
	}
	if p.DiscardedAt != nil {
		if capability != "read" {
			return Project{}, ErrInvalidState
		}
		owner := false
		for _, m := range p.Members {
			if m.UserID == actor.UserID && m.Role == "owner" {
				owner = true
				break
			}
		}
		if !owner {
			return Project{}, ErrNotFound
		}
	}
	for _, m := range p.Members {
		if m.UserID == actor.UserID && (capability != "manage" || m.Role == "owner") {
			return p, nil
		}
	}
	return Project{}, ErrNotFound
}

func (s *Service) authorizeTenant(ctx context.Context, tx Transaction, actor Actor, capability string) error {
	if s.mode == AuthorizationModeRollback {
		return legacyAuthorizeTenant(tx, actor)
	}
	if s.mode != AuthorizationModeLog {
		return s.authorizer.AuthorizeTenant(tx, actor, capability)
	}
	err := s.authorizer.AuthorizeTenant(tx, actor, capability)
	_ = s.recordAudit(ctx, actor, "", "shadow:"+capability, err)
	return legacyAuthorizeTenant(tx, actor)
}

func (s *Service) authorizeProject(ctx context.Context, tx Transaction, actor Actor, projectID, capability string) (Project, error) {
	if s.mode == AuthorizationModeRollback {
		return legacyAuthorizeProject(tx, actor, projectID, capability)
	}
	if s.mode != AuthorizationModeLog {
		return s.authorizer.AuthorizeProject(tx, actor, projectID, capability)
	}
	_, err := s.authorizer.AuthorizeProject(tx, actor, projectID, capability)
	_ = s.recordAudit(ctx, actor, projectID, "shadow:"+capability, err)
	return legacyAuthorizeProject(tx, actor, projectID, capability)
}

type transactionProjectAuthorizer struct{}

func (transactionProjectAuthorizer) AuthorizeTenant(tx Transaction, actor Actor, capability string) error {
	if err := legacyAuthorizeTenant(tx, actor); err != nil {
		return err
	}
	role := actor.Role
	if role == "" && actor.TenantRole != "" {
		role = types.TenantRole(actor.TenantRole)
	}
	if role == "" {
		return nil
	}
	if !role.IsValid() {
		return ErrNotFound
	}
	baseCapability := capability
	if before, _, ok := strings.Cut(capability, ":"); ok {
		baseCapability = before
	}
	required := map[string]types.TenantRole{"read": types.TenantRoleViewer, "create": types.TenantRoleContributor, "write": types.TenantRoleContributor, "manage": types.TenantRoleAdmin,
		"issue-resolve": types.TenantRoleContributor, "issue-dismiss": types.TenantRoleContributor,
		"issue-waive": types.TenantRoleContributor, "issue-dismiss-blocking": types.TenantRoleContributor}[baseCapability]
	if required == "" {
		return ErrInvalidRequest
	}
	if !role.HasPermission(required) {
		return ErrNotFound
	}
	return nil
}

func (transactionProjectAuthorizer) AuthorizeProject(tx Transaction, actor Actor, projectID, capability string) (Project, error) {
	a := transactionProjectAuthorizer{}
	if err := a.AuthorizeTenant(tx, actor, capability); err != nil {
		return Project{}, err
	}
	baseCapability, resource := capability, ""
	if before, after, ok := strings.Cut(capability, ":"); ok {
		baseCapability, resource = before, after
	}
	if baseCapability != "read" && baseCapability != "write" && baseCapability != "manage" &&
		baseCapability != "issue-resolve" && baseCapability != "issue-dismiss" &&
		baseCapability != "issue-waive" && baseCapability != "issue-dismiss-blocking" {
		return Project{}, ErrInvalidRequest
	}
	p, err := tx.Project(actor.TenantID, projectID)
	if err != nil {
		return Project{}, err
	}
	if p.DiscardedAt != nil {
		if baseCapability != "read" {
			return Project{}, ErrInvalidState
		}
		owner := false
		for _, m := range p.Members {
			if m.UserID == actor.UserID && m.Role == "owner" {
				owner = true
			}
		}
		if !owner {
			return Project{}, ErrNotFound
		}
	}
	for _, m := range p.Members {
		if m.UserID != actor.UserID || (m.Status != "" && m.Status != "active") {
			continue
		}
		governance := m.GovernanceRole
		if governance == "" && m.Role == "owner" {
			governance = "owner"
		}
		if baseCapability == "manage" {
			if governance == "owner" || governance == "admin" || m.Role == "owner" {
				return p, nil
			}
			return Project{}, ErrNotFound
		}
		if strings.HasPrefix(baseCapability, "issue-") {
			if governance == "owner" || governance == "admin" || m.Role == "owner" {
				return p, nil
			}
			if baseCapability == "issue-dismiss-blocking" && resource != "" && slices.Contains(m.FunctionRoles, "reviewer") {
				scopes := m.FunctionScopes["reviewer"]
				if slices.Contains(scopes, resource) || slices.Contains(scopes, "asset:"+resource) || slices.Contains(scopes, "delivery:"+resource) {
					return p, nil
				}
			}
			return Project{}, ErrNotFound
		}
		if baseCapability == "read" || m.Role == "owner" || governance == "owner" {
			return p, nil
		}
		if baseCapability == "write" {
			for _, role := range m.FunctionRoles {
				if role != "author" {
					continue
				}
				if resource == "" || slices.Contains(m.FunctionScopes[role], resource) {
					return p, nil
				}
			}
		}
		return Project{}, ErrNotFound
	}
	return Project{}, ErrNotFound
}

func (s *Service) recordAudit(ctx context.Context, actor Actor, projectID, capability string, authErr error) error {
	return s.recordAuditEvent(ctx, AuditEvent{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role, ProjectID: projectID, Capability: capability, Decision: auditDecision(authErr), Reason: auditReason(authErr)})
}

// recordAuditInTransaction keeps the audit write on the caller's transaction.
// SQLite uses a single pooled connection; opening the audit store separately
// while a write transaction is active deadlocks the request until its context
// expires. PostgreSQL still benefits from the same atomic audit boundary.
func (s *Service) recordAuditInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID, capability string, authErr error) {
	if s.audit == nil {
		return
	}
	if _, isGORM := tx.(gormTransaction); isGORM {
		audit, ok := tx.(auditTransaction)
		if !ok {
			return
		}
		_ = audit.RecordAudit(AuditEvent{
			TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role,
			ProjectID: projectID, Capability: capability,
			Decision: auditDecision(authErr), Reason: auditReason(authErr),
		})
		return
	}
	_ = s.recordAudit(ctx, actor, projectID, capability, authErr)
}
func auditDecision(err error) string {
	if err != nil {
		return "deny"
	}
	return "allow"
}
func auditReason(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func (s *Service) recordAuditEvent(ctx context.Context, event AuditEvent) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, event)
}
func (s *Service) Authorize(ctx context.Context, actor Actor, projectID, capability string) error {
	return s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		_, err := s.authorizeProject(ctx, tx, actor, projectID, capability)
		s.recordAuditInTransaction(ctx, tx, actor, projectID, capability, err)
		return err
	})
}
func (s *Service) GetProject(ctx context.Context, actor Actor, id string) (Project, error) {
	var result Project
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) (err error) {
		result, err = s.authorizeProject(ctx, tx, actor, id, "read")
		s.recordAuditInTransaction(ctx, tx, actor, id, "read", err)
		if err == nil && result.DiscardedAt != nil {
			result.Spec = map[string]string{}
			result.SpecFields = map[string]SpecField{}
		}
		return err
	})
	return result, err
}
func (s *Service) ListProjects(ctx context.Context, actor Actor) ([]Project, bool, error) {
	var result []Project
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if !validActor(actor) {
			return ErrNotFound
		}
		if err := s.authorizeTenant(ctx, tx, actor, "read"); err != nil {
			s.recordAuditInTransaction(ctx, tx, actor, "", "read", err)
			return err
		}
		s.recordAuditInTransaction(ctx, tx, actor, "", "read", nil)
		projects, err := tx.Projects(actor, 51)
		result = projects
		return err
	})
	if err != nil {
		return nil, false, err
	}
	truncated := len(result) > 50
	if truncated {
		result = result[:50]
	}
	return result, truncated, nil
}

func projectOwner(p Project, userID string) bool {
	for _, member := range p.Members {
		if member.UserID == userID && member.Role == "owner" {
			return true
		}
	}
	return false
}

func (s *Service) DiscardProject(ctx context.Context, actor Actor, projectID, key string, expectedProjectVersion int64) (json.RawMessage, int, bool, error) {
	if expectedProjectVersion < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	input := map[string]any{"expected_project_version": expectedProjectVersion}
	return s.operation(ctx, actor, "discardProject", projectID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != expectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		if p.DiscardedAt != nil || p.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		now := time.Now().UTC()
		next := p
		next.DiscardedAt = &now
		if next.TemplateCopy != nil {
			copy := *next.TemplateCopy
			copy.Status = TemplateCopyDiscarded
			next.TemplateCopy = &copy
		}
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.discard", Target: projectID, Details: map[string]any{"project_version": next.ProjectVersion}, CreatedAt: now}); err != nil {
				return nil, 0, err
			}
		}
		return next, 200, nil
	})
}

func (s *Service) RestoreProject(ctx context.Context, actor Actor, projectID, key string, expectedProjectVersion int64) (json.RawMessage, int, bool, error) {
	if expectedProjectVersion < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	input := map[string]any{"expected_project_version": expectedProjectVersion}
	return s.operation(ctx, actor, "restoreProject", projectID, key, input, projectID, "read", func(tx Transaction, p Project) (any, int, error) {
		if !projectOwner(p, actor.UserID) {
			return nil, 0, ErrNotFound
		}
		if p.DiscardedAt == nil {
			return nil, 0, ErrInvalidState
		}
		if p.ProjectVersion != expectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		next := p
		next.DiscardedAt = nil
		if p.TemplateCopy != nil && p.TemplateCopy.Status == TemplateCopyDiscarded {
			copy := *p.TemplateCopy
			copy.ID, copy.Version, copy.Status = uuid.NewString(), p.TemplateCopyVersion+1, TemplateCopyDraft
			copy.CreatedBy, copy.CreatedAt = actor.UserID, time.Now().UTC()
			next.TemplateCopy, next.TemplateCopyVersion = &copy, copy.Version
		}
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.restore", Target: projectID, Details: map[string]any{"project_version": next.ProjectVersion}, CreatedAt: time.Now().UTC()}); err != nil {
				return nil, 0, err
			}
		}
		return next, 200, nil
	})
}

// Current authorization precedes replay; only new operations check expected
// versions. Exact response and domain mutation commit together.
func (s *Service) operation(ctx context.Context, actor Actor, op, target, key string, body any, projectID, capability string, write func(Transaction, Project) (any, int, error)) (json.RawMessage, int, bool, error) {
	if !validActor(actor) || len(key) < 8 || len(key) > 128 {
		return nil, 0, false, ErrInvalidRequest
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, 0, false, ErrInvalidRequest
	}
	hash := sha256.Sum256(encoded)
	hashString := hex.EncodeToString(hash[:])
	id := OperationIdentity{Actor: actor, Operation: op, Target: target, Key: key}
	var result OperationResult
	var replayed bool
	err = s.repository.Transaction(ctx, TransactionOptions{RetryLocks: op == "saveSpec" || op == "createProject" || op == "saveTemplateCopy"}, func(tx Transaction) error {
		var p Project
		if projectID == "" {
			if err := s.authorizeTenant(ctx, tx, actor, capability); err != nil {
				s.recordAuditInTransaction(ctx, tx, actor, projectID, capability, err)
				return err
			}
		} else {
			var err error
			p, err = s.authorizeProject(ctx, tx, actor, projectID, capability)
			if err != nil {
				s.recordAuditInTransaction(ctx, tx, actor, projectID, capability, err)
				return err
			}
		}
		s.recordAuditInTransaction(ctx, tx, actor, projectID, capability, nil)
		previous, found, err := tx.Operation(id)
		if err != nil {
			return err
		}
		if found && previous.BodyHash != hashString {
			return ErrIdempotencyConflict
		}
		var sourceIDs []string
		switch op {
		case "saveChapter":
			sourceIDs = body.(SaveChapterInput).SourceIDs
		case "saveWorkingCopy":
			sourceIDs = body.(SaveWorkingCopyInput).SourceIDs
		case "applySelectedRewrite":
			if found {
				var response WorkingCopy
				if err := json.Unmarshal(previous.Body, &response); err != nil {
					return err
				}
				sourceIDs = response.SourceIDs
			} else {
				sourceIDs = body.(ApplyRewriteInput).WorkingCopy.SourceIDs
			}
		case "commitWorkingCopy":
			if found {
				var response CommittedChapterVersion
				if err := json.Unmarshal(previous.Body, &response); err != nil {
					return err
				}
				sourceIDs = response.SourceIDs
			} else {
				chapterID := strings.TrimPrefix(target, projectID+"/")
				workingCopy, err := tx.WorkingCopy(projectID, chapterID)
				if err != nil {
					return err
				}
				sourceIDs = workingCopy.SourceIDs
			}
		case "restoreWorkingCopy":
			if found {
				var response WorkingCopy
				if err := json.Unmarshal(previous.Body, &response); err != nil {
					return err
				}
				sourceIDs = response.SourceIDs
			} else {
				chapterID := strings.TrimPrefix(target, projectID+"/")
				versionID := body.(RestoreWorkingCopyInput).ChapterVersionID
				version, err := tx.ChapterVersion(projectID, chapterID, versionID)
				if err != nil {
					return err
				}
				sourceIDs = version.SourceIDs
			}
		}
		if err := s.checkSources(ctx, actor, projectID, sourceIDs); err != nil {
			return err
		}
		if found {
			result, replayed = previous, true
			return nil
		}
		value, code, err := write(tx, p)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		result = OperationResult{BodyHash: hashString, Body: raw, Status: code}
		return tx.SaveOperation(id, result)
	})
	if err != nil {
		return nil, 0, false, err
	}
	return result.Body, result.Status, replayed, nil
}

type CreateProjectInput struct {
	Name            string `json:"name"`
	TemplateID      string `json:"template_id"`
	TemplateVersion string `json:"template_version,omitempty"`
}
type SaveSpecInput struct {
	ExpectedSpecRevision int64                     `json:"expected_spec_revision"`
	Fields               map[string]string         `json:"fields"`
	FieldMetadata        map[string]SpecFieldInput `json:"field_metadata,omitempty"`
}
type SpecFieldInput struct {
	Origin     string            `json:"origin,omitempty"`
	Status     string            `json:"status,omitempty"`
	Provenance *ProvenanceRecord `json:"provenance,omitempty"`
}
type DraftCandidateInput struct {
	Kind       string            `json:"kind"`
	Title      string            `json:"title"`
	Content    string            `json:"content"`
	Level      string            `json:"level"`
	Provenance *ProvenanceRecord `json:"provenance,omitempty"`
}
type OwnerTransferInput struct {
	ToUserID               string `json:"to_user_id"`
	ExpectedProjectVersion int64  `json:"expected_project_version"`
}
type ActivateProjectInput struct {
	ExpectedSpecRevision   int64 `json:"expected_spec_revision"`
	ExpectedProjectVersion int64 `json:"expected_project_version,omitempty"`
	ReviewedProjectVersion int64 `json:"reviewed_project_version,omitempty"`
}
type TemplateMigrationInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	TemplateID             string `json:"template_id"`
	TemplateVersion        string `json:"template_version"`
}

// TemplateCopyDefinitionInput intentionally excludes source identity, version,
// status and hashes. Those values are owned by the server and copied from the
// current project snapshot.
type TemplateCopyDefinitionInput struct {
	ExpectedProjectVersion      int64                     `json:"expected_project_version"`
	ExpectedTemplateCopyVersion int64                     `json:"expected_template_copy_version"`
	Fields                      []lingdoctemplate.Field   `json:"fields"`
	Sections                    []lingdoctemplate.Section `json:"sections"`
	Terms                       []lingdoctemplate.Term    `json:"terms"`
	RequiredFields              []string                  `json:"required_fields"`
	Rules                       []lingdoctemplate.Rule    `json:"rules"`
}

func projectTemplateCopy(actor Actor, projectID string, version int64, source lingdoctemplate.Template, status string) (*ProjectTemplateCopy, error) {
	definition, err := lingdoctemplate.WithHashes(source)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	return &ProjectTemplateCopy{
		ID: uuid.NewString(), ProjectID: projectID, SourceTemplateID: source.ID,
		SourceTemplateVersion: source.Version, Version: version, Status: status,
		ContentHash: definition.ContentHash, RulesetHash: definition.RulesetHash,
		Definition: definition, CreatedBy: actor.UserID, CreatedAt: time.Now().UTC(),
	}, nil
}

func validateProjectTemplateCopy(copy *ProjectTemplateCopy) error {
	if copy == nil || copy.ID == "" || copy.ProjectID == "" || copy.Version < 1 || copy.SourceTemplateID == "" || copy.SourceTemplateVersion == "" {
		return ErrInvalidState
	}
	contentHash, rulesetHash, err := lingdoctemplate.Hashes(copy.Definition)
	if err != nil || contentHash != copy.ContentHash || rulesetHash != copy.RulesetHash ||
		copy.Definition.ContentHash != copy.ContentHash || copy.Definition.RulesetHash != copy.RulesetHash ||
		copy.Definition.ID != copy.SourceTemplateID || copy.Definition.Version != copy.SourceTemplateVersion {
		return ErrInvalidState
	}
	return nil
}

func editableTemplateDefinition(current lingdoctemplate.Template, input TemplateCopyDefinitionInput) (lingdoctemplate.Template, error) {
	serverRules := make(map[string]lingdoctemplate.Rule, len(current.Rules))
	for _, rule := range current.Rules {
		serverRules[rule.ID] = rule
	}
	seenRules := make(map[string]bool, len(input.Rules))
	for _, rule := range input.Rules {
		serverRule, ok := serverRules[rule.ID]
		if ok {
			if rule.Kind != serverRule.Kind || rule.Evaluator != serverRule.Evaluator || rule.Severity != serverRule.Severity {
				return lingdoctemplate.Template{}, fmt.Errorf("%w: rule identity, executor, and severity are server-owned", ErrInvalidRequest)
			}
			seenRules[rule.ID] = true
			continue
		}
		if rule.Severity != lingdoctemplate.SeverityWarning && rule.Severity != lingdoctemplate.SeverityInfo {
			return lingdoctemplate.Template{}, fmt.Errorf("%w: project-defined rules cannot block", ErrInvalidRequest)
		}
	}
	for id := range serverRules {
		if !seenRules[id] {
			return lingdoctemplate.Template{}, fmt.Errorf("%w: project copies cannot remove built-in rules", ErrInvalidRequest)
		}
	}
	definition := current
	definition.Fields = append([]lingdoctemplate.Field{}, input.Fields...)
	definition.Sections = append([]lingdoctemplate.Section{}, input.Sections...)
	definition.Terms = append([]lingdoctemplate.Term{}, input.Terms...)
	definition.RequiredFields = append([]string{}, input.RequiredFields...)
	definition.Rules = append([]lingdoctemplate.Rule{}, input.Rules...)
	definition.ContentHash, definition.RulesetHash = "", ""
	if len(definition.RequiredFields) == 0 {
		for _, field := range definition.Fields {
			if field.Required {
				definition.RequiredFields = append(definition.RequiredFields, field.ID)
			}
		}
	}
	definition, err := lingdoctemplate.WithHashes(definition)
	if err != nil {
		return lingdoctemplate.Template{}, fmt.Errorf("%w: invalid template copy definition", ErrInvalidRequest)
	}
	return definition, nil
}

func templateFieldDefinitions(template Template) map[string]TemplateField {
	fields := make(map[string]TemplateField, len(template.RequiredFields))
	for _, field := range template.Fields {
		fields[field.ID] = field
	}
	for _, field := range template.RequiredFields {
		value := fields[field]
		value.ID, value.Required = field, true
		if value.Type == "" {
			value.Type = "string"
		}
		fields[field] = value
	}
	return fields
}

func compatibleTemplateValue(value string, field TemplateField) bool {
	switch field.Type {
	case "", "string", "text":
		return true
	case "number", "integer":
		_, err := strconv.ParseFloat(value, 64)
		return err == nil
	case "boolean":
		_, err := strconv.ParseBool(value)
		return err == nil
	default:
		return false
	}
}

func buildTemplateMigrationPreview(p Project, target Template, expectedVersion int64) TemplateMigrationPreview {
	allowed := templateFieldDefinitions(target)
	keys := make([]string, 0, len(p.Spec))
	for key := range p.Spec {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	preview := TemplateMigrationPreview{
		ProjectID: p.ID, SourceTemplateID: p.TemplateID, SourceTemplateVersion: p.TemplateVersion,
		SourceCopyVersion: p.TemplateCopyVersion,
		TargetTemplateID:  target.ID, TargetTemplateVersion: target.Version, TargetCopyVersion: p.TemplateCopyVersion + 1, ExpectedProjectVersion: expectedVersion,
		Fields: make([]TemplateMigrationField, 0, len(keys)+len(target.RequiredFields)), MissingRequired: []string{}, Orphaned: []string{}, Incompatible: []string{},
	}
	contentHash, rulesetHash, _ := lingdoctemplate.Hashes(target)
	preview.TargetContentHash, preview.TargetRulesetHash = contentHash, rulesetHash
	if p.TemplateCopy != nil {
		preview.RulesetChanged = p.TemplateCopy.RulesetHash != rulesetHash
		preview.SectionChanges = sectionChanges(p.TemplateCopy.Definition.Sections, target.Sections)
	}
	oldFields := map[string]lingdoctemplate.Field{}
	if p.TemplateCopy != nil {
		for _, field := range p.TemplateCopy.Definition.Fields {
			oldFields[field.ID] = field
		}
	}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		status := "orphaned"
		migrationField := TemplateMigrationField{FieldID: key, Value: p.Spec[key], Status: status}
		if old, ok := oldFields[key]; ok {
			migrationField.Before, migrationField.OldType = old.Label, old.Type
		}
		if field, ok := allowed[key]; ok {
			status = "preserved"
			migrationField.After, migrationField.NewType = field.Label, field.Type
			if !compatibleTemplateValue(p.Spec[key], field) {
				status = "incompatible"
				preview.Incompatible = append(preview.Incompatible, key)
				if field.Required {
					preview.MissingRequired = append(preview.MissingRequired, key)
				}
			}
		} else {
			preview.Orphaned = append(preview.Orphaned, key)
		}
		migrationField.Status = status
		preview.Fields = append(preview.Fields, migrationField)
		seen[key] = struct{}{}
	}
	fieldKeys := make([]string, 0, len(allowed))
	for key := range allowed {
		fieldKeys = append(fieldKeys, key)
	}
	slices.Sort(fieldKeys)
	for _, key := range fieldKeys {
		if _, ok := seen[key]; ok {
			continue
		}
		field := allowed[key]
		status := "added"
		if field.Required {
			status = "missing"
			preview.MissingRequired = append(preview.MissingRequired, key)
		}
		preview.Fields = append(preview.Fields, TemplateMigrationField{FieldID: key, After: field.Label, NewType: field.Type, Status: status})
	}
	slices.Sort(preview.MissingRequired)
	slices.Sort(preview.Orphaned)
	slices.Sort(preview.Incompatible)
	return preview
}

func (s *Service) PreviewTemplateMigration(ctx context.Context, actor Actor, projectID string, input TemplateMigrationInput) (TemplateMigrationPreview, error) {
	if input.ExpectedProjectVersion < 0 || strings.TrimSpace(input.TemplateID) == "" {
		return TemplateMigrationPreview{}, ErrInvalidRequest
	}
	target, err := s.templates.Get(strings.TrimSpace(input.TemplateID), strings.TrimSpace(input.TemplateVersion))
	if err != nil {
		return TemplateMigrationPreview{}, err
	}
	var result TemplateMigrationPreview
	err = s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, actor, projectID, "read")
		if err != nil {
			return err
		}
		if p.Status != "draft" || p.DiscardedAt != nil {
			return ErrInvalidState
		}
		if input.ExpectedProjectVersion > 0 && p.ProjectVersion != input.ExpectedProjectVersion {
			return ErrVersionConflict
		}
		result = buildTemplateMigrationPreview(p, target, p.ProjectVersion)
		return nil
	})
	return result, err
}

func (s *Service) PreviewTemplateCopyEdit(ctx context.Context, actor Actor, projectID string, input TemplateCopyDefinitionInput) (TemplateMigrationPreview, error) {
	if input.ExpectedProjectVersion < 1 || input.ExpectedTemplateCopyVersion < 1 || input.Fields == nil || input.Sections == nil || input.Terms == nil || input.Rules == nil {
		return TemplateMigrationPreview{}, ErrInvalidRequest
	}
	var result TemplateMigrationPreview
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, actor, projectID, "manage")
		if err != nil {
			return err
		}
		if p.Status != "draft" || p.DiscardedAt != nil || p.TemplateCopy == nil {
			return ErrInvalidState
		}
		if p.ProjectVersion != input.ExpectedProjectVersion || p.TemplateCopyVersion != input.ExpectedTemplateCopyVersion {
			return ErrVersionConflict
		}
		definition, err := editableTemplateDefinition(p.TemplateCopy.Definition, input)
		if err != nil {
			return err
		}
		result = buildTemplateMigrationPreview(p, definition, p.ProjectVersion)
		result.TargetCopyVersion = p.TemplateCopyVersion + 1
		result.TargetContentHash, result.TargetRulesetHash = definition.ContentHash, definition.RulesetHash
		result.SectionChanges = sectionChanges(p.TemplateCopy.Definition.Sections, definition.Sections)
		result.RulesetChanged = p.TemplateCopy.RulesetHash != definition.RulesetHash
		return nil
	})
	return result, err
}

func (s *Service) SaveTemplateCopy(ctx context.Context, actor Actor, projectID, key string, input TemplateCopyDefinitionInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || input.ExpectedTemplateCopyVersion < 1 || input.Fields == nil || input.Sections == nil || input.Terms == nil || input.Rules == nil {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "saveTemplateCopy", projectID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "draft" || p.DiscardedAt != nil || p.TemplateCopy == nil {
			return nil, 0, ErrInvalidState
		}
		if p.ProjectVersion != input.ExpectedProjectVersion || p.TemplateCopyVersion != input.ExpectedTemplateCopyVersion {
			return nil, 0, ErrVersionConflict
		}
		definition, err := editableTemplateDefinition(p.TemplateCopy.Definition, input)
		if err != nil {
			return nil, 0, err
		}
		copy, err := projectTemplateCopy(actor, projectID, p.TemplateCopyVersion+1, definition, TemplateCopyDraft)
		if err != nil {
			return nil, 0, err
		}
		next := p
		next.TemplateCopyVersion, next.TemplateCopy = copy.Version, copy
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project_template.edit", Target: copy.ID, Details: map[string]any{"copy_version": copy.Version, "content_hash": copy.ContentHash, "ruleset_hash": copy.RulesetHash}, CreatedAt: copy.CreatedAt}); err != nil {
				return nil, 0, err
			}
		}
		return next, 200, nil
	})
}

func (s *Service) GetTemplateCopy(ctx context.Context, actor Actor, projectID string, version int64) (ProjectTemplateCopy, error) {
	if version < 1 {
		return ProjectTemplateCopy{}, ErrInvalidRequest
	}
	var result ProjectTemplateCopy
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		reader, ok := tx.(templateCopyReader)
		if !ok {
			return ErrInvalidState
		}
		var err error
		result, err = reader.ProjectTemplateCopy(actor.TenantID, projectID, version)
		return err
	})
	return result, err
}

type templateCopyReader interface {
	ProjectTemplateCopy(uint64, string, int64) (ProjectTemplateCopy, error)
}

type TemplateSectionChange struct {
	SectionID string `json:"section_id"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Status    string `json:"status"`
}

func sectionChanges(before, after []lingdoctemplate.Section) []TemplateSectionChange {
	old, next := make(map[string]lingdoctemplate.Section, len(before)), make(map[string]lingdoctemplate.Section, len(after))
	for _, section := range before {
		old[section.ID] = section
	}
	for _, section := range after {
		next[section.ID] = section
	}
	ids := make([]string, 0, len(old)+len(next))
	seen := map[string]bool{}
	for id := range old {
		ids = append(ids, id)
		seen[id] = true
	}
	for id := range next {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	changes := make([]TemplateSectionChange, 0)
	for _, id := range ids {
		previous, hadPrevious := old[id]
		current, hasCurrent := next[id]
		switch {
		case !hadPrevious:
			changes = append(changes, TemplateSectionChange{SectionID: id, After: current.Title, Status: "added"})
		case !hasCurrent:
			changes = append(changes, TemplateSectionChange{SectionID: id, Before: previous.Title, Status: "removed"})
		case previous.Title != current.Title || previous.Order != current.Order || previous.Required != current.Required || previous.Description != current.Description:
			changes = append(changes, TemplateSectionChange{SectionID: id, Before: previous.Title, After: current.Title, Status: "changed"})
		}
	}
	return changes
}

func (s *Service) ChangeTemplate(ctx context.Context, actor Actor, projectID, key string, input TemplateMigrationInput) (json.RawMessage, int, bool, error) {
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.TemplateVersion = strings.TrimSpace(input.TemplateVersion)
	if input.ExpectedProjectVersion < 1 || input.TemplateID == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	target, err := s.templates.Get(input.TemplateID, input.TemplateVersion)
	if err != nil {
		return nil, 0, false, err
	}
	targetCopy, err := projectTemplateCopy(actor, projectID, 0, target, TemplateCopyDraft)
	if err != nil {
		return nil, 0, false, err
	}
	return s.operation(ctx, actor, "changeTemplate", projectID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		preview := buildTemplateMigrationPreview(p, target, input.ExpectedProjectVersion)
		next := p
		next.TemplateID, next.TemplateVersion = target.ID, target.Version
		targetCopy.Version = p.TemplateCopyVersion + 1
		next.TemplateCopyVersion, next.TemplateCopy = targetCopy.Version, targetCopy
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project_template.change", Target: projectID, Details: map[string]any{"from": p.TemplateID + "/" + p.TemplateVersion, "to": target.ID + "/" + target.Version, "missing_required": preview.MissingRequired, "orphaned": preview.Orphaned}, CreatedAt: time.Now().UTC()}); err != nil {
				return nil, 0, err
			}
		}
		return next, 200, nil
	})
}

type SaveMembersInput struct {
	ExpectedProjectVersion int64    `json:"expected_project_version"`
	CollaboratorUserIDs    []string `json:"collaborator_user_ids"`
	Members                []Member `json:"members,omitempty"`
}
type SaveChapterInput struct {
	ExpectedChapterVersionID *string         `json:"expected_chapter_version_id"`
	ExpectedSpecRevision     int64           `json:"expected_spec_revision"`
	BodyMarkdown             string          `json:"body_markdown"`
	SourceIDs                []string        `json:"source_ids"`
	CitationUsages           []CitationUsage `json:"citation_usages,omitempty"`
}

type SaveWorkingCopyInput struct {
	CitationUsages              []CitationUsage `json:"citation_usages,omitempty"`
	BaseChapterVersionID        *string         `json:"base_chapter_version_id"`
	ExpectedSpecRevision        int64           `json:"expected_spec_revision"`
	ExpectedWorkingCopyRevision int64           `json:"expected_working_copy_revision"`
	BodyMarkdown                string          `json:"body_markdown"`
	SourceIDs                   []string        `json:"source_ids"`
}

type CommitWorkingCopyInput struct {
	ExpectedSpecRevision        int64   `json:"expected_spec_revision"`
	ExpectedWorkingCopyRevision int64   `json:"expected_working_copy_revision"`
	ExpectedChapterVersionID    *string `json:"expected_chapter_version_id"`
}

type RestoreWorkingCopyInput struct {
	ChapterVersionID            string  `json:"chapter_version_id"`
	ExpectedSpecRevision        int64   `json:"expected_spec_revision"`
	ExpectedWorkingCopyRevision int64   `json:"expected_working_copy_revision"`
	ExpectedChapterVersionID    *string `json:"expected_chapter_version_id"`
}

// ApplyRewriteInput is internal to the selected-rewrite application port; the
// HTTP client cannot choose or replace server-managed review items.
type ApplyRewriteInput struct {
	WorkingCopy SaveWorkingCopyInput `json:"working_copy"`
	ReviewItems []ReviewItem         `json:"review_items"`
}

type CommittedChapterVersion struct {
	ChapterVersionID             string       `json:"chapter_version_id"`
	ParentChapterVersionID       *string      `json:"parent_chapter_version_id"`
	CommittedWorkingCopyRevision int64        `json:"committed_working_copy_revision"`
	NextWorkingCopyRevision      int64        `json:"next_working_copy_revision"`
	SpecRevision                 int64        `json:"spec_revision"`
	BodyMarkdown                 string       `json:"body_markdown"`
	SourceIDs                    []string     `json:"source_ids"`
	ReviewItems                  []ReviewItem `json:"review_items"`
}

func (s *Service) CreateProject(ctx context.Context, actor Actor, key string, input CreateProjectInput) (json.RawMessage, int, bool, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 120 {
		return nil, 0, false, ErrInvalidRequest
	}
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.TemplateVersion = strings.TrimSpace(input.TemplateVersion)
	if input.TemplateVersion == "" {
		input.TemplateVersion = lingdoctemplate.DemoTemplateVersion
	}
	template, err := s.templates.Get(input.TemplateID, input.TemplateVersion)
	if err != nil {
		return nil, 0, false, err
	}
	copy, err := projectTemplateCopy(actor, "pending", 1, template, TemplateCopyDraft)
	if err != nil {
		return nil, 0, false, err
	}
	return s.operation(ctx, actor, "createProject", "projects", key, input, "", "create", func(tx Transaction, _ Project) (any, int, error) {
		id := uuid.NewString()
		copy.ID, copy.ProjectID = uuid.NewString(), id
		p := Project{ID: id, Name: input.Name, Status: "draft", ProjectVersion: 1, CurrentContextRevision: 0, DeliveryStatus: "NOT_READY", Spec: map[string]string{}, TemplateID: template.ID, TemplateVersion: template.Version, TemplateCopyVersion: 1, TemplateCopy: copy, Members: []Member{{UserID: actor.UserID, Role: "owner"}}}
		return p, 201, tx.InsertProject(actor.TenantID, p)
	})
}
func (s *Service) SaveSpec(ctx context.Context, actor Actor, projectID, key string, input SaveSpecInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.Fields == nil {
		return nil, 0, false, ErrInvalidRequest
	}
	for k, v := range input.Fields {
		if strings.TrimSpace(k) == "" || len(k) > 100 || len(v) > 10000 {
			return nil, 0, false, ErrInvalidRequest
		}
	}
	for key, metadata := range input.FieldMetadata {
		if _, ok := input.Fields[key]; !ok || !validSpecFieldMetadata(metadata) {
			return nil, 0, false, ErrInvalidRequest
		}
	}
	return s.operation(ctx, actor, "saveSpec", projectID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		if p.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, ErrVersionConflict
		}
		nextFields := maps.Clone(p.SpecFields)
		if nextFields == nil {
			nextFields = map[string]SpecField{}
		}
		changedFields := make(map[string]struct{})
		for key, value := range input.Fields {
			field := nextFields[key]
			origin, status := field.Origin, field.Status
			provenance := field.Provenance
			if metadata, ok := input.FieldMetadata[key]; ok {
				origin, status, provenance = metadata.Origin, metadata.Status, metadata.Provenance
				if origin == "" {
					origin = "human"
				}
				if status == "" {
					status = "draft"
				}
			} else {
				if origin == "" {
					origin = "human"
				}
				if status == "" {
					status = "draft"
				}
				if field.Value != value {
					provenance = nil
				}
			}
			if field.Value != value || field.Origin != origin || field.Status != status || !reflect.DeepEqual(field.Provenance, provenance) {
				field.Value = value
				field.ModifiedBy = actor.UserID
				field.ModifiedAt = time.Now().UTC()
				field.Origin, field.Status, field.Provenance = origin, status, provenance
				changedFields[key] = struct{}{}
			}
			nextFields[key] = field
		}
		if !maps.Equal(p.Spec, input.Fields) || !maps.Equal(p.SpecFields, nextFields) {
			next := p
			next.Spec = maps.Clone(input.Fields)
			next.SpecFields = nextFields
			next.SpecRevision++
			next.ProjectVersion++
			for key := range changedFields {
				field := next.SpecFields[key]
				field.ModifiedProjectVersion = next.ProjectVersion
				next.SpecFields[key] = field
			}
			if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
				return nil, 0, err
			}
			if audit, ok := tx.(auditTransaction); ok {
				if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project_spec.save", Target: projectID, Details: map[string]any{"spec_revision": next.SpecRevision, "field_metadata": next.SpecFields}, CreatedAt: time.Now().UTC()}); err != nil {
					return nil, 0, err
				}
			}
			p = next
		}
		return p, 200, nil
	})
}

func validateChangeSetInput(input CreateChangeSetInput) error {
	hasTemplateUpgrade := input.TemplateUpgrade != nil
	if input.ExpectedContextRevision < 1 || len(input.Fields) > 2 || (!hasTemplateUpgrade && len(input.Fields) == 0) || (hasTemplateUpgrade && len(input.Fields) != 0) || (!hasTemplateUpgrade && len(input.AffectedChapterIDs) == 0) || strings.TrimSpace(input.Reason) == "" || len([]rune(input.Reason)) > 2000 {
		return ErrInvalidRequest
	}
	seen := make(map[string]struct{}, len(input.AffectedChapterIDs))
	for _, id := range input.AffectedChapterIDs {
		if strings.TrimSpace(id) == "" {
			return ErrInvalidRequest
		}
		if _, ok := seen[id]; ok {
			return ErrInvalidRequest
		}
		seen[id] = struct{}{}
	}
	for key, delta := range input.Fields {
		if key != "research_subject" && key != "research_goal" {
			return ErrInvalidRequest
		}
		if delta.OldValue == delta.NewValue || len(delta.NewValue) > 10000 {
			return ErrInvalidRequest
		}
	}
	if upgrade := input.TemplateUpgrade; upgrade != nil {
		if upgrade.ExpectedProjectVersion < 1 || upgrade.ExpectedTemplateCopyVersion < 1 || upgrade.Fields == nil || upgrade.Sections == nil || upgrade.Terms == nil || upgrade.Rules == nil {
			return ErrInvalidRequest
		}
		for key, value := range upgrade.FieldValues {
			if strings.TrimSpace(key) == "" || len(value) > 10000 {
				return ErrInvalidRequest
			}
		}
	}
	return nil
}

func editableTemplateDefinitionFromUpgrade(current lingdoctemplate.Template, input TemplateUpgradeInput) (lingdoctemplate.Template, error) {
	return editableTemplateDefinition(current, TemplateCopyDefinitionInput{
		ExpectedProjectVersion: input.ExpectedProjectVersion, ExpectedTemplateCopyVersion: input.ExpectedTemplateCopyVersion,
		Fields: input.Fields, Sections: input.Sections, Terms: input.Terms,
		RequiredFields: input.RequiredFields, Rules: input.Rules,
	})
}

// templateSpecAfterUpgrade keeps orphaned historic values available for review,
// applies explicit user mappings, and rejects an upgrade that leaves a current
// required field absent or type-incompatible.
func templateSpecAfterUpgrade(spec map[string]string, definition lingdoctemplate.Template, values map[string]string) (map[string]string, error) {
	fields := templateFieldDefinitions(definition)
	next := maps.Clone(spec)
	if next == nil {
		next = map[string]string{}
	}
	for key, value := range values {
		field, ok := fields[key]
		if !ok || !compatibleTemplateValue(value, field) {
			return nil, fmt.Errorf("%w: invalid template field migration %q", ErrInvalidRequest, key)
		}
		next[key] = value
	}
	for key, value := range next {
		if field, ok := fields[key]; ok && !compatibleTemplateValue(value, field) {
			return nil, fmt.Errorf("%w: template field %q requires an explicit compatible migration", ErrInvalidRequest, key)
		}
	}
	for _, required := range definition.RequiredFields {
		if strings.TrimSpace(next[required]) == "" {
			return nil, fmt.Errorf("%w: required template field %q has no value", ErrInvalidRequest, required)
		}
	}
	return next, nil
}

func (s *Service) CreateChangeSet(ctx context.Context, actor Actor, projectID, key string, input CreateChangeSetInput) (json.RawMessage, int, bool, error) {
	if err := validateChangeSetInput(input); err != nil {
		return nil, 0, false, err
	}
	return s.operation(ctx, actor, "createChangeSet", projectID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "active" || p.DiscardedAt != nil {
			return nil, 0, ErrInvalidState
		}
		if p.CurrentContextRevision != input.ExpectedContextRevision {
			return nil, 0, ErrVersionConflict
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		var templateUpgrade *TemplateUpgrade
		impactChapterIDs := append([]string{}, input.AffectedChapterIDs...)
		var upgradeChapters []Chapter
		if requested := input.TemplateUpgrade; requested != nil {
			if p.ProjectVersion != requested.ExpectedProjectVersion || p.TemplateCopy == nil || p.TemplateCopyVersion != requested.ExpectedTemplateCopyVersion || p.TemplateCopy.Status != TemplateCopyBound {
				return nil, 0, ErrVersionConflict
			}
			definition, err := editableTemplateDefinitionFromUpgrade(p.TemplateCopy.Definition, *requested)
			if err != nil {
				return nil, 0, err
			}
			if _, err := templateSpecAfterUpgrade(p.Spec, definition, requested.FieldValues); err != nil {
				return nil, 0, err
			}
			chapters, err := tx.Chapters(projectID)
			if err != nil {
				return nil, 0, err
			}
			upgradeChapters = chapters
			impactChapterIDs = make([]string, 0, len(chapters))
			for _, chapter := range chapters {
				impactChapterIDs = append(impactChapterIDs, chapter.ID)
			}
			slices.Sort(impactChapterIDs)
			if len(impactChapterIDs) == 0 {
				return nil, 0, ErrInvalidState
			}
			preview := buildTemplateMigrationPreview(p, definition, p.ProjectVersion)
			impact := buildTemplateUpgradeImpact(p.TemplateCopy, definition, preview, impactChapterIDs, upgradeChapters)
			templateUpgrade = &TemplateUpgrade{BaseTemplateCopyVersion: p.TemplateCopyVersion, Definition: definition, FieldValues: maps.Clone(requested.FieldValues), Preview: preview, Impact: impact}
		}
		keys := make([]string, 0, len(input.Fields))
		for key := range input.Fields {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		fields := make([]ChangeFieldDelta, 0, len(keys))
		for _, key := range keys {
			delta := input.Fields[key]
			if p.Spec[key] != delta.OldValue {
				return nil, 0, ErrVersionConflict
			}
			fields = append(fields, ChangeFieldDelta{Key: key, OldValue: delta.OldValue, NewValue: delta.NewValue})
		}
		impacts := make([]ChangeImpact, 0, len(impactChapterIDs))
		for _, chapterID := range impactChapterIDs {
			chapter, err := tx.Chapter(projectID, chapterID)
			if err != nil {
				return nil, 0, err
			}
			reason := "研究条件变化，需要重新复核"
			if templateUpgrade != nil {
				reason = "模板或规则版本变化，需要重新复核"
			}
			impacts = append(impacts, ChangeImpact{ChapterID: chapter.ID, ChapterVersionID: cloneString(chapter.CurrentVersionID), Title: chapter.Title, Reason: reason, Status: "open"})
		}
		change := ChangeSet{ID: uuid.NewString(), ProjectID: projectID, CreatedBy: actor.UserID, Reason: strings.TrimSpace(input.Reason), Status: "assessed", BaseContextRevision: p.CurrentContextRevision, BaseSpecRevision: p.SpecRevision, Fields: fields, Impacts: impacts, TemplateUpgrade: templateUpgrade, CreatedAt: time.Now().UTC()}
		if err := store.InsertChangeSet(change); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			details := map[string]any{"base_context_revision": change.BaseContextRevision, "fields": change.Fields, "impacts": change.Impacts}
			if change.TemplateUpgrade != nil {
				details["template_upgrade"] = map[string]any{"base_template_copy_version": change.TemplateUpgrade.BaseTemplateCopyVersion, "target_content_hash": change.TemplateUpgrade.Preview.TargetContentHash, "target_ruleset_hash": change.TemplateUpgrade.Preview.TargetRulesetHash}
			}
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.create", Target: change.ID, Details: details, Reason: change.Reason, CreatedAt: change.CreatedAt}); err != nil {
				return nil, 0, err
			}
		}
		return change, 201, nil
	})
}

func buildTemplateUpgradeImpact(current *ProjectTemplateCopy, target lingdoctemplate.Template, preview TemplateMigrationPreview, chapterIDs []string, chapters []Chapter) TemplateUpgradeImpact {
	impact := TemplateUpgradeImpact{
		AffectedFieldIDs: []string{}, AffectedSectionIDs: []string{}, AffectedChapterIDs: append([]string{}, chapterIDs...),
		InvalidatedConfirmationChapterIDs: []string{}, ChangedRuleIDs: []string{},
		ValidationIssueEffect:  "旧问题处置仅匹配原规则/目标/项目版本；新版本重新检查，不改变确认或交付门禁。",
		DeliverySnapshotEffect: "既有冻结快照保留为历史；新快照按升级后的模板和规则重新检查。",
	}
	for _, field := range preview.Fields {
		if field.Status != "preserved" || field.Before != field.After || field.OldType != field.NewType {
			impact.AffectedFieldIDs = append(impact.AffectedFieldIDs, field.FieldID)
		}
	}
	for _, section := range preview.SectionChanges {
		impact.AffectedSectionIDs = append(impact.AffectedSectionIDs, section.SectionID)
	}
	for _, chapter := range chapters {
		if chapter.ConfirmationValid {
			impact.InvalidatedConfirmationChapterIDs = append(impact.InvalidatedConfirmationChapterIDs, chapter.ID)
		}
	}
	oldRules := map[string]lingdoctemplate.Rule{}
	if current != nil {
		for _, rule := range current.Definition.Rules {
			oldRules[rule.ID] = rule
		}
	}
	newRules := make(map[string]lingdoctemplate.Rule, len(target.Rules))
	for _, rule := range target.Rules {
		newRules[rule.ID] = rule
	}
	changed := map[string]bool{}
	for id, old := range oldRules {
		if next, ok := newRules[id]; !ok || !reflect.DeepEqual(old, next) {
			changed[id] = true
		}
	}
	for id, next := range newRules {
		if old, ok := oldRules[id]; !ok || !reflect.DeepEqual(old, next) {
			changed[id] = true
		}
	}
	for id := range changed {
		impact.ChangedRuleIDs = append(impact.ChangedRuleIDs, id)
	}
	slices.Sort(impact.AffectedFieldIDs)
	slices.Sort(impact.AffectedSectionIDs)
	slices.Sort(impact.AffectedChapterIDs)
	slices.Sort(impact.InvalidatedConfirmationChapterIDs)
	slices.Sort(impact.ChangedRuleIDs)
	return impact
}

func (s *Service) ListChangeSets(ctx context.Context, actor Actor, projectID string) ([]ChangeSet, error) {
	var result []ChangeSet
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		var err error
		result, err = store.ChangeSets(projectID)
		return err
	})
	return result, err
}

func (s *Service) GetChangeSet(ctx context.Context, actor Actor, projectID, changeSetID string) (ChangeSet, error) {
	var result ChangeSet
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		var err error
		result, err = store.ChangeSet(projectID, changeSetID)
		return err
	})
	return result, err
}

func (s *Service) ApplyChangeSet(ctx context.Context, actor Actor, projectID, changeSetID, key string) (json.RawMessage, int, bool, error) {
	if strings.TrimSpace(changeSetID) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "applyChangeSet", projectID+"/"+changeSetID, key, map[string]string{"change_set_id": changeSetID}, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "active" || p.DiscardedAt != nil {
			return nil, 0, ErrInvalidState
		}
		if !projectOwner(p, actor.UserID) {
			return nil, 0, ErrNotFound
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		change, err := store.ChangeSet(projectID, changeSetID)
		if err != nil {
			return nil, 0, err
		}
		if change.Status == "applied" {
			return change, 200, nil
		}
		if change.Status != "assessed" {
			return nil, 0, ErrInvalidState
		}
		upgradeStale := change.TemplateUpgrade != nil && (p.TemplateCopy == nil || p.TemplateCopyVersion != change.TemplateUpgrade.BaseTemplateCopyVersion)
		if change.BaseContextRevision != p.CurrentContextRevision || change.BaseSpecRevision != p.SpecRevision || upgradeStale {
			stale := change
			stale.Status = "stale"
			if err := store.UpdateChangeSet(change, stale); err != nil {
				return nil, 0, err
			}
			if audit, ok := tx.(auditTransaction); ok {
				if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.stale", Target: change.ID, Details: map[string]any{"base_context_revision": change.BaseContextRevision, "current_context_revision": p.CurrentContextRevision, "template_copy_version": p.TemplateCopyVersion}, CreatedAt: time.Now().UTC()}); err != nil {
					return nil, 0, err
				}
			}
			return stale, 409, nil
		}
		for _, impact := range change.Impacts {
			chapter, err := tx.Chapter(projectID, impact.ChapterID)
			if err != nil {
				return nil, 0, err
			}
			if !sameVersion(impact.ChapterVersionID, chapter.CurrentVersionID) {
				stale := change
				stale.Status = "stale"
				if err := store.UpdateChangeSet(change, stale); err != nil {
					return nil, 0, err
				}
				if audit, ok := tx.(auditTransaction); ok {
					if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.stale", Target: change.ID, Details: map[string]any{"chapter_id": impact.ChapterID, "chapter_version_id": impact.ChapterVersionID}, CreatedAt: time.Now().UTC()}); err != nil {
						return nil, 0, err
					}
				}
				return stale, 409, nil
			}
		}
		next := p
		next.Spec = maps.Clone(p.Spec)
		next.SpecFields = maps.Clone(p.SpecFields)
		if next.Spec == nil {
			next.Spec = map[string]string{}
		}
		if next.SpecFields == nil {
			next.SpecFields = map[string]SpecField{}
		}
		changedSpecKeys := make(map[string]struct{}, len(change.Fields))
		for _, field := range change.Fields {
			next.Spec[field.Key] = field.NewValue
			changedSpecKeys[field.Key] = struct{}{}
			metadata := next.SpecFields[field.Key]
			metadata.Value = field.NewValue
			metadata.Origin = "human"
			metadata.Status = "pending_confirmation"
			metadata.ModifiedBy = actor.UserID
			metadata.ModifiedAt = time.Now().UTC()
			next.SpecFields[field.Key] = metadata
		}
		if upgrade := change.TemplateUpgrade; upgrade != nil {
			if p.TemplateCopy == nil || p.TemplateCopyVersion != upgrade.BaseTemplateCopyVersion {
				return nil, 0, ErrVersionConflict
			}
			migratedSpec, err := templateSpecAfterUpgrade(next.Spec, upgrade.Definition, upgrade.FieldValues)
			if err != nil {
				return nil, 0, fmt.Errorf("%w: template migration is no longer valid", ErrInvalidState)
			}
			for fieldID, value := range migratedSpec {
				if next.Spec[fieldID] == value {
					continue
				}
				next.Spec[fieldID] = value
				changedSpecKeys[fieldID] = struct{}{}
				metadata := next.SpecFields[fieldID]
				metadata.Value = value
				metadata.Origin = "human"
				metadata.Status = "pending_confirmation"
				metadata.ModifiedBy = actor.UserID
				metadata.ModifiedAt = time.Now().UTC()
				next.SpecFields[fieldID] = metadata
			}
			copy, err := projectTemplateCopy(actor, projectID, p.TemplateCopyVersion+1, upgrade.Definition, TemplateCopyBound)
			if err != nil {
				return nil, 0, fmt.Errorf("%w: invalid template upgrade snapshot", ErrInvalidState)
			}
			next.TemplateID, next.TemplateVersion = upgrade.Definition.ID, upgrade.Definition.Version
			next.TemplateCopyVersion, next.TemplateCopy = copy.Version, copy
		}
		next.SpecRevision++
		next.CurrentContextRevision++
		next.ProjectVersion++
		for fieldID := range changedSpecKeys {
			metadata := next.SpecFields[fieldID]
			metadata.ModifiedProjectVersion = next.ProjectVersion
			next.SpecFields[fieldID] = metadata
		}
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		targetContext, targetSpec := next.CurrentContextRevision, next.SpecRevision
		now := time.Now().UTC()
		previousChange := change
		change.Status = "applied"
		change.TargetContextRevision = &targetContext
		change.TargetSpecRevision = &targetSpec
		change.AppliedAt = &now
		for i := range change.Impacts {
			change.Impacts[i].Status = "open"
		}
		if err := store.UpdateChangeSet(previousChange, change); err != nil {
			return nil, 0, err
		}
		chapterIDs := make([]string, 0, len(change.Impacts))
		for _, impact := range change.Impacts {
			chapterIDs = append(chapterIDs, impact.ChapterID)
		}
		if err := store.InvalidateChapterConfirmations(projectID, chapterIDs); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			details := map[string]any{"base_context_revision": change.BaseContextRevision, "target_context_revision": targetContext, "target_spec_revision": targetSpec}
			if change.TemplateUpgrade != nil {
				details["template_upgrade"] = map[string]any{"base_copy_version": change.TemplateUpgrade.BaseTemplateCopyVersion, "target_copy_version": next.TemplateCopyVersion, "content_hash": next.TemplateCopy.ContentHash, "ruleset_hash": next.TemplateCopy.RulesetHash}
			}
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.apply", Target: change.ID, Details: details, CreatedAt: now}); err != nil {
				return nil, 0, err
			}
		}
		return change, 200, nil
	})
}

// RejectChangeSet records the current owner's decision not to apply an
// assessed proposal. It intentionally shares the same operation/idempotency
// boundary as apply so a lost response cannot create a second decision audit.
func (s *Service) RejectChangeSet(ctx context.Context, actor Actor, projectID, changeSetID, key string) (json.RawMessage, int, bool, error) {
	if strings.TrimSpace(changeSetID) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "rejectChangeSet", projectID+"/"+changeSetID, key, map[string]string{"change_set_id": changeSetID}, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "active" || p.DiscardedAt != nil {
			return nil, 0, ErrInvalidState
		}
		if !projectOwner(p, actor.UserID) {
			return nil, 0, ErrNotFound
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		change, err := store.ChangeSet(projectID, changeSetID)
		if err != nil {
			return nil, 0, err
		}
		if change.Status == "rejected" {
			return change, 200, nil
		}
		if change.Status != "assessed" {
			return nil, 0, ErrInvalidState
		}
		previous := change
		change.Status = "rejected"
		if err := store.UpdateChangeSet(previous, change); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.reject", Target: change.ID, Details: map[string]any{"base_context_revision": change.BaseContextRevision}, Reason: change.Reason, CreatedAt: time.Now().UTC()}); err != nil {
				return nil, 0, err
			}
		}
		return change, 200, nil
	})
}

func validSpecFieldMetadata(input SpecFieldInput) bool {
	origin := input.Origin
	if origin == "" {
		origin = "human"
	}
	status := input.Status
	if status == "" {
		status = "draft"
	}
	validOrigin := origin == "human" || origin == "ai_generated" || origin == "ai_assisted_human"
	validStatus := status == "draft" || status == "pending_confirmation" || status == "confirmed" || status == "superseded"
	if !validOrigin || !validStatus {
		return false
	}
	if (origin == "ai_generated" || origin == "ai_assisted_human") && status == "confirmed" {
		return false
	}
	return input.Provenance == nil || input.Provenance.SourceType != "" && input.Provenance.CreatedBy != ""
}

func (s *Service) CreateDraftCandidate(ctx context.Context, actor Actor, projectID, key string, input DraftCandidateInput) (json.RawMessage, int, bool, error) {
	input.Kind = strings.TrimSpace(input.Kind)
	input.Title = strings.TrimSpace(input.Title)
	if input.Kind == "" || input.Title == "" || input.Content == "" || len(input.Content) > 200000 {
		return nil, 0, false, ErrInvalidRequest
	}
	if input.Level == "" {
		input.Level = "background"
	}
	if input.Level != "candidate_evidence" && input.Level != "background" && input.Level != "discovery" {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "createDraftCandidate", projectID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		candidateStore, ok := tx.(draftCandidateTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		candidate := DraftCandidate{ID: uuid.NewString(), ProjectID: projectID, Kind: input.Kind, Title: input.Title, Content: input.Content, Level: input.Level, BasedOnContextRevision: p.CurrentContextRevision, Provenance: input.Provenance, CreatedBy: actor.UserID, CreatedAt: time.Now().UTC()}
		if err := candidateStore.InsertDraftCandidate(candidate); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "draft_candidate.create", Target: candidate.ID, Details: map[string]any{"level": candidate.Level, "kind": candidate.Kind}, CreatedAt: time.Now().UTC()}); err != nil {
				return nil, 0, err
			}
		}
		return candidate, 201, nil
	})
}

func (s *Service) ListAuditEvents(ctx context.Context, actor Actor, projectID string) ([]AuditEvent, error) {
	var result []AuditEvent
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		audit, ok := tx.(auditTransaction)
		if !ok {
			return ErrInvalidState
		}
		var err error
		result, err = audit.AuditEvents(projectID)
		return err
	})
	return result, err
}

func validateValidationIssueBinding(binding ValidationIssueBinding) error {
	if strings.TrimSpace(binding.IssueID) == "" || strings.TrimSpace(binding.RuleID) == "" ||
		strings.TrimSpace(binding.RulesetHash) == "" || strings.TrimSpace(binding.TargetID) == "" || binding.ProjectVersion < 1 ||
		(binding.Severity != "blocking" && binding.Severity != "warning" && binding.Severity != "info") {
		return ErrInvalidRequest
	}
	if binding.TargetVersion != nil && (strings.TrimSpace(*binding.TargetVersion) == "" || len(*binding.TargetVersion) > 256) {
		return ErrInvalidRequest
	}
	return nil
}

func sameValidationIssueBinding(left, right ValidationIssueBinding) bool {
	if left.IssueID != right.IssueID || left.RuleID != right.RuleID || left.RulesetHash != right.RulesetHash ||
		left.Severity != right.Severity || left.TargetID != right.TargetID || left.ProjectVersion != right.ProjectVersion {
		return false
	}
	if left.TargetVersion == nil || right.TargetVersion == nil {
		return left.TargetVersion == nil && right.TargetVersion == nil
	}
	return *left.TargetVersion == *right.TargetVersion
}

// ReplayValidationIssueDisposition returns a completed action before current
// version checks, while still rechecking the caller's present project scope and
// the exact capability used by that recorded decision.
func (s *Service) ReplayValidationIssueDisposition(ctx context.Context, actor Actor, projectID, issueID, key, action, reason string, expectedProjectVersion int64) (json.RawMessage, int, bool, bool, error) {
	action, reason = strings.ToLower(strings.TrimSpace(action)), strings.TrimSpace(reason)
	if !validActor(actor) || strings.TrimSpace(projectID) == "" || strings.TrimSpace(issueID) == "" ||
		len(key) < 8 || len(key) > 128 || (action != "resolve" && action != "dismiss" && action != "waive") ||
		reason == "" || len([]rune(reason)) > 2000 || expectedProjectVersion < 1 {
		return nil, 0, false, false, ErrInvalidRequest
	}
	identity := OperationIdentity{Actor: actor, Operation: "validationIssueDisposition", Target: projectID + "/" + issueID, Key: key}
	var previous OperationResult
	var found bool
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		var err error
		previous, found, err = tx.Operation(identity)
		if err != nil || !found {
			return err
		}
		var disposition ValidationIssueDisposition
		if err := json.Unmarshal(previous.Body, &disposition); err != nil {
			return err
		}
		if disposition.IssueID != issueID || disposition.ProjectVersion != expectedProjectVersion || disposition.Action != action || disposition.Reason != reason {
			return ErrIdempotencyConflict
		}
		capability := "issue-" + disposition.Action
		if disposition.Action == "dismiss" && disposition.Severity == "blocking" {
			capability = "issue-dismiss-blocking"
		}
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, capability+":"+disposition.TargetID); err != nil {
			return err
		}
		project, err := tx.Project(actor.TenantID, projectID)
		if err != nil {
			return err
		}
		if project.Status != "active" || project.DiscardedAt != nil {
			return ErrInvalidState
		}
		return nil
	})
	if err != nil || !found {
		return nil, 0, false, found, err
	}
	return previous.Body, previous.Status, true, true, nil
}

// RecordValidationIssueDisposition appends an immutable, version-bound audit
// event. It deliberately does not mutate the rule result or close a G3 impact
// task; those gates continue to be evaluated independently.
func (s *Service) RecordValidationIssueDisposition(ctx context.Context, actor Actor, projectID, key string, input ValidationIssueDispositionInput) (json.RawMessage, int, bool, error) {
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ExpectedProjectVersion < 1 || validateValidationIssueBinding(input.ValidationIssueBinding) != nil ||
		(input.Action != "resolve" && input.Action != "dismiss" && input.Action != "waive") ||
		input.Reason == "" || len([]rune(input.Reason)) > 2000 ||
		(input.Action == "waive" && input.Severity == "blocking") {
		return nil, 0, false, ErrInvalidRequest
	}
	capability := "issue-" + input.Action
	if input.Action == "dismiss" && input.Severity == "blocking" {
		capability = "issue-dismiss-blocking"
	}
	capability += ":" + input.TargetID
	return s.operation(ctx, actor, "validationIssueDisposition", projectID+"/"+input.IssueID, key, input, projectID, capability, func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "active" || p.DiscardedAt != nil {
			return nil, 0, ErrInvalidState
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		audit, ok := tx.(auditTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		// A human disposition is single-use for one exact finding revision. Check
		// inside the same transaction as the append so concurrent duplicate actions
		// cannot both succeed with different idempotency keys.
		events, err := audit.AuditEvents(projectID)
		if err != nil {
			return nil, 0, err
		}
		for _, event := range events {
			if event.Action != "validation_issue.disposition" || event.Target != input.IssueID {
				continue
			}
			raw, err := json.Marshal(event.Details)
			if err != nil {
				return nil, 0, err
			}
			var details struct {
				Disposition ValidationIssueDisposition `json:"validation_issue_disposition"`
			}
			if err := json.Unmarshal(raw, &details); err != nil {
				return nil, 0, err
			}
			if sameValidationIssueBinding(input.ValidationIssueBinding, details.Disposition.ValidationIssueBinding) {
				return nil, 0, ErrInvalidState
			}
		}
		now := time.Now().UTC()
		binding := input.ValidationIssueBinding
		binding.TargetVersion = cloneString(binding.TargetVersion)
		disposition := ValidationIssueDisposition{ValidationIssueBinding: binding, Action: input.Action, Reason: input.Reason,
			ActorID: actor.UserID, CreatedAt: now}
		if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID,
			Action: "validation_issue.disposition", Target: input.IssueID,
			Details:   map[string]any{"validation_issue_disposition": disposition, "expected_project_version": input.ExpectedProjectVersion},
			CreatedAt: now}); err != nil {
			return nil, 0, err
		}
		return disposition, 201, nil
	})
}

// ValidationIssueDispositions returns only decisions whose complete binding
// still matches the evaluator's current issue. Older versions remain in the
// audit log but cannot be replayed onto a changed rule or target.
func (s *Service) ValidationIssueDispositions(ctx context.Context, actor Actor, projectID string, current []ValidationIssueBinding) (map[string]ValidationIssueDisposition, error) {
	if len(current) > 500 {
		return nil, ErrInvalidRequest
	}
	byID := make(map[string]ValidationIssueBinding, len(current))
	for _, binding := range current {
		if err := validateValidationIssueBinding(binding); err != nil {
			return nil, err
		}
		if _, exists := byID[binding.IssueID]; exists {
			return nil, ErrInvalidRequest
		}
		byID[binding.IssueID] = binding
	}
	result := make(map[string]ValidationIssueDisposition)
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		audit, ok := tx.(auditTransaction)
		if !ok {
			return ErrInvalidState
		}
		events, err := audit.AuditEvents(projectID)
		if err != nil {
			return err
		}
		latest := make(map[string]ValidationIssueDisposition)
		for _, event := range events {
			if event.Action != "validation_issue.disposition" {
				continue
			}
			raw, err := json.Marshal(event.Details)
			if err != nil {
				return err
			}
			var details struct {
				Disposition ValidationIssueDisposition `json:"validation_issue_disposition"`
			}
			if err := json.Unmarshal(raw, &details); err != nil {
				return err
			}
			if details.Disposition.IssueID == "" {
				return ErrInvalidState
			}
			previous, exists := latest[details.Disposition.IssueID]
			if !exists || details.Disposition.ProjectVersion > previous.ProjectVersion ||
				(details.Disposition.ProjectVersion == previous.ProjectVersion && details.Disposition.CreatedAt.After(previous.CreatedAt)) {
				latest[details.Disposition.IssueID] = details.Disposition
			}
		}
		for id, binding := range byID {
			if disposition, exists := latest[id]; exists && sameValidationIssueBinding(binding, disposition.ValidationIssueBinding) {
				result[id] = disposition
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) RequestOwnerTransfer(ctx context.Context, actor Actor, projectID, key string, input OwnerTransferInput) (json.RawMessage, int, bool, error) {
	input.ToUserID = strings.TrimSpace(input.ToUserID)
	if input.ToUserID == "" || input.ToUserID == actor.UserID || input.ExpectedProjectVersion < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "requestOwnerTransfer", projectID, key, input, "", "read", func(tx Transaction, _ Project) (any, int, error) {
		p, err := tx.Project(actor.TenantID, projectID)
		if err != nil {
			return nil, 0, err
		}
		currentOwner := ""
		for _, member := range p.Members {
			if member.Role == "owner" {
				currentOwner = member.UserID
				break
			}
		}
		if currentOwner == "" {
			return nil, 0, ErrInvalidState
		}
		ownerActive, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: currentOwner})
		if err != nil {
			return nil, 0, err
		}
		role := string(actor.Role)
		if role == "" {
			role = actor.TenantRole
		}
		isRecoveryAdmin := actor.SystemAdmin || role == "admin" || role == "owner"
		if actor.UserID != currentOwner && !(isRecoveryAdmin && !ownerActive) {
			return nil, 0, ErrNotFound
		}
		if p.ProjectVersion != input.ExpectedProjectVersion || p.Status == "archived" {
			return nil, 0, ErrVersionConflict
		}
		if p.DiscardedAt != nil {
			return nil, 0, ErrInvalidState
		}
		active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: input.ToUserID})
		if err != nil || !active {
			return nil, 0, ErrInvalidState
		}
		transferStore, ok := tx.(ownerTransferTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		transfer := OwnerTransfer{ID: uuid.NewString(), ProjectID: projectID, FromUserID: currentOwner, ToUserID: input.ToUserID, Status: "pending", ExpectedProjectVersion: p.ProjectVersion, CreatedAt: time.Now().UTC()}
		if err := transferStore.InsertOwnerTransfer(transfer); err != nil {
			return nil, 0, err
		}
		return transfer, 201, nil
	})
}

func (s *Service) AcceptOwnerTransfer(ctx context.Context, actor Actor, projectID, transferID, key string) (json.RawMessage, int, bool, error) {
	return s.operation(ctx, actor, "acceptOwnerTransfer", projectID+"/"+transferID, key, map[string]string{"transfer_id": transferID}, "", "read", func(tx Transaction, _ Project) (any, int, error) {
		if !validActor(actor) {
			return nil, 0, ErrNotFound
		}
		p, err := tx.Project(actor.TenantID, projectID)
		if err != nil {
			return nil, 0, err
		}
		if p.DiscardedAt != nil {
			return nil, 0, ErrInvalidState
		}
		store, ok := tx.(ownerTransferTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		transfer, err := store.OwnerTransfer(projectID, transferID)
		if err != nil || transfer.Status != "pending" || transfer.ToUserID != actor.UserID {
			return nil, 0, ErrInvalidState
		}
		if transfer.ExpectedProjectVersion != p.ProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		active, err := tx.ActiveMember(actor)
		if err != nil || !active {
			return nil, 0, ErrNotFound
		}
		if err := store.EnsureMember(projectID, actor.UserID, "collaborator"); err != nil {
			return nil, 0, err
		}
		next := p
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		if err := store.SetMemberRole(projectID, transfer.FromUserID, "collaborator"); err != nil {
			return nil, 0, err
		}
		if err := store.SetMemberRole(projectID, actor.UserID, "owner"); err != nil {
			return nil, 0, err
		}
		acceptedAt := time.Now().UTC()
		if err := store.AcceptOwnerTransfer(projectID, transferID, transfer.ExpectedProjectVersion, acceptedAt); err != nil {
			return nil, 0, err
		}
		transfer.Status = "accepted"
		transfer.AcceptedAt = &acceptedAt
		return transfer, 200, nil
	})
}

func (s *Service) ActivationDiff(ctx context.Context, actor Actor, projectID string, sinceProjectVersion int64) (ActivationDiff, error) {
	var result ActivationDiff
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, actor, projectID, "read")
		if err != nil {
			return err
		}
		if sinceProjectVersion < 0 {
			return ErrInvalidRequest
		}
		result = ActivationDiff{ProjectID: projectID, CurrentProjectVersion: p.ProjectVersion, CurrentSpecRevision: p.SpecRevision, ChangedSinceVersion: sinceProjectVersion, ChangedFields: []SpecFieldChange{}, PendingAIFields: []SpecFieldChange{}}
		for key, field := range p.SpecFields {
			change := SpecFieldChange{Key: key, Value: field.Value, Origin: field.Origin, Status: field.Status, ModifiedBy: field.ModifiedBy, ModifiedAt: field.ModifiedAt, ModifiedProjectVersion: field.ModifiedProjectVersion}
			if (field.Origin == "ai_generated" || field.Origin == "ai_assisted_human") && field.Status == "pending_confirmation" {
				result.PendingAIFields = append(result.PendingAIFields, change)
			}
			if field.ModifiedProjectVersion > sinceProjectVersion || (field.ModifiedProjectVersion == 0 && sinceProjectVersion < p.ProjectVersion) {
				result.ChangedFields = append(result.ChangedFields, change)
			}
		}
		slices.SortFunc(result.ChangedFields, func(a, b SpecFieldChange) int { return strings.Compare(a.Key, b.Key) })
		slices.SortFunc(result.PendingAIFields, func(a, b SpecFieldChange) int { return strings.Compare(a.Key, b.Key) })
		return nil
	})
	return result, err
}

func (s *Service) ListDraftCandidates(ctx context.Context, actor Actor, projectID string) ([]DraftCandidate, error) {
	var result []DraftCandidate
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, actor, projectID, "read"); err != nil {
			return err
		}
		p, err := tx.Project(actor.TenantID, projectID)
		if err != nil {
			return err
		}
		if p.DiscardedAt != nil {
			return ErrInvalidState
		}
		store, ok := tx.(draftCandidateTransaction)
		if !ok {
			return ErrInvalidState
		}
		result, err = store.DraftCandidates(projectID)
		return err
	})
	return result, err
}
func (s *Service) SaveMembers(ctx context.Context, actor Actor, projectID, key string, input SaveMembersInput) (json.RawMessage, int, bool, error) {
	if input.Members != nil {
		return s.saveMemberPermissions(ctx, actor, projectID, key, input)
	}
	if input.ExpectedProjectVersion < 1 || input.CollaboratorUserIDs == nil || len(input.CollaboratorUserIDs) > 20 {
		return nil, 0, false, ErrInvalidRequest
	}
	ids := slices.Clone(input.CollaboratorUserIDs)
	slices.Sort(ids)
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return nil, 0, false, ErrInvalidRequest
	}
	for _, id := range ids {
		if strings.TrimSpace(id) != id || id == "" || len(id) > 64 || id == actor.UserID {
			return nil, 0, false, ErrInvalidRequest
		}
	}
	return s.operation(ctx, actor, "saveMembers", projectID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		for _, id := range ids {
			active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: id})
			if err != nil {
				return nil, 0, err
			}
			if !active {
				return nil, 0, ErrInvalidState
			}
		}
		existing := []string{}
		members := []Member{}
		for _, m := range p.Members {
			if m.Role == "collaborator" {
				existing = append(existing, m.UserID)
			} else {
				members = append(members, m)
			}
		}
		slices.Sort(existing)
		if !slices.Equal(existing, ids) {
			next := p
			next.ProjectVersion++
			if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
				return nil, 0, err
			}
			if err := tx.ReplaceCollaborators(projectID, ids); err != nil {
				return nil, 0, err
			}
			for _, id := range ids {
				members = append(members, Member{UserID: id, Role: "collaborator"})
			}
			slices.SortFunc(members, func(a, b Member) int { return strings.Compare(a.UserID, b.UserID) })
			next.Members = members
			p = next
		}
		return p, 200, nil
	})
}
func (s *Service) ActivateProject(ctx context.Context, actor Actor, projectID, key string, input ActivateProjectInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "activateProject", projectID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		owner := false
		for _, member := range p.Members {
			if member.UserID == actor.UserID && member.Role == "owner" {
				owner = true
				break
			}
		}
		if !owner {
			return nil, 0, ErrNotFound
		}
		// G7 callers predate the project-version review fields. Treat omitted
		// values as the current version while still enforcing explicit stale
		// values for G3 clients.
		if input.ExpectedProjectVersion == 0 {
			input.ExpectedProjectVersion = p.ProjectVersion
		}
		if input.ReviewedProjectVersion == 0 {
			input.ReviewedProjectVersion = p.ProjectVersion
		}
		if input.ExpectedProjectVersion < 1 || input.ReviewedProjectVersion < 1 {
			return nil, 0, ErrInvalidRequest
		}
		if input.ExpectedProjectVersion > 0 && p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		if input.ReviewedProjectVersion > 0 && p.ProjectVersion != input.ReviewedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		if p.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, ErrVersionConflict
		}
		if p.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		var templateCopy *ProjectTemplateCopy
		if p.TemplateCopy == nil {
			// Upgrade legacy draft rows created before project copies existed. The
			// exact source version is copied once and bound in this transaction.
			template, err := s.templates.Get(p.TemplateID, p.TemplateVersion)
			if err != nil || template.Version != p.TemplateVersion {
				return nil, 0, ErrInvalidState
			}
			templateCopy, err = projectTemplateCopy(actor, p.ID, p.TemplateCopyVersion+1, template, TemplateCopyDraft)
			if err != nil {
				return nil, 0, err
			}
		} else {
			copy := *p.TemplateCopy
			if copy.Status != TemplateCopyDraft || validateProjectTemplateCopy(&copy) != nil {
				return nil, 0, ErrInvalidState
			}
			templateCopy = &copy
		}
		template := templateCopy.Definition
		for _, field := range template.RequiredFields {
			if strings.TrimSpace(p.Spec[field]) == "" {
				return nil, 0, ErrInvalidState
			}
		}
		for fieldID, field := range templateFieldDefinitions(template) {
			if field.Required && !compatibleTemplateValue(p.Spec[fieldID], field) {
				return nil, 0, ErrInvalidState
			}
		}
		next := p
		next.Status = "active"
		copy := *templateCopy
		copy.Status = TemplateCopyBound
		next.TemplateCopy, next.TemplateCopyVersion = &copy, copy.Version
		next.CurrentContextRevision = 1
		next.DeliveryStatus = "NOT_READY"
		next.BaselineConfirmationID = uuid.NewString()
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		for _, section := range template.Sections {
			if err := tx.InsertChapter(Chapter{ID: uuid.NewString(), ProjectID: projectID, SectionID: section.ID, Title: section.Title}); err != nil {
				return nil, 0, err
			}
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.activate", Target: projectID, Details: map[string]any{"project_version": next.ProjectVersion, "context_revision": next.CurrentContextRevision, "template_copy_version": copy.Version, "template_content_hash": copy.ContentHash, "ruleset_hash": copy.RulesetHash, "chapter_count": len(template.Sections), "delivery_status": next.DeliveryStatus, "baseline_confirmation_id": next.BaselineConfirmationID}, CreatedAt: time.Now().UTC()}); err != nil {
				return nil, 0, err
			}
		}
		return next, 200, nil
	})
}
func (s *Service) ListChapters(ctx context.Context, actor Actor, projectID string) ([]Chapter, error) {
	var result []Chapter
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		project, err := s.authorizeProject(ctx, tx, actor, projectID, "read")
		if err != nil {
			return err
		}
		result, err = tx.Chapters(projectID)
		if err != nil {
			return err
		}
		template, err := s.templates.Get(project.TemplateID, project.TemplateVersion)
		if err != nil {
			return err
		}
		orderChapters(result, template)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range result {
		ids = append(ids, c.SourceIDs...)
	}
	if len(ids) > 0 {
		if s.sources == nil {
			return nil, ErrSourceUnavailable
		}
		if statusPolicy, ok := s.sources.(CitationStatusPolicy); ok {
			statuses, err := statusPolicy.CitationStatuses(ctx, projectID, actor.UserID, ids)
			if err != nil {
				return nil, err
			}
			bySource := make(map[string]CitationStatus, len(statuses))
			for _, status := range statuses {
				bySource[status.SourceID] = status
			}
			for i := range result {
				if len(result[i].SourceIDs) == 0 {
					continue
				}
				result[i].CitationStatuses = make([]CitationStatus, 0, len(result[i].SourceIDs))
				redact := false
				for _, sourceID := range result[i].SourceIDs {
					status, ok := bySource[sourceID]
					if !ok {
						return nil, ErrSourceUnavailable
					}
					result[i].CitationStatuses = append(result[i].CitationStatuses, status)
					if status.Status == "unavailable" {
						// A revoked source invalidates the derived chapter read.  Returning
						// a redacted 200 here leaves a caller with a seemingly readable
						// chapter list and violates F07's fail-closed contract.
						return nil, ErrSourceUnavailable
					}
					if status.Status != "available" {
						redact = true
					}
				}
				if redact {
					result[i].BodyMarkdown = ""
					result[i].CitationUsages = []CitationUsage{}
					result[i].ConfirmationValid = false
				}
			}
			return result, nil
		}
		if err := s.sources.Validate(ctx, projectID, actor.UserID, ids); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// orderChapters keeps the API order aligned with the template's section order.
// Section IDs are opaque identifiers; sorting them lexicographically can put a
// later section before an earlier one (for example, "method" before "question").
func orderChapters(chapters []Chapter, template Template) {
	ranks := make(map[string]int, len(template.Sections))
	for index, section := range template.Sections {
		ranks[section.ID] = index
	}
	slices.SortStableFunc(chapters, func(left, right Chapter) int {
		leftRank, leftKnown := ranks[left.SectionID]
		rightRank, rightKnown := ranks[right.SectionID]
		if leftKnown && rightKnown {
			if leftRank < rightRank {
				return -1
			}
			if leftRank > rightRank {
				return 1
			}
			return strings.Compare(left.SectionID, right.SectionID)
		}
		if leftKnown {
			return -1
		}
		if rightKnown {
			return 1
		}
		return strings.Compare(left.SectionID, right.SectionID)
	})
}

var sourceMarker = regexp.MustCompile(`\[\[source:([A-Za-z0-9_-]+)\]\]`)

func (s *Service) SaveChapter(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveChapterInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.SourceIDs == nil || len(input.BodyMarkdown) > 200000 {
		return nil, 0, false, ErrInvalidRequest
	}
	markers := sourceMarker.FindAllStringSubmatch(input.BodyMarkdown, -1)
	if strings.Count(input.BodyMarkdown, "[[source:") != len(markers) {
		return nil, 0, false, ErrInvalidRequest
	}
	refs := make([]string, 0, len(markers))
	for _, m := range markers {
		refs = append(refs, m[1])
	}
	slices.Sort(refs)
	refs = slices.Compact(refs)
	declared := slices.Clone(input.SourceIDs)
	slices.Sort(declared)
	if len(slices.Compact(slices.Clone(declared))) != len(declared) || !slices.Equal(refs, declared) {
		return nil, 0, false, ErrInvalidRequest
	}
	usages, err := normalizeCitationUsages(input.CitationUsages, declared)
	if err != nil {
		return nil, 0, false, err
	}
	return s.operation(ctx, actor, "saveChapter", projectID+"/"+chapterID, key, input, projectID, "write:"+chapterID, func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "active" {
			return nil, 0, ErrInvalidState
		}
		if p.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, ErrVersionConflict
		}
		old, err := tx.Chapter(projectID, chapterID)
		if err != nil {
			return nil, 0, err
		}
		if !sameVersion(old.CurrentVersionID, input.ExpectedChapterVersionID) {
			return nil, 0, ErrVersionConflict
		}
		if input.CitationUsages == nil {
			allowed := make(map[string]struct{}, len(declared))
			for _, sourceID := range declared {
				allowed[sourceID] = struct{}{}
			}
			usages = usages[:0]
			for _, usage := range old.CitationUsages {
				if _, ok := allowed[usage.SourceID]; ok {
					usages = append(usages, usage)
				}
			}
			usages = completeCitationUsages(usages, declared)
		}
		nextProject := p
		nextProject.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, nextProject); err != nil {
			return nil, 0, err
		}
		next := old
		id := uuid.NewString()
		next.CurrentVersionID = &id
		next.BodyMarkdown = input.BodyMarkdown
		next.SourceIDs = declared
		next.CitationUsages = usages
		next.ConfirmationValid = false
		if err := tx.AppendChapter(old, next, p.SpecRevision); err != nil {
			return nil, 0, err
		}
		workingCopy, err := tx.WorkingCopy(projectID, chapterID)
		if err != nil {
			return nil, 0, err
		}
		nextCopy := workingCopy
		nextCopy.BaseChapterVersionID = &id
		nextCopy.SpecRevision = p.SpecRevision
		nextCopy.WorkingCopyRevision++
		nextCopy.BodyMarkdown = input.BodyMarkdown
		nextCopy.SourceIDs = slices.Clone(declared)
		nextCopy.CitationUsages = slices.Clone(usages)
		nextCopy.ReviewItems = slices.Clone(old.ReviewItems)
		nextCopy.UpdatedAt = time.Now().UTC()
		if err := tx.SaveWorkingCopy(workingCopy, nextCopy); err != nil {
			return nil, 0, err
		}
		return next, 201, nil
	})
}

func normalizeCitationUsages(values []CitationUsage, sourceIDs []string) ([]CitationUsage, error) {
	if values == nil {
		return []CitationUsage{}, nil
	}
	allowed := make(map[string]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		allowed[id] = struct{}{}
	}
	result := make([]CitationUsage, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.SourceID = strings.TrimSpace(value.SourceID)
		value.Purpose = strings.TrimSpace(value.Purpose)
		value.Limitation = strings.TrimSpace(value.Limitation)
		if value.SourceID == "" || len([]rune(value.Purpose)) > 2000 || len([]rune(value.Limitation)) > 2000 {
			return nil, ErrInvalidRequest
		}
		if _, ok := allowed[value.SourceID]; !ok {
			return nil, ErrInvalidRequest
		}
		if _, ok := seen[value.SourceID]; ok {
			return nil, ErrInvalidRequest
		}
		seen[value.SourceID] = struct{}{}
		result = append(result, value)
	}
	result = completeCitationUsages(result, sourceIDs)
	slices.SortFunc(result, func(a, b CitationUsage) int { return strings.Compare(a.SourceID, b.SourceID) })
	return result, nil
}

func completeCitationUsages(values []CitationUsage, sourceIDs []string) []CitationUsage {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value.SourceID] = struct{}{}
	}
	for _, sourceID := range sourceIDs {
		if _, ok := seen[sourceID]; ok {
			continue
		}
		values = append(values, CitationUsage{SourceID: sourceID})
	}
	return values
}
func sameVersion(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (s *Service) GenerationContext(ctx context.Context, actor Actor, projectID, chapterID string) (GenerationContext, error) {
	var result GenerationContext
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, actor, projectID, "read")
		if err != nil {
			return err
		}
		if p.DiscardedAt != nil {
			return ErrInvalidState
		}
		chapter, err := tx.Chapter(projectID, chapterID)
		if err != nil {
			return err
		}
		result = GenerationContext{ProjectID: p.ID, ProjectVersion: p.ProjectVersion, SpecRevision: p.SpecRevision, Spec: p.Spec, TemplateID: p.TemplateID, TemplateVersion: p.TemplateVersion, ChapterID: chapter.ID, ChapterVersionID: chapter.CurrentVersionID, ChapterBody: chapter.BodyMarkdown, Chapter: chapter}
		return nil
	})
	return result, err
}
