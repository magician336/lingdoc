package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/lingdoc/membership"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type candidateOneConnShares struct{ interfaces.KBShareService }
type candidateOneConnAuthorizer struct{ service *core.Service }

// workingCopyBodyHash 是工作副本正文的摘要。
//
// 算法与采纳端是同一个：正文的 UTF-8 字节的 SHA-256，小写十六进制、64 个
// 字符。它是 HTTP 契约的一部分，所以这里按契约重算一遍，而不是问服务端要
// 一个值——那样这条断言就只是把服务端的话抄回来。
func workingCopyBodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func (a candidateOneConnAuthorizer) Authorize(ctx context.Context, userID, projectID, capability string) error {
	caller := types.CallerFromContext(ctx)
	if caller.UserID != userID {
		return core.ErrNotFound
	}
	return a.service.Authorize(ctx, core.Actor{TenantID: caller.TenantID, UserID: userID, Role: caller.Role}, projectID, capability)
}
func (a candidateOneConnAuthorizer) AuthorizeCandidateInTransaction(ctx context.Context, tx *gorm.DB, userID, projectID, capability string) error {
	caller := types.CallerFromContext(ctx)
	tenant, ok := types.TenantIDFromContext(ctx)
	if !ok || caller.TenantID != tenant || caller.UserID != userID {
		return core.ErrNotFound
	}
	_, err := a.service.AuthorizeInTransaction(ctx, core.NewTransactionAdapter(tx), core.Actor{TenantID: caller.TenantID, UserID: userID, Role: caller.Role}, projectID, capability)
	return err
}

type candidateOneConnFixture struct {
	db        *gorm.DB
	runtime   *SourceRuntime
	projects  *core.Service
	members   *membership.Service
	store     *candidateadoption.SQLiteCandidateAdoptionStore
	adoption  *candidateadoption.CandidateAdoptionService
	ctx       context.Context
	actor     core.Actor
	candidate candidateadoption.Candidate
	input     candidateadoption.AcceptCandidateInput
	card      core.EvidenceCard
}

// The old delivery fixture starts with a hand-written active project, so it
// intentionally skips CreateProject/ActivateProject. Reuse the server-hashed
// bound-copy seed here whenever that fixture exercises modern formal changes.
// This is the existing one-connection fixture's copy seed, not a product repair.
func seedDeliveryBoundTemplateCopy(t *testing.T, db *gorm.DB, copyID string) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS lingdoc_project_template_copies(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,source_template_id TEXT NOT NULL,source_template_version TEXT NOT NULL,version INTEGER NOT NULL,status TEXT NOT NULL,content_hash TEXT NOT NULL,ruleset_hash TEXT NOT NULL,definition_json TEXT NOT NULL,created_by TEXT NOT NULL,created_at DATETIME NOT NULL,UNIQUE(project_id,version))`).Error)
	definition, err := core.ContractDemoTemplate{}.Get("template-demo", "1")
	require.NoError(t, err)
	definition, err = template.WithHashes(definition)
	require.NoError(t, err)
	definitionJSON, err := json.Marshal(definition)
	require.NoError(t, err)
	require.NoError(t, db.Exec(`INSERT INTO lingdoc_project_template_copies(id,project_id,source_template_id,source_template_version,version,status,content_hash,ruleset_hash,definition_json,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, copyID, "project-1", "template-demo", "1", 1, core.TemplateCopyBound, definition.ContentHash, definition.RulesetHash, string(definitionJSON), "reader", time.Now().UTC()).Error)
	require.NoError(t, db.Exec("UPDATE lingdoc_projects SET current_context_revision=1,template_copy_version=1 WHERE id=?", "project-1").Error)
}

func assertDeliveryBoundTemplatePrerequisites(t *testing.T, service *core.Service, ctx context.Context, actor core.Actor, project core.Project) {
	t.Helper()
	require.Equal(t, "active", project.Status)
	require.NotNil(t, project.TemplateCopy, "modern workspace fixture must load its real server-owned template copy")
	require.Positive(t, project.TemplateCopyVersion)
	copy, err := service.GetTemplateCopy(ctx, actor, project.ID, project.TemplateCopyVersion)
	require.NoError(t, err)
	require.Equal(t, core.TemplateCopyBound, copy.Status)
	require.Equal(t, project.ID, copy.ProjectID)
	require.Equal(t, project.TemplateCopyVersion, copy.Version)
	require.Equal(t, project.TemplateID, copy.SourceTemplateID)
	require.Equal(t, project.TemplateVersion, copy.SourceTemplateVersion)
	definition, err := template.WithHashes(copy.Definition)
	require.NoError(t, err)
	require.Equal(t, definition.ContentHash, copy.ContentHash)
	require.Equal(t, definition.RulesetHash, copy.RulesetHash)
	require.Equal(t, copy.ContentHash, project.TemplateCopy.ContentHash)
	owner := false
	for _, member := range project.Members {
		if member.UserID == actor.UserID && (member.Role == "owner" || member.GovernanceRole == "owner") && (member.Status == "" || member.Status == "active") {
			owner = true
		}
	}
	require.True(t, owner, "fixture actor must be the current active project Owner")
	require.NoError(t, service.Authorize(ctx, actor, project.ID, "manage"))
	require.NoError(t, service.Authorize(ctx, actor, project.ID, "write"))
}

