/**
 * G3 验收台 · 真实证据与验收矩阵的接口。
 *
 * 这一层回答一个问题：**右栏矩阵的每一行，跟本轮真实证据是什么关系？**
 *
 * 守住的边界只有一条，但很重要：
 * **跑通前置步骤（身份、隔离项目、章节就绪）不构成任何一条 G3 判据的证据。**
 * 所以没被本轮覆盖的行，状态**原样保留**（切片行仍是 `TODO`，历史快照行仍是
 * `PASS (follow-up)` / `BLOCKED`），**绝不**因为前置链跑通就被判成 `PASS`。
 *
 * 「本轮是否足以判定该行」由规则表决定：规则要求的一批流程步骤**全部**在本轮
 * 成功执行、且本轮 head 未过期，才算判定。未判定就不改状态——宁可少说，也不要
 * 写出一个假的 `PASS`。
 */

import type { G3MatrixRow } from './g3Matrix'
import type { G3Status } from './g3Status'
import type { G3BlockedSnapshot } from './runner/blocked'
import type { G3LifecycleSnapshot } from './runner/changeset'
import type { G3DependencySnapshot } from './runner/dependencies'
import type { G3ModelSnapshot } from './runner/model'
import type { G3StabilizationSnapshot } from './runner/stabilization'
import type {
  G3BackendHead,
  G3ChapterFact,
  G3RunContext,
  G3RunVerdict,
  G3StepRecord,
} from './runner/runContext'

/** 一行判据要成为「本轮已判定」所必需的条件。 */
export interface G3RowEvidenceRule {
  /** 判据成立所必需、且本轮必须成功执行的流程步骤名。 */
  requires: readonly string[]
  /** 判据成立时该行应取的状态（只取仓库门禁词表内的值）。 */
  status: G3Status
  /** 证据说明。 */
  note: string
  /**
   * 本轮判定卡里对应的判据编号。
   *
   * 给了它，就**以判定结论为准**：步骤跑到了但判定不成立时，这一行仍然不算判定，
   * 状态保持原样——「步骤跑到了」不等于「判据成立」。
   */
  verdictId?: string
  /**
   * 承接未实现那半段的独立 GitHub Issue。
   *
   * 部分覆盖的判据用它指向缺口清单（例如 #246）；给了它，页面上这一行就保留
   * 这个跟进 Issue，而不是像完全判定那样清空。
   */
  followUpIssue?: number
  /**
   * **另一条同样能判定这一行的路径**。
   *
   * 同一行可能有多条独立证据路径：例如 AC-G3-14 既可以由「确定性链」判定
   * （三态裁决那半段），也可以由「真实模型链」判定（模型侧那半段）。
   * 哪条路径本轮跑到了、且它的判定卡成立，这一行就按哪条路径的状态与说明落。
   */
  alternatives?: readonly G3RowEvidenceAlternative[]
}

/** 另一条判定路径。 */
export interface G3RowEvidenceAlternative {
  /** 该路径成立所必需、且本轮必须成功执行的流程步骤名。 */
  requires: readonly string[]
  /** 该路径成立时该行应取的状态。 */
  status: G3Status
  /** 该路径的证据说明。 */
  note: string
  /** 该路径的判定卡编号。 */
  verdictId?: string
  /** 该路径承接未实现那半段的独立 GitHub Issue。 */
  followUpIssue?: number
}

/**
 * 规则表。
 *
 * 每一行写清「这条判据要成立，本轮必须**成功执行**哪些步骤」。
 * 补进来的那一刻，对应行才会被本轮证据驱动；规则表之外的行状态**原样保留**。
 *
 * 步骤名取自 `runner/changeset.ts` 的 `G3_CS_STEPS`——两边共用同一份常量，
 * 名字对不上时矩阵那几行会退回「未被本轮覆盖」，而不是给出假的 `PASS`。
 */
