import assert from 'node:assert/strict';
import test from 'node:test';
import { compatibleTurnIds, historyNotice, makeSelectionSnapshot, nextPending } from '../ds/r11-bridge-combination-20261025/host/site/workspace-assistant-state.js';

const turn = (id, status = 'completed', selection = null, sources = []) => ({ id, status,
  context: { context_revision: 1, document_selection: selection, source_refs: sources } });

test('keeps plain and same-selection turns and reports exclusions', () => {
  const selection = { text_sha256: 'same' };
  const turns = [turn('plain'), turn('same', 'completed', selection), turn('old', 'completed', { text_sha256: 'old' })];
  const included = compatibleTurnIds(turns, selection, [], 1);
  assert.deepEqual(included, ['plain', 'same']);
  assert.deepEqual(historyNotice(turns, included, selection, [], 1), { excluded: 1, reasons: { selection_changed: 1 } });
});

test('requires current authorized sources and ignores running turns in notice', () => {
  const oldSource = { source_id: 'source-1', asset_revision: 1, quoted_text_hash: 'hash' };
  const turns = [turn('source', 'completed', null, [oldSource]), turn('running', 'running')];
  const included = compatibleTurnIds(turns, null, [], 1);
  assert.deepEqual(included, []);
  assert.deepEqual(historyNotice(turns, included, null, [], 1), { excluded: 1, reasons: { sources_changed: 1 } });
});

test('history compatibility retains excerpt identity for the same source', () => {
  const ref = { source_id: 'source-1', asset_revision: 1, quoted_text_hash: 'full-hash', excerpt_text_hash: 'excerpt-a' };
  const current = { id: 'source-1', asset_revision: 1, quoted_text_hash: 'full-hash', excerpt_text_hash: 'excerpt-a', model_use_allowed: true };
  const turns = [turn('same', 'completed', null, [ref]), turn('changed', 'completed', null, [{ ...ref, excerpt_text_hash: 'excerpt-b' }])];
  const included = compatibleTurnIds(turns, null, [current], 1);
  assert.deepEqual(included, ['same']);
  assert.deepEqual(historyNotice(turns, included, null, [current], 1), { excluded: 1, reasons: { sources_changed: 1 } });
});

test('reports removed sources even when the same selection remains', () => {
  const selection = { text_sha256: 'same' };
  const oldSource = { source_id: 'source-1', asset_revision: 1, quoted_text_hash: 'hash' };
  const turns = [turn('combined', 'completed', selection, [oldSource])];
  assert.deepEqual(historyNotice(turns, [], selection, [], 1), { excluded: 1, reasons: { sources_changed: 1 } });
});

test('captures only explicit nonempty selection snapshots', () => {
  const result = { selection: { text: '正文' }, state: { documentId: 2, revision: 7 } };
  assert.deepEqual(makeSelectionSnapshot(result, 'hash'), {
    document_id: 2, document_revision: 7, text: '正文', text_sha256: 'hash', locator: '正文选区',
  });
  assert.throws(() => makeSelectionSnapshot({ selection: { text: '' }, state: result.state }, 'hash'), /请选择/);
});

test('captures selection text and restorable range', () => {
  const range = {
    anchor: { story: 'body', path: [4, 0, 0, 2], off: 3 },
    focus: { story: 'body', path: [4, 0, 0, 2], off: 9 },
  };
  const result = { selection: { text: '正文', ...range }, state: { documentId: 2, revision: 7 } };
  const snapshot = makeSelectionSnapshot(result, 'hash');
  assert.deepEqual(snapshot.selection_range, range);
  assert.equal(snapshot.document_revision, 7);
});

test('pending request replays exactly one payload and key', () => {
  const first = nextPending(null, JSON.stringify({ question: '问' }));
  assert.equal(nextPending(first, first.serialized), first);
  assert.throws(() => nextPending(first, JSON.stringify({ question: '改' })), /上次操作结果待确认/);
});
