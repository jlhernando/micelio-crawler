/**
 * WebSocket client for real-time crawl progress.
 *
 * Every createWsClient() call subscribes to one shared socket. The socket
 * opens with the first subscriber, reconnects while anyone is subscribed,
 * and closes shortly after the last one leaves (the grace period avoids a
 * reconnect when one page unmounts just before the next mounts).
 */

export interface WsMessage {
  type: 'progress' | 'page' | 'error' | 'complete' | 'paused' | 'resumed' | 'stopping' | 'cancelled' | 'analysis_progress' | 'tuned' | 'analysis_complete' | 'log_upload_progress' | 'log_progress' | 'log_complete' | 'log_delete_done' | 'merge_progress';
  crawlId?: string;
  data: Record<string, unknown>;
}

export type WsMessageHandler = (msg: WsMessage) => void;

const RECONNECT_MS = 2000;
const IDLE_CLOSE_MS = 1000;

const handlers = new Set<WsMessageHandler>();
let ws: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let idleTimer: ReturnType<typeof setTimeout> | null = null;

function connect() {
  reconnectTimer = null;
  if (handlers.size === 0 || ws) return;
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const socket = new WebSocket(`${protocol}//${location.host}/ws`);
  ws = socket;

  socket.onmessage = (event) => {
    let msg: WsMessage;
    try {
      msg = JSON.parse(event.data) as WsMessage;
    } catch {
      return; // ignore malformed messages
    }
    for (const h of [...handlers]) {
      try { h(msg); } catch (err) { console.error('ws handler failed:', err); }
    }
  };

  socket.onclose = () => {
    if (ws === socket) ws = null;
    if (handlers.size > 0 && !reconnectTimer) reconnectTimer = setTimeout(connect, RECONNECT_MS);
  };

  socket.onerror = () => {
    socket.close();
  };
}

function shutdown() {
  idleTimer = null;
  if (handlers.size > 0) return;
  if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
  const socket = ws;
  ws = null;
  socket?.close();
}

export function createWsClient(onMessage: WsMessageHandler): { close: () => void } {
  // Wrap so the same function can subscribe twice and unsubscribe independently.
  const handler: WsMessageHandler = (msg) => onMessage(msg);
  handlers.add(handler);
  if (idleTimer) { clearTimeout(idleTimer); idleTimer = null; }
  if (!ws && !reconnectTimer) connect();

  let closed = false;
  return {
    close() {
      if (closed) return;
      closed = true;
      handlers.delete(handler);
      if (handlers.size === 0 && !idleTimer) idleTimer = setTimeout(shutdown, IDLE_CLOSE_MS);
    },
  };
}
