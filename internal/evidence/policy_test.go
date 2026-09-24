package evidence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// policyCase 是一套真实回路：真 SQLite 绑定 + 真文件读取 + 真来源产出。
//
// 来源必须由 Resolve 真实产出。手搓一个 Source 结构体只能证明「字段抄对了」，
// 而这条链路要证的是**复核与产出用的是同一个判据**——两边各写一套判据时，
// 「可用」在产出时点与复核时点会各自演化，测试却双双是绿的。
type policyCase struct {
	store   *Bindings
	reader  OriginReader
	gateway AssetGateway
	policy  SourcePolicy
	asset   Asset
	origin  string
}

type mutableKnowledgeSignal struct {
	signal  KnowledgeSignal
	present bool
}

func (r *mutableKnowledgeSignal) CurrentKnowledgeSignal(context.Context, string) (KnowledgeSignal, bool, error) {
	return r.signal, r.present, nil
}

func newPolicyCase(t *testing.T, kb *fakeKBRead) *policyCase {
	t.Helper()
	ctx := context.Background()

	// 合成资料落成真实文件，且写成 LF：Windows 检出可能把 fixture 变 CRLF，
	// 而坐标是按 rune 写死的。判据只该取决于实现，不取决于检出方式。
	origin := syntheticOrigin(t)
	path := writeText(t, "synthetic.txt", origin)
	reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
		"k-demo": {ID: "k-demo", FilePath: path, FileType: "txt", ParseStatus: types.ParseStatusCompleted},
	}})

	store := newTestStore(t)
	asset, err := store.Bind(ctx, bindInputKB("p-1", "k-demo", "kb-ok", nil))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	gateway := NewAssetGateway(store, NewFixedAuthorizer(store, kb))
	return &policyCase{
		store: store, reader: reader, origin: origin, asset: asset,
		gateway: gateway,
		policy:  NewSourcePolicy(gateway, reader, store),
	}
}

// resolve 走真实产出路径拿一条来源。
func (c *policyCase) resolve(t *testing.T, asset Asset) Source {
	t.Helper()
	srcs, err := NewSourceResolver(c.reader).
		Resolve(context.Background(), asset, []*types.SearchResult{goalHit()})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(srcs) != 1 {
		t.Fatalf("产出 %d 条来源, want 1", len(srcs))
	}
	return srcs[0]
}

// validate 是绝大多数用例的入口：一个项目、一条来源。
func (c *policyCase) validate(t *testing.T, src Source) *ValidateResult {
	t.Helper()
	res, err := c.policy.Validate(context.Background(), "p-1", Actor{UserID: "u-1", TenantID: "7"}, []Source{src})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return res
}

// 一批已产出的来源里，仍可用的那批应当原样通过复核：版本没动、坐标仍对得上。
func TestValidateKeepsUsableSources(t *testing.T) {
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)
	if src.Status != SourceAvailable {
		t.Fatalf("前提不成立：产出的来源状态 = %q", src.Status)
	}

	res := c.validate(t, src)
	if len(res.Unusable) != 0 {
		t.Fatalf("可用的来源被判为不可用: %+v", res.Unusable)
	}
	if len(res.Usable) != 1 {
		t.Fatalf("可用的来源数 = %d, want 1", len(res.Usable))
	}
	got := res.Usable[0]
	if got.Status != SourceAvailable {
		t.Fatalf("复核后的状态 = %q, want available", got.Status)
	}
	if got.ID != src.ID || got.AssetRevision != 1 {
		t.Fatalf("复核放行的来源与输入的来源不是同一条: %+v", got)
	}
}

func TestValidateRefreshesCurrentSignalBeforeAllowingSource(t *testing.T) {
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)
	current := &mutableKnowledgeSignal{
		signal:  signal(nil),
		present: true,
	}
	c.store.SetKnowledgeSignalReader(current)
	current.signal.ParseStatus = types.ParseStatusProcessing

	res := c.validate(t, src)
	if len(res.Usable) != 0 || len(res.Unusable) != 1 {
		t.Fatalf("当前 processing 的来源仍被放行: %+v", res)
	}
	if res.Unusable[0].AssetDeny != DenyNotReady {
		t.Fatalf("当前 processing 的拒绝原因 = %+v, want not_ready", res.Unusable[0])
	}
}

