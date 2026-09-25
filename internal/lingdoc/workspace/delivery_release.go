package workspace

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
)

// DeliveryReleaseService 是 T13 在被装配起来之后的那一份：读 T12 的交付输入、
// 补成冻结输入、再交给 T13 的检查与冻结。
//
// 契约把 /checks、/releases、/exports 这些 HTTP 入口标成「容量有余才启用的独立入口，
// 核心流程不依赖本接口」，但同一句话里也写了「领域授权/检索/检查函数仍必须实现」。
// 这一层就是那些领域函数的组装点：没有它，T13 的检查与冻结在仓里没有一个生产调用者，
// 「T12→T13」也就只是两套类型相邻摆着。
type DeliveryReleaseService struct {
	inputs  *candidateadoption.DeliveryInputService
	builder *DeliveryInputBuilder
	store   delivery.SnapshotStore
}

// NewDeliveryReleaseService 依赖不齐时返回 nil。三个依赖各自都是必需的：
// 少一个，链路就有一截是空的（读不到、补不上、或者冻不下来）。
func NewDeliveryReleaseService(inputs *candidateadoption.DeliveryInputService, builder *DeliveryInputBuilder, store delivery.SnapshotStore) *DeliveryReleaseService {
	if inputs == nil || builder == nil || store == nil {
		return nil
	}
	return &DeliveryReleaseService{inputs: inputs, builder: builder, store: store}
}

// Check 回答「此刻冻结会得到什么结论」，不落快照。
//
// expectedProjectVersion 是调用方在说「我以为工作区长这样」——契约里 /checks 与
// /releases 的请求体都只有这一个字段。对不上就是别人先改了，这时要报冲突，
// 而不是把新的内容当成他以为的那一份检查下去：检查结论会贴在一个他没有看过的工作区上。
func (s *DeliveryReleaseService) Check(ctx context.Context, actorID, projectID string, expectedProjectVersion int64) (delivery.CheckResult, error) {
	input, err := s.assemble(ctx, actorID, projectID, expectedProjectVersion)
	if err != nil {
		return delivery.CheckResult{}, err
	}
	return delivery.Evaluate(input), nil
}

// Prepare 冻结一份快照并落存。
func (s *DeliveryReleaseService) Prepare(ctx context.Context, actorID, projectID string, expectedProjectVersion int64) (delivery.ReleaseSnapshot, error) {
	input, err := s.assemble(ctx, actorID, projectID, expectedProjectVersion)
	if err != nil {
		return delivery.ReleaseSnapshot{}, err
	}
	return s.releases(ctx).Prepare(input)
}

// Get 取一份已冻结的快照，并按**此刻**的工作区重算 is_current。
//
// 重算是有意为之：冻结的内容与摘要不再变（那正是冻结的意思），但「它还对不对得上
// 现在的工作区」是个随时间变化的判断，存下来的那个值从写下的一刻就在过期。
func (s *DeliveryReleaseService) Get(ctx context.Context, actorID, projectID, snapshotID string) (delivery.ReleaseSnapshot, error) {
	if s == nil || s.inputs == nil || s.store == nil {
		return delivery.ReleaseSnapshot{}, candidateadoption.ErrInvalidState
	}
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(projectID) == "" || strings.TrimSpace(snapshotID) == "" {
		return delivery.ReleaseSnapshot{}, candidateadoption.ErrInvalidRequest
	}
	// 先判成员再读快照：顺序反过来的话，非成员能从「找不到」与「没权限」的差别里
	// 问出某个项目有没有冻结过东西。
	if err := s.inputs.Authorizer.AuthorizeProject(ctx, actorID, projectID); err != nil {
		return delivery.ReleaseSnapshot{}, err
	}
	return s.releases(ctx).Get(projectID, snapshotID)
}

// assemble 是一次「读 T12 → 补 T13」：读的那一步已经在可重复读事务里取齐了
// 项目、研究条件、章节版本与确认，补的那一步只补 T12 手里没有的东西（冻结来源）。
func (s *DeliveryReleaseService) assemble(ctx context.Context, actorID, projectID string, expectedProjectVersion int64) (delivery.DeliveryInput, error) {
	if s == nil || s.inputs == nil || s.builder == nil {
		return delivery.DeliveryInput{}, candidateadoption.ErrInvalidState
	}
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(projectID) == "" {
		return delivery.DeliveryInput{}, candidateadoption.ErrInvalidRequest
	}
	input, err := s.inputs.Read(ctx, actorID, projectID)
	if err != nil {
		return delivery.DeliveryInput{}, err
	}
	if input.ProjectVersion != expectedProjectVersion {
		return delivery.DeliveryInput{}, candidateadoption.ErrVersionConflict
	}
	return s.builder.Build(ctx, actorID, input)
}

// releases 现建一台绑定本次请求上下文的 ReleaseService。
//
// delivery.CurrentnessChecker.IsCurrent 的签名不带 ctx，而判「此刻还对不对得上」
// 必须带身份去读工作区（T12 的读取侧要在调用者的调用者上下文里跑）。所以上下文在
// 构造时钉住：这一台只服务这一次调用，不跨请求复用。快照库是共享的那个，
// 否则刚冻下的快照下一次就取不到了。
func (s *DeliveryReleaseService) releases(ctx context.Context) *delivery.ReleaseService {
	return delivery.NewReleaseService(s.store, delivery.CurrentnessFunc(func(frozen delivery.DeliveryInput) (bool, error) {
		return s.stillCurrent(ctx, frozen)
	}))
}

// stillCurrent 拿冻结输入记下的项目版本、研究条件与逐章版本去对此刻的工作区。
//
// 判据取保守的一方：项目版本、研究条件、任一章节版本对不上都算不再当前。
// 「还当前」是会被下游当作事实用的那一侧（is_current 为真表示这份交付仍然代表
// 工作区），所以宁可多报几次「变了」——重冻一次很便宜，按一份过期的交付发文件不便宜。
//
// 这里直接读 Reader 而不再判一次成员：本次调用的入口（Check/Prepare/Get）已经在
// 读之前判过同一个项目，且用的是同一个调用者上下文里的身份。
func (s *DeliveryReleaseService) stillCurrent(ctx context.Context, frozen delivery.DeliveryInput) (bool, error) {
	current, err := s.inputs.Reader.ReadDeliveryInput(ctx, frozen.ProjectID)
	if err != nil {
		return false, err
	}
	return sameDeliveryState(frozen, current), nil
}

// sameDeliveryState 逐项对照冻结输入与此刻的工作区。
func sameDeliveryState(frozen delivery.DeliveryInput, current candidateadoption.WorkspaceDeliveryInput) bool {
	if frozen.ProjectVersion != int(current.ProjectVersion) || frozen.SpecRevision != current.SpecRevision {
		return false
	}
	// 章节集合与逐章版本都要对得上：新增、删除、改动任一章节都会让冻结输入不再
	// 描述此刻的工作区，而项目版本理论上会跟着变——两条都判，是因为版本号是
	// 协作方给的，而这里是拿它断言事实。
	versions := make(map[string]*string, len(current.Chapters))
	for _, chapter := range current.Chapters {
		versions[chapter.ChapterID] = chapter.ChapterVersionID
	}
	if len(versions) != len(frozen.Chapters) {
		return false
	}
	for _, chapter := range frozen.Chapters {
		if !sameString(versions[chapter.ChapterID], chapter.ChapterVersionID) {
			return false
		}
	}
	return true
}
