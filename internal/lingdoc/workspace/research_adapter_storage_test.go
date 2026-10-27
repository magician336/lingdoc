package workspace

// #623 的回归：上传资料的「原文」必须接到既有存储端口。
//
// 上传资料的 file_path 是存储引用（resource://…），不是文件系统路径；原文
// （引文用来对照证明的那份文本）只有存储层读得回来。ResearchAdapter 持有
// knowledge 服务（它满足 GetKnowledgeFile），却把这条能力丢在一边、另装了
// 一个不带存储字节的读取器 ⇒ 每份上传资料都读不到原文 ⇒ StableOriginal
// 恒为 false ⇒ SaveSourceAnchor 对真实资料恒答 ErrEvidenceIncomplete
// （HTTP 422 evidence_incomplete）。
//
// 下面两条用例分别钉住最窄缝（读取侧的 StableOriginal）与用户症状缝
// （真实 source-anchor 链路的 422）。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// applySQLiteMigrations 让夹具的库形态来自生产迁移文件本身，而不是手抄
// 生产表结构（scripts/ci/test_lingdoc_fixture_schema_source.py 守护的形状：
// 基表 + 生产迁移，二者只能以迁移文件为准）。
func applySQLiteMigrations(t *testing.T, db *gorm.DB, names ...string) {
	t.Helper()
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if err := db.Exec(string(body)).Error; err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

// applyCreateTablesFromMigration 从迁移文件里提取指定表的 CREATE TABLE 语句
// 执行——表定义仍以迁移文件为准，只是不整文件应用（本包既有夹具已用窄形状
// 建过 knowledges/chunks，整跑基线的 IF NOT EXISTS 会跳过建表、却让基线里
// 针对这些表的索引语句失败）。
func applyCreateTablesFromMigration(t *testing.T, db *gorm.DB, migrationFile string, tables ...string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", migrationFile))
	if err != nil {
		t.Fatalf("read migration %s: %v", migrationFile, err)
	}
	for _, table := range tables {
		pattern := regexp.MustCompile(`(?is)CREATE TABLE (?:IF NOT EXISTS )?` + table + `\s*\(.*?\);`)
		stmt := pattern.FindString(string(body))
		if stmt == "" {
			t.Fatalf("no CREATE TABLE %s statement in %s", table, migrationFile)
		}
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("apply CREATE TABLE %s from %s: %v", table, migrationFile, err)
		}
	}
}

// adapterStorageStub 是存储侧端口：按 #623 复现形态回答资料的源文件字节，
// 或回答「读不到」。它只实现 GetKnowledgeFile——这正是 KnowledgeFileSource
// 端口唯一要求的方法，也是生产 knowledge 服务已满足的能力。
type adapterStorageStub struct {
	interfaces.KnowledgeService
	data  []byte
	err   error
	calls int
}

func (s *adapterStorageStub) GetKnowledgeFile(context.Context, string) (io.ReadCloser, string, error) {
	s.calls++
	if s.err != nil {
		return nil, "", s.err
	}
	return io.NopCloser(bytes.NewReader(s.data)), "material.txt", nil
}

// adapterStorageSourcesStub 让读取测试只依赖 sources 端口的既有契约：
// 资产就绪、来源可用，稳定原文的判定完全交给被测的原文读取接线。
type adapterStorageSourcesStub struct {
	SourceApplicationService
	assets []evidence.Asset
	source evidence.Source
}

func (s *adapterStorageSourcesStub) ListAssets(context.Context, Actor, string) ([]evidence.Asset, error) {
	return s.assets, nil
}

func (s *adapterStorageSourcesStub) GetSource(context.Context, Actor, string, string) (evidence.Source, error) {
	return s.source, nil
}