func TestValidateRefreshesReparseFingerprintBeforeAllowingSource(t *testing.T) {
	ctx := context.Background()
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)
	current := &mutableKnowledgeSignal{
		signal: signal(func(s *KnowledgeSignal) {
			s.KnowledgeID = "k-demo"
			s.FileHash = "hash-after-reparse"
		}),
		present: true,
	}
	c.store.SetKnowledgeSignalReader(current)

	// Call Validate directly: no preceding list/retrieve request may be required
	// to observe that the original has been reparsed.
	res := c.validate(t, src)
	if len(res.Usable) != 0 || len(res.Unusable) != 1 {
		t.Fatalf("重解析后仍放行旧来源: %+v", res)
	}
	if res.Unusable[0].Status != SourceStale {
		t.Fatalf("重解析后的来源状态 = %q, want stale", res.Unusable[0].Status)
	}

	assets, err := c.store.BoundAssets(context.Background(), "p-1")
	if err != nil {
		t.Fatalf("BoundAssets: %v", err)
	}
	if len(assets) != 1 || assets[0].AssetRevision != 2 {
		t.Fatalf("Validate 未刷新当前修订: %+v, want revision 2", assets)
	}
}

// 复核必须现查授权，不许拿产出时的结论复用——否则撤权之后引用照旧可用（F07）。
func TestValidateReflectsRevocationImmediately(t *testing.T) {
	// 第 2 次问到时改口：一次 Validate 问一次权限，两次调用之间撤权。
	kb := &fakeKBRead{allowed: map[string]bool{"kb-ok": true}, revokeAt: map[string]int{"kb-ok": 2}}
	c := newPolicyCase(t, kb)
	src := c.resolve(t, c.asset)

	if res := c.validate(t, src); len(res.Usable) != 1 {
		t.Fatalf("撤权前就判成不可用: %+v", res.Unusable)
	}
	res := c.validate(t, src)
	if len(res.Usable) != 0 {
		t.Fatal("撤权后仍放行——复核用了历史授权结论")
	}
	if len(res.Unusable) != 1 || res.Unusable[0].AssetDeny != DenyNotAuthorized {
		t.Fatalf("撤权后的结论 = %+v, want not_authorized", res.Unusable)
	}
}

// 资料此刻不在允许集合里时，坐标是否对得上已无意义：不该再去读原文，
// 也不该因为「坐标碰巧还对得上」就报可用。
func TestValidateReportsAssetLayerBeforeLocator(t *testing.T) {
	ctx := context.Background()
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)

	t.Run("不在本项目", func(t *testing.T) {
		// 同样的知识在别的项目里另有一份绑定，但这条来源挂的是本项目的资料。
		other, err := c.store.Bind(ctx, bindInputKB("p-2", "k-demo", "kb-ok", nil))
		if err != nil {
			t.Fatalf("Bind(p-2): %v", err)
		}
		if other.ID == c.asset.ID {
			t.Fatal("前提不成立：两份绑定不该是同一行")
		}
		foreign := c.resolve(t, other)
		res := c.validate(t, foreign)
		if len(res.Usable) != 0 {
			t.Fatal("别的项目的资料经本项目复核被放行")
		}
		if len(res.Unusable) != 1 || res.Unusable[0].AssetDeny != DenyNotFound {
			t.Fatalf("结论 = %+v, want not_found（与「不存在」不可分辨）", res.Unusable)
		}
	})

	t.Run("已不就绪", func(t *testing.T) {
		// 同版本、只翻转状态：指纹不吃 ParseStatus，所以这不会递增版本。
		if _, err := c.store.ObserveAsset(ctx, "p-1", c.asset.ID, signal(func(s *KnowledgeSignal) {
			s.KnowledgeID = "k-demo"
			s.ParseStatus = types.ParseStatusProcessing
		})); err != nil {
			t.Fatalf("ObserveAsset: %v", err)
		}
		res := c.validate(t, src)
		if len(res.Unusable) != 1 || res.Unusable[0].AssetDeny != DenyNotReady {
			t.Fatalf("结论 = %+v, want not_ready", res.Unusable)
		}
		if res.Unusable[0].Status != "" {
			t.Fatalf("资料层没过时坐标层不该作答，得到 status=%q", res.Unusable[0].Status)
		}
	})
}

