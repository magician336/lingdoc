export const BASELINE_ID: 'LD-V1-20261103-R1';
export type SavePhase = 'connecting' | 'not_registered' | 'unsaved' | 'saving' | 'saved' | 'save_failed' | 'conflict' | 'unavailable';
export interface SavedFacts {
  sha256: string;
  diskVersion: number;
  goDiskVersion: number;
  versionId: string;
  nativeDocumentId: number;
  nativeRevision: number;
  verified: boolean;
  ackPending: boolean;
}
export interface NativeSaveState {
  baselineId: typeof BASELINE_ID;
  phase: SavePhase;
  engine: { documentId: number; revision: number; dirty: boolean } | null;
  saved?: SavedFacts | null;
  boundToCurrentDocument?: boolean;
  dsDiskVersion?: number;
  goDiskVersion?: number | null;
  error?: { code: string; message: string; status?: number | null; persisted?: SavedFacts | null } | null;
}
export interface NativeAdapter {
  baselineId: typeof BASELINE_ID;
  refresh(): Promise<NativeSaveState>;
  /** Delegates to the existing host. A successful older save may leave phase=unsaved. */
  save(): Promise<unknown>;
  getState(): NativeSaveState;
  subscribe(listener: (state: NativeSaveState) => void): () => void;
  /** Releases observers only; never reloads the native editor. */
  dispose(): void;
}
export function createNativeAdapter(options: {hostWindow: Window; onState?: (state: NativeSaveState) => void}): NativeAdapter;
