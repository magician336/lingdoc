import {
  ArrowLeft,
  ArrowRight,
  Check,
  CircleDashed,
  Download,
  FileCheck2,
  GitBranch,
  GitCompareArrows,
  MessageSquareText,
  Plus,
  Save,
  Trash2,
} from 'lucide-react'
import { useState } from 'react'
import {
  decisionLabels,
  reviewTasks,
  type Decision,
  type Section,
  type TaskRecord,
} from '../data/scenario'
import { useLive } from '../lib/live-context'
import { RecordCompletion } from './RecordCompletion'
import { DecisionForm, InlineNotice, ProgressBadge } from './ReviewPrimitives'

type ReviewTab = 'tasks' | 'delivery' | 'figure'

export function ReviewDelivery({
  records,
  sections,
  selectedTask,
  tab,
  onTab,
  onSelectTask,
  onDecision,
  onApplyEdit,
  onBackWrite,
  onExport,
  onToast,
}: {
  records: Record<string, TaskRecord>
  sections: Section[]
  selectedTask: string
  tab: ReviewTab
  onTab: (tab: ReviewTab) => void
  onSelectTask: (id: string) => void
  onDecision: (id: string, decision: Decision, reason: string) => void
  onApplyEdit: (id: string) => void
  onBackWrite: () => void
  onExport: () => void
  onToast: (message: string) => void
}) {
  const live = useLive()
  const comment = live.data!.comment
  const sourceVersions = live.data!.sourceVersions
  const changed = live.data!.changeApplied
  const pendingCount = Object.entries(records).filter(([id, record]) => id != 'R3' && (changed || id === 'C01') && record.progress !== 'completed').length
  const selectedReview = changed ? reviewTasks.find((task) => task.id === selectedTask) : undefined
  const selectedSection = sections.find((section) => section.id === (selectedReview?.target || (selectedTask === 'C01' ? 'SAMPLE' : 'METHOD')))

  return (
    <main className="review-page page-enter">
      <header className="review-page-header">
        <div><p className="eyebrow">审阅与交付</p><h1>知道改什么，也知道尚未完成什么</h1></div>
        <button className="button secondary" onClick={onBackWrite}><ArrowLeft size={16} />返回写作</button>
      </header>
      <div className="review-tabs" role="tablist">
        <button role="tab" aria-selected={tab === 'tasks'} className={tab === 'tasks' ? 'selected' : ''} onClick={() => onTab('tasks')}><MessageSquareText size={16} />意见与变化<span>{pendingCount}</span></button>
        <button role="tab" aria-selected={tab === 'delivery'} className={tab === 'delivery' ? 'selected' : ''} onClick={() => onTab('delivery')}><FileCheck2 size={16} />交付预览</button>
        <button hidden role="tab" aria-selected={tab === 'figure'} className={tab === 'figure' ? 'selected' : ''} onClick={() => onTab('figure')}><GitBranch size={16} />路线图试验</button>
      </div>

      {tab === 'tasks' && (
        <div className="review-workspace">
          <aside className="review-list">
            <div className="review-list-heading"><span>任务</span><span>决定 / 进度</span></div>
            <button className={`review-list-row ${selectedTask === 'C01' ? 'selected' : ''}`} onClick={() => onSelectTask('C01')}>
              <span><strong>导师意见 · 样本选择</strong><small>人工意见 C01 · 本地持久化</small></span><ProgressBadge progress={records.C01.progress} />
            </button>
            <div className="review-change-heading"><GitCompareArrows size={15} /><span>{changed ? 'CONTACT-v1 → CONTACT-v2' : '资料更新尚未登记'}</span></div>
            {changed && reviewTasks.filter(task => task.id !== 'R3').map((task) => (
              <button className={`review-list-row ${selectedTask === task.id ? 'selected' : ''}`} key={task.id} onClick={() => onSelectTask(task.id)}>
                <span><strong>{task.title}</strong><small>{task.id} · {task.reason}</small></span><ProgressBadge progress={records[task.id].progress} />
              </button>
            ))}
            <button disabled={!changed} className={`review-list-row ${selectedTask === 'METHOD' ? 'selected' : ''}`} onClick={() => onSelectTask('METHOD')}>
              <span><strong>访谈方法 · 覆盖判断</strong><small>系统未发现直接依赖，不预设已完成</small></span><ProgressBadge progress={records.METHOD.progress} />
            </button>
          </aside>

          <section className="review-detail">
            {!changed && selectedTask !== 'C01' && <><h2>先登记资料变化</h2><p>社区联系记录将由 v1 的四个社区更新为 v2 的两个社区。登记后，显式关联的正文与图表才会进入待回看。</p><button className="button primary" disabled={live.busy} onClick={() => void live.act({ type: 'publish_source' })}>登记更新并生成回看</button></>}
            {selectedTask === 'C01' && (
              <>
                <div className="detail-title"><div><p className="eyebrow">人工意见 C01</p><h2>样本选择</h2></div><ProgressBadge progress={records.C01.progress} /></div>
                <blockquote className="comment-quote">{comment.text}</blockquote>
                <div className="old-excerpt"><span>意见对应旧摘录</span><p>{comment.excerpt}</p><small>旧摘录仅用于核对。原生选区可在写作页人工重新绑定。</small></div>
                {selectedSection && <DocumentSection section={selectedSection} />}
                <DecisionForm disabled={live.busy || records.C01.progress === 'completed'} key={`C01-${live.data!.revision}`} record={records.C01} onSubmit={(decision, reason) => onDecision('C01', decision, reason)} />
                <RecordCompletion id="C01" /><button className="button secondary" onClick={onBackWrite}>返回写作处理这条意见<ArrowRight size={16} /></button>
              </>
            )}

            {selectedReview && (
              <>
                <div className="detail-title"><div><p className="eyebrow">变化回看 {selectedReview.id}</p><h2>{selectedReview.title}</h2></div><ProgressBadge progress={records[selectedReview.id].progress} /></div>
                <InlineNotice tone="warning">系统只提示需要检查：{selectedReview.reason} 不会自动修改正文、样本量或图表。</InlineNotice>
                <div className="version-comparison compact-comparison">
                  <section><span className="version-label">旧依据 · CONTACT-v1</span><p>{sourceVersions.v1.text}</p></section>
                  <section><span className="version-label">新依据 · CONTACT-v2</span><p>{sourceVersions.v2.text}</p></section>
                </div>
                {selectedSection && <DocumentSection section={selectedSection} />}
                {selectedReview.target === 'FIG01' ? (
                  <div className="figure-pending"><GitBranch size={22} /><div><strong>演示路线图包含“四社区联系与招募”节点</strong><p>当前图源为独立演示，尚未接入本次 DOCX；更新文稿图片仍待接线。</p></div><button className="button secondary" onClick={() => onTab('figure')}>打开路线图试验</button></div>
                ) : (
                  <div className="demo-edit-row">
                    <div><strong>记录实际修改</strong><p>请在原生文稿中人工修改并保存，再记录处理结果。</p></div>
                    <button className="button secondary" onClick={() => onApplyEdit(selectedReview.id)}>返回原生正文修改</button>
                  </div>
                )}
                {records[selectedReview.id].draftRecorded && <InlineNotice>已记录处理方向，仍需修改并保存正文。</InlineNotice>}
                <DecisionForm disabled={live.busy || records[selectedReview.id].progress === 'completed'} key={`${selectedReview.id}-${live.data!.revision}`} record={records[selectedReview.id]} onSubmit={(decision, reason) => onDecision(selectedReview.id, decision, reason)} /><RecordCompletion id={selectedReview.id} />
              </>
            )}

            {changed && selectedTask === 'METHOD' && (
              <>
                <div className="detail-title"><div><p className="eyebrow">覆盖范围判断</p><h2>访谈方法</h2></div><ProgressBadge progress={records.METHOD.progress} /></div>
                <InlineNotice>显式关系未发现访谈方法直接受影响。这只说明本次检查范围，不等于系统确认不受影响。</InlineNotice>
                {selectedSection && <DocumentSection section={selectedSection} />}
                <DecisionForm disabled={live.busy || records.METHOD.progress === 'completed'} key={`METHOD-${live.data!.revision}`} record={records.METHOD} onSubmit={(decision, reason) => onDecision('METHOD', decision, reason)} />
              </>
            )}

            {records[selectedTask]?.decision && (
              <div className="decision-receipt">
                <Check size={17} /><span>已保存决定：<strong>{decisionLabels[records[selectedTask].decision as Decision]}</strong>{records[selectedTask].reason && ` · ${records[selectedTask].reason}`}</span>
              </div>
            )}
          </section>
        </div>
      )}

      {tab === 'delivery' && <DeliveryPreview records={records} onExport={onExport} onBackWrite={onBackWrite} />}
      {tab === 'figure' && <FigureExperiment onToast={onToast} />}
    </main>
  )
}

