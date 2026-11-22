import { useSyncExternalStore } from 'react';

// Routes live in the hash, so the page works at any sub-path of a reverse
// proxy without configuration:
//
//   #/status
//   #/runs
//   #/items/<tab>
//   #/items/<tab>/<srv>/<key>
//
// With several servers, "?srv=<n>" after any route scopes the page to one
// server. Links keep it, so the scope follows the user across pages.

export type TabName = 'attention' | 'fixed' | 'stopped';

export const tabNames: readonly TabName[] = ['attention', 'fixed', 'stopped'];

export interface ItemKey {
  srv: number;
  key: string;
}

export type Route = ({ page: 'status' } | { page: 'runs' } | { page: 'items'; tab: TabName; item?: ItemKey }) & {
  /** The server that scopes the page, or undefined for every server. */
  srv?: number;
};

const runsPattern = /^#\/runs\/?$/;
const itemsPattern = /^#\/items(?:\/(attention|fixed|stopped))?(?:\/(\d+)\/(\d+))?\/?$/;

function scopeParam(query: string): number | undefined {
  const srv = new URLSearchParams(query).get('srv');
  return srv !== null && /^\d+$/.test(srv) ? Number(srv) : undefined;
}

/** Parses a location hash. An unknown hash is the Status page. */
export function parseRoute(hash: string): Route {
  const [path, query = ''] = hash.split('?', 2);
  const srv = scopeParam(query);
  const scope = srv === undefined ? {} : { srv };
  if (runsPattern.test(path)) return { page: 'runs', ...scope };
  const match = itemsPattern.exec(path);
  if (!match) return { page: 'status', ...scope };
  const tab = (match[1] as TabName | undefined) ?? 'attention';
  if (match[2] === undefined) return { page: 'items', tab, ...scope };
  return { page: 'items', tab, item: { srv: Number(match[2]), key: match[3] }, ...scope };
}

/** Returns the hash of route. */
export function routeHash(route: Route): string {
  const query = route.srv === undefined ? '' : `?${new URLSearchParams({ srv: String(route.srv) })}`;
  if (route.page === 'status' || route.page === 'runs') return `#/${route.page}${query}`;
  const base = `#/items/${route.tab}`;
  return `${route.item ? `${base}/${route.item.srv}/${route.item.key}` : base}${query}`;
}

/** Returns the hash of the detail of item on tab in the scope srv. */
export function itemHash(tab: TabName, item: ItemKey, srv?: number): string {
  return routeHash({ page: 'items', tab, item, srv });
}

/** Returns the server scope of route: undefined (every server) when the
 * install has one server or route names a server that does not exist. An
 * unknown serverCount (the servers are not loaded yet) keeps the scope of
 * route, so links keep a shared scope until the servers validate it. */
export function routeScope(route: Route, serverCount: number | undefined): number | undefined {
  if (serverCount === undefined) return route.srv;
  return serverCount > 1 && route.srv !== undefined && route.srv < serverCount ? route.srv : undefined;
}

/** Returns route in the scope srv. It drops an open item of another server. */
export function withScope(route: Route, srv: number | undefined): Route {
  if (route.page === 'items' && route.item && srv !== undefined && route.item.srv !== srv) {
    return { page: 'items', tab: route.tab, srv };
  }
  return { ...route, srv };
}

function subscribe(listener: () => void) {
  window.addEventListener('hashchange', listener);
  return () => window.removeEventListener('hashchange', listener);
}

/** Returns the current route and follows hash changes. */
export function useRoute(): Route {
  const hash = useSyncExternalStore(subscribe, () => window.location.hash);
  return parseRoute(hash);
}

/** Moves to route and keeps the browser history. */
export function navigate(route: Route) {
  window.location.hash = routeHash(route).slice(1);
}
