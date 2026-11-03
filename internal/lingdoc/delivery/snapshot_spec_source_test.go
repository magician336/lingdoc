package delivery

import (
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/stretchr/testify/require"
)

// #324：冻结输入里那份**原始规格**（SpecSource）必须随快照完整落库、完整读回。
// 导出与就绪复核拿的是从存储读回的冻结输入，溯源校验只能拿原始规格去对溯源
// 快照（Spec 那份是路线感知投影，对必需路线节点必然失配）；SpecSource 在存储
// 往返里丢一次，路线节点项目就在导出链上回到 403。
func TestSnapshotStoreRoundTripsTheOriginalSpec(t *testing.T) {
	forEachSnapshotStore(t, func(t *testing.T, store snapshotStoreUnderTest) {
		input := validDeliveryInput()
		input.SpecSource = map[string]string{
			"research_subject":                      input.Spec["research_subject"],
			lingdoctemplate.DemoRequiredRouteNodeID: "合成路线占位值（真正的值由路线图节点提供）",
		}
		input.Spec[lingdoctemplate.DemoRequiredRouteNodeID] = ""

		service := NewReleaseService(store)
		snapshot, err := service.Prepare(input)
		require.NoError(t, err)

		readback, err := store.Get(snapshot.ProjectID, snapshot.ID)
		require.NoError(t, err)
		require.NotNil(t, readback.FrozenInput.SpecSource, "a persisted frozen input must carry the original spec")
		require.Equal(t, input.SpecSource, readback.FrozenInput.SpecSource,
			"the original spec must survive the store round trip")
		require.Equal(t, input.Spec, readback.FrozenInput.Spec,
			"the projection spec must survive the store round trip too")
	})
}
