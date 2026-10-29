package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// The identity is an authenticated-context fixture; all HTTP handlers, current
// membership checks, catalog/copy adapters and transactions are production code.
// This is not evidence for a real account login or an external model call.
func TestCatalogHTTPProductionDatabaseParity(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var db *gorm.DB
			var owner, reader core.Actor
			if dialect == "postgres" {
				db, owner, reader = postgresCatalogFixture(t)
			} else {
				_, db, owner, _, _ = governanceAdapterFixture(t)
				reader = core.Actor{TenantID: owner.TenantID, UserID: "parity-reader", Role: types.TenantRoleViewer}
				if err := db.Exec("INSERT INTO tenant_members(tenant_id,user_id,role,status) VALUES(?,?,?,'active')", reader.TenantID, reader.UserID, reader.Role).Error; err != nil {
					t.Fatal(err)
				}
			}
			ctx := governanceCallerContext(context.Background(), owner)
			readerCtx := governanceCallerContext(context.Background(), reader)
			newRuntime := func(options ...TemplateGovernanceRuntimeOption) (*TemplateGovernanceRuntime, *gin.Engine) {
				t.Helper()
				projects := core.NewService(core.NewGORMRepository(db))
				runtime, err := NewTemplateGovernanceRuntime(db, projects, nil, nil, options...)
				if err != nil {
					t.Fatal(err)
				}
				projects.SetTemplateCatalogResolver(runtime)
				router := gin.New()
				group := router.Group("/api/v1/lingdoc")
				RegisterTemplateGovernanceRoutes(group, runtime)
				h, err := NewHandler(HandlerDependencies{Projects: projects, Members: newMembershipService(projects, core.NewGORMRepository(db)), Chapters: projects,
					TemplateApp: projects, ChangeSets: projects, Sources: emptySourcesStub{}})
				if err != nil {
					t.Fatal(err)
				}
				group.POST("/projects", h.createProject)
				group.GET("/projects/:projectId", h.getProject)
				return runtime, router
			}
			runtime, router := newRuntime()
			set, fixtures := seedDemoRuleSetFixture(t, runtime, owner, ctx)
			call := func(c context.Context, method, path, key string, input any, want int, target any) string {
				t.Helper()
				body := ""
				if input != nil {
					raw, err := json.Marshal(input)
					if err != nil {
						t.Fatal(err)
					}
					body = string(raw)
				}
				response := governanceHTTPRequest(router, c, method, "/api/v1/lingdoc"+path, body, key)
				if response.Code != want {
					t.Fatalf("%s %s: got %d want %d: %s (request=%s)", method, path, response.Code, want, response.Body.String(), body)
				}
				if target != nil {
					var envelope struct{ Data json.RawMessage }
					if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(envelope.Data, target); err != nil {
						t.Fatal(err)
					}
				}
				return response.Body.String()
			}
			base := "/template-catalog/profiles"
			profile := base + "/" + template.DemoTemplateID
			call(ctx, http.MethodPost, base, "http-parity-profile", governance.CreateProfileInput{ID: template.DemoTemplateID, Name: "HTTP parity synthetic", Reason: "spec 139 acceptance"}, 201, nil)
			def := template.DemoTemplate()
			def.Rules, def.ContentHash, def.RulesetHash = nil, "", ""
			input := governance.SaveDraftInput{TemplateID: def.ID, Version: def.Version, Definition: def, RuleSet: governance.RuleSetRef{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}, SourceType: "lingdoc_builtin", Scope: "internal-demo", Curator: owner.UserID, Reason: "HTTP parity"}
			var draft governance.TemplateVersion
			call(ctx, http.MethodPost, profile+"/versions", "http-parity-draft", input, 200, &draft)
			var hidden []governance.Profile
			call(readerCtx, http.MethodGet, base, "", nil, 200, &hidden)
			if len(hidden) != 0 {
				t.Fatal("draft-only profile visible to reader")
			}
			// Both malformed structure and client hashes must be refused at the
			// actual HTTP/application boundary, not only by internal helpers.
			invalid := input
			invalid.Version = "bad"
			invalid.Definition = template.DemoTemplate()
			invalid.Definition.Version = "bad"
			invalid.Definition.Rules = nil
			invalid.Definition.ContentHash, invalid.Definition.RulesetHash = "", ""
			invalid.Definition.Fields[0].Type = "script"
			call(ctx, http.MethodPost, profile+"/versions", "http-bad-structure", invalid, 400, nil)
			forged := input
			forged.Definition.RulesetHash = "client-forged"
			call(ctx, http.MethodPost, profile+"/versions", "http-forged-hash", forged, 400, nil)
			call(ctx, http.MethodPost, profile+"/versions", "http-duplicate-version", input, 409, nil)
			publish := governance.PublishInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, ReplayFixtureIDs: fixtures, Reason: "real registered replay"}
			versionPath := profile + "/versions/" + draft.Version
			call(readerCtx, http.MethodPost, versionPath+"/publish", "http-reader-publish", publish, 404, nil)
			stale := publish
			stale.ExpectedRevision++
			call(ctx, http.MethodPost, versionPath+"/publish", "http-stale-publish", stale, 409, nil)
			missingReplay := publish
			missingReplay.ReplayFixtureIDs = []string{"client-pass"}
			call(ctx, http.MethodPost, versionPath+"/publish", "http-fake-replay", missingReplay, 422, nil)
			var published governance.TemplateVersion
			call(ctx, http.MethodPost, versionPath+"/publish", "http-publish", publish, 200, &published)
			call(ctx, http.MethodPost, versionPath+"/publish", "http-publish", publish, 200, nil)
			if published.State != template.RuleSetPublished || published.ContentHash == "" || published.RulesetHash == "" {
				t.Fatal("publication missing immutable server-computed basis")
			}
			var visible []governance.Profile
			call(readerCtx, http.MethodGet, base+"?scope=internal-demo&state=published", "", nil, 200, &visible)
			if len(visible) != 1 {
				t.Fatalf("matching scope: %+v", visible)
			}
			call(readerCtx, http.MethodGet, base+"?scope=unmatched&state=published", "", nil, 200, &visible)
			if len(visible) != 0 {
				t.Fatal("scope filter ignored")
			}
			copyInput := core.CreateProjectInput{Name: "HTTP exact catalog copy", TemplateID: published.TemplateID, TemplateVersion: published.Version}
			var copied core.Project
			call(ctx, http.MethodPost, "/projects", "http-copy", copyInput, 201, &copied)
			if copied.TemplateCopy == nil || copied.TemplateCopy.SourceTemplateVersion != published.Version || copied.TemplateCopy.ContentHash != published.ContentHash || copied.TemplateCopy.RulesetHash != published.RulesetHash {
				t.Fatal("copy did not preserve exact source version and hashes")
			}
			count := func(table, where string, args ...any) int64 {
				t.Helper()
				var n int64
				if err := db.Table(table).Where(where, args...).Count(&n).Error; err != nil {
					t.Fatal(err)
				}
				return n
			}
			if count("audit_logs", "action=? AND outcome=?", "lingdoc.template.copy", "success") != 1 {
				t.Fatal("successful copy audit missing or duplicated")
			}
			if _, err := runtime.Service().ListAuditHistory(ctx, governanceActor(owner), template.DemoTemplateID, governance.AuditHistoryFilter{Limit: 2}); err != nil {
				t.Fatalf("service audit history read failed before HTTP assertion: %v", err)
			}
			var auditIDs = map[uint64]bool{}
			var sawCopySuccess bool
			afterID := uint64(0)
			for pageNumber := 0; pageNumber < 20; pageNumber++ {
				var page governance.AuditHistoryPage
				path := profile + "/audit-events?limit=2"
				if afterID > 0 {
					path = fmt.Sprintf("%s/audit-events?limit=2&after_id=%d", profile, afterID)
				}
				call(ctx, http.MethodGet, path, "", nil, 200, &page)
				if pageNumber == 0 && (len(page.Events) != 2 || page.NextAfterID == 0 || page.Events[0].Action != "lingdoc.template.copy") {
					t.Fatalf("first audit history page is incomplete or unstable: %+v", page)
				}
				for _, event := range page.Events {
					if auditIDs[event.ID] || event.TargetID == "" || event.ActorUserID == "" {
						t.Fatalf("audit event was duplicated or omitted its attribution: %+v", event)
					}
					auditIDs[event.ID] = true
					if event.Action == "lingdoc.template.copy" && event.Outcome == "success" {
						if event.ActorUserID != owner.UserID {
							t.Fatalf("copy success was attributed to the wrong actor: %+v", event)
						}
						sawCopySuccess = true
					}
				}
				if page.NextAfterID == 0 {
					break
				}
				if afterID > 0 && page.NextAfterID >= afterID {
					t.Fatalf("audit history cursor did not advance: after_id=%d next=%d", afterID, page.NextAfterID)
				}
				afterID = page.NextAfterID
				if pageNumber == 19 {
					t.Fatal("audit history pagination did not terminate")
				}
			}
			if len(auditIDs) < 4 || !sawCopySuccess {
				t.Fatalf("audit history omitted lifecycle/copy events: ids=%v copy_success=%v", auditIDs, sawCopySuccess)
			}
			call(readerCtx, http.MethodGet, profile+"/audit-events", "", nil, 403, nil)
			// Fail only successful copy auditing; the outer application must
			// roll back project, template copy and operation before retry.
			trigger := `CREATE TRIGGER parity_deny_copy BEFORE INSERT ON audit_logs WHEN NEW.action='lingdoc.template.copy' AND NEW.outcome='success' BEGIN SELECT RAISE(ABORT,'synthetic audit failure'); END`
			drop := "DROP TRIGGER parity_deny_copy"
			if dialect == "postgres" {
				trigger = `CREATE FUNCTION parity_deny_copy_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='lingdoc.template.copy' AND NEW.outcome='success' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER parity_deny_copy BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION parity_deny_copy_fn()`
				drop = "DROP TRIGGER parity_deny_copy ON audit_logs"
			}
			if err := db.Exec(trigger).Error; err != nil {
				t.Fatal(err)
			}
			copyInput.Name = "audit rollback copy"
			call(ctx, http.MethodPost, "/projects", "http-copy-audit-fails", copyInput, 500, nil)
			if count("lingdoc_projects", "name=?", copyInput.Name) != 0 || count("audit_logs", "action=? AND outcome=?", "lingdoc.template.copy", "success") != 1 {
				t.Fatal("copy audit failure left partial success")
			}
			if count("audit_logs", "action=? AND outcome=?", "lingdoc.template.copy", "failure") != 1 {
				t.Fatal("rolled-back successful copy did not leave a failure receipt")
			}
			var failureReceipt struct {
				TargetID string
				Details  string
			}
			if err := db.Table("audit_logs").Select("target_id,details").Where("action=? AND outcome=?", "lingdoc.template.copy", "failure").Take(&failureReceipt).Error; err != nil {
				t.Fatal(err)
			}
			var failureDetails struct {
				Reason    string `json:"reason"`
				TargetRef string `json:"target_ref"`
				Details   struct {
					ErrorCode string `json:"error_code"`
					Template  string `json:"template_id"`
					Version   string `json:"version"`
					ProjectID string `json:"project_id"`
				} `json:"details"`
			}
			if err := json.Unmarshal([]byte(failureReceipt.Details), &failureDetails); err != nil {
				t.Fatal(err)
			}
			if failureDetails.Details.ErrorCode != "audit_persistence_failed" ||
				failureDetails.TargetRef != published.TemplateID+"/"+published.Version ||
				failureDetails.Details.Template != published.TemplateID ||
				failureDetails.Details.Version != published.Version ||
				failureDetails.Details.ProjectID == "" ||
				!strings.Contains(failureDetails.Reason, "successful template-copy audit persistence failed") {
				t.Fatalf("copy rollback receipt lost resolved target or persistence error classification: %+v", failureDetails)
			}
			var latestAuditPage governance.AuditHistoryPage
			call(ctx, http.MethodGet, profile+"/audit-events?limit=1", "", nil, 200, &latestAuditPage)
			if len(latestAuditPage.Events) != 1 || latestAuditPage.Events[0].Action != "lingdoc.template.copy" || latestAuditPage.Events[0].Outcome != "failure" {
				t.Fatalf("catalog history did not expose the persisted copy failure receipt: %+v", latestAuditPage)
			}
			if err := db.Exec(drop).Error; err != nil {
				t.Fatal(err)
			}
			call(ctx, http.MethodPost, "/projects", "http-copy-audit-fails", copyInput, 201, nil)
			// New service instances consume only database state, not old objects.
			runtime, router = newRuntime()
			var current governance.TemplateVersion
			call(ctx, http.MethodGet, versionPath, "", nil, 200, &current)
			if current.ContentHash != published.ContentHash {
				t.Fatal("fresh service lost publication")
			}
			call(ctx, http.MethodPost, versionPath+"/publish", "http-publish", publish, 200, nil)
			for _, action := range []string{"withdraw", "recall"} {
				if action == "recall" {
					// A new version is required: withdrawn -> recalled is not
					// a legal transition. Keep the earlier project's v1 copy.
					input.Version, input.Definition.Version = "2", "2"
					call(ctx, http.MethodPost, profile+"/versions", "http-v2-draft", input, 200, &draft)
					// Every template version needs separately reviewed, hash-pinned
					// replay evidence. Rebind the builtin fixture IDs to the v2
					// candidate through the deployment bootstrap option and rebuild
					// the runtime; the published v1 was already replayed above.
					versionFixtures := BuiltinTemplateGovernanceFixtures()
					fixtureIDs := make([]string, 0, len(versionFixtures))
					for i := range versionFixtures {
						versionFixtures[i].TemplateVersion = draft.Version
						versionFixtures[i].TemplateContentHash = draft.ContentHash
						versionFixtures[i].PolicyHash = draft.PolicyHash
						fixtureIDs = append(fixtureIDs, versionFixtures[i].Fixture.ID)
					}
					runtime, router = newRuntime(WithReviewedTemplateFixtures(versionFixtures))
					publish.Version, publish.ExpectedRevision = "2", draft.Revision
					publish.ReplayFixtureIDs = fixtureIDs
					versionPath = profile + "/versions/2"
					var persisted governance.TemplateVersion
					call(ctx, http.MethodGet, versionPath, "", nil, 200, &persisted)
					if err := runtime.Verify(ctx, governanceActor(owner), persisted, fixtureIDs); err != nil {
						t.Fatalf("v2 persisted replay fixture verification: %v", err)
					}
					if err := (governance.ExampleReplayVerifier{Reader: runtime}).Verify(ctx, governanceActor(owner), draft, fixtureIDs); err != nil {
						t.Fatalf("v2 synthetic replay fixture verification: %v", err)
					}
					call(ctx, http.MethodPost, versionPath+"/publish", "http-publish-v2", publish, 200, &published)
				}
				lifecycle := governance.GovernanceInput{TemplateID: published.TemplateID, Version: published.Version, ExpectedRevision: published.Revision, Reason: "spec 139 " + action}
				var payload any = lifecycle
				if action == "recall" {
					payload = governance.RecallInput{GovernanceInput: lifecycle, MigrationAdvice: "owner decides; never rewrite copies automatically"}
				}
				call(ctx, http.MethodPost, versionPath+"/"+action, "http-"+action, payload, 200, &current)
				if string(current.State) != map[string]string{"withdraw": "withdrawn", "recall": "recalled"}[action] {
					t.Fatal("wrong lifecycle state")
				}
				copyInput.TemplateVersion, copyInput.Name = published.Version, "denied-"+action
				response := governanceHTTPRequest(router, ctx, http.MethodPost, "/api/v1/lingdoc/projects", fmt.Sprintf(`{"name":%q,"template_id":%q,"template_version":%q}`, copyInput.Name, published.TemplateID, published.Version), "http-copy-denied-"+action)
				var failure struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusUnprocessableEntity || failure.Error.Code != "invalid_state" || count("lingdoc_projects", "name=?", copyInput.Name) != 0 {
					t.Fatalf("closed version accepted/failed unexpectedly: %d %s", response.Code, response.Body.String())
				}
				var preserved core.Project
				call(ctx, http.MethodGet, "/projects/"+copied.ID, "", nil, 200, &preserved)
				if preserved.ProjectVersion != copied.ProjectVersion || preserved.TemplateCopy.ContentHash != copied.TemplateCopy.ContentHash || preserved.TemplateCopy.SourceTemplateVersion != "1" {
					t.Fatal("source lifecycle rewrote existing copy")
				}
			}
			for _, action := range []string{"publish", "withdraw", "recall", "copy"} {
				if count("audit_logs", "action=? AND outcome=?", "lingdoc.template."+action, "success") == 0 {
					t.Fatalf("missing %s history", action)
				}
			}
		})
	}
}
