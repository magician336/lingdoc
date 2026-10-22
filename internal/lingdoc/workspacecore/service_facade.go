package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Repository owns atomic commit/rollback and database isolation. Callbacks see
// only domain values. ReadOnly and RepeatableRead request a stable snapshot;
// RepeatableRead allows audited reads to append their authorization event.
// RetryLocks allows bounded retries of rolled-back spec transactions, never
// partial writes.
type Repository interface {
	Transaction(context.Context, TransactionOptions, func(Transaction) error) error
}
type TransactionOptions struct {
	ReadOnly, RetryLocks bool
	// RepeatableRead keeps a writable consistent snapshot so authorization audits
	// can commit atomically with the decision; it is not a read-only transaction.
	RepeatableRead bool
}

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

type OperationIdentity struct {
	Actor                  Actor
	Operation, Target, Key string
}
type OperationResult struct {
	BodyHash string
	Body     json.RawMessage
	Status   int
	Output   operationOutputKind
}

// SaveSpecResult is the transport-neutral outcome of saving a project spec.
// The application boundary exposes the domain value and whether the value was
// replayed; HTTP status codes, response envelopes, and serialized bytes remain
// transport concerns. The legacy SaveSpec method below is retained as the
// compatibility adapter for existing transports during the incremental
// migration.
type SaveSpecResult struct {
	Project  Project
	Replayed bool
}

// CreateProjectResult is the transport-neutral outcome of creating a project.
// The HTTP adapter owns the established 201 status and response envelope.
type CreateProjectResult struct {
	Project  Project
	Replayed bool
}

// ProjectMutationResult is the transport-neutral outcome shared by project
// and membership mutations. The HTTP adapter supplies the operation's status.
type ProjectMutationResult struct {
	Project  Project
	Replayed bool
}

// OwnerTransferResult is the transport-neutral outcome of an owner transfer
// request or acceptance.
type OwnerTransferResult struct {
	Transfer OwnerTransfer
	Replayed bool
}

type ChapterMutationResult struct {
	Chapter  Chapter
	Replayed bool
}

type WorkingCopyMutationResult struct {
	WorkingCopy WorkingCopy
	Replayed    bool
}

type CommittedChapterMutationResult struct {
	Version  CommittedChapterVersion
	Replayed bool
}

type ChangeSetMutationResult struct {
	ChangeSet ChangeSet
	Replayed  bool
}

type DraftCandidateMutationResult struct {
	Candidate DraftCandidate
	Replayed  bool
}

type ValidationIssueDispositionResult struct {
	Disposition ValidationIssueDisposition
	Replayed    bool
}

type ValidationIssueDispositionReplayResult struct {
	Disposition ValidationIssueDisposition
	Replayed    bool
	Found       bool
}

// decodeMutationPayload centralizes the JSON boundary for typed mutation
// results while keeping the domain label visible to callers. Persisted
// operation bodies are transport-shaped bytes, so this is the only place
// where these typed mutation results should decode them.
func decodeMutationPayload[T any](raw json.RawMessage, label string) (T, error) {
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("decode %s: %w", label, err)
	}
	return value, nil
}

// adaptMutationResult owns the repeated raw-operation to typed-result
// conversion. The operation remains responsible for authorization,
// idempotency, transactions, and replay ordering; this adapter only carries
// the serialized value and replay bit across the ApplicationService seam.
func adaptMutationResult[T any](invoke func() (json.RawMessage, int, bool, error), label string) (T, bool, error) {
	raw, _, replayed, err := invoke()
	if err != nil {
		var zero T
		return zero, replayed, err
	}
	value, err := decodeMutationPayload[T](raw, label)
	if err != nil {
		var zero T
		return zero, replayed, err
	}
	return value, replayed, nil
}

// markReplayPayloadDecodeError marks a persisted replay body that could not be
// decoded during source-sensitive preflight. The operation result must expose
// this as a replay attempt while ordinary authorization, version and storage
// errors remain non-replayed.
//
// The marker itself belongs to the shared pipeline, because the pipeline is
// what has to recognise it. The workspace marks and recognises the same
// marker, so a body that failed to decode reads as a replay here and there.
func markReplayPayloadDecodeError(err error) error {
	return operationcore.MarkReplayPayloadDecodeError(err)
}

func isReplayPayloadDecodeError(err error) bool {
	return operationcore.IsReplayPayloadDecodeError(err)
}

// Service owns validation, permissions, version expectations and replay order.
// Swapping storage cannot swap or omit these rules.
type Service struct {
	repository      Repository
	templates       TemplateReader
	ruleSets        RuleSetSnapshotReader
	sources         SourcePolicy
	authorizer      AuthorizationPolicy
	audit           AuditSink
	mode            AuthorizationMode
	catalog         TemplateCatalogResolver
	agentRuntime    AgentRuntime
	impactPreflight ImpactPreflightChecker
	impactRules     ImpactRuleChecker
	runtimeRecovery RuntimeRecoveryChecker
	// impactTasks and businessAudit are the narrow writer ports of the
	// change-set apply transaction. They are nil in the default composition,
	// in which case the writes go through the caller's transaction exactly as
	// before. The composition root may substitute a controlled failure
	// implementation to exercise the atomicity boundary (#256).
	impactTasks           ImpactTaskWriter
	businessAudit         BusinessAuditWriter
	requireActivationRead bool
	runtimeMode           string
}

// RuleSetSnapshotReader reads an exact tenant-scoped RuleSet version. The
// returned value must be an immutable snapshot with server-verified hashes.
type RuleSetSnapshotReader interface {
	GetVersion(context.Context, Actor, string, string) (lingdoctemplate.RuleSet, error)
}

// TransactionalRuleSetSnapshotReader revalidates and locks the exact RuleSet
// version using the same storage transaction that persists its project copy.
// Production adapters implement this to serialize binding against withdraw or
// recall transitions. Binding fails closed unless the configured reader has
// this capability; a detached read cannot authorize a new binding safely.
type TransactionalRuleSetSnapshotReader interface {
	GetVersionInTransaction(context.Context, Transaction, Actor, string, string) (lingdoctemplate.RuleSet, error)
}

// WithRuleSetSnapshotReader connects project-copy binding to the independent
// RuleSet store. It is configured once at the composition root.
func (s *Service) WithRuleSetSnapshotReader(reader RuleSetSnapshotReader) *Service {
	if s != nil {
		s.ruleSets = reader
	}
	return s
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
	var policy AuthorizationPolicy
	if authorizer != nil {
		policy = legacyProjectAuthorizerAdapter{legacy: authorizer}
	} else if mode == AuthorizationModeRollback {
		// Rollback is an explicit compatibility boundary. The service still
		// calls one AuthorizationPolicy seam; only this adapter translates the
		// structured request back to the legacy capability decision.
		policy = legacyAuthorizationPolicy{}
	}
	return NewServiceWithAuthorizationPolicy(repository, reader, sources, policy, audit, mode)
}

