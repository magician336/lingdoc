package candidateadoption

import (
	"context"
	"errors"
	"testing"
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
