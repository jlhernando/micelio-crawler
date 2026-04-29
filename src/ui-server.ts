import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { execFile } from 'node:child_process';
import { readFileSync, existsSync, statSync } from 'node:fs';
import { join, extname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { WebSocketServer, type WebSocket } from 'ws';
import { UiStore } from './ui-store.js';
import { UiCrawlManager } from './ui-crawl-manager.js';
import { createApiHandler } from './ui-api.js';

const MIME_TYPES: Record<string, string> = {
  '.html': 'text/html',
  '.js': 'application/javascript',
  '.css': 'text/css',
  '.json': 'application/json',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
};

export async function startUiServer(port: number, autoOpen = true): Promise<void> {
  // Resolve the dashboard static files directory
  const thisDir = fileURLToPath(new URL('.', import.meta.url));
  const distDir = join(thisDir, '..', 'dist', 'dashboard');

  if (!existsSync(distDir)) {
    console.error(
      'Error: Dashboard not built. Run "npm run build:ui" first.\n' +
      `Expected at: ${distDir}`
    );
    process.exit(1);
  }

  // Initialise store & crawl manager
  const store = new UiStore();
  const crawlManager = new UiCrawlManager(store);
  const apiHandler = createApiHandler(store, crawlManager);

  // ── HTTP Server ─────────────────────────────────────────

  const server = createServer(async (req: IncomingMessage, res: ServerResponse) => {
    const url = new URL(req.url || '/', `http://${req.headers.host}`);
    const path = url.pathname;

    // COOP/COEP headers — enable SharedArrayBuffer for graph workers
    res.setHeader('Cross-Origin-Opener-Policy', 'same-origin');
    res.setHeader('Cross-Origin-Embedder-Policy', 'require-corp');

    // API routes
    if (path.startsWith('/api/')) {
      await apiHandler(req, res);
      return;
    }

    // Static file serving
    serveStatic(distDir, path, res);
  });

  // ── WebSocket Server ────────────────────────────────────

  const wss = new WebSocketServer({ noServer: true });
  const clients = new Set<WebSocket>();

  server.on('upgrade', (req, socket, head) => {
    const url = new URL(req.url || '/', `http://${req.headers.host}`);
    if (url.pathname === '/ws') {
      wss.handleUpgrade(req, socket, head, (ws) => {
        wss.emit('connection', ws, req);
      });
    } else {
      socket.destroy();
    }
  });

  wss.on('connection', (ws: WebSocket) => {
    clients.add(ws);
    ws.on('close', () => clients.delete(ws));
    ws.on('error', () => clients.delete(ws));
  });

  // Broadcast crawl progress to all connected WebSocket clients
  crawlManager.setProgressCallback((crawlId, event) => {
    const message = JSON.stringify({ ...event, crawlId });
    for (const client of clients) {
      if (client.readyState === 1) { // WebSocket.OPEN
        client.send(message);
      }
    }
  });

  // ── Start listening ─────────────────────────────────────

  server.on('error', (err: NodeJS.ErrnoException) => {
    if (err.code === 'EADDRINUSE') {
      console.error(`Error: Port ${port} is already in use. Try a different port with --port <number>.`);
    } else {
      console.error(`Server error: ${err.message}`);
    }
    process.exit(1);
  });

  server.listen(port, () => {
    const url = `http://localhost:${port}`;
    process.stderr.write(`\n  Micelio UI running at ${url}\n\n`);

    if (autoOpen) {
      const cmd = process.platform === 'darwin' ? 'open'
        : process.platform === 'win32' ? 'cmd'
          : 'xdg-open';
      const args = process.platform === 'win32' ? ['/c', 'start', '', url] : [url];
      execFile(cmd, args, () => { /* ignore errors */ });
    }
  });

  // Graceful shutdown
  const shutdown = () => {
    process.stderr.write('\nShutting down UI server...\n');
    for (const client of clients) client.close();
    wss.close();
    server.close(() => {
      store.close();
      process.exit(0);
    });
  };

  process.on('SIGINT', shutdown);
  process.on('SIGTERM', shutdown);
}

function serveStatic(distDir: string, urlPath: string, res: ServerResponse): void {
  // Resolve and verify path stays within distDir (prevent directory traversal)
  const resolvedDist = resolve(distDir);
  let filePath = resolve(distDir, urlPath === '/' ? 'index.html' : '.' + urlPath);

  if (!filePath.startsWith(resolvedDist)) {
    res.writeHead(403, { 'Content-Type': 'text/plain' });
    res.end('Forbidden');
    return;
  }

  // SPA fallback: if not a file with extension, serve index.html
  if (!existsSync(filePath) || (statSync(filePath).isDirectory())) {
    filePath = join(distDir, 'index.html');
  }

  if (!existsSync(filePath)) {
    res.writeHead(404, { 'Content-Type': 'text/plain' });
    res.end('Not Found');
    return;
  }

  const ext = extname(filePath);
  const contentType = MIME_TYPES[ext] || 'application/octet-stream';
  const content = readFileSync(filePath);

  // Cache static assets aggressively (hashed filenames), not index.html
  const cacheControl = ext === '.html'
    ? 'no-cache'
    : 'public, max-age=31536000, immutable';

  res.writeHead(200, {
    'Content-Type': contentType,
    'Content-Length': content.length,
    'Cache-Control': cacheControl,
  });
  res.end(content);
}