// NewServiceWithAuthorizationPolicy is the structured constructor used by
// Workspace internals and new adapters. New policy implementations can be
// introduced without making legacy capability strings part of their API.
func NewServiceWithAuthorizationPolicy(repository Repository, reader TemplateReader, sources SourcePolicy, policy AuthorizationPolicy, audit AuditSink, mode AuthorizationMode) *Service {
	if reader == nil {
		reader = ContractDemoTemplate{}
	}
	if policy == nil {
		policy = transactionProjectAuthorizer{}
	}
	if mode != AuthorizationModeLog && mode != AuthorizationModeRollback {
		mode = AuthorizationModeEnforce
	}
	return &Service{repository: repository, templates: reader, sources: sources, authorizer: policy, audit: audit, mode: mode}
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

func (s *Service) authorizeTenant(ctx context.Context, tx Transaction, request AuthorizationRequest) error {
	if !validAuthorizationAction(request.Action) {
		return ErrInvalidRequest
	}
	if err := authorizeAPIKeyProject(ctx, tx, request.ProjectID, request.capability()); err != nil {
		return err
	}
	err := s.authorizer.AuthorizeTenant(tx, request)
	if s.mode == AuthorizationModeLog {
		_ = s.recordAudit(ctx, request.Actor, request.ProjectID, "shadow:"+request.capability(), err)
	}
	return err
}
func (s *Service) authorizeProject(ctx context.Context, tx Transaction, request AuthorizationRequest) (Project, error) {
	if !validAuthorizationAction(request.Action) {
		return Project{}, ErrInvalidRequest
	}
	if err := authorizeAPIKeyProject(ctx, tx, request.ProjectID, request.capability()); err != nil {
		return Project{}, err
	}
	project, err := s.authorizer.AuthorizeProject(tx, request)
	if s.mode == AuthorizationModeLog {
		_ = s.recordAudit(ctx, request.Actor, request.ProjectID, "shadow:"+request.capability(), err)
	}
	return project, err
}

type legacyAuthorizationPolicy struct{}

func (legacyAuthorizationPolicy) AuthorizeTenant(tx Transaction, request AuthorizationRequest) error {
	return legacyAuthorizeTenant(tx, request.Actor)
}
func (legacyAuthorizationPolicy) AuthorizeProject(tx Transaction, request AuthorizationRequest) (Project, error) {
	return legacyAuthorizeProject(tx, request.Actor, request.ProjectID, request.capability())
}

type transactionProjectAuthorizer struct{}

func (transactionProjectAuthorizer) AuthorizeTenant(tx Transaction, request AuthorizationRequest) error {
	return authorizeCurrentTenant(tx, request)
}
func (transactionProjectAuthorizer) AuthorizeProject(tx Transaction, request AuthorizationRequest) (Project, error) {
	return authorizeCurrentProject(tx, request)
}
func (s *Service) recordAudit(ctx context.Context, actor Actor, projectID, capability string, authErr error) error {
	return s.recordAuditEvent(ctx, AuditEvent{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role, ProjectID: projectID, Capability: capability, Decision: auditDecision(authErr), Reason: auditReason(authErr)})
}

// auditAuthorizationAction returns the bounded category string for an
// authorization audit row.
//
// `lingdoc_project_audits.action` is a **category** column: 64 characters on
// PostgreSQL (`varchar(64)`, and `size:64` in the GORM model). The capability
// string is "action" or "action:resource", and the resource is caller-supplied
// and unbounded, so writing the composite into that column overflowed it.
// "authorization.issue-dismiss-blocking:<36-char uuid>" is 73 characters and
// PostgreSQL rejected it with SQLSTATE 22001; the write shares the operation
// transaction, so the whole operation failed and the raw database error
// reached the transport unmapped — an HTTP 500 on a valid request.
//
// The resource stays where it belongs: the full capability is kept in
// `details.capability` (JSON, unbounded) and on the event itself, and the
// action column keeps the category.
func auditAuthorizationAction(capability string) string {
	action, _, _ := strings.Cut(capability, ":")
	return "authorization." + action
}

// auditWriteFailed states its own client presentation. The operation did not
// take effect because its audit record could not be written, which is a
// storage failure rather than a business decision: the caller's request was
// valid and nothing changed. It carries its own code so the transport mapper
// stays a backstop (ADR 0003) instead of growing a branch per failure.
//
// The presentation is declared with clienterror, which is the one mechanism
// the transport renders. A presentation declared on a private interface here
// would be invisible to it: the failure would fall back to the generic 500
// and lose the code that names the cause, turning a known storage failure
// into an unexplained one.
func auditWriteFailed(cause error) error {
	return clienterror.Wrap(
		clienterror.Form{
			Status: 500, Code: "audit_write_failed",
			Message: "审计记录写入失败，本次操作未生效，请稍后重试。",
		},
		"audit_write_failed: "+cause.Error(), cause)
}

// recordAuditInTransaction keeps the audit write on the caller's transaction.
// SQLite uses a single pooled connection; opening the audit store separately
// while a write transaction is active deadlocks the request until its context
// expires. PostgreSQL still benefits from the same atomic audit boundary.
func (s *Service) recordAuditInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID, capability string, authErr error) error {
	// The GORM-backed lingdoc_project_audits.project_id is a required foreign
	// key. Tenant-level authorization (for example, createProject and
	// ListProjects) has no project row to reference yet, so skip that database
	// event. In-memory repositories still receive the event so shadow/log-mode
	// authorization behavior remains observable in tests and non-SQLite callers.
	if projectID == "" {
		if _, isGORM := tx.(gormTransaction); isGORM {
			return nil
		}
	}
	if s.audit == nil {
		return nil
	}
	event := AuditEvent{
		ID: uuid.NewString(), ActorID: actor.UserID, Action: auditAuthorizationAction(capability),
		Target: projectID, CreatedAt: time.Now().UTC(),
		Details: map[string]any{"tenant_id": actor.TenantID, "role": string(actor.Role),
			"capability": capability, "decision": auditDecision(authErr), "reason": auditReason(authErr)},
		TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role,
		ProjectID: projectID, Capability: capability,
		Decision: auditDecision(authErr), Reason: auditReason(authErr),
	}
	var err error
	if _, isGORM := tx.(gormTransaction); isGORM {
		audit, ok := tx.(auditTransaction)
		if !ok {
			return nil
		}
		err = audit.RecordAudit(event)
	} else {
		err = s.recordAudit(ctx, actor, projectID, capability, authErr)
	}
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"audit_event": "lingdoc_authorization",
			"tenant_id":   actor.TenantID,
			"project_id":  projectID,
			"capability":  capability,
			"decision":    auditDecision(authErr),
		})
	}
	return err
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
	if err := s.audit.Record(ctx, event); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"audit_event": "lingdoc",
			"tenant_id":   event.TenantID,
			"project_id":  event.ProjectID,
			"capability":  event.Capability,
			"decision":    event.Decision,
		})
		return err
	}
	return nil
}
func (s *Service) Authorize(ctx context.Context, actor Actor, projectID, capability string) error {
	request, err := authorizationRequestFromCapability(actor, projectID, capability)
	if err != nil {
		return err
	}
	// Authorization checks append their decision to the project audit log in
	// this transaction. Keep the decision and audit on one transaction, but do
	// not mark it read-only: PostgreSQL correctly rejects writes in a read-only
	// transaction even when SQLite permits them.
	return s.repository.Transaction(ctx, TransactionOptions{RepeatableRead: true}, func(tx Transaction) error {
		_, err := s.authorizeProject(ctx, tx, request)
		_ = s.recordAuditInTransaction(ctx, tx, actor, projectID, request.capability(), err)
		return err
	})
}
func (s *Service) GetProject(ctx context.Context, actor Actor, id string) (Project, error) {
	var result Project
	// GetProject also records the authorization decision in the same
	// transaction, so PostgreSQL requires a writable transaction here.
	err := s.repository.Transaction(ctx, TransactionOptions{RepeatableRead: true}, func(tx Transaction) (err error) {
		request := authorizationRequest(actor, id, AuthorizationRead, "")
		result, err = s.authorizeProject(ctx, tx, request)
		_ = s.recordAuditInTransaction(ctx, tx, actor, id, request.capability(), err)
		if err == nil && result.DiscardedAt != nil {
			result.Spec = map[string]string{}
			result.SpecFields = map[string]SpecField{}
		}
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return s.projectProvenanceProjection(ctx, actor, result)
}
func (s *Service) ListProjects(ctx context.Context, actor Actor) ([]Project, bool, error) {
	var result []Project
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if !validActor(actor) {
			return ErrNotFound
		}
		if err := s.authorizeTenant(ctx, tx, authorizationRequest(actor, "", AuthorizationRead, "")); err != nil {
			_ = s.recordAuditInTransaction(ctx, tx, actor, "", "read", err)
			return err
		}
		_ = s.recordAuditInTransaction(ctx, tx, actor, "", "read", nil)
		projects, err := tx.Projects(actor, 51)
		if err != nil {
			return err
		}
		result = []Project{}
		for _, p := range projects {
			visible, e := s.authorizeProject(ctx, tx, authorizationRequest(actor, p.ID, AuthorizationRead, ""))
			if e == nil && visible.DiscardedAt == nil {
				result = append(result, visible)
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	truncated := len(result) > 50
	if truncated {
		result = result[:50]
	}
	for i, project := range result {
		visible, err := s.projectProvenanceProjection(ctx, actor, project)
		if err != nil {
			return nil, false, err
		}
		result[i] = visible
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
	input := projectVersionInput{ExpectedProjectVersion: expectedProjectVersion}
	return runOperation(s, ctx, actor, operationInput(operationDiscardProject, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[ProjectMutationTransaction], func(tx ProjectMutationTransaction, p Project) (operationResult[Project], error) {
		if p.ProjectVersion != expectedProjectVersion {
			return operationResult[Project]{}, ErrVersionConflict
		}
		if p.DiscardedAt != nil || p.Status != "draft" {
			return operationResult[Project]{}, ErrInvalidState
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
			return operationResult[Project]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.discard", Target: projectID, Details: map[string]any{"project_version": next.ProjectVersion}, CreatedAt: now}); err != nil {
				return operationResult[Project]{}, err
			}
		}
		return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
	})
}

// DiscardProjectResult exposes the discarded project without transport fields.
func (s *Service) DiscardProjectResult(ctx context.Context, actor Actor, projectID, key string, expectedProjectVersion int64) (ProjectMutationResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.DiscardProject(ctx, actor, projectID, key, expectedProjectVersion)
	}, "project mutation result")
	if err != nil {
		return ProjectMutationResult{Replayed: replayed}, err
	}
	return ProjectMutationResult{Project: project, Replayed: replayed}, nil
}

func (s *Service) RestoreProject(ctx context.Context, actor Actor, projectID, key string, expectedProjectVersion int64) (json.RawMessage, int, bool, error) {
	if expectedProjectVersion < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	input := projectVersionInput{ExpectedProjectVersion: expectedProjectVersion}
	return runOperation(s, ctx, actor, operationInput(operationRestoreProject, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationRead, "")), mutationPort[ProjectMutationTransaction], func(tx ProjectMutationTransaction, p Project) (operationResult[Project], error) {
		if !projectOwner(p, actor.UserID) {
			return operationResult[Project]{}, ErrNotFound
		}
		if p.DiscardedAt == nil {
			return operationResult[Project]{}, ErrInvalidState
		}
		if p.ProjectVersion != expectedProjectVersion {
			return operationResult[Project]{}, ErrVersionConflict
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
			return operationResult[Project]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.restore", Target: projectID, Details: map[string]any{"project_version": next.ProjectVersion}, CreatedAt: time.Now().UTC()}); err != nil {
				return operationResult[Project]{}, err
			}
		}
		return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
	})
}

// RestoreProjectResult exposes the restored project without transport fields.
func (s *Service) RestoreProjectResult(ctx context.Context, actor Actor, projectID, key string, expectedProjectVersion int64) (ProjectMutationResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.RestoreProject(ctx, actor, projectID, key, expectedProjectVersion)
	}, "project mutation result")
	if err != nil {
		return ProjectMutationResult{Replayed: replayed}, err
	}
	return ProjectMutationResult{Project: project, Replayed: replayed}, nil
}

// Current authorization precedes replay; only new operations check expected
// versions. Exact response and domain mutation commit together.
//
// The pipeline that enforces this belongs to `operationcore`. This function
// declares what the operation is and fills the seams the pipeline runs on; it
// decides nothing about the order of the steps.
func runOperation[T any, I any, R any](s *Service, ctx context.Context, actor Actor, operation operationDescriptor[I], port func(Transaction) (T, error), write func(T, Project) (operationResult[R], error)) (json.RawMessage, int, bool, error) {
	return runOperationThroughPipeline(s, ctx, actor, operation, port, write)
}

// operationSourceIDs resolves exactly the references checked for a new write or
// stored replay. It only reads through the caller transaction.
func saveChapterSourceIDs(_ ChapterWorkspaceTransaction, operation operationDescriptor[SaveChapterInput], _ OperationResult, _ bool) ([]string, error) {
	return slices.Clone(operation.Input.SourceIDs), nil
}

func saveWorkingCopySourceIDs(_ ChapterWorkspaceTransaction, operation operationDescriptor[SaveWorkingCopyInput], _ OperationResult, _ bool) ([]string, error) {
	return slices.Clone(operation.Input.SourceIDs), nil
}

func applyRewriteSourceIDs(_ ChapterWorkspaceTransaction, operation operationDescriptor[ApplyRewriteInput], previous OperationResult, found bool) ([]string, error) {
	if found {
		response, err := decodeMutationPayload[WorkingCopy](previous.Body, "working copy mutation result")
		if err != nil {
			return nil, markReplayPayloadDecodeError(err)
		}
		return slices.Clone(response.SourceIDs), nil
	}
	return slices.Clone(operation.Input.WorkingCopy.SourceIDs), nil
}

func commitWorkingCopySourceIDs(tx ChapterWorkspaceTransaction, operation operationDescriptor[CommitWorkingCopyInput], previous OperationResult, found bool) ([]string, error) {
	if found {
		response, err := decodeMutationPayload[CommittedChapterVersion](previous.Body, "committed chapter result")
		if err != nil {
			return nil, markReplayPayloadDecodeError(err)
		}
		return slices.Clone(response.SourceIDs), nil
	}
	chapterID := strings.TrimPrefix(operation.Target, operation.Request.ProjectID+"/")
	workingCopy, err := tx.WorkingCopy(operation.Request.ProjectID, chapterID)
	if err != nil {
		return nil, err
	}
	return slices.Clone(workingCopy.SourceIDs), nil
}

func restoreWorkingCopySourceIDs(tx ChapterWorkspaceTransaction, operation operationDescriptor[RestoreWorkingCopyInput], previous OperationResult, found bool) ([]string, error) {
	if found {
		response, err := decodeMutationPayload[WorkingCopy](previous.Body, "working copy mutation result")
		if err != nil {
			return nil, markReplayPayloadDecodeError(err)
		}
		return slices.Clone(response.SourceIDs), nil
	}
	chapterID := strings.TrimPrefix(operation.Target, operation.Request.ProjectID+"/")
	version, err := tx.ChapterVersion(operation.Request.ProjectID, chapterID, operation.Input.ChapterVersionID)
	if err != nil {
		return nil, err
	}
	return slices.Clone(version.SourceIDs), nil
}

