package workspacecore

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// The stabilization audit record.
//
// An authorized Owner may complete a stabilization through the API without
// being personally present in the UI. What the server owes the record is
// therefore NOT a claim that a person clicked a button, but a truthful account
// of who acted, how they were authorized, through which channel, and what the
// machine actually decided.
//
// Three things this record must never say:
//
//   - that the Owner "was present at the UI". The server sees a credential
//     class, not a person in front of a screen.
//   - that a human judged the blockers. The verdict is computed.
//   - that a fact it cannot prove is verified. An unprovable fact is recorded
//     as unprovable.
//
// And one thing it must always carry: the executing subject, the authorization
// that let it act, the channel, the moment, the object, the evidence the
// verdict leaned on, and a correlation id that ties the record to the audit row
// and to the request that produced it.

// stabilizationAuditExecutor is the real subject that performed the operation.
type stabilizationAuditExecutor struct {
	UserID   string `json:"user_id"`
	TenantID uint64 `json:"tenant_id"`
	Role     string `json:"role"`
}

// stabilizationAuditAuthorization is how the executing subject was allowed to
// act, stated as the checks that were actually run.
type stabilizationAuditAuthorization struct {
	// Method names the authorization path that was taken.
	Method string `json:"method"`
	// Capability is the capability string the authorization was asked for.
	Capability string `json:"capability"`
	// ProjectOwner reports whether the executing subject is the project's
	// Owner. Stabilization additionally requires it.
	ProjectOwner bool `json:"project_owner"`
	// HumanDecisionGate reports the outcome of the human-decision gate.
	//
	// The gate's criterion is "this caller is not a machine principal". The
	// field reports THAT criterion and its outcome. It does not report that a
	// person was present: a signed-in session's credential passes the gate
	// whether a person or a script presented it.
	HumanDecisionGate string `json:"human_decision_gate"`
}

// stabilizationAuditChannel classifies the channel the operation came through.
type stabilizationAuditChannel struct {
	// Class is the credential class of the caller, which is the most the
	// server can prove about the channel.
	Class string `json:"class"`
	// PrincipalType and PrincipalID name the credential the request carried.
	PrincipalType string `json:"principal_type"`
	PrincipalID   string `json:"principal_id"`
	// UIOrigin says whether the request can be attributed to the UI. The
	// server cannot tell a browser click from a script replaying the same
	// session credential, so this is "unprovable" unless something stronger
	// than a credential exists.
	UIOrigin string `json:"ui_origin"`
	// Automation says whether the request can be attributed to a person
	// rather than to a scheduled or scripted run. Same limit, same answer.
	Automation string `json:"automation"`
	// Limitation states the capability boundary in words, so a reader does
	// not have to infer it from the fields above.
	Limitation string `json:"limitation"`
}

// stabilizationAuditTarget is the object the operation acted on.
type stabilizationAuditTarget struct {
	ProjectID       string `json:"project_id"`
	ChangeSetID     string `json:"change_set_id"`
	ContextRevision int64  `json:"context_revision"`
}

// stabilizationAuditEvidence is what the verdict leaned on.
type stabilizationAuditEvidence struct {
	ClosureEvidence []ObjectVersionRef `json:"closure_evidence"`
	PreflightProofs []string           `json:"preflight_proof_ids"`
	RepostedTaskIDs []string           `json:"reposted_task_ids"`
}

// stabilizationAuditDecision is the verdict and where it came from.
type stabilizationAuditDecision struct {
	Verdict string `json:"verdict"`
	// VerdictSource names what produced the verdict. It is a machine
	// evaluation, and the record says so rather than dressing it up as a
	// human judgement.
	VerdictSource string          `json:"verdict_source"`
	Blockers      []ImpactBlocker `json:"blockers"`
}

// stabilizationAuditRecord is the domain audit record of one stabilization.
type stabilizationAuditRecord struct {
	Executor           stabilizationAuditExecutor      `json:"executor"`
	Authorization      stabilizationAuditAuthorization `json:"authorization"`
	Channel            stabilizationAuditChannel       `json:"channel"`
	OccurredAt         time.Time                       `json:"occurred_at"`
	Target             stabilizationAuditTarget        `json:"target"`
	Evidence           stabilizationAuditEvidence      `json:"evidence"`
	Decision           stabilizationAuditDecision      `json:"decision"`
	AuditCorrelationID string                          `json:"audit_correlation_id"`
	RequestID          string                          `json:"request_id,omitempty"`
}

// stabilizationChannelClass values. They name the CREDENTIAL class of the
// caller, which is the strongest channel fact the server holds.
const (
	// stabilizationChannelUserSession is a signed-in web session. It does not
	// say whether a person or a script produced the request.
	stabilizationChannelUserSession = "user_session"
	// stabilizationChannelAPIKey is a machine credential. Stabilization
	// refuses these before they can act, so a record with this class can
	// only exist for a refused attempt.
	stabilizationChannelAPIKey = "api_key"
	// stabilizationChannelIM is an IM identity.
	stabilizationChannelIM = "im"
	// stabilizationChannelEmbed is an embed identity.
	stabilizationChannelEmbed = "embed"
	// stabilizationChannelUnbound is a call that arrived without an
	// authenticated principal in its context - a direct domain call, not an
	// HTTP request.
	stabilizationChannelUnbound = "unbound_context"
	// stabilizationChannelUnknown is a principal of a class this server does
	// not know.
	stabilizationChannelUnknown = "unknown_principal"
)

