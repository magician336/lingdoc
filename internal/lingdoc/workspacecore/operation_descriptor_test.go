package workspacecore

import (
	"testing"
)

func TestSourceOperationDescriptorKeepsTypedInputAndSensitivity(t *testing.T) {
	descriptor := sourceOperationInput(operationSaveChapter, "project/chapter", "operation-key", SaveChapterInput{
		SourceIDs: []string{"source-1", "source-2"},
	}, authorizationRequest(Actor{TenantID: 7, UserID: "owner"}, "project", AuthorizationWrite, "chapter"), saveChapterSourceIDs)
	if descriptor.Name != operationSaveChapter || descriptor.Target != "project/chapter" || descriptor.Key != "operation-key" || descriptor.Output != operationOutputChapter {
		t.Fatalf("descriptor identity = %+v", descriptor)
	}
	if descriptor.SourceIDs == nil {
		t.Fatal("source-sensitive operation lost its source decoder")
	}
	ids, err := operationSourceIDs(nil, descriptor, OperationResult{}, false)
	if err != nil {
		t.Fatalf("source decoder: %v", err)
	}
	if len(ids) != 2 || ids[0] != "source-1" || ids[1] != "source-2" {
		t.Fatalf("source ids = %v", ids)
	}
	// The descriptor owns the concrete input; mutating the decoded source list
	// must not mutate the request that is hashed for idempotency.
	ids[0] = "changed"
	if descriptor.Input.SourceIDs[0] != "source-1" {
		t.Fatalf("source decoder leaked mutable input: %+v", descriptor.Input.SourceIDs)
	}
}
