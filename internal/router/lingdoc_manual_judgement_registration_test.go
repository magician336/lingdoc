package router

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/dig"
)

// The manual-judgement channel of the F01 runner (#649) is registered by the
// production router, not by the workspace handler.  The workspace package
// proves that the two endpoints answer correctly; what nothing proved was
// that the production router registers them at all.  The three tests that
// arrived with the endpoint called workspace.RegisterManualJudgementRoutes
// directly, so deleting the registration branch in internal/router/router.go
// left the whole suite green while production stopped serving the two
// endpoints.
//
// These tests therefore go through router.NewRouter with a real dig
// container - the assembly path production uses - and read the route table
// the assembled engine actually serves.
//
// The narrow port behind the two routes is a stub here: this guard is about
// the registration, not about what the endpoints answer.  The real port and
// the real service behind it are proven in internal/container.

// manualJudgementPortStub is the narrow port the two routes speak to.  It
// answers with the smallest truthful answer it can; the guard only asks
// whether the routes are on the route table at all.
type manualJudgementPortStub struct{}

func (manualJudgementPortStub) SubmitManualJudgement(
	context.Context, core.Actor, string, string, core.ManualJudgementInput,
) (json.RawMessage, int, bool, error) {
	return json.RawMessage(`{}`), http.StatusCreated, false, nil
}

func (manualJudgementPortStub) ExportManualJudgements(
	context.Context, core.Actor, string,
) ([]core.ManualJudgementAudit, error) {
	return nil, nil
}

// provideZero provides the zero value of T as the dependency of that type.
// The router registers its routes from the handlers it is given, so it needs
// their types rather than their behaviour.  The one dependency the router
// reads at startup is the system handler (it binds the deployment
// capabilities snapshot onto it), and that one is a real instance.
func provideZero[T any](t *testing.T, c *dig.Container) {
	t.Helper()
	if err := c.Provide(func() T {
		var zero T
		return zero
	}); err != nil {
		t.Fatalf("provide the zero value: %v", err)
	}
}

