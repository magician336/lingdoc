package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrAssetNotFound 表示「这个项目里没有这份资料」。
//
// 与 DenyNotFound 是同一件事的两个层次：这里给内部调用方，那里给消费者。
// 别的项目的资料 ID 与不存在的 ID 都收敛到这里，不泄露存在性。
var ErrAssetNotFound = errors.New("asset not found")

// ErrInvalidBinding 表示绑定入参不成立（缺项目或资料 ID）。
var ErrInvalidBinding = errors.New("invalid binding input")

var ErrIdempotencyConflict = errors.New("idempotency conflict")

// ErrObservationConflict 表示这次观测读到的资产版本已经被别的观测提交。
// 调用方可以安全地重读并重试；它不是内容冲突，也不是资料不存在。
var ErrObservationConflict = errors.New("asset observation conflict")

// 修订行的状态。旧修订不删除、只标 superseded：冻结与确认要能回指「当时那一版」。
const (
	RevisionActive     = "active"
	RevisionSuperseded = "superseded"
)

// OriginWeKnora 是 T09 唯一会写的来源：`Bind` 绑的是底座的一份知识。
// 设计表还留了 academic/conversation/upload，由首次写它们的那张任务补上。
const OriginWeKnora = "weknora"

// ProjectAsset 对应设计表 `lingdoc_project_assets`（04-数据库设计与开发方案.md §4.2）。
//
// 它是资料域自己的行，不复制 WeKnora 原文件；`Asset`（契约对外形状）由本行映射而来，
// 两者刻意分开：表结构会演进，契约字段名不能跟着动。
//
// 与迁移的对应关系是逐列的（`migrations/versioned/000098_lingdoc_evidence_assets.up.sql`）：
// GORM 的列名/类型/默认值改了这里就必须改那里，否则线上建出来的表与代码里的查询对不上。
type ProjectAsset struct {
	ID              string `gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64 `gorm:"not null;index:idx_lingdoc_project_assets_scope,priority:1"`
	ProjectID       string `gorm:"type:varchar(36);not null;uniqueIndex:idx_lingdoc_project_assets_binding,priority:1;index:idx_lingdoc_project_assets_scope,priority:2"`
	KnowledgeID     string `gorm:"type:varchar(36);not null;uniqueIndex:idx_lingdoc_project_assets_binding,priority:2"`
	Origin          string `gorm:"type:varchar(32);not null;default:'weknora'"`
	KnowledgeBaseID string `gorm:"type:varchar(36);not null;default:''"`
	// Title 是绑定时从底座取的快照：契约把 title 标成必填，而底座改名不会产生
	// 任何事件，所以这里只能如实是「绑定时叫什么」。
	Title string `gorm:"type:varchar(255);not null;default:''"`
	// AssetRevision 指向 lingdoc_asset_revisions 里当前有效的那一版。
	AssetRevision   int64  `gorm:"not null;default:1"`
	ProcessingState string `gorm:"type:varchar(32);not null;default:'pending'"`
	CreatedBy       string `gorm:"type:varchar(36);not null;default:''"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// 没有 `index`：本域的查询都走 project_id / id，没有一个按 deleted_at 过滤，
	// 建一条用不上的索引只会让模型与迁移多一处可以漂移的地方（迁移里也没有它）。
	DeletedAt gorm.DeletedAt
}

// TableName 用设计文档里的表名，不用 GORM 的默认复数规则。
func (ProjectAsset) TableName() string { return "lingdoc_project_assets" }