// 资料版本前进了就不采信旧坐标。判据与 Resolve 对 ContentRevision 的态度一致：
// 被记录过变更的引用，即便坐标此刻仍对得上，也不得采信。
func TestValidateStalesASourceWhoseAssetMovedOn(t *testing.T) {
	ctx := context.Background()
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)

	if _, err := c.store.ObserveAsset(ctx, "p-1", c.asset.ID, signal(func(s *KnowledgeSignal) {
		s.KnowledgeID = "k-demo"
		s.FileHash = "hash-v2"
	})); err != nil {
		t.Fatalf("ObserveAsset: %v", err)
	}

	// 原文一个字没动，坐标仍然逐字对得上——正是这条用例要挡的「假可用」。
	if _, _, err := c.reader.OriginText(ctx, "k-demo"); err != nil {
		t.Fatalf("OriginText: %v", err)
	}
	res := c.validate(t, src)
	if len(res.Usable) != 0 {
		t.Fatal("资料已前进到第 2 版，产出于第 1 版的引用不该仍判可用")
	}
	if len(res.Unusable) != 1 {
		t.Fatalf("结论数 = %d, want 1", len(res.Unusable))
	}
	got := res.Unusable[0]
	if got.Status != SourceStale {
		t.Fatalf("状态 = %q, want stale", got.Status)
	}
	if got.AssetDeny != "" {
		t.Fatalf("资料层放行了，不该带拒绝原因：%q", got.AssetDeny)
	}
	if !strings.Contains(got.Detail, "1") || !strings.Contains(got.Detail, "2") {
		t.Fatalf("理由里要能看出是哪两版：%q", got.Detail)
	}
}

// 版本没动，但坐标本身对不上了：逐字比对取回的那一段，与产出时记录的不一致。
func TestValidateStalesASourceWhoseCoordinatesMoved(t *testing.T) {
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)

	moved := src
	moved.Anchor.StartAt++ // 长度仍自洽，取回的字串却不再是记录的那一段
	res := c.validate(t, moved)
	if len(res.Usable) != 0 {
		t.Fatal("坐标漂移的来源仍被判可用")
	}
	if len(res.Unusable) != 1 || res.Unusable[0].Status != SourceStale {
		t.Fatalf("结论 = %+v, want stale", res.Unusable)
	}
}

// 越界不属于「曾经可用但现在不可信」，是「取不回来」：两档不能混。
func TestValidateSeparatesOutOfRangeFromStale(t *testing.T) {
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)

	out := src
	out.Anchor.EndAt = len([]rune(c.origin)) + 1
	res := c.validate(t, out)
	if len(res.Unusable) != 1 || res.Unusable[0].Status != SourceUnavailable {
		t.Fatalf("结论 = %+v, want unavailable", res.Unusable)
	}
}

// 被标过重写或编辑的引用，即便坐标此刻仍对得上也不得采信——与 Resolve 同一条规矩。
func TestValidateKeepsRewriteFlagBeforeContent(t *testing.T) {
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)

	rewritten := src
	rewritten.Anchor.ContentRewritten = true
	res := c.validate(t, rewritten)
	if len(res.Usable) != 0 {
		t.Fatal("被标过重写的引用仍被判可用")
	}
	if len(res.Unusable) != 1 || res.Unusable[0].Status != SourceStale {
		t.Fatalf("结论 = %+v, want stale", res.Unusable)
	}
}

