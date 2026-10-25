# 内容完整性对照（original vs formatted-review）

> 机器产物原样归档（2026-10-25 执行）：下列 `D:\wt\...` 是执行机上的路径，仅供溯源；两份文件的**权威身份是各自的 sha256**（原始导出件 `df5549b0…` 的机械检查见 BETA_STATUS §20.9；修复件 `5f4f598b…` 已提交入本目录，可独立核验）。

- 原始件：D:\wt\lingdoc-485-evidence\track-d-experiment\run-final-20261025-093259-c2345229\export-196b61e0de1242e6247ddb6bf2e91747.docx
  - sha256 = df5549b044261b6c30463e63cd4e639f0e7f59d116e27e6e429e571a3865639a
  - size = 3176 bytes
- 修复件：D:\wt\lingdoc-485-evidence\track-d-experiment\run-final-20261025-093259-c2345229\export-196b61e0de1242e6247ddb6bf2e91747-formatted-review.docx
  - sha256 = 5f4f598b930b5347f7767cc04cd395ed159e24cb5ebc432c40be9f58b650a0b3
  - size = 11833 bytes

- 对照项：43 项，PASS 43 / FAIL 0

## 逐项结论

| 结果 | 项 |
| --- | --- |
| PASS | P0 标题(Title) |
| PASS | P1 副标题(Subtitle) |
| PASS | P2 H1 研究问题 |
| PASS | P3 正文 P3 |
| PASS | P4 H1 研究方案 |
| PASS | P5 正文 P5 |
| PASS | P6 H1 引用资料 |
| PASS | P8 H1 待核事项与处理结论 |
| PASS | 引用来源行 |
| PASS | 引用行[List Bullet] 文档编号：SYNTH-RPT-C-01… |
| PASS | 引用行[List Bullet] 版本：v1.0（固定版本，供重复验收使用）… |
| PASS | 引用行[List Bullet] 数据性质：全合成数据。不含任何真实实验记录、真实… |
| PASS | 引用行[List Bullet] 合成日期：2026-04-18… |
| PASS | 引用行[List Bullet] 用途：为 LingDoc Beta 首版真实验收… |
| PASS | 引用行[Heading3] 1. 研究目的… |
| PASS | 引用行[正文] 本记录评估合成多孔氧化物复合薄膜的孔隙率与抗压强… |
| PASS | 引用行[Heading3] 2. 材料与方法… |
| PASS | 引用行[Heading4] 2.1 样品… |
| PASS | 引用行[正文] 合成基体为氧化物-氧化物复合体系，编号 PC-0… |
| PASS | 引用行[Heading4] 2.2 测试条件… |
| PASS | 引用行[List Bullet] 孔隙率：阿基米德排水法，每个样品平行测 3 次。… |
| PASS | 引用行[List Bullet] 抗压强度：准静态压缩，加载速率 0.5 mm/m… |
| PASS | 引用行[List Bullet] 所有结果均为合成数据，不做显著性检验。… |
| PASS | 引用行[Heading4] 2.3 数据处理… |
| PASS | P9 段0(类别) |
| PASS | P9 段1(待核事项) |
| PASS | P9 段2(处理结论) |
| PASS | P9 段3(理由) |
| PASS | P10 段0(类别) |
| PASS | P10 段1(待核事项) |
| PASS | P10 段2(处理结论) |
| PASS | P10 段3(理由) |
| PASS | P11 段0(类别) |
| PASS | P11 段1(待核事项) |
| PASS | P11 段2(处理结论) |
| PASS | P11 段3(理由) |
| PASS | 保护字符串×1 『仅供内部演示；并非正式申报材料…』 实际×1 |
| PASS | 保护字符串×1 『不把它视为真实科研结果…』 实际×1 |
| PASS | 保护字符串×1 『包含100条虚构记录 [1]。…』 实际×1 |
| PASS | 保护字符串×3 『处理：保留警示…』 实际×3 |
| PASS | 保护字符串×3 『理由：仅作内部演示，保留该项并随文件明确…』 实际×3 |
| PASS | 保护字符串×1 『定位：synth-chapter-c.m…』 实际×1 |
| PASS | 表格 4 行×3 列（实际 4×3） |

## 引用区 Markdown → Word 结构映射（仅去标记，文字未动）

| Word 元素 | 原始行（截断） |
| --- | --- |
| List Bullet | - 文档编号：SYNTH-RPT-C-01 |
| List Bullet | - 版本：v1.0（固定版本，供重复验收使用） |
| List Bullet | - 数据性质：全合成数据。不含任何真实实验记录、真实人员信息、真实机构名称或未公开数据。 |
| List Bullet | - 合成日期：2026-04-18 |
| List Bullet | - 用途：为 LingDoc Beta 首版真实验收提供「固定合成科研材料」，供资料接入、处 |
| Heading3 | ## 1. 研究目的 |
| 正文 | 本记录评估合成多孔氧化物复合薄膜的孔隙率与抗压强度之间的关系，并登记每一处结论的来源，供下游 |
| Heading3 | ## 2. 材料与方法 |
| Heading4 | ### 2.1 样品 |
| 正文 | 合成基体为氧化物-氧化物复合体系，编号 PC-01 至 PC-08。烧结温度设定为 1150 |
| Heading4 | ### 2.2 测试条件 |
| List Bullet | - 孔隙率：阿基米德排水法，每个样品平行测 3 次。 |
| List Bullet | - 抗压强度：准静态压缩，加载速率 0.5 mm/min，取 5 个平行样的算术平均。 |
| List Bullet | - 所有结果均为合成数据，不做显著性检验。 |
| Heading4 | ### 2.3 数据处理 |

## 新文档样式清单（非空段落按样式计数）

- (正文): 19
- ListParagraph: 8
- Heading1: 4
- Heading4: 3
- Heading3: 2
- Title: 1
- Subtitle: 1
- CiteSource: 1

## 表格内容

| 待核事项 | 处理结论 | 理由 |
| --- | --- | --- |
| 研究问题确认‘孔隙率每上升 5 个百分点，平均强度下降约 12.4 MPa’这一 | 处理：保留警示 | 理由：仅作内部演示，保留该项并随文件明确列出。 |
| 研究问题核实是否需要将‘后续验证需增加样本量与烧结温度对照’列为具体的研究子问题 | 处理：保留警示 | 理由：仅作内部演示，保留该项并随文件明确列出。 |
| 研究问题检查现有草稿（如有）是否已涵盖关于数据性质为全合成数据的声明，以避免重复 | 处理：保留警示 | 理由：仅作内部演示，保留该项并随文件明确列出。 |
