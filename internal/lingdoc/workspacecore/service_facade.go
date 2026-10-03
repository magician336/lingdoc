package workspacecore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"maps"
	"regexp"
	"slices"
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

// ProjectAuthorizer is the application policy seam for LingDoc project
// access. It operates on the existing Transaction port so authorization and
// idempotent writes share one repository transaction; it does not depend on
// GORM or define a second identity/tenant model.
type ProjectAuthorizer interface {
	AuthorizeTenant(Transaction, Actor, string) error
	AuthorizeProject(Transaction, Actor, string, string) (Project, error)
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

// NewServiceWithAuthorizer injects the project policy while keeping the
// repository/transaction architecture as the storage boundary. A nil policy
// uses the compatibility policy implemented by this package.
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

func legacyAuthorizeTenant(tx Transaction, actor Actor) error {
	if !validActor(actor) {
		return ErrNotFound
	}
	active, err := tx.ActiveMember(actor)
	if err != nil || !active {
		return ErrNotFound
	}
	return nil
}

func legacyAuthorizeProject(tx Transaction, actor Actor, projectID, capability string) (Project, error) {
	if err := legacyAuthorizeTenant(tx, actor); err != nil {
		return Project{}, err
	}
	baseCapability := capability
	if before, _, ok := strings.Cut(capability, ":"); ok {
		baseCapability = before
	}
	p, err := tx.Project(actor.TenantID, projectID)
	if err != nil {
		return Project{}, err
	}
	for _, member := range p.Members {
		if member.UserID != actor.UserID || (member.Status != "" && member.Status != "active") {
			continue
		}
		if baseCapability == "read" || member.Role == "owner" {
			return p, nil
		}
		if baseCapability == "write" {
			if member.Role == "owner" || member.Role == "collaborator" {
				return p, nil
			}
		}
		if baseCapability == "manage" && member.Role == "owner" {
			return p, nil
		}
	}
	return Project{}, ErrNotFound
}

func (s *Service) recordAudit(ctx context.Context, actor Actor, projectID, capability string, authErr error) error {
	return s.recordAuditEvent(ctx, AuditEvent{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role, ProjectID: projectID, Capability: capability, Decision: auditDecision(authErr), Reason: auditReason(authErr)})
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
func validActor(actor Actor) bool { return actor.TenantID != 0 && actor.UserID != "" }

type transactionProjectAuthorizer struct{}

func (transactionProjectAuthorizer) AuthorizeTenant(tx Transaction, actor Actor, capability string) error {
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
	if actor.Role != "" {
		if !actor.Role.IsValid() {
			return ErrNotFound
		}
		baseCapability := capability
		if before, _, ok := strings.Cut(capability, ":"); ok {
			baseCapability = before
		}
		required := map[string]types.TenantRole{
			"read": types.TenantRoleViewer, "create": types.TenantRoleContributor,
			"write": types.TenantRoleContributor, "manage": types.TenantRoleAdmin,
		}[baseCapability]
		if required == "" {
			return ErrInvalidRequest
		}
		if !actor.Role.HasPermission(required) {
			return ErrNo