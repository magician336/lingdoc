package candidateadoption

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

type deliveryInputReaderFunc func(context.Context, string) (WorkspaceDeliveryInput, error)

func (f deliveryInputReaderFunc) ReadDeliveryInput(ctx context.Context, projectID string) (WorkspaceDeliveryInput, error) {
	return f(ctx, projectID)
}

type deliveryInputAuthorizerFunc func(context.Context, string, string) error

func (f deliveryInputAuthorizerFunc) AuthorizeProject(ctx context.Context, actorID, projectID string) error {
	return f(ctx, actorID, projectID)
}

func TestDeliveryInputServiceAuthorizesBeforeReading(t *testing.T) {
	denied := errors.New("not a member")
	readerCalls := 0
	service := DeliveryInputService{
		Authorizer: deliveryInputAuthorizerFunc(func(_ context.Context, actorID, projectID string) error {
			if actorID != "actor-1" || projectID != "project-1" {
				t.Fatalf("authorization scope = %s/%s", actorID, projectID)
			}
			return denied
		}),
		Reader: deliveryInputReaderFunc(func(context.Context, string) (WorkspaceDeliveryInput, error) {
			readerCalls++
			return WorkspaceDeliveryInput{}, nil
		}),
	}
	if _, err := service.Read(context.Background(), "actor-1", "project-1"); !errors.Is(err, denied) {
		t.Fatalf("got %v, want authorization error", err)
	}
	if readerCalls != 0 {
		t.Fatal("workspace content read before project authorization")
	}
}

func TestDeliveryInputServiceRejectsMissingDependenciesAndIDs(t *testing.T) {
	service := DeliveryInputService{}
	if _, err := service.Read(context.Background(), "actor-1", "project-1"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("got %v, want invalid state", err)
	}
	service = DeliveryInputService{
		Authorizer: deliveryInputAuthorizerFunc(func(context.Context, string, string) error { return nil }),
		Reader:     deliveryInputReaderFunc(func(context.Context, string) (WorkspaceDeliveryInput, error) { return WorkspaceDeliveryInput{}, nil }),
	}
	if _, err := service.Read(context.Background(), "", "project-1"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("got %v, want invalid request", err)
	}
}

// 路线感知的投影会**覆盖**规格里路线节点的值：模板声明为必需、而路线图还没给出
// 的那个节点，投影里是空的。原始规格不能因此丢失——溯源快照记的就是它（#324）。
//
// 少了原始规格，交付检查就会拿投影那份去比溯源快照：值对不上，来源访问被拒，
// 带必需路线节点的项目连检查都读不出来。
func TestReadDeliveryInputKeepsTheStoredSpecUnderTheRouteProjection(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	seedRouteNodeWorkspace(t, store)

	input, err := store.ReadDeliveryInput(context.Background(), "project-1")
	require.NoError(t, err)
	require.Equal(t, "", input.Spec[lingdoctemplate.DemoRequiredRouteNodeID],
		"the route aware projection leaves a declared but missing route node empty")
	require.Equal(t, routeNodePlaceholder, input.SpecSource[lingdoctemplate.DemoRequiredRouteNodeID],
		"the stored spec is the one the provenance snapshot records")
	require.Equal(t, "验证", input.SpecSource["research_goal"],
		"the stored spec stays whole under the route aware projection")
}

// routeNodePlaceholder 是激活闸门逼客户端写进规格的那个占位值。路线图还没给出
// 这个节点时，它不能顶替那个节点——但它仍然是规格里的值，溯源快照记的就是它。
const routeNodePlaceholder = "合成路线占位值（真正的值由路线图节点提供）"

// seedRouteNodeWorkspace 落一个「模板副本声明了必需路线节点、规格里带占位值」
// 的项目，并让路线图那几张表存在——路线感知的投影只在它们存在时才算。
func seedRouteNodeWorkspace(t *testing.T, store *SQLiteCandidateAdoptionStore) {
	t.Helper()
	require.NoError(t, store.UpsertProject(context.Background(), "project-1", 6))
	spec, err := json.Marshal(map[string]string{
		"research_goal":                         "验证",
		lingdoctemplate.DemoRequiredRouteNodeID: routeNodePlaceholder,
	})
	require.NoError(t, err)
	require.NoError(t, store.DB().Model(&projectRow{}).Where("id = ?", "project-1").Updates(map[string]any{
		"spec_json": string(spec), "template_copy_version": 1,
	}).Error)

	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS lingdoc_members (project_id TEXT NOT NULL, user_id TEXT NOT NULL, role TEXT NOT NULL, PRIMARY KEY (project_id, user_id))`,
		`CREATE TABLE IF NOT EXISTS lingdoc_member_permissions (project_id TEXT NOT NULL, user_id TEXT NOT NULL, governance_role TEXT NOT NULL, function_roles_json TEXT NOT NULL DEFAULT '[]', function_scopes_json TEXT NOT NULL DEFAULT '{}', status TEXT NOT NULL, PRIMARY KEY (project_id, user_id))`,
		`CREATE TABLE IF NOT EXISTS lingdoc_g3_records (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, kind TEXT NOT NULL, record_key TEXT NOT NULL, version BIGINT NOT NULL, body_json TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, UNIQUE(project_id,kind,record_key))`,
		`CREATE TABLE IF NOT EXISTS lingdoc_project_template_copies (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, source_template_id TEXT NOT NULL, source_template_version TEXT NOT NULL, version INTEGER NOT NULL, status TEXT NOT NULL, content_hash TEXT NOT NULL, ruleset_hash TEXT NOT NULL, definition_json TEXT NOT NULL, created_by TEXT NOT NULL, created_at DATETIME NOT NULL, UNIQUE(project_id,version))`,
	} {
		require.NoError(t, store.DB().Exec(ddl).Error)
	}

	definition, err := lingdoctemplate.WithHashes(lingdoctemplate.DemoRouteTemplate())
	require.NoError(t, err)
	definitionJSON, err := json.Marshal(definition)
	require.NoError(t, err)
	require.NoError(t, store.DB().Exec(
		`INSERT INTO lingdoc_project_template_copies(id,project_id,source_template_id,source_template_version,version,status,content_hash,ruleset_hash,definition_json,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"route-copy", "project-1", definition.ID, definition.Version, 1, core.TemplateCopyBound,
		definition.ContentHash, definition.RulesetHash, string(definitionJSON), "reader", time.Now().UTC()).Error)
}
