# Spec-753 开工卡片与验收矩阵 —— 正文研究条件变更检测(措辞提交一致性比对,人工确认门)

- 票:magician336/lingdoc#753(裁决 #748;走查证据 #744;与 #751 构成一致性闭环两半)
- 分支:feat/753-condition-change-detection(基线:#757 合并后的 main,回填)
- 性质:跨模块(workspacecore 检测门 + HTTP 错误契约 + 前端弹层第二态)+ 审计(否决留痕),走完整两道门

## 开工卡片

**用户可见结果**
措辞提交(POST /working-copy/commit)时,服务端比对「已登记研究条件值在正文中的出现」:旧正文(base 章节版本)包含某字段值而新正文(工作副本)不再包含 → 疑似条件变化 → 提交被 422 拦下并返回疑似清单;用户可 (a) 改走正式变更(既有语义路径,正文保留工作副本)或 (b) 填写理由「仍按措辞提交」→ 提交成功,否决留痕审计。疑似≠判定,人工门不放宽。

**范围内**
- workspacecore `CommitWorkingCopy`:四重 CAS 通过后、AppendChapter 前加检测门;
  - 检测规则(MVP 纯文本):对每个 spec 字段值 v(trim 非空且有效 rune 数 ≥4):旧正文含 v 且新正文不含 v → 疑似;
  - `CommitWorkingCopyInput` 加可选 `condition_change_dismissal {dismissed_fields []string, confirmed bool, reason string}`:confirmed 时 dismissed_fields 必须与服务端重算的疑似字段集合相等(照 AdoptionConfirmation「确认内容对不上响亮失败」先例),不等 → 409;reason 必填;
  - 疑似非空且未确认 → 422 `condition_change_confirmation_required`(clienterror,details 带 suspected[{field_id,value}]);
  - 确认放行 → 审计 `chapter.commit.condition_dismissed`(details:fields/values/spec_revision/working_copy_revision/reason);
- HTTP:错误 details 透传(sendErrorDetails 既有通道);
- 前端:commitWordingDraft 捕获 422 → ChapterCommitReview 弹层第二态(疑似清单+两个出口:改走正式变更=既有 semantic 路径;仍按措辞=填理由带确认字段重发);
- 契约:CommitWorkingCopyRequest schema 加可选字段+错误响应;check_lingdoc_routes/validate_artifacts/x-delivery-phase/validation-result 同步。

**范围外**
- 模型辅助检测(qwen 判定疑似,记账)——票面允许的最小版先行,模型辅助另票;
- 不自动把措辞提交转语义变更;
- 不改 SaveChapter 直存遗留端点(UI 不使用)与 working copy 自愈 rebase 逻辑;
- 不做语义级(同义改写)检测——纯文本消失才算疑似,误报由否决门吸收。

**领域不变量与事务边界**
- 检测在事务内、CAS 之后:拦截时零写入(幂等层无业务写);
- 疑似判定只基于「值在正文的字面出现」,服务端单一定义,前端不自行判定;
- 否决确认绑内容:dismissed_fields 与服务端重算集合必须相等,防「看到的疑似」与「确认的疑似」错位;
- 审计只在确认放行成功时写一条;普通提交(无疑似)行为与审计不变;
- 幂等:确认重提交是不同 payload → 新幂等键(前端 mutate 惯例);同键重放照既有管线。

**成功/失败/恢复场景**
- 成功(无疑似):201 照旧;成功(否决后):201+审计;
- 失败:疑似未确认 422;确认字段集合不符 409;确认缺理由 422;CAS 不符 409(既有);
- 恢复:用户改走语义路径(正文保留),或补理由重发。

**持久化/并发/审计**
- 无新表;审计走既有 AuditEvent;并发由既有 CAS+幂等管线覆盖。

**当前风险与未决决策**
- 阈值决策:值长度 ≥4 rune(约 2 汉字)才检测,短值("是"/"500")噪音大——记为决策,可调;
- 误报(措辞改写删掉复述):由否决门吸收并留痕,票面口径「疑似≠判定」;
- 前端弹层第二态是 UI 增量,浏览器人眼链照 #750 排练或后续人工核对。

## 验收矩阵(状态 TODO,head 回填)

| # | 验收项 | 入口 | 不变量 | 成功 | 失败/恢复 | 持久化 | 证据 | 状态 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 开工前门:本卡片+矩阵先行 | 本文档 | 先锁范围 | 先于代码提交 | — | N/A | 提交序列 | TODO |
| 2 | 疑似触发:改掉正文中的条件值 → 422+suspected 清单 | 措辞提交 | 字面比对;服务端判定 | 422 details 含 field_id/value | 改走语义或否决 | 零写入 | core+HTTP 测试 | TODO |
| 3 | 无疑似:纯措辞 → 201 照旧、无新审计 | 措辞提交 | 不误伤既有路径 | 201 | — | 与既有一致 | core 测试 | TODO |
| 4 | 阈值:短值(<4 rune)与从未出现的值不触发 | — | 噪音控制 | 不拦截 | — | — | core 边界测试 | TODO |
| 5 | 否决留痕:集合一致+理由 → 201+审计 | 弹层「仍按措辞」 | 确认绑内容 | 201;audit condition_dismissed | — | 审计行 | core 测试断言审计 | TODO |
| 6 | 确认集合不符 → 拒绝 | 恶意/陈旧确认 | 防错位确认 | 409/422 零写入 | 重看清单 | — | core 测试 | TODO |
| 7 | 确认缺理由 → 422 | — | 理由必填 | 422 | — | — | core 测试 | TODO |
| 8 | 语义路径不受影响 | 弹层「改走正式变更」 | 正文保留工作副本 | 打开变更面板不提交 | — | — | 前端测试 | TODO |
| 9 | HTTP 契约+校验器+validation-result 同步 | 契约测试 | 路由校验/新鲜度 | 全绿 | — | — | 契约三测+discover | TODO |
| 10 | 前端弹层第二态:清单+两出口+理由必填 | ChapterCommitReview | 前端不自行判定 | 拦截→展示→两出口可用 | 取消回编辑 | — | 组件测试+type-check+build | TODO |
| 11 | 既有回归:SaveWorkingCopy/自愈 rebase/确认链不变 | — | 零回归 | 定向包全绿 | — | — | WSL go test 定向 | TODO |
| 12 | 文档同步:矩阵回写+PR body | 本文件 | 证据=当前 head | 全行闭环 | — | — | 本文件+PR | TODO |
