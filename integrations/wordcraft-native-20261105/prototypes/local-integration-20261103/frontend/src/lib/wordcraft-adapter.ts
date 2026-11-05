/**
 * LD-V1 原生接入合同。演示环境不创建实现，也不提供 contentEditable 回退。
 * 正式适配必须复用 WorkspaceSaveCoordinator，不能由组件自行宣称保存成功。
 */
export interface WordCraftAdapter {
  mount(host: HTMLElement, options: { projectId: string; documentId: string }): Promise<void>
  status(): Promise<{ documentId: string; revision: number; diskVersion: number }>
  focusSection?(sectionId: string): Promise<boolean>
  export(expected: { documentId: string; revision: number; diskVersion: number }): Promise<Blob>
  ack(receipt: { documentId: string; revision: number; verified: boolean }): Promise<void>
  unmount?(): void
}

export type SaveFailure =
  | { kind: 'native_revision_changed'; detail: string }
  | { kind: 'disk_conflict'; detail: string }
  | { kind: 'registration_pending'; detail: string }
  | { kind: 'ack_pending'; detail: string }
  | { kind: 'result_unknown'; idempotencyKey: string; detail: string }
