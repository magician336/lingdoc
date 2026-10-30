# Issue #682 评审返工验收矩阵

目标：修复 PR #755 对已确认来源变化的审查阻断，并保留既有来源、权限、历史及兼容契约。受影响消费者包括证据卡、上下文包和写作生成；目录、来源和卡片的验证及版本化写入必须沿现有事务和权限边界执行，不以元数据冒充原文。撤稿自动监测不在本期范围；本期处理系统已知的撤稿、来源失效和目录变化。

**证据口径（2026-10-30）：下表保留返工中间阶段的状态快照；此后当前工作树的补充验证见 §4–§6。提交及其远端 CI 在编写时仍未完成，历史 head 的结果不能代替新提交的检查。** 正式目录、额度和研究者验收仍待确定。

| 验收项 | 用户入口 | 领域不变量 / 事务边界 | 成功 | 失败、权限与恢复 | 持久化与并发 | 测试 / 运行证据 | 中间阶段状态 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 人工关联和快照 | 证据卡 HTTP | 来源、候选、锚点、目录和章节同一确认；各份 ≤8 MiB | 可证明的原文可确认 | 书目不授予权限；证据不足拒绝 | 原件、分块和确认元组持久化 | 合成 TXT/PDF HTTP 及 §1 parser→独立 HTTP，中间树 PASS；政策/人员为合成 | NOT RUN |
| 模型上下文和选材 | 调研助手、写作 | 用固定快照并复核当前来源 | 选材返回确认文本 | 不混用实时文本，撤权拒绝 | 绑定资产修订、主张和章范围 | §3 快照与上下文中间树 PASS；源和模型为测试替身 | NOT RUN |
| 失效传播 | 目录、书目和卡片 HTTP | 已知撤稿/规则或关联变化影响对应卡 | 受影响卡需复核 | 恢复后需人工重确认 | 保留历史版本；审计失败回滚 | SQLite/HTTP 回归覆盖撤销、目录/撤稿和回滚；最终待跑 | NOT RUN |
| 确认、重放与提交后撤权 | 确认证据卡 HTTP | 锁内逐章/逐来源重鉴权 | 同一有效确认可安全重放 | 旧键不复活新版本；已提交不可读须如实说明 | 保留同键和历史，拒绝重复写入 | §3 多章确认中间树 PASS；提交后回归已编写 | NOT RUN |
| 评审报告 | 创建、读取、重放及拒绝 HTTP | 复验原报告来源、快照、版本 | 有权读取与顺序重放 | 撤权后拒绝不泄露；损坏/缺失拒绝读取 | 原记录复验；并发初始无记录仍可能重复调用模型 | §3 中间树 HTTP/竞态 PASS，含只读用户、损坏和撤权 | NOT RUN |
| 写作候选采纳 | 采纳 HTTP/application | 提交事务内重验权限、来源与证据 | 当前证据可采纳 | 不把快照当权限凭据 | 事务内重验及单连接 | 两项 `TestAcademicCandidateAdoption...` 已编写，最终树待跑 | NOT RUN |
| 迁移与资源锁 | SQLite / PostgreSQL | SQLite 000063、PG 000142 | 空表 down/up 成功 | 非空禁止降级删除 | 受测行并发更新等待释放 | 中间树 PG 17 实测及 §3 五项锁 PASS，不覆盖全部跨项目锁序 | NOT RUN |
| 调研面板和接口契约 | 用户界面 / OpenAPI | 明确确认后才写入；快照限制可见 | 显示确认时间、快照及回执 | 已提交不可读时同键恢复 | 同一页面保留原请求；切换身份清除 | 面板中间树 36/36 PASS，`npm run type-check` 中间树 PASS；响应契约中间树 PASS | NOT RUN |
| Crossref 与项目隔离 | 调研和来源读取 | provider 兼容、来源/项目/租户绑定 | 可用元数据可作为候选 | 不相关来源、撤权或撤稿拒绝 | 依赖当前目录、权限及证据版本 | 已有定向测试；合成书目不是论文原文验证 | NOT RUN |
| 完整真实上传与研究者 | 浏览器、上传/队列、写作 | 同一环境连续摄入及人工审核 | 全链路有可复查证据 | 不能将替身或分库结果当真实验收 | 生产对象存储与队列 | §1 未执行真实上传 HTTP/队列、生产存储或真实研究者 | NOT RUN |
| 最终 head / PR gate | 最终提交与 CI | 固定代码和工作树 | 当前头检查齐全 | 未通过不得 Ready | 迁移、重启、并发复验 | 工作树仍变；历史测试/CI 不能代表最终头 | NOT RUN |

