import {
  ArrowLeft,
  ArrowRight,
  BookOpen,
  Check,
  ChevronRight,
  Columns2,
  Expand,
  Focus,
  MessageSquareText,
  PanelRight,
  Sparkles,
  X,
} from 'lucide-react'
import { useState } from 'react'
import {
  type Decision,
  type SaveState,
  type Section,
} from '../data/scenario'
import { useLive } from '../lib/live-context'
import { RecordCompletion } from './RecordCompletion'
import { ControlledAssistant } from './ControlledAssistant'
import { NativeHost } from './NativeHost'
import { DecisionForm, InlineNotice, Modal, ProgressBadge } from './ReviewPrimitives'

const suggestedText = '计划选择具备老年人数字服务使用场景、社区协作意愿及基本招募条件的社区开展问卷与访谈，用于分析所选社区的使用障碍与支持需求，不据此概括全市情况。'

export function WritingWorkspace({
  sourceOpen,
  rightOpen,
  focused,
  rightTab,
  saveState,
  section,
  record,
  compareOpen,
  onSourceOpen,
  onRightOpen,
  onFocused,
  onRightTab,
  onCompare,
  onDecision,
  onApplySuggestion,
  onSaveStatus,
  onExport,
  onOpenReview,
  onNext,
  onToast,
}: {
  sourceOpen: boolean
  rightOpen: boolean
  focused: boolean
  rightTab: 'comment' | 'assistant'
  saveState: SaveState
  section: Section
  record: import('../lib/live-context').LiveRecord
  compareOpen: boolean
  onSourceOpen: (open: boolean) => void
  onRightOpen: (open: boolean) => void
  onFocused: (focused: boolean) => void
  onRightTab: (tab: 'comment' | 'assistant') => void
  onCompare: (open: boolean) => void
  onDecision: (id: string, decision: Decision, reason: string) => void
  onApplySuggestion: (text: string) => void
  onSaveStatus: () => void
  onExport: () => void
  onOpenReview: () => void
  onNext: () => void
  onToast: (message: string) => void
}) {
  const live = useLive()
  const comment = live.data!.comment
  const sourceVersions = live.data!.sourceVersions
  const [sourceVersion, setSourceVersion] = useState<'v1' | 'v2'>('v1')
  const [readingExpanded, setReadingExpanded] = useState(false)
  const [draftSuggestion, setDraftSuggestion] = useState(suggestedText)
  const source = sourceVersions[sourceVersion]

  return (
    <main className={`writing-page ${focused ? 'is-focused' : ''}`}>
      {!focused && (
        <div className="task-toolbar">
          <div className="current-task">
            <span className="task-number">当前意见</span>
            <strong>处理导师对样本选择的意见</strong>
            <ProgressBadge progress={record.progress} />
          </div>
          <div className="task-actions">
            <button className="text-button" onClick={onOpenReview}>审阅总览<ChevronRight size={15} /></button>
            <button className="text-button" onClick={onNext}>处理下一项<ArrowRight size={15} /></button>
            <button className="button secondary compact-button" onClick={() => onFocused(true)}><Focus size={16} />专注写作</button>
          </div>
        </div>
      )}

      <div className="workspace-controls">
        <div>
          {!focused && <button className={`text-button ${sourceOpen ? 'active' : ''}`} onClick={() => onSourceOpen(!sourceOpen)}><BookOpen size={16} />资料</button>}
          {!focused && <button className={`text-button ${rightOpen ? 'active' : ''}`} onClick={() => onRightOpen(!rightOpen)}><PanelRight size={16} />意见与助手</button>}
        </div>
        {focused ? <button className="button secondary compact-button" onClick={() => onFocused(false)}><Columns2 size={16} />退出专注</button> : <span>查看资料或切换辅助区不会改变当前任务和会话</span>}
      </div>

      <div className={`writing-grid ${sourceOpen && !focused ? 'with-source' : ''} ${rightOpen && !focused ? 'with-right' : ''}`}>
        <aside hidden={!sourceOpen || focused} className="source-panel panel-enter">
            <div className="panel-header">
              <div><p className="eyebrow">项目资料</p><h2>社区联系记录</h2></div>
              <button className="icon-button" onClick={() => onSourceOpen(false)} aria-label="关闭资料面板"><X size={18} /></button>
            </div>
            <div className="segmented-control" aria-label="资料版本">
              <button className={sourceVersion === 'v1' ? 'selected' : ''} onClick={() => setSourceVersion('v1')}>v1 · 正文依据</button>
              <button className={sourceVersion === 'v2' ? 'selected' : ''} onClick={() => setSourceVersion('v2')}>{live.data!.changeApplied ? 'v2 · 最新' : 'v2 · 待登记'}</button>
            </div>
            {sourceVersion === 'v2' && <InlineNotice tone="warning">{live.data!.changeApplied ? '资料 v2 已登记。历史依据 v1 仍保留，相关回看等待你的判断。' : '这是待登记的联系记录更新。登记后保存资料新版本，并生成显式关联的回看任务。'}</InlineNotice>}
            <div className="source-meta"><span>{source.versionId || source.id || `CONTACT-${sourceVersion}`}</span><span>段落 {source.paragraphId || 'P1'}</span></div>
            <blockquote className="source-quote">{source.text}</blockquote>
            {!live.data!.changeApplied && <button className="button primary full" disabled={live.busy} onClick={() => void live.act({ type: 'publish_source' })}>登记资料更新并生成回看</button>}
            <section className="source-relation">
              <p className="eyebrow">显式关联理由</p>
              <p>样本场地范围与招募安排依据这份联系记录。关系只提示需要检查，不代表系统已判断正文错误。</p>
            </section>
            <section className="source-snapshot">
              <p className="eyebrow">场景预置方案 RS1（演示）</p>
              <p>“拟进入四个社区”仍关联 CONTACT-v1 / P1，不随最新资料自动改写。</p>
            </section>
            <button className="button secondary full" onClick={() => setReadingExpanded(true)}><Expand size={16} />展开阅读并比较版本</button>
          </aside>

        <NativeHost section={section} saveState={saveState} onSaveStatus={onSaveStatus} onExport={onExport} />

        <aside hidden={!rightOpen || focused} className="right-panel panel-enter">
            <div className="right-panel-tabs">
              <button className={rightTab === 'comment' ? 'selected' : ''} onClick={() => onRightTab('comment')}><MessageSquareText size={16} />意见</button>
              <button className={rightTab === 'assistant' ? 'selected' : ''} onClick={() => onRightTab('assistant')}><Sparkles size={16} />助手</button>
              <button className="icon-button" onClick={() => onRightOpen(false)} aria-label="关闭意见与助手面板"><X size={18} /></button>
            </div>
            <div hidden={rightTab !== 'comment'} className="comment-panel panel-scroll">
                <div className="comment-author"><span>{comment.author}</span><span>本地持久化意见 C01</span></div>
                <blockquote className="comment-quote">{comment.text}</blockquote>
                <section className="anchor-section">
                  <div className="section-heading compact"><h3>指向的正文</h3><span className="status-neutral">{record.anchor && record.anchor.hostSessionId === live.nativeSessionId && record.anchor.documentId === live.native?.engine?.documentId && record.anchor.revision === live.native?.engine?.revision ? '选区修订一致' : '需人工重新定位'}</span></div>
                  <p>{record.anchor?.excerpt || comment.excerpt}</p>
                  <button className="text-button" disabled={live.busy} onClick={() => void live.bindAnchor()}>绑定当前原生选区<ArrowRight size={14} /></button>
                </section>
                <InlineNotice>请在原生正文中框选对应文字，再绑定选区。文稿变化或重开后，保留旧摘录，需人工重新定位；不会猜测位置。</InlineNotice>
                <details className="comment-edit"><summary>录入或修改人工意见</summary><form key={live.data!.revision} onSubmit={event => { event.preventDefault(); const form = new FormData(event.currentTarget); void live.act({ type: 'comment', id: 'C01', text: String(form.get('text')), author: String(form.get('author')) }) }}><label>意见来源<input name="author" defaultValue={comment.author} required /></label><label>意见原文<textarea name="text" defaultValue={comment.text} rows={4} required /></label><button className="button secondary full" disabled={live.busy}>保存意见</button></form></details>
                <DecisionForm key={`C01-${live.data!.revision}`} disabled={live.busy || record.progress === 'completed'} record={record} onSubmit={(decision, reason) => onDecision('C01', decision, reason)} />
                <RecordCompletion id="C01" />
                <button className="button secondary full" onClick={() => { onRightTab('assistant'); onToast('已切换到助手；当前意见、决定和输入状态均保留。') }}><Sparkles size={16} />请求 AI 局部帮助</button>
                {record.draftRecorded && <InlineNotice tone="warning">已记录处理意向；修改并保存正文后，再登记实际结果。</InlineNotice>}
              </div>
            <div hidden={rightTab !== 'assistant'} className="assistant-tab-content"><ControlledAssistant /></div>
          </aside>
      </div>

      {readingExpanded && (
        <Modal title="社区联系记录" eyebrow="展开阅读 · 返回后保留刚才的文稿位置" onClose={() => setReadingExpanded(false)} wide>
          <div className="version-comparison">
            <section><span className="version-label">正文依据 · v1</span><p>{sourceVersions.v1.text}</p></section>
            <section><span className="version-label">{live.data!.changeApplied ? '最新资料 · v2' : '待登记更新 · v2'}</span><p>{sourceVersions.v2.text}</p></section>
          </div>
          <InlineNotice tone="warning">变化：可进入场地由四个社区变为两个社区。系统不自动推断样本量减半，也不自动修改研究方法。</InlineNotice>
          <button className="button primary" onClick={() => setReadingExpanded(false)}><ArrowLeft size={16} />返回刚才的文稿位置</button>
        </Modal>
      )}

      {compareOpen && (
        <Modal title="建议与原稿对照" eyebrow="S06 · 采用前校验正文版本" onClose={() => onCompare(false)} wide>
          {saveState === 'conflict' && <InlineNotice tone="error">正文版本已变化，必须重新比较。当前建议不能采用。</InlineNotice>}
          <div className="suggestion-comparison">
            <section><p className="eyebrow">当前演示原稿</p><p>{section.text}</p></section>
            <section>
              <p className="eyebrow">建议 · 可直接修改后再采用</p>
              <textarea value={draftSuggestion} onChange={(event) => setDraftSuggestion(event.target.value)} rows={7} />
            </section>
          </div>
          <div className="modal-actions">
            <button className="button secondary" onClick={() => { onCompare(false); onToast('已放弃本次建议，原稿未改变。') }}>放弃建议</button>
            <button className="button primary" disabled={saveState === 'conflict'} onClick={() => { onApplySuggestion(draftSuggestion); onCompare(false) }}><Check size={16} />返回正文人工修改</button>
          </div>
          <p className="modal-footnote">此为固定建议演示，未调用模型。正文由你在 WordCraft 中修改，建议不会自动采用。</p>
        </Modal>
      )}
    </main>
  )
}
