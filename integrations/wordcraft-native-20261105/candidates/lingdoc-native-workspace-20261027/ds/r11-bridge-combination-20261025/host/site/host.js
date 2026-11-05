import { WorkspaceSaveCoordinator } from './save-coordinator.js';

const config = window.WORDCRAFT_HOST_CONFIG;
const debugMode = new URLSearchParams(location.search).has('debug');
document.body.classList.toggle('debug-mode', debugMode);
const hostDebug = document.querySelector('.host-debug');
if (hostDebug) hostDebug.hidden = !debugMode;
const log = document.getElementById('host-log');
let diskVersion = 0;
let goDiskVersion = null;
let bookmark = null;
let pendingSave = null;
let pendingGoRegistration = null;
let lastSave = null;
const saveSources = [];
let projectId = config.projectId || 'project-1';
let researchPanel = null;
const researchReady = config.researchEnabled
  ? import('./research-panel.js').then(async ({ createResearchPanel }) => {
    researchPanel = createResearchPanel({ config, engine, save: saveIntent,
      canRetryCitation: (cardId, sectionId) => Boolean(rejectedCitationAttempt()?.citations.some(ref => ref.evidenceCardId === cardId && ref.sectionId === sectionId)), retryCitation: retryRejectedCitation });
    const bootstrap = await researchPanel.ready;
    projectId = bootstrap.projectId;
  })
  : Promise.resolve();
researchReady.catch(error => report({ error: error.message, source: 'research-bootstrap' }));
const report = value => { log.textContent = JSON.stringify(value, null, 2); return value; };
let toastTimer = null;
function notify(message, tone = 'success') {
  let toast = document.getElementById('lingdoc-toast');
  if (!toast) {
    toast = document.createElement('div');
    toast.id = 'lingdoc-toast';
    toast.setAttribute('role', 'status');
    toast.setAttribute('aria-live', 'polite');
    document.body.append(toast);
  }
  clearTimeout(toastTimer);
  toast.textContent = message;
  toast.dataset.tone = tone;
  toast.dataset.visible = 'true';
  toastTimer = setTimeout(() => { toast.dataset.visible = 'false'; }, tone === 'error' ? 8000 : 4200);
}

let lastNonemptySelection = null;
async function engine(request) {
  const response = window.wasmBindings.host_request(JSON.stringify(request));
  let timer;
  try {
    const timeout = new Promise((_, reject) => { timer = setTimeout(() => reject(Error('engine bridge timeout')), 15000); });
    const text = await Promise.race([response, timeout]);
    const reply = JSON.parse(text);
    if (!reply.ok) throw Object.assign(Error(reply.error), { reply });
    if (request.op === 'selection' && reply.result.selection?.text?.trim()) lastNonemptySelection = reply.result;
    return reply.result;
  } finally { clearTimeout(timer); }
}