// ProjectAssetRevision 记录某一版资料**当时观测到的内容指纹**。
//
// 指纹存在这里而不是资产表的列上：修订行本身就是「当时那一版是什么」的记录，
// 当前指纹 = 当前修订行的 ContentHash，不需要第二个可漂移的副本。
type ProjectAssetRevision struct {
	ID         string `gorm:"type:varchar(36);primaryKey"`
	AssetID    string `gorm:"type:varchar(36);not null;uniqueIndex:idx_lingdoc_asset_revisions_no,priority:1"`
	RevisionNo int64  `gorm:"not null;uniqueIndex:idx_lingdoc_asset_revisions_no,priority:2"`
	// 列名沿用设计表的 weknora_knowledge_id：重解析后这里会指向新的知识 ID，
	// 所以它记的是「这一版对应底座的哪一份」，不是资料的稳定身份。
	KnowledgeID string `gorm:"column:weknora_knowledge_id;type:varchar(36);not null;default:''"`
	// ContentHash 存 FingerprintOf 的摘要。底座的 FileHash 若可得，它是主要成分，
	// 所以这里叫 content_hash 并不勉强；依据记在 Metadata 里，供人工核对。
	ContentHash string `gorm:"type:varchar(64);not null;default:''"`
	Status      string `gorm:"type:varchar(32);not null;default:'active'"`
	// Metadata 是解析摘要（此处为指纹依据），字段名对齐设计文档的 metadata_json。
	Metadata  string `gorm:"column:metadata_json;type:text;not null;default:''"`
	CreatedAt time.Time
}

// TableName 用设计文档里的表名。
func (ProjectAssetRevision) TableName() string { return "lingdoc_asset_revisions" }

// BindInput 是一次绑定的输入。Signal 是绑定当刻对底座的观测，用于建立基线版本。
type BindInput struct {
	TenantID        uint64
	ProjectID       string
	KnowledgeID     string
	KnowledgeBaseID string
	Title           string
	CreatedBy       string
	Signal          KnowledgeSignal
}

// Bindings 是资料域的绑定存储（单体内直连 GORM，沿用仓库现有仓储形态）。
//
// 它同时满足 T03 定义的 BindingSource，所以 AssetGateway 可以直接用它。
type Bindings struct {
	db *gorm.DB
}

type operationRow struct {
	TenantID     uint64 `gorm:"primaryKey"`
	UserID       string `gorm:"primaryKey;size:64"`
	Operation    string `gorm:"primaryKey;size:40"`
	Target       string `gorm:"primaryKey;size:100"`
	Key          string `gorm:"primaryKey;size:128"`
	BodyHash     string `gorm:"not null;size:64"`
	ResponseJSON string `gorm:"column:response_json;not null;type:text"`
	ResponseCode int    `gorm:"not null"`
	CreatedAt    time.Time
}

func (operationRow) TableName() string { return "lingdoc_operations" }

// NewBindings 组装绑定存储。
func NewBindings(db *gorm.DB) *Bindings { return &Bindings{db: db} }

// BoundAssets 实现 BindingSource：返回该项目当前绑定的全部资料。
func (b *Bindings) BoundAssets(ctx context.Context, projectID string) ([]Asset, error) {
	var rows []ProjectAsset
	if err := b.db.WithContext(ctx).
		Where("project_id = ?", projectID).
		Order("created_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Asset, 0, len(rows))
	for _, row := range rows {
		asset, err := b.assetWithCurrentRevision(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, asset)
	}
	return out, nil
}

// AssetForKnowledge resolves the current project asset revision that owns a
// knowledge row. Source IDs are chunk IDs, so this joins through the revision
// table rather than assuming the original bound knowledge is still current.
func (b *Bindings) AssetForKnowledge(ctx context.Context, projectID, knowledgeID string) (Asset, error) {
	var row ProjectAsset
	err := b.db.WithContext(ctx).
		Joins("JOIN lingdoc_asset_revisions AS r ON r.asset_id = lingdoc_project_assets.id").
		Where("lingdoc_project_assets.project_id = ? AND r.weknora_knowledge_id = ?", projectID, knowledgeID).
		Order("r.revision_no DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Asset{}, ErrAssetNotFound
	}
	if err != nil {
		return Asset{}, err
	}
	return b.assetWithCurrentRevision(ctx, row)
}

// Bind 绑定一份资料。同一（项目, 资料）重复绑定返回既有的那一条，不产生新版本——
// 绑定的幂等由唯一索引兜底，并发下第二个写会撞约束，此时回读既有行。
func (b *Bindings) Bind(ctx context.Context, in BindInput) (Asset, error) {
	if in.ProjectID == "" || in.KnowledgeID == "" {
		return Asset{}, ErrInvalidBinding
	}

	existing, err := b.findBinding(ctx, in.ProjectID, in.KnowledgeID)
	switch {
	case err == nil:
		return existing.toAsset(), nil
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return Asset{}, err
	}

	fp := FingerprintOf(in.Signal)
	row := ProjectAsset{
		ID:              uuid.New().String(),
		TenantID:        in.TenantID,
		ProjectID:       in.ProjectID,
		KnowledgeID:     in.KnowledgeID,
		KnowledgeBaseID: in.KnowledgeBaseID,
		Origin:          OriginWeKnora,
		Title:           in.Title,
		AssetRevision:   1,
		ProcessingState: string(ProcessingStateOf(in.Signal.ParseStatus)),
		CreatedBy:       in.CreatedBy,
	}

	err = b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return tx.Create(&ProjectAssetRevision{
			ID:          uuid.New().String(),
			AssetID:     row.ID,
			RevisionNo:  1,
			KnowledgeID: row.KnowledgeID,
			ContentHash: fp.Digest,
			Status:      RevisionActive,
			Metadata:    metadataFor(fp),
		}).Error
	})
	if err != nil {
		// 并发重复绑定：唯一索引挡住了第二个写，回读先到的那一条。
		if again, findErr := b.findBinding(ctx, in.ProjectID, in.KnowledgeID); findErr == nil {
			return again.toAsset(), nil
		}
		return Asset{}, err
	}
	return row.toAsset(), nil
}

