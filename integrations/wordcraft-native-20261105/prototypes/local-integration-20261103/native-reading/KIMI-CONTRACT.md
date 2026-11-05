# LingDoc 原生界面交接合同

此文件描述当前实现，不把设计建议写成已实现能力。最终设计取包内 START-HERE.md 与 DESIGN.md；文件版本以 CURRENT.json 的 baselineId、逐文件 SHA256 和构建时间为准。Git HEAD 仅为定位信息，包内包含构建时未提交的文件内容。

## 任务与约束

Kimi 在云端 chat 模式承担界面设计、可审阅的源码修改建议及明确标注的交互 mock。包是选定源码快照，不是完整可编译仓库；不需要配置本地 Go、WSL、模型凭据或完整 Rust 工程。不得宣称云端 mock 已接真实模型/原生保存。不得另建外围工作台替代 WordCraft 原生编辑器。

原生开始页保留新建、打开、最近等既有逻辑；项目入口进入现有项目的文稿与资料。正文、原文阅读是不同视图：正文保持一个原生编辑会话，资料为只读阅读视图，可主动并排，不能用资料替换正文再切回。资源与聊天独立开关。界面保持干净，不添加自动建议、AI 自动写入或意见处理。

## 真实范围与必要缺口

- 已有：单个真实项目入口、一个项目主文稿、TXT/MD/PDF/DOCX 资料副本导入、原文阅读、项目资料问答、会话历史与显式上下文。
- 新建多个项目尚未实现；现有项目卡片选择已接通。Go 项目核心支持多项目，但本地 bootstrap、资源适配与宿主仍绑定一个项目；界面不要显示可用的新建项目按钮。
- 项目主文稿 DS 保存与 Go 版本登记已接通；Go 版本链按项目而非多文稿，且要求六节申报投影。普通文档入口走独立浏览器下载，不写项目保存链。多文稿正式管理需要后端文稿身份扩展，不属于此次布局工作。
- PDF 是本地页图阅读；扫描 PDF 不执行 OCR，只读且不能问答。DOCX 是解析文本，必须标明不保留版式/页眉脚注图片。
- 同名不同原件是独立导入，不伪装成 v2 更新。相同检索文本但不同原件造成 Go 去重时，新副本降为只读，旧引用绑定不变。
- 问答最多四份上下文、总计 12000 Unicode 字符；超过预算不截断，不调用模型，并明确提示可以打开原文。单次问题最多 4000 字符。
- 当前服务不是完整安装产品：布局设计本身不要求增加新后台能力。必要对接工作是保持现有动作、状态、身份与失败提示完整，避免 mock 覆盖真实失败状态。

## HTTP 合同

同源接口前缀 `/native-reading/workspace/`。请求需要启动时提供的 `X-Workspace-Token`；POST 必须有精确同源 Origin。凭据不放设计包。

| 路由 | 输入 / 输出 |
| --- | --- |
| GET state | 工作区状态，见 synthetic-state.json |
| POST action | `{kind,key,expectedSessionId,...}` → 新状态；kind 为 newSession/selectSession/send/addContext/removeContext |
| POST source | `{sourceId,page?,version?}` → `{payload,page}`；payload 为 source.open 页图或 source.openText 文本 |
| POST import | `{projectId,name,base64,key,folder}` → `{record,state}`；每文件 ≤10 MiB，TXT/MD/PDF/DOCX |

key 每个新操作唯一；失败发送不能自动重试。selectSession 的 sessionId 是目标，expectedSessionId 是请求发起时当前会话。状态 stateRevision 单调，前端丢弃旧响应；切会话发生冲突返回409，不把旧输入发到新会话。

state 含 projectId/projectTitle/documentTitle、sources、sessions/currentSessionId、messages、contextSources、busy/error、imports/importBusy/importError。busy 表示问答请求；importBusy 是导入操作，二者不可相互覆盖。来源卡有 sourceId/title/version/quote/page，旧引用不自动打开更新内容。usedSources 是真实送入模型的资料，不等于模型逐条引用。

## 原生桥与 UI 动作

JS 通过 `wordcraftHost.engine` 调用：workspace.hydrate、workspace.status、workspace.actions、workspace.layout、workspace.home、workspace.editor。native workspace.actions 输出 enterProject/importSources/standaloneCommand/body/openSource 或上述会话 action，由宿主执行；Rust 不直接访问模型或磁盘。

阅读采用 source.begin 获得 documentId/viewEpoch，再 source.commit 原子提交 payload/page/mode；任何正文身份或视图世代变更都拒绝过期结果。不得拆成无身份校验的 open 与 mode 两次异步写入。

普通入口 `/` 先设置 WORDCRAFT_STANDALONE，移除项目 host/save-coordinator；`/project` 保留项目保存服务。前端设计不可恢复被隔离的保存钩子。保存态只能来自实际回执，不因完成动画或 AI 回答设置已保存。

## 模型边界

导入仅本地提取、保存副本和关键词索引；明确关闭 multimodel、问题生成、graph 和 extract。当前 KB 无摘要/嵌入模型，Wiki 关闭。AI 仅在用户 send 时按项目检索，返回文字与核验出处；不读取正文、不处理人工意见、不自动写作。新增资料不自动加入固定上下文，但属于默认项目检索范围；移除会话资料将排除后续检索，旧历史仍可能含其内容。

## 验证与回执模板

包内 verify-handoff.mjs 只证明文件哈希和清单一致。单元测试证明约束与模拟分支；浏览器测试证明原生操作；真实模型回执与 Go/DS 保存读回须由本地主线单独报告。截图仅是视觉证据。未运行的测试必须写未运行。

Kimi 回执：baselineId；verify 结果；阅读文件；改动文件；采用的真实接口；mock 列表；未确认问题；是否代码完成/本地联调通过/用户实际试用。仅收到设计包不等于全端同步。

请先给布局与交互改动清单，再给按文件组织的修改。不得编辑 runtime、凭据、数据库、用户原件、保存事务或后台授权策略。当前阶段尚无包内截图时，不依据想象声称已看过最终运行效果。


## 导入原文核验未完成

本地提取和 Go 上传绑定成功不等于出处可核验。Go 对带图片标记的文本等情况可能返回 `source_access_denied`，目前不能进一步区分原文与授权原因。仅限用户本次上传的已保留原件，在成功上传绑定后读回原文遇到此错误时，记录为 `readable_only/searchable:false`，sourceId 使用本地 importId；可阅读自己的副本，不进入 AI 检索。notice 明确“原文核验未完成，暂不可用于问答”，diagnostic 保留内部原因。普通鉴权错误、网络错误、上传/绑定失败仍为失败，不变成只读成功。

目录仅隔离被该导入记录明确绑定的 unavailableAssetId，或通过 Go metadata 的 local_operation_key 与 local_content_sha256 同时匹配该导入记录的资产。其他资料权限错误仍报错。state.catalogWarnings 提供隔离说明，不表示删除资产或自动获得权限。首错保留，不自动重传或调用模型。
