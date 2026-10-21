package workspacecore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
)

// The member and owner storage, as ports.
//
// The tables, the rows and the ORM stay in this module. A caller asks a
// question in the workspace's language — which members a tenant has, whether a
// project holds a resource — and receives a domain value. It never learns a
// table name, a column or a row struct.
//
// These ports exist because the member and owner context is leaving the
// aggregate. A context that has left must not become storage aware: a second
// copy of the table names would be a second copy of the knowledge about how
// records are stored, and the two copies would drift.

// MemberDirectoryReader lists the members of a tenant that a project may see.
type MemberDirectoryReader interface {
	ListActiveTenantMembers(ctx context.Context, tx Transaction, tenantID uint64) ([]MemberDirectoryEntry, error)
}

// MemberRecords reads and writes the workspace's general records.
//
// The record's storage shape stays inside this module. A caller receives the
// decoded domain payload and the version, not the row.
type MemberRecords interface {
	Get(ctx context.Context, tx Transaction, projectID, kind, id string, dst any) (int64, error)
	Put(ctx context.Context, tx Transaction, projectID, kind, id string, expected int64, value any) error
	ListRecords(ctx context.Context, tx Transaction, projectID, kind string) ([]G5Record, error)
	ListTenantInvitations(ctx context.Context, tx Transaction, tenantID uint64, userID string) ([]MemberInvitation, error)
}

// G5Record is one stored record: its identity, its version, and its payload.
//
// The payload stays raw because the kinds differ: the caller decodes the one
// it knows.
type G5Record struct {
	ID      string
	Version int64
	Payload json.RawMessage
}

// OwnerTransferRecords reads and cancels a project's owner transfers.
//
// Cancel refuses a transfer that is not pending, and reports ErrVersionConflict
// when the row it changed was not the one it read.
type OwnerTransferRecords interface {
	List(ctx context.Context, tx Transaction, projectID string, project Project, userID string) ([]OwnerTransfer, error)
	Cancel(ctx context.Context, tx Transaction, projectID, transferID string) error
}

// TenantAPIKeyReader reads the tenant's API keys.
type TenantAPIKeyReader interface {
	Has(ctx context.Context, tx Transaction, tenantID uint64, keyID uint64) (bool, error)
	VerifyMachineNativeKey(ctx context.Context, tx Transaction, tenantID uint64, keyID uint64) error
}

// ProjectResourceReader reports whether a project holds a resource.
type ProjectResourceReader interface {
	ResourceExists(ctx context.Context, tx Transaction, projectID, kind, id string) (bool, error)
}

// memberRecords is the workspace's implementation of the member and owner
// storage ports.
//
// It holds the aggregate because the composition root builds the ports from
// it. The ports never hand the aggregate back: a caller receives records, not
// the workspace.
type memberRecords struct {
	service *Service
}

// NewMemberRecords returns the reader and writer for the workspace's general records.
//
// The service is the workspace's. The records it holds are the workspace's own,
// and the port never gives the service back to a caller.
func NewMemberRecords(service *Service) MemberRecords { return &memberRecords{service: service} }

// NewMemberDirectoryReader returns the reader for the members a tenant has.
//
// The service is the workspace's. The directory is the workspace's own, and the
// port never gives the service back to a caller.
func NewMemberDirectoryReader(service *Service) MemberDirectoryReader {
	return &memberRecords{service: service}
}

// NewOwnerTransferRecords returns the reader and canceller for owner transfers.
//
// The service is the workspace's. The transfers are the workspace's own, and
// the port never gives the service back to a caller.
func NewOwnerTransferRecords(service *Service) OwnerTransferRecords {
	return &memberRecords{service: service}
}

// NewTenantAPIKeyReader returns the reader for a tenant's API keys.
//
// The service is the workspace's. The keys are the workspace's own, and the
// port never gives the service back to a caller.
func NewTenantAPIKeyReader(service *Service) TenantAPIKeyReader {
	return &memberRecords{service: service}
}

// NewProjectResourceReader returns the reader for a project's resources.
//
// The service is the workspace's. The resources are the workspace's own, and
// the port never gives the service back to a caller.
func NewProjectResourceReader(service *Service) ProjectResourceReader {
	return &memberRecords{service: service}
}

// memberRecords returns the workspace's general records.
func (s *Service) memberRecords() MemberRecords { return &memberRecords{service: s} }

// memberDirectoryReader returns the workspace's member directory.
func (s *Service) memberDirectoryReader() MemberDirectoryReader { return &memberRecords{service: s} }