func (b *Bindings) bindTx(tx *gorm.DB, in BindInput) (Asset, error) {
	if in.ProjectID == "" || in.KnowledgeID == "" {
		return Asset{}, ErrInvalidBinding
	}
	var existing ProjectAsset
	if err := tx.Where("project_id = ? AND knowledge_id = ?", in.ProjectID, in.KnowledgeID).First(&existing).Error; err == nil {
		return existing.toAsset(), nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Asset{}, err
	}
	fp := FingerprintOf(in.Signal)
	row := ProjectAsset{ID: uuid.New().String(), TenantID: in.TenantID, ProjectID: in.ProjectID,
		KnowledgeID: in.KnowledgeID, KnowledgeBaseID: in.KnowledgeBaseID, Origin: OriginWeKnora,
		Title: in.Title, AssetRevision: 1, ProcessingState: string(ProcessingStateOf(in.Signal.ParseStatus)), CreatedBy: in.CreatedBy}
	if err := tx.Create(&row).Error; err != nil {
		return Asset{}, err
	}
	if err := tx.Create(&ProjectAssetRevision{ID: uuid.New().String(), AssetID: row.ID, RevisionNo: 1,
		KnowledgeID: row.KnowledgeID, ContentHash: fp.Digest, Status: RevisionActive, Metadata: metadataFor(fp)}).Error; err != nil {
		return Asset{}, err
	}
	return row.toAsset(), nil
}

// BindIdempotent persists the exact bind response in the workspace operation
// table. The write and the operation record share one transaction, so a retry
// after a response loss cannot create a second binding or a different result.
func (b *Bindings) BindIdempotent(ctx context.Context, tenantID uint64, userID, target, key string, in BindInput) (Asset, bool, error) {
	if len(key) < 8 || len(key) > 128 || userID == "" {
		return Asset{}, false, ErrInvalidBinding
	}
	body, err := json.Marshal(in)
	if err != nil {
		return Asset{}, false, err
	}
	sum := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(sum[:])
	var result Asset
	var replay bool
	err = b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var previous operationRow
		err := tx.Where("tenant_id = ? AND user_id = ? AND operation = ? AND target = ? AND key = ?", tenantID, userID, "bindAsset", target, key).First(&previous).Error
		if err == nil {
			if previous.BodyHash != bodyHash {
				return ErrIdempotencyConflict
			}
			if err := json.Unmarshal([]byte(previous.ResponseJSON), &result); err != nil {
				return err
			}
			replay = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		result, err = b.bindTx(tx, in)
		if err != nil {
			return err
		}
		response, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Create(&operationRow{TenantID: tenantID, UserID: userID, Operation: "bindAsset", Target: target, Key: key,
			BodyHash: bodyHash, ResponseJSON: string(response), ResponseCode: 201}).Error
	})
	return result, replay, err
}