// Only an external sharing service is stubbed; same-tenant access takes the
// existing owner branch. Core, SourceRuntime, ResearchAdapter, bindings,
// original text, model consent and adoption persistence are actual adapters.
func newCandidateOneConnFixture(t *testing.T) candidateOneConnFixture {
	t.Helper()
	_, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db, deliveryMethodChapter())
	ensureImpactRouteTables(t, db)
	for _, ddl := range []string{
		`CREATE TABLE knowledge_bases(id TEXT PRIMARY KEY,tenant_id INTEGER NOT NULL,chunking_config TEXT,deleted_at DATETIME)`,
		`CREATE TABLE lingdoc_g2_objects(project_id TEXT NOT NULL,kind TEXT NOT NULL,id TEXT NOT NULL,version INTEGER NOT NULL,payload_json TEXT NOT NULL,PRIMARY KEY(project_id,kind,id))`,
		`CREATE TABLE lingdoc_g2_object_versions(project_id TEXT NOT NULL,kind TEXT NOT NULL,id TEXT NOT NULL,version INTEGER NOT NULL,payload_json TEXT NOT NULL,PRIMARY KEY(project_id,kind,id,version))`,
		`CREATE TABLE lingdoc_working_copies(project_id TEXT NOT NULL,chapter_id TEXT NOT NULL,base_chapter_version_id TEXT,spec_revision INTEGER NOT NULL,working_copy_revision INTEGER NOT NULL,body_markdown TEXT NOT NULL,source_ids_json TEXT NOT NULL,citation_usages_json TEXT NOT NULL,review_items_json TEXT NOT NULL,created_at DATETIME,updated_at DATETIME,PRIMARY KEY(project_id,chapter_id))`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	require.NoError(t, db.Exec("ALTER TABLE chunks ADD COLUMN source_content TEXT NOT NULL DEFAULT ''").Error)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases(id,tenant_id) VALUES(?,?)", "kb-1", 7).Error)
	originPath := filepath.Join(t.TempDir(), "actual-origin.txt")
	require.NoError(t, os.WriteFile(originPath, []byte(sourcePolicyQuote), 0600))
	require.NoError(t, db.Exec("UPDATE knowledges SET file_type='txt',file_path=? WHERE id=?", originPath, "knowledge-1").Error)
	setFixtureUploadHash(t, db, []byte(sourcePolicyQuote))
	seedFixtureOriginProof(t, db, "source-1", "knowledge-1", 7, []byte(sourcePolicyQuote))
	require.NoError(t, db.Exec("UPDATE chunks SET source_content=content WHERE id=?", "source-1").Error)
	seedDeliveryBoundTemplateCopy(t, db, "actual-copy")
	// A second, genuinely bound asset is deliberately excluded from generation.
	require.NoError(t, db.Exec(`INSERT INTO knowledges(id,tenant_id,knowledge_base_id,type,title,file_type,file_size,file_hash,file_path,parse_status) VALUES(?,?,?,?,?,?,?,?,?,?)`, "unused-knowledge", 7, "kb-1", "file", "Unused synthetic", "txt", 0, "unused-hash", "", "completed").Error)
	shares := candidateOneConnShares{}
	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, shares, nil, delivery.NewFixedTemplateReader())
	require.NoError(t, runtime.bindings.RefreshAsset(context.Background(), "project-1", assetID))
	currentAsset, err := runtime.bindings.CurrentAsset(context.Background(), "project-1", assetID)
	require.NoError(t, err)
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects})
	connectAggregateCapabilities(runtime, projects)
	runtime.EnableFullWorkflowPolicy()
	adapter := NewResearchAdapter(db, projects, runtime, shares, nil, nil)
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: adapter})
	_, err = runtime.bindings.Bind(context.Background(), evidence.BindInput{TenantID: 7, ProjectID: "project-1", KnowledgeID: "unused-knowledge", KnowledgeBaseID: "kb-1", CreatedBy: "reader", Title: "Unused synthetic", Signal: evidence.KnowledgeSignal{KnowledgeID: "unused-knowledge", ParseStatus: "completed", FileHash: "unused-hash"}})
	require.NoError(t, err)
	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}
	ctx := types.WithExecutionTenant(types.WithCaller(policyContext(), types.Caller{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}), 7)
	policy := core.ProjectAssetPolicy{ID: assetID, ProjectID: "project-1", AssetID: assetID, Version: 1, AssetRevision: int64(currentAsset.AssetRevision), PolicyVersion: 1, Scope: "project", Purpose: "合成软件流程验证", ModelUseAllowed: true, AllowedUserIDs: []string{}, CreatedBy: "reader", GrantedBy: "reader", Reason: "合成人工许可fixture", ContextRevision: 1}
	policyJSON, err := json.Marshal(policy)
	require.NoError(t, err)
	require.NoError(t, core.NewTransactionAdapter(db).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: core.AssetPolicyKind, ID: assetID, Version: 1, Payload: policyJSON}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	t.Cleanup(cancel)
	p, err := projects.GetProject(deadline, actor, "project-1")
	require.NoError(t, err)
	expectation := core.VersionExpectation{ExpectedProjectVersion: p.ProjectVersion, ExpectedContextRevision: p.CurrentContextRevision}
	raw, _, _, err := projects.SaveSourceAnchor(deadline, actor, p.ID, "oneconn-anchor", core.SaveSourceAnchorInput{VersionExpectation: expectation, SourceID: "source-1", SelectionEnd: len([]rune(sourcePolicyQuote)), ExtractionType: "native_text", OCRConfidence: 1, HumanVerified: true, Confirmed: true})
	require.NoError(t, err)
	var anchor core.SourceAnchor
	require.NoError(t, json.Unmarshal(raw, &anchor))
	raw, _, _, err = projects.SaveResearchClaim(deadline, actor, p.ID, "oneconn-claim", core.SaveResearchClaimInput{VersionExpectation: expectation, Text: "合成内容仅测试代码路径", Scope: "软件流程", Reason: "人工录入fixture", Confirmed: true})
	require.NoError(t, err)
	var claim core.ResearchClaim
	require.NoError(t, json.Unmarshal(raw, &claim))
	raw, _, _, err = projects.SaveEvidenceCard(deadline, actor, p.ID, "oneconn-card", core.SaveEvidenceCardInput{VersionExpectation: expectation, ClaimRef: &core.VersionRef{ID: claim.ID, Version: claim.Version}, AnchorRefs: []core.VersionRef{{ID: anchor.ID, Version: anchor.Version}}, SupportType: "supports", Scope: "软件fixture", ScopeChapterIDs: []string{"chapter-2"}, Limitations: "不是真实研究案例", Confirmed: true})
	require.NoError(t, err)
	var card core.EvidenceCard
	require.NoError(t, json.Unmarshal(raw, &card))
	expectation.ExpectedVersion = card.Version
	raw, _, _, err = projects.ConfirmEvidenceCard(deadline, actor, p.ID, card.ID, "oneconn-card-confirm", core.ConfirmationInput{VersionExpectation: expectation, Confirmed: true, Reason: "合成fixture逐字核对，不是科学证明"})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &card))
	require.NoError(t, db.Exec(`INSERT INTO lingdoc_working_copies(project_id,chapter_id,base_chapter_version_id,spec_revision,working_copy_revision,body_markdown,source_ids_json,citation_usages_json,review_items_json) SELECT project_id,chapter_id,id,spec_revision,1,body_markdown,source_ids_json,citation_usages_json,review_items_json FROM lingdoc_chapter_versions WHERE id=?`, "version-2").Error)
	store := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	current, err := store.GenerationContext(deadline, p.ID, "chapter-2")
	require.NoError(t, err)
	current.Basis.AssetVersions = []candidateadoption.AssetVersion{{AssetID: assetID, AssetRevision: currentAsset.AssetRevision}}
	current.Basis.EvidenceCardRefs = []core.VersionRef{{ID: card.ID, Version: card.Version, Kind: "EvidenceCard"}}
	candidate := candidateadoption.Candidate{ID: "oneconn-candidate", ProjectID: p.ID, ChapterID: "chapter-2", Validity: "fresh", RunID: "synthetic-local-run", BodyMarkdown: "合成候选 [[source:source-1]]", SourceIDs: []string{"source-1"}, Basis: current.Basis, WritingAction: writingaction.ActionGenerate, Origin: writingaction.OriginGenerated}
	candidate.Claims = []candidateadoption.ClaimEvidence{{Text: candidate.BodyMarkdown, References: []candidateadoption.ClaimEvidenceReference{{CardID: card.ID, CardVersion: card.Version, AnchorID: anchor.ID, AnchorVersion: anchor.Version, SourceID: anchor.SourceID, Locator: anchor.Locator, SelectionStart: anchor.SelectionStart, SelectionEnd: anchor.SelectionEnd, QuotedTextHash: anchor.QuotedTextHash}}}}
	require.NoError(t, store.UpsertCandidate(deadline, candidate))
	// 采纳是一次显式确认：确认载荷要说清采纳的是哪个动作的结果、写到哪里、
	// 谁确认的、候选是什么来源。夹具里的候选记了动作与来源，确认载荷就得
	// 确认同一件事。
	confirmation := &candidateadoption.AdoptionConfirmation{
		WritingAction: candidate.WritingAction,
		Target:        writingaction.TargetFormalVersionWhole,
		ConfirmedAt:   time.Now().UTC(),
		ConfirmedBy:   actor.UserID,
		Origin:        candidate.Origin,
	}
	// 采纳要声明「用户看到的是哪一份工作副本」：修订号与正文摘要。这份夹具
	// 里的草稿是刚插进去的那一行，所以读回来再声明——声明的是事实，不是猜的。
	var workingCopy struct {
		WorkingCopyRevision int64
		BodyMarkdown        string
	}
	require.NoError(t, db.Raw("SELECT working_copy_revision, body_markdown FROM lingdoc_working_copies WHERE project_id = ? AND chapter_id = ?", p.ID, "chapter-2").Scan(&workingCopy).Error)
	input := candidateadoption.AcceptCandidateInput{ProjectID: p.ID, ChapterID: "chapter-2", CandidateID: candidate.ID, ActorID: actor.UserID, IdempotencyKey: "oneconn-adoption", ExpectedChapterVersionID: current.ChapterVersionID, ExpectedSpecRevision: current.SpecRevision, ReplaceExisting: true, ExpectedWorkingCopyRevision: workingCopy.WorkingCopyRevision, ExpectedBodyHash: workingCopyBodyHash(workingCopy.BodyMarkdown), Confirmation: confirmation}
	adoption := candidateadoption.NewCandidateAdoptionService(store, runtime.CandidateAdoptionSourcePolicy(), candidateOneConnAuthorizer{projects})
	return candidateOneConnFixture{
		db, runtime, projects, newMembershipService(projects, core.NewGORMRepository(db)),
		store, adoption, deadline, actor, candidate, input, card,
	}
}

