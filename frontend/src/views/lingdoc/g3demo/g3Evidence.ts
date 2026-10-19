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
import type { G3LifecycleSnapshot } from './runner/changeset'
import type { G3BackendHead, G3ChapterFact, G3RunContext, G3StepRecord } from './runner/runContext'

/** 一行判据要成为「本轮已判定」所必需的条件。 */
export interface G3RowEvidenceRule {
  /** 判据成立所必需、且本轮必须成功执行的流程步骤名。 */
  requires: readonly string[]
  /** 判据成立时该行应取的状态（只取仓库门禁词表内的值）。 */
  status: G3Status
  /** 证据说明。 */
  note: string
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

  const missingSteps = rule.requires.filter((name) => !observedSteps.includes(name))
  const covered = missingSteps.length === 0
  const decided = covered && !stale && ctx.head !== null

  return {
    rowId: row.id,
    covered,
    decided,
    observedSteps: rule.requires.filter((name) => observedSteps.includes(name)),
    missingSteps,
    stale,
    note: decided
      ? rule.note
      : stale
        ? `本轮已跑到该行所需步骤，但 head 已变化（${ctx.staleReason ?? '未知'}），结论过期，状态保持原样。`
        : `本轮未覆盖该行，还缺步骤：${missingSteps.join('、')}，状态保持原样。`,
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
    return {
      ...row,
      status: rule.status,
      evidence: rule.note,
      note: item.note,
      // 这一行既然已被本轮实跑判定，接它的「跟进 Issue」就不再是待办：
      // 保留它会让人以为还有未完成的事。
      followUpIssue: undefined,
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
  extra: { missingSections: string[]; lifecycle?: G3LifecycleSnapshot },
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