// 弱档：原文取不回来时，与产出时同一判据——坐标自洽即仍可用，强度是声明过的。
// 换一个能读到原文的读取方时复核应当升级判据：坐标对不上就是 stale。
func TestValidateRechecksAtTheStrongestTierAvailable(t *testing.T) {
	ctx := context.Background()
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)

	// 读取方看不到原文（PDF 那类：需要重解析，拿不到可比对的字节）。
	weak := NewSourcePolicy(c.gateway, NewOriginReader(&fakeKnowledge{
		rows: map[string]*types.Knowledge{"k-demo": {ID: "k-demo", FilePath: "whatever.pdf", FileType: "pdf"}},
	}), c.store)
	res, err := weak.Validate(ctx, "p-1", Actor{UserID: "u-1", TenantID: "7"}, []Source{src})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(res.Usable) != 1 {
		t.Fatalf("弱档下坐标自洽的来源应当仍可用: %+v", res.Unusable)
	}

	// 强档读取方在，而坐标对不上：复核比产出时的弱档更严，结论是 stale。
	moved := src
	moved.Anchor.StartAt++
	res, err = c.policy.Validate(ctx, "p-1", Actor{UserID: "u-1", TenantID: "7"}, []Source{moved})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(res.Unusable) != 1 || res.Unusable[0].Status != SourceStale {
		t.Fatalf("结论 = %+v, want stale", res.Unusable)
	}
}

// 上游故障要如实报错，不能退化成「这条来源不可用」——那会把一次故障
// 说成一批引用失效，消费者据此删掉的是好数据。
func TestValidateReportsUpstreamFailure(t *testing.T) {
	ctx := context.Background()
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)
	actor := Actor{UserID: "u-1", TenantID: "7"}

	broken := errors.New("底座不可达")
	reader := NewSourcePolicy(c.gateway, NewOriginReader(&fakeKnowledge{err: broken}), c.store)
	if _, err := reader.Validate(ctx, "p-1", actor, []Source{src}); !errors.Is(err, broken) {
		t.Fatalf("原文读取故障被吞掉了: %v", err)
	}

	denied := NewAssetGateway(c.store, NewFixedAuthorizer(c.store, &fakeKBRead{err: broken}))
	authz := NewSourcePolicy(denied, c.reader, c.store)
	if _, err := authz.Validate(ctx, "p-1", actor, []Source{src}); !errors.Is(err, broken) {
		t.Fatalf("授权判定故障被吞掉了: %v", err)
	}

	// 修订行读不到时不能退化成弱判据：归位校验是复核挡住「锚点指向别的文档」
	// 的唯一一道，静默降级等于把这道守卫拆了，而且拆得没有声音。
	blind := NewSourcePolicy(c.gateway, c.reader, brokenRevisions{err: broken})
	if _, err := blind.Validate(ctx, "p-1", actor, []Source{src}); !errors.Is(err, broken) {
		t.Fatalf("修订行读取故障被吞掉了: %v", err)
	}
}

// brokenRevisions 让归位校验的存储不可达，只用于证「故障不外泄成结论」。
type brokenRevisions struct{ err error }

func (b brokenRevisions) KnowledgeOfRevision(context.Context, string, int) (string, bool, error) {
	return "", false, b.err
}

// 归位：坐标要回原文，但「哪一份知识是这份资料的正文」得由资料侧说了算。
// 锚点指向另一份知识的来源，即便那份知识里坐标逐字对得上，也不是这份资料的证据。
// （产出侧有同义的守卫，见 source.go 的 ErrAssetKnowledgeMismatch。）
func TestValidateRejectsAnAnchorFromAnotherKnowledge(t *testing.T) {
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)

	// 另一份知识，内容一模一样：坐标必然"对得上"，正是要挡的那种。
	path := writeText(t, "other.txt", c.origin)
	reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
		"k-demo":  {ID: "k-demo", FilePath: path, FileType: "txt", ParseStatus: types.ParseStatusCompleted},
		"k-other": {ID: "k-other", FilePath: path, FileType: "txt", ParseStatus: types.ParseStatusCompleted},
	}})
	policy := NewSourcePolicy(c.gateway, reader, c.store)

	forged := src
	forged.Anchor.KnowledgeID = "k-other"
	res, err := policy.Validate(context.Background(), "p-1", Actor{UserID: "u-1", TenantID: "7"}, []Source{forged})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(res.Usable) != 0 {
		t.Fatal("锚点指向别的文档、坐标又恰好对得上的来源被判可用")
	}
	if len(res.Unusable) != 1 || res.Unusable[0].Status != SourceStale {
		t.Fatalf("结论 = %+v, want stale", res.Unusable)
	}
	if !strings.Contains(res.Unusable[0].Detail, "k-other") {
		t.Fatalf("理由里要能看出锚点指着哪个知识：%q", res.Unusable[0].Detail)
	}
}

