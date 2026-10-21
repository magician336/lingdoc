package workspacecore

// TransferOwnerInput is the request that moves a project's ownership.
//
// The operation itself lives in the member and owner context
// (internal/lingdoc/membership); the request type stays here because it is
// part of the workspace's own language.
type TransferOwnerInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	NewOwnerUserID         string `json:"new_owner_user_id"`
}
