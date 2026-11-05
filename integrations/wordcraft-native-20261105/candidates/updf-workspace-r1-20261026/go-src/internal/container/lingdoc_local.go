package container

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

// databaseKnowledgeFiles reuses the production resource catalog and storage
// resolver, with every metadata lookup using the caller's database/transaction.
type databaseKnowledgeFiles struct{ db *gorm.DB }

func databaseOriginFiles(db *gorm.DB) workspace.OriginFileReader {
	return workspace.KnowledgeFileOriginReader{Service: databaseKnowledgeFiles{db: db}}
}

func (r databaseKnowledgeFiles) GetKnowledgeFile(ctx context.Context, id string) (io.ReadCloser, string, error) {
	var knowledge types.Knowledge
	if err := r.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).First(&knowledge).Error; err != nil {
		return nil, "", err
	}
	catalog := service.NewResourceCatalog(repository.NewResourceRepository(r.db))
	_, resource, err := catalog.ResolvePath(ctx, knowledge.FilePath)
	if err != nil {
		return nil, "", err
	}
	var tenant types.Tenant
	if err = r.db.WithContext(ctx).First(&tenant, knowledge.TenantID).Error; err != nil {
		return nil, "", err
	}
	var kb types.KnowledgeBase
	if err = r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", knowledge.KnowledgeBaseID, knowledge.TenantID).First(&kb).Error; err != nil {
		return nil, "", err
	}
	backendID, provider := "", kb.GetStorageProvider()
	if kb.StorageBackendID != nil {
		backendID = *kb.StorageBackendID
	}
	if resource != nil {
		if resource.TenantID != knowledge.TenantID {
			return nil, "", fmt.Errorf("source file resource owner mismatch")
		}
		backendID, provider = resource.StorageBackendID, resource.Provider
	} else if id, path, ok := types.ParseStorageBackendPath(knowledge.FilePath); ok {
		backendID, provider = id, types.ParseProviderScheme(path)
	}
	resolver := service.NewStorageBackendService(repository.NewStorageBackendRepository(r.db), r.db, catalog)
	files, _, err := resolver.ResolveFileService(ctx, &tenant, backendID, provider, os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	if err != nil {
		return nil, "", err
	}
	reader, err := files.GetFile(ctx, knowledge.FilePath)
	return reader, knowledge.FileName, err
}

type LocalChapter struct {
	SectionID string `json:"sectionId"`
	ChapterID string `json:"chapterId"`
	Title     string `json:"title"`
}

type LocalResearchBootstrap struct {
	ProjectID       string          `json:"projectId"`
	KnowledgeBaseID string          `json:"knowledgeBaseId"`
	TenantID        uint64          `json:"tenantId"`
	ActorID         string          `json:"actorId"`
	Chapters        []LocalChapter  `json:"chapters"`
	LocalOnly       bool            `json:"localOnly"`
	MaterialFormats []string        `json:"materialFormats"`
	AI              LocalResearchAI `json:"ai"`
}

type LocalResearchApplication struct {
	Handler   http.Handler
	Bootstrap LocalResearchBootstrap
	Stop      <-chan struct{}
	Cleaner   interfaces.ResourceCleaner
}

type localResearchDependencies struct {
	dig.In
	DB               *gorm.DB
	Users            interfaces.UserService
	Tenants          interfaces.TenantService
	KnowledgeBases   interfaces.KnowledgeBaseService
	Knowledge        *handler.KnowledgeHandler
	Chunks           *handler.ChunkHandler
	Workspace        *workspace.Handler
	Projects         workspace.ApplicationService
	Runtime          *workspace.SourceRuntime
	Cleaner          interfaces.ResourceCleaner
	Models           interfaces.ModelService
	KBShares         interfaces.KBShareService
	KnowledgeObjects interfaces.KnowledgeService
	WebSearch        interfaces.WebSearchService
}