function DocumentSection({ section }: { section: Section }) {
  return <section className="document-section"><div className="section-heading compact"><h3>初始稿参考 · {section.title}</h3><span>实时内容请查看原生正文</span></div><p>{section.text}</p></section>
}

function DeliveryPreview({ records, onExport, onBackWrite }: { records: Record<string, TaskRecord>; onExport: () => void; onBackWrite: () => void }) {
  const live = useLive()
  const entries = [
    ['C01', '导师意见 · 样本选择'],
    ['R1', '样本范围与适用边界'],
    ['R2', '招募与进度安排'],
    ['METHOD', '访谈方法覆盖判断'],
  ]
  const visibleEntries = entries.filter(([id]) => live.data!.changeApplied || id === 'C01')
  const pending = visibleEntries.filter(([id]) => records[id].progress !== 'completed').length
  return (
    <div className="delivery-layout">
      <section className="delivery-document">
        <p className="eyebrow">交付预览</p>
        <h2>社区老年人数字公共服务使用障碍研究</h2>
        <div className="delivery-facts"><span>当前原生修订：{live.native?.engine?.revision ?? '加载中'}</span><span>原生保存版本：{live.native?.saved ? `v${live.native.saved.goDiskVersion}` : '尚未确认'}</span><span>输出：真实保存的 DOCX 副本</span></div>
        <h3>本次审阅覆盖</h3>
        <div className="coverage-table">
          {visibleEntries.map(([id, title]) => <div key={id}><span>{title}</span><ProgressBadge progress={records[id].progress} /></div>)}
        </div>
        <p className="muted-copy">当前仅导出文稿副本。人工意见与回看记录保存在项目中，尚未附入 DOCX；内部资料与未采纳候选不会被打包。</p>
      </section>
      <aside className="delivery-summary">
        <CircleDashed size={26} />
        <h3>尚不能宣告交付完成</h3>
        <div className="big-stat">{pending}<span>项未完成</span></div>
        <p>可导出已保存文稿供人工审阅。导出副本不会自动关闭未完成任务，也不代表正式冻结交付。</p>
        <button className="button primary full" onClick={onExport}><Download size={16} />导出导师审阅稿</button>
        <button className="button secondary full" onClick={onBackWrite}>返回继续处理</button>
      </aside>
    </div>
  )
}

