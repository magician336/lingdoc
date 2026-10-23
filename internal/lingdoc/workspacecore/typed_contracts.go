package workspacecore

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ProjectMembershipTransaction is the cohesive persistence port for project
// lifecycle and membership state. The legacy Transaction surface remains
// available for compatibility, while mutation callbacks use narrower ports.
type ProjectMembershipTransaction interface {
	ActiveMember(Actor) (bool, error)
	Project(uint64, string) (Project, error)
	Projects(Actor, int) ([]Project, error)
	InsertProject(uint64, Project) error
	UpdateProject(uint64, Project, Project) error
	ReplaceCollaborators(string, []string) error
	ReplaceMembers(string, []Member) error
}

// ChapterWorkspaceTransaction is the cohesive persistence port for chapter
// versions and the mutable working copy that precedes them.
type ChapterWorkspaceTransaction interface {
	Chapters(string) ([]Chapter, error)
	Chapter(string, string) (Chapter, error)
	WorkingCopy(string, string) (WorkingCopy, error)
	SaveWorkingCopy(WorkingCopy, WorkingCopy) error
	ChapterVersions(string, string) ([]ChapterVersion, error)
	ChapterVersion(string, string, string) (ChapterVersion, error)
	InsertChapter(Chapter) error
	AppendChapter(Chapter, Chapter, int64) error
}

// OperationTransaction is the cohesive persistence port for idempotency and
// replay records. Operation records are still committed by the same
// Repository transaction runner as every other port.
type OperationTransaction interface {
	Operation(OperationIdentity) (OperationResult, bool, error)
	SaveOperation(OperationIdentity, OperationResult) error
}

// AuditTransaction is the cohesive persistence port for in-transaction audit
// records. The legacy unexported name below is retained for existing callers.
type AuditTransaction interface {
	RecordAudit(AuditEvent) error
	AuditEvents(string) ([]AuditEvent, error)
}

type auditTransaction = AuditTransaction

// Mutation callbacks depend on a small set of cohesive transaction ports. The
// port groups match the domain aggregates rather than individual operations;
// the repository still owns the surrounding transaction and idempotency
// boundary. A concrete transaction may implement more than one group.
type ProjectMutationTransaction interface {
	ProjectMembershipTransaction
}

type ChapterMutationTransaction interface {
	ProjectMembershipTransaction
	ChapterWorkspaceTransaction
}

type ChangeSetMutationTransaction interface {
	ProjectMembershipTransaction
	ChapterWorkspaceTransaction
	changeSetTransaction
	AuditTransaction
}

type CandidateMutationTransaction interface {
	draftCandidateTransaction
}

type AuditMutationTransaction interface {
	AuditTransaction
}

type ownerTransferStore interface {
	InsertOwnerTransfer(OwnerTransfer) error
	OwnerTransfer(string, string) (OwnerTransfer, error)
	EnsureMember(string, string, string) error
	SetMemberRole(string, string, string) error
	AcceptOwnerTransfer(string, string, int64, time.Time) error
}

type OwnerTransferMutationTransaction interface {
	ProjectMembershipTransaction
	ownerTransferStore
}

// mutationPort preserves the explicit ErrInvalidState compatibility path while
// keeping callbacks typed to one of the cohesive mutation ports above.
func mutationPort[T any](tx Transaction) (T, error) {
	port, ok := any(tx).(T)
	if !ok {
		var zero T
		return zero, ErrInvalidState
	}
	return port, nil
}

// AuthorizationAction is the small, structured vocabulary understood by the
// Workspace policy. Callers select an action and (when needed) a resource;
// they do not need to know the legacy capability encoding.
type AuthorizationAction string

