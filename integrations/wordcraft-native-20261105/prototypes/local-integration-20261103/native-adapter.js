// This adapter observes and delegates. Only the existing host owns export/DS/Go/ack.
export const BASELINE_ID = 'LD-V1-20261103-R1';

export function createNativeAdapter({ hostWindow, onState } = {}) {
  if (!hostWindow) throw new Error('A same-origin native host window is required');
  let disposed = false, sequence = 0, pending = null, failure = null, boundHost = null, failureHostSave = null;
  let state = Object.freeze({ baselineId: BASELINE_ID, phase: 'connecting', engine: null });
  const listeners = new Set(onState ? [onState] : []);
  const error = (code, message) => Object.assign(new Error(message), { code });
  function host() {
    if (disposed) throw error('DISPOSED', 'Adapter is disposed; the editor has not been closed.');
    let value;
    try { value = hostWindow.wordcraftHost; }
    catch { throw error('CROSS_ORIGIN_HOST', 'Mount the native host through the approved same-origin integration.'); }
    if (!value?.engine || !value?.saveIntent) throw error('HOST_NOT_READY', 'Native host is not ready.');
    if (boundHost && boundHost !== value) throw error('HOST_CHANGED', 'Native host changed; create a new adapter for the new document host.');
    boundHost = value;
    return value;
  }
  function publish(next) {
    if (disposed) return;
    state = structuredClone({ baselineId: BASELINE_ID, ...next });
    for (const listener of listeners) {
      // A rendering error must not cause a persisted save to be reported as failed.
      try { listener(structuredClone(state)); } catch { /* UI owns its error reporting. */ }
    }
  }
  function facts(saved) {
    if (!saved?.receipt || !saved?.version) return null;
    return {
      sha256: saved.receipt.sha256,
      diskVersion: saved.receipt.diskVersion,
      goDiskVersion: saved.version.diskVersion,
      versionId: saved.version.id,
      nativeDocumentId: saved.receipt.documentId,
      nativeRevision: saved.receipt.revision,
      verified: saved.receipt.verified === true
        && saved.receipt.sha256 === saved.version.docSha256
        && saved.receipt.sha256 === saved.delivery?.version?.docSha256,
      ackPending: Boolean(saved.ackPending),
    };
  }
  async function refresh() {
    const id = ++sequence;
    try {
      const currentHost = host();
      const live = await currentHost.engine({ op: 'status' });
      if (disposed || id !== sequence) return structuredClone(state);
      if (host() !== currentHost) throw error('HOST_CHANGED', 'Native host changed; reconnect before acting.');
      const saved = facts(currentHost.lastSave);
      const savedLive = currentHost.lastSave?.live;
      const boundToCurrentDocument = savedLive?.documentId === live.documentId && savedLive?.revision === live.revision;
      // Native toolbar saves use the same coordinator but bypass adapter.save().
      if (failure && currentHost.lastSave !== failureHostSave && saved?.verified) failure = null;
      const busy = Boolean(pending || hostWindow.lingdocDiagnostics?.().transactionActive);
      const phase = busy ? 'saving' : failure ? (failure.status === 409 ? 'conflict' : 'save_failed')
        : live.dirty ? 'unsaved' : saved?.verified && !saved.ackPending && boundToCurrentDocument ? 'saved' : 'not_registered';
      publish({ phase, engine: { documentId: live.documentId, revision: live.revision, dirty: live.dirty },
        saved, boundToCurrentDocument, dsDiskVersion: currentHost.diskVersion, goDiskVersion: currentHost.goDiskVersion,
        error: failure });
      return structuredClone(state);
    } catch (cause) {
      if (!disposed && id === sequence) publish({ ...state, phase: 'unavailable', error: { code: cause.code || 'HOST_UNAVAILABLE', message: cause.message } });
      throw cause;
    }
  }
  function save() {
    if (pending) return pending;
    const currentHost = host();
    ++sequence; // Any earlier status read must not overwrite this operation.
    failure = null;
    publish({ ...state, phase: 'saving', error: null });
    pending = Promise.resolve().then(() => currentHost.saveIntent('frontend-native-adapter')).then(result => {
      if (disposed || host() !== currentHost) throw error('HOST_CHANGED', 'Save completed for a previous host; current editor was not acknowledged by this adapter.');
      return result;
    }).catch(cause => {
      if (!disposed) {
        failureHostSave = currentHost.lastSave;
        failure = { code: cause.code || 'SAVE_INCOMPLETE', status: cause.status ?? null,
          message: cause.message, persisted: facts(cause.saveFacts) };
      }
      throw cause;
    }).finally(async () => {
      pending = null;
      // Do not mask the actual save result if a subsequent status read fails.
      if (!disposed) await refresh().catch(() => {});
    });
    return pending;
  }
  return Object.freeze({
    baselineId: BASELINE_ID, refresh, save,
    getState: () => structuredClone(state),
    subscribe(listener) { host(); listeners.add(listener); listener(structuredClone(state)); return () => listeners.delete(listener); },
    dispose() { disposed = true; ++sequence; listeners.clear(); },
  });
}
