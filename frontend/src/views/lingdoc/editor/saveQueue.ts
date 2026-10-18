// 各章独立 900ms CAS 保存队列（写入链版）。
// 关键不变式：同一章的写请求严格串行按序核销；A 在途时输入 B，A 成功只推进 revision，
// 绝不清掉 B 的脏标记/正文；B 在其后自动排队写出。迟响应永不覆盖新稿。
export type SaveState = 'clean' | 'dirty' | 'saving' | 'error' | 'conflict'

export interface ChapterSaveContext {
  chapterId: string
  expectedWorkingCopyRevision: number
}

export interface SaveResult {
  ok: boolean
  conflict?: boolean
  newWorkingCopyRevision?: number
  error?: string
}

interface DirtyEntry { body: string; seq: number }

export class ChapterSaveQueue {
  private delays = new Map<string, ReturnType<typeof setTimeout>>()
  private revisions = new Map<string, number>()
  private states = new Map<string, SaveState>()
  private dirty = new Map<string, DirtyEntry>()
  private seqs = new Map<string, number>()
  private chains = new Map<string, Promise<void>>()
  private listeners: Array<() => void> = []
  private paused = false

  constructor(
    private saveChapter: (ctx: ChapterSaveContext, body: string) => Promise<SaveResult>,
    private delayMs = 900,
  ) {}

  onChange(fn: () => void) { this.listeners.push(fn) }
  private emit() { for (const f of this.listeners) f() }

  setBaseline(chapterId: string, workingCopyRevision: number) {
    // 不降级：晚到的旧基线（如父级回执早于队列推进）不得把 CAS 基线往回拉
    this.revisions.set(chapterId, Math.max(this.revisions.get(chapterId) ?? 0, workingCopyRevision))
    if (!this.states.has(chapterId)) this.states.set(chapterId, 'clean')
  }

  state(chapterId: string): SaveState { return this.states.get(chapterId) ?? 'clean' }

  overallState(chapterIds: string[]): SaveState {
    let any = false
    for (const id of chapterIds) {
      const s = this.state(id)
      if (s !== 'clean') {
        any = true
        if (s === 'saving') return 'saving'
        if (s === 'conflict') return 'conflict'
        if (s === 'error') return 'error'
      }
    }
    return any ? 'dirty' : 'clean'
  }

  private bump(chapterId: string): number {
    const n = (this.seqs.get(chapterId) ?? 0) + 1
    this.seqs.set(chapterId, n)
    return n
  }

  // 编辑输入：标脏（带单调序号）并排队；与保存完成无关，保存端按序号核销
  markDirty(chapterId: string, body: string) {
    this.dirty.set(chapterId, { body, seq: this.bump(chapterId) })
    const s = this.states.get(chapterId)
    if (s !== 'saving') this.states.set(chapterId, 'dirty')
    this.emit()
    const old = this.delays.get(chapterId)
    if (old) clearTimeout(old)
    this.delays.set(chapterId, setTimeout(() => { void this.runChapter(chapterId) }, this.delayMs))
  }

  private scheduleLocked(chapterId: string) {
    void this.runChapter(chapterId)
  }

  private async runChapter(chapterId: string): Promise<void> {
    const existing = this.chains.get(chapterId)
    if (existing) return existing
    const p = (async () => {
      for (;;) {
        if (this.paused) { if (this.dirty.has(chapterId)) { this.states.set(chapterId, 'dirty'); this.emit() }; return }
        const entry = this.dirty.get(chapterId)
        if (!entry) return
        const expected = this.revisions.get(chapterId) ?? 0
        this.states.set(chapterId, 'saving')
        this.emit()
        let r: SaveResult
        try {
          r = await this.saveChapter({ chapterId, expectedWorkingCopyRevision: expected }, entry.body)
        } catch (e) {
          r = { ok: false, error: e instanceof Error ? e.message : String(e) }
        }
        if (r.ok) {
          this.revisions.set(chapterId, r.newWorkingCopyRevision ?? expected + 1)
          // 仅当没有更新的输入时才清脏；有 B 在则保留并继续循环写 B
          const latest = this.dirty.get(chapterId)
          if (latest && latest.seq === entry.seq) {
            this.dirty.delete(chapterId)
            this.states.set(chapterId, 'clean')
            this.emit()
            return
          }
          // seq 已更新（B 在途/A 期间有新输入）→ 继续循环写出最新稿
        } else {
          // 失败/409：当前输入保留（dirty 不动），状态如实；不再自动重试，等待显式 flush/新输入
          this.states.set(chapterId, r.conflict ? 'conflict' : 'error')
          this.emit()
          return
        }
      }
    })()
    this.chains.set(chapterId, p)
    try { await p } finally { this.chains.delete(chapterId) }
  }

  // Ctrl+S：取消定时器、立即写出全部脏章；返回可 await 的结果
  async flushAll(): Promise<{ ok: boolean; results: Map<string, SaveResult> }> {
    for (const t of this.delays.values()) clearTimeout(t)
    this.delays.clear()
    const ids = [...this.dirty.keys()]
    const results = new Map<string, SaveResult>()
    let ok = true
    for (const id of ids) {
      const before = this.revisions.get(id) ?? 0
      await this.runChapter(id)
      const after = this.revisions.get(id) ?? 0
      const failed = this.states.get(id) === 'error' || this.states.get(id) === 'conflict'
      results.set(id, failed
        ? { ok: false, conflict: this.states.get(id) === 'conflict' }
        : { ok: true, newWorkingCopyRevision: after })
      if (failed || after === before && this.dirty.has(id)) ok = false
      else if (this.states.get(id) !== 'clean') ok = false
      void before
    }
    return { ok, results }
  }

  async pauseAndWait(): Promise<void> {
    this.paused = true
    for (const t of this.delays.values()) clearTimeout(t)
    this.delays.clear()
    await Promise.all([...this.chains.values()])
  }
  dispose() { this.paused = true; for (const t of this.delays.values()) clearTimeout(t) }
}

// 确认/导出的诚实前置：本地脏章立即阻断（未过 900ms 也要阻断）。
export function blockReasonFor(queue: ChapterSaveQueue, chapterId: string, confirmed: boolean): string | null {
  const s = queue.state(chapterId)
  if (s === 'dirty' || s === 'saving' || s === 'error') return '当前稿有未保存修改，请先保存（Ctrl+S）后再确认/导出。'
  if (s === 'conflict') return '保存发生版本冲突（内容已被他人修改），当前输入已保留；请重新读取后处理。'
  if (confirmed && s === 'clean') return null
  return null
}
