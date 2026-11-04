package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// #469 残留②:租户级授权拒绝事件落在 project_id="" 上,按课题读不到。
// 新读面 GET /runtime/tenant-events 把这部分事件按租户读出来,授权给
// **租户管理者**(与租户范围控制记录的处置一致);既有按课题读端点
// (/projects/:projectId/runtime/events)语义零变化。
func TestG6TenantEventsEndpointServesTheTenantLevelDenials(t *testing.T) {
	f := newRoadmapHTTPFixture(t)
	ctx := policyContext()

	// 触发一次租户级授权拒绝:同租户但不在租户成员表里的用户列项目。
	stranger := Actor{TenantID: 7, UserID: "not-a-tenant-member"}
	if _, _, err := f.service.ListProjects(ctx, stranger); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("a stranger listed the projects: %v", err)
	}

	// 既有按课题读端点:租户级事件按设计不可见(契约①)。
	byProject := httptest.NewRecorder()
	f.engine.ServeHTTP(byProject, httptest.NewRequest(http.MethodGet, "/api/projects/"+f.project.ID+"/runtime/events", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, byProject.Code, "body=%s", byProject.Body.String())
	var projectListed struct {
		Data []core.RuntimeEvent `json:"data"`
	}
	require.NoError(t, json.Unmarshal(byProject.Body.Bytes(), &projectListed))
	for _, event := range projectListed.Data {
		require.False(t, event.Category == "permission" && event.Status == "deny",
			"a project-scoped read must not return tenant-level denials: %+v", event)
	}

	// 新读面:租户管理者(context 带租户管理员角色)读出拒绝事件。
	tenantManager := func() context.Context {
		c := context.WithValue(context.Background(), types.UserIDContextKey, deliveryActorID)
		c = context.WithValue(c, types.TenantIDContextKey, uint64(7))
		return context.WithValue(c, types.TenantRoleContextKey, types.TenantRoleAdmin)
	}()
	request := httptest.NewRequest(http.MethodGet, "/api/runtime/tenant-events", nil).WithContext(tenantManager)
	response := httptest.NewRecorder()
	f.engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "body=%s", response.Body.String())
	var listed struct {
		Data []core.RuntimeEvent `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	denied := 0
	for _, event := range listed.Data {
		if event.Category == "permission" && event.Status == "deny" {
			denied++
			require.NotEmpty(t, event.Capability, "the denial event must name the refused capability")
		}
	}
	require.Equal(t, 1, denied, "the tenant read must return exactly one denial event: %+v", listed.Data)

	// 非租户管理者(同租户普通成员)读租户级事件面:拒绝(存在性隐藏)。
	plainMember := func() context.Context {
		c := context.WithValue(context.Background(), types.UserIDContextKey, "plain-member")
		c = context.WithValue(c, types.TenantIDContextKey, uint64(7))
		return context.WithValue(c, types.TenantRoleContextKey, types.TenantRoleContributor)
	}()
	memberResponse := httptest.NewRecorder()
	f.engine.ServeHTTP(memberResponse, httptest.NewRequest(http.MethodGet, "/api/runtime/tenant-events", nil).WithContext(plainMember))
	require.NotEqual(t, http.StatusOK, memberResponse.Code, "a plain member must not read tenant-level events")
}
