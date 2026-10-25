package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// The manual-judgement channel over the real HTTP seam (issue #649).
//
// The domain side of this channel (who may record a judgement, what is
// written down, how the channel is classified, where the audit record
// lives) is another work line's, and it is the authority for the shapes
// below: the authoritative record and the export record are the domain
// side's own types, and the transport publishes them as they are.
//
// The evidence here is therefore about the transport: the routes, the
// envelope, the error mapping, and above all the **shape** of the two
// answers - because the runner compares the ledger against the export byte
// for byte, and that comparison is the whole point of the channel.
//
// The fake below answers in exactly the shape the domain side answers in,
// and it answers from one stored string per fact: that is what makes the
// byte-for-byte agreement below a property of the transport rather than a
// coincidence of two independently written literals.

const (
	manualJudgementRunID       = "f01-20261025T013341Z-5105ae32"
	manualJudgementAssertionID = "F01-06c"
	manualJudgementProse       = "变更集评估后才允许应用；服务端的合法前态只有 assessed。"
	manualJudgementEvidence    = "run:f01-20261025T013341Z-5105ae32/f01-report.json"
	manualJudgementActor       = "user-owner"
	manualJudgementAuthority   = "project_owner"
	// The channel the server can actually prove: a credential class.  A UI
	// click and a script replay are the same credential, so the server does
	// not claim to tell them apart.
	manualJudgementChannel = "unverified_channel"
	// The two channel layers the server cannot prove at all.
	manualJudgementUnprovable = "unprovable"
	// A timestamp that is not the canonical form on purpose: the export has
	// to echo the string that was written down, not a reformatted one.
	manualJudgementOccurredAt = "2026-10-25T01:33:41.5105+00:00"
	manualJudgementAuditID    = "audit-649-0001"
)

// manualJudgementFake is the domain side of the channel, reduced to the two
// methods the transport asks for.
//
// It stores one string per fact and projects the export out of the same
// strings, so the export and the authoritative record cannot drift apart in
// the fake itself.
type manualJudgementFake struct {
	records []core.ManualJudgementAudit
	stored  []core.ManualJudgementRecord
	err     error
	calls   int
	key     string
	input   core.ManualJudgementInput
	actor   core.Actor
}

func (f *manualJudgementFake) SubmitManualJudgement(ctx context.Context, actor core.Actor, projectID, key string, input core.ManualJudgementInput) (json.RawMessage, int, bool, error) {
	f.calls++
	f.actor = actor
	f.key = key
	f.input = input
	if f.err != nil {
		return nil, 0, false, f.err
	}
	record := core.ManualJudgementRecord{
		RunID:         input.RunID,
		AssertionID:   input.AssertionID,
		AssertionText: input.AssertionText,
		Verdict:       input.Verdict,
		Actor:         manualJudgementActor,
		Authorization: manualJudgementAuthority,
		Channel:       manualJudgementChannel,
		OccurredAt:    manualJudgementOccurredAt,
		EvidenceRef:   input.EvidenceRef,
		AuditID:       manualJudgementAuditID,
	}
	f.stored = append(f.stored, record)
	f.records = append(f.records, core.ManualJudgementAudit{
		AuditID:           record.AuditID,
		Actor:             record.Actor,
		Authorization:     record.Authorization,
		Channel:           record.Channel,
		OccurredAt:        record.OccurredAt,
		AssertionID:       record.AssertionID,
		RunID:             record.RunID,
		EvidenceRef:       record.EvidenceRef,
		AssertionText:     record.AssertionText,
		ChannelClass:      "user_session",
		ChannelUIOrigin:   manualJudgementUnprovable,
		ChannelAutomation: manualJudgementUnprovable,
	})
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, 0, false, err
	}
	return json.RawMessage(raw), http.StatusCreated, false, nil
}

func (f *manualJudgementFake) ExportManualJudgements(ctx context.Context, actor core.Actor, projectID string) ([]core.ManualJudgementAudit, error) {
	if f.err != nil {
		return nil, f.err
	}
	// The domain side answers with an empty list rather than with nothing,
	// so an empty project publishes an empty array.
	if f.records == nil {
		return []core.ManualJudgementAudit{}, nil
	}
	return f.records, nil
}

