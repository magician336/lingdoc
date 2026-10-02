import assert from 'node:assert/strict'
import test from 'node:test'
import { webcrypto } from 'node:crypto'
import { clearGenerationAttempt, generationIdempotencyKey } from './generationAttempt'

class MemoryStorage {
  private readonly values = new Map<string, string>()

  getItem(key: string): string | null { return this.values.get(key) ?? null }
  setItem(key: string, value: string): void { this.values.set(key, value) }
  removeItem(key: string): void { this.values.delete(key) }
  entries(): Array<[string, string]> { return [...this.values.entries()] }
}

const cryptoProvider = webcrypto as unknown as Crypto

test('generation retries after reload keep the same key without persisting request data', async () => {
  const storage = new MemoryStorage()
  const body = { chapter_id: 'chapter-1', instruction: 'sensitive research instruction' }

  const first = await generationIdempotencyKey(storage, 'project-1', 'chapter-1', body, cryptoProvider)
  // A remounted page calls the same helper with the same persistent storage.
  const afterReload = await generationIdempotencyKey(storage, 'project-1', 'chapter-1', body, cryptoProvider)

  assert.equal(afterReload, first)
  assert.equal(first.length, 101)
  assert.equal(storage.entries().length, 1)
  assert.equal(storage.entries()[0][1].includes(body.instruction), false)
})

test('changed request bodies get a different key; success cleanup starts a new attempt', async () => {
  const storage = new MemoryStorage()
  const firstBody = { instruction: 'draft section one' }
  const first = await generationIdempotencyKey(storage, 'project-1', 'chapter-1', firstBody, cryptoProvider)
  const edited = await generationIdempotencyKey(storage, 'project-1', 'chapter-1', { instruction: 'draft section two' }, cryptoProvider)
  assert.notEqual(edited, first)

  clearGenerationAttempt(storage, 'project-1', 'chapter-1')
  const nextIntentionalRun = await generationIdempotencyKey(storage, 'project-1', 'chapter-1', firstBody, cryptoProvider)
  assert.notEqual(nextIntentionalRun, first)
})