export const G3_ROW_EVIDENCE_RULES: Readonly<Record<string, G3RowEvidenceRule>> = Object.freeze({
  'AC-G3-01': {
    requires: [
      '创建变更集',
      '评估变更集',
      '读影响评估',
      '应用变更集',
      '读上下文修订',
      '读章节版本列表',
      '复写章节正文',
      '重新确认章节',
    ],
    status: 'PASS',
    note:
      '本轮实跑判定成立：Owner 应用后推进上下文修订，受影响章节的确认失效并进入待复核，' +
      '复写后按当前规格修订与当前章节版本重新确认（证据见左栏与「变更集生命周期」判定卡）。',
  },
  'AC-G3-02': {
    requires: [
      '创建并发变更集',
      '应用并发变更集',
      '读回陈旧变更集',
      '读上下文修订',
      '读影响任务',
    ],
    status: 'PASS',
    note:
      '本轮实跑判定成立：两个变更集同基于一个上下文修订，先应用的推进修订，' +
      '后应用的返回 409 并落为 STALE，未产生新上下文修订、也未产生影响任务。',
  },
  'AC-G3-04': {
    requires: ['创建变更集', '读影响评估', '应用变更集', '读章节版本列表', '读影响任务'],
    status: 'PASS',
    note:
      '本轮实跑判定成立：未列入受影响范围的章节不在影响评估的对象与影响里，' +
      '没有为它产生影响任务，其章节版本的确认标记未被本次变更集置为失效；' +
      '未受影响理由按响应如实呈现（服务端未给出理由时标注「服务端未提供未受影响理由」）。',
  },
  'AC-G3-11': {
    requires: ['创建逆向变更集', '读回原变更集', '读逆向影响评估', '应用逆向变更集'],
    status: 'PASS',
    note:
      '本轮实跑判定成立：撤销经由新建的逆向变更集完成（原变更集历史仍可读），' +
      '逆向变更集重新评估后应用，项目指针没有静默回退。',
  },
  'AC-G3-05': {
    requires: ['建立已确认依赖边', '读影响评估', '应用变更集', '读影响任务', '重复应用变更集'],
    verdictId: 'AC-G3-05',
    status: 'PASS',
    note:
      '本轮实跑判定成立：同一变更集里两条来源边指向同一目标、动作相同，' +
      '服务端把它们合并成一条逻辑 ImpactTask，causes 保留两条上游原因；' +
      '重复应用同一变更集后影响任务数量不变，没有产生第二条任务。',
  },
  'AC-G3-13': {
    requires: ['建立建议依赖边', '读影响评估', '应用变更集', '读章节版本列表', '读影响任务'],
    verdictId: 'AC-G3-13',
    status: 'PASS',
    note:
      '本轮实跑判定成立：建议边（status=suggested）只产生「可能受影响」的影响，' +
      '没有为它的目标章节产生影响任务，该章节的版本级确认标记仍为 true——' +
      '建议边不单独使章节失效，也不构成导出阻断项。',
  },
  'AC-G3-06': {
    requires: ['模型-旧异步-结论'],
    verdictId: 'AC-G3-06',
    status: 'WARNING',
    note:
      '本轮由**真实模型**驱动（非确定性，结论不保证逐字复现）：revision 4 的生成运行在途时，' +
      '变更集把项目推进到 revision 5；旧运行返回后只能落成**历史候选**，' +
      '新修订的章节、确认与交付快照在返回前后一致。模型运行标识与耗时见判定卡；' +
      '结论依赖真实模型，因此状态记为 WARNING，与确定性结论分列展示。',
  },
  'AC-G3-14': {
    requires: ['创建变更草稿'],
    verdictId: 'AC-G3-14',
    status: 'PASS (follow-up)',
    followUpIssue: 246,
    note:
      '本轮实跑的是**生成那半段**：助手变更草稿已生成（status=draft）。' +
      '**未实现那半段**：草稿里的「确定受影响 / 可能受影响」分组解释没有实现' +
      '（工作台那个分组是影响解析结果，不可互替），缺口清单见 #246；' +
      '因此本行不是完全判定，状态记为 PASS (follow-up)。',
    alternatives: [
      {
        requires: ['模型-助手草稿-结论'],
        verdictId: 'AC-G3-14',
        status: 'WARNING',
        followUpIssue: 246,
        note:
          '本轮由**真实模型**判定（模型侧，非确定性）：助手把自然语言整理成变更草稿（status=draft），' +
          '并且有**否定性证据**——没有直接应用变更、没有关闭人工任务、没有放行导出。' +
          '**未实现那半段**：助手草稿里的「确定受影响 / 可能受影响」解释分组仍然没有实现，' +
          '缺口清单见 #246。结论依赖真实模型，状态记为 WARNING，与确定性结论分列展示；' +
          '模型运行标识与耗时见判定卡。',
      },
    ],
  },
  'AC-G3-15': {
    requires: ['裁决依赖边-确认', '裁决依赖边-修改', '裁决依赖边-拒绝', '重投被拒绝的候选边'],
    verdictId: 'AC-G3-15',
    status: 'PASS (follow-up)',
    followUpIssue: 246,
    note:
      '本轮实跑的是**三态裁决那半段**：确认 / 修改 / 拒绝都跑通，被拒绝的候选在同一版本里' +
      '再投一次不会新建边。**未实现那半段**：AI 批量提出候选依赖没有端点，缺口清单见 #246；' +
      '因此本行不是完全判定，状态记为 PASS (follow-up)。',
    alternatives: [
      {
        requires: ['模型-候选依赖-结论'],
        verdictId: 'AC-G3-15',
        status: 'WARNING',
        followUpIssue: 246,
        note:
          '本轮由**真实模型**判定（模型侧，非确定性）：候选依赖由真实模型**批量提出**，' +
          '页面对其确认 / 修改 / 拒绝三态分别落证，被拒候选在同一版本里再投不会新建边。' +
          '**未实现那半段**：候选的「对象版本 / 依据片段 / 建议理由」没有独立的结构化展示位，' +
          '缺口清单见 #246。结论依赖真实模型，状态记为 WARNING，与确定性结论分列展示；' +
          '模型运行标识与耗时见判定卡。',
      },
    ],
  },
  'AC-G3-09': {
    requires: [
      '阻断-交付检查',
      '阻断-接受不处理规则问题',
      '阻断-处置规则问题',
      '阻断-重检交付检查',
      '阻断-读影响任务',
      '阻断-稳定化（规则问题处置后）',
    ],
    verdictId: 'AC-G3-09',
    status: 'PASS (follow-up)',
    followUpIssue: 246,
    note:
      '本轮实跑判定成立（部分覆盖）：规则问题被人工处置之后，**对应的 ImpactTask 仍为 OPEN**、' +
      '稳定化仍为 BLOCKED，项目仍然不能稳定——「规则问题与任务分离」这半段成立。' +
      '**未实现那半段**：「问题不再阻断当次求值」在当前后端上不成立（人工处置只是工作流元数据，' +
      '求值结论不变）；且对阻断级规则问题调用「接受不处理」（dismiss）当前返回 500，' +
      '缺口清单见 #246；因此本行不是完全判定，状态记为 PASS (follow-up)。',
  },
  'AC-G3-12': {
    requires: [
      '阻断-替换目标对象版本',
      '阻断-稳定化（版本不匹配）',
      '阻断-重投变更集',
      '阻断-重投-应用变更集',
      '阻断-稳定化（重投闭环后）',
    ],
    verdictId: 'AC-G3-12',
    status: 'PASS',
    note:
      '本轮实跑判定成立：目标对象在任务「已完成」之后被新版本替换，稳定化重新读取时发现版本不匹配，' +
      '这一版修订**没有被放行**（服务端在重新读取闭环证据时按 409 version_conflict 拒掉这一次稳定化）；' +
      '随后按新基线重投了对应影响任务，重投任务闭环之后稳定化转为已稳定。',
  },
  'AC-G3-07': {
    requires: [
      '阻断-人工复核探针-应用变更集',
      '阻断-人工复核探针-缺少人工复核完成',
      '阻断-人工复核探针-带人工复核完成',
    ],
    verdictId: 'AC-G3-07',
    status: 'PASS (follow-up)',
    followUpIssue: 243,
    note:
      '本轮实跑的是**人工复核那半段**：研究主张上的复核任务缺少 human_decision 时被服务端拒掉' +
      '（422 invalid_state），带上人工复核决策之后才允许闭环——异步作业成功关不掉人工复核任务。' +
      '**未覆盖那半段**：「证据重检返回新候选」需要真实模型，造证据卡要走模型侧（见 #243）；' +
      '因此本行不是完全判定，状态记为 PASS (follow-up)。',
  },
  'G3.1': {
    requires: [
      '稳定化-创建变更集',
      '稳定化-应用变更集',
      '稳定化-应用过期基线变更集',
      '稳定化-读回过期变更集',
      '稳定化-读上下文修订',
      '稳定化-读影响任务',
    ],
    verdictId: 'G3.1',
    status: 'PASS',
    note:
      '本轮实跑判定成立：两个变更集基于同一个上下文修订，先应用的推进修订，' +
      '后应用的被服务端按 409 拒掉并落为 STALE；被拒之后上下文修订条数与影响任务条数都没有变化，' +
      '没有半完成写入（证据见左栏「稳定化-应用过期基线变更集」等步骤与「G3.1」判定卡）。',
  },
  'G3.6': {
    requires: [
      '稳定化-稳定化（阻断态）',
      '稳定化-稳定化',
      '稳定化-交付检查',
      '稳定化-冻结交付快照',
      '稳定化-发起导出',
      '稳定化-轮询导出产物',
      '稳定化-下载导出产物',
    ],
    verdictId: 'G3.6',
    status: 'PASS',
    note:
      '本轮实跑判定成立：阻断影响未闭环时稳定化以 200 成功响应返回 BLOCKED 并带完整阻断原因，' +
      '此时冻结交付快照被服务端拒掉（不能导出）；全部阻断影响闭环后稳定化返回 STABILIZED，' +
      '交付检查通过、快照冻结、导出产物 verified 并按字节与哈希核对一致' +
      '（证据见左栏与「G3.6」判定卡）。',
  },
})

