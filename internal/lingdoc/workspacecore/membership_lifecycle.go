package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"slices"
	"time"
)

type MemberInvitation struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	UserID     string     `json:"user_id"`
	Version    int64      `json:"version"`
	Assignment Member     `json:"assignment"`
	Status     string     `json:"status"`
	Inviter    string     `json:"inviter"`
	Reason     string     `json:"reason"`
	ExpiresAt  time.Time  `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
}
type InviteMemberInput struct {
	ExpectedProjectVersion    int64  `json:"expected_project_version"`
	Assignment                Member `json:"assignment"`
	Reason                    string `json:"reason"`
	InvitationID              string `json:"invitation_id,omitempty"`
	ExpectedInvitationVersion int64  `json:"expected_invitation_version,omitempty"`
}
type AcceptMemberInput struct {
	ExpectedInvitationVersion int64 `json:"expected_invitation_version"`
}
type MemberLifecycleInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	Action                 string `json:"action"`
	Reason                 string `json:"reason"`
}
type ActivationReadReceipt struct {
	ID             string    `json:"id"`
	ActorID        string    `json:"actor_id"`
	ProjectID      string    `json:"project_id"`
	ProjectVersion int64     `json:"project_version"`
	SpecRevision   int64     `json:"spec_revision"`
	ReadAt         time.Time `json:"read_at"`
}
type projectRecordRow struct {
	ProjectID   string `gorm:"primaryKey"`
	Kind        string `gorm:"primaryKey"`
	ID          string `gorm:"primaryKey"`
	Version     int64
	PayloadJSON string
}

func (projectRecordRow) TableName() string { return "lingdoc_g5_records" }

type projectRecordTransaction interface {
	GetProjectRecord(string, string, string, any) (int64, error)
	PutProjectRecord(string, string, string, int64, any) error
	ListProjectRecords(string, string) ([]projectRecordRow, error)
}

func (t gormTransaction) GetProjectRecord(p, k, id string, dst any) (int64, error) {
	var row projectRecordRow
	err := t.db.Where("project_id = ? AND kind = ? AND id = ?", p, k, id).Take(&row).Error
	if err != nil {
		return 0, storageError(err)
	}
	return row.Version, json.Unmarshal([]byte(row.PayloadJSON), dst)
}
func (t gormTransaction) PutProjectRecord(p, k, id string, expected int64, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if expected == 0 {
		return t.db.Create(&projectRecordRow{ProjectID: p, Kind: k, ID: id, Version: 1, PayloadJSON: string(raw)}).Error
	}
	result := t.db.Model(&projectRecordRow{}).Where("project_id = ? AND kind = ? AND id = ? AND version = ?", p, k, id, expected).Updates(map[string]any{"version": expected + 1, "payload_json": string(raw)})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}
func (t gormTransaction) ListProjectRecords(p, k string) ([]projectRecordRow, error) {
	var rows []projectRecordRow
	err := t.db.Where("project_id = ? AND kind = ?", p, k).Order("id").Find(&rows).Error
	return rows, err
}
func recordProjectAuditEvent(tx Transaction, actor Actor, p, action, target, reason string) error {
	audit, ok := tx.(auditTransaction)
	if !ok {
		return ErrInvalidState
	}
	return audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: p, ActorID: actor.UserID, UserID: actor.UserID, TenantID: actor.TenantID, Action: action, Target: target, Reason: reason, Decision: "allow", CreatedAt: time.Now().UTC()})
}

// CaptureTenantSuspension is invoked by the tenant membership change adapter.
// It never grants access; recovery requires ChangeMemberState(restore_tenant).
func (s *Service) CaptureTenantSuspension(ctx context.Context, tenantID uint64, userID string) error {
	return s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		projects, err := tx.Projects(Actor{TenantID: tenantID, UserID: userID}, 10000)
		if err != nil {
			return err
		}
		for _, p := range projects {
			m, _ := projectMember(p, userID)
			if governanceRole(m) == "owner" || m.Status != "" && m.Status != "active" {
				continue
			}
			next := p
			next.ProjectVersion++
			next.Members = slices.Clone(p.Members)
			for i := range next.Members {
				if next.Members[i].UserID == userID {
					next.Members[i].Status = "suspended_by_tenant"
				}
			}
			if err := tx.UpdateProject(tenantID, p, next); err != nil {
				return err
			}
			if err := tx.ReplaceMembers(p.ID, next.Members); err != nil {
				return err
			}
			if err := recordProjectAuditEvent(tx, Actor{TenantID: tenantID, UserID: userID}, p.ID, "member.tenant_suspended", userID, "tenant_membership_changed"); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Service) RecordActivationRead(ctx context.Context, actor Actor, pid string, version int64) (ActivationReadReceipt, error) {
	if err := RequireHumanDecision(ctx); err != nil {
		return ActivationReadReceipt{}, err
	}
	var result ActivationReadReceipt
	err := s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, pid, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		if !projectOwner(p, actor.UserID) || p.Status != "draft" {
			return ErrInvalidState
		}
		if p.ProjectVersion != version {
			return ErrVersionConflict
		}
		store, ok := tx.(projectRecordTransaction)
		if !ok {
			return ErrInvalidState
		}
		result = ActivationReadReceipt{ID: uuid.NewString(), ActorID: actor.UserID, ProjectID: pid, ProjectVersion: p.ProjectVersion, SpecRevision: p.SpecRevision, ReadAt: time.Now().UTC()}
		if err := store.PutProjectRecord(pid, "activation_read", result.ID, 0, result); err != nil {
			return err
		}
		return recordProjectAuditEvent(tx, actor, pid, "project.activation_diff_read", result.ID, "")
	})
	return result, err
}
func validateActivationRead(tx Transaction, actor Actor, p Project, id string) error {
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return ErrInvalidState
	}
	var receipt ActivationReadReceipt
	_, err := store.GetProjectRecord(p.ID, "activation_read", id, &receipt)
	if err != nil {
		return err
	}
	if receipt.ActorID != actor.UserID || receipt.ProjectVersion != p.ProjectVersion || receipt.SpecRevision != p.SpecRevision {
		return ErrVersionConflict
	}
	return nil
}

var _ = errors.Is
