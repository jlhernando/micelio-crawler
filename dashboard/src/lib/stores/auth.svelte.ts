// Set when the server answers 401: it was started with --auth-token (or
// MICELIO_AUTH_TOKEN) and this browser has no login cookie yet. App shows
// the login screen instead of pages that cannot load, and the WebSocket
// stops reconnecting. failed is set when a login just used a wrong token:
// the server sends the browser back with ?login=failed (never the token).
export const auth = $state({ required: false, failed: false });

if (typeof location !== 'undefined') {
  const url = new URL(location.href);
  if (url.searchParams.get('login') === 'failed') {
    auth.required = true;
    auth.failed = true;
    url.searchParams.delete('login');
    history.replaceState(history.state, '', url.pathname + url.search + url.hash);
  }
}

export function markUnauthorized(status: number) {
  if (status === 401) auth.required = true;
}

// Logging in is a visit to /?token=<token>: the server sets an HttpOnly
// session cookie and redirects back without the token in the URL (to
// ?login=failed when the token is wrong).
export function loginWithToken(token: string) {
  location.href = `${location.pathname}?token=${encodeURIComponent(token)}${location.hash}`;
}