/** 本轮对某一行的覆盖情况。 */
export interface G3RowEvidence {
  rowId: string
  /** 本轮是否覆盖到这一行。 */
  covered: boolean
  /** 本轮是否**足以判定**这一行。 */
  decided: boolean
  /** 与这一行相关、且本轮成功跑过的步骤。 */
  observedSteps: string[]
  /** 判定这一行还缺的步骤。 */
  missingSteps: string[]
  /** 本轮 head 已变化，既有结论过期。 */
  stale: boolean
  /** 给读者看的一句话。 */
  note: string
}

/** 本轮证据在矩阵上的投影。 */
export interface G3MatrixWithRun {
  rows: G3MatrixRow[]
  evidence: G3RowEvidence[]
}

/** 一行判据的各条判定路径的评估结果。 */
interface G3GroupEvaluation {
  group: G3RowEvidenceAlternative
  missingSteps: string[]
  covered: boolean
  verdict: G3RunVerdict | undefined
  verdictMissing: boolean
  verdictFailed: boolean
  decided: boolean
}

/** 评估一行判据的全部判定路径。 */
function evaluateGroups(
  ctx: G3RunContext,
  rule: G3RowEvidenceRule,
  observedSteps: readonly string[],
): G3GroupEvaluation[] {
  const groups: G3RowEvidenceAlternative[] = [
    {
      requires: rule.requires,
      status: rule.status,
      note: rule.note,
      verdictId: rule.verdictId,
      followUpIssue: rule.followUpIssue,
    },
    ...(rule.alternatives ?? []),
  ]

  return groups.map((group) => {
    const missingSteps = group.requires.filter((name) => !observedSteps.includes(name))
    const covered = missingSteps.length === 0
    // 判定结论优先：规则给了 verdictId 时，**步骤跑到了也不等于判定成立**。
    const verdict = group.verdictId
      ? (ctx.verdicts ?? []).find((item) => item.id === group.verdictId)
      : undefined
    const verdictMissing = Boolean(group.verdictId) && !verdict
    const verdictFailed = Boolean(group.verdictId) && verdict !== undefined && !verdict.holds
    // 判定卡自带状态时（例如模型不可用记 NOT RUN），以判定卡为准。
    const verdictStatus = verdict?.status
    const decided =
      covered &&
      !ctx.stale &&
      ctx.head !== null &&
      !verdictMissing &&
      (!verdictFailed || verdictStatus !== undefined)
    return { group, missingSteps, covered, verdict, verdictMissing, verdictFailed, decided }
  })
}