async function api(endpoint, payload) {
  const response = await fetch(`/api/${endpoint}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Host-Token': config.token },
    body: JSON.stringify(payload),
  });
  const data = await response.json();
  if (!response.ok) throw Object.assign(Error(data.error), { status: response.status });
  return data;
}

async function goApi(path, { method = 'GET', body, key, raw = false } = {}) {
  if (!config.goOrigin) throw new Error('WORDCRAFT_GO_ORIGIN is required for the save version chain');
  const headers = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (key) headers['Idempotency-Key'] = key;
  const response = await fetch(`${config.goOrigin}/api/docx/projects/${encodeURIComponent(projectId)}/${path}`, {
    method, headers, body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (raw) {
    if (!response.ok) throw Object.assign(new Error(`Go HTTP ${response.status}`), { status: response.status });
    return response;
  }
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw Object.assign(new Error(payload.message || payload.error || `Go HTTP ${response.status}`), {
    status: response.status, code: payload.error, details: payload.details,
  });
  return payload;
}

const hash = async bytes => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', new Uint8Array(bytes)))).map(x => x.toString(16).padStart(2, '0')).join('');

async function latestSavedVersion() {
  try {
    const version = await goApi('versions/latest');
    goDiskVersion = version.diskVersion;
    return version;
  } catch (error) {
    if (error.status === 404 && error.code === 'NOT_FOUND') { goDiskVersion = 0; return null; }
    throw error;
  }
}

const coordinator = new WorkspaceSaveCoordinator({
  async persistSavedFile() {
    if (!pendingSave) {
      const state = await engine({ op: 'status' });
      const citations = researchPanel?.snapshot() ?? [];
      const exported = await engine({ op: 'export', expectedDocumentId: state.documentId, expectedRevision: state.revision, expectedDiskVersion: diskVersion });
      const sha256 = await hash(exported.bytes);
      if (sha256 !== exported.sha256) throw new Error('engine serialization digest mismatch');
      pendingSave = {
        state, exported, citations,
        payload: { bytes: exported.bytes, sha256, saveToken: exported.saveToken, documentId: state.documentId, revision: state.revision, expectedDiskVersion: diskVersion },
      };
    }
    // An unknown DS result retries the original bytes, identity and saveToken only.
    const { payload } = pendingSave;
    const receipt = pendingSave.receipt ?? await api('save', payload);
    if (receipt.sha256 !== payload.sha256 || receipt.bytes !== payload.bytes.length || receipt.saveToken !== payload.saveToken || receipt.documentId !== payload.documentId || receipt.revision !== payload.revision || receipt.baseDiskVersion !== payload.expectedDiskVersion || !receipt.verified) throw new Error('unverified host receipt');
    diskVersion = receipt.diskVersion;
    pendingSave.receipt = receipt;
    return { receipt };
  },
  async registerSavedVersion(receipt) {
    if (!pendingGoRegistration || pendingGoRegistration.saveToken !== receipt.saveToken) {
      const historical = await api('version', { diskVersion: receipt.diskVersion });
      if (historical.sha256 !== receipt.sha256) throw new Error('DS 历史版本摘要与保存回执不一致，未送入 Go');
      pendingGoRegistration = {
        saveToken: receipt.saveToken,
        key: `go-save-${receipt.diskVersion}-${receipt.sha256.slice(0, 32)}`,
        expectedDiskVersion: goDiskVersion,
        data: historical.data,
        citations: pendingSave?.citations ?? [],
      };
    }
    // The same idempotency key always replays the exact same expected version and bytes.
    const cached = pendingGoRegistration;
    // Only the response to THIS attempt can authorize a changed-payload retry.
    // A timeout after an earlier 422 must never inherit its rejection status.
    delete cached.rejection;
    let result;
    try {
      result = await goApi('versions', {
        method: 'POST', key: cached.key,
        body: { expectedDiskVersion: cached.expectedDiskVersion, data: cached.data,
          ...(researchPanel ? { citations: cached.citations } : {}) },
      });
    } catch (error) {
      if (error.status === 422 && error.code === 'CITATION_INVALID') {
        cached.rejection = { key: cached.key, identity: JSON.stringify([cached.expectedDiskVersion, cached.data, cached.citations]) };
        error.message = '文件已落盘，但引用登记被拒绝。请在资料面板重新核对并选择证据卡，补登记这个已保存版本。' + error.message;
      }
      throw error;
    }
    cached.registered = true;
    goDiskVersion = result.version.diskVersion;
    return result;
  },
  async deliveryStatus(id) { return goApi(`versions/${encodeURIComponent(id)}/delivery`); },
  async ackSavedRevision(receipt) {
    return engine({
      op: 'ack', expectedDocumentId: receipt.documentId, expectedRevision: receipt.revision,
      saveToken: receipt.saveToken, sha256: receipt.sha256, diskVersion: receipt.diskVersion, verified: true,
    });
  },
  releaseSavedRequest(receipt) {
    if (pendingSave?.payload.saveToken === receipt.saveToken) pendingSave = null;
    if (pendingGoRegistration?.saveToken === receipt.saveToken) pendingGoRegistration = null;
  },
  async status() { return engine({ op: 'status' }); },
});

function rejectedCitationAttempt() {
  const cached = pendingGoRegistration;
  if (coordinator.transaction || coordinator.nextTransaction || coordinator.version || !coordinator.receipt ||
      !cached?.rejection || cached.registered || cached.saveToken !== coordinator.receipt.saveToken ||
      cached.rejection.key !== cached.key ||
      cached.rejection.identity !== JSON.stringify([cached.expectedDiskVersion, cached.data, cached.citations])) return null;
  return cached;
}

async function retryRejectedCitation(selected) {
  const cached = rejectedCitationAttempt();
  if (!cached || !selected.length) throw new Error('只有已明确拒绝且没有进行中的保存，才可重新选择引用关联。');
  const sameCard = (left, right) => left.sectionId === right.sectionId && left.sourceId === right.sourceId && left.evidenceCardId === right.evidenceCardId;
  if (selected.some(ref => !cached.citations.some(old => sameCard(old, ref)))) throw new Error('此处仅可更新原引用卡片的版本，不能替换为其他卡片或来源。');
  const citations = cached.citations.filter(old => !selected.some(ref => sameCard(old, ref)));
  citations.push(...selected);
  pendingGoRegistration = { ...cached, citations, key: 'go-citation-retry-' + crypto.randomUUID(),
    rejectedAttempt: { key: cached.key, citations: cached.citations }, rejection: undefined, registered: false };
  // DS receipt/bytes and expected Go version are unchanged. The coordinator
  // resumes at Go registration and retains the original revision ack rules.
  return saveIntent('citation-retry');
}

async function open() {
  await researchReady;
  const file = await api('open', {});
  const result = await engine({ op: 'open', data: file.data });
  diskVersion = file.diskVersion;
  bookmark = null;
  pendingSave = null;
  pendingGoRegistration = null;
  lastSave = null;
  deliveryError = null;
  const latest = await latestSavedVersion();
  researchPanel?.restore(latest?.citations ?? []);
  if (latest && file.receipt?.sha256 === latest.docSha256) {
    const delivery = await goApi(`versions/${encodeURIComponent(latest.id)}/delivery`);
    lastSave = { receipt: file.receipt, version: latest, delivery, live: await engine({ op: 'status' }), ackPending: false };
  }
  return report({ opened: result, sourceSha256: file.sha256, diskVersion, goDiskVersion, savedVersionId: lastSave?.version.id ?? null, receipt: file.receipt });
}

async function selection() { bookmark = await engine({ op: 'selection' }); return report(bookmark); }
async function insert(text = '宿主合成文本 HOST-R4-20261024') {
  if (!bookmark) throw Error('Read a canvas selection first');
  const result = await engine({ op: 'insert', text, bookmark: bookmark.selection, expectedDocumentId: bookmark.state.documentId, expectedRevision: bookmark.state.revision });
  bookmark = null;
  return report(result);
}

async function saveIntent(source = 'host-api') {
  if (!saveSources.includes(source)) saveSources.push(source);
  const current = await engine({ op: 'status' });
  try {
    const result = await coordinator.request(current.revision);
    if (lastSave?.version?.id !== result.version?.id) deliveryError = null;
    lastSave = result;
    researchPanel?.acceptSaved(result.version?.citations ?? []);
    notify(result.ackPending || result.live?.dirty
      ? '当前版本已保存；编辑器中仍有新修改，请再保存一次。'
      : '文稿与引用已保存');
    return report({
      receipt: result.receipt, version: result.version, delivery: result.delivery,
      live: result.live, ackPending: result.ackPending, source,
    });
  } catch (error) {
    report({ error: error.message, source, saveFacts: error.saveFacts ?? null });
    researchPanel?.saveFailure(error);
    notify('保存未完成：' + error.message, 'error');
    throw error;
  }
}

let pendingSaveAs = null;
let lastSaveAs = null;

// Used when the embedded browser cannot show a native file picker. The browser
// manages the download directory; the user can still name or cancel the copy.
function pickDownloadName(suggestedName) {
  return new Promise(resolve => {
    const dialog = document.createElement('dialog');
    dialog.id = 'lingdoc-save-as-dialog';
    dialog.style.cssText = 'width:440px;max-width:90vw;border:0;border-radius:12px;padding:24px;box-shadow:0 20px 70px #0005;color:#1f2328';
    dialog.innerHTML = `<form><h2 style="margin-top:0">另存为 DOCX</h2>
      <label>文件名<input id="save-as-name" required maxlength="180" style="display:block;width:95%;margin:12px 0;padding:8px"></label>
      <p>保存后下载一份 DOCX 副本，下载位置由浏览器管理。</p>
      <button id="save-as-download" type="submit">保存并下载</button>
      <button id="save-as-cancel" type="button">取消</button></form>`;
    const input = dialog.querySelector('#save-as-name');
    input.value = suggestedName;
    const finish = name => { dialog.close(); dialog.remove(); resolve(name); };
    dialog.querySelector('#save-as-cancel').onclick = () => finish(null);
    dialog.oncancel = event => { event.preventDefault(); finish(null); };
    input.oninput = () => input.setCustomValidity('');
    dialog.querySelector('form').onsubmit = event => {
      event.preventDefault();
      let name = input.value.trim();
      if (name && !name.includes('.')) name += '.docx';
      if (!name || /[<>:"/\\|?*\u0000-\u001f]/.test(name) || !/\.docx$/i.test(name)) {
        input.setCustomValidity('请输入有效的 DOCX 文件名，例如“申请书副本.docx”。'); input.reportValidity(); return;
      }
      finish(name);
    };
    document.body.append(dialog); dialog.showModal(); input.focus(); input.select();
  });
}

async function saveAsCopy(suggestedName) {
  let name = String(suggestedName || '申请书副本.docx').split(/[\\/]/).pop();
  if (!/\.docx$/i.test(name)) name = '申请书副本.docx';
  let handle;
  // Pick BEFORE any export or transaction: cancelling must leave the document,
  // DS disk version and Go version exactly as they were.
  if (typeof window.showSaveFilePicker === 'function') {
    try {
      handle = await window.showSaveFilePicker({ suggestedName: name, types: [{ description: 'DOCX', accept: {
        'application/vnd.openxmlformats-officedocument.wordprocessingml.document': ['.docx'],
      } }], excludeAcceptAllOption: true });
      name = handle.name;
      if (!/\.docx$/i.test(name)) throw new Error('另存为文件名必须以 .docx 结尾');
    } catch (error) {
      if (error.name === 'AbortError') return report({ cancelled: true, source: 'save-as' });
      if (!['SecurityError', 'NotAllowedError'].includes(error.name)) throw error;
    }
  }
  if (!handle) {
    name = await pickDownloadName(name);
    if (name === null) return report({ cancelled: true, source: 'save-as' });
  }
  const saved = await saveIntent('save-as');
  try {
    const historical = await api('version', { diskVersion: saved.receipt.diskVersion });
    const bytes = Uint8Array.from(atob(historical.data), c => c.charCodeAt(0));
    const digest = await hash(bytes);
    if (digest !== saved.receipt.sha256 || digest !== saved.version.docSha256) throw new Error('副本字节与保存版本不一致，已停止另存为');
    if (handle) {
      const writable = await handle.createWritable();
      try { await writable.write(bytes); await writable.close(); }
      catch (error) { await writable.abort().catch(() => {}); throw error; }
    } else {
      const url = URL.createObjectURL(new Blob([bytes], { type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' }));
      const anchor = document.createElement('a');
      anchor.href = url; anchor.download = name; document.body.append(anchor); anchor.click(); anchor.remove();
      setTimeout(() => URL.revokeObjectURL(url), 10000);
    }
    lastSaveAs = { name, sha256: digest, versionId: saved.version.id, method: handle ? 'file-picker' : 'browser-download' };
    return report({ ...saved, copy: lastSaveAs, source: 'save-as' });
  } catch (error) {
    throw new Error(`项目版本已保存，但另存副本失败：${error.message}`);
  }
}

function saveAsIntent(name) {
  return pendingSaveAs ??= saveAsCopy(name).finally(() => { pendingSaveAs = null; });
}

let deliveryDialog = null;
let deliveryBusy = false;
let deliveryError = null;
let lastExport = null;
const deliverySections = [
  ['topic-summary', '选题说明'], ['topic-basis', '选题依据'], ['research-content', '研究内容'],
  ['innovation', '创新之处'], ['expected-results', '预期成果'], ['references', '参考文献'],
];

function ensureDeliveryDialog() {
  if (deliveryDialog) return deliveryDialog;
  deliveryDialog = document.createElement('dialog');
  deliveryDialog.id = 'lingdoc-delivery-dialog';
  deliveryDialog.style.cssText = 'width:560px;max-width:90vw;border:0;border-radius:12px;padding:24px;box-shadow:0 20px 70px #0005;color:#1f2328';
  deliveryDialog.innerHTML = `
    <h2 style="margin:0 0 8px">准备交付</h2>
    <p id="delivery-version" style="margin:0 0 16px;color:#59636e"></p>
    <p id="delivery-status" role="status" style="white-space:pre-line">读取指定保存版本…</p>
    <div id="delivery-chapters">${deliverySections.map(([sectionId, title]) => `<button id="delivery-confirm-${sectionId}" type="button" style="display:block;width:100%;margin:8px 0;text-align:left">确认${title}</button>`).join('')}</div>
    <div style="display:flex;gap:12px;margin-top:20px">
      <button id="delivery-freeze" type="button">冻结六章确认后的版本</button>
      <button id="delivery-export" type="button">导出指定版本 DOCX</button>
      <button id="delivery-close" type="button">关闭</button>
    </div>`;
  document.body.append(deliveryDialog);
  deliveryDialog.querySelector('#delivery-close').onclick = () => deliveryDialog.close();
  deliveryDialog.querySelector('#delivery-freeze').onclick = () => freezeDelivery().catch(showDeliveryError);
  deliveryDialog.querySelector('#delivery-export').onclick = () => exportDelivery().catch(showDeliveryError);
  for (const [sectionId] of deliverySections) {
    deliveryDialog.querySelector(`#delivery-confirm-${sectionId}`).onclick = () => confirmDeliveryChapter(sectionId).catch(error => showDeliveryError(error, sectionId));
  }
  return deliveryDialog;
}

