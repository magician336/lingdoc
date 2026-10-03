package workspace

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/types"
)

// DeliveryExportService 是 T14 被装配起来之后的那一份：从 T13 的快照渲染一份真实
// DOCX、校验它、再按**此刻**的授权把字节交出去。
//
// 它与 DeliveryReleaseService 共用同一个快照库——各自建一个的话，刚冻下的快照在
// 导出时取不到，而那看起来会像是「快照不存在」而不是「装配错了」。
type DeliveryExportService struct {
	snapshots delivery.SnapshotStore
	exports   delivery.ExportStore
	renderer  delivery.FrozenRenderer
	validator delivery.FrozenValidator
	inputs    *candidateadoption.DeliveryInputService
	sources   candidateadoption.SourcePolicy
	audit     AuditSink
}

// NewDeliveryExportService 依赖不齐时返回 nil，与同一包里的
// NewDeliveryReleaseService 同一条：宁可让调用方拿到 nil 并在装配时报错，
// 也不要交出一台会在调用时空转的服务器。
//
// 产物库还必须能记「哪一次动作产出了它」（delivery.ExportRecorder）。理由与
// FreezeRecorder 逐字相同：契约把 Idempotency-Key 标成 startExport 的必填头，
// 退化成「照渲不误」就等于收下了这个头却不当回事。
func NewDeliveryExportService(
	snapshots delivery.SnapshotStore,
	exports delivery.ExportStore,
	document DeliveryDocument,
	inputs *candidateadoption.DeliveryInputService,
	sources candidateadoption.SourcePolicy,
	audits ...AuditSink,
) *DeliveryExportService {
	return NewDeliveryExportServiceWithPorts(snapshots, exports, document, document, inputs, sources, audits...)
}

// NewDeliveryExportServiceWithPorts keeps the export application independent
// of the concrete file format. Production uses DeliveryDocument as the DOCX
// default, while tests or another delivery adapter can replace both ports.
func NewDeliveryExportServiceWithPorts(
	snapshots delivery.SnapshotStore,
	exports delivery.ExportStore,
	renderer delivery.FrozenRenderer,
	validator delivery.FrozenValidator,
	inputs *candidateadoption.DeliveryInputService,
	sources candidateadoption.SourcePolicy,
	audits ...AuditSink,
) *DeliveryExportService {
	if snapshots == nil || exports == nil || inputs == nil || inputs.Reader == nil || inputs.Authorizer == nil || sources == nil {
		return nil
	}
	if renderer == nil || validator == nil {
		return nil
	}
	if _, ok := exports.(delivery.ExportRecorder); !ok {
		return nil
	}
	var audit AuditSink
	if len(audits) > 0 {
		audit = audits[0]
	}
	return &DeliveryExportService{snapshots: snapshots, exports: exports, renderer: renderer, validator: validator, inputs: inputs, sources: sources, audit: audit}
}

// Start 渲染并校验一份导出，返回产物，以及它是不是这次动作的重放。
//
// key 是这次用户动作的幂等键。重试是**新的动作**（契约 §6），所以重试要换新键；
// 同一个键永远换回同一个结果，包括失败。
func (s *DeliveryExportService) Start(ctx context.Context, actorID, projectID, snapshotID, key string) (delivery.ExportArtifact, bool, error) {
	if s == nil {
		return delivery.ExportArtifact{}, false, candidateadoption.ErrInvalidState
	}
	if err := s.authorizeSnapshotSources(ctx, actorID, projectID, snapshotID); err != nil {
		s.recordDeliveryAudit(ctx, actorID, projectID, "delivery.export", err)
		return delivery.ExportArtifact{}, false, err
	}
	return s.service(ctx, actorID, projectID).StartAs(ctx, s.deliveryActor(ctx, actorID), projectID, snapshotID, key)
}

// Get 读一份产物的状态。它不返回字节——下载要单独走一次，因为**下载那一刻**的
// 授权必须重查一次。
func (s *DeliveryExportService) Get(ctx context.Context, actorID, projectID, exportID string) (delivery.ExportArtifact, error) {
	if s == nil {
		return delivery.ExportArtifact{}, candidateadoption.ErrInvalidState
	}
	return s.service(ctx, actorID, projectID).GetAs(ctx, s.deliveryActor(ctx, actorID), projectID, exportID)
}

// Download 交出一份已校验文件的字节与它自己的产物记录。
//
// 每一次都重判一次授权，而不是复用创建时的那次判定：来源撤权之后，一枚旧的
// export_id 不能变成后门（契约 §7「下载再次检查权限」）。
func (s *DeliveryExportService) Download(ctx context.Context, actorID, projectID, exportID string) (delivery.ExportArtifact, []byte, error) {
	if s == nil {
		return delivery.ExportArtifact{}, nil, candidateadoption.ErrInvalidState
	}
	if err := s.inputs.Authorizer.AuthorizeProject(ctx, actorID, projectID); err != nil {
		s.recordDeliveryAudit(ctx, actorID, projectID, "delivery.download", err)
		return delivery.ExportArtifact{}, nil, err
	}
	artifact, err := s.exports.GetExport(projectID, exportID)
	if err != nil {
		return delivery.ExportArtifact{}, nil, err
	}
	if err := s.authorizeSnapshotSources(ctx, actorID, projectID, artifact.SnapshotID); err != nil {
		s.recordDeliveryAudit(ctx, actorID, projectID, "delivery.download", err)
		return delivery.ExportArtifact{}, nil, err
	}
	return s.service(ctx, actorID, projectID).DownloadAs(ctx, s.deliveryActor(ctx, actorID), projectID, exportID)
}