// manualJudgementRouter assembles the production router the way the
// composition root does: dig resolves RouterParams and the router is built
// from them.  The narrow port is handed in, so the only difference between
// the two tests below is whether that port is composed at all - which is
// exactly the decision the production router has to make.
func manualJudgementRouter(t *testing.T, port workspace.ManualJudgementApplication) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	c := dig.New()
	enabled := true
	if err := c.Provide(func() *config.Config {
		return &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
	}); err != nil {
		t.Fatalf("provide the configuration: %v", err)
	}
	if err := c.Provide(func() *handler.SystemHandler { return &handler.SystemHandler{} }); err != nil {
		t.Fatalf("provide the system handler: %v", err)
	}
	provideZero[interfaces.FileService](t, c)
	provideZero[interfaces.UserService](t, c)
	provideZero[interfaces.KnowledgeBaseService](t, c)
	provideZero[interfaces.KnowledgeService](t, c)
	provideZero[interfaces.ChunkService](t, c)
	provideZero[interfaces.SessionService](t, c)
	provideZero[interfaces.MessageService](t, c)
	provideZero[interfaces.ModelService](t, c)
	provideZero[interfaces.EvaluationService](t, c)
	provideZero[interfaces.KBShareService](t, c)
	provideZero[interfaces.AgentShareService](t, c)
	provideZero[interfaces.TenantService](t, c)
	provideZero[interfaces.TenantAPIKeyService](t, c)
	provideZero[interfaces.TenantMemberService](t, c)
	provideZero[interfaces.AuditLogService](t, c)
	provideZero[interfaces.StorageBackendResolver](t, c)
	provideZero[interfaces.ResourceCatalog](t, c)
	provideZero[interfaces.EmbedChannelService](t, c)
	provideZero[*handler.KnowledgeBaseHandler](t, c)
	provideZero[*handler.KnowledgeHandler](t, c)
	provideZero[*handler.TenantHandler](t, c)
	provideZero[*handler.TenantMemberHandler](t, c)
	provideZero[*handler.TenantInvitationHandler](t, c)
	provideZero[*handler.AuditLogHandler](t, c)
	provideZero[*handler.ChunkHandler](t, c)
	provideZero[*session.Handler](t, c)
	provideZero[*handler.MessageHandler](t, c)
	provideZero[*handler.MessageSuggestionHandler](t, c)
	provideZero[*handler.ModelHandler](t, c)
	provideZero[*handler.ModelCredentialsHandler](t, c)
	provideZero[*handler.SandboxConfigHandler](t, c)
	provideZero[*handler.SandboxSkillHandler](t, c)
	provideZero[*handler.MeEnvVarHandler](t, c)
	provideZero[*handler.EvaluationHandler](t, c)
	provideZero[*handler.AuthHandler](t, c)
	provideZero[*handler.InitializationHandler](t, c)
	provideZero[*handler.MCPServiceHandler](t, c)
	provideZero[*handler.MCPCredentialsHandler](t, c)
	provideZero[*handler.MCPOAuthHandler](t, c)
	provideZero[*handler.WebSearchHandler](t, c)
	provideZero[*handler.WebSearchProviderHandler](t, c)
	provideZero[*handler.WebSearchProviderCredentialsHandler](t, c)
	provideZero[*handler.VectorStoreHandler](t, c)
	provideZero[*handler.StorageBackendHandler](t, c)
	provideZero[*handler.FAQHandler](t, c)
	provideZero[*handler.TagHandler](t, c)
	provideZero[*handler.CustomAgentHandler](t, c)
	provideZero[*handler.UserResourceFavoriteHandler](t, c)
	provideZero[*handler.SkillHandler](t, c)
	provideZero[*handler.OrganizationHandler](t, c)
	provideZero[*handler.IMHandler](t, c)
	provideZero[*handler.EmbedChannelHandler](t, c)
	provideZero[*redis.Client](t, c)
	provideZero[*handler.DataSourceHandler](t, c)
	provideZero[*handler.DataSourceCredentialsHandler](t, c)
	provideZero[*handler.WeKnoraCloudHandler](t, c)
	provideZero[*handler.WikiPageHandler](t, c)
	provideZero[*handler.MemoryHandler](t, c)
	provideZero[*workspace.Handler](t, c)
	provideZero[*rulesetstore.HTTPHandler](t, c)
	provideZero[*workspace.DeliveryHandler](t, c)
	provideZero[*workspace.DeliveryExportHandler](t, c)
	provideZero[*candidateadoption.CandidateAdoptionHandler](t, c)

	// The narrow port is optional in the production dependency graph: an
	// absent port leaves the field at its zero value, and the router then
	// registers nothing for it.
	if port != nil {
		if err := c.Provide(func() workspace.ManualJudgementApplication { return port }); err != nil {
			t.Fatalf("provide the manual-judgement port: %v", err)
		}
	}

	var engine *gin.Engine
	if err := c.Invoke(func(params RouterParams) { engine = NewRouter(params) }); err != nil {
		t.Fatalf("assemble the production router: %v", err)
	}
	return engine
}

// The two production paths the manual-judgement channel serves.
var manualJudgementProductionRoutes = []string{
	"POST /api/v1/lingdoc/projects/:projectId/manual-judgements",
	"GET /api/v1/lingdoc/projects/:projectId/manual-judgements/audit-records",
}

// manualJudgementRouteTable reads the route table the assembled engine
// serves, keyed by "METHOD path".
func manualJudgementRouteTable(engine *gin.Engine) map[string]bool {
	table := map[string]bool{}
	for _, route := range engine.Routes() {
		table[route.Method+" "+route.Path] = true
	}
	return table
}

// When the narrow port is composed, the production router serves the two
// manual-judgement endpoints.  This is the seam the workspace package cannot
// prove on its own: the workspace package registers the routes itself, while
// production reaches them through router.NewRouter.
func TestNewRouterServesTheManualJudgementRoutesWhenThePortIsComposed(t *testing.T) {
	engine := manualJudgementRouter(t, manualJudgementPortStub{})

	table := manualJudgementRouteTable(engine)
	for _, want := range manualJudgementProductionRoutes {
		if !table[want] {
			t.Errorf("the production router does not serve %s", want)
		}
	}
}

// When the narrow port is not composed, the two endpoints are absent: an
// absent port is an absent endpoint, not a route that fails on the first
// request.
func TestNewRouterOmitsTheManualJudgementRoutesWhenThePortIsAbsent(t *testing.T) {
	engine := manualJudgementRouter(t, nil)

	table := manualJudgementRouteTable(engine)
	for _, absent := range manualJudgementProductionRoutes {
		if table[absent] {
			t.Errorf("the production router serves %s although the narrow port is not composed", absent)
		}
	}
}