// ObserveAsset 用一次新观测更新绑定：指纹变了就递增版本并新建修订行，
// 旧修订标 superseded；只有状态变了就只更新 processing_state。
//
// 判据在 Observe（见 revision.go），这里只负责把它落库。
func (b *Bindings) ObserveAsset(
	ctx context.Context, projectID, assetID string, sig KnowledgeSignal,
) (ObserveResult, error) {
	// 资产行是版本闸门。每次重试都会在同一事务里重新读取资产和当前修订，
	// 通过 revision/state 的 CAS 更新抢占写入权；因此并发观测不会互相覆盖同一
	// revision_no 的 hash 或 KnowledgeID，也不会让 current revision 倒退。
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var result ObserveResult
		err := b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var row ProjectAsset
			if err := tx.Where("project_id = ? AND id = ?", projectID, assetID).First(&row).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAssetNotFound
				}
				return err
			}

			// 当前指纹取自当前修订行。行缺失（历史数据/被手工清过）时按「未观测过」处理，
			// 本事务会补建基线。
			current, err := currentRevisionTx(tx, row.ID, row.AssetRevision)
			if err != nil {
				return err
			}
			res := Observe(BindingState{
				Revision:    row.AssetRevision,
				Fingerprint: current.ContentHash,
			}, sig)
			needsRevision := res.Next.Fingerprint != current.ContentHash
			stateUnchanged := res.State == AssetState(row.ProcessingState)
			if !needsRevision && stateUnchanged {
				result = res
				return nil
			}

			// 重解析后底座会给出新的知识 ID，那一版就该指向新的那一份；
			// 拿不到新 ID 时沿用绑定时的值。
			knowledgeID := sig.KnowledgeID
			if knowledgeID == "" {
				knowledgeID = row.KnowledgeID
			}

			update := tx.Model(&ProjectAsset{}).
				Where("id = ? AND asset_revision = ? AND processing_state = ?",
					row.ID, row.AssetRevision, row.ProcessingState).
				Updates(map[string]any{
					"asset_revision":   res.Next.Revision,
					"processing_state": string(res.State),
				})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrObservationConflict
			}

			if needsRevision {
				// 旧修订只做不可变历史；不再 upsert 同一 revision_no。
				// 若并发事务已经写入该版本，唯一键错误会回滚本次 CAS，下一次重试
				// 会重新读取并沿用已经提交的修订。
				if err := tx.Model(&ProjectAssetRevision{}).
					Where("asset_id = ? AND status = ? AND revision_no <> ?",
						row.ID, RevisionActive, res.Next.Revision).
					Update("status", RevisionSuperseded).Error; err != nil {
					return err
				}
				revision := ProjectAssetRevision{
					ID:          uuid.New().String(),
					AssetID:     row.ID,
					RevisionNo:  res.Next.Revision,
					KnowledgeID: knowledgeID,
					ContentHash: res.Next.Fingerprint,
					Status:      RevisionActive,
					Metadata:    metadataFor(res.Fingerprint),
				}
				if err := tx.Create(&revision).Error; err != nil {
					return err
				}
			}
			result = res
			return nil
		})
		if !errors.Is(err, ErrObservationConflict) && !isObservationUniqueConflict(err) {
			return result, err
		}
		if err := ctx.Err(); err != nil {
			return ObserveResult{}, err
		}
	}
	return ObserveResult{}, ErrObservationConflict
}

// isObservationUniqueConflict turns a duplicate immutable revision into a retry.
// This is the one expected race that cannot be represented by the asset-row CAS:
// a missing baseline has the same revision/state values in both readers.
func isObservationUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "unique failed")
}

