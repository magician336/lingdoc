import { createHash } from 'node:crypto';
import { readFile, open, rename, mkdir } from 'node:fs/promises';
import path from 'node:path';

export const BASELINE = 'LD-V1-20261103-R1';
export const SOURCE_TEXT = {
  v1: '四个社区表示愿意在本期协助研究团队开展联系与招募。',
  v2: '其中两个社区本期不能配合，目前可进入的场地由四个社区变为两个社区。',
};
const digest = value => createHash('sha256').update(typeof value === 'string' ? value : JSON.stringify(value)).digest('hex');
export const fail = (status, message) => { throw Object.assign(new Error(message), { status }); };
const text = (value, max = 8000) => typeof value === 'string' && value.trim().length <= max ? value.trim() : fail(400, '文字为空或超出长度限制');
export async function atomicJSON(file, value) {
  await mkdir(path.dirname(file), { recursive: true });
  const temp = `${file}.${process.pid}.tmp`;
  const handle = await open(temp, 'w');
  try { await handle.writeFile(JSON.stringify(value, null, 2)); await handle.sync(); } finally { await handle.close(); }
  await rename(temp, file);
}

/** Single owned runtime; all changes serialized, version checked and durably recorded. */
export async function createChainStore({ directory, projectId, sources, latestVersion }) {
  const file = path.join(directory, 'review-chain.json');
  let state;
  try { state = JSON.parse(await readFile(file, 'utf8')); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (!state) {
    const source = await sources.uploadText({ title: '社区联系记录-v1.txt', text: SOURCE_TEXT.v1, key: 'initial-contact-v1' });
    const saved = await latestVersion();
    const record = () => ({ decision: null, reason: '', progress: 'awaiting_decision', history: [], baseVersion: saved.diskVersion, baseDocSha256:saved.docSha256 });
    state = {
      schema: 1, baselineId: BASELINE, projectId, revision: 1, sourceLatest: 1, changeApplied: false,
      sourceVersions: { v1: { id:'CONTACT-v1', versionId:'CONTACT-v1', title:'社区联系记录', version:1, text:source.document.text, sha256:digest(source.document.text), ...source }, v2: { id:'CONTACT-v2', versionId:'CONTACT-v2', title:'社区联系记录', version:2, text:SOURCE_TEXT.v2, prepared:true } },
      comment: { id:'C01', text:'说明社区选择标准，不要据此概括全市情况。', author:'导师意见（虚构场景）', excerpt:'据此了解全市老年人使用数字公共服务的情况', createdAt:new Date().toISOString() },
      records: { C01:record(), R1:record(), R2:record(), R3:record(), METHOD:record() },
      dependencies: [{ id:'DEP1', sourceVersion:'CONTACT-v1', target:'SAMPLE', taskId:'R1', reason:'样本场地范围依据联系记录' }, { id:'DEP2', sourceVersion:'CONTACT-v1', target:'SCHEDULE', taskId:'R2', reason:'招募进度依据可进入场地' }, { id:'DEP3', sourceVersion:'CONTACT-v1', target:'FIG01', taskId:'R3', reason:'计划路线图包含四社区招募节点，图片联动尚未接入' }],
      backend: { sources:'Go knowledge assets', document:'Go/SQLite + DS + WordCraft', review:'local-review-chain-v1; durable extension, not legacy Go ImpactTask', ai:'mock', figure:'mock' },
      applied: {}, events: [],
    };
    await atomicJSON(file, state);
  }
  if (state.schema !== 1 || state.projectId !== projectId || state.baselineId !== BASELINE) throw Error('Review store identity mismatch');
  let serial = Promise.resolve();
  const publicState = () => { const { applied, ...visible } = state; return structuredClone(visible); };
  async function action(input) {
    if (!input || !/^[a-zA-Z0-9_-]{8,100}$/.test(input.key || '')) fail(400, '缺少有效请求编号');
    const fingerprint = digest(input);
    if (state.applied[input.key]) {
      if (state.applied[input.key] !== fingerprint) fail(409, '请求编号已被其他内容使用');
      return publicState();
    }
    if (input.expectedRevision !== state.revision) fail(409, '项目记录已更新，请重新读取后核对再提交');
    const next = structuredClone(state);
    const now = new Date().toISOString();
    const id = input.id || 'C01';
    const row = next.records[id];
    if (!row) fail(404, '找不到此处理项');
    if (/^R[123]$/.test(id) && !state.changeApplied) fail(409, '请先登记资料更新，尚无变化任务');
    const event = { at:now, type:input.type, id, actor:'local-user' };
    switch (input.type) {
      case 'publish_source': {
        if (state.changeApplied) fail(409, '这次资料更新已经登记');
        const updatedText = text(input.text === undefined ? SOURCE_TEXT.v2 : input.text, 200000);
        if (!updatedText || updatedText === state.sourceVersions.v1.text) fail(400, '新版本应包含实际变化');
        const source = await sources.uploadText({ title:'社区联系记录-v2.txt', text:updatedText, key:input.key });
        next.sourceVersions.v2 = { ...next.sourceVersions.v2, ...source, text:source.document.text, sha256:digest(source.document.text), prepared:false };
        next.sourceLatest = 2; next.changeApplied = true;
        const saved = await latestVersion();
        for (const dep of next.dependencies) {
          Object.assign(next.records[dep.taskId], { baseVersion:saved.diskVersion, baseDocSha256:saved.docSha256, change:{ from:'CONTACT-v1', to:'CONTACT-v2', dependencyId:dep.id, reason:dep.reason }, createdAt:now });
        }
        Object.assign(event, { from:'CONTACT-v1', to:'CONTACT-v2' });
        break;
      }
      case 'comment': {
        if (id !== 'C01') fail(400, '当前试用支持一条活动人工意见');
        const value = text(input.text);
        if (!value) fail(400, '请填写意见正文');
        const saved = await latestVersion();
        event.previous = structuredClone(next.comment);
        next.comment = { ...next.comment, text:value, author:text(input.author || '人工录入', 120), updatedAt:now };
        Object.assign(row, { decision:null, reason:'', progress:'awaiting_decision', baseVersion:saved.diskVersion, baseDocSha256:saved.docSha256, resultVersion:null });
        Object.assign(event, { text:value, author:next.comment.author });
        break;
      }
      case 'anchor': {
        const a = input.anchor;
        if (!a || !Number.isSafeInteger(a.documentId) || !Number.isSafeInteger(a.revision) || !a.selection || !text(a.excerpt || a.selection.text)) fail(400, '请先在真实正文中框选原文');
        row.anchor = { documentId:a.documentId, revision:a.revision, hostSessionId: typeof a.hostSessionId === 'string' ? text(a.hostSessionId,100) : null, selection:a.selection, excerpt:text(a.excerpt || a.selection.text), status:'user_bound', boundAt:now };
        if (id === 'C01') next.comment.excerpt = row.anchor.excerpt;
        event.anchor = structuredClone(row.anchor);
        break;
      }
      case 'decide': {
        if (!['modify_document','update_plan','adjust_both','no_change','defer'].includes(input.decision)) fail(400, '未知处理决定');
        const reason = text(input.reason || '');
        if (input.decision === 'no_change' && !reason) fail(400, '无需调整必须说明理由');
        Object.assign(row, { decision:input.decision, reason, progress:input.decision === 'no_change' ? 'completed' : input.decision === 'defer' ? 'deferred' : 'pending', resultVersion:null });
        Object.assign(event, { decision:input.decision, reason });
        break;
      }
      case 'complete': {
        if (row.progress !== 'pending' || row.decision !== 'modify_document') fail(409, '请先决定修改正文；研究方案与图源尚无可核对的完成凭据');
        if (id === 'R3') fail(409, '路线图编辑尚未接入，不能用正文保存冒充图源更新');
        const reason = text(input.reason || '');
        if (!reason) fail(400, '请说明本次实际处理的内容');
        const saved = await latestVersion();
        if (!input.savedVersion || saved.diskVersion !== input.savedVersion.diskVersion || saved.docSha256 !== input.savedVersion.docSha256) fail(409, '保存版本与服务器最新版本不一致，请核对');
        if (saved.diskVersion <= row.baseVersion || saved.docSha256 === row.baseDocSha256) fail(409, '尚无这条任务产生后的新正文保存版本');
        // This is explicitly a human assertion, not an automatic semantic judgement.
        row.resultVersion = { diskVersion:saved.diskVersion, versionId:saved.id || saved.versionId, docSha256:saved.docSha256 };
        row.progress = 'completed'; row.completionReason = reason;
        Object.assign(event, { resultVersion:row.resultVersion, reason, humanConfirmed:true });
        break;
      }
      default: fail(400, '未知操作');
    }
    row.history.push(event);
    next.events.push(event); next.revision++;
    next.applied[input.key] = fingerprint;
    await atomicJSON(file, next);
    state = next;
    return publicState();
  }
  return { read:publicState, act(input) { const work = serial.then(() => action(input)); serial = work.catch(() => {}); return work; } };
}