func TestCandidateActualOneConnectionAdaptersAdoptSelectedAssetAndRecheckReplay(t *testing.T) {
	f := newCandidateOneConnFixture(t)
	current, err := f.store.GenerationContext(f.ctx, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	require.Len(t, current.Basis.AssetVersions, 2)
	require.Len(t, f.candidate.Basis.AssetVersions, 1)
	require.NoError(t, f.db.Exec("UPDATE knowledges SET file_hash=? WHERE id=?", "unrelated-new-hash", "unused-knowledge").Error)
	result, err := f.adoption.AcceptCandidate(f.ctx, f.input)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	require.False(t, result.Chapter.ConfirmationValid)
	replay, err := f.adoption.AcceptCandidate(f.ctx, f.input)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, result.Chapter.CurrentVersionID, replay.Chapter.CurrentVersionID)
	require.Equal(t, 1, mustCandidateDB(t, f.db).Stats().MaxOpenConnections)
	var count int64
	require.NoError(t, f.db.Table("lingdoc_g3_records").Where("kind='edge'").Count(&count).Error)
	require.Positive(t, count)
	require.NoError(t, f.db.Exec("UPDATE chunks SET content_revision=1 WHERE id=?", "source-1").Error)
	_, err = f.adoption.AcceptCandidate(f.ctx, f.input)
	require.Error(t, err)
	require.False(t, errors.Is(err, context.DeadlineExceeded))
}
func mustCandidateDB(t *testing.T, db *gorm.DB) *sql.DB {
	t.Helper()
	raw, err := db.DB()
	require.NoError(t, err)
	return raw
}

