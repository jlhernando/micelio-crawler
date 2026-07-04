/**
 * WebSocket client for real-time crawl progress.
 */

export interface WsMessage {
  type: 'progress' | 'page' | 'error' | 'complete' | 'paused' | 'resumed' | 'stopping' | 'cancelled' | 'analysis_progress' | 'analysis_complete' | 'log_upload_progress' | 'log_progress' | 'log_complete' | 'log_delete_done' | 'merge_progress';
  crawlId?: string;
  data: Record<string, unknown>;
}

export type WsMessageHandler = (msg: WsMessage) => void;

export function createWsClient(onMessage: WsMessageHandler): { close: () => void } {
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const url = `${protocol}//${location.host}/ws`;

  let ws: WebSocket | null = null;
  let closed = false;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  function connect() {
    if (closed) return;
    ws = new WebSocket(url);

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as WsMessage;
        onMessage(msg);
      } catch { /* ignore malformed messages */ }
    };

    ws.onclose = () => {
      if (!closed) {
        reconnectTimer = setTimeout(connect, 2000);
      }
    };

    ws.onerror = () => {
      ws?.close();
    };
  }

  connect();

  return {
    close() {
      closed = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      ws?.close();
    },
  };
}