function FigureExperiment({ onToast }: { onToast: (message: string) => void }) {
  const [nodes, setNodes] = useState([
    { id: 'N1', text: '四社区联系与招募' },
    { id: 'N2', text: '问卷与半结构访谈' },
    { id: 'N3', text: '整理障碍与支持需求' },
  ])
  const [edges, setEdges] = useState<[string, string][]>([['N1', 'N2'], ['N2', 'N3']])
  const [from, setFrom] = useState('N1')
  const [to, setTo] = useState('N2')
  const [savedAt, setSavedAt] = useState('')

  const addNode = () => {
    const id = `N${Date.now()}`
    setNodes((current) => [...current, { id, text: '新研究步骤' }])
    setFrom(id)
  }
  const addEdge = () => {
    if (from === to || edges.some(([a, b]) => a === from && b === to)) return
    setEdges((current) => [...current, [from, to]])
  }

  return (
    <div className="figure-layout">
      <section className="figure-editor">
        <div className="section-heading"><div><p className="eyebrow">可编辑图源 · 试验状态</p><h2>研究技术路线</h2></div><span className="demo-tag">不持久化</span></div>
        <InlineNotice>节点和连线仅保存到当前演示页面。DOCX 内一张图片不等于保留可编辑图源。</InlineNotice>
        <div className="flow-nodes">
          {nodes.map((node, index) => (
            <div className="flow-node-wrap" key={node.id}>
              <div className="flow-node"><span>{String(index + 1).padStart(2, '0')}</span><input aria-label={`节点 ${index + 1} 文本`} value={node.text} onChange={(event) => setNodes((current) => current.map((item) => item.id === node.id ? { ...item, text: event.target.value } : item))} /></div>
              {index < nodes.length - 1 && <div className="flow-connector">↓</div>}
            </div>
          ))}
        </div>
        <button className="text-button" onClick={addNode}><Plus size={15} />添加节点</button>
      </section>
      <aside className="figure-tools">
        <section><p className="eyebrow">连线编辑</p><div className="edge-form"><select aria-label="连线起点" value={from} onChange={(event) => setFrom(event.target.value)}>{nodes.map((node) => <option value={node.id} key={node.id}>{node.text}</option>)}</select><span>→</span><select aria-label="连线终点" value={to} onChange={(event) => setTo(event.target.value)}>{nodes.map((node) => <option value={node.id} key={node.id}>{node.text}</option>)}</select><button className="icon-button" onClick={addEdge} aria-label="添加连线"><Plus size={17} /></button></div>
          <div className="edge-list">{edges.map(([a, b], index) => <div key={`${a}-${b}`}><span>{nodes.find((n) => n.id === a)?.text} → {nodes.find((n) => n.id === b)?.text}</span><button className="icon-button small" onClick={() => setEdges((current) => current.filter((_, i) => i !== index))} aria-label="删除连线"><Trash2 size={14} /></button></div>)}</div>
        </section>
        <section><p className="eyebrow">图源接入边界</p><p>当前节点为前端演示，并未读回本次 DOCX 中的图。编辑图源不会自动插入或替换文稿图片。</p><button className="button secondary full" onClick={() => onToast('原生图片替换尚未接入；旧图未被覆盖，可在接入后由用户主动更新。')}>主动更新文稿旧图</button></section>
        <button className="button primary full" onClick={() => { setSavedAt(new Date().toLocaleTimeString('zh-CN')); onToast('演示图源已保存在当前页面；刷新会还原，未写入服务器。') }}><Save size={16} />保存演示图源</button>
        {savedAt && <p className="muted-copy">本次页面内保存：{savedAt}</p>}
      </aside>
    </div>
  )
}
