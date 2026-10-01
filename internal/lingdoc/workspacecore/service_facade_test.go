package workspacecore

import (
	"context"
	"reflect"
	"testing"
)

type repositoryStub struct {
	Repository
	project Project
}

func (r repositoryStub) GetProject(context.Context, Actor, string) (Project, error) {
	return r.project, nil
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
