export function makeSelectionSnapshot(result, textSha256) {
  const text = result?.selection?.text ?? '';
  if (!text.trim() || Array.from(text).length > 6000) throw new Error('请选择 1–6000 字符的正文内容。');
  const selection_range = result?.selection?.anchor && result?.selection?.focus
    ? { anchor: result.selection.anchor, focus: result.selection.focus }
    : undefined;
  return {
    document_id: result.state.documentId,
    document_revision: result.state.revision,
    text,
    text_sha256: textSha256,
    locator: '正文选区',
    ...(selection_range ? { selection_range } : {}),
  };
}

export function compatibleTurnIds(turns, selection, sources, contextRevision) {
  const allowed = new Map(sources.map(source => [source.id, source]));
  return turns.filter(turn => {
    const context = turn.context ?? {};
    if (turn.status !== 'completed' || context.context_revision !== contextRevision) return false;
    if (context.document_selection && context.document_selection.text_sha256 !== selection?.text_sha256) return false;
    return (context.source_refs ?? []).every(ref => {
      const current = allowed.get(ref.source_id);
      return current && current.asset_revision === ref.asset_revision &&
        current.quoted_text_hash === ref.quoted_text_hash &&
        (current.excerpt_text_hash ?? '') === (ref.excerpt_text_hash ?? '') && current.model_use_allowed;
    });
  }).map(turn => turn.id);
}

export function historyNotice(turns, includedIds, selection, sources, contextRevision) {
  const included = new Set(includedIds);
  const allowed = new Map(sources.map(source => [source.id, source]));
  const reasons = {};
  let excluded = 0;
  for (const turn of turns) {
    if (included.has(turn.id) || turn.status !== 'completed') continue;
    const context = turn.context ?? {};
    let reason = 'context_changed';
    if (context.context_revision === contextRevision) {
      if (context.document_selection && context.document_selection.text_sha256 !== selection?.text_sha256) reason = 'selection_changed';
      else if ((context.source_refs ?? []).some(ref => {
        const current = allowed.get(ref.source_id);
        return !current || current.asset_revision !== ref.asset_revision ||
          current.quoted_text_hash !== ref.quoted_text_hash ||
          (current.excerpt_text_hash ?? '') !== (ref.excerpt_text_hash ?? '') || !current.model_use_allowed;
      })) reason = 'sources_changed';
    }
    reasons[reason] = (reasons[reason] ?? 0) + 1;
    excluded++;
  }
  return { excluded, reasons };
}

export function nextPending(previous, serialized) {
  if (previous && previous.serialized !== serialized) throw new Error('上次操作结果待确认，请先读取原回答。');
  return previous ?? { serialized, key: crypto.randomUUID() };
}
