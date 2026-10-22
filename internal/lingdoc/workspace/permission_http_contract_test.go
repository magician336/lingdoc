package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type saveSpecErrorStub struct {
	ProjectApplication
	err error
}

// The handler composes the five context ports by name. The legacy read/write
// split is retired, so there is nothing left to infer: every port is handed
// over explicitly and the handler keeps the one it was given.
func TestHandlerComposesTheExplicitContextPorts(t *testing.T) {
	projects := &focusedProjectPortStub{}
	members := &focusedMembershipPortStub{}
	chapters := &focusedChapterPortStub{}
	templates := &focusedTemplatePortStub{}
	changeSets := &focusedChangeSetPortStub{}
	handler, err := NewHandler(HandlerDependencies{
		Projects:    projects,
		Members:     members,
		Chapters:    chapters,
		TemplateApp: templates,
		ChangeSets:  changeSets,
		Sources:     emptySourcesStub{},
	})
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	if handler.projects == nil || handler.members == nil || handler.chapters == nil ||
		handler.templatesApp == nil || handler.changeSets == nil {
		t.Fatalf("explicit context composition left focused route port nil: projects=%T members=%T chapters=%T templates=%T changeSets=%T", handler.projects, handler.members, handler.chapters, handler.templatesApp, handler.changeSets)
	}
}

func (s *saveSpecErrorStub) SaveSpecResult(context.Context, Actor, string, string, SaveSpecInput) (SaveSpecResult, error) {
	return SaveSpecResult{}, s.err
}

// TestRepresentativeHTTPSpecContract pins the transport half of the
// characterization matrix. The handler consumes the focused write port and
// maps typed application outcomes without asking the domain for an HTTP code.
func TestRepresentativeHTTPSpecContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))

	t.Run("replay keeps transport envelope", func(t *testing.T) {
		service := &saveSpecApplicationStub{result: SaveSpecResult{
			Project:  Project{ID: "project-1", SpecRevision: 1, ProjectVersion: 2},
			Replayed: true,
		}}
		handler, err := NewHandler(contextPorts(service, emptySourcesStub{}))
		if err != nil {
			t.Fatalf("assemble the workspace handler: %v", err)
		}
		engine := gin.New()
		handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})

		request := httptest.NewRequest(http.MethodPut, "/api/projects/project-1/spec", strings.NewReader(`{"expected_spec_revision":0,"fields":{"research_subject":"subject"}}`)).WithContext(ctx)
		request.Header.Set("Idempotency-Key", "g5-c1-http-replay")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
		}
		var envelope struct {
			Data Project `json:"data"`
			Meta struct {
				Replayed        bool `json:"replayed"`
				RefreshRequired bool `json:"refresh_required"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode replay envelope: %v", err)
		}
		if envelope.Data.ID != "project-1" || !envelope.Meta.Replayed || !envelope.Meta.RefreshRequired {
			t.Fatalf("replay envelope = %+v, want typed project and refresh metadata", envelope)
		}
	})

	t.Run("version conflict remains a domain error mapping", func(t *testing.T) {
		service := &saveSpecErrorStub{err: ErrVersionConflict}
		handler, err := NewHandler(HandlerDependencies{
			Projects:    service,
			Members:     &focusedMembershipPortStub{},
			Chapters:    &focusedChapterPortStub{},
			TemplateApp: &focusedTemplatePortStub{},
			ChangeSets:  &focusedChangeSetPortStub{},
			Sources:     emptySourcesStub{},
		})
		if err != nil {
			t.Fatalf("assemble the workspace handler: %v", err)
		}
		engine := gin.New()
		handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})

		request := httptest.NewRequest(http.MethodPut, "/api/projects/project-1/spec", strings.NewReader(`{"expected_spec_revision":0,"fields":{"research_subject":"subject"}}`)).WithContext(ctx)
		request.Header.Set("Idempotency-Key", "g5-c1-http-conflict")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)

		if response.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", response.Code, response.Body.String())
		}
		var envelope struct {
			Data  json.RawMessage `json:"data"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode conflict envelope: %v", err)
		}
		if envelope.Error.Code != "version_conflict" || len(envelope.Data) != 0 {
			t.Fatalf("conflict envelope = %+v, want version_conflict without data", envelope)
		}
	})
}
