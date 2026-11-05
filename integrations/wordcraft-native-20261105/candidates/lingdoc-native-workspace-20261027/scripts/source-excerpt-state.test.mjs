import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import test from 'node:test';
import {
  makeSourceExcerpt, mapTextareaSelectionToSource, pendingSourceRequestFields,
  refreshSourceExcerpt, resolveHistoricalSource, sourceRequestFields, toSourceExcerptInput,
} from '../ds/r11-bridge-combination-20261025/host/site/source-excerpt-state.js';

const sha256 = text => createHash('sha256').update(text).digest('hex');

const source = {
  id: 's1', asset_id: 'a1', asset_revision: 3, model_use_allowed: true,
  locator: '学习方法 · 第 1 段', quoted_text: '学生不是没有时间，而是不知道怎样开始',
  quoted_text_hash: 'full-hash', status: 'available',
};
const segment = { source_id: 's1', chunk_index: 0, start_at: 0, end_at: 20 };

test('maps a global text selection to one source excerpt', () => {
  const excerpt = makeSourceExcerpt({
    source, documentText: source.quoted_text, segment: { ...segment, start_at: 11, end_at: 18 },
    start: 11, end: 18, excerptTextHash: 'excerpt-hash',
  });
  assert.equal(excerpt.id, 's1');
  assert.equal(excerpt.source_id, 's1');
  assert.equal(excerpt.selection_start, 0);
  assert.equal(excerpt.selection_end, 7);
  assert.equal(excerpt.quoted_text, '不知道怎样开始');
  assert.equal(excerpt.excerpt_text_hash, 'excerpt-hash');
  assert.equal(excerpt.source_quoted_text_hash, 'full-hash');
});

test('converts a global segment offset to a relative Unicode range and preserves metadata', () => {
  const excerpt = makeSourceExcerpt({
    source: { ...source, quoted_text: '前文。学生不是没有时间，而是不知道怎样开始' },
    documentText: '前文。学生不是没有时间，而是不知道怎样开始',
    segment: { ...segment, start_at: 3, end_at: 23 },
    start: 14, end: 21, excerptTextHash: 'excerpt-hash',
  });
  assert.equal(excerpt.selection_start, 11);
  assert.equal(excerpt.selection_end, 18);
  assert.equal(excerpt.quoted_text, '不知道怎样开始');
  assert.equal(excerpt.asset_id, 'a1');
  assert.equal(excerpt.asset_revision, 3);
  assert.equal(excerpt.model_use_allowed, true);
  assert.equal(excerpt.locator, '学习方法 · 第 1 段');
});

test('rejects selections outside or across the current source segment', () => {
  assert.throws(() => makeSourceExcerpt({
    source, documentText: source.quoted_text, segment: { ...segment, start_at: 5, end_at: 18 },
    start: 4, end: 18, excerptTextHash: 'excerpt-hash',
  }), /请一次选择一个原文段落内的片段/);
  assert.throws(() => makeSourceExcerpt({
    source, documentText: source.quoted_text, segment: { ...segment, start_at: 0, end_at: 12 },
    start: 11, end: 18, excerptTextHash: 'excerpt-hash',
  }), /请一次选择一个原文段落内的片段/);
});

test('maps exactly the six API source excerpt fields', () => {
  const excerpt = makeSourceExcerpt({
    source, documentText: source.quoted_text, segment, start: 11, end: 18, excerptTextHash: 'excerpt-hash',
  });
  assert.deepEqual(toSourceExcerptInput(excerpt), {
    source_id: 's1', selection_start: 11, selection_end: 18,
    quoted_text: '不知道怎样开始', excerpt_text_hash: 'excerpt-hash', source_quoted_text_hash: 'full-hash',
  });
  assert.deepEqual(Object.keys(toSourceExcerptInput(excerpt)).sort(), [
    'excerpt_text_hash', 'quoted_text', 'selection_end', 'selection_start', 'source_id', 'source_quoted_text_hash',
  ].sort());
});

test('refresh updates source identity and authorization without replacing the excerpt', () => {
  const excerpt = makeSourceExcerpt({
    source, documentText: source.quoted_text, segment, start: 11, end: 18, excerptTextHash: 'excerpt-hash',
  });
  const refreshed = refreshSourceExcerpt(excerpt, {
    ...source, asset_revision: 4, model_use_allowed: false,
    quoted_text: '完整来源已经更新，不能覆盖原选段', quoted_text_hash: 'new-full-hash',
  });
  assert.equal(refreshed.asset_revision, 4);
  assert.equal(refreshed.model_use_allowed, false);
  assert.equal(refreshed.source_quoted_text_hash, 'new-full-hash');
  assert.equal(refreshed.selection_start, 11);
  assert.equal(refreshed.selection_end, 18);
  assert.equal(refreshed.quoted_text, '不知道怎样开始');
  assert.equal(refreshed.excerpt_text_hash, 'excerpt-hash');
});