function showDeliveryError(error, sectionId) {
  deliveryBusy = false;
  const title = id => deliverySections.find(([key]) => key === id)?.[1];
  const details = Array.isArray(error.details) ? error.details.filter(item => typeof item?.message === 'string') : [];
  const fallback = {
    RULE_CHECK_FAILED: '当前保存版本未通过交付检查，服务端未提供具体原因。',
    SOURCE_INVALID: '引用的来源不可用，请检查来源后再确认。',
    VERSION_CHANGED: '保存版本已变化，请关闭此窗口并重新准备交付。',
    PERMISSION_DENIED: '当前账号无权完成此操作，请检查项目权限。',
  };
  const messages = details.length ? details.map(item => {
    const section = title(item.sectionId || sectionId);
    return `${section ? `${section}：` : ''}${item.message}`;
  }) : [`${title(sectionId) ? `${title(sectionId)}：` : ''}${fallback[error.code] || error.message}`];
  deliveryError = `${messages.join('\n')}${error.code ? `\n错误代码：${error.code}` : ''}`;
  renderDeliveryDialog();
}

function renderDeliveryDialog() {
  const dialog = ensureDeliveryDialog();
  const delivery = lastSave?.delivery;
  const chapters = delivery?.chapters ?? [];
  const confirmed = chapters.filter(chapter => chapter.confirmed).length;
  const supported = lastSave?.version?.projection?.sections?.length === 6 && deliverySections.every(([id]) => lastSave.version.projection.sections.some(section => section.sectionId === id));
  dialog.querySelector('#delivery-version').textContent = lastSave
    ? debugMode ? `保存版本 ${lastSave.version.id} · ${lastSave.version.docSha256}` : '当前保存版本已校验'
    : '尚未保存';
  const status = !supported
    ? '当前 DOCX 不匹配六章申请书结构，可以编辑，但不能完成本版交付。'
    : delivery?.snapshot
      ? delivery.deliveryEligible
        ? `已确认 ${confirmed}/6 章 · 冻结快照 ${delivery.snapshot.id}`
        : `已确认 ${confirmed}/6 章 · 交付检查未通过，暂不能导出`
      : `已确认 ${confirmed}/6 章 · 确认只绑定当前保存版本`;
  // A blocked freeze is HTTP 200 with a diagnostic snapshot, not an exception.
  // Derive its explanation from persisted facts so reopening cannot hide it.
  const snapshot = delivery?.snapshot;
  const issues = snapshot && !delivery.deliveryEligible ? (snapshot.check?.issues ?? []).filter(item => item.severity === 'blocking') : [];
  const explanations = issues.map(item => {
    const chapter = chapters.find(chapter => chapter.chapterId === item.target_id || `chapter/${chapter.sectionId}` === item.target_id);
    const title = chapter && deliverySections.find(([id]) => id === chapter.sectionId)?.[1];
    const message = item.message === '正文引用标记必须与 source_ids 完全一致'
      ? '正文引用与已登记的来源不一致，请核对来源关联并重新保存。'
      : item.message || '交付规则未通过，请核对要求。';
    return `${title ? `${title}：` : ''}${message}`;
  });
  if (snapshot && !delivery.deliveryEligible && !explanations.length) {
    explanations.push(snapshot.is_current === false ? '冻结版本已失效，请重新保存并准备交付。' : '当前版本未通过交付检查，请核对后重新保存。');
  }
  dialog.querySelector('#delivery-status').textContent = [status, deliveryError, explanations.join('\n')].filter(Boolean).join('\n\n');
  for (const [sectionId, title] of deliverySections) {
    const chapter = chapters.find(item => item.sectionId === sectionId);
    const button = dialog.querySelector(`#delivery-confirm-${sectionId}`);
    button.textContent = chapter?.confirmed ? `✓ ${title}（已确认）` : `确认${title}`;
    button.disabled = deliveryBusy || !supported || chapter?.confirmed !== false;
  }
  dialog.querySelector('#delivery-freeze').disabled = deliveryBusy || !supported || confirmed !== 6 || Boolean(delivery?.snapshot);
  dialog.querySelector('#delivery-export').disabled = deliveryBusy || !delivery?.deliveryEligible;
}

