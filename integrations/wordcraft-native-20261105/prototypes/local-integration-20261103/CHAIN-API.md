# 资料、人工意见与变化回看：0.2 接口补充

设计基线仍为 `LD-V1-20261103-R1`；本地运行组件为 `review-chain-0.2.0`。本说明对应当前 `server.mjs`、`chain-store.mjs` 与 `frontend/src/lib/live.tsx`，补充冻结的 R1 设计包，不改写原包哈希。旧 `INTEGRATION.json` 的 `native-adapter-0.1.0` 是上一批保存适配模块的交付记录，不代表本批整个界面状态。

当前范围是一个隔离试用项目、两版社区联系记录、一条可编辑人工意见，以及三条显式依赖变化任务。Go 保存资料与 DOCX；本地服务用 `review-chain.json` 保存资料版本连续关系、意见、人工决定、完成依据和历史。此扩展对象**不是已有 Go ImpactTask**，也没有代替 Go 项目的条件修订或正式交付流程。

## 接入与身份

前端、`/native.html` 和以下接口由同一集成网关提供。端口取实际运行入口，不写死在组件里。网关只允许精确的 `127.0.0.1:<port>` 或 `localhost:<port>` Host；一次页面内使用一致的入口，不混用两个 Origin。

1. `GET /integration/config` 返回平铺对象 `{token, baselineId, projectId, componentVersion}`。
2. 后续业务读取与写入带 `X-Integration-Token: <token>`。这是每次运行生成的临时令牌，不能写进代码、日志或交接包。
3. POST 必须同源并携带令牌；浏览器会生成同源 `Origin`。不同端口的前端开发页不能靠放宽 CORS 直接接入。
4. 服务重启后令牌会变化，应重新获取配置；不要把旧令牌当用户长期凭据。此机制仅服务本机单用户试用，不是生产认证。

| 方法与路径 | 返回或用途 |
|---|---|
| `GET /integration/config` | 本次运行配置；无需令牌，拒绝跨站请求 |
| `GET /integration/health` | `{ready:true, baselineId, pid, projectId}` |
| `GET /integration/state` | 当前完整链路状态；需要令牌 |
| `GET /integration/source?version=v1` | 经 Go 原文接口重新读取 v1；需要令牌 |
| `GET /integration/source?version=v2` | v2 登记后可读取；登记前返回 404 |
| `POST /integration/actions` | 提交下文动作；成功返回新的完整状态 |

这些扩展接口成功时返回平铺 JSON，不使用 Go 产品接口的 `{data,meta}` 包装。失败返回非 2xx 与 `{error:"可读原因"}`。不要只看 JSON 是否可解析就显示成功。

## 状态与版本不是同一个计数器

`GET /integration/state` 的主要字段：

```ts
{
  schema: 1,
  baselineId: 'LD-V1-20261103-R1',
  projectId: string,
  revision: number,               // 本地链路记录的全局修订；每次成功动作 +1
  sourceLatest: 1 | 2,            // 本试用资料的逻辑最新版本
  changeApplied: boolean,        // 本地资料更新已登记；不是 Go ChangeSet.applied
  sourceVersions: { v1: SourceVersion, v2: SourceVersion },
  comment: { id: 'C01', text: string, author: string, excerpt: string },
  records: { C01, R1, R2, R3, METHOD },
  dependencies: [...],
  events: [...],
  backend: {...}
}
```

每条 `record` 保留 `decision`、`reason`、`progress`、`baseVersion`、`baseDocSha256`、`history`，以及适用时的 `anchor`、`change`、`resultVersion`、`completionReason`。`baseVersion` 与 `resultVersion.diskVersion` 都指 **Go 已登记 DOCX 磁盘版本**，不是 DS 版本，也不是原生引擎 revision。

原生 `documentId/revision` 描述当前内存中的文稿实例；DS 磁盘版本、Go 磁盘版本、链路 revision、资料逻辑版本分别展示和校验。不要互相替代。