// operationSourceIDs resolves exactly the references checked for a new write or
// stored replay. It only reads through the caller transaction.
func operationSourceIDs[I any](tx ChapterWorkspaceTransaction, operation operationDescriptor[I], previous OperationResult, found bool) ([]string, error) {
	if operation.SourceIDs == nil {
		return nil, nil
	}
	return operation.SourceIDs(tx, operation, previous, found)
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
	ActivationReadID       string `json:"activation_read_id,omitempty"`
	ExpectedSpecRevision   int64  `json:"expected_spec_revision"`
	ExpectedProjectVersion int64  `json:"expected_project_version,omitempty"`
	ReviewedProjectVersion int64  `json:"reviewed_project_version,omitempty"`
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

type BindTemplateCopyRuleSetInput struct {
	ExpectedProjectVersion      int64                                 `json:"expected_project_version"`
	ExpectedTemplateCopyVersion int64                                 `json:"expected_template_copy_version"`
	RuleSetRef                  *lingdoctemplate.RuleSetReference     `json:"ruleset_ref,omitempty"`
	RuleSetID                   string                                `json:"ruleset_id"`
	RuleSetVersion              string                                `json:"ruleset_version"`
	Overrides                   []lingdoctemplate.RuleBindingOverride `json:"overrides,omitempty"`
}

func projectTemplateCopy(actor Actor, projectID string, version int64, source lingdoctemplate.Template, status string) (*ProjectTemplateCopy, error) {
	if source.RuleSetSnapshot != nil || source.RuleSetRef != nil {
		if _, err := lingdoctemplate.VerifyRuleSetBinding(source); err != nil {
			return nil, ErrInvalidRequest
		}
	}
	definition, err := lingdoctemplate.WithHashes(source)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	return &ProjectTemplateCopy{
		ID: uuid.NewString(), ProjectID: projectID, SourceTemplateID: source.ID,
		SourceTemplateVersion: source.Version, Version: version, Status: status,
		ContentHash: definition.ContentHash, RulesetHash: definition.RulesetHash,
		TemplateVersionRuleSetRef: lingdoctemplate.RuleSetReferenceFor(definition),
		Definition:                definition, CreatedBy: actor.UserID, CreatedAt: time.Now().UTC(),
	}, nil
}

func validateProjectTemplateCopy(copy *ProjectTemplateCopy) error {
	if copy == nil || copy.ID == "" || copy.ProjectID == "" || copy.Version < 1 || copy.SourceTemplateID == "" || copy.SourceTemplateVersion == "" {
		return ErrInvalidState
	}
	if copy.SourcePolicyHash != "" || len(copy.BindingPolicies) != 0 {
		hash, err := governance.HashBindingPolicies(copy.BindingPolicies)
		if err != nil || hash != copy.SourcePolicyHash {
			return ErrInvalidState
		}
	}
	contentHash, rulesetHash, err := lingdoctemplate.Hashes(copy.Definition)
	if err != nil {
		return ErrInvalidState
	}
	compiledHash := rulesetHash
	canonicalRef := lingdoctemplate.RuleSetReferenceFor(copy.Definition)
	canonical := copy.Definition.RuleSetSnapshot != nil || copy.Definition.RuleSetRef != nil
	if canonical {
		// A canonical template hash covers the bound rules and overrides. The
		// optional legacy sidecar identifies the immutable source RuleSet; its
		// source hash must never replace the effective template hash.
		snapshot := copy.Definition.RuleSetSnapshot
		if snapshot == nil || canonicalRef == nil || canonicalRef.ID != snapshot.ID ||
			canonicalRef.Version != snapshot.Version || canonicalRef.Hash != snapshot.RulesetHash {
			return ErrInvalidState
		}
		if copy.RuleSetSnapshot != nil && (lingdoctemplate.ValidateRuleSet(*copy.RuleSetSnapshot) != nil ||
			copy.RuleSetSnapshot.ID != canonicalRef.ID || copy.RuleSetSnapshot.Version != canonicalRef.Version ||
			copy.RuleSetSnapshot.RulesetHash != canonicalRef.Hash) {
			return ErrInvalidState
		}
		if copy.SourceRuleSetID != "" || copy.SourceRuleSetVersion != "" || copy.SourceRuleSetHash != "" {
			if copy.SourceRuleSetID != canonicalRef.ID || copy.SourceRuleSetVersion != canonicalRef.Version || copy.SourceRuleSetHash != canonicalRef.Hash {
				return ErrInvalidState
			}
		}
	} else if copy.RuleSetSnapshot != nil {
		if lingdoctemplate.ValidateRuleSet(*copy.RuleSetSnapshot) != nil {
			return ErrInvalidState
		}
		rulesetHash = copy.RuleSetSnapshot.RulesetHash
		// Legacy semantic edits derive a project-owned effective RuleSet while
		// retaining the original catalog source pin. These identities differ by
		// design; the validated effective snapshot still owns RulesetHash.
		if copy.SourceRuleSetID == "" || copy.SourceRuleSetVersion == "" || copy.SourceRuleSetHash == "" {
			return ErrInvalidState
		}
	} else if copy.SourceRuleSetID != "" || copy.SourceRuleSetVersion != "" || copy.SourceRuleSetHash != "" {
		return ErrInvalidState
	}
	if contentHash != copy.ContentHash || rulesetHash != copy.RulesetHash ||
		copy.Definition.ContentHash != copy.ContentHash || copy.Definition.RulesetHash != compiledHash ||
		copy.Definition.ID != copy.SourceTemplateID || copy.Definition.Version != copy.SourceTemplateVersion {
		return ErrInvalidState
	}
	if copy.Definition.RuleSetSnapshot != nil || copy.Definition.RuleSetRef != nil {
		if _, err := lingdoctemplate.VerifyRuleSetBinding(copy.Definition); err != nil {
			return ErrInvalidState
		}
	}
	if !reflect.DeepEqual(copy.TemplateVersionRuleSetRef, lingdoctemplate.RuleSetReferenceFor(copy.Definition)) {
		return ErrInvalidState
	}
	return nil
}

func editableTemplateDefinition(current lingdoctemplate.Template, input TemplateCopyDefinitionInput) (lingdoctemplate.Template, error) {
	if current.RuleSetSnapshot != nil && !reflect.DeepEqual(input.Rules, current.Rules) {
		return lingdoctemplate.Template{}, fmt.Errorf("%w: ruleset-bound copies cannot edit legacy rules independently", ErrInvalidRequest)
	}
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
	case "enum":
		return slices.Contains(field.Options, value)
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
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
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
	if err != nil {
		return TemplateMigrationPreview{}, err
	}
	return s.previewProvenanceProjection(ctx, actor, result)
}

func (s *Service) PreviewTemplateCopyEdit(ctx context.Context, actor Actor, projectID string, input TemplateCopyDefinitionInput) (TemplateMigrationPreview, error) {
	if input.ExpectedProjectVersion < 1 || input.ExpectedTemplateCopyVersion < 1 || input.Fields == nil || input.Sections == nil || input.Terms == nil || input.Rules == nil {
		return TemplateMigrationPreview{}, ErrInvalidRequest
	}
	var result TemplateMigrationPreview
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationManage, ""))
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
	if err != nil {
		return TemplateMigrationPreview{}, err
	}
	return s.previewProvenanceProjection(ctx, actor, result)
}

func (s *Service) SaveTemplateCopy(ctx context.Context, actor Actor, projectID, key string, input TemplateCopyDefinitionInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || input.ExpectedTemplateCopyVersion < 1 || input.Fields == nil || input.Sections == nil || input.Terms == nil || input.Rules == nil {
		return nil, 0, false, ErrInvalidRequest
	}
	return runOperation(s, ctx, actor, operationInput(operationSaveTemplateCopy, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[ProjectMutationTransaction], func(tx ProjectMutationTransaction, p Project) (operationResult[Project], error) {
		if p.Status != "draft" || p.DiscardedAt != nil || p.TemplateCopy == nil {
			return operationResult[Project]{}, ErrInvalidState
		}
		if p.ProjectVersion != input.ExpectedProjectVersion || p.TemplateCopyVersion != input.ExpectedTemplateCopyVersion {
			return operationResult[Project]{}, ErrVersionConflict
		}
		definition, err := editableTemplateDefinition(p.TemplateCopy.Definition, input)
		if err != nil {
			return operationResult[Project]{}, err
		}
		copy, err := projectTemplateCopy(actor, projectID, p.TemplateCopyVersion+1, definition, TemplateCopyDraft)
		if err != nil {
			return operationResult[Project]{}, err
		}
		if err := inheritRuleSetSourcePolicy(p.TemplateCopy, copy); err != nil {
			return operationResult[Project]{}, err
		}
		next := p
		next.TemplateCopyVersion, next.TemplateCopy = copy.Version, copy
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return operationResult[Project]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project_template.edit", Target: copy.ID, Details: map[string]any{"copy_version": copy.Version, "content_hash": copy.ContentHash, "ruleset_hash": copy.RulesetHash}, CreatedAt: copy.CreatedAt}); err != nil {
				return operationResult[Project]{}, err
			}
		}
		return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
	})
}

// BindTemplateCopyRuleSet appends a new project-copy version containing the
// exact published RuleSet snapshot. Reads are authorized before consulting the
// separate RuleSet store; the final project write rechecks authorization and
// version conditions in its own transaction.
func (s *Service) BindTemplateCopyRuleSet(ctx context.Context, actor Actor, projectID, key string, input BindTemplateCopyRuleSetInput) (json.RawMessage, int, bool, error) {
	if input.RuleSetRef != nil {
		ref := *input.RuleSetRef
		ref.ID, ref.Version = strings.TrimSpace(ref.ID), strings.TrimSpace(ref.Version)
		if (input.RuleSetID != "" && input.RuleSetID != ref.ID) ||
			(input.RuleSetVersion != "" && input.RuleSetVersion != ref.Version) ||
			(ref.Hash != "" && len(ref.Hash) != 64) {
			return nil, 0, false, ErrInvalidRequest
		}
		input.RuleSetRef = &ref
		input.RuleSetID, input.RuleSetVersion = ref.ID, ref.Version
	}
	input.RuleSetID = strings.TrimSpace(input.RuleSetID)
	input.RuleSetVersion = strings.TrimSpace(input.RuleSetVersion)
	if input.ExpectedProjectVersion < 1 || input.ExpectedTemplateCopyVersion < 1 || input.RuleSetID == "" || input.RuleSetVersion == "" || len(input.RuleSetID) > 64 || len(input.RuleSetVersion) > 64 {
		return nil, 0, false, ErrInvalidRequest
	}
	if !validActor(actor) || !operationcore.ValidKey(key) {
		return nil, 0, false, ErrInvalidRequest
	}
	if s.ruleSets == nil {
		return nil, 0, false, ErrInvalidState
	}

	return runOperation(s, ctx, actor,
		operationInput(operationBindTemplateCopyRuleSet, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")),
		mutationPort[ProjectMutationTransaction],
		func(tx ProjectMutationTransaction, p Project) (operationResult[Project], error) {
			audit, auditOK := tx.(auditTransaction)
			if !auditOK {
				return operationResult[Project]{}, ErrInvalidState
			}
			if p.Status != "draft" || p.DiscardedAt != nil || p.TemplateCopy == nil || p.TemplateCopy.Status != TemplateCopyDraft {
				return operationResult[Project]{}, ErrInvalidState
			}
			if p.ProjectVersion != input.ExpectedProjectVersion || p.TemplateCopyVersion != input.ExpectedTemplateCopyVersion {
				return operationResult[Project]{}, ErrVersionConflict
			}
			set, err := s.ruleSetVersionForBinding(ctx, tx, actor, input.RuleSetID, input.RuleSetVersion)
			if err != nil {
				return operationResult[Project]{}, err
			}
			if set.ID != input.RuleSetID || set.Version != input.RuleSetVersion {
				return operationResult[Project]{}, ErrNotFound
			}
			if err := lingdoctemplate.CanBindRuleSet(set); err != nil {
				if errors.Is(err, lingdoctemplate.ErrRuleSetNotPublished) {
					return operationResult[Project]{}, ErrRuleSetStateNotAllowed
				}
				return operationResult[Project]{}, ErrInvalidRequest
			}
			if input.RuleSetRef != nil && input.RuleSetRef.Hash != "" && input.RuleSetRef.Hash != set.RulesetHash {
				return operationResult[Project]{}, ErrVersionConflict
			}
			if templateRef := lingdoctemplate.RuleSetReferenceFor(p.TemplateCopy.Definition); templateRef != nil &&
				(templateRef.ID != set.ID || templateRef.Version != set.Version || templateRef.Hash != set.RulesetHash) {
				return operationResult[Project]{}, ErrInvalidRequest
			}
			if err := lingdoctemplate.ValidateRuleBindingOverrides(set, input.Overrides); err != nil {
				return operationResult[Project]{}, ErrInvalidRequest
			}
			definition := lingdoctemplate.Clone(p.TemplateCopy.Definition)
			definition.RuleSetSnapshot = lingdoctemplate.Clone(lingdoctemplate.Template{RuleSetSnapshot: &set}).RuleSetSnapshot
			definition.RuleSetRef = &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}
			definition.RuleBindingOverrides = lingdoctemplate.CloneRuleBindingOverrides(input.Overrides)
			definition.ContentHash, definition.RulesetHash = "", ""
			copy, err := projectTemplateCopy(actor, projectID, p.TemplateCopyVersion+1, definition, TemplateCopyDraft)
			if err != nil {
				return operationResult[Project]{}, err
			}
			if err := inheritRuleSetSourcePolicy(p.TemplateCopy, copy); err != nil {
				return operationResult[Project]{}, err
			}
			next := p
			next.TemplateCopyVersion, next.TemplateCopy = copy.Version, copy
			next.ProjectVersion++
			if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
				return operationResult[Project]{}, err
			}
			action := "project_template.bind_ruleset"
			if len(input.Overrides) > 0 {
				action = "project_template.override_ruleset"
			}
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: action, Target: copy.ID, Details: map[string]any{"copy_version": copy.Version, "ruleset_id": set.ID, "ruleset_version": set.Version, "ruleset_hash": set.RulesetHash, "content_hash": copy.ContentHash, "override_count": len(input.Overrides)}, CreatedAt: copy.CreatedAt}); err != nil {
				return operationResult[Project]{}, err
			}
			return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
		})
}

func (s *Service) ruleSetVersionForBinding(ctx context.Context, tx any, actor Actor, id, version string) (lingdoctemplate.RuleSet, error) {
	reader, ok := s.ruleSets.(TransactionalRuleSetSnapshotReader)
	if !ok || reader == nil {
		// Binding must share the RuleSet state check and project-copy write in
		// one transaction. Falling back to GetVersion can bind a version that
		// was withdrawn after the independent read completed.
		return lingdoctemplate.RuleSet{}, ErrInvalidState
	}
	transaction, ok := tx.(Transaction)
	if !ok {
		return lingdoctemplate.RuleSet{}, ErrInvalidState
	}
	return reader.GetVersionInTransaction(ctx, transaction, actor, id, version)
}

// SaveTemplateCopyResult exposes the saved project without transport fields.
func (s *Service) SaveTemplateCopyResult(ctx context.Context, actor Actor, projectID, key string, input TemplateCopyDefinitionInput) (ProjectMutationResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.SaveTemplateCopy(ctx, actor, projectID, key, input)
	}, "project mutation result")
	if err != nil {
		return ProjectMutationResult{Replayed: replayed}, err
	}
	return ProjectMutationResult{Project: project, Replayed: replayed}, nil
}

// BindTemplateCopyRuleSetResult exposes the saved project without transport fields.
func (s *Service) BindTemplateCopyRuleSetResult(ctx context.Context, actor Actor, projectID, key string, input BindTemplateCopyRuleSetInput) (ProjectMutationResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.BindTemplateCopyRuleSet(ctx, actor, projectID, key, input)
	}, "project mutation result")
	if err != nil {
		return ProjectMutationResult{Replayed: replayed}, err
	}
	return ProjectMutationResult{Project: project, Replayed: replayed}, nil
}

