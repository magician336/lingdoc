// Native desktop transport for the unchanged drawing workspace. It replaces only the
// document BroadcastChannel; next.ai keeps its original iframe postMessage protocol.
(() => {
  const config = window.LINGDOC_DRAWING;
  if (!config?.native) return;
  const NativeBroadcastChannel = window.BroadcastChannel;
  window.BroadcastChannel = class {
    constructor(name) {
      if (name !== 'lingdoc-drawing') return new NativeBroadcastChannel(name);
      this.name = name;
      this.onmessage = null;
      this.closed = false;
    }
    postMessage(data) {
      if (this.closed || data?.token !== config.token) return;
      const discover = data.type === 'discover';
      const body = discover ? { type: 'discover' } : data;
      fetch('/native-reading/drawing-host', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Workspace-Token': config.token },
        body: JSON.stringify(body),
      }).then(async response => {
        const result = await response.json();
        if (!response.ok) throw Error(result.error || '文档桥接失败。');
        if (!this.closed && this.onmessage) this.onmessage({ data: result });
      }).catch(error => {
        if (this.closed || !this.onmessage) return;
        if (discover) this.onmessage({ data: { type: 'host-error', error: error.message } });
        else this.onmessage({ data: { type: 'result', token: config.token, hostId: data.hostId,
          requestId: data.requestId, error: error.message } });
      });
    }
    close() { this.closed = true; this.onmessage = null; }
  };
})();