// 重解析后底座的算的是新的知识 ID，记在当前修订行上；公开 Asset 也必须跟随
// 当前修订，才能让新版检索命中通过 Resolve，再由 Validate 复核同一份知识。
func TestValidateAcceptsTheKnowledgeRecordedForThatRevision(t *testing.T) {
	ctx := context.Background()
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})

	if _, err := c.store.ObserveAsset(ctx, "p-1", c.asset.ID, signal(func(s *KnowledgeSignal) {
		s.KnowledgeID = "k-demo-v2"
		s.FileHash = "hash-v2"
	})); err != nil {
		t.Fatalf("ObserveAsset: %v", err)
	}
	latest, err := c.store.BoundAssets(ctx, "p-1")
	if err != nil {
		t.Fatalf("BoundAssets: %v", err)
	}
	if len(latest) != 1 || latest[0].AssetRevision != 2 {
		t.Fatalf("前提不成立：库里的资料 = %+v", latest)
	}
	if latest[0].KnowledgeID != "k-demo-v2" {
		t.Fatalf("前提不成立：公开资料应指向当前修订知识，得到 %q", latest[0].KnowledgeID)
	}

	path := writeText(t, "v2.txt", c.origin)
	reader := NewOriginReader(&fakeKnowledge{rows: map[string]*types.Knowledge{
		"k-demo":    {ID: "k-demo", FilePath: path, FileType: "txt", ParseStatus: types.ParseStatusCompleted},
		"k-demo-v2": {ID: "k-demo-v2", FilePath: path, FileType: "txt", ParseStatus: types.ParseStatusCompleted},
	}})
	freshHit := goalHit()
	freshHit.KnowledgeID = latest[0].KnowledgeID
	freshSources, err := NewSourceResolver(reader).Resolve(ctx, latest[0], []*types.SearchResult{freshHit})
	if err != nil {
		t.Fatalf("Resolve(当前修订): %v", err)
	}
	if len(freshSources) != 1 {
		t.Fatalf("Resolve(当前修订) 返回 %d 条来源, want 1", len(freshSources))
	}
	fresh := freshSources[0]
	fresh.ID = "s-v2"

	res, err := NewSourcePolicy(c.gateway, reader, c.store).
		Validate(ctx, "p-1", Actor{UserID: "u-1", TenantID: "7"}, []Source{fresh})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(res.Usable) != 1 || len(res.Unusable) != 0 {
		t.Fatalf("第 2 版的知识被判不可用（归位判据取错了行）: %+v", res.Unusable)
	}
}

// 修订行缺失（历史数据/被手工清过）时判不定，退回产出侧用的那条判据——资料行上的
// 知识。退回是为了不造出产出侧认不下的结论：Resolve 正是按这个字段放行的。
func TestValidateFallsBackWhenTheRevisionRowIsGone(t *testing.T) {
	ctx := context.Background()
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)

	if err := c.store.db.WithContext(ctx).
		Exec("DELETE FROM lingdoc_asset_revisions WHERE asset_id = ?", c.asset.ID).Error; err != nil {
		t.Fatalf("清掉修订行: %v", err)
	}
	if res := c.validate(t, src); len(res.Usable) != 1 {
		t.Fatalf("修订行缺失不该把来源判死（产出侧按同一个字段放行）: %+v", res.Unusable)
	}

	// 退回的不变量仍然成立：锚点不是这份资料的知识照样判 stale。
	forged := src
	forged.Anchor.KnowledgeID = "k-other"
	res := c.validate(t, forged)
	if len(res.Unusable) != 1 || res.Unusable[0].Status != SourceStale {
		t.Fatalf("结论 = %+v, want stale", res.Unusable)
	}
}