async function refreshDelivery() {
  if (!lastSave?.version) throw new Error('请先保存当前 DOCX');
  const delivery = await goApi(`versions/${encodeURIComponent(lastSave.version.id)}/delivery`);
  if (delivery.version.id !== lastSave.version.id || delivery.version.docSha256 !== lastSave.receipt.sha256) throw new Error('交付读回与指定保存版本不一致');
  lastSave = { ...lastSave, delivery };
  renderDeliveryDialog();
}

async function prepareDelivery() {
  deliveryError = null;
  const dialog = ensureDeliveryDialog();
  if (!dialog.open) dialog.showModal();
  deliveryBusy = true;
  renderDeliveryDialog();
  try {
    await saveIntent('prepare-delivery');
    lastExport = null;
    await refreshDelivery();
  } catch (error) { showDeliveryError(error); }
  finally { deliveryBusy = false; renderDeliveryDialog(); }
}

async function confirmDeliveryChapter(sectionId) {
  deliveryError = null;
  if (!lastSave?.delivery || !lastSave?.version) throw new Error('交付版本尚未读回');
  const section = lastSave.version.projection.sections.find(item => item.sectionId === sectionId);
  if (!section) throw new Error('保存版本中不存在该章节');
  deliveryBusy = true; renderDeliveryDialog();
  try {
    const result = await goApi('confirmations', {
      method: 'POST', key: `confirm-${lastSave.version.id}-${sectionId}`,
      body: {
        savedVersionId: lastSave.version.id,
        expectedDocSha256: lastSave.version.docSha256,
        expectedManifestHash: lastSave.version.manifestHash,
        expectedSectionDeliveryHash: section.sectionDeliveryHash,
        sectionId, expectedSpecRevision: lastSave.delivery.specRevision, decisions: [],
      },
    });
    if (result.confirmation?.valid !== true) throw new Error('服务端未接受本章确认');
    await refreshDelivery();
  } finally { deliveryBusy = false; renderDeliveryDialog(); }
}