## 1. 真实 PDF parser → 独立 HTTP fixture：中间树实测

代码基线 `a9945163` 加当时未提交工作区；没有完整工作区树摘要，不能等同最终 head。两段使用**两个独立的临时 SQLite 数据库**。第一段合成单页原生文本 PDF，真实 Go `convert` → 当前源码 DocReader PDFParser → Go chunker → `processChunks` → ChunkRepository → 内存 SQLite；文件存储和索引为测试替身，不走真实上传 HTTP 或队列。DocReader 使用 `wechatopenai/weknora-docreader:latest` 镜像依赖，只读挂载本次检出源码并运行 `/app/.venv/bin/python -m docreader.main`，仅绑定 `127.0.0.1` 动态端口。

第二段把第一段合成 capture 的原字节、chunk ID、摘要和页证明复制至另一 SQLite fixture，经 `httptest` 真路由验证关联确认、快照、写作选择与上下文，以及撤稿/目录变化、原件替换和权限拒绝。书目、目录、额度、存储和人工确认均为合成或测试替身。**它不是同库连续摄入、生产对象存储或真实研究者验收。**

当时执行的命令（端口与路径为本机隔离值；PowerShell 请先赋给 `$env:...`）：

```text
LINGDOC_TEST_DOCREADER_ADDR=127.0.0.1:<动态端口>
LINGDOC_TEST_PDF_CAPTURE=<同一个临时绝对路径>
go test -vet=off -count=1 -timeout=5m ./internal/application/service -run '^TestLiveNativePDFConvertChunkAndProof$'
go test -vet=off -count=1 -timeout=5m ./internal/lingdoc/workspace -run '^TestAcademicSnapshotConfirmationHTTPWithRealStorageBytes/native-text-pdf'
```

两条测试当时 PASS；两库关闭、专用 DocReader 容器停止并删除。Go 输出仅在当时工具终端，未保留独立日志；合成 capture 在本机 `D:\LingDocGoCache\build\issue682-native-pdf-capture.json`，不是仓内附件。最终 head 不继承这次 PASS。

此前另一套隔离 HTTP 上传、真实 Qwen 向量/关键词检索和 Edge 只读检查见 [PDF 页定位历史记录](Issue682-PDF页定位验收矩阵-20261029.md)：源码、前端构建和环境不同，不能与本次快照确认拼成同一次全链路。更早“不复制原件”的说明属于旧方案；当前方案保存有界历史原件和分块快照。

## 2. 真实 Qwen：独立合成链

显式设置 `LINGDOC_QWEN_RESEARCH_E2E=1`、私有 Qwen 配置和 DoH 后，`TestIssue682QwenIntegratedSyntheticChain` 曾在中间树真实调用 Qwen3.8-flash；本机 `D:\LingDocGoCache\build\issue682-qwen-isolated-doh-test.log` 的包级摘要为 PASS。独立合成项目使用模拟 Crossref、检索、政策与人工确认；研究提案经 HTTP，写作通过模型适配器；不是 PDF 联测或写作 HTTP/任务队列。默认不开关时是模型替身。

```text
LINGDOC_QWEN_RESEARCH_E2E=1
LINGDOC_QWEN_BASE_URL=<私有配置中的端点>
LINGDOC_QWEN_API_KEY=<仅私有环境注入，不写入仓库或日志>
LINGDOC_QWEN_DOH_URL=<显式配置的 HTTPS DNS>
LINGDOC_QWEN_DOH_BOOTSTRAP_IP=<显式配置的公网地址>
NO_PROXY=<按测试环境配置>
go test -vet=off -count=1 -timeout=5m ./internal/lingdoc/workspace -run '^TestIssue682QwenIntegratedSyntheticChain$'
```

