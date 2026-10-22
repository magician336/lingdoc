# G4 isolated implementation handoff

This package owns catalog versions, server-policy-constrained binding snapshots,
model-suggestion records, deterministic example replay, and template-upgrade
impact facts. It reuses template/ruleset.go and rulesetstore/store.go.

Production composition must supply current transaction-aware Authorizer and
rulesetstore.Authorizer ports. Nil ports fail closed. Actor identity/role comes
from authenticated server context, never a browser body.

Apply SQLiteDDL/PostgreSQLDDL through the application's versioned migration
runner; NewService does not auto-migrate. ResolveForCopy receives the caller's
project transaction and rechecks the published template and live RuleSet gates.
Persist the copy, its immutable RuleSet snapshot/source ref, and its audit in
that same project transaction.

TemplateVersion.ContentHash and Definition.ContentHash cover the existing
compiled template projection. Definition.RulesetHash is its compatibility
digest. TemplateVersion.RulesetHash/RuleSet.Hash and RuleSetSnapshot are the
independent binding version hash. EvaluateRuleSet is the sole new-set evaluator.
Do not replace a binding-version hash with the compatibility digest. Existing
template-demo/1 historical hash handling remains owned by template/delivery.

SaveDraft accepts structural definition fields, no compiled Rules or
client-supplied content/rule hash. Source references are exact versioned IDs.
For nonempty references SourceVerifier must additionally implement
VerifyTemplateSourcesInTransaction(context.Context,*gorm.DB,Actor,[]SourceRef)
so publication/save recheck grants after source IO releases its read snapshot.

ExampleReplayVerifier uses immutable authorized ReplayFixtureReader inputs.
Every enabled binding and trigger scene needs positive and negative examples;
the existing evaluator actually executes them. Missing or false expected
examples reject publication. Never install a production always-pass verifier.

SuggestionContextProvider.Build adapts the shared G2 authorized context builder.
AuthorizedContext uses AuthorizationState="allowed", exact requested source
IDs/versions, ModelUseAllowed, and UTF-8 quotation SHA-256. The small Suggest
model port adapts the same G2AgentModel and budget ledger. Model calls occur
outside DB transactions; before/after bases and grants are checked. Read/refresh
does not execute a model. Missing provider is durably failed/model_unavailable.
real_api_fake_model stays explicitly simulated; it is not real-model acceptance.

AcceptRuleSuggestion takes only human-edited proposed bindings and an unused
version of the same source RuleSet. It retains existing bindings, forbids a
blocking suggestion, creates an unpublished draft in the existing RuleSet
store, and atomically saves the human decision/audit/operation.

BuildUpgradePlan consumes server-resolved Workspace migration and G2 dependency
facts. G3 owns task identity/assignment/state and applies project values,
binding, revision, ChangeSet, ImpactTasks and audit atomically.
ValidateUpgradePlan rejects stale/tampered preview and unresolved migrations.
The caller must resolve/recheck the target publication gates again before apply.

Frontend files are independent of Workspace.vue. TemplateGovernancePanel takes
project, chapters, optional sources; emits refresh/error/dataDirty/close and
exposes hasUnsavedChanges/busy. Parent close/navigation should preserve or
confirm unsaved edits before destroying the component. It keeps template,
rule and suggestion edits separate and prevents editing during submissions.
Parent must return current_snapshot_id and exact RuleSet metadata for project
basis; absent identity/version/hash disables model request.

API routes are /api/v1/lingdoc/template-catalog/profiles and nested
profiles/:id/versions/:version/{publish,withdraw,recall}, rule-definitions,
rulesets/:id/versions/:version/{publish,withdraw,recall}, and
projects/:projectId/rule-suggestions/:runId/{accept,reject}.
All use the existing Result<T> envelope and Idempotency-Key header.

Validation: ROOT's first serial G4 domain run recorded 48 PASS / 2 PostgreSQL
SKIP across three packages. The later shared fake-mode compatibility test needs
ROOT's final focused check. Seven frontend state tests and Vue SFC
parse/script/template compile passed using existing read-only repo dependencies.
Full frontend type-check, real browser interaction, real PostgreSQL and real
model/Word acceptance are separate checks and have not been established here.


Actual production composition is now provided by
workspace.NewTemplateGovernanceRuntime(db,coreService,sourceService,g2Model,...options).
It returns (*TemplateGovernanceRuntime,error); Service() supplies catalog and
RuleSet lifecycle methods, Resolve implements core.TemplateCatalogResolver, and
ImpactRuleChecker() reads current chapters/confirmation decisions/source grants
inside the caller transaction. It does not call public Authorize from that tx.