const (
	AuthorizationDeliveryRead         AuthorizationAction = "delivery-read"
	AuthorizationTenantManage         AuthorizationAction = "tenant-manage"
	AuthorizationResearch             AuthorizationAction = "research"
	AuthorizationAgent                AuthorizationAction = "agent"
	AuthorizationEvidenceWrite        AuthorizationAction = "evidence-write"
	AuthorizationEvidenceConfirm      AuthorizationAction = "evidence-confirm"
	AuthorizationConfirm              AuthorizationAction = "confirm"
	AuthorizationReview               AuthorizationAction = "review"
	AuthorizationChangeAssess         AuthorizationAction = "change-assess"
	AuthorizationImpactResolve        AuthorizationAction = "impact-resolve"
	AuthorizationOpsControl           AuthorizationAction = "ops-control"
	AuthorizationRead                 AuthorizationAction = "read"
	AuthorizationWrite                AuthorizationAction = "write"
	AuthorizationCreate               AuthorizationAction = "create"
	AuthorizationManage               AuthorizationAction = "manage"
	AuthorizationIssueResolve         AuthorizationAction = "issue-resolve"
	AuthorizationIssueDismiss         AuthorizationAction = "issue-dismiss"
	AuthorizationIssueWaive           AuthorizationAction = "issue-waive"
	AuthorizationIssueDismissBlocking AuthorizationAction = "issue-dismiss-blocking"
)

// ResourceKind identifies the canonical object kind used by function scopes.
type ResourceKind string

const (
	ResourceChapter ResourceKind = "chapter"
	ResourceAsset   ResourceKind = "asset"
)

// ResourceRef is the single typed source for resource-scope identifiers.
// Chapter IDs remain opaque for the existing persisted contract; assets use
// the canonical `asset:<id>` representation.
type ResourceRef struct {
	Kind      ResourceKind
	ID        string
	ChapterID string
}

func NewChapterResourceRef(id string) (ResourceRef, error) {
	ref := ResourceRef{Kind: ResourceChapter, ID: id}
	if !ref.valid() {
		return ResourceRef{}, ErrInvalidRequest
	}
	return ref, nil
}

func NewAssetResourceRef(id string) (ResourceRef, error) {
	ref := ResourceRef{Kind: ResourceAsset, ID: id}
	if !ref.valid() {
		return ResourceRef{}, ErrInvalidRequest
	}
	return ref, nil
}

func NewAssetResourceRefForChapter(id, chapterID string) (ResourceRef, error) {
	ref := ResourceRef{Kind: ResourceAsset, ID: id, ChapterID: chapterID}
	if !ref.valid() {
		return ResourceRef{}, ErrInvalidRequest
	}
	return ref, nil
}

func (r ResourceRef) valid() bool {
	validID := func(value string) bool {
		return value != "" && strings.TrimSpace(value) == value && !strings.Contains(value, ":")
	}
	switch r.Kind {
	case ResourceChapter:
		return validID(r.ID) && r.ChapterID == ""
	case ResourceAsset:
		return validID(r.ID) && (r.ChapterID == "" || validID(r.ChapterID))
	default:
		return false
	}
}

// AuthorizationResource returns the persisted scope spelling for this ref.
// Invalid values return an empty string and are rejected by batch authorization.
func (r ResourceRef) AuthorizationResource() string {
	if !r.valid() {
		return ""
	}
	if r.Kind == ResourceAsset {
		return "asset:" + r.ID
	}
	return r.ID
}

type ResourceAuthorizationDecision struct {
	Resource ResourceRef
	Allowed  bool
	Reason   string
}

// AuthorizationRequest is the only authorization input used by Workspace
// internals. Resource is the chapter, asset, delivery, or issue identifier
// whose function scope is being checked. The legacy capability string is
// derived only at the compatibility and audit boundaries.
type AuthorizationRequest struct {
	Actor     Actor
	ProjectID string
	Action    AuthorizationAction
	Resource  string
}

func (r AuthorizationRequest) capability() string {
	if r.Resource == "" {
		return string(r.Action)
	}
	return string(r.Action) + ":" + r.Resource
}

func authorizationRequest(actor Actor, projectID string, action AuthorizationAction, resource string) AuthorizationRequest {
	return AuthorizationRequest{Actor: actor, ProjectID: projectID, Action: action, Resource: resource}
}