/** 计算一行在本轮里的覆盖情况。 */
export function rowEvidence(ctx: G3RunContext, row: G3MatrixRow): G3RowEvidence {
  const rule = G3_ROW_EVIDENCE_RULES[row.id]
  const observedSteps = ctx.steps.filter((step) => step.ok).map((step) => step.name)
  const stale = ctx.stale

  if (!rule) {
    return {
      rowId: row.id,
      covered: false,
      decided: false,
      observedSteps,
      missingSteps: [],
      stale,
      note: stale
        ? '本轮未覆盖该行；且本轮 head 已变化，既有结论过期。'
        : '本轮未覆盖该行：前置链的证据不构成这一条的判据证据，状态保持原样。',
    }
  }

  const evaluations = evaluateGroups(ctx, rule, observedSteps)
  const hit = evaluations.find((item) => item.decided) ?? evaluations.find((item) => item.covered) ?? evaluations[0]!

  return {
    rowId: row.id,
    covered: hit.covered,
    decided: hit.decided,
    observedSteps: hit.group.requires.filter((name) => observedSteps.includes(name)),
    missingSteps: hit.missingSteps,
    stale,
    note: hit.decided
      ? hit.verdict?.detail
        ? `${hit.group.note}｜${hit.verdict.detail}`
        : hit.group.note
      : stale
        ? `本轮已跑到该行所需步骤，但 head 已变化（${ctx.staleReason ?? '未知'}），结论过期，状态保持原样。`
        : hit.missingSteps.length > 0
          ? `本轮未覆盖该行，还缺步骤：${hit.missingSteps.join('、')}，状态保持原样。`
          : hit.verdictFailed
            ? `本轮已跑到该行所需步骤，但判定卡「${hit.group.verdictId}」不成立，状态保持原样。`
            : hit.verdictMissing
              ? `本轮已跑到该行所需步骤，但本轮没有「${hit.group.verdictId}」的判定结论，状态保持原样。`
              : '本轮未覆盖该行，状态保持原样。',
  }
}

