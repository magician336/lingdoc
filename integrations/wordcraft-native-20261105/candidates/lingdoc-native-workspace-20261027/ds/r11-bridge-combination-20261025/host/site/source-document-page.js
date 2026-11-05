// Read-only parsed source text over the existing editor. The DOCX canvas and
// engine session stay alive; returning only restores an exact saved range.
export function mapTextareaSelectionToSource(documentText, selectionStart, selectionEnd) {
  const raw = Array.from(documentText);
  const entries = [];
  for (let index = 0; index < raw.length;) {
    if (raw[index] === '\r' && raw[index + 1] === '\n') {
      entries.push({ text: '\n', start: index, end: index + 2 }); index += 2;
    } else if (raw[index] === '\r') {
      entries.push({ text: '\n', start: index, end: index + 1 }); index += 1;
    } else {
      entries.push({ text: raw[index], start: index, end: index + 1 }); index += 1;
    }
  }
  // Textarea selection offsets are UTF-16 code-unit positions in normalized
  // text. Keep a sparse boundary map back to raw Unicode code-point offsets;
  // an offset inside a surrogate pair deliberately has no mapping.
  const boundaries = [0];
  let normalizedText = '', normalizedOffset = 0;
  for (const entry of entries) {
    normalizedText += entry.text;
    normalizedOffset += entry.text.length;
    boundaries[normalizedOffset] = entry.end;
  }
  const invalid = !Number.isInteger(selectionStart) || !Number.isInteger(selectionEnd) ||
    selectionStart < 0 || selectionEnd < selectionStart || selectionEnd > normalizedText.length ||
    boundaries[selectionStart] === undefined || boundaries[selectionEnd] === undefined;
  if (invalid) throw new Error('选区必须位于完整字符边界。');
  return { normalizedText, start: boundaries[selectionStart], end: boundaries[selectionEnd] };
}

export function makeSourceExcerpt({ source, documentText, segment, start, end, excerptTextHash }) {
  const selectionOutside = !Number.isInteger(start) || !Number.isInteger(end) ||
    !segment || segment.source_id !== source.id || start < segment.start_at || end > segment.end_at || end <= start;
  if (selectionOutside) throw new Error('请一次选择一个原文段落内的片段。');
  const characters = Array.from(documentText);
  const quotedText = characters.slice(start, end).join('');
  if (!quotedText) throw new Error('请一次选择一个原文段落内的片段。');
  return {
    ...source,
    id: source.id,
    source_id: source.id,
    selection_start: start - segment.start_at,
    selection_end: end - segment.start_at,
    quoted_text: quotedText,
    excerpt_text_hash: excerptTextHash,
    source_quoted_text_hash: source.quoted_text_hash,
  };
}

export function toSourceExcerptInput(excerpt) {
  return {
    source_id: excerpt.source_id,
    selection_start: excerpt.selection_start,
    selection_end: excerpt.selection_end,
    quoted_text: excerpt.quoted_text,
    excerpt_text_hash: excerpt.excerpt_text_hash,
    source_quoted_text_hash: excerpt.source_quoted_text_hash,
  };
}

// Pre-excerpt attachments and pending operations retain their original whole-
// source meaning. A partially migrated excerpt is never silently broadened.
export function sourceRequestFields(sources) {
  const sourceIds = [], sourceExcerpts = [];
  for (const source of sources) {
    const markers = [source.source_id, source.selection_start, source.selection_end, source.excerpt_text_hash];
    if (markers.every(value => value === undefined)) {
      sourceIds.push(source.id);
      continue;
    }
    const input = toSourceExcerptInput(source);
    const complete = typeof input.source_id === 'string' && Number.isInteger(input.selection_start) &&
      Number.isInteger(input.selection_end) && typeof input.quoted_text === 'string' &&
      typeof input.excerpt_text_hash === 'string' && typeof input.source_quoted_text_hash === 'string';
    if (!complete) throw new Error('资料选段信息不完整，请重新选择。');
    sourceExcerpts.push(input);
  }
  return {
    source_ids: sourceIds,
    ...(sourceExcerpts.length || !sourceIds.length ? { source_excerpts: sourceExcerpts } : {}),
  };
}

export function pendingSourceRequestFields(pending) {
  if (pending.sourceExcerpts === undefined) return { source_ids: [...pending.sourceIds] };
  if (!Array.isArray(pending.sourceExcerpts)) throw new Error('上次资料选段记录不完整，请结束后重新选择。');
  return { source_ids: [...(pending.requestSourceIds ?? [])], source_excerpts: pending.sourceExcerpts };
}