## 写请求、冲突与重试

所有动作共用以下信封，请求 JSON 上限为 1 MiB：

```js
{
  key: crypto.randomUUID(),       // 8–100 个字母、数字、下划线或连字符
  expectedRevision: state.revision,
  type: 'decide',
  id: 'C01',
  // 动作专属字段
}
```

`id` 省略时默认 `C01`。R1/R2/R3 在 `changeApplied=false` 时不能处理。

- 服务串行处理动作，检查 `expectedRevision`，完整写入临时文件、fsync、rename 后才推进内存状态。
- 完全相同的 `key` 和请求重放不会重复写事件，返回的是当时最新的完整状态，不一定是首次响应的旧快照。
- 同 key 不同请求返回 409。未命中的 key 若依据版本过旧，也返回 409。
- 网络断开、超时或 5xx 属于结果未确认：保留原 key、原 expectedRevision 与原请求内容，先重试原请求。不要生成新 key 自动重做。
- 409 应读取最新状态，让用户重新核对后提交新的决定；不要自动把旧判断套上最新 revision。
- 当前 React `LiveProvider` 在本次页面生命周期内保留未确认请求并提供“重试原请求”；它没有实现刷新后继续恢复待发请求的离线队列。刷新后先读取已持久化状态，再决定后续动作。

## 动作合同

| type | 专属字段 | 实际行为 |
|---|---|---|
| `comment` | `id:'C01', text, author?` | 修改一条活动人工意见。text 必填，最多 8000 字符；author 最多 120。保留 previous 意见和事件历史，把该项重新置为待决定，并以当前 Go 保存版本作为新基线 |
| `anchor` | `id, anchor` | 保存用户从真实原生选区取得的摘录和实例修订；不自动重定位 |
| `decide` | `id, decision, reason?` | 记录人工处理方向；决定修改不等于修改完成 |
| `complete` | `id, reason, savedVersion` | 核对服务器最新保存版本，登记人工确认的处理结果 |
| `publish_source` | `text?`；建议省略 id | 将本试用联系记录 v2 真正上传、绑定并登记，形成 R1/R2/R3 回看项；没有正文改写 |

### 决定与完成

`decision` 允许以下值：

| decision | progress | 说明 |
|---|---|---|
| `modify_document` | `pending` | 用户准备修改正文，仍需保存并登记结果 |
| `update_plan` | `pending` | 记录更新研究方案的意向；方案版本接口未接通，当前不能登记完成 |
| `adjust_both` | `pending` | 正文与方案都需调整；当前不能只用正文保存登记完成 |
| `no_change` | `completed` | reason 必填；这是用户确认无需调整，结果不带正文保存版本 |
| `defer` | `deferred` | 暂缓，仍不算已处理 |

修改正文后的完成请求：

```js
{
  key: 'one-stable-operation-key',
  expectedRevision: latestChainState.revision,
  type: 'complete', id: 'C01',
  reason: '补充社区选择标准，并删除概括全市的表述。',
  savedVersion: {
    diskVersion: native.saved.goDiskVersion,
    docSha256: native.saved.sha256
  }
}
```

前端先 `adapter.refresh()` 并要求 `phase==='saved'`。服务端独立要求：该项为 `pending + modify_document`；不是路线图 R3；说明非空；请求版本与 Go 最新 `diskVersion/docSha256` 完全相同；版本大于该项 baseVersion，且 hash 与基线不同。通过后保存 `{diskVersion,versionId,docSha256}`，事件标记 `humanConfirmed:true`。

这证明用户针对某个真实新保存版本登记了处理说明，不证明模型自动核实了改写语义。保存、意见完成、正式确认、冻结和导出仍是不同操作。DOCX 副本下载不能把所有回看项自动关闭。

### 原生选区绑定