func authorizationRequestFromCapability(actor Actor, projectID, capability string) (AuthorizationRequest, error) {
	action, resource, ok := strings.Cut(capability, ":")
	if !ok {
		action = capability
	}
	request := AuthorizationRequest{Actor: actor, ProjectID: projectID, Action: AuthorizationAction(action), Resource: resource}
	if !validAuthorizationAction(request.Action) || strings.Contains(resource, ":") {
		return AuthorizationRequest{}, ErrInvalidRequest
	}
	return request, nil
}

func validAuthorizationAction(action AuthorizationAction) bool {
	switch action {
	case AuthorizationDeliveryRead, AuthorizationTenantManage, AuthorizationResearch, AuthorizationAgent, AuthorizationEvidenceWrite, AuthorizationEvidenceConfirm, AuthorizationConfirm, AuthorizationReview, AuthorizationChangeAssess, AuthorizationImpactResolve, AuthorizationOpsControl,
		AuthorizationRead, AuthorizationWrite, AuthorizationCreate, AuthorizationManage,
		AuthorizationIssueResolve, AuthorizationIssueDismiss, AuthorizationIssueWaive, AuthorizationIssueDismissBlocking:
		return true
	default:
		return false
	}
}

// AuthorizationPolicy is the structured Workspace authorization seam.
type AuthorizationPolicy interface {
	AuthorizeTenant(Transaction, AuthorizationRequest) error
	AuthorizeProject(Transaction, AuthorizationRequest) (Project, error)
}

// ProjectAuthorizer is the pre-C2 compatibility seam. New Workspace code
// uses AuthorizationPolicy; this interface remains so existing adapters can
// migrate without changing behavior in one step.
type legacyProjectAuthorizerAdapter struct{ legacy ProjectAuthorizer }

func (a legacyProjectAuthorizerAdapter) AuthorizeTenant(tx Transaction, request AuthorizationRequest) error {
	return a.legacy.AuthorizeTenant(tx, request.Actor, request.capability())
}

func (a legacyProjectAuthorizerAdapter) AuthorizeProject(tx Transaction, request AuthorizationRequest) (Project, error) {
	return a.legacy.AuthorizeProject(tx, request.Actor, request.ProjectID, request.capability())
}

// operationName and operationDescriptor keep the operation runner's
// vocabulary inside WorkspaceCore. Callers provide a typed input and a typed
// source decoder instead of unconstrained operation strings and body values.
// Persistence still stores the stable string form in OperationIdentity for
// compatibility with existing rows.
type operationName string