async function freezeDelivery() {
  deliveryError = null;
  if (!lastSave?.version) throw new Error('请先保存当前 DOCX');
  deliveryBusy = true; renderDeliveryDialog();
  try {
    await goApi('freezes', { method: 'POST', key: `freeze-${lastSave.version.id}`, body: { savedVersionId: lastSave.version.id } });
    await refreshDelivery();
  } finally { deliveryBusy = false; renderDeliveryDialog(); }
}

async function exportDelivery() {
  deliveryError = null;
  const snapshotId = lastSave?.delivery?.snapshot?.id;
  if (!snapshotId || !lastSave.delivery.deliveryEligible) throw new Error('当前版本尚不具备交付条件');
  deliveryBusy = true; renderDeliveryDialog();
  try {
    const result = await goApi('exports', { method: 'POST', key: `export-${snapshotId}`, body: { snapshotId } });
    if (result.export?.status !== 'verified') throw new Error('导出文件尚未通过服务端校验');
    const response = await goApi(`exports/${encodeURIComponent(result.export.id)}`, { raw: true });
    const blob = await response.blob();
    const bytes = new Uint8Array(await blob.arrayBuffer());
    const digest = await hash(bytes);
    if (digest !== result.export.file_sha256 || digest !== lastSave.version.docSha256) throw new Error('下载字节不是指定冻结版本');
    lastExport = result.export;
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url; anchor.download = `lingdoc-${result.export.id}.docx`; anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } finally { deliveryBusy = false; renderDeliveryDialog(); }
}