func (s *Service) GetTemplateCopy(ctx context.Context, actor Actor, projectID string, version int64) (ProjectTemplateCopy, error) {
	if version < 1 {
		return ProjectTemplateCopy{}, ErrInvalidRequest
	}
	var result ProjectTemplateCopy
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
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
	return runOperation(s, ctx, actor, operationInput(operationChangeTemplate, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[ProjectMutationTransaction], func(tx ProjectMutationTransaction, p Project) (operationResult[Project], error) {
		if p.Status != "draft" {
			return operationResult[Project]{}, ErrInvalidState
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return operationResult[Project]{}, ErrVersionConflict
		}
		preview := buildTemplateMigrationPreview(p, target, input.ExpectedProjectVersion)
		next := p
		next.TemplateID, next.TemplateVersion = target.ID, target.Version
		targetCopy.Version = p.TemplateCopyVersion + 1
		next.TemplateCopyVersion, next.TemplateCopy = targetCopy.Version, targetCopy
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return operationResult[Project]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project_template.change", Target: projectID, Details: map[string]any{"from": p.TemplateID + "/" + p.TemplateVersion, "to": target.ID + "/" + target.Version, "missing_required": preview.MissingRequired, "orphaned": preview.Orphaned}, CreatedAt: time.Now().UTC()}); err != nil {
				return operationResult[Project]{}, err
			}
		}
		return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
	})
}

// ChangeTemplateResult exposes the changed project without transport fields.
func (s *Service) ChangeTemplateResult(ctx context.Context, actor Actor, projectID, key string, input TemplateMigrationInput) (ProjectMutationResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.ChangeTemplate(ctx, actor, projectID, key, input)
	}, "project mutation result")
	if err != nil {
		return ProjectMutationResult{Replayed: replayed}, err
	}
	return ProjectMutationResult{Project: project, Replayed: replayed}, nil
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
	return runOperation(s, ctx, actor, operationInput(operationCreateProject, "projects", key, input, authorizationRequest(actor, "", AuthorizationCreate, "")), mutationPort[ProjectMutationTransaction], func(tx ProjectMutationTransaction, _ Project) (operationResult[Project], error) {
		featureTx, featureErr := extendedTransaction(tx)
		if featureErr != nil {
			return operationResult[Project]{}, featureErr
		}

		resolved, err := s.resolveTemplate(ctx, featureTx, actor, input.TemplateID, input.TemplateVersion)
		if err != nil {
			return operationResult[Project]{}, err
		}
		id := uuid.NewString()
		copy, err := makeCatalogTemplateCopy(actor, id, 1, resolved, TemplateCopyDraft)
		if err != nil {
			return operationResult[Project]{}, err
		}
		template := resolved.Definition
		p := Project{ID: id, Name: input.Name, Status: "draft", ProjectVersion: 1, CurrentContextRevision: 0, DeliveryStatus: "NOT_READY", Spec: map[string]string{}, TemplateID: template.ID, TemplateVersion: template.Version, TemplateCopyVersion: 1, TemplateCopy: copy, Members: []Member{{UserID: actor.UserID, Role: "owner"}}}
		if err := tx.InsertProject(actor.TenantID, p); err != nil {
			return operationResult[Project]{}, err
		}
		if s.requireActivationRead {
			if err := recordProjectAuditEvent(featureTx, actor, p.ID, "project.create", p.ID, "created"); err != nil {
				return operationResult[Project]{}, err
			}
		}
		return operationResult[Project]{Value: p, Status: operationStatusCreated}, nil
	})
}

// CreateProjectResult exposes the project value and replay state without
// leaking serialized response bytes or an HTTP status code to the caller.
func (s *Service) CreateProjectResult(ctx context.Context, actor Actor, key string, input CreateProjectInput) (CreateProjectResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.CreateProject(ctx, actor, key, input)
	}, "created project result")
	if err != nil {
		return CreateProjectResult{Replayed: replayed}, err
	}
	return CreateProjectResult{Project: project, Replayed: replayed}, nil
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
	prepared, err := s.prepareProvenanceOperation(ctx, actor, "saveSpec", projectID, projectID, input)
	if err != nil {
		return nil, 0, false, err
	}
	ctx = prepared
	return runOperation(s, ctx, actor, operationInput(operationSaveSpec, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationManage, "")), mutationPort[ProjectMutationTransaction], func(tx ProjectMutationTransaction, p Project) (operationResult[Project], error) {
		if p.Status != "draft" {
			return operationResult[Project]{}, ErrInvalidState
		}
		if p.SpecRevision != input.ExpectedSpecRevision {
			return operationResult[Project]{}, ErrVersionConflict
		}
		nextFields := maps.Clone(p.SpecFields)
		if nextFields == nil {
			nextFields = map[string]SpecField{}
		}
		changedFields := make(map[string]struct{})
		for key := range nextFields {
			if _, present := input.Fields[key]; !present {
				delete(nextFields, key)
			}
		}
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
					if field.Provenance != nil {
						proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
						if !ok || !proof.States[proof.Requests[provenanceRequestKey(field.Provenance, field.Value)].ID] {
							return operationResult[Project]{}, ErrSourceUnavailable
						}
					}
					provenance = nil
					origin = "human"
					status = "draft"
				}
			}
			if metadata, explicit := input.FieldMetadata[key]; explicit && field.Provenance != nil && metadata.Provenance == nil && (metadata.Origin == "" || metadata.Origin == "human") {
				proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
				if value == field.Value {
					origin, status, provenance = field.Origin, field.Status, field.Provenance
				} else {
					if !ok || !proof.States[proof.Requests[provenanceRequestKey(field.Provenance, field.Value)].ID] {
						return operationResult[Project]{}, ErrSourceUnavailable
					}
					origin, status, provenance = "human", "draft", nil
				}
			}
			if _, explicit := input.FieldMetadata[key]; explicit {
				provenance, origin, status = serverProvenance(ctx, provenance, value, origin, status)
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
				return operationResult[Project]{}, err
			}
			if audit, ok := tx.(auditTransaction); ok {
				if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project_spec.save", Target: projectID, Details: map[string]any{"spec_revision": next.SpecRevision, "field_metadata": sanitizedSpecAudit(next.SpecFields)}, CreatedAt: time.Now().UTC()}); err != nil {
					return operationResult[Project]{}, err
				}
			}
			p = next
		}
		return operationResult[Project]{Value: p, Status: operationStatusOK}, nil
	})
}

// SaveSpecResult runs the same application operation as SaveSpec and exposes
// its business result without leaking JSON or HTTP concerns to callers such as
// a CLI or background task. Idempotency authorization and replay checks remain
// owned by operation; this method is only the typed compatibility adapter.
func (s *Service) SaveSpecResult(ctx context.Context, actor Actor, projectID, key string, input SaveSpecInput) (SaveSpecResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.SaveSpec(ctx, actor, projectID, key, input)
	}, "saved spec result")
	if err != nil {
		return SaveSpecResult{Replayed: replayed}, err
	}
	return SaveSpecResult{Project: project, Replayed: replayed}, nil
}

