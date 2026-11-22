import { describe, expect, it } from 'vitest';
import type { ItemDetail, ItemRecord, TimelineEvent } from './api';
import { checkResult, fixedEvent, goneEvent } from './timeline';

const item = (change: Partial<ItemRecord> = {}): ItemRecord => ({ srv: 0, key: '7', cached: false, attempts: 0, refreshes: 0, ...change });
const event = (id: number, kind: string, change: Partial<TimelineEvent> = {}): TimelineEvent => ({ id, kind, accepted: false, at: `2026-11-${10 + id}T00:00:00Z`, ...change });

describe('timeline facts', () => {
  it('takes the fixed details from the newest completed event', () => {
    // Newest first: a later request, then the newest completion, then an
    // older completion by Plex alone.
    const timeline = [
      event(4, 'request', { action: 'refresh', accepted: true }),
      event(3, 'completed', { fixedAction: 'refresh', fixedMatch: 'match' }),
      event(1, 'completed'),
    ];
    expect(fixedEvent(timeline)).toMatchObject({ id: 3, fixedAction: 'refresh', fixedMatch: 'match' });
    expect(fixedEvent([event(1, 'request')])).toBeUndefined();
  });

  it('names the removal only for an item that is no longer cached', () => {
    const timeline = [event(2, 'gone'), event(1, 'completed')];
    expect(goneEvent({ item: item(), timeline })?.id).toBe(2);
    expect(goneEvent({ item: item({ cached: true }), timeline })).toBeUndefined();
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
