import { useEffect, useRef, useState } from 'react'
import { useLive } from '../lib/live-context'
import { InlineNotice } from './ReviewPrimitives'
type Target = { documentId: number; revision: number; anchor: unknown; focus: unknown; text: string; hostSessionId: string }
type Candidate = { id: string; status: string; target: Target; original: string; replacement: string; instruction: string; nativeReceipt?: { afterRevision: number }; citations?: Array<{ quote?: string }> }
type Result = { candidates: Candidate[]; candidate?: Candidate }
const labels: Record<string, string> = { generating: '正在生成', proposed: '候选 · 尚未写入', applying: '正在确认写入结果', applied: '已写入 · 待保存', saved: '已保存并核对版本', rejected: '已拒绝', stale: '输入已变化 · 请重新生成', failed: '生成失败', undone: '已撤销' }
function explain(message: string) {
  if (/objects|fields|markers|existing revisions|disabled/i.test(message)) return '此处包含对象、字段、修订或保护限制，请使用原生编辑；本次没有采用候选。'
  if (/stale|revision|document.*changed/i.test(message)) return '文稿已发生变化，请重新选择并核对候选；原有编辑保留。'
  if (/single-paragraph|cross-paragraph|cross-cell|UTF-8|nonempty selection/i.test(message)) return '这版仅支持单个段落内的普通文字，请重新选择范围。'
  if (/operation.*limit|receipt.*limit/i.test(message)) return '当前会话修改记录已达上限，请先保存文稿再重新打开。'
  return message
}
export function ControlledAssistant() {
  const live = useLive()
  const [target, setTarget] = useState<Target | null>(null)
  const [instruction, setInstruction] = useState('')
  const [sources, setSources] = useState<string[]>([])
  const [includeComment, setIncludeComment] = useState(false)
  const [consent, setConsent] = useState(false)
  const consentRevision = useRef<number | undefined>(undefined)
  const [history, setHistory] = useState<Candidate[]>([])
  const [selected, setSelected] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const lock = useRef(false)
  const pendingGenerate = useRef<Record<string, unknown> | null>(null)
  const [retryAvailable, setRetryAvailable] = useState(false)
  const candidatePanel = useRef<HTMLElement | null>(null)
  const invalidateRequest = () => { pendingGenerate.current = null; setRetryAvailable(false); setConsent(false) }
  const candidate = history.find(item => item.id === selected)
  const expectedRevision = candidate?.nativeReceipt?.afterRevision ?? candidate?.target.revision
  const documentChanged = !!candidate && (candidate.target.hostSessionId !== live.nativeSessionId || candidate.target.documentId !== live.native?.engine?.documentId || expectedRevision !== live.native?.engine?.revision)
  useEffect(() => { if (selected) candidatePanel.current?.scrollIntoView({ block: 'nearest' }) }, [selected, candidate?.status])
  const request = async (input?: Record<string, unknown>) => {
    const result = await live.workflow(input) as Result
    setHistory(result.candidates)
    if (result.candidate) setSelected(result.candidate.id)
    return result
  }
  useEffect(() => { let active = true; void live.workflow().then(value => { if (active) setHistory((value as Result).candidates) }).catch(cause => { if (active) setError(String(cause.message)) }); return () => { active = false } }, [live.workflow])
  const run = async (action: () => Promise<void>) => {
    if (lock.current) return
    lock.current = true; setBusy(true); setError('')
    try { await action() } catch (cause) {
      setError(cause instanceof Error ? cause.message : '操作未完成，请核对当前状态')
      // Read persisted outcome without resending an uncertain model/write request.
      try { const value = await live.workflow() as Result; setHistory(value.candidates) } catch { /* Original failure remains visible. */ }
    }
    finally { lock.current = false; setBusy(false) }
  }
  const capture = () => run(async () => {
    const value = await live.engine({ op: 'selection' }) as { state: { documentId: number; revision: number }; selection: { anchor: unknown; focus: unknown; text: string } }
    if (!value.selection?.text?.trim()) throw Error('请先在正文中选中一段内要修改的文字。')
    setTarget({ ...value.state, anchor: value.selection.anchor, focus: value.selection.focus, text: value.selection.text, hostSessionId: live.nativeSessionId })
    setConsent(false); setSelected(''); pendingGenerate.current = null; setRetryAvailable(false)
  })
  const generate = (retry = false) => run(async () => {
    if (!consent) throw Error('请重新确认本次发送内容。')
    if (consentRevision.current !== live.data?.revision) { setConsent(false); throw Error('资料或意见记录已变化，请核对并重新确认发送内容。') }
    if (!retry) {
      if (!target || !consent || !instruction.trim()) throw Error('请选定正文、填写修改目标并确认本次上下文。')
      pendingGenerate.current = { type: 'generate', key: crypto.randomUUID(), target, instruction, sourceVersionIds: sources, includeComment, contextConfirmed: true, expectedChainRevision: consentRevision.current }
    }
    if (!pendingGenerate.current) throw Error('没有待重试的请求')
    setRetryAvailable(true)
    await request(pendingGenerate.current)
    pendingGenerate.current = null; setRetryAvailable(false)
  })
  const accept = () => run(async () => {
    if (!candidate) return
    if (candidate.target.hostSessionId !== live.nativeSessionId) throw Error('文稿已重新打开，请重新选区并生成候选；历史记录仍保留。')
    const existing = await live.engine({ op: 'operationStatus', operationId: candidate.id })
    let receipt: Record<string, unknown>
    if (existing.status === 'applied' && existing.matchesCurrentRevision === true) receipt = existing
    else {
      const state = await live.engine({ op: 'status' })
      if (state.documentId !== candidate.target.documentId || state.revision !== candidate.target.revision) throw Error('文稿在生成候选后已变化，请重新选择并生成；你的新编辑没有被覆盖。')
      await request({ type: 'preflight', key: `${candidate.id}-preflight`, candidateId: candidate.id, target: candidate.target })
      receipt = await live.engine({ op: 'replaceRange', documentId: candidate.target.documentId, revision: candidate.target.revision, anchor: candidate.target.anchor, focus: candidate.target.focus, expectedText: candidate.original, text: candidate.replacement, operationId: candidate.id })
    }
    await request({ type: 'applied', key: `${candidate.id}-applied`, candidateId: candidate.id, receipt })
    live.notify('候选已写入选定位置。请核对正文，然后保存。')
  })
  const save = () => run(async () => {
    if (!candidate) return
    const saved = await live.save()
    if (saved?.phase !== 'saved' || !saved.saved) throw Error('尚未确认当前正文保存成功，请查看保存状态后重试。')
    const receipt = await live.engine({ op: 'operationStatus', operationId: candidate.id })
    await request({ type: 'saved', key: `${candidate.id}-saved-${saved.saved.goDiskVersion}`, candidateId: candidate.id, receipt, savedVersion: { diskVersion: saved.saved.goDiskVersion, docSha256: saved.saved.sha256, nativeDocumentId: saved.saved.nativeDocumentId, nativeRevision: saved.saved.nativeRevision } })
  })
  const undo = () => run(async () => {
    if (!candidate) return
    const receipt = await live.engine({ op: 'operationStatus', operationId: candidate.id })
    const undone = receipt.status === 'undone' && receipt.matchesCurrentRevision === true ? receipt
      : await live.engine({ op: 'undoOperation', operationId: candidate.id, documentId: candidate.target.documentId, expectedRevision: receipt.afterRevision })
    await request({ type: 'undone', key: `${candidate.id}-undone`, candidateId: candidate.id, receipt: undone })
    live.notify('本次修改已撤销；请按需保存撤销后的正文。')
  })
  const locate = () => run(async () => {
    if (!candidate || candidate.status !== 'proposed' || documentChanged) throw Error('原位置已变化，请重新选择；不会猜测位置。')
    await live.engine({ op: 'select', expectedDocumentId: candidate.target.documentId, expectedRevision: candidate.target.revision, range: { anchor: candidate.target.anchor, focus: candidate.target.focus } })
  })
  return <div className="assistant-panel controlled-writing" aria-busy={busy}>
    <div className="assistant-scroll">
      <h3>修改这一处</h3><p className="muted-copy">先明确要表达什么，再比较候选。支持普通段落内文字；复杂范围会提示重新选择。</p>
      {error && <div role="alert" className="controlled-error">{explain(error)}{explain(error) !== error && <details><summary>诊断信息</summary>{error}</details>}</div>}
      <button className="button secondary full" data-testid="cw-capture" disabled={busy} onClick={capture}>使用当前正文选区</button>
      {target && <section className="controlled-target"><strong>允许修改的文字</strong><blockquote>{target.text}</blockquote></section>}
      <label htmlFor="cw-instruction">你想怎么改？</label>
      <textarea disabled={busy} id="cw-instruction" data-testid="cw-instruction" rows={3} value={instruction} onChange={e => { setInstruction(e.target.value); invalidateRequest() }} placeholder="例如：保留原有数字，补清楚样本选择的依据。" />
      <div className="controlled-options" aria-label="快捷明确意图">{['只调整表达，保留事实和数字', '结合所选资料收窄结论', '按照所选人工意见修改'].map(text => <button disabled={busy} key={text} className="button secondary" onClick={() => { setInstruction(text); invalidateRequest() }}>{text}</button>)}</div>
      <fieldset disabled={busy}><legend>本次 AI 可读取</legend><p>选中的正文 + 上方修改要求</p>
        {(['v1', 'v2'] as const).filter(v => v === 'v1' || live.data?.changeApplied).map(v => <label key={v}><input type="checkbox" data-testid={`cw-source-${v}`} checked={sources.includes(v)} onChange={() => { setSources(items => items.includes(v) ? items.filter(item => item !== v) : [...items, v]); invalidateRequest() }} /> 联系记录 {v}<details><summary>查看原文</summary><p>{live.data?.sourceVersions[v].text}</p></details></label>)}
        <label><input type="checkbox" checked={includeComment} onChange={e => { setIncludeComment(e.target.checked); invalidateRequest() }} /> 当前人工意见</label>{includeComment && <blockquote>{live.data?.comment.text}</blockquote>}
        <label className="controlled-consent"><input data-testid="cw-context-confirm" type="checkbox" checked={consent} onChange={e => { setConsent(e.target.checked); consentRevision.current = live.data?.revision; if (!e.target.checked) { pendingGenerate.current = null; setRetryAvailable(false) } }} />确认将以上内容发送给已配置的模型</label>
        {sources.length > 0 && <p className="muted-copy">所选资料会登记项目内的模型使用许可；以后每次请求仍需明确选择上下文。</p>}
      </fieldset>
      <button className="button primary full" data-testid="cw-generate" disabled={busy || !target || !consent || !instruction.trim()} onClick={() => generate()}>{busy ? '正在处理…' : '生成候选，不修改正文'}</button>
      {retryAvailable && <button className="text-button" disabled={busy} onClick={() => generate(true)}>查询或重试原生成请求</button>}
      {candidate && <section ref={candidatePanel} className="controlled-candidate" data-testid="cw-candidate" data-status={candidate.status}>
        <h3 role="status">{labels[candidate.status] || candidate.status}</h3><p className="muted-copy">{candidate.instruction}</p>
        {documentChanged && <InlineNotice tone="warning">文稿已继续变化或重新打开。以下是历史修改记录，不能据此认定当前正文仍采用此结果。</InlineNotice>}
        {candidate.status === 'proposed' && <button className="text-button" disabled={busy || documentChanged} onClick={locate}>在原文中选亮修改位置</button>}
        <strong>修改前</strong><blockquote>{candidate.original}</blockquote>
        <strong>候选文本</strong><div className="controlled-replacement">{candidate.replacement || '尚无可采用的候选'}</div>
        {candidate.citations?.map((citation, index) => <details key={index}><summary>模型返回的依据 {index + 1}</summary><blockquote>{citation.quote}</blockquote></details>)}
        {['proposed', 'applying'].includes(candidate.status) && <div className="controlled-actions">{candidate.status === 'proposed' && <button className="button secondary" data-testid="cw-reject" disabled={busy} onClick={() => run(async () => { await request({ type: 'reject', key: `${candidate.id}-reject`, candidateId: candidate.id }) })}>拒绝</button>}<button className="button primary" data-testid="cw-accept" disabled={busy} onClick={accept}>{candidate.status === 'applying' ? '核对写入结果' : '接受此修改'}</button></div>}
        {['applied', 'saved'].includes(candidate.status) && <div className="controlled-actions"><button className="button secondary" data-testid="cw-undo" disabled={busy} onClick={undo}>撤销此次修改</button><button className="button primary" data-testid="cw-save" disabled={busy} onClick={save}>保存并核对版本</button></div>}
        <InlineNotice>采纳与意见处理完成分别记录。请核对实际正文，再在对应意见中登记判断。</InlineNotice>
      </section>}
      {history.length > 0 && <details className="controlled-history"><summary>修改记录（{history.length}）</summary>{[...history].reverse().map(item => <button className="text-button" key={item.id} onClick={() => setSelected(item.id)}>{labels[item.status] || item.status} · {item.instruction}</button>)}</details>}
    </div>
  </div>
}