func TestCandidateActualOneConnectionProofRejectsChangedNativeFactsAndIdentity(t *testing.T) {
	for _, kind := range []string{"missing_proof", "caller", "sources", "content", "asset_revision", "model_consent", "member", "cross_tenant"} {
		t.Run(kind, func(t *testing.T) {
			f := newCandidateOneConnFixture(t)
			prepared, _, err := f.projects.PrepareCandidateGenerationEvidence(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID, f.candidate.Basis.EvidenceCardRefs, f.candidate.SourceIDs)
			require.NoError(t, err)
			err = f.db.WithContext(prepared).Transaction(func(tx *gorm.DB) error {
				testCtx := prepared
				ids := f.candidate.SourceIDs
				switch kind {
				case "missing_proof":
					testCtx = f.ctx
				case "caller":
					testCtx = types.WithCaller(prepared, types.Caller{TenantID: 7, UserID: "another-user", Role: types.TenantRoleOwner})
				case "sources":
					ids = []string{"unselected"}
				case "content":
					require.NoError(t, tx.Exec("UPDATE chunks SET content='changed' WHERE id=?", "source-1").Error)
				case "asset_revision":
					require.NoError(t, tx.Exec("UPDATE lingdoc_project_assets SET asset_revision=asset_revision+1 WHERE id=?", f.candidate.Basis.AssetVersions[0].AssetID).Error)
				case "model_consent":
					var policy core.ProjectAssetPolicy
					var row struct{ PayloadJSON string }
					require.NoError(t, tx.Table("lingdoc_g2_objects").Where("kind=? AND id=?", core.AssetPolicyKind, f.candidate.Basis.AssetVersions[0].AssetID).Take(&row).Error)
					require.NoError(t, json.Unmarshal([]byte(row.PayloadJSON), &policy))
					policy.ModelUseAllowed = false
					raw, _ := json.Marshal(policy)
					require.NoError(t, tx.Table("lingdoc_g2_objects").Where("kind=? AND id=?", core.AssetPolicyKind, policy.ID).Update("payload_json", string(raw)).Error)
				case "member":
					require.NoError(t, tx.Exec("UPDATE tenant_members SET role='viewer' WHERE user_id=?", f.actor.UserID).Error)
				case "cross_tenant":
					require.NoError(t, tx.Exec("UPDATE chunks SET tenant_id=8 WHERE id=?", "source-1").Error)
				}
				_, e := f.projects.SelectGenerationEvidenceInTransaction(testCtx, core.NewTransactionAdapter(tx), f.actor, f.input.ProjectID, f.input.ChapterID, f.candidate.Basis.EvidenceCardRefs, true, ids)
				require.Error(t, e)
				require.False(t, errors.Is(e, context.DeadlineExceeded))
				return errors.New("abort synthetic change")
			})
			require.Error(t, err)
		})
	}
}

