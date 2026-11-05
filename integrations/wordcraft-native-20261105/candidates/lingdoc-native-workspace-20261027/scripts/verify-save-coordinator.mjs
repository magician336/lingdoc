import assert from 'node:assert/strict';
import { WorkspaceSaveCoordinator } from '../ds/r11-bridge-combination-20261025/host/site/save-coordinator.js';

const receipt = (overrides = {}) => ({
  documentId: 'doc-1', revision: 7, diskVersion: 3, sha256: 'a'.repeat(64),
  saveToken: 'token-7', ...overrides,
});
const version = (sha256 = 'a'.repeat(64)) => ({ id: 'saved-1', diskVersion: 1, docSha256: sha256 });
const delivery = (sha256 = 'a'.repeat(64)) => ({ version: version(sha256), chapters: [] });

function adapterFixture(overrides = {}) {
  const calls = { persist: 0, register: 0, delivery: 0, ack: 0, release: 0, status: 0 };
  return {
    calls,
    async persistSavedFile() { calls.persist += 1; return { receipt: receipt() }; },
    async registerSavedVersion(value) { calls.register += 1; assert.equal(value.sha256, 'a'.repeat(64)); return { version: version() }; },
    async deliveryStatus() { calls.delivery += 1; return delivery(); },
    async ackSavedRevision() { calls.ack += 1; return { revision: 7, dirty: false }; },
    releaseSavedRequest() { calls.release += 1; },
    async status() { calls.status += 1; return { documentId: 'doc-1', revision: 7, dirty: false }; },
    ...overrides,
  };
}

const tests = [];
async function test(name, run) {
  try { await run(); tests.push({ name, status: 'passed' }); }
  catch (error) { tests.push({ name, status: 'failed', error: error.stack }); }
}

await test('原生 ack 必须发生在 Go 登记和读回一致之后', async () => {
  const order = [];
  const adapter = {
    async persistSavedFile() { order.push('ds'); return { receipt: receipt() }; },
    async registerSavedVersion() { order.push('go'); return { version: version() }; },
    async deliveryStatus() { order.push('readback'); return delivery(); },
    async ackSavedRevision() { order.push('ack'); return { revision: 7, dirty: false }; },
    releaseSavedRequest() {},
    async status() { order.push('status'); return { revision: 7, dirty: false }; },
  };
  await new WorkspaceSaveCoordinator(adapter).request(7);
  assert.deepEqual(order, ['ds', 'go', 'readback', 'ack', 'status']);
});

await test('重复请求复用同一事务，完整成功后的新请求才重新导出', async () => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  const adapter = adapterFixture({ async persistSavedFile() { this.calls.persist += 1; await gate; return { receipt: receipt() }; } });
  const coordinator = new WorkspaceSaveCoordinator(adapter);
  const first = coordinator.request();
  assert.strictEqual(coordinator.request(), first);
  release();
  await Promise.all([first, coordinator.request()]);
  assert.deepEqual(adapter.calls, { persist: 1, register: 1, delivery: 1, ack: 1, release: 1, status: 1 });
  await coordinator.request();
  assert.equal(adapter.calls.persist, 2);
  assert.equal(adapter.calls.register, 2);
});

await test('保存期间继续编辑返回实时 dirty 与新 revision', async () => {
  const adapter = adapterFixture({ async status() { return { documentId: 'doc-1', revision: 8, dirty: true }; } });
  const result = await new WorkspaceSaveCoordinator(adapter).request();
  assert.equal(result.receipt.revision, 7);
  assert.equal(result.live.revision, 8);
  assert.equal(result.live.dirty, true);
});

await test('ack 失败保留文件事实，恢复时不重复登记 Go', async () => {
  const adapter = adapterFixture({
    async ackSavedRevision() {
      this.calls.ack += 1;
      if (this.calls.ack === 1) throw new Error('stale revision');
      return { revision: 7, dirty: false };
    },
  });
  const coordinator = new WorkspaceSaveCoordinator(adapter);
  const first = await coordinator.request();
  assert.equal(first.ackPending, true);
  assert.equal(adapter.calls.release, 0);
  const recovered = await coordinator.request();
  assert.equal(recovered.ackPending, false);
  assert.equal(adapter.calls.persist, 1);
  assert.equal(adapter.calls.register, 1);
  assert.equal(adapter.calls.delivery, 2);
  assert.equal(adapter.calls.ack, 2);
  assert.equal(adapter.calls.release, 1);
});