// AssetScope 实现 ScopeReader：判权需要知识库与租户，而契约的 Asset 上没有。
//
// 与 ObserveAsset 一样按 projectID 收敛：项目不对就是 ErrAssetNotFound，
// 与「不存在」不可分辨，所以这个存储不能拿来试探别的项目有什么资料。
func (b *Bindings) AssetScope(ctx context.Context, projectID, assetID string) (AssetScopeRef, error) {
	var row ProjectAsset
	if err := b.db.WithContext(ctx).
		Select("knowledge_base_id", "tenant_id").
		Where("project_id = ? AND id = ?", projectID, assetID).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AssetScopeRef{}, ErrAssetNotFound
		}
		return AssetScopeRef{}, err
	}
	return AssetScopeRef{KnowledgeBaseID: row.KnowledgeBaseID, OwnerTenantID: row.TenantID}, nil
}

// Revisions 返回一份资料的全部修订，按版本号升序。冻结与交付要回指「当时那一版」。
func (b *Bindings) Revisions(ctx context.Context, assetID string) ([]ProjectAssetRevision, error) {
	var rows []ProjectAssetRevision
	if err := b.db.WithContext(ctx).
		Where("asset_id = ?", assetID).
		Order("revision_no ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// KnowledgeOfRevision 实现 RevisionReader：某一版资料当时对应底座的哪一份知识。
//
// 判据取修订行而不是资料行：重解析后资料行上的 `knowledge_id` 仍是**绑定时**那一份
// （它是绑定的身份，`findBinding` 按它查），新知识 ID 只记在修订行里。
// 行不存在或知识 ID 为空时返回 ok=false——历史数据（基线缺失）会走到这条路径，
// 由调用方按「判不定」处理，本方法不替它猜一个知识出来。
func (b *Bindings) KnowledgeOfRevision(
	ctx context.Context, assetID string, revision int,
) (knowledgeID string, ok bool, err error) {
	row, err := b.currentRevision(ctx, assetID, int64(revision))
	if err != nil {
		return "", false, err
	}
	if row.ID == "" || row.KnowledgeID == "" {
		return "", false, nil
	}
	return row.KnowledgeID, true, nil
}

func (b *Bindings) findBinding(ctx context.Context, projectID, knowledgeID string) (ProjectAsset, error) {
	var row ProjectAsset
	err := b.db.WithContext(ctx).
		Where("project_id = ? AND knowledge_id = ?", projectID, knowledgeID).
		First(&row).Error
	return row, err
}

func (b *Bindings) currentRevision(ctx context.Context, assetID string, revisionNo int64) (ProjectAssetRevision, error) {
	return currentRevisionTx(b.db.WithContext(ctx), assetID, revisionNo)
}

func currentRevisionTx(tx *gorm.DB, assetID string, revisionNo int64) (ProjectAssetRevision, error) {
	var row ProjectAssetRevision
	err := tx.
		Where("asset_id = ? AND revision_no = ?", assetID, revisionNo).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ProjectAssetRevision{}, nil
	}
	return row, err
}

// toAsset 把存储行映射成契约形状。只映射契约有的字段，别的留在库里。
func (a ProjectAsset) toAsset() Asset {
	return Asset{
		ID:              a.ID,
		ProjectID:       a.ProjectID,
		KnowledgeID:     a.KnowledgeID,
		Title:           a.Title,
		AssetRevision:   int(a.AssetRevision),
		ProcessingState: AssetState(a.ProcessingState),
	}
}

// assetWithCurrentRevision keeps the public asset identity aligned with the
// revision that search and source validation use. ProjectAsset.KnowledgeID is
// the original binding key; a reparse can move the live knowledge ID forward.
func (b *Bindings) assetWithCurrentRevision(ctx context.Context, row ProjectAsset) (Asset, error) {
	asset := row.toAsset()
	revision, err := b.currentRevision(ctx, row.ID, row.AssetRevision)
	if err != nil {
		return Asset{}, err
	}
	if revision.KnowledgeID != "" {
		asset.KnowledgeID = revision.KnowledgeID
	}
	return asset, nil
}

// metadataFor 记下这次指纹的依据，人工核对时不必反推。
func metadataFor(fp Fingerprint) string {
	raw, err := json.Marshal(struct {
		Basis string `json:"basis"`
	}{Basis: fp.Basis})
	if err != nil {
		// 依据只是审计信息，序列化失败不该挡住绑定本身。
		return ""
	}
	return string(raw)
}