export async function resolveHistoricalSource(ref, source, { restricted = false } = {}) {
  const unavailable = { text: '原文当前不可读取。', verified: false, changed: false };
  const changed = { text: '出处已变化，原文当前不可读取。', verified: false, changed: true };
  if (restricted) return unavailable;
  if (!source) return unavailable;
  const sameSource = source.id === ref.source_id && source.asset_revision !== undefined &&
    source.asset_revision === ref.asset_revision && typeof source.quoted_text_hash === 'string' &&
    source.quoted_text_hash === ref.quoted_text_hash && typeof source.quoted_text === 'string';
  if (!sameSource) return changed;
  const hasRange = ref.selection_start !== undefined || ref.selection_end !== undefined;
  if (!hasRange) return { text: source.quoted_text, verified: true, changed: false };
  if (!Number.isInteger(ref.selection_start) || !Number.isInteger(ref.selection_end) ||
    typeof ref.excerpt_text_hash !== 'string') return changed;
  const excerpt = Array.from(source.quoted_text).slice(ref.selection_start, ref.selection_end).join('');
  if (!excerpt) return changed;
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(excerpt));
  const hash = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('');
  return hash === ref.excerpt_text_hash ? { text: excerpt, verified: true, changed: false } : changed;
}

export function refreshSourceExcerpt(excerpt, source) {
  return {
    ...excerpt,
    ...source,
    id: excerpt.id,
    source_id: excerpt.source_id,
    selection_start: excerpt.selection_start,
    selection_end: excerpt.selection_end,
    quoted_text: excerpt.quoted_text,
    excerpt_text_hash: excerpt.excerpt_text_hash,
    source_quoted_text_hash: source.quoted_text_hash,
  };
}

