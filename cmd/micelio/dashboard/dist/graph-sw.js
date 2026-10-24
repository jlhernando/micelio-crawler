/**
 * Retired service worker.
 *
 * Older dashboards registered this worker to cache /api/crawl/:id/graph
 * responses. That endpoint is gone, so this stub only cleans up: it deletes
 * the old graph-data-* caches and unregisters itself. Browsers re-fetch the
 * worker script on update checks, so existing installs pick this up and remove
 * themselves. The dashboard no longer registers any service worker.
 */
self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) => Promise.all(names.filter((n) => n.startsWith('graph-data-')).map((n) => caches.delete(n))))
      .then(() => self.registration.unregister())
      .catch(() => {})
  );
});
