// Set when the server answers 401: it was started with --auth-token (or
// MICELIO_AUTH_TOKEN) and this browser has no login cookie yet. App shows
// the login screen instead of pages that cannot load, and the WebSocket
// stops reconnecting. failed is set when a login just used a wrong token:
// the server sends the browser back with ?login=failed (never the token).
export const auth = $state({ required: false, failed: false, message: '' });

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

// Logging in posts the token to /login: the server sets an HttpOnly session
// cookie (204), answers 401 for a wrong token and 429 after too many wrong
// ones. The token travels in the request body, never in a URL, so it does
// not end up in the browser history or a proxy's access log.
export async function loginWithToken(token: string) {
  let res: Response;
  try {
    res = await fetch('/login', {
      method: 'POST',
      body: new URLSearchParams({ token }),
      credentials: 'same-origin',
      cache: 'no-store',
    });
  } catch {
    auth.failed = true;
    auth.message = 'Cannot reach the dashboard.';
    return;
  }
  if (res.ok) {
    location.reload();
    return;
  }
  auth.failed = true;
  auth.message = 'Wrong token, try again.';
  if (res.status === 429) {
    const wait = res.headers.get('Retry-After');
    auth.message = `Too many wrong tokens. Try again in ${wait || 'a few'} seconds.`;
  }
}
