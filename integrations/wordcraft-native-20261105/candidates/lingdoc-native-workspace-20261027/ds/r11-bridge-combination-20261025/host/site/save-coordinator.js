export class WorkspaceSaveCoordinator {
  constructor(adapter, onStage = () => {}) {
    this.adapter = adapter;
    this.onStage = onStage;
    this.receipt = null;
    this.version = null;
    this.delivery = null;
    this.transaction = null;
    this.latestRevision = undefined;
    this.nextTransaction = null;
    this.history = [];
  }

  request(revision) {
    if (this.transaction) {
      if (revision === undefined || revision === this.latestRevision) return this.nextTransaction || this.transaction;
      this.latestRevision = revision;
      if (!this.nextTransaction) {
        this.nextTransaction = this.transaction.then(
          () => {
            this.nextTransaction = null;
            return this.request(this.latestRevision);
          },
          error => {
            this.nextTransaction = null;
            throw error;
          },
        );
      }
      return this.nextTransaction;
    }
    this.latestRevision = revision;
    this.transaction = this.run().finally(() => { this.transaction = null; });
    return this.transaction;
  }

  async run() {
    try {
      // Only a fully persisted revision whose sole remaining failure is a stale native ack
      // may move to bounded history. DS, Go and readback failures stay active for exact retry.
      if (this.receipt?.ackError && this.latestRevision !== undefined && this.receipt.revision !== this.latestRevision) {
        this.history.push({ receipt: this.receipt, version: this.version, delivery: this.delivery });
        if (this.history.length > 20) this.history.shift();
        this.adapter.releaseSavedRequest(this.receipt);
        this.receipt = null;
        this.version = null;
        this.delivery = null;
      }
      if (!this.receipt) {
        this.onStage('ds-saving');
        const saved = await this.adapter.persistSavedFile();
        this.receipt = saved.receipt;
        this.onStage('ds-saved', { receipt: this.receipt });
      }
      if (!this.version || this.version.docSha256 !== this.receipt.sha256) {
        this.onStage('go-registering', { receipt: this.receipt });
        const registered = await this.adapter.registerSavedVersion(this.receipt);
        this.version = registered.version;
        this.onStage('go-registered', { receipt: this.receipt, version: this.version });
      }
      this.onStage('reading-back', { receipt: this.receipt, version: this.version });
      this.delivery = await this.adapter.deliveryStatus(this.version.id);
      if (this.delivery.version.docSha256 !== this.receipt.sha256) throw new Error('Go 读回版本与 DS 回执字节不一致');

      if (!this.receipt.ackAccepted) {
        this.onStage('native-ack', { receipt: this.receipt });
        try {
          await this.adapter.ackSavedRevision(this.receipt);
          this.receipt.ackAccepted = true;
          delete this.receipt.ackError;
        } catch (error) {
          this.receipt.ackError = error.message;
          return {
            receipt: this.receipt,
            version: this.version,
            delivery: this.delivery,
            live: null,
            ackPending: true,
            ackError: error,
          };
        }
      }

      const live = await this.adapter.status();
      const result = { receipt: this.receipt, version: this.version, delivery: this.delivery, live, ackPending: false };
      this.adapter.releaseSavedRequest(this.receipt);
      this.receipt = null;
      this.version = null;
      this.delivery = null;
      return result;
    } catch (error) {
      error.saveFacts = { receipt: this.receipt, version: this.version, delivery: this.delivery };
      throw error;
    }
  }
}