// NewLocalResearchApplication uses the production DI container. Only transport
// identity is local: business authorization, indexing, cards and storage remain
// the production services. This listener must never bind a public interface.
func NewLocalResearchApplication(ctx context.Context, address, origin, runtimeID string) (*LocalResearchApplication, error) {
	if err := validateLocalResearchConfig(address, origin, runtimeID); err != nil {
		return nil, err
	}
	// Configure encryption before any production dependency reads model rows.
	aiConfig, err := prepareLocalAIConfiguration(os.Getenv("LINGDOC_LOCAL_AI_CONFIG"))
	if err != nil {
		return nil, err
	}
	container := BuildContainer(dig.New())
	var deps localResearchDependencies
	if err := container.Invoke(func(in localResearchDependencies) { deps = in }); err != nil {
		return nil, err
	}
	var keywordTableCount int64
	if err := deps.DB.WithContext(ctx).Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='lite_embeddings_fts'").Scan(&keywordTableCount).Error; err != nil || keywordTableCount != 1 {
		deps.Cleaner.Cleanup(ctx)
		return nil, fmt.Errorf("local keyword index unavailable: build with -tags sqlite_fts5 (table count %d, error %v)", keywordTableCount, err)
	}
	if err := evidence.NewParsedOriginStore(deps.DB).AutoMigrate(ctx); err != nil {
		deps.Cleaner.Cleanup(ctx)
		return nil, err
	}
	bootstrap, user, tenant, err := bootstrapLocalResearch(ctx, deps)
	if err != nil {
		deps.Cleaner.Cleanup(ctx)
		return nil, fmt.Errorf("local research bootstrap: %w", err)
	}
	if err = workspace.NewDOCXVersionStore(deps.DB).AutoMigrate(ctx); err != nil {
		return nil, err
	}
	bootstrap.AI, err = configureLocalResearchAI(localResearchContext(ctx, user, tenant), deps, aiConfig)
	if err != nil {
		deps.Cleaner.Cleanup(ctx)
		return nil, err
	}
	stop := make(chan struct{})
	var stopOnce sync.Once
	router := gin.New()
	router.Use(gin.Recovery(), middleware.ErrorHandler())
	router.Use(localResearchBoundary(address, origin))
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "persistent": true, "runtimeId": runtimeID})
	})
	router.POST("/shutdown", func(c *gin.Context) {
		if c.GetHeader("X-LingDoc-Runtime-ID") != runtimeID {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
		stopOnce.Do(func() { close(stop) })
	})
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(localResearchContext(c.Request.Context(), user, tenant))
		c.Set("user_id", user.ID)
		c.Set("tenant_id", tenant.ID)
		c.Set("tenant", tenant)
		c.Next()
	})
	router.GET("/api/local/research/bootstrap", func(c *gin.Context) { c.JSON(http.StatusOK, bootstrap) })
	api := router.Group("/api/v1")
	api.POST("/knowledge-bases/:id/knowledge/file", func(c *gin.Context) {
		if c.Param("id") != bootstrap.KnowledgeBaseID {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (8<<20)+(1<<20))
		file, err := c.FormFile("file")
		allowed := false
		if err == nil && file.Size > 0 && file.Size <= 8<<20 {
			for _, format := range bootstrap.MaterialFormats {
				allowed = allowed || strings.EqualFold(filepath.Ext(file.Filename), "."+format)
			}
		}
		if !allowed {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "LOCAL_FORMAT_UNSUPPORTED", "message": "请选择支持的资料格式，文件不超过 8 MiB。"})
			return
		}
		deps.Knowledge.CreateKnowledgeFromFile(c)
	})
	api.GET("/knowledge-bases/:id/knowledge", deps.Knowledge.ListKnowledge)
	api.GET("/knowledge/:id", deps.Knowledge.GetKnowledge)
	api.GET("/knowledge/:id/spans", deps.Knowledge.GetKnowledgeSpans)
	api.GET("/knowledge/:id/stages", deps.Knowledge.GetKnowledgeSpans)
	api.GET("/knowledge/:id/download", deps.Knowledge.DownloadKnowledgeFile)
	api.GET("/chunks/:knowledge_id", deps.Chunks.ListKnowledgeChunks)
	lingdoc := api.Group("/lingdoc")
	deps.Workspace.Register(workspace.RouteGroups{Read: lingdoc, Write: lingdoc})
	coreService, ok := deps.Projects.(*core.Service)
	if !ok {
		return nil, fmt.Errorf("production project service type mismatch")
	}
	workspace.RegisterG2FullRoutes(lingdoc, coreService)
	workspace.RegisterImpactRoutes(lingdoc, coreService)
	docx := &workspace.DOCXHTTPHandler{Runtime: deps.Runtime, Delivery: workspace.NewDOCXDeliveryService(deps.Runtime, delivery.NewSQLiteSnapshotStore(deps.DB), delivery.NewSQLiteExportStore(deps.DB))}
	docx.Register(router)
	return &LocalResearchApplication{Handler: router, Bootstrap: bootstrap, Stop: stop, Cleaner: deps.Cleaner}, nil
}

