package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/evidence"
)

// SaveParsedOrigin is an optional local capability. Deployments that have not
// explicitly migrated the snapshot table keep their existing ingestion path.
func (r *knowledgeRepository) SaveParsedOrigin(ctx context.Context, snapshot evidence.ParsedOriginSnapshot) (bool, error) {
	return evidence.NewParsedOriginStore(r.db).Save(ctx, snapshot)
}

var _ evidence.ParsedOriginWriter = (*knowledgeRepository)(nil)
