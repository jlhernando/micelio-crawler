/**
 * Service Worker: Graph Data Cache
 *
 * Cache-first strategy for /api/crawl/:id/graph responses.
 * First visit fetches from network and caches; revisits serve instantly from cache.
 */

const CACHE_NAME = 'graph-data-v1';
const GRAPH_URL_PATTERN = /\/api\/crawl\/[^/]+\/graph$/;
const MAX_CACHE_ENTRIES = 50;

self.addEventListener('install', (event) => {
  // Activate immediately without waiting for existing clients to close
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  // Clean up old cache versions
  event.waitUntil(
    caches.keys().then((names) =>
      Promise.all(
        names
          .filter((name) => name.startsWith('graph-data-') && name !== CACHE_NAME)
          .map((name) => caches.delete(name))
      )
    ).then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);

  // Only cache graph API GET requests
  if (event.request.method !== 'GET' || !GRAPH_URL_PATTERN.test(url.pathname)) {
    return;
  }

  event.respondWith(
    caches.open(CACHE_NAME).then(async (cache) => {
      const cached = await cache.match(event.request);
      if (cached) return cached;

      const response = await fetch(event.request);
      if (response.ok) {
        cache.put(event.request, response.clone());
        // Evict oldest entries if cache exceeds limit
        const keys = await cache.keys();
        if (keys.length > MAX_CACHE_ENTRIES) {
          await cache.delete(keys[0]);
        }
      }
      return response;
    })
  );
});
