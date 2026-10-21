package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type preflightHTTPBindingProbe struct{ ids []string }

func (p *preflightHTTPBindingProbe) Recheck(_ context.Context, _ core.Actor, _, proofID string) (core.ImpactPreflightEvidence, error) {
	p.ids = append(p.ids, proofID)
	// Intentionally refuse the proof after observing the transport boundary.
	// No invented PASS proof and no mutation of tasks or publication state.
	return core.ImpactPreflightEvidence{}, core.ErrVersionConflict
}
func TestPreflightUsesBodyProofID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newDeliveryGuardFixture(t, false)
	probe := &preflightHTTPBindingProbe{}
	fixture.projects.SetImpactPreflightChecker(probe)
	engine := gin.New()
	RegisterImpactRoutes(engine.Group("/api"), impactPortsFor(fixture.projects))
	request := httptest.NewRequest(http.MethodPost, "/api/projects/"+fixture.project.ID+"/change-sets/actual-change/preflight", strings.NewReader(`{"proof_id":"current-check-receipt"}`)).WithContext(fixture.ctx)
	request.Header.Set("Idempotency-Key", "separate-http-retry-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Equal(t, []string{"current-check-receipt"}, probe.ids, "the proof lookup must never receive the operation retry key")

	request = httptest.NewRequest(http.MethodPost, "/api/projects/"+fixture.project.ID+"/change-sets/actual-change/preflight", strings.NewReader(`{"proof_id":"another-check"}`)).WithContext(fixture.ctx)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Len(t, probe.ids, 1, "no proof lookup without an idempotency header")

	machine := types.WithPrincipal(fixture.ctx, types.Principal{Type: types.PrincipalAPITenant, ID: "synthetic-machine"})
	request = httptest.NewRequest(http.MethodPost, "/api/projects/"+fixture.project.ID+"/change-sets/actual-change/preflight", strings.NewReader(`{"proof_id":"another-check"}`)).WithContext(machine)
	request.Header.Set("Idempotency-Key", "machine-key")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	require.Len(t, probe.ids, 1, "machine callers cannot perform the Owner's preflight decision")
}

func TestUnstableDeliveryExplainsOwnerGate(t *testing.T) {
	require.ErrorIs(t, core.ErrRevisionNotStable, core.ErrInvalidState)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/synthetic-release", nil)
	sendError(ctx, fmt.Errorf("private implementation details: %w", core.ErrRevisionNotStable))
	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	var reply struct {
		Error struct{ Code, Message string }
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &reply))
	require.Equal(t, "context_unstable", reply.Error.Code)
	require.Contains(t, reply.Error.Message, "交付检查回执")
	require.Contains(t, reply.Error.Message, "稳定化")
	require.NotContains(t, reply.Error.Message, "private implementation")
}