// 某个来源不指向任何资料时逐项作答，别把整批打成错误：调用方要的是
// 「这批里哪些能用、哪些不能、为什么」（契约 §8）。
func TestValidateAnswersPerSourceWhenAnAssetIsMissing(t *testing.T) {
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	good := c.resolve(t, c.asset)
	orphan := good
	orphan.ID = "s-orphan"
	orphan.AssetID = ""

	res, err := c.policy.Validate(context.Background(), "p-1", Actor{UserID: "u-1", TenantID: "7"},
		[]Source{good, orphan})
	if err != nil {
		t.Fatalf("一条坏来源不该让整批失败: %v", err)
	}
	if len(res.Usable) != 1 || res.Usable[0].ID != good.ID {
		t.Fatalf("可用集合 = %v, want 仅 %s", sourceIDs(res.Usable), good.ID)
	}
	if len(res.Unusable) != 1 {
		t.Fatalf("逐条作答数 = %d, want 1: %+v", len(res.Unusable), res.Unusable)
	}
	if got := res.Unusable[0]; got.AssetDeny != DenyNotFound || got.Detail == "" {
		t.Fatalf("不指向资料的来源 = %+v, want not_found 且带说明", got)
	}
}

// ID 的归一与网关同一套：带空白的 AssetID 在网关那边会被修剪后回答，
// 复核若不修剪就会得出「网关一条都没答」的假故障。
func TestValidateNormalizesIDsLikeTheGateway(t *testing.T) {
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)

	padded := src
	padded.ID = "  s-padded  "
	padded.AssetID = " " + c.asset.ID + "\t"
	res := c.validate(t, padded)
	if len(res.Usable) != 1 || len(res.Unusable) != 0 {
		t.Fatalf("带空白的 ID 被当成了另一条引用: %+v", res.Unusable)
	}
	if res.Requested[0] != "s-padded" || res.Usable[0].ID != "s-padded" {
		t.Fatalf("归一后的 ID 与 Requested 不一致：%v / %q", res.Requested, res.Usable[0].ID)
	}
}

// 空批次是「没有引用」，不是「全部引用」：成功返回空集，且不触达授权判定。
// 空 ID 一并按「没有这条引用」处理，不落进 Requested——否则调用方会收到一个
// 它没问过的 ID 的结论。
func TestValidateEmptyBatchTouchesNothing(t *testing.T) {
	batches := map[string][]Source{
		"nil":         nil,
		"空切片":         {},
		"只有空 ID":      {{ID: ""}},
		"空 ID 与空白 ID": {{ID: ""}, {ID: "  "}},
	}
	for name, batch := range batches {
		t.Run(name, func(t *testing.T) {
			kb := &fakeKBRead{allowed: map[string]bool{"kb-ok": true}}
			c := newPolicyCase(t, kb)

			res, err := c.policy.Validate(context.Background(), "p-1", Actor{UserID: "u-1", TenantID: "7"}, batch)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if len(res.Requested) != 0 || len(res.Usable) != 0 || len(res.Unusable) != 0 {
				t.Fatalf("空批次返回了内容: %+v", res)
			}
			if kb.calls != 0 {
				t.Fatalf("空批次触达了授权判定 %d 次", kb.calls)
			}
		})
	}
}