/**
 * 把本轮证据投到矩阵上。
 *
 * 返回的行**状态**：被本轮判定的行取规则指定状态；其余行**原样保留**。
 */
export function applyRunEvidence(rows: readonly G3MatrixRow[], ctx: G3RunContext): G3MatrixWithRun {
  const evidence = rows.map((row) => rowEvidence(ctx, row))
  const byId = new Map(evidence.map((item) => [item.rowId, item]))
  const nextRows = rows.map((row) => {
    const item = byId.get(row.id)
    const rule = G3_ROW_EVIDENCE_RULES[row.id]
    if (!item || !item.decided || !rule) {
      return row
    }
    const observedSteps = ctx.steps.filter((step) => step.ok).map((step) => step.name)
    const evaluations = evaluateGroups(ctx, rule, observedSteps)
    const hit = evaluations.find((entry) => entry.decided) ?? evaluations[0]!
    // 状态：判定卡自带状态时以判定卡为准，否则取命中路径的状态。
    const status = hit.verdict?.status ?? hit.group.status
    return {
      ...row,
      status,
      evidence: hit.group.note,
      note: item.note,
      // 完全判定的行：接它的「跟进 Issue」不再是待办，保留会让人以为还有未完成的事；
      // 部分覆盖的行：跟进 Issue 指向缺口清单（例如 #246），必须留着。
      followUpIssue: hit.group.followUpIssue,
    }
  })
  return { rows: nextRows, evidence }
}

