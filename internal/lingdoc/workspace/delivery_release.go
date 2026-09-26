package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
)

// deliveryHistoryLimit 是交付历史列表的上限，快照与产物共用。契约 §3：列表面向
// 演示规模，不引入通用分页平台；若设上限，必须显式提示截断。取 50 与
// listProjects 同值，两处历史在界面上看起来才是同一种东西。
const deliveryHistoryLimit = 50

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
	freezes delivery.FreezeRecorder
}

// NewDeliveryReleaseService 依赖不齐时返回 nil。三个依赖各自都是必需的：
// 少一个，链路就有一截是空的（读不到、补不上、或者冻不下来）。
//
// 快照库还必须能记「哪一次动作冻了它」（delivery.FreezeRecorder）。契约把
// Idempotency-Key 标成 /releases 的必填头，退化成「照冻不误」就等于收下了这个头
// 却不当回事：一次超时重试会在交付历史里多出一条，而调用方以为自己只冻过一次。
func NewDeliveryReleaseService(inputs *candidateadoption.DeliveryInputService, builder *DeliveryInputBuilder, store delivery.SnapshotStore) *DeliveryReleaseService {
	if inputs == nil || builder == nil || store == nil {
		return nil
	}
	freezes, ok := store.(delivery.FreezeRecorder)
	if !ok {
		return nil
	}
	return &DeliveryReleaseService{inputs: inputs, builder: builder, store: store, freezes: freezes}
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

// Prepare 冻结一份快照并落存，返回它是不是这次动作的重放。key 是这次用户动作的
// 幂等键——契约把 Idempotency-Key 标成 /releases 的必填头。
//
// 处理顺序照契约 §6：先判身份，再判是不是已经做过，最后才比版本。
// 「重放先于版本比较」不是两个可以调换的步骤：一次「其实已经冻好了、只是响应
// 丢了」的重试，此刻读回来的工作区完全可能是被那次冻结推动过的版本；先比版本
// 就会把它判成冲突，调用方于是再也拿不回那份已经存在的快照。
func (s *DeliveryReleaseService) Prepare(ctx context.Context, actorID, projectID, key string, expectedProjectVersion int64) (delivery.ReleaseSnapshot, bool, error) {
	if s == nil || s.inputs == nil || s.builder == nil || s.freezes == nil {
		return delivery.ReleaseSnapshot{}, false, candidateadoption.ErrInvalidState
	}
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(projectID) == "" {
		return delivery.ReleaseSnapshot{}, false, candidateadoption.ErrInvalidRequest
	}
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 128 {
		return delivery.ReleaseSnapshot{}, false, candidateadoption.ErrInvalidRequest
	}
	// 1. 身份与项目能力。撤权之后不能从「这个键做过」里把旧快照交出去。
	if err := s.inputs.Authorizer.AuthorizeProject(ctx, actorID, projectID); err != nil {
		return delivery.ReleaseSnapshot{}, false, err
	}
	attempt := delivery.FreezeAttempt{ActorID: actorID, ProjectID: projectID, Key: key}
	requestHash := freezeRequestHash(actorID, projectID, expectedProjectVersion)
	// 2. 已经做过就换回原结果；同键不同请求是冲突，不是又一次冻结。
	replay, found, err := s.freezes.ReplayFreeze(attempt, requestHash)
	if err != nil {
		return delivery.ReleaseSnapshot{}, false, err
	}
	if found {
		return replay, true, nil
	}
	// 3. 只有新动作才比版本，也只有新动作才读工作区。
	input, err := s.assemble(ctx, actorID, projectID, expectedProjectVersion)
	if err != nil {
		return delivery.ReleaseSnapshot{}, false, err
	}
	// 4. 冻结与「这次动作冻了什么」一起落。并发的同键请求只会有一个落笔，
	// 输的那一方拿回赢家的那一份，而不是自己手里这份。
	snapshot, err := s.releases(ctx).Freeze(input)
	if err != nil {
		return delivery.ReleaseSnapshot{}, false, err
	}
	recorded, replayed, err := s.freezes.RecordFreeze(snapshot, attempt, requestHash)
	if err != nil {
		return delivery.ReleaseSnapshot{}, false, err
	}
	return recorded, replayed, nil
}

// freezeRequestHash 是这次动作请求体的规范指纹。契约 §6 要求把它与键一起记下：
// 它是把「重试」与「换了请求却复用同一个键」分开的唯一依据。
func freezeRequestHash(actorID, projectID string, expectedProjectVersion int64) string {
	hasher := sha256.New()
	for _, field := range []string{actorID, projectID, strconv.FormatInt(expectedProjectVersion, 10)} {
		// hash.Hash.Write 从不返回错误（见 hash.Hash 文档）；长度前缀是为了让不同的
		// 字段组合拼不出同一串字节——否则改一下字段边界就成了另一次「同一个请求」。
		_, _ = hasher.Write([]byte(strconv.Itoa(len(field)) + ":" + field))
	}
	return hex.EncodeToString(hasher.Sum(nil))
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

// List 列出项目冻结过的快照，新的在前，并逐条按**此刻**的工作区重算 is_current。
//
// 重算的理由与 Get 逐字相同，只是这一次对整份历史做：列表里交回冻结时记下的
// is_current，等于让翻到第三屏的旧标签冒充当前状态。契约 §7 要求界面在「页面刷新、
// 重新打开、本地编辑成功、展示下载入口时」更新当前性，那个要求能成立的前提正是
// 每次刷新都拿到重算过的值，而不是存下来的那个。
//
// 第二个返回值说的是「这份历史被截断了」——契约 §3 要求设了上限就必须显式提示，
// 界面不能把截断后的列表当成全部历史。
func (s *DeliveryReleaseService) List(ctx context.Context, actorID, projectID string) ([]delivery.ReleaseSnapshot, bool, error) {
	if s == nil || s.inputs == nil || s.store == nil {
		return nil, false, candidateadoption.ErrInvalidState
	}
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(projectID) == "" {
		return nil, false, candidateadoption.ErrInvalidRequest
	}
	// 与 Get 同一条顺序：先判成员再列。反过来的话，非成员能从「空列表」与
	// 「没权限」的差别里问出某个项目有没有冻结过东西。
	if err := s.inputs.Authorizer.AuthorizeProject(ctx, actorID, projectID); err != nil {
		return nil, false, err
	}
	lister, ok := s.store.(delivery.SnapshotLister)
	if !ok {
		return nil, false, candidateadoption.ErrInvalidState
	}
	snapshots, err := lister.ListSnapshots(projectID)
	if err != nil {
		return nil, false, err
	}
	// 先截断再重算，而不是算完再截断：重算一条要读一次整个工作区，被截掉的那些
	// 算出来也没人要。响应一模一样，读的次数从「历史有多长」降到「界面看得到几条」。
	truncated := len(snapshots) > deliveryHistoryLimit
	if truncated {
		snapshots = snapshots[:deliveryHistoryLimit]
	}
	for index := range snapshots {
		current, err := s.stillCurrent(ctx, snapshots[index].FrozenInput)
		if err != nil {
			return nil, false, err
		}
		snapshots[index].IsCurrent = current
	}
	return snapshots, truncated, nil
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

// stillCurrent 直接读 Reader 而不再判一次成员：本次调用的入口（Check/Prepare/Get）
// 已经在读之前判过同一个项目，且用的是同一个调用者上下文里的身份。
func (s *DeliveryReleaseService) stillCurrent(ctx context.Context, frozen delivery.DeliveryInput) (bool, error) {
	return deliveryCurrentness(ctx, s.inputs.Reader, frozen)
}

// deliveryCurrentness 拿冻结输入记下的项目版本、研究条件与逐章版本去对此刻的工作区。
//
// T13 的 is_current 与 T14 的 stale_input 判的是同一件事，所以判据只有这一份：
// 两处各判一次的话，「T13 说这份快照仍代表工作区、T14 却把同一份快照判成过期」
// 是迟早的事，而那时没人知道该信哪一个。
//
// 判据取保守的一方：项目版本、研究条件、任一章节版本对不上都算不再当前。
// 「还当前」是会被下游当作事实用的那一侧（is_current 为真表示这份交付仍然代表
// 工作区），所以宁可多报几次「变了」——重冻一次很便宜，按一份过期的交付发文件不便宜。
func deliveryCurrentness(ctx context.Context, reader candidateadoption.DeliveryInputReader, frozen delivery.DeliveryInput) (bool, error) {
	current, err := reader.ReadDeliveryInput(ctx, frozen.ProjectID)
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
