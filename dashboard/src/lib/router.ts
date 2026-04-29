/** Simple hash-based SPA router for Svelte 5 */

export interface Route {
  path: string;
  params: Record<string, string>;
}

function parseHash(): Route {
  const hash = window.location.hash.slice(1) || '/setup';
  const [path, ...rest] = hash.split('?');
  const params: Record<string, string> = {};

  // Parse route params like /results/:id
  const searchStr = rest.join('?');
  if (searchStr) {
    const sp = new URLSearchParams(searchStr);
    for (const [k, v] of sp) params[k] = v;
  }

  return { path: path || '/setup', params };
}

let listeners: Array<(route: Route) => void> = [];

export function onRouteChange(fn: (route: Route) => void): () => void {
  listeners.push(fn);
  return () => {
    listeners = listeners.filter(l => l !== fn);
  };
}

function notify() {
  const route = parseHash();
  for (const fn of listeners) fn(route);
}

// Initialize
if (typeof window !== 'undefined') {
  window.addEventListener('hashchange', notify);
}

export function navigate(path: string) {
  window.location.hash = path;
}

export function getCurrentRoute(): Route {
  return parseHash();
}

/** Match a route pattern like /results/:id against current path */
export function matchRoute(pattern: string, path: string): Record<string, string> | null {
  const patternParts = pattern.split('/');
  const pathParts = path.split('/');
  if (patternParts.length !== pathParts.length) return null;

  const params: Record<string, string> = {};
  for (let i = 0; i < patternParts.length; i++) {
    if (patternParts[i].startsWith(':')) {
      params[patternParts[i].slice(1)] = pathParts[i];
    } else if (patternParts[i] !== pathParts[i]) {
      return null;
    }
  }
  return params;
}