// stabilizationUnprovable is the value used for a fact the server cannot
// prove. It is a value, not an omission: a missing field would read as "not
// applicable", and this one reads as "not knowable from here".
const stabilizationUnprovable = "unprovable"

// stabilizationChannel classifies the channel the operation came through,
// using only what the server can actually prove.
//
// What the server proves: the credential class of the caller (a signed-in web
// session, a tenant or platform API key, an IM identity, an embed identity)
// and, for a machine credential, the fact that the operation was refused
// before it could act.
//
// What the server canNOT prove: whether a request carrying a signed-in
// session's credential was made by a person clicking in the UI or by a script
// replaying the same credential, and whether an API call was interactive or
// scheduled. Those two are recorded as "unprovable" rather than guessed.
//
// The principal is read from the context VALUE, not through
// types.PrincipalFromContext: that helper synthesizes a "web_user" principal
// from a bare user id, which would make a direct domain call look exactly
// like a browser session. The distinction is the whole point of this record,
// so it must not be manufactured.
func stabilizationChannel(ctx context.Context) stabilizationAuditChannel {
	channel := stabilizationAuditChannel{
		Class:         stabilizationChannelUnbound,
		PrincipalType: stabilizationUnprovable,
		UIOrigin:      stabilizationUnprovable,
		Automation:    stabilizationUnprovable,
		Limitation:    "调用上下文没有绑定主体凭据，无法证明该操作由谁、从哪个界面或以何种自动化方式发起。",
	}
	principal, ok := ctx.Value(types.PrincipalContextKey).(types.Principal)
	if !ok || !principal.Valid() {
		return channel
	}
	channel.PrincipalType = principal.Type
	channel.PrincipalID = principal.ID
	switch principal.Type {
	case types.PrincipalWebUser:
		channel.Class = stabilizationChannelUserSession
		channel.Limitation = "服务端只能证明调用携带已登录会话凭据，无法区分是界面点击还是持同一凭据的脚本或自动化调用。"
	case types.PrincipalIMUser:
		channel.Class = stabilizationChannelIM
		channel.Limitation = "服务端只能证明调用携带 IM 身份，无法区分人工发起与 IM 侧自动化。"
	case types.PrincipalAPITenant, types.PrincipalAPIPlatform, types.PrincipalAPIExternalUser:
		channel.Class = stabilizationChannelAPIKey
		channel.Limitation = "服务端只能证明调用携带 API 密钥凭据，无法区分交互式调用与定时或脚本调用。"
	case types.PrincipalEmbedChannel, types.PrincipalEmbedSession, types.PrincipalEmbedVisitor:
		channel.Class = stabilizationChannelEmbed
		channel.Limitation = "服务端只能证明调用携带嵌入身份，无法区分人工与自动化。"
	default:
		channel.Class = stabilizationChannelUnknown
		channel.Limitation = "未识别的凭据类别，无法证明调用渠道。"
	}
	return channel
}

// stabilizationAuditComplete reports whether the record carries every fact the
// audit must have.
//
// A record missing one of them is not written: the operation is refused
// instead, because an audit that cannot say who acted is a missing audit, not
// a weaker one - and a stabilization without it would leave the revision
// frozen on nobody's authority.
func stabilizationAuditComplete(record stabilizationAuditRecord) bool {
	return record.Executor.UserID != "" &&
		record.Authorization.Method != "" &&
		record.Authorization.Capability != "" &&
		record.Authorization.HumanDecisionGate != "" &&
		record.Channel.Class != "" &&
		record.Channel.PrincipalType != "" &&
		!record.OccurredAt.IsZero() &&
		record.Target.ProjectID != "" &&
		record.Target.ChangeSetID != "" &&
		record.Decision.Verdict != "" &&
		record.Decision.VerdictSource != "" &&
		record.AuditCorrelationID != ""
}

// writeStabilizationAudit writes the record into the project's domain audit
// log, in the same transaction as the stabilization itself.
//
// The audit row's own columns carry the facts a reader must be able to
// rebuild without parsing the details JSON: who acted (user_id, actor_id),
// with which role, under which capability, and what was decided.
func writeStabilizationAudit(tx Transaction, actor Actor, record stabilizationAuditRecord, details map[string]any) error {
	audit, ok := tx.(auditTransaction)
	if !ok {
		return ErrInvalidState
	}
	return audit.RecordAudit(AuditEvent{
		ID:              record.AuditCorrelationID,
		TenantID:        actor.TenantID,
		ProjectID:       record.Target.ProjectID,
		UserID:          actor.UserID,
		ActorID:         actor.UserID,
		Role:            actor.Role,
		Action:          "revision.stabilize",
		Target:          record.Target.ChangeSetID,
		ProjectRevision: record.Target.ContextRevision,
		Capability:      record.Authorization.Capability,
		Decision:        record.Decision.Verdict,
		Details:         details,
		CreatedAt:       record.OccurredAt,
	})
}