// manualJudgementEngine registers the two routes over one engine, the way
// the router registers them: a read group and a write group.
func manualJudgementEngine(t *testing.T, app ManualJudgementApplication) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterManualJudgementRoutes(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")}, app)
	return engine
}

// manualJudgementContext is the caller context of an authenticated web user.
func manualJudgementContext(userID string) context.Context {
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, userID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
	return context.WithValue(ctx, types.RequestIDContextKey, "manual-judgement-http")
}

func manualJudgementServe(t *testing.T, engine *gin.Engine, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(manualJudgementContext("user-owner"))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

// manualJudgementBody is the request a caller may send: five fields, none of
// which says who judged.
func manualJudgementBody() string {
	return `{"assertion_id":"` + manualJudgementAssertionID + `",` +
		`"assertion_text":"` + manualJudgementProse + `",` +
		`"verdict":"passed",` +
		`"run_id":"` + manualJudgementRunID + `",` +
		`"evidence_ref":"` + manualJudgementEvidence + `"}`
}

// manualJudgementEnvelope decodes the success envelope of one answer.
func manualJudgementEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope %q: %v", recorder.Body.String(), err)
	}
	return envelope
}

// manualJudgementError decodes the failure envelope of one answer.
func manualJudgementError(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Error     map[string]any `json:"error"`
		RequestID string         `json:"request_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope %q: %v", recorder.Body.String(), err)
	}
	if envelope.Error == nil {
		t.Fatalf("the failure carries no error object: %s", recorder.Body.String())
	}
	return envelope.Error
}

// jsonTags names the JSON fields a Go struct carries.
func jsonTags(value any) map[string]bool {
	fields := map[string]bool{}
	kind := reflect.TypeOf(value)
	for index := 0; index < kind.NumField(); index++ {
		fields[kind.Field(index).Tag.Get("json")] = true
	}
	return fields
}

// The write route answers with the authoritative record: the ten fields the
// runner's ledger stores, and nothing else.  An answer that carried an
// eleventh field could not be filed as a ledger line without being rewritten
// on the way in.
func TestManualJudgementHTTPPostAnswersTheAuthoritativeRecord(t *testing.T) {
	fake := &manualJudgementFake{}
	engine := manualJudgementEngine(t, fake)

	response := manualJudgementServe(t, engine, http.MethodPost, "/api/projects/project-1/manual-judgements",
		manualJudgementBody(), "manual-judgement-http-1")
	if response.Code != http.StatusCreated {
		t.Fatalf("record judgement = %d, want 201: %s", response.Code, response.Body.String())
	}
	envelope := manualJudgementEnvelope(t, response)
	for _, key := range []string{"data", "request_id", "meta"} {
		if _, ok := envelope[key]; !ok {
			t.Fatalf("the answer carries no %q: %s", key, response.Body.String())
		}
	}
	meta, ok := envelope["meta"].(map[string]any)
	if !ok || meta["replayed"] != false || meta["refresh_required"] != false {
		t.Fatalf("meta = %v, want replayed=false and refresh_required=false", envelope["meta"])
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %v, want the authoritative record", envelope["data"])
	}
	want := []string{"run_id", "assertion_id", "assertion_text", "verdict", "actor",
		"authorization", "channel", "occurred_at", "evidence_ref", "audit_id"}
	got := make([]string, 0, len(data))
	for key := range data {
		got = append(got, key)
	}
	if len(got) != len(want) {
		t.Fatalf("the authoritative record carries %d fields (%v), want the %d ledger fields", len(got), got, len(want))
	}
	for _, key := range want {
		if _, ok := data[key]; !ok {
			t.Fatalf("the authoritative record carries no %q: %v", key, got)
		}
	}
	if data["run_id"] != manualJudgementRunID || data["assertion_id"] != manualJudgementAssertionID ||
		data["assertion_text"] != manualJudgementProse || data["verdict"] != "passed" ||
		data["actor"] != manualJudgementActor || data["authorization"] != manualJudgementAuthority ||
		data["channel"] != manualJudgementChannel || data["occurred_at"] != manualJudgementOccurredAt ||
		data["evidence_ref"] != manualJudgementEvidence || data["audit_id"] != manualJudgementAuditID {
		t.Fatalf("the authoritative record = %v, want the stored strings", data)
	}
	// The write went through with the caller's key and the caller's five
	// fields; nothing else was invented on the way in.
	if fake.key != "manual-judgement-http-1" {
		t.Fatalf("the idempotency key = %q", fake.key)
	}
	if fake.actor.TenantID != 7 || fake.actor.UserID != "user-owner" {
		t.Fatalf("the actor = %+v, want the authenticated caller", fake.actor)
	}
	if fake.input.AssertionID != manualJudgementAssertionID || fake.input.Verdict != "passed" ||
		fake.input.RunID != manualJudgementRunID || fake.input.EvidenceRef != manualJudgementEvidence {
		t.Fatalf("the input = %+v, want the five fields the caller sent", fake.input)
	}
}

// A caller may not say who judged, through which channel, when, or under
// which audit identifier.  The request is refused outright, and the domain
// side is never reached: a body that carries one of those fields is not a
// request the server can believe.
func TestManualJudgementHTTPPostRefusesACallerSuppliedAuthority(t *testing.T) {
	for _, field := range []string{"actor", "authorization", "channel", "occurred_at", "audit_id"} {
		t.Run(field, func(t *testing.T) {
			fake := &manualJudgementFake{}
			engine := manualJudgementEngine(t, fake)
			body := `{"assertion_id":"` + manualJudgementAssertionID + `",` +
				`"assertion_text":"` + manualJudgementProse + `",` +
				`"verdict":"passed",` +
				`"run_id":"` + manualJudgementRunID + `",` +
				`"evidence_ref":"` + manualJudgementEvidence + `",` +
				`"` + field + `":"caller-supplied"}`

			response := manualJudgementServe(t, engine, http.MethodPost, "/api/projects/project-1/manual-judgements",
				body, "manual-judgement-http-"+field)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("a self-attributed judgement = %d, want 400: %s", response.Code, response.Body.String())
			}
			failure := manualJudgementError(t, response)
			if failure["code"] != "invalid_request" || failure["retryable"] != false {
				t.Fatalf("failure = %v, want an invalid_request that is not retryable", failure)
			}
			if fake.calls != 0 {
				t.Fatalf("the refused request reached the domain side %d times", fake.calls)
			}
		})
	}
}

// The request type itself carries only the five fields a caller may send.
//
// The refusal above is the transport's strict decoder at work; this is the
// shape that makes the refusal possible: the request type has no field for
// an actor, a channel, a timestamp or an audit identifier, so a caller
// cannot name them even if the decoder were lenient.
func TestManualJudgementHTTPRequestTypeNamesOnlyTheFiveCallerFields(t *testing.T) {
	fields := jsonTags(core.ManualJudgementInput{})
	if len(fields) != 5 {
		t.Fatalf("the request type carries %d fields (%v), want the five a caller may send", len(fields), fields)
	}
	for _, forbidden := range []string{"actor", "authorization", "channel", "occurred_at", "audit_id"} {
		if fields[forbidden] {
			t.Fatalf("the request type carries %q: a caller could name it", forbidden)
		}
	}
	for _, wanted := range []string{"assertion_id", "assertion_text", "verdict", "run_id", "evidence_ref"} {
		if !fields[wanted] {
			t.Fatalf("the request type carries no %q: %v", wanted, fields)
		}
	}
}

// The export record is the domain side's own type, and its JSON fields are
// the twelve the runner, the auditor and the contract read: the seven audit
// fields and the audit identifier at the top level, the quote the judgement
// rests on, and the three channel keys.
func TestManualJudgementHTTPExportRecordCarriesTheDomainFields(t *testing.T) {
	fields := jsonTags(core.ManualJudgementAudit{})
	for _, wanted := range []string{"audit_id", "actor", "authorization", "channel", "occurred_at",
		"assertion_id", "run_id", "evidence_ref", "assertion_text", "channel_class",
		"channel_ui_origin", "channel_automation"} {
		if !fields[wanted] {
			t.Fatalf("the export record type carries no %q: %v", wanted, fields)
		}
	}
	if len(fields) != 12 {
		t.Fatalf("the export record type carries %d fields (%v), want twelve", len(fields), fields)
	}
}

// The export carries the seven audit fields at the top level, next to the
// audit identifier the judgement names.  A record that hid them one level
// down would leave the runner with nothing to compare.
func TestManualJudgementHTTPExportCarriesTheAuditFieldsAtTheTopLevel(t *testing.T) {
	fake := &manualJudgementFake{}
	engine := manualJudgementEngine(t, fake)
	recorded := manualJudgementServe(t, engine, http.MethodPost, "/api/projects/project-1/manual-judgements",
		manualJudgementBody(), "manual-judgement-http-2")
	if recorded.Code != http.StatusCreated {
		t.Fatalf("record judgement = %d, want 201: %s", recorded.Code, recorded.Body.String())
	}

	response := manualJudgementServe(t, engine, http.MethodGet,
		"/api/projects/project-1/manual-judgements/audit-records", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("export audit records = %d, want 200: %s", response.Code, response.Body.String())
	}
	envelope := manualJudgementEnvelope(t, response)
	records, ok := envelope["data"].([]any)
	if !ok {
		t.Fatalf("data = %v, want an array of records", envelope["data"])
	}
	if len(records) != 1 {
		t.Fatalf("the export holds %d records, want 1", len(records))
	}
	record, ok := records[0].(map[string]any)
	if !ok {
		t.Fatalf("the export record = %v, want an object", records[0])
	}
	top := []string{"audit_id", "actor", "authorization", "channel", "occurred_at",
		"assertion_id", "run_id", "evidence_ref", "assertion_text"}
	for _, field := range top {
		if _, ok := record[field]; !ok {
			t.Fatalf("the export record carries no top-level %q: %v", field, record)
		}
	}
	for _, field := range []string{"channel_class", "channel_ui_origin", "channel_automation"} {
		if _, ok := record[field]; !ok {
			t.Fatalf("the export record carries no %q: %v", field, record)
		}
	}
	if len(record) != len(top)+3 {
		t.Fatalf("the export record carries %d keys (%v), want %d", len(record), record, len(top)+3)
	}
}

// The export carries the quote the judgement rests on, so an auditor reading
// an exported record sees WHICH sentence it was made about without opening
// the audit row's details.  The quote is the very string the write side
// answered with.
func TestManualJudgementHTTPExportCarriesTheQuoteTheJudgementRestsOn(t *testing.T) {
	fake := &manualJudgementFake{}
	engine := manualJudgementEngine(t, fake)
	recorded := manualJudgementServe(t, engine, http.MethodPost, "/api/projects/project-1/manual-judgements",
		manualJudgementBody(), "manual-judgement-http-quote")
	if recorded.Code != http.StatusCreated {
		t.Fatalf("record judgement = %d, want 201: %s", recorded.Code, recorded.Body.String())
	}
	authoritative, ok := manualJudgementEnvelope(t, recorded)["data"].(map[string]any)
	if !ok {
		t.Fatal("the authoritative answer carries no record")
	}

	response := manualJudgementServe(t, engine, http.MethodGet,
		"/api/projects/project-1/manual-judgements/audit-records", "", "")
	records, ok := manualJudgementEnvelope(t, response)["data"].([]any)
	if !ok || len(records) != 1 {
		t.Fatalf("the export holds %v, want one record", manualJudgementEnvelope(t, response)["data"])
	}
	exported, ok := records[0].(map[string]any)
	if !ok {
		t.Fatalf("the export record = %v, want an object", records[0])
	}
	mine, err := json.Marshal(authoritative["assertion_text"])
	if err != nil {
		t.Fatalf("encode the authoritative quote: %v", err)
	}
	theirs, err := json.Marshal(exported["assertion_text"])
	if err != nil {
		t.Fatalf("encode the exported quote: %v", err)
	}
	if string(mine) != string(theirs) {
		t.Fatalf("the exported quote = %s, but the authoritative record says %s", theirs, mine)
	}
	if exported["assertion_text"] != manualJudgementProse {
		t.Fatalf("the exported quote = %v, want the contract's prose", exported["assertion_text"])
	}
}

// The runner compares the export against the ledger byte for byte, so the
// two answers have to agree byte for byte.  The same submission, read back
// from the same stored strings, is what makes that true.
func TestManualJudgementHTTPExportAgreesWithTheAuthoritativeRecordByteForByte(t *testing.T) {
	fake := &manualJudgementFake{}
	engine := manualJudgementEngine(t, fake)
	recorded := manualJudgementServe(t, engine, http.MethodPost, "/api/projects/project-1/manual-judgements",
		manualJudgementBody(), "manual-judgement-http-3")
	if recorded.Code != http.StatusCreated {
		t.Fatalf("record judgement = %d, want 201: %s", recorded.Code, recorded.Body.String())
	}
	authoritative, ok := manualJudgementEnvelope(t, recorded)["data"].(map[string]any)
	if !ok {
		t.Fatalf("the authoritative answer carries no record")
	}

	response := manualJudgementServe(t, engine, http.MethodGet,
		"/api/projects/project-1/manual-judgements/audit-records", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("export audit records = %d, want 200: %s", response.Code, response.Body.String())
	}
	records, ok := manualJudgementEnvelope(t, response)["data"].([]any)
	if !ok || len(records) != 1 {
		t.Fatalf("the export holds %v, want one record", manualJudgementEnvelope(t, response)["data"])
	}
	exported, ok := records[0].(map[string]any)
	if !ok {
		t.Fatalf("the export record = %v, want an object", records[0])
	}
	// The seven fields the runner compares (manual_decision_audit.AUDIT_FIELDS).
	for _, field := range []string{"actor", "authorization", "channel", "occurred_at",
		"assertion_id", "run_id", "evidence_ref"} {
		mine, err := json.Marshal(authoritative[field])
		if err != nil {
			t.Fatalf("encode the authoritative %q: %v", field, err)
		}
		theirs, err := json.Marshal(exported[field])
		if err != nil {
			t.Fatalf("encode the exported %q: %v", field, err)
		}
		if string(mine) != string(theirs) {
			t.Fatalf("the export %q = %s, but the authoritative record says %s", field, theirs, mine)
		}
	}
	if exported["audit_id"] != authoritative["audit_id"] {
		t.Fatalf("the export audit_id = %v, but the authoritative record says %v", exported["audit_id"], authoritative["audit_id"])
	}
	// The timestamp is the string that was written down, not a reformatted one.
	if exported["occurred_at"] != manualJudgementOccurredAt {
		t.Fatalf("the export reformatted occurred_at: %v", exported["occurred_at"])
	}
}

// The server can prove the credential class and nothing more: a UI click
// and a script replay are the same credential.  So the channel names the
// class it can prove, and the two layers it cannot prove say so.
func TestManualJudgementHTTPExportNamesOnlyWhatTheServerCanProve(t *testing.T) {
	fake := &manualJudgementFake{}
	engine := manualJudgementEngine(t, fake)
	recorded := manualJudgementServe(t, engine, http.MethodPost, "/api/projects/project-1/manual-judgements",
		manualJudgementBody(), "manual-judgement-http-4")
	if recorded.Code != http.StatusCreated {
		t.Fatalf("record judgement = %d, want 201: %s", recorded.Code, recorded.Body.String())
	}
	authoritative := manualJudgementEnvelope(t, recorded)["data"].(map[string]any)
	if authoritative["channel"] != manualJudgementChannel {
		t.Fatalf("the authoritative record channel = %v, want %q", authoritative["channel"], manualJudgementChannel)
	}

	response := manualJudgementServe(t, engine, http.MethodGet,
		"/api/projects/project-1/manual-judgements/audit-records", "", "")
	records := manualJudgementEnvelope(t, response)["data"].([]any)
	exported := records[0].(map[string]any)
	if exported["channel"] != manualJudgementChannel {
		t.Fatalf("the export channel = %v, want %q", exported["channel"], manualJudgementChannel)
	}
	if exported["channel_ui_origin"] != manualJudgementUnprovable {
		t.Fatalf("the export channel_ui_origin = %v, want %q", exported["channel_ui_origin"], manualJudgementUnprovable)
	}
	if exported["channel_automation"] != manualJudgementUnprovable {
		t.Fatalf("the export channel_automation = %v, want %q", exported["channel_automation"], manualJudgementUnprovable)
	}
}

// A caller who may not read the project's audit records is refused.  The
// refusal is the repository's own: a resource the caller may not see is not
// found, so the answer does not leak that it exists.
func TestManualJudgementHTTPExportRefusesACallerWithoutPermission(t *testing.T) {
	fake := &manualJudgementFake{err: core.ErrNotFound}
	engine := manualJudgementEngine(t, fake)

	response := manualJudgementServe(t, engine, http.MethodGet,
		"/api/projects/project-1/manual-judgements/audit-records", "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("export for a refused caller = %d, want 404: %s", response.Code, response.Body.String())
	}
	failure := manualJudgementError(t, response)
	if failure["code"] != "not_found" || failure["retryable"] != false {
		t.Fatalf("failure = %v, want a not_found that is not retryable", failure)
	}
}

// A refusal that carries its own status is rendered with that status: the
// transport does not flatten every refusal into one shape.
func TestManualJudgementHTTPRendersTheRefusalStatusTheDomainDeclared(t *testing.T) {
	fake := &manualJudgementFake{err: core.ErrHumanDecisionRequired}
	engine := manualJudgementEngine(t, fake)

	response := manualJudgementServe(t, engine, http.MethodGet,
		"/api/projects/project-1/manual-judgements/audit-records", "", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("export for a refused caller = %d, want 403: %s", response.Code, response.Body.String())
	}
	failure := manualJudgementError(t, response)
	if failure["code"] != "human_decision_required" {
		t.Fatalf("failure = %v, want the code the domain declared", failure)
	}
}

// The write route is a write: it carries the idempotency key like every
// other write in this repository.
func TestManualJudgementHTTPPostRequiresTheIdempotencyKey(t *testing.T) {
	fake := &manualJudgementFake{}
	engine := manualJudgementEngine(t, fake)

	response := manualJudgementServe(t, engine, http.MethodPost, "/api/projects/project-1/manual-judgements",
		manualJudgementBody(), "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("a judgement without an idempotency key = %d, want 400: %s", response.Code, response.Body.String())
	}
	if fake.calls != 0 {
		t.Fatalf("the refused request reached the domain side %d times", fake.calls)
	}
}

// The two routes are registered only when the port is there: a family of
// routes that cannot answer without its port must not be registered at all.
func TestManualJudgementHTTPRegistersNothingWithoutItsPort(t *testing.T) {
	engine := manualJudgementEngine(t, nil)
	if routes := engine.Routes(); len(routes) != 0 {
		t.Fatalf("the routes were registered without a port: %v", routes)
	}
}

// An empty project answers with an empty array rather than with null.
func TestManualJudgementHTTPExportOfAnEmptyProjectIsAnEmptyArray(t *testing.T) {
	fake := &manualJudgementFake{}
	engine := manualJudgementEngine(t, fake)

	response := manualJudgementServe(t, engine, http.MethodGet,
		"/api/projects/project-1/manual-judgements/audit-records", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("export audit records = %d, want 200: %s", response.Code, response.Body.String())
	}
	envelope := manualJudgementEnvelope(t, response)
	records, ok := envelope["data"].([]any)
	if !ok {
		t.Fatalf("data = %v, want an empty array", envelope["data"])
	}
	if len(records) != 0 {
		t.Fatalf("the export holds %d records, want none", len(records))
	}
}