func TestCandidateActualAdapterRejectsMachineAdoptionAndPreservesScope(t *testing.T) {
	f := newCandidateOneConnFixture(t)
	require.NoError(t, f.db.Exec(`CREATE TABLE IF NOT EXISTS lingdoc_g5_records(project_id TEXT NOT NULL,kind TEXT NOT NULL,id TEXT NOT NULL,version INTEGER NOT NULL,payload_json TEXT NOT NULL,PRIMARY KEY(project_id,kind,id))`).Error)
	require.NoError(t, f.db.Exec(`CREATE TABLE tenant_api_keys(id INTEGER PRIMARY KEY,tenant_id INTEGER,scope_type TEXT,full_access INTEGER NOT NULL DEFAULT 0,capabilities TEXT NOT NULL DEFAULT '[]',knowledge_base_ids TEXT NOT NULL DEFAULT '[]',expires_at DATETIME,revoked_at DATETIME)`).Error)
	require.NoError(t, f.db.Exec("INSERT INTO tenant_api_keys(id,tenant_id,scope_type,capabilities,knowledge_base_ids) VALUES(?,?,?,?,?)", 44, 7, "tenant", types.StringArray{"retrieve"}, types.StringArray{"kb-1", "kb-excluded"}).Error)
	p, err := f.projects.GetProject(f.ctx, f.actor, f.input.ProjectID)
	require.NoError(t, err)
	// Native keys have an explicit read seam, never an artificial human write
	// grant. The real native-auth positive/KB tests live in router integration.
	_, _, _, err = f.members.SaveProjectAPIKeyGrant(
		f.ctx,
		f.actor,
		f.input.ProjectID,
		"candidate-api-write-grant",
		core.ProjectAPIKeyGrant{
			ExpectedProjectVersion: p.ProjectVersion,
			KeyID:                  44,
			Capabilities:           []string{"read", "write"},
			Active:                 true,
			Reason:                 "合成机器写权限必须拒绝",
		},
	)
	require.ErrorIs(t, err, core.ErrInvalidRequest)
	_, _, _, err = f.members.SaveProjectAPIKeyGrant(
		f.ctx,
		f.actor,
		f.input.ProjectID,
		"candidate-api-read-grant",
		core.ProjectAPIKeyGrant{
			ExpectedProjectVersion: p.ProjectVersion,
			KeyID:                  44,
			Capabilities:           []string{"read"},
			Active:                 true,
			Reason:                 "合成只读许可不代表人工采纳",
		},
	)
	require.NoError(t, err)
	for _, kb := range []string{"kb-1", "kb-excluded"} {
		scoped := types.WithTenantAPIKeyScope(f.ctx, types.TenantAPIKeyScope{KeyID: 44, ScopeType: types.APIKeyScopeTenant, KnowledgeBaseIDs: types.StringArray{kb}, Capabilities: types.StringArray{"retrieve", "ingest"}})
		scoped = types.WithPrincipal(scoped, types.Principal{Type: types.PrincipalAPITenant, ID: strconv.FormatUint(f.actor.TenantID, 10)})
		prepared, selected, err := f.projects.PrepareCandidateGenerationEvidence(scoped, f.actor, f.input.ProjectID, f.input.ChapterID, f.candidate.Basis.EvidenceCardRefs, f.candidate.SourceIDs)
		require.Error(t, err)
		require.Empty(t, selected.Cards)
		require.False(t, errors.Is(err, context.DeadlineExceeded))
		actualScope, ok := types.TenantAPIKeyScopeFromContext(prepared)
		require.True(t, ok)
		require.Equal(t, types.StringArray{kb}, actualScope.KnowledgeBaseIDs)
		principal, present := types.PrincipalFromContext(prepared)
		require.True(t, present)
		require.Equal(t, types.Principal{Type: types.PrincipalAPITenant, ID: strconv.FormatUint(f.actor.TenantID, 10)}, principal)
	}
}