const (
	operationReassessChangeSet          operationName = "reassessChangeSet"
	operationReverseChangeSet           operationName = "reverseChangeSet"
	operationCreateDependencyEdge       operationName = "createDependencyEdge"
	operationDecideDependencyEdge       operationName = "decideDependencyEdge"
	operationActOnImpactTask            operationName = "actOnImpactTask"
	operationStabilizeRevision          operationName = "stabilizeRevision"
	operationCreateChangeDraft          operationName = "createChangeDraft"
	operationAssessChangeSet            operationName = "assessChangeSet"
	operationEditChangeDraft            operationName = "editChangeDraft"
	operationAcknowledgeImpactPreflight operationName = "acknowledgeImpactPreflight"
	operationMemberAssignment           operationName = "memberAssignment"
	operationApiKeyProjectGrant         operationName = "apiKeyProjectGrant"
	operationRejectOwnerTransfer        operationName = "rejectOwnerTransfer"
	operationInviteMember               operationName = "inviteMember"
	operationAcceptMemberInvitation     operationName = "acceptMemberInvitation"
	operationMemberLifecycle            operationName = "memberLifecycle"
	operationRuntimeControl             operationName = "runtimeControl"
	operationRuntimeEvent               operationName = "runtimeEvent"
	operationRuntimeAlertRule           operationName = "runtimeAlertRule"
	operationTrialCreateWindow          operationName = "trialCreateWindow"
	operationTrialWindowAction          operationName = "trialWindowAction"
	operationTrialRecordSample          operationName = "trialRecordSample"
	operationTrialReviewSample          operationName = "trialReviewSample"
	operationTrialCreateDecision        operationName = "trialCreateDecision"
	operationTrialSignDecision          operationName = "trialSignDecision"
	operationChapterReview              operationName = "chapterReview"
	operationChapterComment             operationName = "chapterComment"
	operationSetReviewAnchor            operationName = "setReviewAnchor"
	operationDiscardProject             operationName = "discardProject"
	operationRestoreProject             operationName = "restoreProject"
	operationArchiveProject             operationName = "archiveProject"
	operationSaveTemplateCopy           operationName = "saveTemplateCopy"
	operationBindTemplateCopyRuleSet    operationName = "bindTemplateCopyRuleSet"
	operationChangeTemplate             operationName = "changeTemplate"
	operationCreateProject              operationName = "createProject"
	operationSaveSpec                   operationName = "saveSpec"
	operationCreateChangeSet            operationName = "createChangeSet"
	operationApplyChangeSet             operationName = "applyChangeSet"
	operationRejectChangeSet            operationName = "rejectChangeSet"
	operationCreateDraftCandidate       operationName = "createDraftCandidate"
	operationValidationIssue            operationName = "validationIssueDisposition"
	operationRequestOwnerTransfer       operationName = "requestOwnerTransfer"
	operationAcceptOwnerTransfer        operationName = "acceptOwnerTransfer"
	operationTransferOwner              operationName = "transferOwner"
	operationSaveMembers                operationName = "saveMembers"
	operationActivateProject            operationName = "activateProject"
	operationSaveChapter                operationName = "saveChapter"
	operationSaveWorkingCopy            operationName = "saveWorkingCopy"
	operationApplySelectedRewrite       operationName = "applySelectedRewrite"
	operationCommitWorkingCopy          operationName = "commitWorkingCopy"
	operationRestoreWorkingCopy         operationName = "restoreWorkingCopy"
)

type operationSourceDecoder[I any] func(ChapterWorkspaceTransaction, operationDescriptor[I], OperationResult, bool) ([]string, error)

type operationOutputKind string

const (
	operationOutputAPIKeyGrant      operationOutputKind = "api_key_grant"
	operationOutputChapterComment   operationOutputKind = "chapter_comment"
	operationOutputChapterReview    operationOutputKind = "chapter_review"
	operationOutputDependencyEdge   operationOutputKind = "dependency_edge"
	operationOutputImpactPreflight  operationOutputKind = "impact_preflight"
	operationOutputImpactTask       operationOutputKind = "impact_task"
	operationOutputInvitation       operationOutputKind = "invitation"
	operationOutputRuntimeAlertRule operationOutputKind = "runtime_alert_rule"
	operationOutputRuntimeControl   operationOutputKind = "runtime_control"
	operationOutputRuntimeEvent     operationOutputKind = "runtime_event"
	operationOutputStabilization    operationOutputKind = "stabilization"
	operationOutputTrialDecision    operationOutputKind = "trial_decision"
	operationOutputTrialSample      operationOutputKind = "trial_sample"
	operationOutputTrialWindow      operationOutputKind = "trial_window"
	operationOutputProject          operationOutputKind = "project"
	operationOutputChapter          operationOutputKind = "chapter"
	operationOutputWorking          operationOutputKind = "working_copy"
	operationOutputCommitted        operationOutputKind = "committed_chapter"
	operationOutputChangeSet        operationOutputKind = "change_set"
	operationOutputCandidate        operationOutputKind = "draft_candidate"
	operationOutputIssue            operationOutputKind = "issue_disposition"
	operationOutputTransfer         operationOutputKind = "owner_transfer"
)

type operationDescriptor[I any] struct {
	Name         operationName
	Target       string
	Key          string
	Input        I
	Request      AuthorizationRequest
	SourceIDs    operationSourceDecoder[I]
	Output       operationOutputKind
	CheckProject func(Project) error
}