await test('旧 ack 失败不得阻塞新修订保存', async () => {
  const adapter = adapterFixture({
    async persistSavedFile() { this.calls.persist += 1; return { receipt: receipt({ revision: this.calls.persist === 1 ? 7 : 8, saveToken: `token-${this.calls.persist === 1 ? 7 : 8}` }) }; },
    async ackSavedRevision(value) {
      this.calls.ack += 1;
      if (value.revision === 7) throw new Error('stale revision');
      return { revision: 8, dirty: false };
    },
    async status() { this.calls.status += 1; return { documentId: 'doc-1', revision: 8, dirty: false }; },
  });
  const coordinator = new WorkspaceSaveCoordinator(adapter);
  const first = coordinator.request(7);
  const second = coordinator.request(8);
  assert.strictEqual(coordinator.request(8), second);
  const firstResult = await first;
  const secondResult = await second;
  assert.equal(firstResult.ackPending, true);
  assert.equal(secondResult.ackPending, false);
  assert.equal(secondResult.live.dirty, false);
  assert.equal(adapter.calls.register, 2);
  assert.equal(adapter.calls.persist, 2);
  assert.equal(adapter.calls.ack, 2);
  assert.equal(adapter.calls.release, 2);
  assert.equal(coordinator.history.length, 1);
  assert.equal(coordinator.history[0].receipt.revision, 7);
});

await test('Go 登记失败不得归档旧事务，排队请求原样收到错误', async () => {
  const conflict = Object.assign(new Error('VERSION_CHANGED'), { status: 409 });
  const adapter = adapterFixture({ async registerSavedVersion() { this.calls.register += 1; throw conflict; } });
  const coordinator = new WorkspaceSaveCoordinator(adapter);
  const first = coordinator.request(7);
  const second = coordinator.request(8);
  await assert.rejects(first, error => error === conflict);
  await assert.rejects(second, error => error === conflict);
  assert.equal(coordinator.history.length, 0);
  assert.equal(coordinator.receipt.revision, 7);
  assert.equal(adapter.calls.release, 0);
  assert.equal(adapter.calls.persist, 1);
});

await test('读回失败不得归档旧事务，排队请求原样收到错误', async () => {
  const failure = new Error('readback unavailable');
  const adapter = adapterFixture({ async deliveryStatus() { this.calls.delivery += 1; throw failure; } });
  const coordinator = new WorkspaceSaveCoordinator(adapter);
  const first = coordinator.request(7);
  const second = coordinator.request(8);
  await assert.rejects(first, error => error === failure);
  await assert.rejects(second, error => error === failure);
  assert.equal(coordinator.history.length, 0);
  assert.equal(coordinator.version.id, 'saved-1');
  assert.equal(adapter.calls.release, 0);
  assert.equal(adapter.calls.persist, 1);
});

await test('DS 成功 Go 失败后只重试同一历史字节', async () => {
  const original = receipt();
  const adapter = adapterFixture({
    async registerSavedVersion(value) {
      this.calls.register += 1;
      assert.strictEqual(value, original);
      if (this.calls.register === 1) throw new Error('Go unavailable');
      return { version: version() };
    },
    async persistSavedFile() { this.calls.persist += 1; return { receipt: original }; },
  });
  const coordinator = new WorkspaceSaveCoordinator(adapter);
  await assert.rejects(coordinator.request(), error => error.saveFacts.receipt === original && !error.saveFacts.version);
  await coordinator.request();
  assert.equal(adapter.calls.persist, 1);
  assert.equal(adapter.calls.register, 2);
  assert.equal(adapter.calls.ack, 1);
});

await test('Go 成功读回失败后只补读回', async () => {
  const adapter = adapterFixture({ async deliveryStatus() { this.calls.delivery += 1; if (this.calls.delivery === 1) throw new Error('readback unavailable'); return delivery(); } });
  const coordinator = new WorkspaceSaveCoordinator(adapter);
  await assert.rejects(coordinator.request(), error => error.saveFacts.version.id === 'saved-1');
  await coordinator.request();
  assert.equal(adapter.calls.persist, 1);
  assert.equal(adapter.calls.register, 1);
  assert.equal(adapter.calls.delivery, 2);
  assert.equal(adapter.calls.ack, 1);
});

await test('409 原样阻断，不自动提高期望版本', async () => {
  const conflict = Object.assign(new Error('VERSION_CHANGED'), { status: 409 });
  const adapter = adapterFixture({ async registerSavedVersion() { this.calls.register += 1; throw conflict; } });
  const coordinator = new WorkspaceSaveCoordinator(adapter);
  await assert.rejects(coordinator.request(), error => error === conflict && error.status === 409);
  assert.equal(adapter.calls.persist, 1);
  assert.equal(adapter.calls.register, 1);
  assert.equal(adapter.calls.delivery, 0);
  assert.equal(adapter.calls.ack, 0);
});

for (const result of tests) console.log(`${result.status === 'passed' ? 'PASS' : 'FAIL'} ${result.name}${result.error ? `\n${result.error}` : ''}`);
const failed = tests.filter(result => result.status === 'failed');
console.log(JSON.stringify({ total: tests.length, passed: tests.length - failed.length, failed: failed.length }, null, 2));
if (failed.length) process.exitCode = 1;