// seedStorageBackedKnowledge 把 seedBoundSource 落好的资料改成 #623 的复现
// 形态：txt、file_path 是 resource:// 存储引用。资产策略读取需要的对象表与
// 知识库表按生产迁移文件落（基线 000000 的 knowledge_bases ＋ 000037 的
// lingdoc_g2_objects；均为 IF NOT EXISTS / 全新表，先落好的 knowledges/chunks
// 不受影响）。
func seedStorageBackedKnowledge(t *testing.T, db *gorm.DB) {
	t.Helper()
	applyCreateTablesFromMigration(t, db, "000000_init.up.sql", "knowledge_bases")
	applyCreateTablesFromMigration(t, db, "000037_lingdoc_full_workflow.up.sql", "lingdoc_g2_objects")
	if err := db.Exec("INSERT OR IGNORE INTO knowledge_bases(id,tenant_id,name,embedding_model_id,summary_model_id) VALUES(?,?,?,?,?)", "kb-1", 7, "synthetic-kb", "embed-1", "summary-1").Error; err != nil {
		t.Fatalf("seed knowledge base: %v", err)
	}
	if err := db.Exec("UPDATE knowledges SET file_type='txt', file_path='resource://synthetic-storage-object' WHERE id=?", "knowledge-1").Error; err != nil {
		t.Fatalf("seed storage-backed knowledge: %v", err)
	}
}

// TestResearchAdapterReadsUploadedOriginThroughStoragePort pins the narrowest
// seam: for an uploaded material whose file path is a storage reference, the
// origin text must come through the storage port, and the read must count as a
// stable original. Storage that cannot answer degrades to "no origin" (false),
// never to a verbatim claim it could not prove.
func TestResearchAdapterReadsUploadedOriginThroughStoragePort(t *testing.T) {
	_, db, assetID := seedBoundSource(t)
	seedStorageBackedKnowledge(t, db)

	storage := &adapterStorageStub{data: []byte(sourcePolicyQuote)}
	sources := &adapterStorageSourcesStub{
		assets: []evidence.Asset{{ID: assetID, KnowledgeID: "knowledge-1", AssetRevision: 1, ProcessingState: evidence.AssetStateReady}},
		source: evidence.Source{ID: "source-1", AssetID: assetID, AssetRevision: 1, Status: evidence.SourceAvailable, QuotedText: sourcePolicyQuote},
	}
	adapter := NewResearchAdapter(db, &adapterProjectsStub{}, sources, nil, storage, nil)
	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}

	read, err := adapter.ReadResearchSource(policyContext(), actor, "project-1", "source-1")
	if err != nil {
		t.Fatalf("read research source: %v", err)
	}
	if !read.StableOriginal {
		t.Fatalf("a storage-backed uploaded material must count as a stable original (#623), storage calls=%d", storage.calls)
	}
	if storage.calls == 0 {
		t.Fatal("the origin read never asked the storage port")
	}

	// 存储答不出（后端故障、对象丢失）是「没有原文」：降档，不得报成
	// 逐字证明，也不得把整次读取变成错误。
	storage.err = errors.New("storage backend unreachable")
	read, err = adapter.ReadResearchSource(policyContext(), actor, "project-1", "source-1")
	if err != nil {
		t.Fatalf("read with failing storage: %v", err)
	}
	if read.StableOriginal {
		t.Fatal("a material storage cannot answer must not count as a stable original")
	}
}