func validateChangeSetInput(input CreateChangeSetInput) error {
	hasTemplateUpgrade := input.TemplateUpgrade != nil
	if input.ExpectedContextRevision < 1 || (!hasTemplateUpgrade && len(input.Fields) == 0) || (hasTemplateUpgrade && len(input.Fields) != 0) || (!hasTemplateUpgrade && len(input.AffectedChapterIDs) == 0) || strings.TrimSpace(input.Reason) == "" || len([]rune(input.Reason)) > 2000 {
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
		if strings.TrimSpace(key) == "" || len(key) > 100 {
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
		if upgrade.RuleSetRef != nil {
			ref := *upgrade.RuleSetRef
			ref.ID = strings.TrimSpace(ref.ID)
			ref.Version = strings.TrimSpace(ref.Version)
			if ref.ID == "" || ref.Version == "" || len(ref.ID) > 64 || len(ref.Version) > 64 || len(ref.Hash) != 64 {
				return ErrInvalidRequest
			}
		} else if upgrade.RuleBindingOverrides != nil {
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

// bindUpgradeRuleSet resolves the requested immutable reference inside the
// ChangeSet transaction. The assessed ChangeSet stores the exact snapshot;
// Apply rechecks that exact version's lifecycle in its own transaction before
// changing the active project copy.
func (s *Service) bindUpgradeRuleSet(ctx context.Context, tx any, actor Actor, current, definition lingdoctemplate.Template, input TemplateUpgradeInput) (lingdoctemplate.Template, error) {
	if input.RuleSetRef == nil {
		return definition, nil
	}
	ref := *input.RuleSetRef
	ref.ID, ref.Version = strings.TrimSpace(ref.ID), strings.TrimSpace(ref.Version)
	if len(ref.Hash) != 64 {
		return lingdoctemplate.Template{}, ErrInvalidRequest
	}
	set, err := s.ruleSetVersionForBinding(ctx, tx, actor, ref.ID, ref.Version)
	if err != nil {
		return lingdoctemplate.Template{}, err
	}
	if set.ID != ref.ID || set.Version != ref.Version {
		return lingdoctemplate.Template{}, ErrNotFound
	}
	if ref.Hash != "" && ref.Hash != set.RulesetHash {
		return lingdoctemplate.Template{}, ErrVersionConflict
	}
	if err := lingdoctemplate.CanBindRuleSet(set); err != nil {
		if errors.Is(err, lingdoctemplate.ErrRuleSetNotPublished) {
			return lingdoctemplate.Template{}, ErrRuleSetStateNotAllowed
		}
		return lingdoctemplate.Template{}, ErrInvalidRequest
	}
	overrides := input.RuleBindingOverrides
	currentRef := lingdoctemplate.RuleSetReferenceFor(current)
	if overrides == nil && currentRef != nil && currentRef.ID == set.ID && currentRef.Version == set.Version && currentRef.Hash == set.RulesetHash {
		overrides = current.RuleBindingOverrides
	}
	if err := lingdoctemplate.ValidateRuleBindingOverrides(set, overrides); err != nil {
		return lingdoctemplate.Template{}, ErrInvalidRequest
	}
	definition.RuleSetSnapshot = lingdoctemplate.Clone(lingdoctemplate.Template{RuleSetSnapshot: &set}).RuleSetSnapshot
	definition.RuleSetRef = &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}
	definition.RuleBindingOverrides = lingdoctemplate.CloneRuleBindingOverrides(overrides)
	definition.ContentHash, definition.RulesetHash = "", ""
	definition, err = lingdoctemplate.WithHashes(definition)
	if err != nil {
		return lingdoctemplate.Template{}, ErrInvalidRequest
	}
	return definition, nil
}

// verifyTemplateUpgradeEvidence checks the immutable RuleSet binding and the
// hashes persisted with a ChangeSet. The assessed payload is itself durable
// evidence; Apply must not trust a tampered preview or recompute a different
// effective identity while creating the next project copy.
func verifyTemplateUpgradeEvidence(upgrade *TemplateUpgrade) (lingdoctemplate.VerifiedRuleSetBinding, error) {
	if upgrade == nil || (upgrade.Definition.RuleSetSnapshot == nil && upgrade.Definition.RuleSetRef == nil) {
		return lingdoctemplate.VerifiedRuleSetBinding{}, nil
	}
	contentHash, effectiveHash, err := lingdoctemplate.Hashes(upgrade.Definition)
	if err != nil {
		return lingdoctemplate.VerifiedRuleSetBinding{}, fmt.Errorf("%w: invalid template upgrade RuleSet evidence: %v", ErrInvalidState, err)
	}
	if upgrade.Definition.ContentHash != contentHash || upgrade.Definition.RulesetHash != effectiveHash ||
		upgrade.Preview.TargetContentHash != contentHash || upgrade.Preview.TargetRulesetHash != effectiveHash {
		return lingdoctemplate.VerifiedRuleSetBinding{}, fmt.Errorf("%w: template upgrade evidence hash mismatch", ErrInvalidState)
	}
	evidence, err := lingdoctemplate.VerifyRuleSetBinding(upgrade.Definition)
	if err != nil {
		return lingdoctemplate.VerifiedRuleSetBinding{}, fmt.Errorf("%w: invalid template upgrade RuleSet evidence: %v", ErrInvalidState, err)
	}
	ref := lingdoctemplate.RuleSetReferenceFor(upgrade.Definition)
	if ref == nil || evidence.Reference != *ref || evidence.ContentHash != contentHash || evidence.EffectiveHash != effectiveHash {
		return lingdoctemplate.VerifiedRuleSetBinding{}, fmt.Errorf("%w: template upgrade RuleSet evidence identity mismatch", ErrInvalidState)
	}
	return evidence, nil
}

func (s *Service) recheckTemplateUpgradeRuleSet(ctx context.Context, tx any, actor Actor, upgrade *TemplateUpgrade) error {
	evidence, err := verifyTemplateUpgradeEvidence(upgrade)
	if err != nil {
		return err
	}
	if upgrade == nil {
		return nil
	}
	definition := upgrade.Definition
	ref := lingdoctemplate.RuleSetReferenceFor(definition)
	if ref == nil {
		return nil
	}
	if definition.RuleSetRef != nil && *definition.RuleSetRef != *ref {
		return ErrInvalidState
	}
	set, err := s.ruleSetVersionForBinding(ctx, tx, actor, ref.ID, ref.Version)
	if err != nil {
		return err
	}
	if set.ID != ref.ID || set.Version != ref.Version {
		return ErrNotFound
	}
	if set.RulesetHash != ref.Hash {
		return ErrVersionConflict
	}
	if evidence.Snapshot.ID != set.ID || evidence.Snapshot.Version != set.Version || evidence.Snapshot.RulesetHash != set.RulesetHash {
		return ErrVersionConflict
	}
	if err := lingdoctemplate.CanBindRuleSet(set); err != nil {
		if errors.Is(err, lingdoctemplate.ErrRuleSetNotPublished) {
			return ErrRuleSetStateNotAllowed
		}
		return fmt.Errorf("%w: current RuleSet snapshot is invalid", ErrInvalidState)
	}
	return nil
}

func templateUpgradeChangesRuleSetBinding(current *ProjectTemplateCopy, target lingdoctemplate.Template) bool {
	targetRef := lingdoctemplate.RuleSetReferenceFor(target)
	if targetRef == nil {
		return false
	}
	if current == nil {
		return true
	}
	currentRef := lingdoctemplate.RuleSetReferenceFor(current.Definition)
	return currentRef == nil || *currentRef != *targetRef ||
		!reflect.DeepEqual(current.Definition.RuleBindingOverrides, target.RuleBindingOverrides)
}

// templateUpgradeAuditDetails records the immutable migration identity in the
// JSON details column. AuditEvent.Reason is intentionally duplicated here:
// gormTransaction reconstructs events from details_json and does not persist
// the top-level Reason field.
func templateUpgradeAuditDetails(change ChangeSet, targetCopy *ProjectTemplateCopy) map[string]any {
	upgrade := change.TemplateUpgrade
	if upgrade == nil {
		return nil
	}
	details := map[string]any{
		"change_set_id":              change.ID,
		"reason":                     change.Reason,
		"base_copy_version":          upgrade.BaseTemplateCopyVersion,
		"base_template_copy_version": upgrade.BaseTemplateCopyVersion,
		"old_copy_version":           upgrade.BaseTemplateCopyVersion,
		"target_copy_version":        upgrade.Preview.TargetCopyVersion,
		"new_copy_version":           upgrade.Preview.TargetCopyVersion,
		"target_content_hash":        upgrade.Preview.TargetContentHash,
		"target_ruleset_hash":        upgrade.Preview.TargetRulesetHash,
	}
	if targetCopy != nil {
		details["target_copy_version"] = targetCopy.Version
		details["new_copy_version"] = targetCopy.Version
		details["content_hash"] = targetCopy.ContentHash
		details["ruleset_hash"] = targetCopy.RulesetHash
		details["effective_ruleset_hash"] = targetCopy.RulesetHash
	} else {
		details["content_hash"] = upgrade.Preview.TargetContentHash
		details["ruleset_hash"] = upgrade.Preview.TargetRulesetHash
		details["effective_ruleset_hash"] = upgrade.Preview.TargetRulesetHash
	}
	ref := lingdoctemplate.RuleSetReferenceFor(upgrade.Definition)
	if ref != nil {
		details["ruleset_id"] = ref.ID
		details["ruleset_version"] = ref.Version
		details["ruleset_source_hash"] = ref.Hash
		details["source_ruleset_hash"] = ref.Hash
	}
	overrides := lingdoctemplate.CloneRuleBindingOverrides(upgrade.Definition.RuleBindingOverrides)
	if overrides == nil {
		overrides = []lingdoctemplate.RuleBindingOverride{}
	}
	details["rule_binding_overrides"] = overrides
	details["overrides"] = overrides
	details["override_count"] = len(overrides)
	if len(overrides) > 0 {
		details["binding_action"] = "override_ruleset"
	} else if ref != nil {
		details["binding_action"] = "bind_ruleset"
	}
	return details
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
	return runOperation(s, ctx, actor, sourceOperationInput(operationCreateChangeSet, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, ""), impactSourceDecoder), mutationPort[ChangeSetMutationTransaction], s.createAssessedChangeMutation(ctx, actor, projectID, input))
}

// CreateChangeSetResult exposes the assessed change set without transport
// fields.
func (s *Service) CreateChangeSetResult(ctx context.Context, actor Actor, projectID, key string, input CreateChangeSetInput) (ChangeSetMutationResult, error) {
	changeSet, replayed, err := adaptMutationResult[ChangeSet](func() (json.RawMessage, int, bool, error) {
		return s.CreateChangeSet(ctx, actor, projectID, key, input)
	}, "change set mutation result")
	if err != nil {
		return ChangeSetMutationResult{Replayed: replayed}, err
	}
	return ChangeSetMutationResult{ChangeSet: changeSet, Replayed: replayed}, nil
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
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
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
	if err == nil {
		for i, change := range result {
			if accessErr := s.CheckSemanticChangeAccess(ctx, actor, projectID, change); accessErr != nil {
				return nil, accessErr
			}
			visible, accessErr := s.changeProvenanceProjection(ctx, actor, change)
			if accessErr != nil {
				return nil, accessErr
			}
			result[i] = visible
		}
	}
	return result, err
}

func (s *Service) GetChangeSet(ctx context.Context, actor Actor, projectID, changeSetID string) (ChangeSet, error) {
	var result ChangeSet
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
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
	if err == nil {
		err = s.CheckSemanticChangeAccess(ctx, actor, projectID, result)
	}
	if err != nil {
		return ChangeSet{}, err
	}
	return s.changeProvenanceProjection(ctx, actor, result)
}

// ownerFreezeRefusal refuses an Owner-only action while the project's Owner
// may not act.
//
// The refusal is the one the action has always answered with: it wraps
// ErrNotFound, so every caller that asks "is this the resource I cannot
// reach?" keeps its answer, and it names the freeze in the optional details,
// so a caller can also tell "the Owner's tenant identity lapsed" from "you
// are not the Owner".
//
// It stays silent when the action is not frozen: the ordinary authorization
// then answers with its own reason.
func ownerFreezeRefusal(tx Transaction, actor Actor, projectID string) error {
	p, err := tx.Project(actor.TenantID, projectID)
	if err != nil {
		// A project that is not there is the ordinary authorization's answer;
		// this gate has nothing to say about it.
		return nil
	}
	if JudgeOwnerAction(tx, p, actor).ReasonCode != ReasonOwnerUnavailable {
		return nil
	}
	return ErrOwnerUnavailable
}

// requireOwnerDecision refuses an Owner-only decision while the project's
// Owner may not take it.
//
// The refusal carries its reason, so a caller can tell the freeze from "you
// are not the Owner". It runs before the ordinary authorization, because the
// ordinary authorization answers a lapsed Owner with a plain "not found" --
// and that is exactly the answer the caller cannot act on.
//
// A caller who is not the Owner is left to the ordinary authorization: the
// freeze is not their reason for being refused.
func (s *Service) requireOwnerDecision(ctx context.Context, actor Actor, projectID string) error {
	return s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		return ownerFreezeRefusal(tx, actor, projectID)
	})
}

func (s *Service) ApplyChangeSet(ctx context.Context, actor Actor, projectID, changeSetID, key string) (json.RawMessage, int, bool, error) {
	if strings.TrimSpace(changeSetID) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	if err := RequireHumanDecision(ctx); err != nil {
		return nil, 0, false, err
	}
	if err := s.requireOwnerDecision(ctx, actor, projectID); err != nil {
		return nil, 0, false, err
	}
	if err := s.Authorize(ctx, actor, projectID, "manage"); err != nil {
		return nil, 0, false, err
	}
	changeInput := map[string]string{"change_set_id": changeSetID}
	prepared, err := s.prepareProvenanceOperation(ctx, actor, "applyChangeSet", projectID+"/"+changeSetID, projectID, changeInput)
	if err != nil {
		return nil, 0, false, err
	}
	ctx = prepared
	return runOperation(s, ctx, actor, ownerDecisionOperation(sourceOperationInput(operationApplyChangeSet, projectID+"/"+changeSetID, key, changeInput, authorizationRequest(actor, projectID, AuthorizationManage, ""), impactSourceDecoder)), mutationPort[ChangeSetMutationTransaction], func(tx ChangeSetMutationTransaction, p Project) (operationResult[ChangeSet], error) {
		featureTx, featureErr := extendedTransaction(tx)
		if featureErr != nil {
			return operationResult[ChangeSet]{}, featureErr
		}

		if p.Status != "active" || p.DiscardedAt != nil {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		if err := ownerDecisionInTransaction(featureTx, p, actor); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		change, err := store.ChangeSet(projectID, changeSetID)
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if change.Status == ChangeSetStatusApplied {
			return operationResult[ChangeSet]{Value: change, Status: operationStatusOK}, nil
		}
		if !ChangeSetStatusCanMove("apply", change.Status) {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		// A governed template migration must never commit without a durable
		// audit record. Check the transactional audit capability before any
		// stale-state or project-copy writes are attempted.
		if change.TemplateUpgrade != nil {
			if _, ok := tx.(auditTransaction); !ok {
				return operationResult[ChangeSet]{}, fmt.Errorf("%w: template upgrade apply requires transactional audit", ErrInvalidState)
			}
		}
		if change.TemplateUpgrade != nil {
			if err := s.recheckTemplateUpgradeRuleSet(ctx, tx, actor, change.TemplateUpgrade); err != nil {
				return operationResult[ChangeSet]{}, err
			}
		}
		upgradeStale := change.TemplateUpgrade != nil && (p.TemplateCopy == nil || p.TemplateCopyVersion != change.TemplateUpgrade.BaseTemplateCopyVersion)
		if change.BaseContextRevision != p.CurrentContextRevision || change.BaseSpecRevision != p.SpecRevision || upgradeStale {
			stale := change
			if !ChangeSetStatusCanMove("markStale", stale.Status) {
				return operationResult[ChangeSet]{}, ErrInvalidState
			}
			stale.Status = ChangeSetStatusStale
			stale.StaleReason = "context_revision_changed"
			if change.BaseSpecRevision != p.SpecRevision {
				stale.StaleReason = "spec_revision_changed"
			}
			if upgradeStale {
				stale.StaleReason = "template_copy_changed"
			}
			if err := store.UpdateChangeSet(change, stale); err != nil {
				return operationResult[ChangeSet]{}, err
			}
			if audit, ok := tx.(auditTransaction); ok {
				if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.stale", Target: change.ID, Details: map[string]any{"stale_reason": stale.StaleReason, "base_context_revision": change.BaseContextRevision, "current_context_revision": p.CurrentContextRevision, "template_copy_version": p.TemplateCopyVersion}, CreatedAt: time.Now().UTC()}); err != nil {
					return operationResult[ChangeSet]{}, err
				}
			}
			return operationResult[ChangeSet]{Value: stale, Status: operationStatusConflict}, nil
		}
		for _, impact := range change.Impacts {
			chapter, err := tx.Chapter(projectID, impact.ChapterID)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			if !sameVersion(impact.ChapterVersionID, chapter.CurrentVersionID) {
				stale := change
				if !ChangeSetStatusCanMove("markStale", stale.Status) {
					return operationResult[ChangeSet]{}, ErrInvalidState
				}
				stale.Status = ChangeSetStatusStale
				stale.StaleReason = "chapter_version_changed"
				if err := store.UpdateChangeSet(change, stale); err != nil {
					return operationResult[ChangeSet]{}, err
				}
				if audit, ok := tx.(auditTransaction); ok {
					if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.stale", Target: change.ID, Details: map[string]any{"stale_reason": stale.StaleReason, "chapter_id": impact.ChapterID, "chapter_version_id": impact.ChapterVersionID}, CreatedAt: time.Now().UTC()}); err != nil {
						return operationResult[ChangeSet]{}, err
					}
				}
				return operationResult[ChangeSet]{Value: stale, Status: operationStatusConflict}, nil
			}
		}
		if err := validateChangeTemplateFields(p, change.Fields); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if _, ok := tx.(impactTransaction); ok {
			if err := ValidateChangeImpactAssessment(featureTx, p, change); err != nil {
				return operationResult[ChangeSet]{}, err
			}
		}
		if err := ValidateSemanticDeltas(featureTx, p, change); err != nil {
			return operationResult[ChangeSet]{}, err
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
			metadata.Provenance = nil
			metadata.Status = "pending_confirmation"
			metadata.ModifiedBy = actor.UserID
			metadata.ModifiedAt = time.Now().UTC()
			next.SpecFields[field.Key] = metadata
		}
		if upgrade := change.TemplateUpgrade; upgrade != nil {
			if p.TemplateCopy == nil || p.TemplateCopyVersion != upgrade.BaseTemplateCopyVersion {
				return operationResult[ChangeSet]{}, ErrVersionConflict
			}
			migratedSpec, err := templateSpecAfterUpgrade(next.Spec, upgrade.Definition, upgrade.FieldValues)
			if err != nil {
				return operationResult[ChangeSet]{}, fmt.Errorf("%w: template migration is no longer valid", ErrInvalidState)
			}
			for fieldID, value := range migratedSpec {
				// An explicit empty value still materializes a previously absent field.
				previous, present := next.Spec[fieldID]
				if present && previous == value {
					continue
				}
				next.Spec[fieldID] = value
				changedSpecKeys[fieldID] = struct{}{}
				metadata := next.SpecFields[fieldID]
				metadata.Value = value
				metadata.Origin = "human"
				metadata.Provenance = nil
				metadata.Status = "pending_confirmation"
				metadata.ModifiedBy = actor.UserID
				metadata.ModifiedAt = time.Now().UTC()
				next.SpecFields[fieldID] = metadata
			}
			copy, err := projectTemplateCopy(actor, projectID, p.TemplateCopyVersion+1, upgrade.Definition, TemplateCopyBound)
			if err != nil {
				return operationResult[ChangeSet]{}, fmt.Errorf("%w: invalid template upgrade snapshot", ErrInvalidState)
			}
			policyBasis, err := inverseTemplatePolicyBasis(featureTx, actor, p, change)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			if err := inheritRuleSetSourcePolicy(policyBasis, copy); err != nil {
				return operationResult[ChangeSet]{}, err
			}
			next.TemplateID, next.TemplateVersion = upgrade.Definition.ID, upgrade.Definition.Version
			next.TemplateCopyVersion, next.TemplateCopy = copy.Version, copy
		}
		if err := s.restoreInheritedFields(ctx, featureTx, actor, change, &next); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if len(changedSpecKeys) > 0 || change.TemplateUpgrade != nil {
			next.SpecRevision++
		}
		next.CurrentContextRevision++
		next.ProjectVersion++
		for fieldID := range changedSpecKeys {
			metadata := next.SpecFields[fieldID]
			metadata.ModifiedProjectVersion = next.ProjectVersion
			next.SpecFields[fieldID] = metadata
		}
		if err := s.ApplySemanticDeltas(ctx, featureTx, actor, p, &next, change); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		targetContext, targetSpec := next.CurrentContextRevision, next.SpecRevision
		now := time.Now().UTC()
		previousChange := change
		change.Status = ChangeSetStatusApplied
		change.DecisionBy = actor.UserID
		change.DecidedAt = &now
		change.TargetContextRevision = &targetContext
		change.TargetSpecRevision = &targetSpec
		change.AppliedAt = &now
		for i := range change.Impacts {
			change.Impacts[i].Status = "open"
		}
		if err := store.UpdateChangeSet(previousChange, change); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		chapterIDs := make([]string, 0, len(change.Impacts))
		for _, impact := range change.Impacts {
			chapterIDs = append(chapterIDs, impact.ChapterID)
		}
		if _, ok := tx.(impactTransaction); ok {
			if err := s.commitChangeImpactTasks(featureTx, actor.TenantID, next, change); err != nil {
				return operationResult[ChangeSet]{}, err
			}
		}
		if err := store.InvalidateChapterConfirmations(projectID, chapterIDs); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			details := map[string]any{"base_context_revision": change.BaseContextRevision, "target_context_revision": targetContext, "target_spec_revision": targetSpec, "reason": change.Reason, "change_set_id": change.ID}
			if change.ReversesChangeSetID != "" {
				details["reverses_change_set_id"] = change.ReversesChangeSetID
			}
			if change.TemplateUpgrade != nil {
				details["template_upgrade"] = templateUpgradeAuditDetails(change, next.TemplateCopy)
			}
			// The tenant travels with the event so that a substituted
			// BusinessAuditWriter can decide its scope without reading storage
			// inside this write transaction (#310).
			if err := s.writeBusinessAudit(featureTx, audit, AuditEvent{ID: uuid.NewString(), TenantID: actor.TenantID, ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.apply", Target: change.ID, Details: details, Reason: change.Reason, CreatedAt: now}); err != nil {
				return operationResult[ChangeSet]{}, err
			}
		}
		return operationResult[ChangeSet]{Value: change, Status: operationStatusOK}, nil
	})
}

