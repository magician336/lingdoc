package workspace

import (
	"context"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
)

// DeliveryExportService 是 T14 被装配起来之后的那一份：从 T13 的快照渲染一份真实
// DOCX、校验它、再按**此刻**的授权把字节交出去。
//
// 它与 DeliveryReleaseService 共用同一个快照库——各自建一个的话，刚冻下的快照在
// 导出时取不到，而那看起来会像是「快照不存在」而不是「装配错了」。
type DeliveryExportService struct {
	snapshots delivery.SnapshotStore
	exports   delivery.ExportStore
	document  DeliveryDocument
	inputs    *candidateadoption.DeliveryInputService
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
) *DeliveryExportService {
	if snapshots == nil || exports == nil || inputs == nil || inputs.Reader == nil || inputs.Authorizer == nil {
		return nil
	}
	if _, ok := exports.(delivery.ExportRecorder); !ok {
		return nil
	}
	return &DeliveryExportService{snapshots: snapshots, exports: exports, document: document, inputs: inputs}
}

// Start 渲染并校验一份导出，返回产物，以及它是不是这次动作的重放。
//
// key 是这次用户动作的幂等键。重试是**新的动作**（契约 §6），所以重试要换新键；
// 同一个键永远换回同一个结果，包括失败。
func (s *DeliveryExportService) Start(ctx context.Context, actorID, projectID, snapshotID, key string) (delivery.ExportArtifact, bool, error) {
	if s == nil {
		return delivery.ExportArtifact{}, false, candidateadoption.ErrInvalidState
	}
	return s.service(ctx).Start(actorID, projectID, snapshotID, key)
}

// Get 读一份产物的状态。它不返回字节——下载要单独走一次，因为**下载那一刻**的
// 授权必须重查一次。
func (s *DeliveryExportService) Get(ctx context.Context, actorID, projectID, exportID string) (delivery.ExportArtifact, error) {
	if s == nil {
		return delivery.ExportArtifact{}, candidateadoption.ErrInvalidState
	}
	return s.service(ctx).Get(actorID, projectID, exportID)
}

// Download 交出一份已校验文件的字节与它自己的产物记录。
//
// 每一次都重判一次授权，而不是复用创建时的那次判定：来源撤权之后，一枚旧的
// export_id 不能变成后门（契约 §7「下载再次检查权限」）。
func (s *DeliveryExportService) Download(ctx context.Context, actorID, projectID, exportID string) (delivery.ExportArtifact, []byte, error) {
	if s == nil {
		return delivery.ExportArtifact{}, nil, candidateadoption.ErrInvalidState
	}
	return s.service(ctx).Download(actorID, projectID, exportID)
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
func (s *DeliveryExportService) service(ctx context.Context) *delivery.ExportService {
	return delivery.NewExportService(
		s.snapshots,
		s.exports,
		s.document,
		s.document,
		delivery.CurrentnessFunc(func(frozen delivery.DeliveryInput) (bool, error) {
			return deliveryCurrentness(ctx, s.inputs.Reader, frozen)
		}),
		delivery.ExportAccessFunc(func(actorUserID, projectID string) error {
			return s.inputs.Authorizer.AuthorizeProject(ctx, actorUserID, projectID)
		}),
	)
}