test('maps textarea offsets after CRLF and non-BMP text back to the raw source', () => {
  const documentText = '开头\r\n😀学生不是没有时间，而是不知道怎样开始\r\n结尾';
  const normalizedText = '开头\n😀学生不是没有时间，而是不知道怎样开始\n结尾';
  const selected = '不知道怎样开始';
  const selectionStart = normalizedText.indexOf(selected);
  const mapped = mapTextareaSelectionToSource(documentText, selectionStart, selectionStart + selected.length);
  const expectedStart = Array.from('开头\r\n😀学生不是没有时间，而是').length;
  assert.equal(mapped.normalizedText, normalizedText);
  assert.equal(mapped.start, expectedStart);
  assert.equal(mapped.end, expectedStart + 7);
  const excerpt = makeSourceExcerpt({ source, documentText,
    segment: { source_id: source.id, start_at: 0, end_at: Array.from(documentText).length },
    start: mapped.start, end: mapped.end, excerptTextHash: 'excerpt-hash' });
  assert.equal(excerpt.quoted_text, selected);
  assert.equal(excerpt.selection_start, expectedStart);
  assert.equal(excerpt.selection_end, expectedStart + 7);
});

test('preserves original CR and CRLF inside a selection with non-BMP characters', () => {
  const documentText = '甲😀\r不知道怎样开始\r\n乙';
  const normalizedText = '甲😀\n不知道怎样开始\n乙';
  const selected = '😀\n不知道怎样开始';
  const selectionStart = normalizedText.indexOf(selected);
  const mapped = mapTextareaSelectionToSource(documentText, selectionStart, selectionStart + selected.length);
  assert.equal(mapped.normalizedText, normalizedText);
  const excerpt = makeSourceExcerpt({ source, documentText,
    segment: { source_id: source.id, start_at: 0, end_at: Array.from(documentText).length },
    start: mapped.start, end: mapped.end, excerptTextHash: 'excerpt-hash' });
  assert.equal(excerpt.quoted_text, '😀\r不知道怎样开始');
  assert.equal(excerpt.selection_start, 1);
  assert.equal(excerpt.selection_end, 10);
  assert.throws(() => mapTextareaSelectionToSource(documentText, 2, 3), /字符边界/);
});

test('legacy pending keeps full-source IDs instead of inventing excerpt fields', () => {
  const pending = { sourceIds: ['legacy-1', 'legacy-2'] };
  assert.deepEqual(pendingSourceRequestFields(pending), { source_ids: ['legacy-1', 'legacy-2'] });
});

test('restored legacy attachments keep full-source semantics beside new excerpts', () => {
  const excerpt = makeSourceExcerpt({
    source, documentText: source.quoted_text, segment, start: 11, end: 18, excerptTextHash: 'excerpt-hash',
  });
  assert.deepEqual(sourceRequestFields([source, excerpt]), {
    source_ids: ['s1'], source_excerpts: [toSourceExcerptInput(excerpt)],
  });
  assert.throws(() => sourceRequestFields([{ ...source, source_id: source.id, selection_start: 0 }]), /重新选择/);
});

function historicalFixture() {
  const full = '学生不是没有时间，而是不知道怎样开始';
  const quoted = '不知道怎样开始';
  const ref = {
    source_id: 's1', asset_revision: 3, quoted_text: quoted,
    quoted_text_hash: sha256(full), excerpt_text_hash: sha256(quoted), selection_start: 11, selection_end: 18,
  };
  return { full, quoted, ref, current: { ...source, quoted_text: full, quoted_text_hash: sha256(full) } };
}

test('history displays an excerpt only after source identity and both hashes verify', async () => {
  const { ref, current, quoted } = historicalFixture();
  assert.deepEqual(await resolveHistoricalSource(ref, current), { text: quoted, verified: true, changed: false });
});

test('changed source, revision, or excerpt hash never substitutes current text', async () => {
  const { ref, current } = historicalFixture();
  for (const changed of [
    { ...current, quoted_text: '当前文本X有时间，而是不知道怎样开始', quoted_text_hash: 'changed' },
    { ...current, asset_revision: 4 },
    current,
  ]) {
    const changedRef = changed === current ? { ...ref, excerpt_text_hash: sha256('别的选段') } : ref;
    const view = await resolveHistoricalSource(changedRef, changed);
    assert.equal(view.verified, false);
    assert.equal(view.changed, true);
    assert.match(view.text, /出处已变化/);
    assert.doesNotMatch(view.text, /当前文本/);
  }
});

test('restricted or unavailable history never falls back to a current or stored quote', async () => {
  const { ref, current } = historicalFixture();
  assert.deepEqual(await resolveHistoricalSource(ref, current, { restricted: true }),
    { text: '原文当前不可读取。', verified: false, changed: false });
  assert.deepEqual(await resolveHistoricalSource(ref, null),
    { text: '原文当前不可读取。', verified: false, changed: false });
});

test('legacy history requires the same full-source hash and asset revision', async () => {
  const { ref, current, full } = historicalFixture();
  const legacy = { source_id: ref.source_id, asset_revision: ref.asset_revision, quoted_text_hash: ref.quoted_text_hash };
  assert.deepEqual(await resolveHistoricalSource(legacy, current), { text: full, verified: true, changed: false });
  assert.equal((await resolveHistoricalSource({ ...legacy, asset_revision: 2 }, current)).changed, true);
});
