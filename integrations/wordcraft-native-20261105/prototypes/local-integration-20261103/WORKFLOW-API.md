# Controlled writing 0.3 workflow extension

This extension leaves DOCX as the only body authority. It stores proposed text and operation history, not a parallel editable document. Figure operations are out of scope.

## Integration

`createWorkflowStore({directory,chain,model,latestVersion})` exposes `read()` and serialized `act(input)`. `createWorkflowModel({goOrigin,origin,projectId,sources})` reuses local Go G2 authorization/session/turn APIs. No provider secrets enter the browser or this adapter.

GET `/integration/workflow` returns `{revision,candidates}`. POST uses the existing same-origin token guard and returns `{revision,candidates,candidate}`. Every mutation requires a stable `key` (8–100 ASCII letters, digits, underscore or hyphen). Reusing a key with different input fails. A replay of preflight always rechecks current context.

| type | required input | effect |
| --- | --- | --- |
| generate | expectedChainRevision, target, instruction, sourceVersionIds, includeComment boolean, contextConfirmed=true | Freeze selected context, create isolated Go session, persist request keys, return proposed candidate only |
| preflight | candidateId, target | Check frozen source/comment and exact target; mark applying, return operationId=candidateId |
| applied | candidateId, receipt | Record matching native receipt; require current revision still matches |
| saved | candidateId, receipt, savedVersion | Associate newest Go save hash/version with still-current native receipt |
| reject | candidateId | Reject unexecuted proposed/failed/stale candidate |
| undone | candidateId, receipt | Record confirmed native undo; disallow subsequent saved association |

`target={documentId,revision,anchor,focus,text,hostSessionId}`. Native receipt requires operationId, status, documentId, beforeRevision, afterRevision, beforeText, afterText, matchesCurrentRevision. Save input is `{diskVersion,docSha256,nativeDocumentId,nativeRevision}`. Browser host identity checks and atomic engine checks remain mandatory; backend receipts do not independently attest a browser's runtime.

Candidate fields: id/status/target/original/replacement/instruction/context/citations/modelReceipt/nativeReceipt/savedVersion/history. Statuses are generating, proposed, failed, stale, applying, applied, saved, rejected, undone. Private modelOperations journal is not returned to the UI.

## Limits and recovery

- Every new generation binds the chain revision actually confirmed by the user. A retry retains frozen context and original Go keys; it never silently substitutes new source or comment text.
- Sources are fetched from Go and exact text/digest checked before generation and acceptance. Selected source versions and source-head changes invalidate an outstanding candidate. User-owned native target checks prevent stale writes.
- Go's existing source policy is a persistent project-asset model-use permission. The UI must disclose that grant. Actual per-request model context remains only explicitly selected excerpts, the selected body text, the instruction and opted-in comment. Each candidate gets a separate session, without unrelated history.
- The Go response must be a completed real-model turn; source citations must match supplied original text. Failed/unknown turns cannot become adoptable candidates. Retrying replays the same Go operation, not a fresh hidden model call. Provider-level exactly-once is not claimed.
- A native operation can succeed while recording its receipt fails. Keep applying and query operationStatus; never blindly repeat replacement. A reopened editor loses the in-memory native receipt map; unknown is not proof that an old operation never ran.
- A saved association does not automatically close an opinion/review task or certify semantic correctness. The user still records the actual decision. Changes or undo after the native operation require human reconciliation.
- Durable local snapshots use atomic JSON writes; transient Windows file locks have a bounded retry of the identical snapshot only. Failure restores last durable in-memory state.

## Verification

`node --test workflow-store.test.mjs workflow-model.test.mjs`: 18 tests passed after fixes, including changed sources/comments, preflight replay, undo, wrong receipt/save, missing consent, fresh-context binding, failed disk writes and model checkpoint recovery. Model tests inject the transport and are not real-provider acceptance.

Real probe `verify-workflow-model.mjs` creates an isolated runtime on 7223 and uses only fictional seed material. It does not edit native DOCX. First real run produced a Go timeout; second no-source minimal run produced provider_failure. These failed first outputs remain under `artifacts/workflow-model/`. Further diagnostic/acceptance results supersede neither original failure nor native/UI verification requirements.

Safe diagnostics subsequently verified authenticated model listing and basic Go transport. Replaying the exact serialized fictional G2 request through the same SSRF transport returned HTTP 503 / service_unavailable_error. See `artifacts/workflow-model/diagnostic-metadata.json`. No successful full workflow is claimed.

At 2026-11-03 10:41 UTC, a single later full-context recovery probe succeeded: real G2 candidate, one source and the manual opinion, verified citation, 456 input / 66 output tokens. Evidence: `artifacts/workflow-model/2026-11-03T10-41-19-077Z/report.json`. Initial failures remain unchanged. Native application/save/reopen still require independent browser acceptance.
