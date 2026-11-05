import { BookOpenCheck, ChevronDown, FileCheck2, FolderOpen, Info, PenLine, Save, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { ProjectOverview } from './components/ProjectOverview'
import { ReviewDelivery } from './components/ReviewDelivery'
import { InlineNotice, Modal } from './components/ReviewPrimitives'
import { WritingWorkspace } from './components/WritingWorkspace'
import { BASELINE_ID, initialSections, saveStateCopy, type Decision, type SaveState, type View } from './data/scenario'
import { LiveProvider } from './lib/live'
import { useLive } from './lib/live-context'
const navItems = [{ id: 'overview' as View, label: '项目概览', icon: FolderOpen }, { id: 'write' as View, label: '写作', icon: PenLine }, { id: 'review' as View, label: '审阅与交付', icon: BookOpenCheck }]
export default function App() { return <LiveProvider><Workspace /></LiveProvider> }
function Workspace() {
  const live = useLive()
  const [view, setViewState] = useState<View>('overview')
  const [nativeStarted, setNativeStarted] = useState(false)
  const setView = (next: View) => { if (next === 'write') setNativeStarted(true); setViewState(next) }
  const [selectedTask, setSelectedTask] = useState('C01')
  const [sourceOpen, setSourceOpen] = useState(() => window.innerWidth > 1100)
  const [rightOpen, setRightOpen] = useState(() => window.innerWidth > 1200)
  const [focused, setFocused] = useState(false)
  const [rightTab, setRightTab] = useState<'comment' | 'assistant'>('comment')
  const [saveOpen, setSaveOpen] = useState(false)
  const [infoOpen, setInfoOpen] = useState(false)
  const [compareOpen, setCompareOpen] = useState(false)
  const [reviewTab, setReviewTab] = useState<'tasks' | 'delivery' | 'figure'>('tasks')
  const toast = live.notice
  const notify = live.notify
  useEffect(() => { if (!toast) return; const timer = window.setTimeout(() => notify(''), 5000); return () => window.clearTimeout(timer) }, [toast, notify])
  const dirty = live.native?.engine?.dirty === true
  useEffect(() => { if (!dirty) return; const keepDraft = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }; window.addEventListener('beforeunload', keepDraft); return () => window.removeEventListener('beforeunload', keepDraft) }, [dirty])
  const phase = live.native?.phase
  const saveState: SaveState = phase === 'saved' ? 'saved' : phase === 'unsaved' || phase === 'not_registered' ? 'unsaved' : phase === 'saving' ? 'saving' : phase === 'conflict' ? 'conflict' : phase === 'save_failed' ? 'failed' : 'unconnected'
  const openTask = (id: string) => { setSelectedTask(id); if (id === 'C01') { setView('write'); setFocused(false); setRightOpen(true); setRightTab('comment') } else { setReviewTab('tasks'); setView('review') } }
  const continueWriting = () => setView('write')
  const recordDecision = (id: string, decision: Decision, reason: string) => { if (decision === 'no_change' && !reason.trim()) { live.notify('无需调整需填写判断理由。'); return } void live.act({ type: 'decide', id, decision, reason }) }
  const pendingCount = live.data ? Object.entries(live.data.records).filter(([id, record]) => id != 'R3' && (live.data!.changeApplied || id === 'C01') && record.progress !== 'completed').length : 0
  if (!live.data) return <div className="integration-loading"><span className="brand-mark">L</span><h1>正在打开科研项目</h1><p>{live.error || '读取资料版本、人工意见与处理记录…'}</p>{live.error && <button className="button primary" onClick={() => void live.refresh()}>重试读取</button>}</div>
  const records = live.data.records
  return <div className="app-shell">
    <header className="app-header">
      <button className="brand" onClick={() => setView('overview')} aria-label="返回项目概览"><span className="brand-mark">L</span><span>LingDoc</span></button>
      <button className="project-switcher" onClick={() => setView('overview')}><span>社区老年人数字公共服务使用障碍研究</span><ChevronDown size={14} /></button>
      <nav className="top-nav" aria-label="主导航">{navItems.map(item => <button key={item.id} className={view === item.id ? 'selected' : ''} onClick={() => setView(item.id)}><item.icon size={16} />{item.label}{item.id === 'review' && <span className="nav-count">{pendingCount}</span>}</button>)}</nav>
      <div className="header-actions"><button className={`save-status-button save-text-${saveState}`} onClick={() => setSaveOpen(true)}><Save size={15} /><span>{saveStateCopy[saveState].label}</span></button><button className="button primary compact-button delivery-button" onClick={() => { setReviewTab('delivery'); setView('review') }}><FileCheck2 size={16} />交付</button></div>
    </header>
    <div className="demo-ribbon"><span>受控写作 0.3 · 虚构资料，模型接线与原生保存 · 图表不在本版范围</span><div><button onClick={() => void live.refresh()}>刷新项目状态</button><button onClick={() => setInfoOpen(true)}><Info size={13} />{BASELINE_ID}</button></div></div>
    {live.error && <div className="integration-error" role="alert"><span>{live.error}</span>{live.pending ? <button className="button secondary compact-button" disabled={live.busy} onClick={() => void live.retry()}>重试原请求</button> : <button className="text-button" onClick={() => void live.refresh()}>重新读取项目</button>}</div>}
    <div className="app-content">
      <div hidden={view !== 'overview'} className="view-scroll"><ProjectOverview records={records} onContinue={continueWriting} onOpenTask={openTask} onOpenReview={() => { setReviewTab('tasks'); setView('review') }} onOpenDocx={() => live.notify('当前为独立试用项目。任意 DOCX 导入尚未接入，现有文稿不会被替换。')} /></div>
      <div hidden={view !== 'write'} className="writing-view-wrapper">{nativeStarted && <WritingWorkspace sourceOpen={sourceOpen} rightOpen={rightOpen} focused={focused} rightTab={rightTab} saveState={saveState} section={initialSections[0]} record={records.C01} compareOpen={compareOpen} onSourceOpen={setSourceOpen} onRightOpen={setRightOpen} onFocused={setFocused} onRightTab={setRightTab} onCompare={setCompareOpen} onDecision={recordDecision} onApplySuggestion={() => live.notify('请在原生正文中自行修改；演示建议不会自动写入。')} onSaveStatus={() => setSaveOpen(true)} onExport={() => void live.download()} onOpenReview={() => { setReviewTab('tasks'); setView('review') }} onNext={() => openTask('R1')} onToast={live.notify} />}</div>
      <div hidden={view !== 'review'} className="view-scroll"><ReviewDelivery records={records} sections={initialSections} selectedTask={selectedTask} tab={reviewTab} onTab={setReviewTab} onSelectTask={setSelectedTask} onDecision={recordDecision} onApplyEdit={() => { setView('write'); live.notify('已返回原生正文，请人工修改后保存，再登记处理完成。') }} onBackWrite={continueWriting} onExport={() => void live.download()} onToast={live.notify} /></div>
    </div>
    <nav className="mobile-nav" aria-label="移动端主导航">{navItems.map(item => <button key={item.id} className={view === item.id ? 'selected' : ''} onClick={() => setView(item.id)}><item.icon size={20} /><span>{item.label}</span></button>)}</nav>
    {saveOpen && <Modal title="文稿保存状态" eyebrow="WordCraft · 当前真实状态" onClose={() => setSaveOpen(false)}><InlineNotice tone={saveState === 'failed' || saveState === 'conflict' ? 'error' : 'info'}>{saveStateCopy[saveState].detail}</InlineNotice><dl className="version-list large"><div><dt>当前修订</dt><dd>{live.native?.engine ? `${live.native.engine.documentId} / ${live.native.engine.revision}` : '正在连接'}</dd></div><div><dt>已保存版本</dt><dd>{live.native?.saved ? `v${live.native.saved.goDiskVersion}` : '尚无可确认的保存版本'}</dd></div><div><dt>当前有新编辑</dt><dd>{live.native?.engine?.dirty ? '是' : '否'}</dd></div></dl>{live.native?.error && <InlineNotice tone="error">{live.native.error.message}</InlineNotice>}<button className="button primary" onClick={() => void live.save()} disabled={phase === 'saving'}><Save size={16} />保存当前文稿</button><p className="modal-footnote">保存与意见完成分别记录。处理意见后请在该条目上登记结果。</p></Modal>}
    {infoOpen && <Modal title="版本与能力边界" eyebrow={BASELINE_ID} onClose={() => setInfoOpen(false)}><dl className="version-list large"><div><dt>前端来源</dt><dd>Kimi 11 月 3 日 LD-V1 源码包</dd></div><div><dt>已接通</dt><dd>原生编辑与保存、受控选区修改、真实模型候选、资料版本、人工意见、变化回看</dd></div><div><dt>仍为演示</dt><dd>研究方案版本；任意 DOCX 导入尚未接入，图表不在本版范围</dd></div><div><dt>项目记录版本</dt><dd>{live.data.revision}</dd></div></dl><p>当前为隔离本地联调，不代表正式发布或全量功能验收。</p></Modal>}
    {toast && <div className="toast" role="status"><span>{toast}</span><button onClick={() => notify('')} aria-label="关闭提示"><X size={16} /></button></div>}
  </div>
}