此前直接网络尝试因配置/URL 校验失败，隔离 DoH 成功不抹掉该历史结果。调用详情和凭据不入仓；最终 head 真实模型复验 NOT RUN。

## 3. 中间树命令与门禁

```text
go test -vet=off ./internal/lingdoc/workspace -run 'TestAcademicReviewHTTPReplayRechecksStoredCardBeforeModel|TestAcademicReviewConcurrentReplayChecksActualStoredRecord|TestAcademicReviewHTTPRejectAfterRevocationReturnsRedactedReceipt|TestReviewReportHTTPLayer|TestLanguageReview' -count=1
go test -vet=off ./internal/lingdoc/workspace -run 'TestAcademicReviewHTTP|TestAcademicReviewConcurrentReplayChecksActualStoredRecord|TestReviewReportHTTPLayer|TestLanguageReview' -count=1
go test -vet=off ./internal/lingdoc/workspacecore -run 'TestAcademicSnapshot|TestAgentContext' -count=1
go test -vet=off ./internal/lingdoc/workspacecore -run '^TestPostgresAcademicSnapshotLocksBlockNativeStateChanges$' -count=1 -v
go test -vet=off ./internal/lingdoc/workspacecore -run '^TestAcademicSnapshotPostgresIsolationAndRollback$' -count=1
```

以上分别在中间树 PASS；PG 测试须显式设置隔离 `LINGDOC_TEST_POSTGRES_DSN`，未设置为 SKIP，不得计 PASS。五项资源锁验证所测更新等待事务释放，不代表所有跨项目交叉锁序无死锁。此前 PG 生产迁移链及单独 000142 down/up 在专用 PostgreSQL 17 容器完成并已清理；单独迁移 CLI 的完整脱敏命令未保留。HTTP 外部文件、数据库和网络不构成跨服务原子事务。

最终交付前须记录最终提交及工作树、该头定向检查、构建/lint、SQLite/PostgreSQL 与真实服务证据、CI、复审和健康评分。正式来源目录、额度和研究者确认仍是独立业务决策。PR 正文参见 [证据草案](Issue682-PR755-评审返工证据草案-20261030.md)。

## 4. 2026-10-30 工作树补充检查（提交与远端 CI 前）

在快照行结构守卫修复后，以下检查针对当时工作树通过；本节不替代最终提交的 CI 和同环境端到端验收：

- `go test -vet=off -count=1 -timeout=8m ./internal/database`：通过，包含 SQLite 迁移列和行结构守卫。
- `go test -vet=off -count=1 -timeout=8m ./internal/lingdoc/workspacecore ./internal/lingdoc/generation ./internal/lingdoc/candidateadoption`：通过。
- `go test -vet=off -count=1 -timeout=6m ./internal/lingdoc/workspace -run 'TestAcademic|TestAgentContext|TestResearchQwen|TestCrossref|Test.*EvidenceCard|Test.*SourceAnchor|Test.*Review|Test.*Rewrite'`：通过。
- 使用私有环境凭据与独立 DoH 再跑 `TestIssue682QwenIntegratedSyntheticChain`：真实 Qwen 调用通过；仍使用合成书目、资产、政策和人工确认，不证明同库 PDF 全链。
- 前端调研面板 36 项测试、两套 TypeScript 检查以及 Go vet 通过。完整 Python 检查曾受 Windows 沙箱的 Git Bash 权限限制；有关测试类在适当权限下单独通过，完整重跑结果以最终报告为准。本机没有 CI 版本的 golangci-lint。
- PostgreSQL 17 的快照迁移、回滚防护、并发锁与混合来源采纳曾在独立隔离环境通过，必须与当前提交的 CI 分别说明。

同环境真实上传/队列/PDF/模型链尚未形成通过证据。启动将私有源码挂载到未核验第三方 DocReader 镜像的操作被自动审批拒绝；宿主运行当前源码的替代方案正在检查。只有获得该链及当前提交必要门禁结果后，才能更新矩阵对应结论。

## 5. 2026-10-30 同环境真实服务补充验收

本节覆盖 §4 中“同环境真实上传/队列/PDF/模型链尚未形成通过证据”的当时状态。采用全新 SQLite、专用 Redis、宿主后端和当前 DocReader 源码（仅监听 loopback），在同一环境完成：