func validateLocalResearchConfig(address, origin, runtimeID string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || port == "" {
		return fmt.Errorf("LINGDOC_LOCAL_ADDR must be a loopback IP and port")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || (u.Hostname() != "localhost" && (net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback())) || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("LINGDOC_LOCAL_ORIGIN must be an exact local HTTP origin")
	}
	if strings.TrimSpace(runtimeID) == "" || os.Getenv("DB_DRIVER") != "sqlite" || strings.TrimSpace(os.Getenv("DB_PATH")) == "" || os.Getenv("DB_PATH") == ":memory:" {
		return fmt.Errorf("local runtime requires runtime ID and a persistent isolated SQLite path")
	}
	return nil
}

func localResearchBoundary(address, origin string) gin.HandlerFunc {
	alias := ""
	if configured, err := url.Parse(origin); err == nil && (configured.Hostname() == "localhost" || configured.Hostname() == "127.0.0.1") {
		aliasHost := "localhost"
		if configured.Hostname() == "localhost" {
			aliasHost = "127.0.0.1"
		}
		alias = "http://" + net.JoinHostPort(aliasHost, configured.Port())
	}
	return func(c *gin.Context) {
		remote, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		parsed := net.ParseIP(remote)
		if err != nil || parsed == nil || !parsed.IsLoopback() || c.Request.Host != address {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		incoming := c.GetHeader("Origin")
		if incoming != "" && incoming != origin && incoming != alias && incoming != "http://"+address {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		if incoming != "" {
			c.Header("Access-Control-Allow-Origin", incoming)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, X-LingDoc-Runtime-ID")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func localResearchContext(ctx context.Context, user *types.User, tenant *types.Tenant) context.Context {
	ctx = types.WithCaller(ctx, types.Caller{TenantID: tenant.ID, UserID: user.ID, Role: types.TenantRoleOwner})
	ctx = types.WithExecutionTenant(ctx, tenant.ID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	ctx = context.WithValue(ctx, types.UserIDContextKey, user.ID)
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: user.ID})
}

func bootstrapLocalResearch(ctx context.Context, deps localResearchDependencies) (LocalResearchBootstrap, *types.User, *types.Tenant, error) {
	fail := func(err error) (LocalResearchBootstrap, *types.User, *types.Tenant, error) {
		return LocalResearchBootstrap{}, nil, nil, err
	}
	const email = "lingdoc-local@localhost.invalid"
	user, err := deps.Users.GetUserByEmail(ctx, email)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		var count int64
		if dbErr := deps.DB.WithContext(ctx).Model(&types.User{}).Where("email = ?", email).Count(&count).Error; dbErr != nil || count != 0 {
			return fail(err)
		}
	}
	if user == nil {
		password := make([]byte, 32)
		if _, err = rand.Read(password); err != nil {
			return fail(err)
		}
		user, err = deps.Users.Register(ctx, &types.RegisterRequest{Username: "LingDoc Local", Email: email, Password: hex.EncodeToString(password)})
		if err != nil {
			return fail(err)
		}
	}
	tenant, err := deps.Tenants.GetTenantByID(ctx, user.TenantID)
	if err != nil {
		return fail(err)
	}
	ctx = localResearchContext(ctx, user, tenant)
	projects, ok := deps.Projects.(*core.Service)
	if !ok {
		return fail(fmt.Errorf("production project service is unavailable"))
	}
	actor := core.Actor{TenantID: tenant.ID, UserID: user.ID, Role: types.TenantRoleOwner}
	created, err := projects.CreateProjectResult(ctx, actor, "local-research-project-v1", core.CreateProjectInput{Name: "本机资料引用试用", TemplateID: "template-demo", TemplateVersion: "1"})
	if err != nil {
		return fail(fmt.Errorf("create project: %w", err))
	}
	project, err := projects.GetProject(ctx, actor, created.Project.ID)
	if err != nil {
		return fail(err)
	}
	if project.Status == "draft" {
		if project.TemplateCopy == nil {
			return fail(fmt.Errorf("project template copy missing"))
		}
		def := project.TemplateCopy.Definition
		sections := []lingdoctemplate.Section{
			{ID: "topic-summary", Title: "选题说明", Order: 1, Required: true},
			{ID: "topic-basis", Title: "选题依据", Order: 2, Required: true},
			{ID: "research-content", Title: "研究内容", Order: 3, Required: true},
			{ID: "innovation", Title: "创新之处", Order: 4, Required: true},
			{ID: "expected-results", Title: "预期成果", Order: 5, Required: true},
			{ID: "references", Title: "参考文献", Order: 6, Required: true},
		}
		if len(def.Sections) != len(sections) || def.Sections[0].ID != sections[0].ID {
			changed, err := projects.SaveTemplateCopyResult(ctx, actor, project.ID, "local-research-template-v1", core.TemplateCopyDefinitionInput{
				ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
				Fields: def.Fields, Sections: sections, Terms: append([]lingdoctemplate.Term{}, def.Terms...), RequiredFields: def.RequiredFields, Rules: def.Rules,
			})
			if err != nil {
				return fail(fmt.Errorf("initialize template: %w", err))
			}
			project = changed.Project
		}
		if len(project.Spec) == 0 {
			saved, err := projects.SaveSpecResult(ctx, actor, project.ID, "local-research-spec-v1", core.SaveSpecInput{
				ExpectedSpecRevision: project.SpecRevision, Fields: map[string]string{"research_subject": "本机资料引用试用", "research_goal": "核对资料导入、人工证据卡和正文引用"},
			})
			if err != nil {
				return fail(fmt.Errorf("initialize project spec: %w", err))
			}
			project = saved.Project
		}
		if project.TemplateCopy.Definition.RuleSetSnapshot == nil {
			set, err := ensureLocalDOCXRules(ctx, deps.DB, actor)
			if err != nil {
				return fail(fmt.Errorf("initialize DOCX rules: %w", err))
			}
			bound, err := projects.BindTemplateCopyRuleSetResult(ctx, actor, project.ID, "local-research-rules-v1", core.BindTemplateCopyRuleSetInput{
				ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
				RuleSetID: set.ID, RuleSetVersion: set.Version,
			})
			if err != nil {
				return fail(fmt.Errorf("bind DOCX rules: %w", err))
			}
			project = bound.Project
		}
		receipt, err := projects.RecordActivationRead(ctx, actor, project.ID, project.ProjectVersion)
		if err != nil {
			return fail(fmt.Errorf("read project activation: %w", err))
		}
		active, err := projects.ActivateProjectResult(ctx, actor, project.ID, "local-research-activate-v1", core.ActivateProjectInput{
			ActivationReadID: receipt.ID, ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion, ReviewedProjectVersion: project.ProjectVersion,
		})
		if err != nil {
			return fail(fmt.Errorf("activate project: %w", err))
		}
		project = active.Project
	}
	kbID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("lingdoc-local-materials:"+user.ID)).String()
	var kb types.KnowledgeBase
	err = deps.DB.WithContext(ctx).Where("id = ? AND tenant_id = ?", kbID, tenant.ID).First(&kb).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		_, err = deps.KnowledgeBases.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
			ID: kbID, Name: "本机资料库", Type: types.KnowledgeBaseTypeDocument,
			Description:      "本地 TXT/MD 人工证据链，不调用模型。",
			IndexingStrategy: types.IndexingStrategy{KeywordEnabled: true},
			ChunkingConfig:   types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: 64},
		})
	}
	if err != nil {
		return fail(fmt.Errorf("initialize knowledge base: %w", err))
	}
	chapters, err := projects.ListChapters(ctx, actor, project.ID)
	if err != nil {
		return fail(err)
	}
	out := LocalResearchBootstrap{ProjectID: project.ID, KnowledgeBaseID: kbID, TenantID: tenant.ID, ActorID: user.ID, LocalOnly: true, MaterialFormats: []string{"txt", "md"}}
	if os.Getenv("DOCREADER_ADDR") != "" {
		out.MaterialFormats = append(out.MaterialFormats, "pdf", "docx")
	}
	for _, chapter := range chapters {
		// The DOCX adapter appends from an existing chapter version. Activation
		// creates only chapter rows, so initialize missing versions through the
		// normal service with an empty, unconfirmed body and no invented source.
		if chapter.CurrentVersionID == nil || *chapter.CurrentVersionID == "" {
			working, err := projects.GetWorkingCopy(ctx, actor, project.ID, chapter.ID)
			if err != nil {
				return fail(fmt.Errorf("read initial chapter %s: %w", chapter.SectionID, err))
			}
			if working.BodyMarkdown != "" || len(working.SourceIDs) != 0 {
				return fail(fmt.Errorf("unversioned chapter %s already has draft content; preserving draft", chapter.SectionID))
			}
			saved, err := projects.SaveChapterResult(ctx, actor, project.ID, chapter.ID, "local-research-chapter-"+chapter.SectionID+"-v1", core.SaveChapterInput{
				ExpectedChapterVersionID: nil, ExpectedSpecRevision: project.SpecRevision,
				BodyMarkdown: "", SourceIDs: []string{}, CitationUsages: []core.CitationUsage{},
			})
			if err != nil {
				return fail(fmt.Errorf("initialize DOCX chapter %s: %w", chapter.SectionID, err))
			}
			chapter = saved.Chapter
		}
		out.Chapters = append(out.Chapters, LocalChapter{SectionID: chapter.SectionID, ChapterID: chapter.ID, Title: chapter.Title})
	}
	if len(out.Chapters) != 6 {
		return fail(fmt.Errorf("expected six DOCX chapter mappings, got %d", len(out.Chapters)))
	}
	return out, user, tenant, nil
}

