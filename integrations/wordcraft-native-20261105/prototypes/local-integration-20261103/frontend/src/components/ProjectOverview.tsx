import {
  ArrowRight,
  BookOpen,
  CheckCircle2,
  FileText,
  GitCompareArrows,
  MessageSquareText,
  Upload,
} from 'lucide-react'
import {
  reviewTasks,
  type TaskRecord,
} from '../data/scenario'
import { useLive } from '../lib/live-context'
import { ProgressBadge } from './ReviewPrimitives'

export function ProjectOverview({
  records,
  onContinue,
  onOpenTask,
  onOpenReview,
  onOpenDocx,
}: {
  records: Record<string, TaskRecord>
  onContinue: () => void
  onOpenTask: (id: string) => void
  onOpenReview: () => void
  onOpenDocx: () => void
}) {
  const live = useLive()
  const comment = live.data!.comment
  const sourceVersions = live.data!.sourceVersions
  const changed = live.data!.changeApplied
  const pending = Object.entries(records).filter(([id, record]) => id != 'R3' && (changed || id === 'C01') && record.progress !== 'completed').length

  return (
    <main className="overview-page page-enter">
      <div className="overview-main">
        <section className="overview-intro">
          <p className="eyebrow">科研项目 · 申报审阅</p>
          <h1>社区老年人数字公共服务<br />使用障碍研究</h1>
          <p>已有初稿、社区联系记录和一条导师意见。本轮目标是完成一次可追溯的审阅，而不是重新组织资料库。</p>
          <div className="project-meta-line">
            <span>本地独立文稿</span><span>项目记录版本 {live.data!.revision}</span><span>虚构科研申报演示</span>
          </div>
        </section>

        <section className="continue-section" aria-labelledby="continue-title">
          <div className="section-heading">
            <div>
              <p className="eyebrow">上次工作位置</p>
              <h2 id="continue-title">继续处理“样本选择”</h2>
            </div>
            <FileText size={22} />
          </div>
          <p className="continue-excerpt">“计划进入四个社区开展问卷与访谈，据此了解全市老年人使用数字公共服务的情况。”</p>
          <div className="continue-context">
            <span><MessageSquareText size={16} />导师意见：说明选择标准，收窄概括范围</span>
            <span>进入原生正文 · 手动选段绑定意见</span>
          </div>
          <div className="continue-actions">
            <button className="button primary" onClick={onContinue}>继续写作<ArrowRight size={17} /></button>
            <button className="button secondary" onClick={onOpenDocx}><Upload size={16} />导入其他 DOCX（待接入）</button>
          </div>
        </section>

        <section className="work-queue" aria-labelledby="queue-title">
          <div className="section-heading queue-heading">
            <div>
              <p className="eyebrow">当前任务</p>
              <h2 id="queue-title">需要你的判断</h2>
            </div>
            <button className="text-button" onClick={onOpenReview}>进入审阅与交付<ArrowRight size={15} /></button>
          </div>
          <div className="queue-list">
            <button className="queue-row" onClick={() => onOpenTask('C01')}>
              <span className="queue-index">01</span>
              <span className="queue-copy">
                <strong>导师意见 · 样本选择</strong>
                <span>{comment.text}</span>
              </span>
              <ProgressBadge progress={records.C01.progress} />
              <ArrowRight size={17} />
            </button>
            <div className="queue-group-label"><GitCompareArrows size={15} />{changed ? '联系记录已更新 · 检查对正文的影响' : '下一步：登记联系记录更新'}</div>
            {!changed && <button className="button secondary full" disabled={live.busy} onClick={() => void live.act({ type: 'publish_source' })}>登记 v2 资料更新并生成回看</button>}
            {changed && reviewTasks.filter(task => task.id !== 'R3').map((task, index) => (
              <button className="queue-row" key={task.id} onClick={() => onOpenTask(task.id)}>
                <span className="queue-index">0{index + 2}</span>
                <span className="queue-copy">
                  <strong>{task.title}</strong>
                  <span>{task.reason}</span>
                </span>
                <ProgressBadge progress={records[task.id].progress} />
                <ArrowRight size={17} />
              </button>
            ))}
          </div>
        </section>
      </div>

      <aside className="overview-rail">
        <section className="rail-section">
          <p className="eyebrow">项目状态</p>
          <div className="big-stat">{pending}<span>项尚未完成</span></div>
          <p className="rail-note">决定、修改、保存与完成分别记录。资料、意见与回看结果保存在本地项目中。</p>
        </section>
        <section className="rail-section">
          <div className="section-heading compact">
            <h3>版本差异</h3><GitCompareArrows size={17} />
          </div>
          <dl className="version-list">
            <div><dt>写作时登记的依据</dt><dd>四社区方案 · CONTACT-v1</dd></div>
            <div><dt>最新资料</dt><dd>{changed ? '两社区 · CONTACT-v2' : '四社区 · CONTACT-v1'}</dd></div>
            <div><dt>研究快照</dt><dd>RS1 · 场景预置示例</dd></div>
          </dl>
        </section>
        <section className="rail-section">
          <div className="section-heading compact">
            <h3>项目资产</h3><BookOpen size={17} />
          </div>
          <button className="asset-row" onClick={onContinue}>
            <span><strong>{sourceVersions.v2.title}</strong><small>{changed ? 2 : 1} 个已登记版本 · 可查看原文</small></span><ArrowRight size={16} />
          </button>
          <div className="asset-row static">
            <span><strong>研究方案 RS1</strong><small>拟进入四个社区 · 方案版本待接入</small></span><CheckCircle2 size={16} />
          </div>
        </section>
        <section className="rail-section coverage-note">
          <p className="eyebrow">覆盖范围提示</p>
          <p>显式关系未发现“访谈方法”直接受影响。这不等于已确认无需调整，仍由你判断。</p>
          <button className="text-button" disabled={!changed} onClick={() => onOpenTask('METHOD')}>记录判断<ArrowRight size={15} /></button>
        </section>
      </aside>
    </main>
  )
}