/** 一次运行的摘要（可落盘，供下次运行比对 head）。 */
export interface G3RunSummary {
  runId: string
  startedAt: string
  accountEmail: string
  head: G3BackendHead | null
  projectId?: string
  projectName?: string
  chapters: G3ChapterFact[]
  steps: G3StepRecord[]
  stale: boolean
  staleReason?: string
  missingSections: string[]
  /** 变更集生命周期链的页面快照（没有跑过就没有这一项）。 */
  lifecycle?: G3LifecycleSnapshot
  /** 依赖图与影响任务链的页面快照（没有跑过就没有这一项）。 */
  dependency?: G3DependencySnapshot
  /** 稳定化与交付闭环链的页面快照（没有跑过就没有这一项）。 */
  stabilization?: G3StabilizationSnapshot
  /** 稳定化阻断与恢复链的页面快照（没有跑过就没有这一项）。 */
  blocked?: G3BlockedSnapshot
  /** 真实模型链的页面快照（没有跑过就没有这一项）。 */
  model?: G3ModelSnapshot
  /** 本轮跑出来的判据判定（矩阵规则表按 verdictId 取结论）。 */
  verdicts?: G3RunVerdict[]
}

/** 运行摘要的本地存储键。 */
export const G3_RUN_SUMMARY_STORAGE_KEY = 'g3demo_last_run'

/** 浏览器本地存储的最小接口。 */
export interface G3RunStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

/** 把运行上下文压成摘要。 */
export function summarizeRun(
  ctx: G3RunContext,
  extra: {
    missingSections: string[]
    lifecycle?: G3LifecycleSnapshot
    dependency?: G3DependencySnapshot
    stabilization?: G3StabilizationSnapshot
    blocked?: G3BlockedSnapshot
    model?: G3ModelSnapshot
  },
): G3RunSummary {
  return {
    runId: ctx.runId,
    startedAt: ctx.startedAt,
    accountEmail: ctx.accountEmail,
    head: ctx.head,
    projectId: ctx.projectId,
    projectName: ctx.projectName,
    chapters: ctx.chapters,
    steps: ctx.steps,
    stale: ctx.stale,
    staleReason: ctx.staleReason,
    missingSections: extra.missingSections,
    lifecycle: extra.lifecycle,
    dependency: extra.dependency,
    stabilization: extra.stabilization,
    blocked: extra.blocked,
    model: extra.model,
    verdicts: ctx.verdicts,
  }
}

/** 保存运行摘要。 */
export function saveRunSummary(storage: G3RunStorage, summary: G3RunSummary): void {
  storage.setItem(G3_RUN_SUMMARY_STORAGE_KEY, JSON.stringify(summary))
}

/** 读取上一次运行摘要。 */
export function loadRunSummary(storage: G3RunStorage): G3RunSummary | null {
  const raw = storage.getItem(G3_RUN_SUMMARY_STORAGE_KEY)
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as Partial<G3RunSummary>
    if (!parsed.runId || !Array.isArray(parsed.steps)) return null
    return parsed as G3RunSummary
  } catch {
    return null
  }
}

/** 把运行摘要还原成运行上下文，好让矩阵能拿它算覆盖。 */
export function contextFromSummary(summary: G3RunSummary): G3RunContext {
  return {
    runId: summary.runId,
    startedAt: summary.startedAt,
    accountEmail: summary.accountEmail,
    steps: summary.steps,
    head: summary.head,
    projectId: summary.projectId,
    projectName: summary.projectName,
    chapters: summary.chapters,
    stale: summary.stale,
    staleReason: summary.staleReason,
    verdicts: summary.verdicts,
  }
}

/**
 * 后端 head 变了没有。
 *
 * head 变了 → 上一次运行的结论**过期**：证据是在另一个代码版本上跑出来的，
 * 不能继续当作当前 head 的证据。
 */
export function headChangedSince(
  previous: G3RunSummary | null,
  current: G3BackendHead | null,
): { changed: boolean; reason: string } {
  if (!previous || !previous.head || !current) {
    return { changed: false, reason: '' }
  }
  if (previous.head.commitId === current.commitId) {
    return { changed: false, reason: '' }
  }
  return {
    changed: true,
    reason: `后端 head 从 ${previous.head.commitId} 变为 ${current.commitId}，上一次运行的结论已过期。`,
  }
}