1. 经真实 HTTP 上传合成双页 PDF，队列使用当前 PDF 解析器产生并索引 6 个分块；向量及关键词检索各命中 6 个分块，错误页锚点返回 HTTP 400。
2. 以模拟 Crossref、测试目录与额度和模拟人工确认完成书目关联，使用正确页锚点创建证据卡并正式确认。数据库历史原件快照与上传文件逐字节相等，agent-context 成功读取。
3. 使用真实 `qwen3.8-flash` 写作任务成功，候选包含来源引用，模型调用记录的 `run_mode=real`；该次写作账本记录输入 957、输出 386 tokens。

合成 PDF、书目 DOI、目录、额度和人工确认均为测试资料；真实上传、队列、PDF 解析、快照和 Qwen 写作共用同一临时数据库。测试副本仅将 DocReader 限制为 loopback，并通过 Go overlay 注入测试目录、额度和 Crossref 模拟 transport；与生产网络书目和真实研究者验收有区别。关联首次请求误用段落 locator 返回 409，改用已验证 PDF 页锚点后通过；该 409 不作为产品故障或成功结果。

非 docs 的 Go、SQL、Python 文件与 go.mod/go.sum 共 3738 项（不含前端 TS/Vue），按测试脚本换行归一后的 SHA-256 为 `b2741387920980661fb83f31d120a1ef57e779bc4f3e1b139518510def96af27`，与测试副本一致；文档在捕获后补记，不计入该摘要。测试环境包含私有凭据和原始日志，不能作为公开附件。当前 PR 最终提交对应 CI、人工复审及正式业务目录/额度仍需各自判断，不能把本项通过扩展为生产验收通过。

另：在同一工作树使用完整权限、Git/Git Bash 2.56 和 D 盘 Python 3.13 单次运行 `python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'`，1012 项全部通过、无跳过。这是本地脚本结果，不替代最终提交的 GitHub CI；本机尚未执行 golangci-lint。

向量索引用真实远端 `qwen3.7-text-embedding-flash`，关键词查询使用隔离 SQLite 的真实检索实现；分别禁用另一种检索方式验证，均返回 6 个命中。测试结束后，专用后端、宿主 DocReader、Redis 和隔离网络已关闭，不影响其他项目容器。

## 6. PostgreSQL 与结构健康复评

在全新专用 PostgreSQL 17 上，当前工作树的生产迁移链行结构和全表列覆盖、快照隔离/回滚/非空降级保护、成员与章节授权、目录历史、资源锁和混合候选采纳/重放均通过。夹具为合成 TXT 及实际哈希；服务器不含向量扩展，迁移采用支持的 `app.skip_embedding=true`。专用容器和网络已删除，不影响其他项目。

```text
go test -vet=off -timeout=4m ./internal/database -run '^TestPostgresGuarded(RowShapesMatchTheVersionedMigrationChain|TablesCoverEveryVersionedMigrationColumn)$' -count=1 -v
go test -vet=off -timeout=4m ./internal/lingdoc/workspacecore -run '^(TestAcademicSnapshotPostgresIsolationAndRollback|TestPostgresAcademicSnapshotLocksBlockNativeStateChanges|TestPostgresAcademicAssociationMembershipRevocationLock|TestPostgresAcademicConfirmationChapterCitationFallback|TestPostgresAcademicEvidenceWritersSerializeOnProject|TestPostgresAcademicCatalogInvalidationPersistsHistory)$' -count=1 -v
go test -vet=off -timeout=4m ./internal/lingdoc/workspace -run '^TestPostgresAcademicMixedCandidateAdoption$' -count=1 -v
```

按项目健康评分协议进行提交前工作树复评：H_L 51.0/100、F_R 52.8/100，置信度 C；CodeGraph 不可用。主要风险为证据权限和不可变性验证分散在确认、上下文、写作、报告和采纳，事务锁顺序与结果语义跨层暴露，以及目录与额度配置仍需测试注入。顺序重放可避免再次调用模型，但并发初始请求仍可能各调用一次；旧报告未持久化的丢弃原因不伪造。分值不代表当前提交的 CI、真人研究者、正式政策或生产迁移已经验收，也不覆盖所有跨项目锁序。
