# R1 材料上传 metadata 契约

> 归属：PR #614（R-1 项目内材料接入闭环，票 #606）。
> 本文把 R1 引入的前后端共享 metadata 约定登记到 docs/08，规格与验收全文见
> `docs/06-灵档产品开发规划/R1-项目内材料接入闭环.md`。

## 约定内容

项目内材料上传（`ProjectMaterialIntakePanel` → `projectMaterialsApi.projectMaterialPorts.upload`）在平台上传接口
`POST /api/v1/knowledge-bases/:id/knowledge/file` 的 `metadata` 字段写入两个键：

| 键 | 值 | 含义 |
|---|---|---|
| `lingdoc_upload_project_id` | LingDoc 项目 ID | 该知识行是**从哪个项目**发起上传的意图标记 |
| `lingdoc_upload_origin` | `'workspace'` | 上传来源为 LingDoc 工作台（区别于平台原生上传） |

## 语义边界

- 这两个键只记录**上传意图**，不是权威的项目归属：绑定事实以
  `POST /api/v1/lingdoc/projects/:id/assets`（BindAsset）落库的
  `lingdoc_project_assets` 为准。
- 不以此 metadata 做任何授权判定；跨项目/跨账号访问控制仍走平台知识库权限与 BindAsset 校验。
- 底座展示知识行时可用它回指「来自哪个项目」，但不得据此推断该知识只属于该项目。

## 变更守则

改键名、加键或改变语义时：同步更新本文件与
`frontend/src/views/lingdoc/projectMaterialsApi.ts`（唯一写入方）、
`frontend/src/views/lingdoc/projectMaterialIntakePanel.test.js`（契约断言）。