// ReassessChangeSet copies a stale proposal onto the current project baseline.
// The stale proposal remains immutable history; changed target fields or an
// upgraded template require a human to resolve the conflict before retrying.
func (s *Service) ReassessChangeSet(ctx context.Context, actor Actor, projectID, changeSetID, key string) (json.RawMessage, int, bool, error) {
	if strings.TrimSpace(changeSetID) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	input := struct {
		SourceChangeSetID string `json:"source_change_set_id"`
	}{SourceChangeSetID: changeSetID}
	return runOperation(s, ctx, actor, sourceOperationInput(operationReassessChangeSet, projectID+"/"+changeSetID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, ""), impactSourceDecoder), mutationPort[Transaction], func(tx Transaction, p Project) (operationResult[ChangeSet], error) {
		// Every reassessment writes a new proposal and must have transactional audit.
		if _, ok := tx.(auditTransaction); !ok {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		if p.Status != "active" || p.DiscardedAt != nil || p.CurrentContextRevision < 1 {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		stale, err := store.ChangeSet(projectID, changeSetID)
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if !ChangeSetStatusCanMove("reassess", stale.Status) {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		inverseSourceID, err := inverseLineageSource(tx, p.ID, stale, map[string]bool{})
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if inverseSourceID != "" {
			if _, err := validateInverseLineage(tx, p, actor, inverseSourceID, stale.ID); err != nil {
				return operationResult[ChangeSet]{}, err
			}
			// Normalize only this local proposal basis, never the old row.
			stale.ReversesChangeSetID = inverseSourceID
		}

		if err := validateChangeTemplateFields(p, stale.Fields); err != nil {
			return operationResult[ChangeSet]{}, fmt.Errorf("%w: field proposal is incompatible with the current template: %v", ErrInvalidState, err)
		}
		fields := make([]ChangeFieldDelta, 0, len(stale.Fields))
		for _, field := range stale.Fields {
			currentValue := p.Spec[field.Key]
			if currentValue == field.NewValue {
				continue
			}
			if currentValue != field.OldValue {
				return operationResult[ChangeSet]{}, ErrVersionConflict
			}
			fields = append(fields, field)
		}

		impactChapterIDs := make([]string, 0, len(stale.Impacts))
		var templateUpgrade *TemplateUpgrade
		if stale.TemplateUpgrade != nil {
			upgrade := *stale.TemplateUpgrade
			if p.TemplateCopy == nil || p.TemplateCopy.Status != TemplateCopyBound || p.TemplateCopyVersion != upgrade.BaseTemplateCopyVersion {
				return operationResult[ChangeSet]{}, ErrVersionConflict
			}
			// Do not carry a reviewed migration over edits to its source values.
			// The caller must inspect the latest project and create a fresh upgrade.
			for _, field := range upgrade.Preview.Fields {
				if p.Spec[field.FieldID] != field.Value {
					return operationResult[ChangeSet]{}, ErrVersionConflict
				}
			}
			if _, err := templateSpecAfterUpgrade(p.Spec, upgrade.Definition, upgrade.FieldValues); err != nil {
				return operationResult[ChangeSet]{}, ErrInvalidState
			}
			chapters, err := tx.Chapters(projectID)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			if len(chapters) == 0 {
				return operationResult[ChangeSet]{}, ErrInvalidState
			}
			for _, chapter := range chapters {
				impactChapterIDs = append(impactChapterIDs, chapter.ID)
			}
			slices.Sort(impactChapterIDs)
			upgrade.Preview = buildTemplateMigrationPreview(p, upgrade.Definition, p.ProjectVersion)
			upgrade.Impact = buildTemplateUpgradeImpact(p.TemplateCopy, upgrade.Definition, upgrade.Preview, impactChapterIDs, chapters)
			templateUpgrade = &upgrade
		} else {
			if len(fields) == 0 && len(stale.SemanticDeltas) == 0 {
				// No semantic/template work is discarded: only a completely
				// already-current field proposal reaches this terminal outcome.
				if !ChangeSetStatusCanMove("satisfy", stale.Status) {
					return operationResult[ChangeSet]{}, ErrInvalidState
				}
				now := time.Now().UTC()
				contextRevision, specRevision := p.CurrentContextRevision, p.SpecRevision
				change := ChangeSet{ID: uuid.NewString(), ProjectID: projectID, CreatedBy: actor.UserID,
					ReassessedFromID: stale.ID, ReversesChangeSetID: stale.ReversesChangeSetID, Reason: stale.Reason, Status: ChangeSetStatusSatisfied,
					BaseContextRevision: contextRevision, TargetContextRevision: &contextRevision,
					BaseSpecRevision: specRevision, TargetSpecRevision: &specRevision,
					Fields: []ChangeFieldDelta{}, Impacts: []ChangeImpact{}, CreatedAt: now}
				if err := store.InsertChangeSet(change); err != nil {
					return operationResult[ChangeSet]{}, err
				}
				if err := recordAuditEvent(tx, actor, projectID, "project.change_set.reassess", change.ID, map[string]any{
					"reassessed_from_id": stale.ID, "reverses_change_set_id": change.ReversesChangeSetID,
					"outcome": "already_satisfied", "reason": change.Reason,
					"base_context_revision": contextRevision, "target_context_revision": contextRevision,
					"base_spec_revision": specRevision, "target_spec_revision": specRevision,
				}); err != nil {
					return operationResult[ChangeSet]{}, err
				}
				return operationResult[ChangeSet]{Value: change, Status: operationStatusCreated}, nil
			}
			for _, impact := range stale.Impacts {
				impactChapterIDs = append(impactChapterIDs, impact.ChapterID)
			}
			slices.Sort(impactChapterIDs)
			impactChapterIDs = slices.Compact(impactChapterIDs)
		}

		reason := "研究条件变化，需要重新复核"
		if templateUpgrade != nil {
			reason = "模板或规则版本变化，需要重新复核"
		}
		impacts := make([]ChangeImpact, 0, len(impactChapterIDs))
		for _, chapterID := range impactChapterIDs {
			chapter, err := tx.Chapter(projectID, chapterID)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			impacts = append(impacts, ChangeImpact{ChapterID: chapter.ID, ChapterVersionID: cloneString(chapter.CurrentVersionID), Title: chapter.Title, Reason: reason, Status: "open"})
		}
		change := ChangeSet{
			ID: uuid.NewString(), ProjectID: projectID, CreatedBy: actor.UserID, ReassessedFromID: stale.ID, ReversesChangeSetID: stale.ReversesChangeSetID,
			Reason: stale.Reason, Status: ChangeSetStatusAssessed, BaseContextRevision: p.CurrentContextRevision,
			BaseSpecRevision: p.SpecRevision, Fields: fields, Impacts: impacts,
			TemplateUpgrade: templateUpgrade, CreatedAt: time.Now().UTC(),
		}
		if len(stale.SemanticDeltas) > 0 {
			plan, err := s.ReassessSemanticPlan(ctx, tx, actor, p, change.ID, stale)
			if err != nil {
				return operationResult[ChangeSet]{}, err
			}
			change.SemanticDeltas = plan
		}
		if _, ok := tx.(impactTransaction); ok {
			if err := AssessChangeImpact(tx, p, change); err != nil {
				return operationResult[ChangeSet]{}, err
			}
		}
		if err := store.InsertChangeSet(change); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.reassess", Target: change.ID, Details: map[string]any{
				"reassessed_from_id": stale.ID, "reverses_change_set_id": change.ReversesChangeSetID, "base_context_revision": change.BaseContextRevision,
				"base_spec_revision": change.BaseSpecRevision, "fields": sanitizedChangeFields(change.Fields), "impacts": change.Impacts,
			}, Reason: change.Reason, CreatedAt: change.CreatedAt}); err != nil {
				return operationResult[ChangeSet]{}, err
			}
		}
		return operationResult[ChangeSet]{Value: change, Status: operationStatusCreated}, nil
	})
}

