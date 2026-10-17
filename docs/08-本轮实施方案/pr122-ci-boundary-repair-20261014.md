# PR #122 CI 与实际 Word 复核追加（2026-10-14）

## 发现及修正

`eef8946` 的 F01、Frontend、DocReader、Workspace Concurrency 已 completed/success。Go Lint 和 App 失败，原失败保留，不把四项成功写成六项全绿。

- Go Lint：Action 获取 PR diff media 时实际返回 HTTP 406，明确说明超过300个文件；随即不带新增差异参数运行全仓 lint，报167个条目。按当前 PR 本地 Git merge-base 的新增行范围，定位7条新增报告；清理未使用的旧辅助函数、测试类型、无效赋值，`strings.Contains`替换等价重复查找，并处理DOCX包读取器的关闭错误。实际采纳仍保留事务内证据验证，不删除当前验证入口。
- 差异入口改为完整 Git 历史、本地 `merge-base`、固定实际 PR head 与 `--new-from-rev`，保持原“只阻断本PR新增行”政策。缺失/非法SHA或无法取到共同祖先时明确失败；不退回全仓，也不跳过检查。不关闭任何linter、不扩大权限、不依赖GitHub diff API。辅助脚本本身改动也触发检查。
- 新增本地边界测试3/3：301个文件、分叉基线、缺失或不可信SHA。使用独立临时Git仓库、无网络与凭证。
- App：SQLite实际已迁移37，旧测试常量仍是36。现在严格要求37并核对全部新治理/版本记录表及选段改写证据字段；真实空库迁移和v4升级、哨兵数据保留均通过。没有清空或降级验收数据库。

依据：[官方Action新增差异机制](https://github.com/golangci/golangci-lint-action#only-new-issues)、[官方CLI差异参数](https://golangci-lint.run/docs/configuration/cli/)。本轮保持原差异检查范围，替换失败的差异获取方式。

## 本地验证

五包命令 `go test -mod=readonly -p 2 -count=1 ./internal/lingdoc/docx ./internal/lingdoc/reviewanchor ./internal/lingdoc/candidateadoption ./internal/lingdoc/workspace ./internal/lingdoc/workspacecore` 退出0：依次1.767s、1.350s、3.638s、128.582s、62.549s。SQLite两项真实迁移测试退出0，5.951s。Go格式和补丁格式已核对。仅复验受新改动影响范围，不重复前端/模型生成。

修补仅落在PR整合隔离树；运行中的5210/18090仍是此前不可变r2，未重新构建/启动这个后续CI修补头。不能把源码新HEAD冒充运行程序版本。远端CI需在新推送头另行验证，本地通过不替代线上CI。

## 实际 Word

本次产品下载的原始DOCX（3200 bytes）在独立 Microsoft Word 16.0 实例只读打开，原始SHA256 `928ADD155AC201C2AD5E38BD164E87CE5371E0E6C374120538FB76ECF2FDF83C` 保持不变。Word计算2页并输出PDF；负责人逐页查看，未见裁切、重叠或乱码。

另存副本追加编辑文本、保存、关闭、重新打开通过；副本SHA256 `38DC11913E9A060BF70B71219EBE35A6D764B8AA8E185229C9D658C0ACA9FC68`。两章、引用及待核附录保留。仅退出本轮可确认自有Word实例，预存文档未动。它是实际应用自动化证据，不是gong人工验收或科研质量结论。

证据：`acceptance/full-workflow-first-pass-20261013/word-20261014-224457/result.json`、`page-1.png`、`page-2.png`、`edited-copy.docx`。源码CI修补证据在`pr122-integration-20261014/ci-repair-r1`和`migration-ci-repair-r1`。

本轮模型调用0。G1–G7整体仍未宣称完成，权限浏览器回报由Kimi与负责人独立核对；不合并PR或部署。
