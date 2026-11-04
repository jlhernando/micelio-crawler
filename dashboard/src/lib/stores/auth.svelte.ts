// Set when the server answers 401: it was started with --auth-token (or
// MICELIO_AUTH_TOKEN) and this browser has no login cookie yet. App shows
// the login screen instead of pages that cannot load, and the WebSocket
// stops reconnecting.
export const auth = $state({ required: false });

export function markUnauthorized(status: number) {
  if (status === 401) auth.required = true;
}

// Logging in is a visit to /?token=<token>: the server sets an HttpOnly
// session cookie and redirects back without the token in the URL.
export function loginWithToken(token: string) {
  location.href = `${location.pathname}?token=${encodeURIComponent(token)}${location.hash}`;
}
