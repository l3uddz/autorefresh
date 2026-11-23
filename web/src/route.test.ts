import { describe, expect, it } from 'vitest';
import { itemHash, parseRoute, routeHash, routeScope, runHash, withScope } from './route';

describe('routes', () => {
  it('parses each page and falls back to Status', () => {
    expect(parseRoute('')).toEqual({ page: 'status' });
    expect(parseRoute('#/status')).toEqual({ page: 'status' });
    expect(parseRoute('#/nowhere')).toEqual({ page: 'status' });
    expect(parseRoute('#/items')).toEqual({ page: 'items', tab: 'attention' });
    expect(parseRoute('#/items/stopped')).toEqual({ page: 'items', tab: 'stopped' });
    expect(parseRoute('#/items/fixed/1/4242')).toEqual({ page: 'items', tab: 'fixed', item: { srv: 1, key: '4242' } });
  });

  it('rejects malformed item keys and tabs', () => {
    expect(parseRoute('#/items/attention/0/12a')).toEqual({ page: 'status' });
    expect(parseRoute('#/items/archive')).toEqual({ page: 'status' });
    expect(parseRoute('#/items/attention/x/12')).toEqual({ page: 'status' });
  });

  it('reads the server scope of any page and ignores a malformed one', () => {
    expect(parseRoute('#/status?srv=1')).toEqual({ page: 'status', srv: 1 });
    expect(parseRoute('#/items/fixed/0/7?srv=0')).toEqual({ page: 'items', tab: 'fixed', item: { srv: 0, key: '7' }, srv: 0 });
    expect(parseRoute('#/items?srv=x')).toEqual({ page: 'items', tab: 'attention' });
    expect(parseRoute('#/items?srv=-1')).toEqual({ page: 'items', tab: 'attention' });
  });

  it('formats what it parses', () => {
    for (const hash of ['#/status', '#/runs', '#/runs?srv=1', '#/items/attention', '#/items/fixed/0/7', '#/status?srv=2', '#/items/stopped/1/9?srv=1']) {
      expect(routeHash(parseRoute(hash))).toBe(hash);
    }
    expect(itemHash('stopped', { srv: 2, key: '9' })).toBe('#/items/stopped/2/9');
    expect(itemHash('stopped', { srv: 2, key: '9' }, 2)).toBe('#/items/stopped/2/9?srv=2');
  });

  it('parses the Runs page with its scope and ignores an invalid scope', () => {
    expect(parseRoute('#/runs')).toEqual({ page: 'runs' });
    expect(parseRoute('#/runs/')).toEqual({ page: 'runs' });
    expect(parseRoute('#/runs?srv=1')).toEqual({ page: 'runs', srv: 1 });
    expect(parseRoute('#/runs?srv=one')).toEqual({ page: 'runs' });
    expect(parseRoute('#/runs/x')).toEqual({ page: 'status' });
    // A scope that names no configured server shows every server.
    expect(routeScope(parseRoute('#/runs?srv=5'), 2)).toBeUndefined();
    expect(withScope({ page: 'runs', srv: 1 }, 0)).toEqual({ page: 'runs', srv: 0 });
  });

  it('opens a run of the Runs page, with or without a scope', () => {
    expect(parseRoute('#/runs/12')).toEqual({ page: 'runs', run: 12 });
    expect(parseRoute('#/runs/12/')).toEqual({ page: 'runs', run: 12 });
    expect(parseRoute('#/runs/12?srv=1')).toEqual({ page: 'runs', run: 12, srv: 1 });
    for (const hash of ['#/runs/12', '#/runs/12?srv=1']) expect(routeHash(parseRoute(hash))).toBe(hash);
    expect(runHash(12)).toBe('#/runs/12');
    expect(runHash(12, 1)).toBe('#/runs/12?srv=1');
    // A run ID is a positive safe integer, as the API takes it.
    expect(parseRoute('#/runs/0')).toEqual({ page: 'status' });
    expect(parseRoute('#/runs/012')).toEqual({ page: 'status' });
    expect(parseRoute('#/runs/99999999999999999999')).toEqual({ page: 'status' });
    // Every server keeps the run open.
    expect(withScope({ page: 'runs', run: 12, srv: 0 }, undefined)).toEqual({ page: 'runs', run: 12 });
    // A new scope closes the run, which may belong to another server.
    expect(withScope({ page: 'runs', run: 12, srv: 0 }, 1)).toEqual({ page: 'runs', srv: 1 });
    expect(withScope({ page: 'runs', run: 12, srv: 1 }, 1)).toEqual({ page: 'runs', run: 12, srv: 1 });
  });

  it('scopes only installs with several servers to a server that exists', () => {
    expect(routeScope({ page: 'status', srv: 1 }, 2)).toBe(1);
    expect(routeScope({ page: 'status', srv: 2 }, 2)).toBeUndefined();
    expect(routeScope({ page: 'status', srv: 0 }, 1)).toBeUndefined();
    expect(routeScope({ page: 'status' }, 3)).toBeUndefined();
  });

  it('keeps the scope of a deep link until the servers are known', () => {
    expect(routeScope({ page: 'items', tab: 'fixed', srv: 1 }, undefined)).toBe(1);
    expect(routeScope({ page: 'status' }, undefined)).toBeUndefined();
  });

  it('keeps an open item of the new scope and drops one of another server', () => {
    const open = { page: 'items', tab: 'attention', item: { srv: 0, key: '5' } } as const;
    expect(withScope(open, 0)).toEqual({ ...open, srv: 0 });
    expect(withScope(open, undefined)).toEqual({ ...open, srv: undefined });
    expect(withScope(open, 1)).toEqual({ page: 'items', tab: 'attention', srv: 1 });
  });
});
