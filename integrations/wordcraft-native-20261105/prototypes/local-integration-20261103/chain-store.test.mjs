// Real filesystem persistence/CAS tests; source and DOCX network services are
// controlled test doubles. Browser/Go acceptance is a separate verification.
import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, mkdir, rename } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createChainStore, SOURCE_TEXT } from './chain-store.mjs';

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), 'runtime', 'store-tests');
await mkdir(root, { recursive: true });
async function fixture() {
  const directory = await mkdtemp(path.join(root, 'case-'));
  let version = { id: 'saved-1', diskVersion: 1, docSha256: 'a'.repeat(64) };
  const uploads = [];
  const sources = { async uploadText({ title, text, key }) {
    uploads.push({ title, text, key });
    return { projectId: 'p1', knowledgeId: `knowledge-${uploads.length}`, asset: { id: `asset-${uploads.length}` }, sourceId: `source-${uploads.length}`, document: { text } };
  } };
  const options = { directory, projectId: 'p1', sources, latestVersion: async () => structuredClone(version) };
  const store = await createChainStore(options);
  return { store, options, directory, uploads, setVersion: value => { version = value; } };
}
const decide = (store, fields = {}) => store.act({ key: 'decision-key-001', expectedRevision: store.read().revision, type: 'decide', id: 'C01', decision: 'modify_document', reason: '按意见收窄适用范围', ...fields });

test('state, decision history and idempotency survive opening another store', async () => {
  const f = await fixture();
  const request = { key: 'persist-decision-001', expectedRevision: 1, type: 'decide', id: 'C01', decision: 'defer', reason: '等待进一步说明' };
  await f.store.act(request);
  const reopened = await createChainStore(f.options);
  assert.equal(f.uploads.length, 1, 'existing state must not seed another source');
  assert.deepEqual(reopened.read(), f.store.read());
  const replay = await reopened.act(request);
  assert.equal(replay.revision, 2);
  assert.equal(replay.records.C01.history.length, 1);
  assert.equal(replay.events.length, 1);
  await assert.rejects(reopened.act({ ...request, reason: 'changed' }), { status: 409 });
});

test('two concurrent updates from one revision cannot overwrite each other', async () => {
  const f = await fixture();
  const outcomes = await Promise.allSettled([
    decide(f.store, { key: 'concurrent-key-001', expectedRevision: 1 }),
    decide(f.store, { key: 'concurrent-key-002', expectedRevision: 1, decision: 'defer' }),
  ]);
  assert.equal(outcomes.filter(row => row.status === 'fulfilled').length, 1);
  assert.equal(outcomes.find(row => row.status === 'rejected').reason.status, 409);
  assert.equal(f.store.read().revision, 2);
  assert.equal(f.store.read().events.length, 1);
  const reopened = await createChainStore(f.options);
  assert.equal(reopened.read().records.C01.decision, 'modify_document');
});

test('concurrent retry of the identical operation creates one durable event', async () => {
  const f = await fixture();
  const input = { key: 'duplicate-key-001', expectedRevision: 1, type: 'decide', id: 'C01', decision: 'no_change', reason: '核对适用范围后维持原文' };
  const [a, b] = await Promise.all([f.store.act(input), f.store.act(input)]);
  assert.deepEqual(a, b);
  assert.equal(a.revision, 2);
  assert.equal(a.records.C01.progress, 'completed');
  assert.equal(a.events.length, 1);
});

test('read returns an isolated snapshot and does not expose idempotency storage', async () => {
  const f = await fixture();
  const returned = f.store.read();
  assert.equal(returned.applied, undefined);
  returned.records.C01.progress = 'completed';
  returned.sourceVersions.v1.text = 'rewritten';
  assert.equal(f.store.read().records.C01.progress, 'awaiting_decision');
  assert.equal(f.store.read().sourceVersions.v1.text, SOURCE_TEXT.v1);
});

test('completion rejects absent, unchanged and mismatched saved evidence without persisting', async () => {
  const f = await fixture();
  await decide(f.store);
  const revision = f.store.read().revision;
  const attempt = (suffix, savedVersion) => f.store.act({ key: `evidence-key-${suffix}`, expectedRevision: revision, type: 'complete', id: 'C01', reason: '已修改', savedVersion });
  await assert.rejects(attempt('absent', undefined), { status: 409 });
  await assert.rejects(attempt('same', { diskVersion: 1, docSha256: 'a'.repeat(64) }), { status: 409 });
  f.setVersion({ id: 'saved-2', diskVersion: 2, docSha256: 'a'.repeat(64) });
  await assert.rejects(attempt('duplicate-bytes', { diskVersion: 2, docSha256: 'a'.repeat(64) }), { status: 409 });
  f.setVersion({ id: 'saved-2', diskVersion: 2, docSha256: 'b'.repeat(64) });
  await assert.rejects(attempt('wrong-hash', { diskVersion: 2, docSha256: 'c'.repeat(64) }), { status: 409 });
  assert.equal(f.store.read().revision, revision);
  assert.equal(f.store.read().records.C01.progress, 'pending');
  const reopened = await createChainStore(f.options);
  assert.equal(reopened.read().records.C01.progress, 'pending');
});

