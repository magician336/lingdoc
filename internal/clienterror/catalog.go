package clienterror

// The catalog holds the outward forms that more than one domain answers with.
//
// A form is shared only when the status code, the error code and the message
// are all identical. Several forms share an error code but differ in wording —
// for example three wordings for `invalid_state`, two for `version_conflict`,
// two for `stale_input` and two for `dependency_unavailable`. Those are
// distinct forms and are never merged into one sentence: the wording a caller
// reads is part of the contract.
//
// The catalog lives in this package on purpose. Every consumer already
// imports `clienterror`, so a shared form costs no new dependency edge.
//
// A domain that needs a different wording for an error another surface already
// owns does not edit the sentinel. It declares a named scenario value that
// wraps the sentinel and carries its own form.
var (
	// NotFound is the answer for a resource that does not exist or is not
	// reachable by the caller.
	NotFound = Form{Status: 404, Code: "not_found", Message: "资源不存在或不可访问。", Retryable: false}

	// InvalidRequest is the answer for a request whose fields do not follow
	// the agreed shape.
	InvalidRequest = Form{Status: 400, Code: "invalid_request", Message: "请求字段不符合约定。", Retryable: false}

	// IdempotencyConflict is the answer for one operation key used by two
	// different requests.
	IdempotencyConflict = Form{Status: 409, Code: "idempotency_conflict", Message: "同一个操作键对应不同请求。", Retryable: false}

	// IdempotencyKeyConflict is the answer for one idempotency key used by
	// two different requests. It shares the error code with
	// IdempotencyConflict and keeps its own wording.
	IdempotencyKeyConflict = Form{Status: 409, Code: "idempotency_conflict", Message: "同一幂等键对应不同请求。", Retryable: false}

	// IdempotencyKeyExpired is the answer for one idempotency key that was
	// issued under an older request digest rule. The request itself is
	// fine -- the key belongs to a rule this build no longer speaks, so the
	// caller replaces the key instead of looking for a request that never
	// changed.
	IdempotencyKeyExpired = Form{Status: 409, Code: "idempotency_key_expired", Message: "该幂等键属于旧版摘要规则，请换一个新的幂等键重试。", Retryable: false}

	// VersionConflict is the answer for content that changed underneath the
	// caller.
	VersionConflict = Form{Status: 409, Code: "version_conflict", Message: "内容已变化，请先读取当前版本。", Retryable: false}

	// BaselineConflict is the answer for content or a baseline that changed
	// underneath the caller. It shares the error code with VersionConflict
	// and keeps its own wording.
	BaselineConflict = Form{Status: 409, Code: "version_conflict", Message: "内容或基线已变化，请先读取当前版本。", Retryable: false}

	// RuleSetStateNotAllowed is the answer for a rule set whose current state
	// does not allow the requested operation.
	RuleSetStateNotAllowed = Form{Status: 409, Code: "invalid_state", Message: "当前版本状态不允许此操作。", Retryable: false}

	// StageNotAllowed is the answer for an operation the current stage does
	// not allow. It shares the error code with the other invalid_state forms
	// and keeps its own wording.
	StageNotAllowed = Form{Status: 422, Code: "invalid_state", Message: "当前阶段不能执行该动作。", Retryable: false}

	// Forbidden is the answer for a caller that may not perform the action.
	Forbidden = Form{Status: 403, Code: "forbidden", Message: "无权执行该动作。", Retryable: false}

	// SourceAccessDenied is the answer for a source whose authorization is
	// gone.
	SourceAccessDenied = Form{Status: 403, Code: "source_access_denied", Message: "资料授权已不可用。", Retryable: false}

	// Unauthenticated is the answer for a request without a usable identity.
	Unauthenticated = Form{Status: 401, Code: "unauthenticated", Message: "请登录后重试。", Retryable: false}

	// DependencyUnavailable is the answer for a dependency that is not
	// configured or not ready.
	DependencyUnavailable = Form{Status: 503, Code: "dependency_unavailable", Message: "依赖暂时不可用。", Retryable: true}

	// CandidateStaleInput is the answer for a candidate or snapshot that was
	// built on an input that has changed since.
	CandidateStaleInput = Form{Status: 409, Code: "stale_input", Message: "候选稿或快照基于旧输入。", Retryable: false}

	// PreflightBlocked is the answer for a request that has to clear the
	// delivery blockers first.
	PreflightBlocked = Form{Status: 422, Code: "preflight_blocked", Message: "请先处理交付阻断项。", Retryable: false}

	// InvalidModelOutput is the answer for a model output that does not fit
	// the current goal and the rule constraints. It is the only 502 in the
	// catalog and it used to be a private branch of one transport.
	InvalidModelOutput = Form{Status: 502, Code: "invalid_model_output", Message: "模型输出不符合当前目标和规则约束。", Retryable: true}

	// InternalError is the answer for a failure that declares no form of its
	// own: the transport's fallback.
	InternalError = Form{Status: 500, Code: "internal_error", Message: "服务器暂时无法完成请求。", Retryable: true}

	// OperationFailed is the fallback answer of the surfaces that report a
	// failed operation in their own words.
	OperationFailed = Form{Status: 500, Code: "internal_error", Message: "操作失败，请稍后再试。", Retryable: true}
)