// ownerTransferRecords returns the workspace's owner transfer records.
func (s *Service) ownerTransferRecords() OwnerTransferRecords { return &memberRecords{service: s} }

// tenantAPIKeyReader returns the workspace's tenant API keys.
func (s *Service) tenantAPIKeyReader() TenantAPIKeyReader { return &memberRecords{service: s} }

// listActiveTenantMembers reads the members of a tenant that a project may see.
//
// The row shape stays here: the caller receives the domain entries.
func (s *Service) listActiveTenantMembers(tx Transaction, tenantID uint64) ([]MemberDirectoryEntry, error) {
	var out []MemberDirectoryEntry
	db, err := TransactionDatabase(tx)
	if err != nil {
		return nil, err
	}
	err = db.Table("tenant_members AS tm").Select("tm.user_id, u.username AS name, tm.role AS tenant_role").Joins("JOIN users u ON u.id=tm.user_id AND u.deleted_at IS NULL AND u.is_active=?", true).Where("tm.tenant_id=? AND tm.status='active' AND tm.deleted_at IS NULL", tenantID).Order("u.username,tm.user_id").Limit(200).Scan(&out).Error
	return out, err
}

// getMemberRecord reads one general record and its version.
//
// The record's storage shape stays here: the caller receives the decoded
// payload and the version.
func (s *Service) getMemberRecord(tx Transaction, projectID, kind, id string, dst any) (int64, error) {
	store, ok := tx.(g5Transaction)
	if !ok {
		return 0, ErrInvalidState
	}
	return store.G5Get(projectID, kind, id, dst)
}

// putMemberRecord writes one general record, refusing a stale version.
func (s *Service) putMemberRecord(tx Transaction, projectID, kind, id string, expected int64, value any) error {
	store, ok := tx.(g5Transaction)
	if !ok {
		return ErrInvalidState
	}
	return store.G5Put(projectID, kind, id, expected, value)
}

// listMemberRecords reads the records of one kind in a project.
//
// The row shape stays here: the caller receives each record's identity, its
// version and its payload.
func (s *Service) listMemberRecords(tx Transaction, projectID, kind string) ([]G5Record, error) {
	store, ok := tx.(g5Transaction)
	if !ok {
		return nil, ErrInvalidState
	}
	rows, err := store.G5List(projectID, kind)
	if err != nil {
		return nil, err
	}
	records := make([]G5Record, 0, len(rows))
	for _, row := range rows {
		records = append(records, G5Record{
			ID:      row.ID,
			Version: row.Version,
			Payload: json.RawMessage(row.PayloadJSON),
		})
	}
	return records, nil
}

// listTenantInvitations reads the invitations a tenant holds for one user.
//
// The row shape stays here: the caller receives the domain invitations.
func (s *Service) listTenantInvitations(tx Transaction, tenantID uint64, userID string) ([]MemberInvitation, error) {
	result := []MemberInvitation{}
	db, err := TransactionDatabase(tx)
	if err != nil {
		return result, err
	}
	var rows []g5RecordRow
	if err := db.Table("lingdoc_g5_records AS g").Select("g.*").Joins("JOIN lingdoc_projects p ON p.id=g.project_id").Where("p.tenant_id=? AND g.kind='invitation'", tenantID).Order("g.id").Find(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		var item MemberInvitation
		if err := json.Unmarshal([]byte(row.PayloadJSON), &item); err != nil {
			return result, err
		}
		if item.UserID == userID && item.Status == "invited" && time.Now().UTC().Before(item.ExpiresAt) {
			result = append(result, item)
		}
	}
	return result, nil
}

// listOwnerTransfers reads the owner transfers of a project that one user may see.
//
// The row shape stays here: the caller receives the domain transfers.
func (s *Service) listOwnerTransfers(tx Transaction, projectID string, p Project, userID string) ([]OwnerTransfer, error) {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return nil, err
	}
	var rows []ownerTransferRow
	if err := db.Where("project_id=?", projectID).Order("created_at DESC,id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := []OwnerTransfer{}
	for _, row := range rows {
		item := OwnerTransfer(row)
		if !projectOwner(p, userID) && item.FromUserID != userID && item.ToUserID != userID {
			continue
		}
		if item.Status == "pending" && time.Since(item.CreatedAt) > 72*time.Hour {
			item.Status = "expired"
		}
		result = append(result, item)
	}
	return result, nil
}

// cancelOwnerTransfer cancels a pending owner transfer.
//
// The affected row count is the guard: a transfer that was not pending, or
// that was not the row this call read, is reported as ErrVersionConflict.
func (s *Service) cancelOwnerTransfer(tx Transaction, projectID, transferID string) error {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return err
	}
	update := db.Model(&ownerTransferRow{}).Where("id=? AND project_id=? AND status='pending'", transferID, projectID).Update("status", "cancelled")
	return affected(update)
}

