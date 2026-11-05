import { compatibleTurnIds, historyNotice, makeSelectionSnapshot } from './workspace-assistant-state.js';
import { pendingSourceRequestFields, refreshSourceExcerpt, resolveHistoricalSource, sourceRequestFields } from './source-document-page.js';

export function createWorkspaceAssistant({ root, request, projectPath, expectation, engine, action,
  openSource, openSourcePicker, upload, closeWorkspace, openWorkspace }) {
  const $ = id => root.querySelector('#assistant-' + id);
  const contextSelectionEnabled = false;
  const rows = value => Array.isArray(value) ? value : value?.items ?? value?.data ?? [];
  let sessionId = null, turns = [], sessions = [], selection = null, sources = [], pending = null;
  let contextRevision = 0, historySources = new Map(), historyViews = new Map(), storageKey = null;
  let lastSessionKey = null;

  function persist() {
    if (!sessionId) return;
    localStorage.setItem(lastSessionKey, sessionId);
    localStorage.setItem(storageKey, JSON.stringify({ selection, sources, pending, draft: $('question').value }));
  }
  function restoreLocal(id) {
    sessionId = id;
    storageKey = 'lingdoc-workspace-assistant:' + id;
    selection = null; sources = []; pending = null;
    const stored = localStorage.getItem(storageKey);
    if (!stored) return;
    try {
      const value = JSON.parse(stored);
      selection = value.selection ?? null;
      sources = value.sources ?? [];
      pending = value.pending ?? null;
      $('question').value = value.draft ?? '';
    } catch { /* Ignore an invalid local draft; server history remains authoritative. */ }
  }
  function notice(text, error = false) {
    $('status').textContent = text;
    $('status').classList.toggle('error', error);
  }
  async function sha256(text) {
    const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text));
    return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('');
  }
  function appendButton(container, text, run, className = '') {
    const button = document.createElement('button');
    button.type = 'button'; button.textContent = text; button.className = className;
    button.onclick = () => action(run); container.append(button); return button;
  }
  function paragraph(container, text, className = '') {
    const element = document.createElement('p');
    element.textContent = text; element.className = className; container.append(element); return element;
  }
  function contextDetails(container, context, restricted = false) {
    const detail = document.createElement('details');
    const summary = document.createElement('summary');
    const count = (context.source_refs?.length ?? 0) + (context.document_selection ? 1 : 0);
    summary.textContent = count ? `查看本轮上下文（${count} 项）` : '本轮未附加资料';
    detail.append(summary);
    if (context.document_selection && !restricted) {
      paragraph(detail, `正文选区 · ${Array.from(context.document_selection.text).length} 字符 · 修订 ${context.document_selection.document_revision}`, 'assistant-context-label');
      paragraph(detail, context.document_selection.text, 'assistant-context-text');
    }
    for (const ref of context.source_refs ?? []) {
      paragraph(detail, ref.locator || '资料片段', 'assistant-context-label');
      const source = historySources.get(ref.source_id);
      const view = historyViews.get(ref) ?? { text: '原文当前不可读取。', verified: false, changed: false };
      paragraph(detail, view.text, 'assistant-context-text');
      if (source && !restricted) appendButton(detail, view.verified ? '回看原文' : '查看当前原文',
        () => openSource(ref.source_id, view.verified ? view.text : undefined), 'research-text-button');
    }
    container.append(detail);
  }
  function reasonText(reasons) {
    const labels = { selection_changed: '正文选区已更换', sources_changed: '资料或授权已变化', context_changed: '项目上下文已变化' };
    return Object.entries(reasons ?? {}).map(([key, count]) => `${labels[key] || key} ${count} 轮`).join('；');
  }
  function renderHistoryPreview() {
    if (hasPausedContext()) {
      $('history-notice').textContent = '此对话为只读，原附件与待确认请求保留。新建对话可直接提问。';
      return;
    }
    const included = compatibleTurnIds(turns, selection, sources, contextRevision);
    const usage = historyNotice(turns, included, selection, sources, contextRevision);
    $('history-notice').textContent = usage.excluded
      ? `本轮不发送 ${usage.excluded} 轮旧内容：${reasonText(usage.reasons)}。可查看下方实际附件。`
      : `本轮携带 ${included.length} 轮兼容历史；附件会随后续提问保留。`;
  }
  function renderContext() {
    $('context').replaceChildren();
    if (!selection && !sources.length) paragraph($('context'), '未添加附件，也可以直接聊天。', 'assistant-empty');
    if (selection) {
      const item = document.createElement('article');
      paragraph(item, `${selection.locator || '正文选区'} · ${Array.from(selection.text).length} 字符`, 'assistant-context-label');
      const detail = document.createElement('details');
      const summary = document.createElement('summary'); summary.textContent = '查看完整选区'; detail.append(summary);
      paragraph(detail, selection.text, 'assistant-context-text'); item.append(detail);
      appendButton(item, '移除选区', async () => { selection = null; persist(); render(); }, 'research-text-button');
      $('context').append(item);
    }
    const groups = new Map();
    for (const source of sources) {
      const key = source.asset_id ?? source.id;
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(source);
    }
    for (const excerpts of groups.values()) {
      const item = document.createElement('article');
      const title = excerpts[0].locator?.split(' · ')[0] || '资料';
      paragraph(item, `${title} · 已选 ${excerpts.length} 段`, 'assistant-context-label');
      for (const source of excerpts) {
        const detail = document.createElement('details');
        const summary = document.createElement('summary'); summary.textContent = `查看完整片段（${Array.from(source.quoted_text).length} 字符）`;
        detail.append(summary); paragraph(detail, source.quoted_text, 'assistant-context-text'); item.append(detail);
        appendButton(item, '移除资料', async () => { sources = sources.filter(item => item.id !== source.id); persist(); render(); }, 'research-text-button');
      }
      $('context').append(item);
    }
  }
  function renderSessions() {
    $('sessions').replaceChildren();
    if (!sessionId) {
      const option = document.createElement('option'); option.value = ''; option.textContent = '新对话'; $('sessions').append(option);
    }
    for (const session of sessions) {
      const option = document.createElement('option');
      option.value = session.id; option.textContent = session.title || '历史对话'; option.selected = session.id === sessionId;
      $('sessions').append(option);
    }
  }
  function renderMessages() {
    $('messages').replaceChildren();
    if (!turns.length) paragraph($('messages'), '可以直接提问。资料和正文选区暂不加入对话；历史出处仍可只读查看。', 'assistant-empty');
    for (const turn of turns) {
      const item = document.createElement('article'); item.className = 'assistant-turn'; item.dataset.turnId = turn.id;
      paragraph(item, turn.user_input || '这条消息当前不可读取', 'assistant-question');
      if (turn.status === 'completed') paragraph(item, turn.reply, 'assistant-answer');
      else paragraph(item, turn.status === 'running' ? '这次请求仍待确认，可读取已有回答。' : `这次回答不可用：${turn.failure_code || turn.status}`, 'error');
      const suggestions = turn.suggestions || {};
      for (const [index, citation] of (suggestions.citations ?? []).entries()) {
        paragraph(item, '“' + citation.quote + '”', 'research-ai-quote');
        appendButton(item, `出处 ${index + 1} · 回看原文`, () => openSource(citation.source_id, citation.quote), 'research-text-button');
      }
      if (suggestions.history_usage) {
        const usage = suggestions.history_usage;
        const parts = [`携带 ${usage.included_turns} 轮历史`];
        if (usage.excluded_turns) parts.push(`排除 ${usage.excluded_turns} 轮（${reasonText(usage.reasons)}）`);
        if (usage.truncated_turns) parts.push(`长度限制省略 ${usage.truncated_turns} 轮`);
        paragraph(item, parts.join(' · '), 'assistant-history-usage');
      }
      contextDetails(item, turn.context ?? {}, turn.status === 'restricted');
      if (turn.status === 'completed') appendButton(item, '复制回答', async () => {
        await navigator.clipboard.writeText(turn.reply); notice('回答已复制。');
      }, 'research-text-button');
      $('messages').append(item);
    }
  }
  function render() {
    $('selection').textContent = selection ? '更新正文选区' : '添加正文选区';
    $('send').disabled = hasPausedContext();
    $('release').hidden = !pending;
    $('send').textContent = hasPausedContext() ? '只读对话' : '发送';
    renderSessions(); renderContext(); renderMessages(); renderHistoryPreview();
  }
  async function refreshSources() {
    const previous = sources;
    const restored = await Promise.allSettled(previous.map(source => request(projectPath('/sources/' + encodeURIComponent(source.id)))));
    sources = restored.flatMap((item, index) => item.status === 'fulfilled'
      ? [refreshSourceExcerpt(previous[index], item.value)] : []);
    if (restored.some(item => item.status === 'rejected')) notice('部分附件已不可读取，发送前请移除失效资料。', true);
  }
  async function loadTurns() {
    turns = sessionId ? rows(await request(projectPath('/agent-sessions/' + encodeURIComponent(sessionId) + '/turns'))) : [];
    const ids = new Set();
    for (const turn of turns) for (const ref of turn.context?.source_refs ?? []) ids.add(ref.source_id);
    const loaded = await Promise.allSettled([...ids].map(id => request(projectPath('/sources/' + encodeURIComponent(id)))));
    historySources = new Map(loaded.filter(item => item.status === 'fulfilled').map(item => [item.value.id, item.value]));
    historyViews = new Map();
    const refs = turns.flatMap(turn => (turn.context?.source_refs ?? []).map(ref => ({ ref, restricted: turn.status === 'restricted' })));
    await Promise.all(refs.map(async ({ ref, restricted }) => {
      historyViews.set(ref, await resolveHistoricalSource(ref, historySources.get(ref.source_id), { restricted }));
    }));
    if (pending?.turnId) {
      const current = turns.find(turn => turn.id === pending.turnId);
      if (current && current.status !== 'running') { pending = null; persist(); }
    }
  }
  async function loadSessions() {
    sessions = rows(await request(projectPath('/agents/workspace_assistant/sessions')))
      .sort((a, b) => b.created_at.localeCompare(a.created_at)).slice(0, 12);
    await Promise.all(sessions.map(async session => {
      const items = rows(await request(projectPath('/agent-sessions/' + encodeURIComponent(session.id) + '/turns')));
      session.title = items.find(turn => turn.user_input)?.user_input?.slice(0, 28) || '历史对话';
    }));
  }
  async function step(name, suffix, bodyFactory) {
    let operation = pending.steps[name];
    if (!operation) {
      operation = { body: await bodyFactory(), key: crypto.randomUUID() };
      pending.steps[name] = operation; persist();
    }
    if (operation.done) return operation.value;
    const value = await request(projectPath(suffix), { method: 'POST', body: operation.body, key: operation.key });
    operation.done = true; operation.value = value; persist(); return value;
  }
  function hasPausedContext() {
    return !!(selection || sources.length || pending || turns.some(turn =>
      turn.context?.document_selection || turn.context?.source_refs?.length));
  }
  async function ask() {
    // Paused UI capability: never replay an old attachment/pending payload or
    // silently carry a source-bearing conversation into a new model request.
    if (hasPausedContext()) {
      throw new Error('资料上下文已暂停。此对话保留为只读，请读取已有回答，或新建对话直接提问。原附件与待确认请求已保留。');
    }
    if (!pending) {
      const question = $('question').value.trim();
      if (!question || Array.from(question).length > 4000) throw new Error('请输入 1–4000 字符的问题。');
      if (sources.length > 4 || sources.reduce((sum, source) => sum + Array.from(source.quoted_text).length, 0) > 12000) throw new Error('所选资料超过 4 段或 12000 字符。');
      const requestFields = sourceRequestFields(sources);
      pending = { question, sourceIds: sources.map(source => source.id),
        sourceHashes: Object.fromEntries(sources.map(source => [source.id, source.quoted_text_hash])),
        requestSourceIds: requestFields.source_ids, sourceExcerpts: requestFields.source_excerpts,
        documentSelection: selection, steps: {} };
      persist();
    }
    // Once the model operation exists, replay it before reading any new context.
    if (!pending.model) {
      const currentSources = await Promise.all(pending.sourceIds.map(id => request(projectPath('/sources/' + encodeURIComponent(id)))));
      if (currentSources.some(source => source.quoted_text_hash !== pending.sourceHashes[source.id])) throw new Error('资料已变化，请重新添加后再发送。');
      for (const assetId of new Set(currentSources.map(source => source.asset_id))) {
        const policy = await request(projectPath('/assets/' + encodeURIComponent(assetId) + '/policy'));
        if (policy.model_use_allowed) continue;
        const draft = await step('grant:' + assetId, '/assets/' + encodeURIComponent(assetId) + '/policy/proposals', async () => ({
          ...await expectation(policy.version), confirmed: true, reason: '用户明确允许所选资料用于工作区助手。',
          policy: { ...policy, model_use_allowed: true, purpose: '用户选择片段后用于多轮工作区聊天', reason: '用户明确允许 DeepSeek 阅读所选资料。' },
        }));
        await step('assess:' + assetId, '/change-sets/' + encodeURIComponent(draft.id) + '/assess', async () => ({}));
        await step('apply:' + assetId, '/change-sets/' + encodeURIComponent(draft.id) + '/apply', async () => ({}));
      }
      if (!sessionId) {
        const session = await step('session', '/agents/workspace_assistant/sessions', () => expectation());
        sessionId = session.id;
        storageKey = 'lingdoc-workspace-assistant:' + sessionId;
        sessions.unshift({ ...session, title: pending.question.slice(0, 28) });
        persist();
      }
      pending.model = { key: crypto.randomUUID(), body: { ...await expectation(), user_input: pending.question,
        ...pendingSourceRequestFields(pending), card_refs: [], document_selection: pending.documentSelection ?? undefined,
        sensitive_context_confirmed: true } };
      persist();
    }
    notice('DeepSeek 正在回答；本次不会修改正文。');
    const turn = await request(projectPath('/agent-sessions/' + encodeURIComponent(sessionId) + '/turns'), {
      method: 'POST', ...pending.model, timeoutMs: 75000,
    });
    pending.turnId = turn.id;
    contextRevision = turn.context?.context_revision ?? contextRevision;
    await refreshSources();
    if (turn.status !== 'running') pending = null;
    $('question').value = ''; persist();
    await loadTurns(); render();
    notice(turn.status === 'completed' ? '回答已保存，可以继续追问。' : '这次回答仍待确认，请稍后读取。', turn.status !== 'completed');
  }
  function toggleTools(open) {
    $('tools-menu').hidden = !open;
    $('tools').setAttribute('aria-expanded', String(open));
  }
  $('tools').onclick = event => {
    event.stopPropagation();
    toggleTools($('tools-menu').hidden);
  };
  document.addEventListener('click', event => {
    if (!event.target.closest('.assistant-tools-wrap')) toggleTools(false);
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape') { toggleTools(false); event.stopPropagation(); }
  }, true);
  $('upload').onclick = () => { toggleTools(false); upload(); };
  $('add-source').onclick = () => { toggleTools(false); openSourcePicker(); };
  let citationActive = false, citationLayout = null;
  const canvas = document.getElementById('wordcraft_canvas');
  function outsideCitationPage(event) {
    if (!citationLayout) return false;
    const rect = canvas.getBoundingClientRect();
    const x = (event.clientX - rect.left) * canvas.width / rect.width;
    const y = (event.clientY - rect.top) * canvas.height / rect.height;
    return !(citationLayout.pages ?? []).some(page => {
      const left = page.x ?? 0, top = page.y ?? 0;
      const width = page.w ?? page.width, height = page.h ?? page.height;
      return Number.isFinite(width) && Number.isFinite(height) && x >= left && x <= left + width && y >= top && y <= top + height;
    });
  }
  function cancelCitationMode() {
    if (!citationActive) return;
    citationActive = false;
    citationLayout = null;
    document.body.classList.remove('citation-selection-mode');
    document.getElementById('citation-mode-bar')?.remove();
    canvas.removeEventListener('pointerup', finishCitationMode);
    canvas.removeEventListener('pointerdown', cancelCitationMargin);
    document.removeEventListener('keydown', citationKeydown, true);
    openWorkspace();
  }
  function cancelCitationMargin(event) {
    if (!outsideCitationPage(event)) return;
    event.preventDefault(); event.stopPropagation();
    cancelCitationMode();
  }
  function citationKeydown(event) {
    if (event.key !== 'Escape') return;
    event.preventDefault(); event.stopPropagation();
    cancelCitationMode();
  }
  async function finishCitationMode(event) {
    if (!citationActive || event.target !== canvas) return;
    const result = await engine({ op: 'selection' });
    if (!result.selection?.text?.trim()) {
      notice('请拖动选择正文，或点击纸边取消。');
      return;
    }
    selection = makeSelectionSnapshot(result, await sha256(result.selection.text));
    selection.locator = '正文圈选';
    persist();
    cancelCitationMode();
    render();
  }
  function startCitationMode() {
    if (!contextSelectionEnabled) { notice('框选加入上下文已暂停；可只读查看历史出处。'); return; }
    if (citationActive) return;
    citationActive = true;
    closeWorkspace();
    document.body.classList.add('citation-selection-mode');
    const bar = document.createElement('div');
    bar.id = 'citation-mode-bar';
    bar.innerHTML = '<span>拖动选择原文，纸边点击或 Esc 取消</span><button type="button" aria-label="取消圈选">取消</button>';
    bar.querySelector('button').onclick = cancelCitationMode;
    document.body.append(bar);
    engine({ op: 'layout' }).then(layout => { citationLayout = layout; }).catch(error => {
      notice('无法读取页面边界，请稍后重试：' + error.message, true);
    });
    canvas.addEventListener('pointerup', finishCitationMode);
    canvas.addEventListener('pointerdown', cancelCitationMargin);
    document.addEventListener('keydown', citationKeydown, true);
  }
  $('selection').onclick = () => { toggleTools(false); startCitationMode(); };
  $('cite').onclick = () => { toggleTools(false); startCitationMode(); };
  $('composer').onsubmit = event => { event.preventDefault(); action(async () => {
    try { await ask(); }
    catch (error) {
      if (error.status >= 400 && error.status < 500 && error.status !== 408) { pending = null; persist(); }
      notice(error.message + (pending ? ' 原请求已保留，可继续读取。' : ''), true);
      throw error;
    }
  }); };
  $('reload').onclick = () => action(async () => { await loadTurns(); render(); notice('已读取已有回答，本次未调用模型。'); });
  $('release').onclick = () => action(async () => { pending = null; persist(); render(); notice('已结束等待，服务器记录保留。'); });
  $('new').onclick = () => action(async () => {
    sessionId = null; storageKey = null; turns = []; selection = null; sources = []; pending = null;
    $('question').value = ''; render(); notice('新对话已准备好，不继承上一对话附件。');
  });
  $('sessions').onchange = () => action(async () => {
    if (!$('sessions').value) return;
    restoreLocal($('sessions').value); await refreshSources(); await loadTurns(); persist(); render();
  });
  let composing = false;
  $('question').addEventListener('compositionstart', () => { composing = true; });
  $('question').addEventListener('compositionend', () => { composing = false; });
  $('question').addEventListener('keydown', event => {
    if (event.key !== 'Enter' || event.shiftKey || composing || event.isComposing) return;
    event.preventDefault();
    $('composer').requestSubmit();
  });
  $('question').oninput = persist;

  return {
    async init() {
      lastSessionKey = 'lingdoc-workspace-assistant:last:' + projectPath('');
      contextRevision = (await expectation()).expected_context_revision;
      await loadSessions();
      const last = localStorage.getItem(lastSessionKey) || sessions[0]?.id;
      if (last) { restoreLocal(last); await refreshSources(); await loadTurns(); }
      render();
    },
    render,
    addSource(source) {
      if (!contextSelectionEnabled) throw new Error('资料上下文已暂停，未添加片段。');
      const index = sources.findIndex(item => item.id === source.id);
      if (index < 0) sources.push({ ...source }); else sources[index] = { ...source };
      persist(); render();
    },
    show() { render(); },
    pending: () => pending,
  };
}
