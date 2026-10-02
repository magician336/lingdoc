type StorageLike = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>
type CryptoLike = Pick<Crypto, 'randomUUID' | 'subtle'>

function attemptStorageKey(projectId: string, chapterId: string): string {
  return `lingdoc:generation-attempt:${projectId}:${chapterId}`
}

export async function generationIdempotencyKey(
  storage: StorageLike,
  projectId: string,
  chapterId: string,
  body: unknown,
  cryptoProvider: CryptoLike = globalThis.crypto,
): Promise<string> {
  const key = attemptStorageKey(projectId, chapterId)
  let nonce = storage.getItem(key)
  if (!nonce) {
    nonce = cryptoProvider.randomUUID()
    storage.setItem(key, nonce)
  }

  const serialized = JSON.stringify(body)
  if (serialized === undefined) throw new TypeError('generation request body must be JSON serializable')
  const digest = await cryptoProvider.subtle.digest('SHA-256', new TextEncoder().encode(serialized))
  const bodyHash = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
  // The key is stable across a page reload for the same in-flight body, while
  // only a random nonce—not chapter text or request data—is persisted.
  return `${nonce}.${bodyHash}`
}

export function clearGenerationAttempt(storage: StorageLike, projectId: string, chapterId: string): void {
  storage.removeItem(attemptStorageKey(projectId, chapterId))
}