export function createSourceDocumentPage({ request, projectPath, engine }) {
  const editor = document.getElementById('editor');
  if (!editor) throw new Error('缺少正文容器，无法打开资料原文。');
  let page = null, saved = null, pulseTimer = null, generation = 0;

  function element(tag, id, text = '') {
    const node = document.createElement(tag);
    if (id) node.id = id;
    node.textContent = text;
    return node;
  }
  function clearPulse() {
    clearTimeout(pulseTimer);
    pulseTimer = null;
  }
  function dismiss() {
    generation++;
    clearPulse();
    page?.remove();
    page = null;
    saved = null;
  }
  async function captureSelection() {
    const result = await engine({ op: 'selection' });
    const range = result.selection?.anchor && result.selection?.focus
      ? { anchor: result.selection.anchor, focus: result.selection.focus }
      : null;
    return {
      document_id: result.state.documentId,
      document_revision: result.state.revision,
      text: result.selection?.text || '',
      selection_range: range,
    };
  }
  function appendLocatedText(container, text, start, end) {
    const characters = Array.from(text);
    container.append(document.createTextNode(characters.slice(0, start).join('')));
    const mark = document.createElement('mark');
    mark.id = 'source-document-target';
    mark.textContent = characters.slice(start, end).join('');
    container.append(mark, document.createTextNode(characters.slice(end).join('')));
    mark.scrollIntoView({ block: 'center' });
    mark.classList.add('source-location-pulse');
    clearPulse();
    pulseTimer = setTimeout(() => mark.classList.remove('source-location-pulse'), 1500);
  }
  function showStaleReturn(expectedPage = page, expectedSaved = saved) {
    if (!expectedPage || !expectedSaved || page !== expectedPage || saved !== expectedSaved) return;
    expectedPage.querySelector('#source-document-status').textContent = '正文已变化，无法恢复原位置';
    const snapshot = expectedPage.querySelector('#source-document-return');
    snapshot.hidden = false;
    expectedPage.querySelector('#source-document-dismiss').hidden = false;
    expectedPage.querySelector('#source-document-return-snapshot').textContent = expectedSaved.text || '原位置没有文字选区。';
  }
  async function close() {
    if (!page || !saved) { dismiss(); return; }
    const closePage = page, closeSaved = saved, closeGeneration = generation;
    const isCurrent = () => generation === closeGeneration && page === closePage && saved === closeSaved;
    let state;
    try { state = await engine({ op: 'status' }); }
    catch {
      if (!isCurrent()) return;
      closePage.querySelector('#source-document-status').textContent = '无法确认正文版本，未恢复原位置';
      closePage.querySelector('#source-document-dismiss').hidden = false;
      return;
    }
    if (!isCurrent()) return;
    if (state.documentId !== closeSaved.document_id || state.revision !== closeSaved.document_revision) {
      showStaleReturn(closePage, closeSaved);
      return;
    }
    try {
      if (closeSaved.selection_range) {
        await engine({
          op: 'select',
          range: closeSaved.selection_range,
          expectedDocumentId: closeSaved.document_id,
          expectedRevision: closeSaved.document_revision,
        });
      }
    } catch {
      if (isCurrent()) showStaleReturn(closePage, closeSaved);
      return;
    }
    if (!isCurrent()) return;
    dismiss();
  }
  async function open({ sourceId, mode = 'read', excerpt, onExcerpt } = {}) {
    if (!sourceId || !['read', 'select'].includes(mode)) throw new Error('资料出处或阅读模式无效。');
    if (mode === 'select' && typeof onExcerpt !== 'function') throw new Error('缺少选段接收方。');
    const currentGeneration = ++generation;
    const selection = page && saved ? saved : await captureSelection();
    const sourceDocument = await request(projectPath('/sources/' + encodeURIComponent(sourceId) + '/document'));
    if (currentGeneration !== generation) return;
    if (!sourceDocument || typeof sourceDocument.text !== 'string' || !Array.isArray(sourceDocument.segments)) {
      throw new Error('资料原文响应不完整，未打开原文页。');
    }
    saved = selection;
    clearPulse();
    page?.remove();

    page = element('section', 'source-document-page');
    page.setAttribute('aria-label', '资料原文');
    const header = document.createElement('header');
    const heading = document.createElement('div');
    heading.append(element('p', 'source-document-kind', mode === 'select' ? '资料原文 · 自由选段' : '资料原文 · 出处阅读'));
    heading.append(element('h2', 'source-document-title', sourceDocument.source?.locator || sourceDocument.source?.title || '原文'));
    const closeButton = element('button', 'source-document-close', '返回正文');
    closeButton.type = 'button';
    closeButton.onclick = () => { close().catch(() => showStaleReturn()); };
    header.append(heading, closeButton);
    const body = document.createElement('div');
    body.className = 'source-document-body';
    body.append(element('p', 'source-document-status'));
    body.querySelector('#source-document-status').setAttribute('role', 'status');
    const content = mode === 'select'
      ? element('textarea', 'source-document-select-text')
      : element('pre', 'source-document-text');
    if (mode === 'select') {
      content.readOnly = true;
      content.value = sourceDocument.text;
      content.setAttribute('aria-label', '只读资料原文，拖选几句话后添加为附件');
    }
    body.append(content);
    const returnSnapshot = element('section', 'source-document-return');
    returnSnapshot.hidden = true;
    returnSnapshot.append(element('h3', '', '原正文选区快照'));
    returnSnapshot.append(element('p', 'source-document-return-snapshot'));
    const dismissButton = element('button', 'source-document-dismiss', '放弃恢复并返回正文');
    dismissButton.type = 'button';
    dismissButton.hidden = true;
    dismissButton.onclick = dismiss;
    body.append(returnSnapshot, dismissButton);
    page.append(header, body);
    editor.append(page);

    const segment = sourceDocument.segments.find(item => item.source_id === sourceId);
    const length = Array.from(sourceDocument.text).length;
    const validRange = segment && Number.isInteger(segment.start_at) && Number.isInteger(segment.end_at) &&
      segment.start_at >= 0 && segment.end_at > segment.start_at && segment.end_at <= length;
    const located = validRange ? Array.from(sourceDocument.text).slice(segment.start_at, segment.end_at).join('') : '';
    const sourceExcerpt = sourceDocument.source?.quoted_text || '';
    const sourceMatches = Boolean(sourceExcerpt) && located === sourceExcerpt;
    const excerptMatches = excerpt === undefined ||
      (typeof excerpt === 'string' && excerpt.length > 0 && sourceExcerpt.includes(excerpt) && located.includes(excerpt));
    if (validRange && sourceMatches && excerptMatches && mode === 'read') {
      appendLocatedText(content, sourceDocument.text, segment.start_at, segment.end_at);
    } else if (mode === 'select') {
      const status = page.querySelector('#source-document-status');
      status.textContent = validRange && sourceMatches ? '在一个原文段落内拖选几句话，再添加为当前对话的附件。' : '出处已变化，无法精确选段';
      const addButton = element('button', 'source-document-add-excerpt', '添加所选片段');
      addButton.type = 'button';
      addButton.disabled = !(validRange && sourceMatches);
      addButton.onclick = async () => {
        addButton.disabled = true;
        try {
          const mapped = mapTextareaSelectionToSource(sourceDocument.text, content.selectionStart, content.selectionEnd);
          if (mapped.normalizedText !== content.value) throw new Error('原文显示与来源文本不一致，请重新打开。');
          const { start, end } = mapped;
          const text = Array.from(sourceDocument.text).slice(start, end).join('');
          const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text));
          const excerptTextHash = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('');
          const selected = makeSourceExcerpt({ source: sourceDocument.source, documentText: sourceDocument.text,
            segment, start, end, excerptTextHash });
          await onExcerpt(selected);
          await close();
        } catch (error) {
          status.textContent = error.message;
          addButton.disabled = false;
        }
      };
      body.insertBefore(addButton, returnSnapshot);
    } else {
      content.textContent = sourceDocument.text;
      page.querySelector('#source-document-status').textContent = '出处已变化，无法精确定位';
    }
  }

  return { open, close };
}