type candidatePostCommitSourceMutation struct {
	candidateadoption.WorkspaceWriter
	db *gorm.DB
}

func (w candidatePostCommitSourceMutation) AcceptCandidate(ctx context.Context, in candidateadoption.AcceptCandidateInput, candidate candidateadoption.Candidate) (candidateadoption.AcceptResult, error) {
	result, err := w.WorkspaceWriter.AcceptCandidate(ctx, in, candidate)
	if err != nil {
		return result, err
	}
	// This state change occurs after the real parent transaction committed.
	err = w.db.WithContext(ctx).Exec("UPDATE chunks SET content_revision=1 WHERE id=?", "source-1").Error
	return result, err
}
func TestCandidateActualPostCommitRecheckDeniesDataWithoutClaimingRollback(t *testing.T) {
	f := newCandidateOneConnFixture(t)
	f.adoption.Writer = candidatePostCommitSourceMutation{f.store, f.db}
	_, err := f.adoption.AcceptCandidate(f.ctx, f.input)
	require.Error(t, err)
	require.Contains(t, err.Error(), "acceptance committed")
	require.False(t, errors.Is(err, context.DeadlineExceeded))
	var chapter struct{ CurrentVersionID string }
	require.NoError(t, f.db.Table("lingdoc_chapters").Where("id=?", f.input.ChapterID).Take(&chapter).Error)
	require.NotEqual(t, *f.input.ExpectedChapterVersionID, chapter.CurrentVersionID)
	var count int64
	require.NoError(t, f.db.Table("lingdoc_candidate_adoptions").Where("project_id=? AND idempotency_key=?", f.input.ProjectID, f.input.IdempotencyKey).Count(&count).Error)
	require.Equal(t, int64(1), count)
	_, err = f.adoption.AcceptCandidate(f.ctx, f.input)
	require.Error(t, err)
	require.NoError(t, f.db.Table("lingdoc_candidate_adoptions").Where("project_id=? AND idempotency_key=?", f.input.ProjectID, f.input.IdempotencyKey).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestCandidateFocusedTableHelperPreservesExistingNativeWorkspaceSchema(t *testing.T) {
	_, db := newSourceCurrentnessHandler(t)
	type nativeColumn struct {
		Name, Type string
		Notnull    int
		DfltValue  *string
		Pk         int
	}
	var before, after []nativeColumn
	require.NoError(t, db.Raw("PRAGMA table_info(lingdoc_projects)").Scan(&before).Error)
	var beforeSQL, afterSQL string
	require.NoError(t, db.Raw("SELECT sql FROM sqlite_master WHERE type='table' AND name='lingdoc_projects'").Scan(&beforeSQL).Error)
	require.NoError(t, candidateadoption.NewSQLiteCandidateAdoptionStore(db).AutoMigrate(context.Background()))
	require.NoError(t, db.Raw("PRAGMA table_info(lingdoc_projects)").Scan(&after).Error)
	require.NoError(t, db.Raw("SELECT sql FROM sqlite_master WHERE type='table' AND name='lingdoc_projects'").Scan(&afterSQL).Error)
	require.Equal(t, beforeSQL, afterSQL)
	require.Equal(t, before, after)
	var project struct{ TenantID uint64 }
	require.NoError(t, db.Table("lingdoc_projects").Where("id=?", "project-1").Take(&project).Error)
	require.Equal(t, uint64(7), project.TenantID)
	require.Error(t, db.Exec("UPDATE lingdoc_projects SET tenant_id=NULL WHERE id=?", "project-1").Error)
	require.True(t, db.Migrator().HasTable("lingdoc_candidates"))
	require.True(t, db.Migrator().HasTable("lingdoc_chapter_confirmation_requests"))
}

func TestCandidateProvenanceSurvivesAdoptionAndStalesOnWorkingCopyEdit(t *testing.T) {
	f := newCandidateOneConnFixture(t)
	adopted, err := f.adoption.AcceptCandidate(f.ctx, f.input)
	require.NoError(t, err)
	versions, err := f.projects.ListChapterVersions(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	var pinned core.ChapterVersion
	for _, version := range versions {
		if version.ID == *adopted.Chapter.CurrentVersionID {
			pinned = version
		}
	}
	require.Len(t, pinned.ClaimProvenance, 1)
	require.Equal(t, "needs_revalidation", pinned.ClaimProvenance[0].Status)
	copy, err := f.projects.GetWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	require.Len(t, copy.ClaimProvenance, 1)
	require.Equal(t, "needs_revalidation", copy.ClaimProvenance[0].Status)
	require.Equal(t, f.candidate.ID, copy.ClaimProvenance[0].CandidateID)
	in := core.SaveWorkingCopyInput{ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, BaseChapterVersionID: copy.BaseChapterVersionID, BodyMarkdown: copy.BodyMarkdown + " edited", SourceIDs: copy.SourceIDs, CitationUsages: copy.CitationUsages}
	_, _, _, err = f.projects.SaveWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID, "claim-provenance-edit", in)
	require.NoError(t, err)
	edited, err := f.projects.GetWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	require.Len(t, edited.ClaimProvenance, 1)
	require.Equal(t, "stale", edited.ClaimProvenance[0].Status)
	versions, err = f.projects.ListChapterVersions(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	for _, version := range versions {
		if version.ID == pinned.ID {
			require.Len(t, version.ClaimProvenance, 1)
			require.Equal(t, "needs_revalidation", version.ClaimProvenance[0].Status)
		}
	}
	_, _, _, err = f.projects.RestoreWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID, "claim-provenance-restore", core.RestoreWorkingCopyInput{ChapterVersionID: pinned.ID, ExpectedSpecRevision: edited.SpecRevision, ExpectedWorkingCopyRevision: edited.WorkingCopyRevision, ExpectedChapterVersionID: edited.BaseChapterVersionID})
	require.NoError(t, err)
	restored, err := f.projects.GetWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	require.Len(t, restored.ClaimProvenance, 2)
	require.Equal(t, "stale", restored.ClaimProvenance[0].Status)
	require.Equal(t, "needs_revalidation", restored.ClaimProvenance[1].Status)
}

type boundRewriteProvenanceModel struct{}

func (*boundRewriteProvenanceModel) RunMode() string { return "mock" }
func (*boundRewriteProvenanceModel) RewriteSelected(_ context.Context, _ Actor, p SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	if len(p.EvidenceConstraints) != 1 || len(p.EvidenceConstraints[0].Anchors) != 1 {
		return SelectedRewriteOutput{}, ErrRewriteSourceDenied
	}
	card := p.EvidenceConstraints[0]
	anchor := card.Anchors[0]
	body := "改写合成事实 [[source:source-1]]"
	return SelectedRewriteOutput{ReplacementMarkdown: body, SourceIDs: []string{"source-1"}, RunMode: "mock", Claims: []candidateadoption.ClaimEvidence{{Text: body, References: []candidateadoption.ClaimEvidenceReference{{CardID: card.CardRef.ID, CardVersion: card.CardRef.Version, AnchorID: anchor.ID, AnchorVersion: anchor.Version, SourceID: anchor.SourceID}}}}}, nil
}
func TestRewriteProvenanceSurvivesApplyAndStalesOnEdit(t *testing.T) {
	f := newCandidateOneConnFixture(t)
	copy, err := f.projects.GetWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	require.NotEmpty(t, copy.BodyMarkdown)
	service := NewSelectedRewriteService(f.db, f.projects, f.projects, f.runtime, &boundRewriteProvenanceModel{})
	require.NoError(t, service.store.AutoMigrate(f.ctx))
	selection := RewriteSelection{StartUTF16: 0, EndUTF16: len(utf16.Encode([]rune(copy.BodyMarkdown))), SelectedText: copy.BodyMarkdown}
	request := SelectedRewriteRequest{BaseChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, Selection: selection, Instruction: "合成改写", SourceIDs: []string{}, EvidenceCardRefs: []generation.EvidenceCardRef{{ID: f.card.ID, Version: f.card.Version}}}
	candidate, err := service.Create(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID, "rewrite-provenance", request)
	require.NoError(t, err)
	require.Equal(t, candidateadoption.ValidityFresh, candidate.Validity)
	applied, _, err := service.Apply(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID, candidate.CandidateID, "rewrite-apply", ApplySelectedRewriteInput{BaseChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision})
	require.NoError(t, err)
	require.Len(t, applied.ClaimProvenance, 1)
	current, err := f.projects.GetWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	require.Len(t, current.ClaimProvenance, 1)
	require.Equal(t, "rewrite", current.ClaimProvenance[0].OriginKind)
	_, _, _, err = f.projects.CommitWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID, "rewrite-provenance-commit", core.CommitWorkingCopyInput{ExpectedSpecRevision: current.SpecRevision, ExpectedWorkingCopyRevision: current.WorkingCopyRevision, ExpectedChapterVersionID: current.BaseChapterVersionID})
	require.NoError(t, err)
	current, err = f.projects.GetWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	versions, err := f.projects.ListChapterVersions(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	var committed core.ChapterVersion
	for _, version := range versions {
		if current.BaseChapterVersionID != nil && version.ID == *current.BaseChapterVersionID {
			committed = version
		}
	}
	require.Len(t, committed.ClaimProvenance, 1)
	require.Equal(t, "needs_revalidation", committed.ClaimProvenance[0].Status)
	in := core.SaveWorkingCopyInput{ExpectedSpecRevision: current.SpecRevision, ExpectedWorkingCopyRevision: current.WorkingCopyRevision, BaseChapterVersionID: current.BaseChapterVersionID, BodyMarkdown: current.BodyMarkdown + " 编辑", SourceIDs: current.SourceIDs, CitationUsages: current.CitationUsages}
	_, _, _, err = f.projects.SaveWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID, "rewrite-provenance-edit", in)
	require.NoError(t, err)
	current, err = f.projects.GetWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	require.Equal(t, "stale", current.ClaimProvenance[0].Status)
	versions, err = f.projects.ListChapterVersions(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	for _, version := range versions {
		if version.ID == committed.ID {
			require.Len(t, version.ClaimProvenance, 1)
			require.Equal(t, "needs_revalidation", version.ClaimProvenance[0].Status)
		}
	}
}

func TestRewriteProvenanceFailureCanRetryWithoutSecondEdit(t *testing.T) {
	f := newCandidateOneConnFixture(t)
	copy, err := f.projects.GetWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	service := NewSelectedRewriteService(f.db, f.projects, f.projects, f.runtime, &boundRewriteProvenanceModel{})
	require.NoError(t, service.store.AutoMigrate(f.ctx))
	request := SelectedRewriteRequest{BaseChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision, Selection: RewriteSelection{EndUTF16: len(utf16.Encode([]rune(copy.BodyMarkdown))), SelectedText: copy.BodyMarkdown}, Instruction: "合成改写", SourceIDs: []string{}, EvidenceCardRefs: []generation.EvidenceCardRef{{ID: f.card.ID, Version: f.card.Version}}}
	candidate, err := service.Create(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID, "rewrite-fault-create", request)
	require.NoError(t, err)
	require.NoError(t, f.db.Migrator().DropTable(&core.DraftClaimProvenance{}))
	input := ApplySelectedRewriteInput{BaseChapterVersionID: copy.BaseChapterVersionID, ExpectedSpecRevision: copy.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision}
	_, _, err = service.Apply(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID, candidate.CandidateID, "rewrite-fault-apply", input)
	require.ErrorIs(t, err, ErrRewriteUnavailable)
	after, err := f.projects.GetWorkingCopy(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID)
	require.NoError(t, err)
	require.Equal(t, copy.WorkingCopyRevision+1, after.WorkingCopyRevision)
	require.True(t, after.ClaimProvenanceUnavailable)
	require.NoError(t, f.db.AutoMigrate(&core.DraftClaimProvenance{}))
	recovered, _, err := service.Apply(f.ctx, f.actor, f.input.ProjectID, f.input.ChapterID, candidate.CandidateID, "rewrite-fault-apply", input)
	require.NoError(t, err)
	require.Equal(t, after.WorkingCopyRevision, recovered.WorkingCopyRevision)
	require.Len(t, recovered.ClaimProvenance, 1)
}