// TestSaveSourceAnchorAcceptsStorageBackedUploadedMaterial pins the user
// symptom: a real source-anchor created against an uploaded material must stop
// answering 422 evidence_incomplete once the origin bytes are reachable through
// the storage port. Everything else in the chain is the production assembly:
// the real SourceRuntime, the real workspacecore service and the real adapter.
func TestSaveSourceAnchorAcceptsStorageBackedUploadedMaterial(t *testing.T) {
	_, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db, deliveryMethodChapter())
	ensureImpactRouteTables(t, db)
	// 知识库/对象/工作副本表按生产迁移文件落：knowledge_bases 的 CREATE 从
	// 基线文件提取，lingdoc_g2_objects / g2_object_versions 从 000037 提取
	//（000037 非独立迁移——其中语句引用 lingdoc_change_sets 等先序表），
	// lingdoc_working_copies 用独立的 000033。chunks 的 source_content 仍需补
	// 一列（ALTER 不属手抄生产表的守护范围）。
	applyCreateTablesFromMigration(t, db, "000000_init.up.sql", "knowledge_bases")
	applySQLiteMigrations(t, db, "000033_lingdoc_working_copies.up.sql")
	applyCreateTablesFromMigration(t, db, "000037_lingdoc_full_workflow.up.sql",
		"lingdoc_g2_objects", "lingdoc_g2_object_versions")
	if err := db.Exec("ALTER TABLE chunks ADD COLUMN source_content TEXT NOT NULL DEFAULT ''").Error; err != nil {
		t.Fatalf("alter chunks: %v", err)
	}
	if err := db.Exec("INSERT INTO knowledge_bases(id,tenant_id,name,embedding_model_id,summary_model_id) VALUES(?,?,?,?,?)", "kb-1", 7, "synthetic-kb", "embed-1", "summary-1").Error; err != nil {
		t.Fatalf("seed knowledge base: %v", err)
	}
	// #623 的复现形态：上传的 txt，file_path 是 resource:// 存储引用。
	if err := db.Exec("UPDATE knowledges SET file_type='txt', file_path='resource://synthetic-storage-object' WHERE id=?", "knowledge-1").Error; err != nil {
		t.Fatalf("seed storage-backed knowledge: %v", err)
	}
	if err := db.Exec("UPDATE chunks SET source_content=content WHERE id=?", "source-1").Error; err != nil {
		t.Fatalf("seed chunk content: %v", err)
	}
	seedDeliveryBoundTemplateCopy(t, db, "storage-copy")

	shares := candidateOneConnShares{}
	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, shares, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects})
	connectAggregateCapabilities(runtime, projects)
	runtime.EnableFullWorkflowPolicy()
	storage := &adapterStorageStub{data: []byte(sourcePolicyQuote)}
	adapter := NewResearchAdapter(db, projects, runtime, shares, storage, nil)
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: adapter})

	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}
	ctx := types.WithExecutionTenant(types.WithCaller(policyContext(), types.Caller{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}), 7)
	policy := core.ProjectAssetPolicy{ID: assetID, ProjectID: "project-1", AssetID: assetID, Version: 1, AssetRevision: 1, PolicyVersion: 1, Scope: "project", Purpose: "合成软件流程验证", ModelUseAllowed: true, AllowedUserIDs: []string{}, CreatedBy: "reader", GrantedBy: "reader", Reason: "合成人工许可fixture", ContextRevision: 1}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = core.NewTransactionAdapter(db).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: core.AssetPolicyKind, ID: assetID, Version: 1, Payload: policyJSON}); err != nil {
		t.Fatalf("insert asset policy: %v", err)
	}

	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	t.Cleanup(cancel)
	p, err := projects.GetProject(deadline, actor, "project-1")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	expectation := core.VersionExpectation{ExpectedProjectVersion: p.ProjectVersion, ExpectedContextRevision: p.CurrentContextRevision}
	input := core.SaveSourceAnchorInput{VersionExpectation: expectation, SourceID: "source-1", SelectionEnd: len([]rune(sourcePolicyQuote)), ExtractionType: "native_text", OCRConfidence: 1, HumanVerified: true, Confirmed: true}

	if _, _, _, err = projects.SaveSourceAnchor(deadline, actor, p.ID, "storage-anchor", input); err != nil {
		t.Fatalf("source-anchor on a storage-backed uploaded material must stop answering 422 evidence_incomplete (#623): %v", err)
	}
	if storage.calls == 0 {
		t.Fatal("the anchor path never asked the storage port for the origin bytes")
	}

	// 存储读不到时 422 必须保持响亮：降档是「没有原文」，不是「接受锚点」。
	storage.err = errors.New("storage backend unreachable")
	if _, _, _, err = projects.SaveSourceAnchor(deadline, actor, p.ID, "storage-anchor-missing", input); !errors.Is(err, core.ErrEvidenceIncomplete) {
		t.Fatalf("a material storage cannot answer must keep the loud evidence_incomplete rejection, got: %v", err)
	}
}