Reviewed BindingPolicies are draft-editable only under the current curator
permission and become immutable with the exact catalog version. policy_hash is
computed independently by HashBindingPolicies and belongs in project copy
sidecars as BindingPolicies/SourcePolicyHash. Missing policy grants nothing;
mandatory blocking bindings cannot change. Optional severity may return to a
reviewed source ceiling for inverse ChangeSets. All policy parameter changes
remain under the existing declarative RuleSet validator.

Rule catalog APIs now wrap existing rulesetstore under the governance parent
transaction and operation ledger: CreateRuleSetDraft, PublishRuleSet,
WithdrawRuleSet, RecallRuleSet, GetRuleSetVersion, ListRuleSetVersions.
Exact request retries recheck current authorization and current result state;
mutation, existing-store audit, governance audit and operation receipt commit
or roll back together. There is no second RuleSet table.

The runtime registers 20 immutable real evaluator fixtures for the exact
builtin template-demo/1 and ruleset-demo/1. Their IDs are
`demo-<existing-rule-id>-<write_check|preflight>-<positive|negative>`;
existing rule IDs are required-fields, chapter-nonempty, chapter-confirmed,
review-items-decided, source-available. These are explicit synthetic software
fixtures, not research or model acceptance. No catalog data is auto-seeded.
The first RuleSet publication may use the existing fixed demo template as
context. Custom content or policy requires WithReviewedTemplateFixtures
registered on the server, pinned to content, policy and RuleSet hashes.
Unknown client example IDs or incorrect expected outcomes fail closed.

Source validation has two phases: outside-tx real SourceApplication.GetSource,
then parent-tx current tenant/project/KB ownership, current asset revision and
fingerprint, original locator/quote, and G2 import consent checks. It preserves
Caller API-key scopes. Cross-tenant shares cannot be safely revalidated through
the current external share port in a single-connection parent transaction, so
this first pass refuses that scope instead of fabricating allowed grants.
Source-free and same-tenant currently authorized contexts use the real G2
BuildAgentContext and injected shared G2AgentModel/budget once. Unconfigured
models remain unavailable; real_api_fake_model remains explicitly simulated.

New runtime/policy/lifecycle tests are pending ROOT's serial Go run. Production
PostgreSQL, remote shares, paid-model behavior and browser/E2E acceptance are
not established by the earlier 48-test domain evidence.


The frozen delivery chain now carries the project RuleSetSnapshot,
TemplateCopyRulesetHash, SourceRuleSetID/Version/Hash and reviewed policy snapshot.
The template definition still holds its compiled digest. Candidate delivery
reads the exact template_binding sidecar inside its own parent transaction;
DeliveryInputBuilder verifies both identities, and the existing evaluator runs
write_check for editing or preflight for frozen delivery. SQLite snapshot
serialization preserves all copy metadata and binding content. Frozen receipt
checks recompute the canonical digest and actual result before export; a saved
passed flag cannot replace those checks. CheckResult.receipt_id is optional
server workflow metadata, not a client assertion of evaluation success.

RouteEvaluationFields is the sole G3 route/spec projection used by the runtime
checker and production frozen input reader. Required-route lifecycle blockers
remain owned by G3; a route's reviewed feasibility does not become a scientific
confirmation. sameDeliveryState now also compares exact copy/effective rule
and source-policy identities, rather than relying only on chapter versions.

RegisterTemplateGovernanceRoutes(group,runtime) adds actual typed catalog, RuleSet and
suggestion endpoints to the already-authenticated LingDoc group. It reuses the
existing caller/body/operation/response helpers. Publication, withdrawal,
recall, acceptance and rejection require a human principal; API keys cannot
perform those decisions. The operation observer reports replayed=true only
when the stored request hash matched; it does not grant authority.

Rule suggestions must select the exact current project's template ID/version,
with the project basis and source-catalog RuleSet both pinned. Another catalog
cannot mix its target/definition with the project's G2 context. Proposed model
rules remain nonbinding until human acceptance creates an unpublished existing
RuleSet-store draft. BindingOverride.remove_parameter_keys supports reviewed
inverse ChangeSets, with unknown/duplicate/delete-plus-edit keys rejected.

Final branch-local frontend static check: Vue SFC parse/script/template PASS;
7 state behavior tests PASS using existing read-only dependencies. Go files
are frozen for ROOT's serial complete compile/test run. No real paid model,
real PostgreSQL, remote-share transaction reader, browser E2E, publishing,
remote PR, or merge was performed by this branch.