// ApplyChangeSetResult exposes the applied change set without transport fields.
func (s *Service) ApplyChangeSetResult(ctx context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	changeSet, replayed, err := adaptMutationResult[ChangeSet](func() (json.RawMessage, int, bool, error) {
		raw, status, replayed, err := s.ApplyChangeSet(ctx, actor, projectID, changeSetID, key)
		if err != nil {
			return raw, status, replayed, err
		}
		if status == 409 {
			// A stale ChangeSet is durably marked before the legacy operation
			// returns its conflict status. Preserve that domain error at the typed
			// seam so the HTTP adapter emits the established 409 envelope.
			return raw, status, replayed, ErrVersionConflict
		}
		return raw, status, replayed, nil
	}, "change set mutation result")
	if err != nil {
		return ChangeSetMutationResult{Replayed: replayed}, err
	}
	return ChangeSetMutationResult{ChangeSet: changeSet, Replayed: replayed}, nil
}

// RejectChangeSet records the current owner's decision not to apply an
// assessed proposal. It intentionally shares the same operation/idempotency
// boundary as apply so a lost response cannot create a second decision audit.
func (s *Service) RejectChangeSet(ctx context.Context, actor Actor, projectID, changeSetID, key string) (json.RawMessage, int, bool, error) {
	if strings.TrimSpace(changeSetID) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	if err := s.requireOwnerDecision(ctx, actor, projectID); err != nil {
		return nil, 0, false, err
	}
	return runOperation(s, ctx, actor, ownerDecisionOperation(operationInput(operationRejectChangeSet, projectID+"/"+changeSetID, key, map[string]string{"change_set_id": changeSetID}, authorizationRequest(actor, projectID, AuthorizationManage, ""))), mutationPort[ChangeSetMutationTransaction], func(tx ChangeSetMutationTransaction, p Project) (operationResult[ChangeSet], error) {
		featureTx, featureErr := extendedTransaction(tx)
		if featureErr != nil {
			return operationResult[ChangeSet]{}, featureErr
		}
		if p.Status != "active" || p.DiscardedAt != nil {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		if err := ownerDecisionInTransaction(featureTx, p, actor); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		store, ok := tx.(changeSetTransaction)
		if !ok {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		change, err := store.ChangeSet(projectID, changeSetID)
		if err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if change.Status == ChangeSetStatusRejected {
			return operationResult[ChangeSet]{Value: change, Status: operationStatusOK}, nil
		}
		if !ChangeSetStatusCanMove("reject", change.Status) {
			return operationResult[ChangeSet]{}, ErrInvalidState
		}
		previous := change
		change.Status = ChangeSetStatusRejected
		now := time.Now().UTC()
		change.DecisionBy = actor.UserID
		change.DecidedAt = &now
		if err := store.UpdateChangeSet(previous, change); err != nil {
			return operationResult[ChangeSet]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.change_set.reject", Target: change.ID, Details: map[string]any{"base_context_revision": change.BaseContextRevision}, Reason: change.Reason, CreatedAt: now}); err != nil {
				return operationResult[ChangeSet]{}, err
			}
		}
		return operationResult[ChangeSet]{Value: change, Status: operationStatusOK}, nil
	})
}

// RejectChangeSetResult exposes the rejected change set without transport
// fields.
func (s *Service) RejectChangeSetResult(ctx context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	changeSet, replayed, err := adaptMutationResult[ChangeSet](func() (json.RawMessage, int, bool, error) {
		return s.RejectChangeSet(ctx, actor, projectID, changeSetID, key)
	}, "change set mutation result")
	if err != nil {
		return ChangeSetMutationResult{Replayed: replayed}, err
	}
	return ChangeSetMutationResult{ChangeSet: changeSet, Replayed: replayed}, nil
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
	return input.Provenance == nil || strings.TrimSpace(input.Provenance.SourceType) != ""
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
	prepared, err := s.prepareProvenanceOperation(ctx, actor, "createDraftCandidate", projectID, projectID, input)
	if err != nil {
		return nil, 0, false, err
	}
	ctx = prepared
	return runOperation(s, ctx, actor, operationInput(operationCreateDraftCandidate, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, "")), mutationPort[CandidateMutationTransaction], func(tx CandidateMutationTransaction, p Project) (operationResult[DraftCandidate], error) {
		if p.Status != "draft" {
			return operationResult[DraftCandidate]{}, ErrInvalidState
		}
		candidateStore, ok := tx.(draftCandidateTransaction)
		if !ok {
			return operationResult[DraftCandidate]{}, ErrInvalidState
		}
		provenance, _, _ := serverProvenance(ctx, input.Provenance, input.Content, "human", "draft")
		candidate := DraftCandidate{ID: uuid.NewString(), ProjectID: projectID, Kind: input.Kind, Title: input.Title, Content: input.Content, Level: input.Level, BasedOnContextRevision: p.CurrentContextRevision, Provenance: provenance, CreatedBy: actor.UserID, CreatedAt: time.Now().UTC()}
		if err := candidateStore.InsertDraftCandidate(candidate); err != nil {
			return operationResult[DraftCandidate]{}, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "draft_candidate.create", Target: candidate.ID, Details: map[string]any{"level": candidate.Level, "kind": candidate.Kind}, CreatedAt: time.Now().UTC()}); err != nil {
				return operationResult[DraftCandidate]{}, err
			}
		}
		return operationResult[DraftCandidate]{Value: candidate, Status: operationStatusCreated}, nil
	})
}

// CreateDraftCandidateResult exposes the candidate without transport fields.
func (s *Service) CreateDraftCandidateResult(ctx context.Context, actor Actor, projectID, key string, input DraftCandidateInput) (DraftCandidateMutationResult, error) {
	candidate, replayed, err := adaptMutationResult[DraftCandidate](func() (json.RawMessage, int, bool, error) {
		return s.CreateDraftCandidate(ctx, actor, projectID, key, input)
	}, "draft candidate result")
	if err != nil {
		return DraftCandidateMutationResult{Replayed: replayed}, err
	}
	return DraftCandidateMutationResult{Candidate: candidate, Replayed: replayed}, nil
}

func (s *Service) ListAuditEvents(ctx context.Context, actor Actor, projectID string) ([]AuditEvent, error) {
	var result []AuditEvent
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		audit, ok := tx.(auditTransaction)
		if !ok {
			return ErrInvalidState
		}
		result, err = audit.AuditEvents(projectID)
		m, _ := projectMember(p, actor.UserID)
		if err == nil && governanceRole(m) != "owner" && governanceRole(m) != "admin" {
			filtered := []AuditEvent{}
			for _, item := range result {
				if item.ActorID == actor.UserID || item.UserID == actor.UserID {
					filtered = append(filtered, item)
				}
			}
			result = filtered
		}
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
	return sameValidationIssueTargetVersion(left, right)
}

// sameValidationIssueFinding matches the fields that identify the **finding**
// itself. The issue id is deliberately left out: it is the delivery
// evaluator's handle for the finding, and the second evaluator derives the same
// finding from the same rule evaluation without publishing that handle.
// Everything that decides *whether the decision still applies* — rule, ruleset,
// severity, target, target version and project version — is still compared.
func sameValidationIssueFinding(left, right ValidationIssueBinding) bool {
	if left.RuleID != right.RuleID || left.RulesetHash != right.RulesetHash ||
		left.Severity != right.Severity || left.TargetID != right.TargetID || left.ProjectVersion != right.ProjectVersion {
		return false
	}
	return sameValidationIssueTargetVersion(left, right)
}

func sameValidationIssueTargetVersion(left, right ValidationIssueBinding) bool {
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
		!operationcore.ValidKey(key) || (action != "resolve" && action != "dismiss" && action != "waive") ||
		reason == "" || len([]rune(reason)) > 2000 || expectedProjectVersion < 1 {
		return nil, 0, false, false, ErrInvalidRequest
	}
	identity := OperationIdentity{Actor: actor, Operation: "validationIssueDisposition", Target: projectID + "/" + issueID, Key: key}
	var previous OperationResult
	var found bool
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		var err error
		previous, found, err = tx.Operation(identity)
		if err != nil || !found {
			return err
		}
		disposition, err := decodeMutationPayload[ValidationIssueDisposition](previous.Body, "replayed issue disposition")
		if err != nil {
			return err
		}
		if disposition.IssueID != issueID || disposition.ProjectVersion != expectedProjectVersion || disposition.Action != action || disposition.Reason != reason {
			return ErrIdempotencyConflict
		}
		issueAction := AuthorizationAction("issue-" + disposition.Action)
		if disposition.Action == "dismiss" && disposition.Severity == "blocking" {
			issueAction = AuthorizationIssueDismissBlocking
		}
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, issueAction, disposition.TargetID)); err != nil {
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

// ReplayValidationIssueDispositionResult returns the typed replay payload
// while preserving the distinction between a missing and completed operation.
func (s *Service) ReplayValidationIssueDispositionResult(ctx context.Context, actor Actor, projectID, issueID, key, action, reason string, expectedProjectVersion int64) (ValidationIssueDispositionReplayResult, error) {
	raw, _, replayed, found, err := s.ReplayValidationIssueDisposition(ctx, actor, projectID, issueID, key, action, reason, expectedProjectVersion)
	if err != nil || !found {
		return ValidationIssueDispositionReplayResult{Replayed: replayed, Found: found}, err
	}
	disposition, err := decodeMutationPayload[ValidationIssueDisposition](raw, "replayed issue disposition")
	if err != nil {
		return ValidationIssueDispositionReplayResult{Replayed: replayed, Found: found}, err
	}
	return ValidationIssueDispositionReplayResult{Disposition: disposition, Replayed: replayed, Found: true}, nil
}

// RecordValidationIssueDisposition appends an immutable, version-bound audit
// event. It deliberately does not mutate the rule result or close a impact
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
	issueAction := AuthorizationAction("issue-" + input.Action)
	if input.Action == "dismiss" && input.Severity == "blocking" {
		issueAction = AuthorizationIssueDismissBlocking
	}
	request := authorizationRequest(actor, projectID, issueAction, input.TargetID)
	return runOperation(s, ctx, actor, operationInput(operationValidationIssue, projectID+"/"+input.IssueID, key, input, request), mutationPort[AuditMutationTransaction], func(tx AuditMutationTransaction, p Project) (operationResult[ValidationIssueDisposition], error) {
		if p.Status != "active" || p.DiscardedAt != nil {
			return operationResult[ValidationIssueDisposition]{}, ErrInvalidState
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return operationResult[ValidationIssueDisposition]{}, ErrVersionConflict
		}
		audit, ok := tx.(auditTransaction)
		if !ok {
			return operationResult[ValidationIssueDisposition]{}, ErrInvalidState
		}
		// A human disposition is single-use for one exact finding revision. Check
		// inside the same transaction as the append so concurrent duplicate actions
		// cannot both succeed with different idempotency keys.
		events, err := audit.AuditEvents(projectID)
		if err != nil {
			return operationResult[ValidationIssueDisposition]{}, err
		}
		for _, event := range events {
			if event.Action != "validation_issue.disposition" || event.Target != input.IssueID {
				continue
			}
			raw, err := json.Marshal(event.Details)
			if err != nil {
				return operationResult[ValidationIssueDisposition]{}, err
			}
			var details struct {
				Disposition ValidationIssueDisposition `json:"validation_issue_disposition"`
			}
			if err := json.Unmarshal(raw, &details); err != nil {
				return operationResult[ValidationIssueDisposition]{}, err
			}
			if sameValidationIssueBinding(input.ValidationIssueBinding, details.Disposition.ValidationIssueBinding) {
				return operationResult[ValidationIssueDisposition]{}, ErrInvalidState
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
			return operationResult[ValidationIssueDisposition]{}, err
		}
		return operationResult[ValidationIssueDisposition]{Value: disposition, Status: operationStatusCreated}, nil
	})
}

// RecordValidationIssueDispositionResult exposes the recorded disposition
// without transport fields.
func (s *Service) RecordValidationIssueDispositionResult(ctx context.Context, actor Actor, projectID, key string, input ValidationIssueDispositionInput) (ValidationIssueDispositionResult, error) {
	disposition, replayed, err := adaptMutationResult[ValidationIssueDisposition](func() (json.RawMessage, int, bool, error) {
		return s.RecordValidationIssueDisposition(ctx, actor, projectID, key, input)
	}, "issue disposition result")
	if err != nil {
		return ValidationIssueDispositionResult{Replayed: replayed}, err
	}
	return ValidationIssueDispositionResult{Disposition: disposition, Replayed: replayed}, nil
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
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			return err
		}
		latest, err := s.latestValidationIssueDispositions(ctx, tx, projectID)
		if err != nil {
			return err
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

// ValidationIssueDispositionsInTransaction returns the recorded decisions that
// apply to the given rule findings, read **inside the caller's transaction**.
//
// 第二个求值器（模板治理求值）从**同一条规则判断**得出**同一条 finding**，但它不
// 发布交付链的 issue id —— 那个 id 是交付链给 finding 的把手，而不是 finding
// 本身的身份。因此这里的匹配跑在**标识 finding 本身**的字段上：规则、规则集
// 摘要、严重度、目标、目标版本、项目版本。版本一变匹配即失效，问题重新出现；
// 被记录的处置仍带着完整绑定（含 issue id）留在审计里。
//
// 它在调用方的事务里读，是因为稳定化聚合器正跑在那个事务里：另开一条读事务会
// 和 SQLite 的单连接互锁。
func (s *Service) ValidationIssueDispositionsInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID string, findings []ValidationIssueBinding) (map[string]ValidationIssueDisposition, error) {
	if len(findings) > 500 {
		return nil, ErrInvalidRequest
	}
	byID := make(map[string]ValidationIssueBinding, len(findings))
	for _, binding := range findings {
		if err := validateValidationIssueBinding(binding); err != nil {
			return nil, err
		}
		if _, exists := byID[binding.IssueID]; exists {
			return nil, ErrInvalidRequest
		}
		byID[binding.IssueID] = binding
	}
	if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
		return nil, err
	}
	latest, err := s.latestValidationIssueDispositions(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]ValidationIssueDisposition)
	for id, binding := range byID {
		matched, found := ValidationIssueDisposition{}, false
		for _, disposition := range latest {
			if !sameValidationIssueFinding(binding, disposition.ValidationIssueBinding) {
				continue
			}
			if !found || disposition.CreatedAt.After(matched.CreatedAt) {
				matched, found = disposition, true
			}
		}
		if found {
			result[id] = matched
		}
	}
	return result, nil
}

