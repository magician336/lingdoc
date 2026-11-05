// A small WordCraft adapter for the existing LingDoc material and G2 APIs.
// The DOCX stays the only body; this panel never calls SaveChapter/WorkingCopy.
import { createWorkspaceAssistant } from './workspace-assistant.js';
import { createSourceDocumentPage, pendingSourceRequestFields, refreshSourceExcerpt, resolveHistoricalSource, sourceRequestFields } from './source-document-page.js';

export function createResearchPanel({ config, engine, save, canRetryCitation, retryCitation }) {
  const stylesheet = document.createElement('link');
  stylesheet.rel = 'stylesheet'; stylesheet.href = '/research-panel.css'; document.head.append(stylesheet);
  let bootstrap, project, assets = [], anchors = [], claims = [], cards = [], knowledgeById = new Map();
  let currentSource = null, associations = [], savedAssociations = [], pendingBuild = null, busy = false;
  const attempts = new Map();
  const root = document.createElement('aside');
  root.id = 'research-panel';
  root.dataset.contextPaused = 'true';
  root.dataset.evidencePaused = 'true';
  const evidenceEditingEnabled = false;
  root.hidden = true;
  root.innerHTML = [
    '<nav id="research-rail" aria-label="研究工作区">',
    '<div class="research-brand" aria-label="LingDoc">L</div>',
    '<button id="research-view-materials" type="button" aria-selected="true" aria-controls="research-materials" title="资料库"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5.5A2.5 2.5 0 0 1 6.5 3H20v16H6.5A2.5 2.5 0 0 0 4 21.5z"/><path d="M4 5.5v16M8 7h8M8 11h6"/></svg><span>资料</span></button>',
    '<button id="research-view-evidence" type="button" aria-selected="false" aria-controls="research-evidence" title="证据与引用"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 3h10v4H7zM5 5H3v16h14v-2M7 12h10M7 16h7"/><path d="m15 9 2 2 4-5"/></svg><span>证据</span></button>',
    '<div class="research-rail-caption">研究工作区</div></nav>',
    '<p id="research-status" class="research-global-status" role="status" aria-live="polite">正在连接本地资料服务…</p>',
    '<section id="research-materials" aria-label="资料库">',
    '<header><div><h2>资料库</h2><p>项目原文与检索</p></div><button id="research-close" class="research-close" type="button" aria-label="关闭研究工作区" title="关闭">×</button></header>',
    '<div class="research-pane-content">',
    '<button id="research-upload" class="research-primary research-wide" type="button">＋ 上传资料</button>',
    '<details class="research-format-help"><summary>支持哪些文件？</summary><p id="research-formats" class="research-intro">正在读取支持的文件类型…</p></details>',
    '<div class="research-section-title"><h3>项目资料 <span id="research-asset-count" class="research-count">0</span></h3></div>',
    '<div id="research-assets" class="research-assets"></div>',
    '<div class="research-search-block"><label for="research-query">在资料中查找</label><div class="research-search-row"><input id="research-query" type="search" placeholder="输入原文关键词，回车检索"><button id="research-search" class="research-primary" type="button">查找</button></div></div>',
    '<div id="research-hits" class="research-hits"></div>',
    '<section id="research-excerpt" class="research-excerpt" hidden><div class="research-section-title"><h3>原文与候选证据</h3></div>',
    '<p id="research-source-title" class="research-source-title"></p><p id="research-origin-note" class="research-note"></p><button id="research-original" class="research-text-button" type="button">下载原文件核对</button><label for="research-source">拖选需要引用的原文</label><textarea id="research-source" readonly rows="7"></textarea>',
    '<div class="research-evidence-form"><label for="research-claim">这段原文说明什么？</label><textarea id="research-claim" rows="2" placeholder="用自己的话写清要支持或质疑的观点"></textarea>',
    '<label for="research-support">与观点的关系</label><select id="research-support"><option value="supports">支持这个观点</option><option value="refutes">反驳这个观点</option><option value="background">提供背景</option></select>',
    '<label for="research-chapter">用于哪一章？</label><select id="research-chapter"></select>',
    '<label for="research-scope">适用范围</label><input id="research-scope" placeholder="例如：仅用于说明研究背景">',
    '<label for="research-limits">局限或需注意的地方</label><textarea id="research-limits" rows="2" placeholder="例如：尚未与其他来源交叉核对"></textarea>',
    '<label class="research-check"><input id="research-reviewed" type="checkbox"><span>我已核对所选原文及上述信息</span></label>',
    '<button id="research-create" class="research-primary research-wide" type="button">保存候选证据卡</button></div></section></div></section>',
    '<section id="research-evidence" aria-label="证据与引用" hidden><header><div><h2>证据与引用</h2><p>人工核对后写入正文</p></div><button id="research-evidence-close" class="research-close" type="button" aria-label="关闭研究工作区" title="关闭">×</button></header>',
    '<div class="research-pane-content"><div class="research-section-title"><h3>证据卡</h3></div><div id="research-cards"></div>',
    '<div class="research-section-title research-saved-heading"><h3>上次保存的引用</h3></div><div id="research-citations"></div>',
    '<div class="research-sticky-action"><button id="research-save" class="research-primary research-wide" type="button">保存文稿与引用</button><p class="research-note">引用随 DOCX 保存；当前提供来源追溯，不生成规范参考文献格式。</p></div></div></section>',
    '<aside id="research-ai" aria-label="AI 助手" hidden><header><div><h2>AI 助手 <span id="research-ai-model" class="research-model">DeepSeek</span></h2><p>多轮对话与资料问答</p></div><button id="research-ai-close" class="research-close" type="button" aria-label="关闭研究工作区" title="关闭">×</button></header>',
    '<div class="assistant-mode-tabs" role="tablist" aria-label="助手模式"><button id="research-chat-workspace" type="button" role="tab" aria-selected="true">多轮助手</button><button id="research-chat-source" type="button" role="tab" aria-selected="false">资料问答</button></div>',
    '<div id="research-source-chat" hidden><div class="research-ai-feed"><p class="research-intro">从右侧检索并选择原文片段，再针对这些内容提问。最多 4 段、合计 12000 字符。</p>',
    '<div class="research-section-title"><h3>本次阅读</h3><button id="research-ai-add-source" class="research-text-button" type="button">添加资料</button></div>',
    '<section id="research-ai-source-picker" class="research-source-picker" hidden><label for="research-ai-source-query">在项目资料中查找</label><div class="research-search-row"><input id="research-ai-source-query" type="search" placeholder="输入原文关键词"><button id="research-ai-source-search" class="research-secondary" type="button">查找</button></div><div id="research-ai-source-hits" class="research-hits"></div></section>',
    '<div id="research-ai-sources"></div>',
    '<p id="research-ai-status" role="status" aria-live="polite"></p><div id="research-ai-answers"></div></div>',
    '<div class="research-ai-composer"><label for="research-ai-question">想了解什么？</label><textarea id="research-ai-question" rows="3" maxlength="4000" placeholder="针对所选原文提出问题…"></textarea>',
    '<label class="research-check"><input id="research-ai-consent" type="checkbox"><span>允许将所列片段和问题发送给 DeepSeek</span></label>',
    '<p class="research-note">首次授权可能使已有证据卡进入待复核。AI 回答仅供参考，请通过出处核对原文。</p>',
    '<div class="research-ai-actions"><button id="research-ai-ask" class="research-primary" type="button">提问</button><button id="research-ai-reload" class="research-secondary" type="button">读取已有回答</button></div>',
    '<button id="research-ai-release" class="research-text-button" type="button" hidden>结束等待，保留记录</button></div></div>',
    '<div id="research-workspace-chat"><div class="assistant-session-bar"><select id="assistant-sessions" aria-label="最近对话"></select><button id="assistant-new" type="button">新对话</button></div>',
    '<div id="assistant-messages" class="assistant-messages"></div><div class="assistant-bottom"><div id="assistant-history-notice" class="assistant-history-notice" role="status"></div>',
    '<section id="assistant-source-picker" class="research-source-picker" hidden><label for="assistant-source-query">在项目资料中查找</label><div class="research-search-row"><input id="assistant-source-query" type="search" placeholder="输入原文关键词"><button id="assistant-source-search" class="research-secondary" type="button">查找</button></div><div id="assistant-source-hits" class="research-hits"></div></section>',
    '<div class="assistant-attachments"><div id="assistant-context"></div></div>',
    '<form id="assistant-composer" class="assistant-composer"><label for="assistant-question">向工作区助手提问</label><textarea id="assistant-question" maxlength="4000" required placeholder="向 LingDoc 提问，Shift+Enter 换行"></textarea>',
    '<input id="research-file" type="file" accept=".txt,.md" hidden>',
    '<p id="assistant-status" role="status" aria-live="polite"></p><div class="assistant-action-bar"><div class="assistant-tools-wrap"><button id="assistant-tools" type="button" aria-label="添加上传或上下文" aria-haspopup="menu" aria-expanded="false">＋</button>',
    '<div id="assistant-tools-menu" role="menu" hidden><button id="assistant-upload" type="button" role="menuitem">上传资料</button><button id="assistant-add-source" type="button" role="menuitem">添加资料</button><button id="assistant-selection" type="button" role="menuitem">添加正文选区</button><button id="assistant-cite" type="button" role="menuitem">引用原文</button></div></div>',
    '<span id="assistant-upload-status"></span><button id="assistant-reload" class="research-text-button" type="button">读取已有回答</button><button id="assistant-send" class="research-primary" type="submit" aria-label="发送">↑</button></div>',
    '<button id="assistant-release" class="research-text-button" type="button" hidden>结束等待，保留记录</button></form></div></div></aside>',
  ].join('');
  document.body.append(root);
  const toggle = document.createElement('button');
  toggle.id = 'research-toggle';
  toggle.type = 'button';
  toggle.textContent = '资料与引用';
  toggle.setAttribute('aria-controls', root.id);
  document.body.append(toggle);
  const navigation = document.createElement('nav');
  navigation.id = 'workspace-navigation';
  navigation.setAttribute('aria-label', '工作区导航');
  navigation.innerHTML = '<div class="workspace-identity"><strong>LingDoc</strong><span>写作工作台</span></div>'
    + '<div class="workspace-views"><button id="workspace-materials" type="button" aria-controls="research-materials research-evidence">左侧栏</button><button id="workspace-evidence" type="button" aria-controls="research-evidence">证据</button><button id="workspace-ai" type="button" aria-controls="research-ai">右侧 AI</button><button id="workspace-focus" type="button">专注写作</button></div>'
    + '<span id="workspace-save-status" role="status" aria-live="polite"></span><button id="workspace-save" type="button">保存文稿</button>';
  document.body.append(navigation);
  document.body.classList.add('workspace-shell');
  const nav = id => navigation.querySelector('#workspace-' + id);
  const $ = id => root.querySelector('#research-' + id);
  $('chat-source').textContent = '资料问答记录';
  $('source-chat').querySelector('.research-intro').textContent = '资料问答暂为只读。可读取历史回答、查看出处，不再添加或发送资料上下文。';
  $('source-chat').querySelector('.research-ai-feed').append($('ai-reload'));
  $('source-chat').querySelector('h3').textContent = '保留的历史附件';
  const pausedNote = document.createElement('p');
  pausedNote.className = 'research-note';
  pausedNote.textContent = '框选和资料上下文已暂停。历史附件保留，不会自动发送；可新建对话直接提问。';
  $('workspace-chat').prepend(pausedNote);
  $('evidence').querySelector('header p').textContent = '只读查看 · 新建、确认和引用已暂停';
  $('excerpt').querySelector('label[for="research-source"]').textContent = '只读原文';
  $('excerpt').querySelector('h3').textContent = '只读原文';
  let statusTimer = null;
  const status = (text, error = false) => {
    clearTimeout(statusTimer);
    $('status').textContent = text;
    $('status').classList.toggle('error', error);
    $('status').dataset.visible = 'true';
    if (!error) statusTimer = setTimeout(() => { $('status').dataset.visible = 'false'; }, 3600);
  };
  const rows = value => Array.isArray(value) ? value : value?.items ?? value?.data ?? [];
  const projectPath = suffix => '/api/v1/lingdoc/projects/' + encodeURIComponent(bootstrap.projectId) + suffix;
  const cardName = card => claims.find(item => item.id === card.claim_ref?.id)?.text || '证据卡';

  async function request(url, { body, method = 'GET', key, reuseUploadedFile = false, timeoutMs = 60000 } = {}) {
    const headers = { Accept: 'application/json' };
    if (key) headers['Idempotency-Key'] = key;
    const multipart = body instanceof FormData;
    if (body !== undefined && !multipart) headers['Content-Type'] = 'application/json';
    const response = await fetch(config.goOrigin + url, {
      method, headers, body: body === undefined ? undefined : multipart ? body : JSON.stringify(body),
      signal: AbortSignal.timeout(timeoutMs),
    });
    const payload = await response.json().catch(() => ({}));
    if (reuseUploadedFile && response.status === 409 && payload.code === 'duplicate_file' && payload.data?.id) return payload.data;
    if (!response.ok || payload.success === false) {
      const detail = payload.error;
      throw Object.assign(new Error(payload.message || detail?.message || (typeof detail === 'string' ? detail : '资料服务请求失败')), {
        status: response.status, code: detail?.code || payload.code || detail,
      });
    }
    return Object.hasOwn(payload, 'data') ? payload.data : payload;
  }

  async function write(suffix, body, slot) {
    // An unknown reply replays exactly the same payload and idempotency key.
    const serialized = JSON.stringify(body), pending = attempts.get(slot);
    if (pending && pending.serialized !== serialized) throw new Error('上次操作结果待确认，请先重试原操作。');
    const operation = pending || { serialized, body, key: crypto.randomUUID() };
    attempts.set(slot, operation);
    const result = await request(projectPath(suffix), { method: 'POST', body: operation.body, key: operation.key });
    attempts.delete(slot);
    return result;
  }

  async function expectation(version = 0) {
    project = await request(projectPath(''));
    return { expected_project_version: project.project_version, expected_context_revision: project.current_context_revision, expected_version: version };
  }

  let workspaceAI = null, selectionWorkspace = null;
  async function action(run) {
    if (busy) return;
    busy = true;
    $('status').classList.remove('error');
    root.setAttribute('aria-busy', 'true');
    root.querySelectorAll('button:not(#research-close)').forEach(button => { button.disabled = true; });
    try { await run(); }
    catch (error) { status(error.message + (typeof error.code === 'string' ? ' [' + error.code + ']' : ''), true); }
    finally {
      busy = false; root.removeAttribute('aria-busy');
      root.querySelectorAll('button').forEach(button => { button.disabled = false; });
      renderCards(); renderCitations(); sourceAI.render(); workspaceAI?.render();
    }
  }

  function appendButton(container, text, run, disabled = false) {
    const button = document.createElement('button');
    button.type = 'button'; button.textContent = text; button.disabled = disabled;
    button.onclick = () => action(run); container.append(button); return button;
  }
  function paragraph(container, text, className = '') {
    const p = document.createElement('p'); p.textContent = text; p.className = className; container.append(p);
  }
  const sourceDocumentPage = createSourceDocumentPage({ request, projectPath, engine });
  const openSourceDocument = (id, excerpt) => sourceDocumentPage.open({ sourceId: id, mode: 'read', excerpt });
  const sourceAI = createSourceAI({ $, request, projectPath, expectation, action, appendButton, paragraph,
    openSource: openSourceDocument, openSourcePicker: () => showSourcePicker('source'),
    refresh, getBootstrap: () => bootstrap, isBusy: () => busy });
  workspaceAI = createWorkspaceAssistant({ root: $('workspace-chat'), request, projectPath, expectation, engine, action,
    openSource: openSourceDocument, openSourcePicker: () => showSourcePicker('workspace'),
    upload: () => $('file').click(),
    closeWorkspace: () => {
      selectionWorkspace = { leftOpen, rightOpen, side: document.body.dataset.side };
      show(false);
    }, openWorkspace: () => {
      show(true);
      if (selectionWorkspace) {
        leftOpen = selectionWorkspace.leftOpen; rightOpen = selectionWorkspace.rightOpen;
        document.body.dataset.side = selectionWorkspace.side || 'right';
        selectionWorkspace = null; layoutEditor();
      }
    } });
  $('file').onchange = () => action(upload);
  $('upload').onclick = () => $('file').click();
  function selectChatMode(mode) {
    const workspace = mode === 'workspace';
    $('workspace-chat').hidden = !workspace;
    $('source-chat').hidden = workspace;
    $('chat-workspace').setAttribute('aria-selected', String(workspace));
    $('chat-source').setAttribute('aria-selected', String(!workspace));
    if (workspace) workspaceAI.show(); else sourceAI.render();
  }
  function picker(mode) {
    const prefix = mode === 'workspace' ? '#assistant-source' : '#research-ai-source';
    return { panel: root.querySelector(prefix + '-picker'), query: root.querySelector(prefix + '-query'), hits: root.querySelector(prefix + '-hits') };
  }
  function showSourcePicker(mode) {
    status('资料上下文已暂停。请到项目资料库只读查看原文。');
  }
  async function openSourceForSelection(mode, sourceId) {
    await openSourceDocument(sourceId);
  }
  async function searchSourcePicker(mode) {
    const elements = picker(mode), query = elements.query.value.trim();
    if (!query || !assets.length) throw new Error('请先导入资料并填写关键词。');
    const results = rows(await request(projectPath('/retrieval'), { method: 'POST', body: { query, asset_ids: assets.map(item => item.id) } }));
    elements.hits.replaceChildren();
    if (!results.length) paragraph(elements.hits, '没有找到匹配片段，请换用原文中的词语。');
    results.forEach(source => appendButton(elements.hits, (source.locator || '原文') + ' · ' + source.quoted_text.slice(0, 72),
      () => openSourceForSelection(mode, source.id)));
  }
  for (const mode of ['workspace', 'source']) {
    const elements = picker(mode);
    root.querySelector((mode === 'workspace' ? '#assistant' : '#research-ai') + '-source-search').onclick = () => action(() => searchSourcePicker(mode));
    elements.query.addEventListener('keydown', event => {
      if (event.key !== 'Enter') return;
      event.preventDefault();
      root.querySelector((mode === 'workspace' ? '#assistant' : '#research-ai') + '-source-search').click();
    });
  }
  $('chat-workspace').onclick = () => selectChatMode('workspace');
  $('chat-source').onclick = () => selectChatMode('source');
  async function refresh() {
    [assets, anchors, claims, cards] = await Promise.all(['assets', 'source-anchors', 'research-claims', 'evidence-cards']
      .map(suffix => request(projectPath('/' + suffix)).then(rows)));
    $('assets').replaceChildren();
    $('asset-count').textContent = String(assets.length);
    if (!assets.length) paragraph($('assets'), '尚未加入资料。');
    assets.forEach(asset => paragraph($('assets'), (asset.title || '资料') + ' · ' + (asset.processing_state === 'ready' ? '可查找' : asset.processing_state)));
    // A successful upload survives a later bind/network failure. Show those
    // real knowledge objects so retry never requires uploading another copy.
    const uploaded = rows(await request('/api/v1/knowledge-bases/' + encodeURIComponent(bootstrap.knowledgeBaseId) + '/knowledge'));
    knowledgeById = new Map(uploaded.map(item => [item.id, item]));
    for (const item of uploaded.filter(item => !assets.some(asset => asset.knowledge_id === item.id))) {
      paragraph($('assets'), (item.title || item.file_name || '已上传资料') + ' · 尚未加入项目');
      appendButton($('assets'), '检查处理状态并加入项目', async () => {
        await bindProcessed(item.id); await refresh(); status('资料已加入项目，可以查找原文。');
      });
    }
    renderCards(); renderCitations();
  }
  // Port of upstream projectMaterialIntake.materialReadiness (acb9ac7):
  // parse_status alone, and synthesized stage placeholders, are not proof.
  async function bindProcessed(id) {
    const [knowledge, trace] = await Promise.all([
      request('/api/v1/knowledge/' + encodeURIComponent(id)),
      request('/api/v1/knowledge/' + encodeURIComponent(id) + '/spans'),
    ]);
    const attempt = trace.current_attempt;
    const stages = trace.trace?.children?.filter(node => node.name === 'embedding' && node.kind === 'stage') ?? [];
    const stage = stages[0];
    if (knowledge.parse_status !== 'completed' || trace.knowledge_id !== id ||
        !Number.isInteger(attempt) || attempt < 1 || attempt !== trace.latest_attempt || trace.parse_status !== 'completed' ||
        stages.length !== 1 || !stage.span_id || stage.attempt !== attempt || stage.knowledge_id !== id || stage.status !== 'done') {
      throw new Error(stage?.error_message || '资料已上传，但当前索引尚未确认完成。请稍后刷新并重试加入项目。');
    }
    await write('/assets', { knowledge_id: id }, 'bind:' + id);
  }
  async function openSource(id) {
    currentSource = await request(projectPath('/sources/' + encodeURIComponent(id)));
    const asset = assets.find(item => item.id === currentSource.asset_id);
    const knowledge = knowledgeById.get(asset?.knowledge_id);
    $('origin-note').textContent = ['pdf', 'docx'].includes(knowledge?.file_type)
      ? '以下为与上传原件绑定的解析文字。可下载原件核对排版、表格和上下文；当前位置是文字片段，不是原文件页码。'
      : '以下文字来自上传资料，可下载原文件核对。';
    $('original').hidden = !asset?.knowledge_id;
    $('source-title').textContent = currentSource.locator || currentSource.title || '原文片段';
    $('source').value = currentSource.quoted_text || '';
    $('excerpt').hidden = false;
    $('reviewed').checked = false;
    $('source').setSelectionRange(0, 0);
    selectView('materials');
    $('excerpt').scrollIntoView({ block: 'start' });
  }
  function associationKey(item) { return item.sectionId + ':' + item.sourceId + ':' + item.evidenceCardId; }
  function renderCitations() {
    $('citations').replaceChildren();
    if (!savedAssociations.length) paragraph($('citations'), '尚无已保存的引用。新增引用已暂停。');
    for (const item of savedAssociations) {
      const chapter = bootstrap?.chapters.find(ch => ch.sectionId === item.sectionId);
      appendButton($('citations'), (chapter?.title || item.sectionId) + ' · 回看来源', async () => {
        await openSourceDocument(item.sourceId);
        status('已读取当前可访问的原文。证据卡版本：' + item.evidenceCardVersion);
      }, busy);
    }
  }
  function renderCards() {
    $('cards').replaceChildren();
    if (!cards.length) paragraph($('cards'), '尚无证据卡。');
    for (const card of cards) {
      const article = document.createElement('article');
      article.dataset.cardId = card.id;
      paragraph(article, cardName(card), 'research-card-title');
      paragraph(article, card.scope + ' · ' + (card.status === 'CONFIRMED' ? '已确认' : card.status === 'CANDIDATE' ? '待确认' : card.status));
      if (card.limitations) paragraph(article, '局限：' + card.limitations);
      if (card.invalid_reason) paragraph(article, card.invalid_reason, 'error');
      appendButton(article, '回看原文', async () => { await openSource(card.source_ids[0]); status('请核对原文和适用范围。'); }, busy);
      if (evidenceEditingEnabled && card.status === 'CANDIDATE') {
        appendButton(article, '确认已核对，允许引用', async () => {
          const slot = 'confirm:' + card.id;
          const input = attempts.get(slot)?.body || { ...await expectation(card.version), confirmed: true, reason: '人工核对原文、观点与本章适用范围后确认。' };
          await write('/evidence-cards/' + encodeURIComponent(card.id) + '/confirm', input, slot);
          await refresh(); status('证据卡已确认，可引用到指定章节。');
        }, busy);
      }
      if (evidenceEditingEnabled && card.status === 'CONFIRMED') {
        const chapters = bootstrap.chapters.filter(ch => card.scope_chapter_ids.includes(ch.chapterId));
        for (const chapter of chapters) {
          appendButton(article, '引用到' + chapter.title, () => insertCard(card.id, chapter), busy);
          if (canRetryCitation?.(card.id, chapter.sectionId)) appendButton(article, '已重新核对：用此卡补登记' + chapter.title, async () => {
            const fresh = await request(projectPath('/evidence-cards/' + encodeURIComponent(card.id)));
            if (fresh.version !== card.version || fresh.status !== 'CONFIRMED') throw new Error('卡片已变化，请刷新并重新核对后再操作。');
            const selected = fresh.source_ids.map(sourceId => ({ sectionId: chapter.sectionId, sourceId, evidenceCardId: fresh.id, evidenceCardVersion: fresh.version }));
            const saved = await retryCitation(selected);
            for (const ref of selected) {
              associations = associations.filter(old => associationKey(old) !== associationKey(ref));
              associations.push(ref);
            }
            status(saved.ackPending ? '已补登记原保存版本。编辑器还有新修改，请再保存一次。' : '已补登记原保存版本，未重新导出或写文件。');
          }, busy);
        }
      }
      $('cards').append(article);
    }
  }

  async function insertCard(id, chapter) {
    if (!evidenceEditingEnabled) throw new Error('证据引用已暂停，正文未改动。');
    const card = await request(projectPath('/evidence-cards/' + encodeURIComponent(id)));
    if (card.status !== 'CONFIRMED' || !card.scope_chapter_ids.includes(chapter.chapterId)) throw new Error('这张卡片已变化或不适用于该章，请重新核对。');
    const latestAnchors = rows(await request(projectPath('/source-anchors')));
    const parts = [], next = [];
    for (const reference of card.anchor_refs) {
      const anchor = latestAnchors.find(item => item.id === reference.id && item.version === reference.version);
      if (!anchor || anchor.status !== 'CURRENT') throw new Error('原文定位已变化，请重新核对证据卡。');
      const source = await request(projectPath('/sources/' + encodeURIComponent(anchor.source_id)));
      const quote = Array.from(source.quoted_text).slice(anchor.selection_start, anchor.selection_end).join('');
      const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(quote))))
        .map(byte => byte.toString(16).padStart(2, '0')).join('');
      if (!quote || digest !== anchor.quoted_text_hash) throw new Error('原文与证据卡不一致，已停止插入。');
      parts.push('“' + quote + '” [[source:' + anchor.source_id + ']]');
      next.push({ sectionId: chapter.sectionId, sourceId: anchor.source_id, evidenceCardId: card.id, evidenceCardVersion: card.version });
    }
    const state = await engine({ op: 'status' });
    const manifest = await engine({ op: 'manifest', expectedDocumentId: state.documentId, expectedRevision: state.revision,
      templateId: 'nsfc2026-annual-application', adapterVersion: 'nsfc2026-r4-adapter-v1' });
    await engine({ op: 'focusSection', sectionId: chapter.sectionId, expectedDocumentId: state.documentId, expectedRevision: state.revision,
      expectedManifestHash: manifest.manifestHash, adapterVersion: 'nsfc2026-r4-adapter-v1' });
    const selection = await engine({ op: 'selection' });
    await engine({ op: 'insert', text: parts.join('\n') + '\n', bookmark: selection.selection,
      expectedDocumentId: selection.state.documentId, expectedRevision: selection.state.revision });
    for (const item of next) {
      const index = associations.findIndex(old => associationKey(old) === associationKey(item));
      if (index < 0) associations.push(item); else associations[index] = item;
    }
    renderCitations();
    status('已插入' + chapter.title + '，尚未保存。可继续编辑或撤销，再按 Ctrl+S 保存。');
  }

  async function upload() {
    const file = $('file').files[0];
    if (!file) throw new Error('请先选择资料文件。');
    const type = file.name.split('.').at(-1).toLowerCase();
    if (!bootstrap.materialFormats.includes(type) || file.size > 8 * 1024 * 1024) throw new Error('请选择不超过 8 MiB 的受支持资料：' + bootstrap.materialFormats.join(' / '));
    if (['txt', 'md'].includes(type)) {
      const text = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer());
      if (/[\r\0]/.test(text) || text.includes('![') || /<img|data:image\//i.test(text)) {
        throw new Error('文本资料需使用 UTF-8、LF 换行，且不含图片引用；原文件未改动。');
      }
    }
    status('正在上传并处理原文…');
    const form = new FormData(); form.append('file', file);
    const knowledge = await request('/api/v1/knowledge-bases/' + encodeURIComponent(bootstrap.knowledgeBaseId) + '/knowledge/file', { method: 'POST', body: form, reuseUploadedFile: true });
    const id = knowledge.id || knowledge.knowledge?.id;
    if (!id) throw new Error('上传没有返回资料标识，已停止。');
    for (let i = 0; i < 90; i++) {
      const observed = await request('/api/v1/knowledge/' + encodeURIComponent(id));
      if (observed.parse_status === 'failed') throw new Error(observed.error_message || '资料处理失败；原件已保留。');
      if (observed.parse_status === 'completed') {
        await bindProcessed(id);
        await refresh();
        selectView('materials');
        status('已入库，待选择引用片段。请检索原文并选择需要发送的段落。');
        $('query').focus();
        return;
      }
      await new Promise(resolve => setTimeout(resolve, 500));
    }
    throw new Error('资料仍在处理，未重复上传。请稍后刷新。资料标识：' + id);
  }

  async function createCard() {
    if (!evidenceEditingEnabled) throw new Error('新建证据已暂停，现有资料与证据保留。');
    if (!pendingBuild) {
      const text = $('source'), claim = $('claim').value.trim(), scope = $('scope').value.trim();
      if (!currentSource || text.selectionEnd <= text.selectionStart) throw new Error('请在原文框中拖选一个片段。');
      if (!claim || !scope || !$('reviewed').checked) throw new Error('请填写观点和适用范围，并确认已核对原文。');
      pendingBuild = { sourceId: currentSource.id, start: Array.from(text.value.slice(0, text.selectionStart)).length,
        end: Array.from(text.value.slice(0, text.selectionEnd)).length, claimText: claim, scope,
        limitations: $('limits').value.trim(), chapterId: $('chapter').value, support: $('support').value };
    }
    const draft = pendingBuild;
    status('正在保存原文定位、观点和候选卡片…');
    async function step(name, suffix, input) {
      if (!draft[name]) {
        const body = attempts.get(name)?.body || { ...await expectation(), ...input };
        draft[name] = await write(suffix, body, name);
      }
      if (!draft[name]?.id || !Number.isInteger(draft[name].version)) throw new Error('资料服务未返回有效的' + name + '记录，后续步骤已停止。');
      return draft[name];
    }
    const anchor = await step('anchor', '/source-anchors', { source_id: draft.sourceId, selection_start: draft.start, selection_end: draft.end,
      extraction_type: 'native_text', ocr_confidence: 1, human_verified: true, confirmed: true });
    const claim = await step('claim', '/research-claims', { text: draft.claimText, scope: draft.scope, status: 'active', reason: '用户依据所选原文创建主张。', confirmed: true });
    await step('card', '/evidence-cards', { anchor_refs: [{ id: anchor.id, version: anchor.version }],
      claim_ref: { id: claim.id, version: claim.version }, support_type: draft.support, scope: draft.scope,
      scope_chapter_ids: [draft.chapterId], limitations: draft.limitations, gaps: '', alternative_explanation: '', confirmed: true });
    pendingBuild = null;
    await refresh();
    selectView('evidence');
    status('候选证据卡已保存。请核对后点击“确认已核对，允许引用”。');
  }

  let activeView = null, leftOpen = true, rightOpen = true, saveReceiptTimer;
  function layoutEditor(notifyEngine = true) {
    const editor = document.getElementById('editor');
    const compact = window.matchMedia('(max-width: 1099px)').matches;
    const open = !root.hidden && !compact;
    root.dataset.leftOpen = String(leftOpen);
    root.dataset.rightOpen = String(rightOpen);
    const leftPane = activeView === 'evidence' ? $('evidence') : $('materials');
    editor.style.left = !root.hidden && leftOpen ? (compact ? '56px' : `${56 + leftPane.getBoundingClientRect().width}px`) : '';
    editor.style.right = open && rightOpen && bootstrap?.ai?.configured ? `${$('ai').getBoundingClientRect().width}px` : '';
    const leftVisible = !root.hidden && leftOpen;
    const rightVisible = !root.hidden && rightOpen && bootstrap?.ai?.configured;
    nav('materials').setAttribute('aria-pressed', String(leftVisible));
    nav('materials').setAttribute('aria-expanded', String(leftVisible));
    nav('evidence').setAttribute('aria-pressed', String(leftVisible && activeView === 'evidence'));
    nav('ai').setAttribute('aria-pressed', String(!!rightVisible));
    nav('ai').setAttribute('aria-expanded', String(!!rightVisible));
    nav('focus').setAttribute('aria-pressed', String(root.hidden));
    if (notifyEngine) window.dispatchEvent(new Event('resize'));
  }
  function selectView(view) {
    leftOpen = true;
    activeView = view;
    const materials = view === 'materials';
    $('materials').hidden = !materials;
    $('evidence').hidden = materials;
    $('view-materials').setAttribute('aria-selected', String(materials));
    $('view-evidence').setAttribute('aria-selected', String(!materials));
    document.body.dataset.side = 'left';
    layoutEditor();
    if (!materials && bootstrap && !busy) action(async () => { await refresh(); status('已读取最新资料与卡片状态。'); });
  }
  $('view-materials').onclick = () => selectView('materials');
  $('view-evidence').onclick = () => selectView('evidence');
  function show(open) {
    if (!open) { leftOpen = false; rightOpen = false; }
    root.hidden = !open;
    toggle.hidden = open;
    if (open && !activeView) selectView(savedAssociations.length ? 'evidence' : 'materials');
    layoutEditor();
  }
  window.addEventListener('resize', () => layoutEditor(false));
  toggle.onclick = () => nav('materials').click();
  function closeSide(side) {
    if (side === 'left') leftOpen = false; else rightOpen = false;
    if (!leftOpen && !rightOpen) return show(false);
    layoutEditor();
  }
  $('close').onclick = () => closeSide('left');
  $('evidence-close').onclick = () => closeSide('left');
  $('ai-close').onclick = () => closeSide('right');
  for (const id of ['close', 'evidence-close']) {
    $(id).setAttribute('aria-label', '收起资料与证据'); $(id).title = '收起左侧栏';
  }
  $('ai-close').setAttribute('aria-label', '收起 AI 助手'); $('ai-close').title = '收起右侧栏';
  nav('materials').onclick = () => {
    if (!root.hidden && leftOpen) return closeSide('left');
    root.hidden = false; toggle.hidden = true; selectView(activeView || 'materials');
  };
  nav('evidence').onclick = () => { root.hidden = false; toggle.hidden = true; selectView('evidence'); };
  nav('ai').onclick = () => {
    if (!root.hidden && rightOpen) return closeSide('right');
    root.hidden = false; toggle.hidden = true; rightOpen = true;
    document.body.dataset.side = 'right'; layoutEditor();
    if (!bootstrap?.ai?.configured) status('AI 服务尚未就绪，请稍后重试。', true);
  };
  nav('focus').onclick = () => show(false);
  nav('save').onclick = () => {
    if (busy) return;
    nav('save').disabled = true;
    nav('save').textContent = '正在保存…';
    clearTimeout(saveReceiptTimer);
    nav('save-status').textContent = '';
    action(async () => {
      try {
        const saved = await save('workspace-navigation');
        nav('save-status').textContent = saved.ackPending || saved.live?.dirty ? '该版本已保存，仍有待保存修改' : '文稿与引用已保存';
        saveReceiptTimer = setTimeout(() => { nav('save-status').textContent = ''; }, 5000);
      } catch (error) {
        nav('save-status').textContent = '保存未完成，请重试';
        throw error;
      }
    }).finally(() => { nav('save').disabled = false; nav('save').textContent = '保存文稿'; });
  };
  $('query').addEventListener('keydown', event => {
    if (event.key === 'Enter') { event.preventDefault(); $('search').click(); }
  });
  $('search').onclick = () => action(async () => {
    const query = $('query').value.trim();
    if (!query || !assets.length) throw new Error('请先导入资料并填写关键词。');
    const results = rows(await request(projectPath('/retrieval'), { method: 'POST', body: { query, asset_ids: assets.map(item => item.id) } }));
    $('hits').replaceChildren();
    if (!results.length) paragraph($('hits'), '没有找到匹配片段，请换用原文中的词语。');
    results.forEach(source => appendButton($('hits'), (source.locator || '原文') + ' · ' + source.quoted_text.slice(0, 72), () => openSource(source.id)));
    status('找到 ' + results.length + ' 个片段。点击只读查看原文。');
  });
  $('create').onclick = () => action(createCard);
  $('original').onclick = () => action(async () => {
    const source = await request(projectPath('/sources/' + encodeURIComponent(currentSource.id)));
    const asset = assets.find(item => item.id === source.asset_id);
    if (!asset?.knowledge_id) throw new Error('原资料已变化，请刷新后重试。');
    const response = await fetch(config.goOrigin + '/api/v1/knowledge/' + encodeURIComponent(asset.knowledge_id) + '/download');
    if (!response.ok) throw new Error('原文件当前不可读取（' + response.status + '）。');
    const blob = await response.blob();
    const link = document.createElement('a'), url = URL.createObjectURL(blob);
    link.href = url; link.download = knowledgeById.get(asset.knowledge_id)?.file_name || asset.title || 'source';
    link.click(); setTimeout(() => URL.revokeObjectURL(url), 30000);
    status('已请求下载上传时的原文件，可与解析文字核对。');
  });
  $('save').onclick = () => action(async () => {
    const saved = await save('research-panel');
    status(saved.ackPending || saved.live?.dirty ? '该版本的文稿及引用已保存，编辑器仍有待保存修改。' : '文稿及其实际引用已保存。');
  });
  const ready = (async () => {
    bootstrap = await request('/api/local/research/bootstrap');
    $('file').accept = bootstrap.materialFormats.map(type => '.' + type).join(',');
    $('formats').textContent = '支持 ' + bootstrap.materialFormats.join(' / ').toUpperCase() + '，每份不超过 8 MiB。PDF 需带文字层；扫描件的 OCR 尚未接入。';
    const sectionOrder = ['topic-summary', 'topic-basis', 'research-content', 'innovation', 'expected-results', 'references'];
    bootstrap.chapters.sort((left, right) => sectionOrder.indexOf(left.sectionId) - sectionOrder.indexOf(right.sectionId));
    for (const chapter of bootstrap.chapters) {
      const option = document.createElement('option'); option.value = chapter.chapterId; option.textContent = chapter.title; $('chapter').append(option);
    }
    await refresh();
    await sourceAI.init();
    await workspaceAI.init();
    status('本地资料服务已就绪。');
    show(true);
    return bootstrap;
  })();
  ready.catch(error => status(error.message, true));
  return {
    ready,
    snapshot: () => associations.map(item => ({ ...item })),
    restore: items => {
      associations = (items || []).map(item => ({ ...item }));
      savedAssociations = associations.map(item => ({ ...item }));
      activeView = savedAssociations.length ? 'evidence' : (activeView || 'materials');
      if (!root.hidden) selectView(activeView);
      renderCitations();
    },
    // Keep insertion references through deletion/save/undo. Only markers in the
    // saved bytes become persisted links; the server filters this local cache.
    acceptSaved: items => { savedAssociations = (items || []).map(item => ({ ...item })); renderCitations(); },
    saveFailure: error => { status(error.message, true); renderCards(); },
    async refresh() { await ready; await refresh(); },
    show: () => show(true),
  };
}