func (s *DeliveryExportService) deliveryActor(ctx context.Context, actorID string) delivery.Actor {
	caller := types.CallerFromContext(ctx)
	if caller.UserID == "" {
		caller.UserID = actorID
	}
	return delivery.Actor{TenantID: caller.TenantID, UserID: caller.UserID, Role: caller.Role}
}

func (s *DeliveryExportService) recordDeliveryAudit(ctx context.Context, actorID, projectID, capability string, err error) {
	if s.audit == nil {
		return
	}
	caller := types.CallerFromContext(ctx)
	if caller.UserID == "" {
		caller.UserID = actorID
	}
	decision, reason := "allow", ""
	if err != nil {
		decision, reason = "deny", err.Error()
	}
	_ = s.audit.Record(ctx, AuditEvent{TenantID: caller.TenantID, UserID: caller.UserID, Role: caller.Role, ProjectID: projectID, Capability: capability, Decision: decision, Reason: reason})
}

func (s *DeliveryExportService) authorizeSnapshotSources(ctx context.Context, actorID, projectID, snapshotID string) error {
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(projectID) == "" || strings.TrimSpace(snapshotID) == "" {
		return candidateadoption.ErrInvalidRequest
	}
	if err := s.inputs.Authorizer.AuthorizeProject(ctx, actorID, projectID); err != nil {
		return err
	}
	snapshot, err := s.snapshots.Get(projectID, snapshotID)
	if err != nil {
		return err
	}
	if snapshot.Check.Status != delivery.CheckPassed {
		// Keep the established preflight_blocked response for snapshots that were
		// never eligible for export; no file can be rendered from these inputs.
		return nil
	}
	ids := make([]string, 0)
	for _, chapter := range snapshot.FrozenInput.Chapters {
		ids = append(ids, chapter.SourceIDs...)
	}
	ids = normalizeSourceIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	return s.sources.Validate(ctx, projectID, actorID, ids)
}

// List 列出项目导出过的产物，新的在前。
//
// 与 DeliveryReleaseService.List 分开而不是合成一个「交付历史」：两者读的是两个库，
// 各自的授权口径与截断都要能单独演进。界面把它们按 snapshot_id 拼起来是界面的事，
// 服务端不为了少一次请求而在这一层做一次跨库连接。
//
// 产物本身没有「当前性」——当前与否是**快照**的属性（同一个快照的几个产物共享同一个
// 结论）。所以这里不做重算，界面拿到 ExportArtifact.snapshot_id 之后按契约 §7 去读
// getRelease。
func (s *DeliveryExportService) List(ctx context.Context, actorID, projectID string) ([]delivery.ExportArtifact, bool, error) {
	if s == nil || s.inputs == nil || s.exports == nil {
		return nil, false, candidateadoption.ErrInvalidState
	}
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(projectID) == "" {
		return nil, false, candidateadoption.ErrInvalidRequest
	}
	// 先判成员再列，与同一包里三处读操作同一条：非成员不该从「空列表」与
	// 「没权限」的差别里问出某个项目导出过什么。
	if err := s.inputs.Authorizer.AuthorizeProject(ctx, actorID, projectID); err != nil {
		return nil, false, err
	}
	lister, ok := s.exports.(delivery.ExportLister)
	if !ok {
		return nil, false, candidateadoption.ErrInvalidState
	}
	artifacts, err := lister.ListExports(projectID)
	if err != nil {
		return nil, false, err
	}
	if len(artifacts) > deliveryHistoryLimit {
		return artifacts[:deliveryHistoryLimit], true, nil
	}
	return artifacts, false, nil
}

// service 现建一台绑定本次请求上下文的 ExportService。
//
// 与 DeliveryReleaseService.releases 同一条：delivery 的 CurrentnessChecker 与
// ExportAccessChecker 签名里都没有 ctx，而这两件事都必须带着身份去读工作区
// （T12 的读取侧要在调用者的调用者上下文里跑）。所以上下文在构造时钉住，
// 这一台只服务这一次调用，不跨请求复用。
//
// 两个判定都接回 T12 那一份：授权是 T07 交给它的成员判定，当前性是**与 T13 同一个**
// deliveryCurrentness。导出侧不另立一套口径。
func (s *DeliveryExportService) service(ctx context.Context, actorID, projectID string) *delivery.ExportService {
	return delivery.NewExportService(
		s.snapshots,
		s.exports,
		s.renderer,
		s.validator,
		delivery.CurrentnessFunc(func(frozen delivery.DeliveryInput) (bool, error) {
			current, err := deliveryCurrentness(ctx, s.inputs.Reader, frozen)
			if err != nil || !current {
				return current, err
			}
			ids := make([]string, 0, len(frozen.Sources))
			for _, source := range frozen.Sources {
				ids = append(ids, source.ID)
			}
			if len(ids) == 0 {
				return true, nil
			}
			if err := s.sources.Validate(ctx, projectID, actorID, ids); err != nil {
				if errors.Is(err, candidateadoption.ErrSourceAccessDenied) || errors.Is(err, candidateadoption.ErrStaleInput) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		}),
		delivery.ExportAccessFunc(func(actorUserID, projectID string) error {
			return s.inputs.Authorizer.AuthorizeProject(ctx, actorUserID, projectID)
		}),
		s.audit,
	)
}