// latestValidationIssueDispositions returns the newest recorded decision per
// issue id. Older decisions stay in the audit log; only the newest one can
// describe the present state of a finding.
func (s *Service) latestValidationIssueDispositions(ctx context.Context, tx Transaction, projectID string) (map[string]ValidationIssueDisposition, error) {
	audit, ok := tx.(auditTransaction)
	if !ok {
		return nil, ErrInvalidState
	}
	events, err := audit.AuditEvents(projectID)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]ValidationIssueDisposition)
	for _, event := range events {
		if event.Action != "validation_issue.disposition" {
			continue
		}
		raw, err := json.Marshal(event.Details)
		if err != nil {
			return nil, err
		}
		var details struct {
			Disposition ValidationIssueDisposition `json:"validation_issue_disposition"`
		}
		if err := json.Unmarshal(raw, &details); err != nil {
			return nil, err
		}
		if details.Disposition.IssueID == "" {
			return nil, ErrInvalidState
		}
		previous, exists := latest[details.Disposition.IssueID]
		if !exists || details.Disposition.ProjectVersion > previous.ProjectVersion ||
			(details.Disposition.ProjectVersion == previous.ProjectVersion && details.Disposition.CreatedAt.After(previous.CreatedAt)) {
			latest[details.Disposition.IssueID] = details.Disposition
		}
	}
	return latest, nil
}

func (s *Service) ActivationDiff(ctx context.Context, actor Actor, projectID string, sinceProjectVersion int64) (ActivationDiff, error) {
	var result ActivationDiff
	err := func() error {
		p, err := s.GetProject(ctx, actor, projectID)
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
	}()
	return result, err
}

func (s *Service) ListDraftCandidates(ctx context.Context, actor Actor, projectID string) ([]DraftCandidate, error) {
	var result []DraftCandidate
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
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
	if err != nil {
		return nil, err
	}
	return s.draftProvenanceProjection(ctx, actor, projectID, result)
}
func (s *Service) ActivateProject(ctx context.Context, actor Actor, projectID, key string, input ActivateProjectInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 {
		return nil, 0, false, ErrInvalidRequest
	}
	return runOperation(s, ctx, actor, operationInput(operationActivateProject, projectID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, "")), mutationPort[ChapterMutationTransaction], func(tx ChapterMutationTransaction, p Project) (operationResult[Project], error) {
		featureTx, featureErr := extendedTransaction(tx)
		if featureErr != nil {
			return operationResult[Project]{}, featureErr
		}

		owner := false
		for _, member := range p.Members {
			if member.UserID == actor.UserID && member.Role == "owner" {
				owner = true
				break
			}
		}
		if !owner {
			return operationResult[Project]{}, ErrNotFound
		}
		if s.requireActivationRead {
			if input.ActivationReadID == "" {
				return operationResult[Project]{}, ErrInvalidRequest
			}
			if err := validateActivationRead(featureTx, actor, p, input.ActivationReadID); err != nil {
				return operationResult[Project]{}, err
			}
		}
		// G7 callers predate the project-version review fields. Treat omitted
		// values as the current version while still enforcing explicit stale
		// values for clients.
		if input.ExpectedProjectVersion == 0 {
			input.ExpectedProjectVersion = p.ProjectVersion
		}
		if input.ReviewedProjectVersion == 0 {
			input.ReviewedProjectVersion = p.ProjectVersion
		}
		if input.ExpectedProjectVersion < 1 || input.ReviewedProjectVersion < 1 {
			return operationResult[Project]{}, ErrInvalidRequest
		}
		if input.ExpectedProjectVersion > 0 && p.ProjectVersion != input.ExpectedProjectVersion {
			return operationResult[Project]{}, ErrVersionConflict
		}
		if input.ReviewedProjectVersion > 0 && p.ProjectVersion != input.ReviewedProjectVersion {
			return operationResult[Project]{}, ErrVersionConflict
		}
		if p.SpecRevision != input.ExpectedSpecRevision {
			return operationResult[Project]{}, ErrVersionConflict
		}
		if p.Status != "draft" {
			return operationResult[Project]{}, ErrInvalidState
		}
		var templateCopy *ProjectTemplateCopy
		if p.TemplateCopy == nil {
			// Upgrade legacy draft rows created before project copies existed. The
			// exact source version is copied once and bound in this transaction.
			template, err := s.templates.Get(p.TemplateID, p.TemplateVersion)
			if err != nil || template.Version != p.TemplateVersion {
				return operationResult[Project]{}, ErrInvalidState
			}
			templateCopy, err = projectTemplateCopy(actor, p.ID, p.TemplateCopyVersion+1, template, TemplateCopyDraft)
			if err != nil {
				return operationResult[Project]{}, err
			}
		} else {
			copy := *p.TemplateCopy
			if copy.Status != TemplateCopyDraft || validateProjectTemplateCopy(&copy) != nil {
				return operationResult[Project]{}, ErrInvalidState
			}
			templateCopy = &copy
		}
		template := templateCopy.Definition
		for _, field := range template.RequiredFields {
			if strings.TrimSpace(p.Spec[field]) == "" {
				return operationResult[Project]{}, ErrInvalidState
			}
		}
		for fieldID, field := range templateFieldDefinitions(template) {
			if field.Required && !compatibleTemplateValue(p.Spec[fieldID], field) {
				return operationResult[Project]{}, ErrInvalidState
			}
		}
		if err := validateInitializationRules(p); err != nil {
			return operationResult[Project]{}, err
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
		if s.requireActivationRead {
			store, ok := tx.(projectRecordTransaction)
			if !ok {
				return operationResult[Project]{}, ErrInvalidState
			}
			if err := store.PutProjectRecord(projectID, "baseline_confirmation", next.BaselineConfirmationID, 0, baselineRecord(next, actor)); err != nil {
				return operationResult[Project]{}, err
			}
		}
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return operationResult[Project]{}, err
		}
		for _, section := range template.Sections {
			if err := tx.InsertChapter(Chapter{ID: uuid.NewString(), ProjectID: projectID, SectionID: section.ID, Title: section.Title}); err != nil {
				return operationResult[Project]{}, err
			}
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.activate", Target: projectID, Details: map[string]any{"project_version": next.ProjectVersion, "context_revision": next.CurrentContextRevision, "template_copy_version": copy.Version, "template_content_hash": copy.ContentHash, "ruleset_hash": copy.RulesetHash, "chapter_count": len(template.Sections), "delivery_status": next.DeliveryStatus, "baseline_confirmation_id": next.BaselineConfirmationID}, CreatedAt: time.Now().UTC()}); err != nil {
				return operationResult[Project]{}, err
			}
		}
		return operationResult[Project]{Value: next, Status: operationStatusOK}, nil
	})
}

// ActivateProjectResult exposes the activated project without transport fields.
func (s *Service) ActivateProjectResult(ctx context.Context, actor Actor, projectID, key string, input ActivateProjectInput) (ProjectMutationResult, error) {
	project, replayed, err := adaptMutationResult[Project](func() (json.RawMessage, int, bool, error) {
		return s.ActivateProject(ctx, actor, projectID, key, input)
	}, "project mutation result")
	if err != nil {
		return ProjectMutationResult{Replayed: replayed}, err
	}
	return ProjectMutationResult{Project: project, Replayed: replayed}, nil
}
func (s *Service) ListChapters(ctx context.Context, actor Actor, projectID string) ([]Chapter, error) {
	var result []Chapter
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		project, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
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
					markChapterUnconfirmed(&result[i])
				}
			}
			return result, nil
		}
		if err := legacySourceRevalidate(ctx, s.sources, projectID, actor.UserID, ids); err != nil {
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

func (s *Service) SaveChapter(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveChapterInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.SourceIDs == nil || len(input.BodyMarkdown) > 200000 {
		return nil, 0, false, ErrInvalidRequest
	}
	if err := sourcemarker.Validate(input.BodyMarkdown); err != nil {
		return nil, 0, false, ErrInvalidRequest
	}
	refs := sourcemarker.IDs(input.BodyMarkdown)
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
	return runOperation(s, ctx, actor, sourceOperationInput(operationSaveChapter, projectID+"/"+chapterID, key, input, authorizationRequest(actor, projectID, AuthorizationWrite, chapterID), saveChapterSourceIDs), mutationPort[ChapterMutationTransaction], func(tx ChapterMutationTransaction, p Project) (operationResult[Chapter], error) {
		if p.Status != "active" {
			return operationResult[Chapter]{}, ErrInvalidState
		}
		if p.SpecRevision != input.ExpectedSpecRevision {
			return operationResult[Chapter]{}, ErrVersionConflict
		}
		old, err := tx.Chapter(projectID, chapterID)
		if err != nil {
			return operationResult[Chapter]{}, err
		}
		if !sameVersion(old.CurrentVersionID, input.ExpectedChapterVersionID) {
			return operationResult[Chapter]{}, ErrVersionConflict
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
			return operationResult[Chapter]{}, err
		}
		workingCopy, err := tx.WorkingCopy(projectID, chapterID)
		if err != nil {
			return operationResult[Chapter]{}, err
		}
		next := old
		id := uuid.NewString()
		next.CurrentVersionID = &id
		next.BodyMarkdown = input.BodyMarkdown
		next.SourceIDs = declared
		next.CitationUsages = usages
		markChapterUnconfirmed(&next)
		next.ReviewItems = rebaseReviewItems(old.BodyMarkdown, input.BodyMarkdown, old.ReviewItems, workingCopy.WorkingCopyRevision+1, &id)
		if err := tx.AppendChapter(old, next, p.SpecRevision); err != nil {
			return operationResult[Chapter]{}, err
		}
		nextCopy := workingCopy
		nextCopy.BaseChapterVersionID = &id
		nextCopy.SpecRevision = p.SpecRevision
		nextCopy.WorkingCopyRevision++
		nextCopy.BodyMarkdown = input.BodyMarkdown
		nextCopy.SourceIDs = slices.Clone(declared)
		nextCopy.CitationUsages = slices.Clone(usages)
		nextCopy.ReviewItems = rebaseReviewItems(old.BodyMarkdown, input.BodyMarkdown, old.ReviewItems, nextCopy.WorkingCopyRevision, &id)
		nextCopy.UpdatedAt = time.Now().UTC()
		if err := tx.SaveWorkingCopy(workingCopy, nextCopy); err != nil {
			return operationResult[Chapter]{}, err
		}
		return operationResult[Chapter]{Value: next, Status: operationStatusCreated}, nil
	})
}

// SaveChapterResult exposes the saved chapter without transport fields.
func (s *Service) SaveChapterResult(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveChapterInput) (ChapterMutationResult, error) {
	chapter, replayed, err := adaptMutationResult[Chapter](func() (json.RawMessage, int, bool, error) {
		return s.SaveChapter(ctx, actor, projectID, chapterID, key, input)
	}, "chapter mutation result")
	if err != nil {
		return ChapterMutationResult{Replayed: replayed}, err
	}
	return ChapterMutationResult{Chapter: chapter, Replayed: replayed}, nil
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
	prepared, snapshot, err := s.PrepareProjectProvenance(ctx, actor, projectID, true)
	if err != nil {
		return result, err
	}
	ctx = prepared
	err = s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		if p.DiscardedAt != nil {
			return ErrInvalidState
		}
		if err := s.ValidateProjectProvenanceInTransaction(ctx, tx, actor, p, snapshot, true); err != nil {
			return err
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
