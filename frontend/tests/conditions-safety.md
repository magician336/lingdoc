# 研究条件辅助输入保护接入

基线 e52a3dd122c1a7dbb7aad47f129e92e10b01739f；语义参考 a82e8c9c048e292453df56f030c878eb151024c7。此候选没有整体搬运旧 UI 补丁。条件理由与影响章节按 projectId/trim reason/sorted ID 快照判脏；抽屉隐藏、主题恢复到原值后仍受离开/刷新/同项目重读保护。预览仅确认发出时的整体快照。apply/reject 仅豁免已经处理的条件内容，其余章正文、队列保存、待核、引用与模板输入仍需要明确确认。

初始 getProject/listChapters/listAssets/listChangeSets/全部 getWorkingCopy/当前章版本读取在旧 EditorView 仍可用时准备。失败、旧请求和新输入后的确认取消均在 pause 前结束。安装阶段以独立响应式导航锁将工作台设为 inert，并暂停、排空旧保存链；旧请求释放 busy 不会重新打开输入；同项目兼容 spec/base 的较新已收到回执覆盖暂读旧副本，保持 CAS 修订号。初始安装复用已准备副本，避免重复 GET；成功同项目保持活跃章，文档 JSON 一致时恢复文本选区。明确成功重读会重挂编辑器；取消/失败保留原 EditorView、selection 与 Undo。

新增条件 preview/apply/reject 与 409 补读回包按 editorSessionEpoch 校验，busy/attempts/receipt 的变更也受保护。候选/生成等既有 helper 的全局迟响应语义不在此次改造范围，不以本候选声明全面修复。

定向验证：固定 Node v22.19.0，私有 TEMP/TMP，执行 --import ./node_modules/tsx/dist/loader.mjs --test src/views/lingdoc/uiSafety.test.ts，10/10 PASS。conditions-safety.browser.mjs 在 UI_SAFETY_BUILD_ONLY=1 模式成功打包真实 Workspace/LingDocEditor SFC 与 Tiptap；浏览器尚未由此子任务执行，宿主执行和全量/types 交主代理独立验证。

PowerShell（frontend cwd，目录均为指定 acceptance 下的私有证据）：

```powershell
$env:TEMP='D:/Mon3tr/projects/lingdoc-dachuang/acceptance/owner-ui-integration-20261013/conditions-runtime'
$env:TMP=$env:TEMP
$env:UI_SAFETY_EVIDENCE='D:/Mon3tr/projects/lingdoc-dachuang/acceptance/owner-ui-integration-20261013/conditions-browser'
Remove-Item Env:UI_SAFETY_BUILD_ONLY -ErrorAction SilentlyContinue
& 'C:/Users/gongqc/AppData/Local/Author Software/nvm/installs/v22.19.0/node.exe' tests/conditions-safety.browser.mjs
```

runner 所有结果均来自实际断言；通过运行真实组件和 synthetic API 重现隐藏条件/native unload、六类初读失败、快照/晚预览、失败 apply、非当前章真实输入与保存失败、GET 中新增输入取消、旧预览回包不能解锁安装临界段、同项目排空回执/CAS 单调性、apply/reject 和 receipt 归属。fixture 校验 working_copy_revision/spec_revision/base_chapter_version_id；它不连接产品后台、不访问模型、不承担真实权限/部署验收。

未执行：浏览器、全量/type-check、Go、产品服务、模型。未 install、commit、push、开新 PR、合并或部署。共享 node_modules 只读，所有缓存和证据使用独立目录。