```js
{
  type: 'anchor', id: 'C01', key, expectedRevision,
  anchor: {
    hostSessionId: currentNativeSessionId,
    documentId: selectionResult.state.documentId,
    revision: selectionResult.state.revision,
    selection: selectionResult.selection,
    excerpt: selectionResult.selection.text
  }
}
```

选区来自 `wordcraftHost.engine({op:'selection'})`，不能用演示 section ID 或 Markdown 偏移伪造。服务存储 `status:'user_bound'`，表示用户绑定，不表示已具备跨版本稳定锚点。

当前页面只有 hostSessionId、documentId、revision 三者均相同时显示“选区修订一致”。页面重开产生新的 hostSessionId；旧摘录仍保留，但必须显示“需人工重新定位”。hostSessionId 不是服务器 runtimeId，也不是永久文档 ID。切换页面/面板用隐藏方式保留原生实例；不得重新创建 iframe 以刷新外围状态。将来若主动替换宿主，同样必须换会话标识并重新创建适配器，不能继续复用旧选区。

## 资料的真实持久化与逻辑连续关系

Go 已有路径负责 TXT 上传、解析、项目资产绑定、来源校验和原文读取。服务端 `go-sources.mjs` 将结果保留为 `knowledgeId`、`asset`、`sourceId`、`source`、`document`、`uploadedTextSha256`。前端不要直接 import 这个 Node 模块；调用集成网关即可。

`CONTACT-v1`、`CONTACT-v2` 是本地链路的逻辑版本。每次上传对应 Go 的实际知识和资产 ID；**v2 不必有 `asset_revision=2`，也不要求复用 v1 的 asset ID**。当前 Go 上传路由没有提供稳定资料的替换版本操作，连续关系由本地 `sourceVersions` 和 `change.from/to` 明确记录。

首次打开时 v1 已在 Go，v2 只是 `prepared:true` 的演示更新草稿。`publish_source` 省略 text 时采用场景中的 v2 文本；显式空文本或与 v1 相同的文本会被拒绝。上传和原文读回成功后才设 `prepared:false/sourceLatest:2/changeApplied:true`。v1 及旧依赖依据继续保留，不自动改成最新版本。

登记更新时，固定显式关系 DEP1/DEP2/DEP3 分别指向样本范围、进度安排、路线图，并为 R1/R2/R3 记录新旧资料版本、关系理由及当时的 Go 正文保存基线。这里没有调用 AI 自动发现全篇关系，也没有把样本量自动减半。METHOD 的“未发现直接依赖”不等于系统宣告不受影响，是否无需调整仍由用户说明并确认。

`GET /integration/source?version=v1|v2` 会重新经 Go 校验并返回 `{source,text,segments,contentSha256}`。资料失效或无法读取时必须显示错误；历史快照文本不能冒充刚刚成功读回的原文。

当前只支持这个演示项目的 v1→v2 更新；任意多资料上传、任意版本连续更新、通用图源或方案生命周期不属于这个接口已实现范围。

## 原生接入与校验记录

保留 `native-adapter.js` 对原宿主保存协调器的单一调用路径。外壳只调用 `refresh/save/subscribe/dispose`，不自行 export→DS→Go→ack。网关的 DS/Go 代理只允许当前项目及固定路径，继续执行上游版本和令牌校验。

本批已知证据：24 项单元检查（13 原生适配 + 11 链路存储）、10 项网关检查通过。真实主链记录 `artifacts/chain-2026-11-03T05-34-27.166Z/verification.json` 完成 14 项业务检查后在 reload 脚本竞态失败，文件仍为 `passed:false`，不得改写为通过。之后 `artifacts/reopen-2026-11-03T05-38-17.579Z/verification.json` 的 11 项补验 `passed:true`，包括 Go/DS 整服务重开、两版资料与正文恢复、原生选区绑定、面板独立与专注布局、下载 hash、旧宿主锚点失效；pageErrors 为空，测试浏览器已退出。

这些证据支持本地联调路径；不代表 Kimi 云端已回收这些改动、完整 CI 通过、人工试用完成或正式发布。