// This is a thin client of the existing G2 session, source-policy and turn APIs.
// Unknown outcomes keep their original payload/key, including across refreshes.
function createSourceAI({ $, request, projectPath, expectation, action, appendButton, paragraph,
  openSource, openSourcePicker, refresh, getBootstrap, isBusy }) {
  const contextSelectionEnabled = false;
  let configured = false, selected = [], turns = [], historySources = new Map(), historyViews = new Map();
  let state = { sessionId: null, pending: null, selected: [], draft: '' }, storageKey;
  const path = value => encodeURIComponent(value);
  const rows = value => Array.isArray(value) ? value : value?.items ?? [];
  const notice = (message, error = false) => {
    $('ai-status').textContent = message; $('ai-status').classList.toggle('error', error);
  };
  function persist() {
    if (!storageKey) return;
    state.selected = selected;
    state.draft = $('ai-question').value;
    localStorage.setItem(storageKey, JSON.stringify(state));
  }
  function contextDetails(container, context, restricted = false) {
    const detail = document.createElement('details'), summary = document.createElement('summary');
    summary.textContent = context.source_refs?.length ? `查看本轮资料（${context.source_refs.length} 段）` : '本轮未附加资料';
    detail.append(summary);
    for (const ref of context.source_refs ?? []) {
      paragraph(detail, ref.locator || '资料片段', 'research-card-title');
      const view = historyViews.get(ref) ?? { text: '原文当前不可读取。', verified: false, changed: false };
      paragraph(detail, view.text, 'research-ai-text');
      if (historySources.has(ref.source_id) && !restricted) {
        appendButton(detail, view.verified ? '回看原文' : '查看当前原文',
          () => openSource(ref.source_id, view.verified ? view.text : undefined), isBusy());
      }
    }
    container.append(detail);
  }
  function addSource(source) {
    if (!contextSelectionEnabled) throw new Error('资料上下文已暂停，未添加片段。');
    const index = selected.findIndex(item => item.id === source.id);
    if (index < 0 && selected.length >= 4) throw new Error('本次最多选择 4 段原文。');
    if (index < 0) selected.push({ ...source }); else selected[index] = { ...source };
    $('ai-consent').checked = false;
    persist(); render();
  }
  function render() {
    if (!configured) return;
    const locked = isBusy() || !!state.pending;
    $('ai-question').disabled = locked;
    $('ai-consent').disabled = locked;
    $('ai-add-source').disabled = locked;
    $('ai-ask').disabled = isBusy();
    $('ai-ask').textContent = state.pending ? '继续读取上次提问' : '提问';
    $('ai-reload').disabled = isBusy();
    $('ai-release').hidden = !state.pending;
    $('ai-release').disabled = isBusy();
    $('ai-sources').replaceChildren();
    if (!selected.length) paragraph($('ai-sources'), '还未选择原文。', 'research-note');
    for (const source of selected) {
      const item = document.createElement('article');
      paragraph(item, source.locator || '所选原文', 'research-card-title');
      const detail = document.createElement('details'), title = document.createElement('summary');
      title.textContent = '查看保留的片段（不会发送，' + Array.from(source.quoted_text).length + ' 字符）';
      detail.append(title); paragraph(detail, source.quoted_text, 'research-ai-text'); item.append(detail);
      appendButton(item, '移除', async () => {
        selected = selected.filter(s => s.id !== source.id); $('ai-consent').checked = false; persist(); render();
      }, locked);
      $('ai-sources').append(item);
    }
    $('ai-answers').replaceChildren();
    for (const turn of turns.slice().reverse()) {
      const item = document.createElement('article'); item.dataset.turnId = turn.id; item.dataset.status = turn.status;
      paragraph(item, turn.user_input || '已失效的提问', 'research-card-title');
      if (turn.status === 'completed') {
        paragraph(item, turn.reply, 'research-ai-text');
        for (const [index, citation] of (turn.suggestions?.citations || []).entries()) {
          paragraph(item, '“' + citation.quote + '”', 'research-ai-quote');
          const button = appendButton(item, '出处 ' + (index + 1) + ' · 回看原文', async () => {
            await openSource(citation.source_id, citation.quote);
          }, isBusy());
          button.dataset.sourceId = citation.source_id;
        }
      } else {
        const messages = { running: '这次请求结果仍待确认。读取已有回答不会再次调用模型。',
          failed: '这次未获得可用回答，未自动重试。', stale: '资料或权限已变化，这次回答不再适用。', restricted: '当前无权读取这次回答或其来源。' };
        paragraph(item, (messages[turn.status] || '这次回答当前不可用。') + (turn.failure_code ? '（' + turn.failure_code + '）' : ''), 'error');
      }
      contextDetails(item, turn.context ?? {}, turn.status === 'restricted');
      $('ai-answers').append(item);
    }
  }
  async function refreshSelected() {
    const previous = selected;
    const restored = await Promise.allSettled(previous.map(source => request(projectPath('/sources/' + path(source.id)))));
    selected = restored.flatMap((item, index) => item.status === 'fulfilled'
      ? [refreshSourceExcerpt(previous[index], item.value)] : []);
    persist();
    return restored.some(item => item.status === 'rejected');
  }
  async function reload() {
    if (!state.sessionId) {
      const sessions = rows(await request(projectPath('/agents/research_agent/sessions')));
      state.sessionId = sessions.sort((a, b) => b.created_at.localeCompare(a.created_at))[0]?.id || null;
      persist();
    }
    turns = state.sessionId ? rows(await request(projectPath('/agent-sessions/' + path(state.sessionId) + '/turns'))) : [];
    const ids = new Set();
    for (const turn of turns) for (const ref of turn.context?.source_refs ?? []) ids.add(ref.source_id);
    const loaded = await Promise.allSettled([...ids].map(id => request(projectPath('/sources/' + path(id)))));
    historySources = new Map(loaded.filter(item => item.status === 'fulfilled').map(item => [item.value.id, item.value]));
    historyViews = new Map();
    const refs = turns.flatMap(turn => (turn.context?.source_refs ?? []).map(ref => ({ ref, restricted: turn.status === 'restricted' })));
    await Promise.all(refs.map(async ({ ref, restricted }) => {
      historyViews.set(ref, await resolveHistoricalSource(ref, historySources.get(ref.source_id), { restricted }));
    }));
    const known = state.pending?.turnId && turns.find(turn => turn.id === state.pending.turnId);
    if (known && known.status !== 'running') { state.pending = null; persist(); }
    render();
  }
  async function step(name, suffix, bodyFactory) {
    const pending = state.pending;
    let operation = pending.steps[name];
    if (!operation) {
      operation = { body: await bodyFactory(), key: crypto.randomUUID() };
      pending.steps[name] = operation; persist();
    }
    if (operation.done) return operation.value;
    const value = await request(projectPath(suffix), { method: 'POST', body: operation.body, key: operation.key });
    operation.done = true; operation.value = value; persist(); return value;
  }
  async function ask() {
    if (!contextSelectionEnabled) throw new Error('资料问答当前仅支持读取历史回答与查看出处，不会发送新请求。');
    if (!state.pending) {
      const question = $('ai-question').value.trim();
      if (!question || Array.from(question).length > 2000) throw new Error('请填写 1–2000 字符的问题。');
      if (!selected.length || !$('ai-consent').checked) throw new Error('请先选择原文，并勾选同意发送给 DeepSeek。');
      if (selected.length > 4 || selected.reduce((sum, s) => sum + Array.from(s.quoted_text).length, 0) > 12000) throw new Error('所选原文超过本片的长度限制，请移除部分片段。');
      const requestFields = sourceRequestFields(selected);
      state.pending = { question, sourceIds: selected.map(s => s.id),
        sourceHashes: Object.fromEntries(selected.map(s => [s.id, s.quoted_text_hash])),
        requestSourceIds: requestFields.source_ids, sourceExcerpts: requestFields.source_excerpts, steps: {} };
      persist();
    }
    const pending = state.pending;
    notice('正在核对所选原文与资料使用授权…'); render();
    // A resumed model request must replay verbatim, before fetching new context.
    if (!pending.model) {
      const sources = await Promise.all(pending.sourceIds.map(id => request(projectPath('/sources/' + path(id)))));
      if (sources.some(source => source.quoted_text_hash !== pending.sourceHashes[source.id])) {
        state.pending = null; persist();
        throw new Error('原文已变化，本次未发送给模型。请重新打开原文后选择。');
      }
      for (const assetId of new Set(sources.map(source => source.asset_id))) {
        const policy = await request(projectPath('/assets/' + path(assetId) + '/policy'));
        if (policy.model_use_allowed) continue;
        const draft = await step('grant:' + assetId, '/assets/' + path(assetId) + '/policy/proposals', async () => ({
          ...await expectation(policy.version), confirmed: true, reason: '用户在资料问答中明确允许所选资料用于 AI。',
          policy: { ...policy, model_use_allowed: true, purpose: '用户选择片段后用于资料问答', reason: '用户明确允许 DeepSeek 阅读所选资料。' },
        }));
        await step('assess:' + assetId, '/change-sets/' + path(draft.id) + '/assess', async () => ({}));
        await step('apply:' + assetId, '/change-sets/' + path(draft.id) + '/apply', async () => ({}));
      }
      if (!state.sessionId) {
        const session = await step('session', '/agents/research_agent/sessions', () => expectation());
        state.sessionId = session.id; persist();
      }
      pending.model = { key: crypto.randomUUID(), body: { ...await expectation(), user_input: pending.question,
        ...pendingSourceRequestFields(pending), card_refs: [], sensitive_context_confirmed: true } };
      persist();
    }
    notice('DeepSeek 正在阅读所选片段，最长约 1 分钟。');
    const turn = await request(projectPath('/agent-sessions/' + path(state.sessionId) + '/turns'), {
      method: 'POST', ...pending.model, timeoutMs: 75000,
    });
    pending.turnId = turn.id;
    if (turn.status !== 'running') state.pending = null;
    $('ai-question').value = '';
    await refreshSelected();
    persist();
    await reload(); await refresh();
    notice(turn.status === 'completed' ? '回答已保存。可点出处核对原文；文稿未改动。' :
      turn.status === 'running' ? '请求仍在处理中，请稍后读取上次结果。' : '这次未得到可用回答，未自动重试。', turn.status !== 'completed');
  }
  $('ai-add-source').onclick = () => action(async () => {
    if (state.pending) throw new Error('请先读取上次提问结果。');
    openSourcePicker();
  });
  $('ai-ask').onclick = () => action(async () => {
    try { await ask(); }
    catch (error) {
      if (error.status >= 400 && error.status < 500 && error.status !== 408) { state.pending = null; persist(); }
      notice(error.message + (state.pending ? ' 上次请求已保留；继续读取会复用原请求，不会新建提问。' : ''), true);
      throw error;
    }
  });
  $('ai-reload').onclick = () => action(async () => { await reload(); notice('已从本地数据库读取回答，本次未调用模型。'); });
  $('ai-release').onclick = () => action(async () => {
    if (!state.pending) return;
    state.unresolved = [...(state.unresolved || []), state.pending];
    state.pending = null; persist();
    await reload();
    notice('已结束等待，原请求与回答记录保留，未重发。可以继续读取历史；再次提问会发起一次新的模型调用。');
  });
  $('ai-question').oninput = persist;
  return {
    addSource, render, async init() {
      configured = getBootstrap().ai?.configured === true;
      $('ai').hidden = !configured;
      if (!configured) return;
      $('ai-model').textContent = document.body.classList.contains('debug-mode') ? 'DeepSeek · ' + getBootstrap().ai.model : 'DeepSeek';
      storageKey = 'lingdoc-source-ai:' + getBootstrap().projectId;
      const stored = localStorage.getItem(storageKey);
      if (stored) {
        try { state = JSON.parse(stored); }
        catch { state = { sessionId: null, pending: null, selected: [], draft: '' }; }
      }
      selected = Array.isArray(state.selected) ? state.selected : [];
      $('ai-question').value = state.draft ?? '';
      if (state.pending) {
        $('ai-question').value = state.pending.question; $('ai-consent').checked = true;
      }
      const unavailable = await refreshSelected();
      if (state.pending) notice(unavailable ? '上次提问的部分原文已不可读取。请求记录已保留，可结束等待并重新选择资料。' :
        '有一次提问待确认，可继续读取上次结果。', unavailable);
      else if (unavailable) notice('部分附件已不可读取，发送前请移除失效资料。', true);
      try { await reload(); }
      catch { notice('历史回答暂时不可读取。原请求记录仍保留，可以稍后读取或结束等待。', true); }
      render();
    },
  };
}
