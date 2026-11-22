import { describe, expect, it } from 'vitest';
import type { ItemDetail, ItemRecord, TimelineEvent } from './api';
import { checkResult, decisionView, disposition, eventView, lastDecision, searchSource, verdictName } from './timeline';

const item = (change: Partial<ItemRecord> = {}): ItemRecord => ({ srv: 0, key: '7', cached: false, attempts: 0, refreshes: 0, ...change });
const event = (id: number, kind: string, change: Partial<TimelineEvent> = {}): TimelineEvent => ({ id, kind, accepted: false, at: `2026-11-${10 + id}T00:00:00Z`, searches: [], ...change });

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

describe('event views', () => {
  it('names manual and scheduled requests with their outcome', () => {
    expect(eventView(event(1, 'request', { source: 'manual', action: 'match', accepted: true, match: { guid: 'plex://a', title: 'A', year: 2019 } }))).toEqual({ title: 'Match accepted to A (2019)', by: 'by hand', tone: 'success', detail: undefined });
    expect(eventView(event(1, 'request', { source: 'auto', action: 'refresh', error: 'Plex unavailable' }))).toEqual({ title: 'Refresh failed', by: 'by autorefresh', tone: 'error', detail: 'Plex unavailable' });
    expect(eventView(event(1, 'request', { source: 'auto', action: 'tmdb-match', accepted: true, links: ['tmdb-5'], match: { guid: 'plex://b' } })).detail).toBe('Found by tmdb-5');
  });

  it('names completed and removed items', () => {
    expect(eventView(event(1, 'completed', { fixedAction: 'refresh', fixedMatch: 'match' }))).toMatchObject({ title: 'Fixed', detail: 'By match · then refresh' });
    expect(eventView(event(1, 'completed', { fixedAction: 'tmdb-match', fixedMatch: 'tmdb-match' })).detail).toBe('By TMDB match');
    expect(eventView(event(1, 'completed'))).toMatchObject({ title: 'Completed by Plex on its own', tone: 'neutral' });
    expect(eventView(event(1, 'gone'))).toMatchObject({ title: 'Removed: Plex no longer had it', tone: 'warning' });
  });

  it('explains each decision outcome', () => {
    expect(decisionView(event(1, 'match-decision', { outcome: 'no-match', conflict: 'different candidates' }))).toMatchObject({ title: 'Fix match: not matched · different candidates', tone: 'error' });
    expect(decisionView(event(1, 'match-decision', { outcome: 'matched', tmdbYear: 2019 })).detail).toBe('TMDB confirmed the year 2019.');
    expect(decisionView(event(1, 'match-decision', { outcome: 'matched' })).detail).toBe('The folder ID searches agreed on one candidate.');
    expect(decisionView(event(1, 'match-decision', { outcome: 'no-ids' })).title).toBe('Fix match: no folder IDs');
    expect(decisionView(event(1, 'match-decision', { outcome: 'failed', error: 'TMDB unavailable' })).detail).toBe('TMDB unavailable');
  });

  it('finds the newest decision, or none', () => {
    const decision = event(2, 'match-decision', { outcome: 'no-match', searches: [
      { query: 'tmdb-1', source: 'path', failed: false, candidates: [{ guid: 'plex://a', score: 0, verdict: 'different candidates' }] },
      { query: 'imdb-tt2', source: 'path', failed: true, candidates: [] },
      { query: 'tmdb-3', source: 'tmdb', fromId: 'imdb-tt2', failed: false, candidates: [{ guid: 'plex://b', score: 0, verdict: 'different candidates' }, { guid: 'plex://c', score: 0 }] },
    ] });
    expect(lastDecision([event(3, 'request'), decision, event(1, 'match-decision')])).toBe(decision);
    expect(lastDecision([event(3, 'request')])).toBeUndefined();
    expect(decision.searches.map(searchSource)).toEqual(['folder ID', 'folder ID', 'TMDB ID of imdb-tt2']);
    expect(verdictName('confirms')).toBe('confirms the pick');
    expect(verdictName('year differs')).toBe('year differs');
    expect(verdictName(undefined)).toBeUndefined();
  });
});