// hasTenantAPIKey reports whether the tenant owns the key.
func (s *Service) hasTenantAPIKey(tx Transaction, tenantID uint64, keyID uint64) (bool, error) {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return false, err
	}
	var count int64
	if err := db.Table("tenant_api_keys").Where("id=? AND tenant_id=?", keyID, tenantID).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 1, nil
}

// verifyMachineNativeKey reports whether the key is a usable native machine key.
//
// The key stays inside this module: the caller learns whether the key may be
// used, not what it contains.
func (s *Service) verifyMachineNativeKey(tx Transaction, tenantID uint64, keyID uint64) error {
	_, err := currentMachineNativeKey(tx, tenantID, keyID)
	return err
}

// resourceExists reports whether a project holds a stored resource.
//
// The kinds that name a stored resource are answered from the project's own
// rows. The template scope is not one of them: it names the project's template
// copy, and that rule belongs to the caller, which holds the project.
func (s *Service) resourceExists(tx Transaction, projectID, kind, id string) (bool, error) {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return false, err
	}
	var count int64
	switch kind {
	case "asset", "knowledge":
		q := db.Model(&evidence.ProjectAsset{}).Where("project_id=?", projectID)
		if kind == "asset" {
			q = q.Where("id=?", id)
		} else {
			q = q.Where("knowledge_id=?", id)
		}
		if err := q.Count(&count).Error; err != nil {
			return false, err
		}
	case "delivery":
		if id == projectID {
			count = 1
		} else {
			if err := db.Table("lingdoc_release_snapshots").Where("project_id=? AND id=?", projectID, id).Count(&count).Error; err != nil {
				return false, err
			}
		}
	default:
		return false, ErrInvalidRequest
	}
	return count == 1, nil
}

// ListActiveTenantMembers lists the members of a tenant that a project may see.
func (r *memberRecords) ListActiveTenantMembers(_ context.Context, tx Transaction, tenantID uint64) ([]MemberDirectoryEntry, error) {
	return r.service.listActiveTenantMembers(tx, tenantID)
}

// Get reads one general record and returns its version.
func (r *memberRecords) Get(_ context.Context, tx Transaction, projectID, kind, id string, dst any) (int64, error) {
	return r.service.getMemberRecord(tx, projectID, kind, id, dst)
}

// Put writes one general record, refusing a stale version.
func (r *memberRecords) Put(_ context.Context, tx Transaction, projectID, kind, id string, expected int64, value any) error {
	return r.service.putMemberRecord(tx, projectID, kind, id, expected, value)
}

// ListRecords reads the records of one kind in a project.
//
// The caller decodes the payload it knows: the kinds differ.
func (r *memberRecords) ListRecords(_ context.Context, tx Transaction, projectID, kind string) ([]G5Record, error) {
	return r.service.listMemberRecords(tx, projectID, kind)
}

// ListTenantInvitations reads the invitations a tenant holds for one user.
func (r *memberRecords) ListTenantInvitations(_ context.Context, tx Transaction, tenantID uint64, userID string) ([]MemberInvitation, error) {
	return r.service.listTenantInvitations(tx, tenantID, userID)
}

// List reads the owner transfers of a project that one user may see.
func (r *memberRecords) List(_ context.Context, tx Transaction, projectID string, project Project, userID string) ([]OwnerTransfer, error) {
	return r.service.listOwnerTransfers(tx, projectID, project, userID)
}

// Cancel cancels a pending owner transfer.
func (r *memberRecords) Cancel(_ context.Context, tx Transaction, projectID, transferID string) error {
	return r.service.cancelOwnerTransfer(tx, projectID, transferID)
}

// Has reports whether the tenant owns the key.
func (r *memberRecords) Has(_ context.Context, tx Transaction, tenantID uint64, keyID uint64) (bool, error) {
	return r.service.hasTenantAPIKey(tx, tenantID, keyID)
}

// VerifyMachineNativeKey reports whether the key is a usable native machine key.
func (r *memberRecords) VerifyMachineNativeKey(_ context.Context, tx Transaction, tenantID uint64, keyID uint64) error {
	return r.service.verifyMachineNativeKey(tx, tenantID, keyID)
}

// ResourceExists reports whether a project holds a stored resource.
func (r *memberRecords) ResourceExists(_ context.Context, tx Transaction, projectID, kind, id string) (bool, error) {
	return r.service.resourceExists(tx, projectID, kind, id)
}