// Publish the same three existing DOCX checks through the real catalog. No
// source is fabricated and empty chapters remain blocked at confirmation.
func ensureLocalDOCXRules(ctx context.Context, db *gorm.DB, actor core.Actor) (lingdoctemplate.RuleSet, error) {
	store := rulesetstore.NewStore(db, rulesetstore.TenantMembershipAuthorizer{})
	current := rulesetstore.Actor{TenantID: actor.TenantID, UserID: actor.UserID, Role: string(actor.Role)}
	set, err := store.GetVersion(ctx, current, "lingdoc-local-docx-rules", "1")
	if errors.Is(err, rulesetstore.ErrRuleSetNotFound) {
		set = lingdoctemplate.RuleSet{ID: "lingdoc-local-docx-rules", Version: "1", State: lingdoctemplate.RuleSetDraft}
		for _, name := range []string{"nonempty_chapter", "source_available", "review_items_decided"} {
			set.Bindings = append(set.Bindings, lingdoctemplate.RuleBinding{
				RuleID: "docx-" + name, DefinitionID: "computed/" + name, DefinitionVersion: "1",
				Origin: "builtin", Severity: "blocking", TriggerScenes: []string{"preflight", "write_check"}, Enabled: true, Owner: "lingdoc",
			})
		}
		err = store.CreateDraft(ctx, current, set)
	}
	if err != nil {
		return lingdoctemplate.RuleSet{}, err
	}
	if set.State == lingdoctemplate.RuleSetDraft {
		return store.Publish(ctx, current, set.ID, set.Version)
	}
	if set.State != lingdoctemplate.RuleSetPublished {
		return lingdoctemplate.RuleSet{}, fmt.Errorf("local DOCX rules are not published")
	}
	return set, nil
}

// Keep the transport timeout bounded without limiting asynchronous parsing.
const LocalResearchReadHeaderTimeout = 10 * time.Second
