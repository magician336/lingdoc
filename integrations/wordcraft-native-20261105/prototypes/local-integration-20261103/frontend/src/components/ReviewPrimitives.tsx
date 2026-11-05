import { X } from 'lucide-react'
import { useEffect, useId, type ReactNode } from 'react'
import {
  decisionLabels,
  progressLabels,
  type Decision,
  type Progress,
  type TaskRecord,
} from '../data/scenario'

export function ProgressBadge({ progress }: { progress: Progress }) {
  return <span className={`progress-badge progress-${progress}`}>{progressLabels[progress]}</span>
}

export function Modal({
  title,
  eyebrow,
  children,
  onClose,
  wide = false,
}: {
  title: string
  eyebrow?: string
  children: ReactNode
  onClose: () => void
  wide?: boolean
}) {
  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [onClose])

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <section
        className={`modal-panel ${wide ? 'modal-wide' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="modal-header">
          <div>
            {eyebrow && <p className="eyebrow">{eyebrow}</p>}
            <h2>{title}</h2>
          </div>
          <button className="icon-button" onClick={onClose} aria-label="关闭对话框" autoFocus>
            <X size={19} />
          </button>
        </header>
        <div className="modal-body">{children}</div>
      </section>
    </div>
  )
}

export function DecisionForm({
  record,
  onSubmit,
  disabled = false,
}: {
  record: TaskRecord
  onSubmit: (decision: Decision, reason: string) => void
  disabled?: boolean
}) {
  const reasonId = useId()
  return (
    <form
      className="decision-form"
      onSubmit={(event) => {
        event.preventDefault()
        const data = new FormData(event.currentTarget)
        const decision = data.get('decision') as Decision | null
        const reason = String(data.get('reason') || '').trim()
        if (decision) onSubmit(decision, reason)
      }}
    >
      <fieldset disabled={disabled}>
        <legend>你的决定</legend>
        <div className="decision-options">
          {(['modify_document', 'no_change', 'defer'] as Decision[]).map((decision) => (
            <label key={decision} className="decision-option">
              <input type="radio" name="decision" value={decision} defaultChecked={record.decision === decision} required />
              <span>{decisionLabels[decision]}</span>
            </label>
          ))}
        </div>
        <label className="field-label" htmlFor={reasonId}>
          判断依据{record.decision === 'no_change' ? '（无需调整时必填）' : '（可选）'}
        </label>
        <textarea
          id={reasonId}
          name="reason"
          rows={3}
          defaultValue={record.reason}
          placeholder="记录你如何判断，以及后续需要核对什么"
        />
      </fieldset>
      <div className="form-footer">
        <p>决定修改不等于处理完成；完成仍需真实保存版本或无需调整理由。</p>
        <button className="button primary" type="submit" disabled={disabled}>保存决定</button>
      </div>
    </form>
  )
}

export function InlineNotice({ children, tone = 'info' }: { children: ReactNode; tone?: 'info' | 'warning' | 'error' }) {
  return <div className={`inline-notice notice-${tone}`}>{children}</div>
}