// Owner-only business decisions keep their current-project precondition on
// both new operations and replays. G5 manage permission alone is not an
// academic decision grant (a former Owner can remain a project Admin).
func ownerDecisionOperation[I any](operation operationDescriptor[I]) operationDescriptor[I] {
	actor := operation.Request.Actor
	operation.CheckProject = func(p Project) error {
		if !projectOwner(p, actor.UserID) {
			return ErrNotFound
		}
		if p.Status != "active" || p.DiscardedAt != nil {
			return ErrInvalidState
		}
		return nil
	}
	return operation
}

func operationInput[I any](name operationName, target, key string, input I, request AuthorizationRequest) operationDescriptor[I] {
	return operationDescriptor[I]{Name: name, Target: target, Key: key, Input: input, Request: request, Output: operationOutputFor(name)}
}

func sourceOperationInput[I any](name operationName, target, key string, input I, request AuthorizationRequest, sourceIDs operationSourceDecoder[I]) operationDescriptor[I] {
	descriptor := operationInput(name, target, key, input, request)
	descriptor.SourceIDs = sourceIDs
	return descriptor
}

func operationOutputFor(name operationName) operationOutputKind {
	switch name {
	case operationApiKeyProjectGrant:
		return operationOutputAPIKeyGrant
	case operationReassessChangeSet, operationReverseChangeSet, operationCreateChangeDraft, operationAssessChangeSet, operationEditChangeDraft:
		return operationOutputChangeSet
	case operationChapterComment:
		return operationOutputChapterComment
	case operationChapterReview:
		return operationOutputChapterReview
	case operationCreateDependencyEdge, operationDecideDependencyEdge:
		return operationOutputDependencyEdge
	case operationAcknowledgeImpactPreflight:
		return operationOutputImpactPreflight
	case operationActOnImpactTask:
		return operationOutputImpactTask
	case operationInviteMember, operationAcceptMemberInvitation:
		return operationOutputInvitation
	case operationMemberAssignment, operationMemberLifecycle:
		return operationOutputProject
	case operationRuntimeAlertRule:
		return operationOutputRuntimeAlertRule
	case operationRuntimeControl:
		return operationOutputRuntimeControl
	case operationRuntimeEvent:
		return operationOutputRuntimeEvent
	case operationStabilizeRevision:
		return operationOutputStabilization
	case operationRejectOwnerTransfer:
		return operationOutputTransfer
	case operationTrialCreateDecision, operationTrialSignDecision:
		return operationOutputTrialDecision
	case operationTrialRecordSample, operationTrialReviewSample:
		return operationOutputTrialSample
	case operationTrialCreateWindow, operationTrialWindowAction:
		return operationOutputTrialWindow
	case operationSetReviewAnchor:
		return operationOutputWorking
	case operationCreateChangeSet, operationApplyChangeSet, operationRejectChangeSet:
		return operationOutputChangeSet
	case operationCreateDraftCandidate:
		return operationOutputCandidate
	case operationValidationIssue:
		return operationOutputIssue
	case operationRequestOwnerTransfer, operationAcceptOwnerTransfer:
		return operationOutputTransfer
	case operationSaveChapter:
		return operationOutputChapter
	case operationSaveWorkingCopy, operationApplySelectedRewrite, operationRestoreWorkingCopy:
		return operationOutputWorking
	case operationCommitWorkingCopy:
		return operationOutputCommitted
	default:
		return operationOutputProject
	}
}

// operationStatus is the runner's transport-neutral status vocabulary. HTTP
// codes are converted only at the old raw method boundary; mutation callbacks
// return operationResult instead.
type operationStatus uint8

const (
	operationStatusOK operationStatus = iota + 1
	operationStatusCreated
	operationStatusConflict
)

func (s operationStatus) HTTPStatus() int {
	switch s {
	case operationStatusCreated:
		return 201
	case operationStatusConflict:
		return 409
	default:
		return 200
	}
}

type operationResult[T any] struct {
	Value  T
	Status operationStatus
	Output operationOutputKind
}

type projectVersionInput struct {
	ExpectedProjectVersion int64 `json:"expected_project_version"`
}

