package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

type repositoryStub struct {
	Repository
	project     Project
	saveChapter func(context.Context, Actor, string, string, string, SaveChapterInput) (json.RawMessage, int, bool, error)
}

func (r repositoryStub) GetProject(context.Context, Actor, string) (Project, error) {
	return r.project, nil
}

func (r repositoryStub) SaveChapter(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveChapterInput) (json.RawMessage, int, bool, error) {
	if r.saveChapter == nil {
		return nil, 0, false, nil
	}
	return r.saveChapter(ctx, actor, projectID, chapterID, key, input)
}

func TestServiceUsesInjectedRepository(t *testing.T) {
	want := Project{ID: "project-1", Name: "test"}
	service := NewService(repositoryStub{project: want})

	got, err := service.GetProject(context.Background(), Actor{TenantID: 1, UserID: "user-1"}, want.ID)
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetProject() = %#v, want %#v", got, want)
	}
}

func TestServiceSaveChapterPreservesRepositoryOutcomes(t *testing.T) {
	type outcome struct {
		body     json.RawMessage
		status   int
		replayed bool
		err      error
	}
	cases := []struct {
		name       string
		response   outcome
		wantBody   string
		wantStatus int
		wantReplay bool
		wantErr    error
	}{
		{name: "successful write", response: outcome{json.RawMessage(`{"version_id":"v1"}`), 201, false, nil}, wantBody: `{"version_id":"v1"}`, wantStatus: 201},
		{name: "version conflict", response: outcome{err: ErrVersionConflict}, wantErr: ErrVersionConflict},
		{name: "revoked project access", response: outcome{err: ErrNotFound}, wantErr: ErrNotFound},
		{name: "source changed", response: outcome{err: ErrSourceUnavailable}, wantErr: ErrSourceUnavailable},
		{name: "lost response retry", response: outcome{json.RawMessage(`{"version_id":"v1"}`), 201, true, nil}, wantBody: `{"version_id":"v1"}`, wantStatus: 201, wantReplay: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			repository := repositoryStub{saveChapter: func(context.Context, Actor, string, string, string, SaveChapterInput) (json.RawMessage, int, bool, error) {
				calls++
				return tc.response.body, tc.response.status, tc.response.replayed, tc.response.err
			}}
			service := NewService(repository)
			body, status, replayed, err := service.SaveChapter(context.Background(), Actor{TenantID: 1, UserID: "writer"}, "p1", "c1", "save-key-1", SaveChapterInput{})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if string(body) != tc.wantBody || status != tc.wantStatus || replayed != tc.wantReplay || calls != 1 {
				t.Fatalf("SaveChapter() = %s/%d/replay=%v/calls=%d, want %s/%d/replay=%v/calls=1", body, status, replayed, calls, tc.wantBody, tc.wantStatus, tc.wantReplay)
			}
		})
	}
}
