import type { Asset, Source } from '@/api/lingdoc/workspace'
import type { KnowledgeTraceNode } from '@/utils/knowledgeTrace'

export interface MaterialKnowledge {
  id: string
  knowledge_base_id: string
  title?: string
  file_name?: string
  parse_status: string
  error_message?: string
}
export interface MaterialTrace {
  knowledge_id: string
  current_attempt: number
  latest_attempt: number
  parse_status: string
  trace?: KnowledgeTraceNode & { attempt?: number; knowledge_id?: string }
}
export interface MaterialReference { id: string; kb: string; key: string }
export interface MaterialRow {
  ref: MaterialReference
  knowledge?: MaterialKnowledge
  asset?: Asset
  indexReady: boolean
  label: string
  readError: string
  actionError: string
  notice: string
  busy: boolean
  query: string
  hits: Source[]
}
export interface MaterialObservation { knowledge: MaterialKnowledge; trace: MaterialTrace; assets: Asset[] }
export interface MaterialPorts {
  upload(kb: string, file: File, project: string, options?: unknown): Promise<{ knowledge: MaterialKnowledge; reused: boolean }>
  observe(project: string, reference: MaterialReference): Promise<MaterialObservation>
  bind(project: string, knowledge: string, key: string): Promise<Asset>
  search(project: string, query: string, asset: string): Promise<Source[]>
  newKey(): string
}
export interface MaterialStorage { getItem(key: string): string | null; setItem(key: string, value: string): void }

/** Only real, current-attempt stage rows prove indexing. The spans API also
 * synthesizes "done" placeholders from parse_status; those have no span_id. */
export function materialReadiness(knowledge: MaterialKnowledge, trace: MaterialTrace): { ready: boolean; label: string } {
  switch (knowledge.parse_status) {
    case 'pending': case 'unprocessed': return { ready: false, label: '等待处理' }
    case 'processing': case 'finalizing': return { ready: false, label: '正在处理' }
    case 'failed': return { ready: false, label: '处理失败：请检查解析或索引配置后重新处理' }
    case 'cancelled': return { ready: false, label: '处理已取消' }
    case 'completed': break
    default: return { ready: false, label: '处理状态未知，请重新读取' }
  }
  const attempt = trace.current_attempt
  if (trace.knowledge_id !== knowledge.id || !Number.isInteger(attempt) || attempt <= 0 ||
      attempt !== trace.latest_attempt || trace.parse_status !== 'completed') {
    return { ready: false, label: '解析完成；当前索引状态无法确认' }
  }
  const stages = trace.trace?.children?.filter(node => node.name === 'embedding' && node.kind === 'stage') ?? []
  if (stages.length !== 1) return { ready: false, label: '解析完成；缺少唯一索引阶段记录' }
  const stage = stages[0] as KnowledgeTraceNode & { attempt?: number; knowledge_id?: string }
  if (!stage.span_id || stage.attempt !== attempt || stage.knowledge_id !== knowledge.id) {
    return { ready: false, label: '解析完成；缺少真实的当前索引记录' }
  }
  if (stage.status === 'failed') return { ready: false, label: '解析完成，但索引失败；尚不可检索。' +
    (stage.error_message ? '原因：' + stage.error_message : '请检查资料库索引与模型配置后，由有权限人员重新处理。') }
  if (stage.status !== 'done') return { ready: false, label: '解析完成；索引未完成或未启用' }
  return { ready: true, label: '处理完成（当前索引阶段已完成）' }
}

function message(error: unknown): string {
  const item = error as { message?: string; error?: { message?: string } }
  return item?.error?.message || item?.message || '请求失败，请重新读取后重试。'
}
function validRef(value: unknown): value is MaterialReference {
  if (!value || typeof value !== 'object') return false
  return ['id', 'kb', 'key'].every(key => {
    const v = (value as Record<string, unknown>)[key]
    return typeof v === 'string' && v.length > 0 && v.length <= 512 && !/[\x00-\x1f]/.test(v)
  })
}
function rowFor(ref: MaterialReference): MaterialRow {
  return { ref, indexReady: false, label: '正在重新核实材料', readError: '', actionError: '', notice: '', busy: false, query: '', hits: [] }
}

/** Owns upload/read/bind recovery for ONE immutable user/tenant/project.
 * Browser persistence is only an intent/reference cache, never authorization
 * or proof that the server bound the material to the project. */
export class ProjectMaterialIntake {
  rows: MaterialRow[] = []
  uploading = false
  uploadError = ''
  storageWarning = ''
  private active = true
  private sequences = new Map<string, number>()
  private storageKey: string