// Legacy transfer storage remains a compatibility alias for G5 adapters.
type ownerTransferTransaction = ownerTransferStore

// AuthorizeRequest is the structured application seam for callers that already
// know their action and resource. Capability conversion stays at legacy edges.
func (s *Service) AuthorizeRequest(ctx context.Context, request AuthorizationRequest) error {
	return s.readWithAudit(ctx, request.Actor, request.ProjectID, request.capability(),
		TransactionOptions{RepeatableRead: true},
		func(tx Transaction) (Project, error) {
			return s.authorizeProject(ctx, tx, request)
		})
}

// authorizeProjectWithoutShadow keeps a multi-resource authorization from
// emitting one shadow event for every resource. The batch records one
// aggregate decision after all policy checks have completed.
func (s *Service) authorizeProjectWithoutShadow(ctx context.Context, tx Transaction, request AuthorizationRequest) (Project, error) {
	if !validAuthorizationAction(request.Action) {
		return Project{}, ErrInvalidRequest
	}
	if err := authorizeAPIKeyProject(ctx, tx, request.ProjectID, request.capability()); err != nil {
		return Project{}, err
	}
	return s.authorizer.AuthorizeProject(tx, request)
}

// AuthorizeResourceBatch checks a set of same-action resources in one stable
// project transaction and writes one aggregate authorization audit row. A
// chapter-scoped author may reread assets already cited by that formal chapter;
// a caller-supplied chapter ID alone never grants access to an unrelated asset.
// Researcher scopes continue to match the canonical asset resource.
//
// The aggregate audit row is written on the decision's own transaction, so a
// failed audit write refuses the batch: the caller must not act on a decision
// nobody recorded.
func (s *Service) AuthorizeResourceBatch(ctx context.Context, actor Actor, projectID string, action AuthorizationAction, resources []ResourceRef) ([]ResourceAuthorizationDecision, error) {
	if !validActor(actor) || strings.TrimSpace(projectID) != projectID || projectID == "" || !validAuthorizationAction(action) || len(resources) == 0 {
		return nil, ErrInvalidRequest
	}
	decisions := make([]ResourceAuthorizationDecision, len(resources))
	for i, resource := range resources {
		if !resource.valid() {
			return nil, ErrInvalidRequest
		}
		decisions[i] = ResourceAuthorizationDecision{Resource: resource}
	}
	var batchErr error
	err := s.repository.Transaction(ctx, TransactionOptions{RepeatableRead: true}, func(tx Transaction) error {
		if _, err := s.authorizeProjectWithoutShadow(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, "")); err != nil {
			batchErr = err
			return s.recordAuditInTransaction(ctx, tx, actor, projectID, string(action)+":asset-batch", batchErr)
		}
		for i, resource := range resources {
			if resource.Kind == ResourceAsset && resource.ChapterID != "" {
				if _, err := tx.Chapter(projectID, resource.ChapterID); err != nil {
					if errors.Is(err, ErrNotFound) {
						decisions[i].Reason = "not_found"
						batchErr = ErrNotFound
						continue
					}
					return err
				}
			}
			request := authorizationRequest(actor, projectID, action, resource.AuthorizationResource())
			_, authErr := s.authorizeProjectWithoutShadow(ctx, tx, request)
			if authErr != nil && errors.Is(authErr, ErrNotFound) && resource.Kind == ResourceAsset && resource.ChapterID != "" {
				_, chapterErr := s.authorizeProjectWithoutShadow(ctx, tx, authorizationRequest(actor, projectID, action, resource.ChapterID))
				if chapterErr == nil {
					cited, err := chapterCitesAsset(tx, actor.TenantID, projectID, resource.ChapterID, resource.ID)
					if err != nil {
						return err
					}
					if cited {
						authErr = nil
					}
				} else if !errors.Is(chapterErr, ErrNotFound) {
					return chapterErr
				}
			}
			if authErr == nil {
				decisions[i].Allowed = true
				decisions[i].Reason = "allowed"
				continue
			}
			if !errors.Is(authErr, ErrNotFound) {
				return authErr
			}
			decisions[i].Reason = "not_authorized"
			batchErr = ErrNotFound
		}
		return s.recordAuditInTransaction(ctx, tx, actor, projectID, string(action)+":asset-batch", batchErr)
	})
	if err != nil {
		return nil, err
	}
	if s.mode == AuthorizationModeLog {
		_ = s.recordAudit(ctx, actor, projectID, "shadow:"+string(action)+":asset-batch", batchErr)
	}
	if batchErr != nil && len(decisions) > 0 {
		// Project-level denial makes every result unusable. Resource-level
		// denial is represented per item and returned below as decisions.
		if decisions[0].Reason == "" {
			return nil, batchErr
		}
	}
	return decisions, nil
}