test('human completion binds the actual newer saved version and survives restart', async () => {
  const f = await fixture();
  await decide(f.store);
  f.setVersion({ id: 'saved-2', diskVersion: 2, docSha256: 'b'.repeat(64) });
  await f.store.act({ key: 'complete-key-001', expectedRevision: 2, type: 'complete', id: 'C01', reason: '去掉全市概括并说明选择标准', savedVersion: { diskVersion: 2, docSha256: 'b'.repeat(64) } });
  const reopened = await createChainStore(f.options);
  const row = reopened.read().records.C01;
  assert.equal(row.progress, 'completed');
  assert.deepEqual(row.resultVersion, { diskVersion: 2, versionId: 'saved-2', docSha256: 'b'.repeat(64) });
  assert.equal(row.history.at(-1).humanConfirmed, true);
  assert.equal(row.completionReason, '去掉全市概括并说明选择标准');
});

test('source publication retains v1 and creates review bases at the current saved version exactly once', async () => {
  const f = await fixture();
  await assert.rejects(decide(f.store, { id: 'R1' }), { status: 409 });
  await assert.rejects(f.store.act({ key: 'empty-source-text', expectedRevision: 1, type: 'publish_source', text: '' }), { status: 400 });
  assert.equal(f.uploads.length, 1, 'explicit empty text must not publish demonstration content');
  f.setVersion({ id: 'saved-3', diskVersion: 3, docSha256: 'c'.repeat(64) });
  const request = { key: 'publish-contact-v2', expectedRevision: 1, type: 'publish_source' };
  await f.store.act(request);
  const reopened = await createChainStore(f.options);
  await reopened.act(request);
  assert.equal(f.uploads.length, 2);
  const state = reopened.read();
  assert.equal(state.sourceVersions.v1.text, SOURCE_TEXT.v1);
  assert.equal(state.sourceVersions.v2.text, SOURCE_TEXT.v2);
  assert.equal(state.sourceLatest, 2);
  for (const id of ['R1', 'R2', 'R3']) {
    assert.equal(state.records[id].baseVersion, 3);
    assert.equal(state.records[id].progress, 'awaiting_decision');
    assert.equal(state.records[id].change.from, 'CONTACT-v1');
    assert.equal(state.records[id].change.to, 'CONTACT-v2');
  }
  await assert.rejects(reopened.act({ ...request, key: 'publish-contact-again', expectedRevision: state.revision }), { status: 409 });
});

test('unsupported plan and figure work cannot be completed using a DOCX save', async () => {
  const f = await fixture();
  await decide(f.store, { decision: 'update_plan' });
  f.setVersion({ id: 'saved-2', diskVersion: 2, docSha256: 'b'.repeat(64) });
  await assert.rejects(f.store.act({ key: 'plan-complete-001', expectedRevision: 2, type: 'complete', id: 'C01', reason: '保存过正文', savedVersion: { diskVersion: 2, docSha256: 'b'.repeat(64) } }), { status: 409 });
  await f.store.act({ key: 'publish-figure-001', expectedRevision: 2, type: 'publish_source' });
  await decide(f.store, { key: 'figure-decision-001', id: 'R3' });
  f.setVersion({ id: 'saved-3', diskVersion: 3, docSha256: 'c'.repeat(64) });
  await assert.rejects(f.store.act({ key: 'figure-complete-001', expectedRevision: 4, type: 'complete', id: 'R3', reason: '保存过正文', savedVersion: { diskVersion: 3, docSha256: 'c'.repeat(64) } }), { status: 409 });
  assert.equal(f.store.read().records.R3.progress, 'pending');
});

test('no-change requires a reason; rejected actions do not block later valid actions', async () => {
  const f = await fixture();
  await assert.rejects(decide(f.store, { decision: 'no_change', reason: '  ' }), { status: 400 });
  await decide(f.store, { decision: 'defer' });
  assert.equal(f.store.read().records.C01.progress, 'deferred');
});

test('project mismatch and invalid JSON fail closed without replacing existing records', async () => {
  const f = await fixture();
  const file = path.join(f.directory, 'review-chain.json');
  const before = await readFile(file, 'utf8');
  await assert.rejects(createChainStore({ ...f.options, projectId: 'different-project' }), /identity mismatch/);
  assert.equal(await readFile(file, 'utf8'), before);
  await writeFile(file, '{incomplete JSON');
  await assert.rejects(createChainStore(f.options), SyntaxError);
  assert.equal(await readFile(file, 'utf8'), '{incomplete JSON');
  assert.equal(f.uploads.length, 1);
});

test('failed durable write does not advance state or consume the request key', async () => {
  const f = await fixture();
  const file = path.join(f.directory, 'review-chain.json');
  const temporary = `${file}.${process.pid}.tmp`;
  // Make the exact temporary filename unwritable as a file; preserve the fault
  // fixture by renaming it rather than deleting any test evidence.
  await mkdir(temporary);
  const request = { key: 'write-failure-key-001', expectedRevision: 1, type: 'decide', id: 'C01', decision: 'defer', reason: '等待意见澄清' };
  await assert.rejects(f.store.act(request));
  assert.equal(f.store.read().revision, 1);
  assert.equal(JSON.parse(await readFile(file, 'utf8')).revision, 1);
  await rename(temporary, path.join(f.directory, 'retained-write-fault'));
  await f.store.act(request);
  const reopened = await createChainStore(f.options);
  assert.equal(reopened.read().revision, 2);
  assert.equal(reopened.read().events.length, 1);
});
