package interfaces

import (
	"context"
	"io"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

type ChatAttachmentRepository interface {
	Create(ctx context.Context, document *types.ChatAttachment) error
	GetByID(ctx context.Context, tenantID uint64, documentID string) (*types.ChatAttachment, error)
	GetScoped(ctx context.Context, tenantID uint64, sessionID, documentID string) (*types.ChatAttachment, error)
	ListScoped(ctx context.Context, tenantID uint64, sessionID string) ([]*types.ChatAttachment, error)
	MarkProcessing(ctx context.Context, tenantID uint64, documentID string, startedAt time.Time) error
	MarkReady(ctx context.Context, tenantID uint64, documentID, content string, chunks, imageRefs, metadata types.JSON, tokenCount, chunkCount int, readyAt time.Time) error
	MarkFailed(ctx context.Context, tenantID uint64, documentID, message string) error
	DeleteScoped(ctx context.Context, tenantID uint64, sessionID, documentID string) error
	ListExpired(ctx context.Context, before time.Time, limit int) ([]*types.ChatAttachment, error)
}

type ChatAttachmentService interface {
	Create(ctx context.Context, tenantID uint64, sessionID, fileName, mimeType string, fileSize int64, reader io.Reader, options types.ChatAttachmentCreateOptions) (*types.ChatAttachment, error)
	Get(ctx context.Context, tenantID uint64, sessionID, documentID string) (*types.ChatAttachment, error)
	OpenFile(ctx context.Context, tenantID uint64, sessionID, documentID string) (io.ReadCloser, string, error)
	List(ctx context.Context, tenantID uint64, sessionID string) ([]*types.ChatAttachment, error)
	Delete(ctx context.Context, tenantID uint64, sessionID, documentID string) error
	ResolveForPrompt(ctx context.Context, tenantID uint64, sessionID string, documentIDs []string, query string) (*types.ChatAttachmentPromptResult, error)
	Process(ctx context.Context, task *asynq.Task) error
	CleanupExpired(ctx context.Context) error
}