// ReassessChangeSetResult keeps reassessment on the same typed application
// boundary as apply and reject while the legacy raw method remains available.
func (s *Service) ReassessChangeSetResult(ctx context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	raw, _, replayed, err := s.ReassessChangeSet(ctx, actor, projectID, changeSetID, key)
	if err != nil {
		return ChangeSetMutationResult{Replayed: replayed}, err
	}
	change, err := decodeMutationPayload[ChangeSet](raw, "change set reassessment result")
	if err != nil {
		return ChangeSetMutationResult{Replayed: replayed}, err
	}
	return ChangeSetMutationResult{ChangeSet: change, Replayed: replayed}, nil
}

// extendedTransaction checks the compatibility edge to the existing feature
// helpers. A focused-only adapter fails closed instead of panicking.
func extendedTransaction(port any) (Transaction, error) {
	tx, ok := port.(Transaction)
	if !ok {
		return nil, ErrInvalidState
	}
	return tx, nil
}

func impactSourceDecoder[I any](tx ChapterWorkspaceTransaction, op operationDescriptor[I], _ OperationResult, _ bool) ([]string, error) {
	sourceTx, err := extendedTransaction(tx)
	if err != nil {
		return nil, err
	}
	if err := inverseOperationAccess(sourceTx, op.Request, string(op.Name), op.Target, op.Input); err != nil {
		return nil, err
	}
	return OperationSourceIDs(sourceTx, string(op.Name), op.Target, op.Request.ProjectID, op.Input)
}
func chapterCommentSourceIDs(tx ChapterWorkspaceTransaction, op operationDescriptor[ChapterCommentInput], _ OperationResult, _ bool) ([]string, error) {
	chapterID := strings.TrimPrefix(op.Target, op.Request.ProjectID+"/")
	version, err := tx.ChapterVersion(op.Request.ProjectID, chapterID, op.Input.ChapterVersionID)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), version.SourceIDs...), nil
}
func chapterReviewSourceIDs(tx ChapterWorkspaceTransaction, op operationDescriptor[ChapterReviewInput], _ OperationResult, _ bool) ([]string, error) {
	chapterID := strings.TrimPrefix(op.Target, op.Request.ProjectID+"/")
	version, err := tx.ChapterVersion(op.Request.ProjectID, chapterID, op.Input.ChapterVersionID)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), version.SourceIDs...), nil
}
func reviewAnchorSourceIDs(tx ChapterWorkspaceTransaction, op operationDescriptor[SetReviewAnchorInput], previous OperationResult, found bool) ([]string, error) {
	if found {
		copy, err := decodeMutationPayload[WorkingCopy](previous.Body, "working copy mutation result")
		if err != nil {
			return nil, markReplayPayloadDecodeError(err)
		}
		return append([]string(nil), copy.SourceIDs...), nil
	}
	chapterID := strings.Split(strings.TrimPrefix(op.Target, op.Request.ProjectID+"/"), "/")[0]
	copy, err := tx.WorkingCopy(op.Request.ProjectID, chapterID)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), copy.SourceIDs...), nil
}

type ImpactPreflightAcknowledgement struct {
	ClosedTaskIDs   []string `json:"closed_task_ids"`
	ProofID         string   `json:"proof_id"`
	ContextRevision int64    `json:"context_revision"`
}