// 一次请求里每一维度的失配各出现一条，且请求集合与返回集合逐项相等：
// 少了哪一条，消费者就会把「没被复核」当成「复核通过」。
func TestValidateAccountsForEveryRequestedSource(t *testing.T) {
	ctx := context.Background()
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	actor := Actor{UserID: "u-1", TenantID: "7"}

	// 先拿一条第 1 版的，再让资料前进一版，然后拿一条第 2 版的。
	before := c.resolve(t, c.asset)
	if _, err := c.store.ObserveAsset(ctx, "p-1", c.asset.ID, signal(func(s *KnowledgeSignal) {
		s.KnowledgeID = "k-demo"
		s.FileHash = "hash-v2"
	})); err != nil {
		t.Fatalf("ObserveAsset: %v", err)
	}
	// 第 2 版那条要从库里读回来的资料产出：Resolve 不会回库刷新，
	// 拿产出时的旧对象去解析只会拿到第 1 版的来源（verify_t09 钉过这条）。
	latest, err := c.store.BoundAssets(ctx, "p-1")
	if err != nil {
		t.Fatalf("BoundAssets: %v", err)
	}
	if len(latest) != 1 || latest[0].AssetRevision != 2 {
		t.Fatalf("前提不成立：库里的版本 = %+v", latest)
	}
	usable := c.resolve(t, latest[0])

	movedOn := before // 产出于第 1 版，资料已到第 2 版
	movedOn.ID = "s-moved-on"
	unbound := usable // 资料不在本项目
	unbound.ID = "s-unbound"
	unbound.AssetID = "a-missing"
	shifted := usable // 版本没动，坐标漂了
	shifted.ID = "s-shifted"
	shifted.Anchor.StartAt++

	res, err := c.policy.Validate(ctx, "p-1", actor, []Source{usable, movedOn, unbound, shifted, usable})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// 同一条来源出现两次只答一次（与 asset_ids 的 uniqueItems 一致）。
	want := []string{usable.ID, "s-moved-on", "s-unbound", "s-shifted"}
	if len(res.Requested) != len(want) {
		t.Fatalf("Requested = %v, want %v", res.Requested, want)
	}
	for i, id := range want {
		if res.Requested[i] != id {
			t.Fatalf("Requested = %v, want %v", res.Requested, want)
		}
	}
	if len(res.Usable) != 1 || res.Usable[0].ID != usable.ID {
		t.Fatalf("可用集合 = %v, want 仅 %s", sourceIDs(res.Usable), usable.ID)
	}
	reasons := map[string]UnusableSource{}
	for _, got := range res.Unusable {
		reasons[got.SourceID] = got
	}
	if len(reasons) != 3 {
		t.Fatalf("逐条作答数 = %d, want 3: %+v", len(reasons), res.Unusable)
	}
	if got := reasons["s-unbound"]; got.AssetDeny != DenyNotFound || got.Status != "" {
		t.Fatalf("未绑定的来源 = %+v, want not_found（坐标层不作答）", got)
	}
	if got := reasons["s-moved-on"]; got.Status != SourceStale || got.AssetDeny != "" {
		t.Fatalf("版本前进过的来源 = %+v, want stale", got)
	}
	if got := reasons["s-shifted"]; got.Status != SourceStale {
		t.Fatalf("坐标漂移的来源 = %+v, want stale", got)
	}
}

// 网关对请求的资料既没说允许也没说拒绝（协作方坏了）时要如实报错，
// 不能凭空造一个原因——编出来的原因会让调用方按错误的理由丢弃引用。
func TestValidateReportsGatewayThatAnswersNothing(t *testing.T) {
	c := newPolicyCase(t, &fakeKBRead{allowed: map[string]bool{"kb-ok": true}})
	src := c.resolve(t, c.asset)

	policy := NewSourcePolicy(silentGateway{}, c.reader, c.store)
	_, err := policy.Validate(context.Background(), "p-1", Actor{UserID: "u-1", TenantID: "7"}, []Source{src})
	if err == nil {
		t.Fatal("网关一条都没答，复核却给出了结论")
	}
}

// silentGateway 违反 ResolveResult 的不变量：每个请求的 ID 都必须有归宿。
type silentGateway struct{}

func (silentGateway) ResolveAllowed(context.Context, string, Actor, []string) (*ResolveResult, error) {
	return &ResolveResult{}, nil
}