  constructor(readonly project: string, identity: string, private ports: MaterialPorts, private storage?: MaterialStorage) {
    this.storageKey = 'lingdoc:material-intake:v1:' + JSON.stringify([identity, project])
    if (!storage) this.storageWarning = '本机存储不可用；材料引用仅在本次页面中保留，刷新后需重新选择文件。'
    try {
      const raw: unknown = JSON.parse(storage?.getItem(this.storageKey) || '[]')
      if (!Array.isArray(raw)) throw Error('Invalid saved references')
      const refs = raw.filter(validRef).slice(0, 20)
      this.rows = refs.filter((ref, i) => refs.findIndex(item => item.id === ref.id) === i).map(rowFor)
    } catch { this.storageWarning = '本机材料记录无法恢复；请重新选择文件，由服务端核实重复材料。' }
  }
  dispose() { this.active = false; this.rows.forEach(row => { row.hits = []; row.knowledge = undefined; row.indexReady = false }) }
  forget(row: MaterialRow) {
    if (!this.active || row.busy || this.uploading) return
    this.sequences.set(row.ref.id, (this.sequences.get(row.ref.id) || 0) + 1)
    row.hits = []
    row.knowledge = undefined
    this.rows = this.rows.filter(item => item !== row)
    this.persist()
  }
  private persist() {
    try { this.storage?.setItem(this.storageKey, JSON.stringify(this.rows.map(row => row.ref))) }
    catch { this.storageWarning = '本机无法保存材料引用；刷新后需重新选择文件，不会自动重传。' }
  }
  async upload(kb: string, file: File, options?: unknown) {
    if (!this.active || this.uploading) return
    this.uploadError = ''
    if (!kb || !this.project || !file || this.rows.length >= 20) {
      this.uploadError = '请选择材料存储库和文件；本机每个项目最多追踪20份上传，请先核对已有材料。'
      return
    }
    this.uploading = true
    try {
      const result = await this.ports.upload(kb, file, this.project, options)
      const knowledge = result.knowledge
      if (!knowledge?.id || knowledge.knowledge_base_id !== kb) throw Error('上传响应的材料或存储库不匹配，不能据此绑定。')
      if (!this.active) return
      let row = this.rows.find(item => item.ref.id === knowledge.id)
      if (row && row.ref.kb !== kb) throw Error('重复材料的存储库不匹配。')
      if (!row) {
        row = rowFor({ id: knowledge.id, kb, key: this.ports.newKey() })
        this.rows.push(row)
      }
      this.persist()
      row.notice = result.reused ? '复用服务端已有材料；尚未据此确认项目绑定。' : '文件已上传；解析、索引与项目绑定需要分别确认。'
      await this.refresh(row)
    } catch (error) {
      if (this.active) this.uploadError = message(error) + ' 未确认项目绑定；若上传响应丢失，可重新选择同一文件，由服务端去重。'
    } finally { if (this.active) this.uploading = false }
  }
  async refresh(row: MaterialRow): Promise<boolean> {
    if (!this.active || !this.rows.includes(row)) return false
    const sequence = (this.sequences.get(row.ref.id) || 0) + 1
    this.sequences.set(row.ref.id, sequence)
    row.indexReady = false
    row.hits = []
    row.knowledge = undefined
    row.asset = undefined
    row.label = '正在读取当前处理状态'
    row.readError = ''
    try {
      const result = await this.ports.observe(this.project, row.ref)
      if (!this.active || this.sequences.get(row.ref.id) !== sequence) return false
      if (result.knowledge.id !== row.ref.id || result.knowledge.knowledge_base_id !== row.ref.kb) {
        throw Error('材料身份与保存的上传记录不一致，拒绝使用。')
      }
      const status = materialReadiness(result.knowledge, result.trace)
      row.knowledge = result.knowledge
      row.indexReady = status.ready
      row.label = status.label
      row.asset = result.assets.find(asset => asset.project_id === this.project && asset.knowledge_id === row.ref.id)
      return true
    } catch (error) {
      if (this.active && this.sequences.get(row.ref.id) === sequence) {
        row.indexReady = false
        row.knowledge = undefined
        row.asset = undefined
        row.hits = []
        row.readError = message(error)
        row.label = '无法确认当前权限或处理状态'
      }
      return false
    }
  }
  async refreshAll() {
    // Bound request volume; no polling loop per row.
    for (const row of this.rows) if (!row.busy && this.active) await this.refresh(row)
  }
  async bind(row: MaterialRow) {
    if (!this.active || row.busy) return
    row.busy = true
    row.actionError = ''
    try {
      if (!await this.refresh(row)) return
      if (!row.indexReady) throw Error('当前索引状态尚不可确认，不能绑定为可用材料。')
      if (row.asset) { row.notice = '当前项目已绑定该材料，无需重复绑定。'; return }
      const asset = await this.ports.bind(this.project, row.ref.id, row.ref.key)
      if (!this.active) return
      if (asset.project_id !== this.project || asset.knowledge_id !== row.ref.id) throw Error('绑定响应不属于当前项目或材料。')
      if (!await this.refresh(row)) {
        row.notice = '绑定请求已提交，但读回失败；请重新读取确认，不会重新上传。'
        return
      }
      row.notice = row.asset ? '已读回当前项目绑定；仍需用项目检索核实命中。' : '绑定响应已返回，但尚未读回该绑定；请重新读取。'
    } catch (error) {
      if (this.active) row.actionError = message(error) + ' 已上传材料保留；重试沿用同一绑定幂等键。'
    } finally { if (this.active) row.busy = false }
  }
  async search(row: MaterialRow) {
    if (!this.active || row.busy || !row.query.trim()) return
    row.busy = true
    row.actionError = ''
    row.hits = []
    try {
      if (!await this.refresh(row)) return
      if (!row.indexReady || !row.asset || row.asset.processing_state !== 'ready') throw Error('必须先核实当前项目已绑定且材料可用。')
      const asset = row.asset.id
      const sequence = this.sequences.get(row.ref.id)
      const hits = await this.ports.search(this.project, row.query.trim(), asset)
      if (!this.active || sequence !== this.sequences.get(row.ref.id)) return
      if (hits.some(hit => hit.project_id !== this.project || hit.asset_id !== asset || hit.status !== 'available')) {
        throw Error('检索返回了非当前项目或不可用来源，已拒绝展示。')
      }
      row.hits = hits
      row.notice = hits.length ? '项目检索已返回该绑定材料的来源。' : '本次未命中；不代表已验证可检索，请换用文件中的原文关键词或检查索引。'
    } catch (error) { if (this.active) row.actionError = message(error) }
    finally { if (this.active) row.busy = false }
  }
}
