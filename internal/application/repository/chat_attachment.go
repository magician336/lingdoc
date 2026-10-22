package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type chatAttachmentRepository struct{ db *gorm.DB }

func NewChatAttachmentRepository(db *gorm.DB) interfaces.ChatAttachmentRepository {
	return &chatAttachmentRepository{db: db}
}

func (r *chatAttachmentRepository) Create(ctx context.Context, document *types.ChatAttachment) error {
	return r.db.WithContext(ctx).Create(document).Error
}

func (r *chatAttachmentRepository) GetByID(ctx context.Context, tenantID uint64, documentID string) (*types.ChatAttachment, error) {
	var document types.ChatAttachment
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, documentID).First(&document).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &document, err
}

func (r *chatAttachmentRepository) GetScoped(ctx context.Context, tenantID uint64, sessionID, documentID string) (*types.ChatAttachment, error) {
	var document types.ChatAttachment
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND session_id = ? AND id = ?", tenantID, sessionID, documentID).First(&document).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &document, err
}

func (r *chatAttachmentRepository) ListScoped(ctx context.Context, tenantID uint64, sessionID string) ([]*types.ChatAttachment, error) {
	var documents []*types.ChatAttachment
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND session_id = ?", tenantID, sessionID).Order("created_at ASC").Find(&documents).Error
	return documents, err
}

func (r *chatAttachmentRepository) MarkProcessing(ctx context.Context, tenantID uint64, documentID string, startedAt time.Time) error {
	return r.db.WithContext(ctx).Model(&types.ChatAttachment{}).
		Where("tenant_id = ? AND id = ?", tenantID, documentID).
		Updates(map[string]interface{}{"status": types.ChatAttachmentStatusProcessing, "started_at": startedAt, "error_message": ""}).Error
}

func (r *chatAttachmentRepository) MarkReady(ctx context.Context, tenantID uint64, documentID, content string, chunks, imageRefs, metadata types.JSON, tokenCount, chunkCount int, readyAt time.Time) error {
	return r.db.WithContext(ctx).Model(&types.ChatAttachment{}).
		Where("tenant_id = ? AND id = ?", tenantID, documentID).
		Updates(map[string]interface{}{
			"status": types.ChatAttachmentStatusReady, "content": content, "chunks": chunks,
			"image_refs": imageRefs, "metadata": metadata, "token_count": tokenCount,
			"chunk_count": chunkCount, "ready_at": readyAt, "error_message": "",
		}).Error
}

func (r *chatAttachmentRepository) MarkFailed(ctx context.Context, tenantID uint64, documentID, message string) error {
	return r.db.WithContext(ctx).Model(&types.ChatAttachment{}).
		Where("tenant_id = ? AND id = ?", tenantID, documentID).
		Updates(map[string]interface{}{"status": types.ChatAttachmentStatusFailed, "error_message": message}).Error
}

func (r *chatAttachmentRepository) DeleteScoped(ctx context.Context, tenantID uint64, sessionID, documentID string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND session_id = ? AND id = ?", tenantID, sessionID, documentID).Delete(&types.ChatAttachment{}).Error
}

func (r *chatAttachmentRepository) ListExpired(ctx context.Context, before time.Time, limit int) ([]*types.ChatAttachment, error) {
	var documents []*types.ChatAttachment
	err := r.db.WithContext(ctx).Where("expires_at <= ?", before).Order("expires_at ASC").Limit(limit).Find(&documents).Error
	return documents, err
}
