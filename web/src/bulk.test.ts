import { describe, expect, it } from 'vitest';
import { bulkRequests, listParams } from './bulk';

describe('bulk requests', () => {
  const filters = { srv: 1, library: '3', reason: 'no-art', type: 'movie', q: '  harbor ', all: true };

  it('builds list parameters with the tab and sort', () => {
    expect(listParams('attention', filters, '-tries').toString()).toBe('srv=1&library=3&reason=no-art&type=movie&q=harbor&all=1&tab=attention&sort=-tries');
    // The Stopped tab has no "all" filter, and a library needs a server.
    expect(listParams('stopped', { library: '3', all: true }, '').toString()).toBe('tab=stopped');
  });

  it('sends a matching selection with the filters and without tab or sort', () => {
    const [request] = bulkRequests('stop', 'attention', filters, { kind: 'matching' }, [0, 1]);
    expect(request).toEqual({ srv: 1, path: 'items/stop?srv=1&library=3&reason=no-art&type=movie&q=harbor&all=1', body: { matching: true } });
  });

  it('sends one matching request for each server without a server filter', () => {
    const requests = bulkRequests('resume', 'stopped', { reason: 'unmatched', all: true }, { kind: 'matching' }, [0, 1]);
    expect(requests.map(r => r.path)).toEqual(['items/resume?srv=0&reason=unmatched', 'items/resume?srv=1&reason=unmatched']);
  });

  it('groups selected rows by server and sends only their keys', () => {
    const items = [{ srv: 0, key: '1' }, { srv: 1, key: '2' }, { srv: 0, key: '3' }];
    expect(bulkRequests('refresh', 'attention', filters, { kind: 'keys', items }, [0, 1])).toEqual([
      { srv: 0, path: 'items/refresh?srv=0', body: { keys: ['1', '3'] } },
      { srv: 1, path: 'items/refresh?srv=1', body: { keys: ['2'] } },
    ]);
  });
});
