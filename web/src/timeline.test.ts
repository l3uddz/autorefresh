import { describe, expect, it } from 'vitest';
import type { ItemDetail, ItemRecord, TimelineEvent } from './api';
import { checkResult, disposition } from './timeline';

const item = (change: Partial<ItemRecord> = {}): ItemRecord => ({ srv: 0, key: '7', cached: false, attempts: 0, refreshes: 0, ...change });
const event = (id: number, kind: string, change: Partial<TimelineEvent> = {}): TimelineEvent => ({ id, kind, accepted: false, at: `2026-11-${10 + id}T00:00:00Z`, ...change });

describe('disposition', () => {
  const detail = (timeline: TimelineEvent[], change: Partial<ItemRecord> = {}): ItemDetail => ({ item: item(change), timeline });

  it('takes the fixed details from the newest completed event', () => {
    // Newest first: a later request, then the newest completion, then an
    // older completion by Plex alone.
    const timeline = [
      event(4, 'request', { action: 'refresh', accepted: true }),
      event(3, 'completed', { fixedAction: 'refresh', fixedMatch: 'match' }),
      event(1, 'completed'),
    ];
    expect(disposition(detail(timeline))).toMatchObject({ kind: 'fixed', event: { id: 3, fixedAction: 'refresh', fixedMatch: 'match' } });
    expect(disposition(detail([event(1, 'request')]))).toBeUndefined();
  });

  it('shows a removal only while it is the newest completed or gone event', () => {
    // Plex removed the item, a scan added it back, and a check completed it.
    expect(disposition(detail([event(3, 'completed'), event(2, 'request'), event(1, 'gone')]))).toMatchObject({ kind: 'fixed', event: { id: 3 } });
    // A later request does not hide the removal.
    expect(disposition(detail([event(3, 'request'), event(2, 'gone'), event(1, 'completed')]))).toMatchObject({ kind: 'removed', event: { id: 2 } });
  });

  it('names no removal of a cached item, but keeps its fix', () => {
    expect(disposition(detail([event(2, 'gone'), event(1, 'completed')], { cached: true }))).toBeUndefined();
    // The Fixed tab lists an item by its newest completion, also when a
    // later read added it back.
    expect(disposition(detail([event(2, 'completed')], { cached: true }))).toMatchObject({ kind: 'fixed' });
  });
});

describe('check result', () => {
  const detail = (change: Partial<ItemRecord>, timeline: TimelineEvent[]): ItemDetail => ({ item: item(change), timeline });

  it('reports an item that Plex no longer has as gone, not complete', () => {
    expect(checkResult(detail({}, [event(2, 'gone'), event(1, 'request')]))).toEqual({ kind: 'gone' });
  });

  it('reports a completed item as complete and an incomplete one with its reasons', () => {
    expect(checkResult(detail({}, [event(2, 'completed')]))).toEqual({ kind: 'complete' });
    expect(checkResult(detail({ cached: true, reasons: ['no-art'] }, [event(1, 'request')]))).toEqual({ kind: 'missing', reasons: ['no-art'] });
  });
});