window.wordcraftHost = {
  engine, api, goApi, open, selection, insert, saveIntent, saveAsIntent, prepareDelivery, hash,
  get research() { return researchPanel; },
  get lastNonemptySelection() { return lastNonemptySelection; },
  save: () => saveIntent('host-api'),
  get diskVersion() { return diskVersion; },
  get goDiskVersion() { return goDiskVersion; },
  get lastSave() { return lastSave; },
};
window.lingdocDiagnostics = () => ({
  transactionActive: Boolean(coordinator.transaction || coordinator.nextTransaction),
  saveSources: [...saveSources], lastSave, lastExport, lastSaveAs, saveAsPending: Boolean(pendingSaveAs), diskVersion, goDiskVersion,
});

for (const [id, fn] of Object.entries({ 'host-open': open, 'host-selection': selection, 'host-insert': () => insert() })) {
  document.getElementById(id).onclick = () => fn().catch(error => report({ error: error.message, state: error.reply?.state }));
}
document.getElementById('host-save').onclick = () => saveIntent('host-button').catch(() => {});

async function boot() {
  for (let i = 0; i < 300; i++) {
    if (window.WORDCRAFT_FONT_READY?.error) { report({ error: window.WORDCRAFT_FONT_READY.error }); return; }
    if (window.WORDCRAFT_FONT_READY?.ok && window.wasmBindings?.host_request) {
      try { await engine({ op: 'status' }); await open(); return; }
      catch (error) { if (i === 299) report({ error: error.message }); }
    }
    await new Promise(resolve => setTimeout(resolve, 200));
  }
  report({ error: 'Engine and required local font did not become ready' });
}
boot();

document.addEventListener('keydown', event => {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
    event.preventDefault(); event.stopImmediatePropagation();
    saveIntent(window.parent === window ? 'parent-keyboard' : 'iframe-keyboard').catch(() => {});
  }
}, true);
