import { describe, expect, it } from 'vitest';
import { countdown, itemDetail, outcomeView, since } from './now';

describe('now panel', () => {
  it('names each outcome of a finished item with its tone', () => {
    const cases: [string | undefined, string, string][] = [
      ['refresh', 'refreshed', 'green'],
      ['accepted', 'refreshed', 'green'],
      ['match', 'matched', 'purple'],
      ['tmdb-match', 'matched', 'purple'],
      ['title-match', 'matched', 'purple'],
      ['refresh-failed', 'failed', 'red'],
      ['match-failed', 'failed', 'red'],
      ['tmdb-match-failed', 'failed', 'red'],
      ['title-match-failed', 'failed', 'red'],
      ['search-failed', 'failed', 'red'],
      ['failed', 'failed', 'red'],
      ['skipped', 'skipped', 'gray'],
      ['stopped', 'skipped', 'gray'],
      ['changed', 'skipped', 'gray'],
      ['not-cached', 'skipped', 'gray'],
      ['gone', 'gone', 'gray'],
      ['dry-run', 'dry run', 'gray'],
      ['other-thing', 'other thing', 'gray'],
      [undefined, '', 'gray'],
    ];
    for (const [outcome, text, tone] of cases) {
      expect(outcomeView(outcome), String(outcome)).toEqual({ text, tone });
    }
  });

  it('counts down to the next item in whole seconds and never below now', () => {
    const now = Date.parse('2026-11-23T12:00:00Z');
    expect(countdown('2026-11-23T12:00:04.200Z', now)).toBe('in 5s');
    expect(countdown('2026-11-23T12:00:00Z', now)).toBe('now');
    expect(countdown('2026-11-23T11:59:57Z', now)).toBe('now');
    expect(countdown(undefined, now)).toBe('');
  });

  it('says how long ago the last item finished', () => {
    const now = Date.parse('2026-11-23T12:00:00Z');
    expect(since('2026-11-23T11:59:59.600Z', now)).toBe('now');
    expect(since('2026-11-23T11:59:58Z', now)).toBe('2s ago');
    expect(since('2026-11-23T11:58:30Z', now)).toBe('1m ago');
    expect(since('2026-11-23T12:00:03Z', now)).toBe('now');
    expect(since(undefined, now)).toBe('');
  });

  it('joins the library and the reasons of an item', () => {
    expect(itemDetail({ key: '1', library: 'TV Shows', reasons: ['no-summary', 'no-poster'] }, true)).toBe('TV Shows · no summary · no poster');
    expect(itemDetail({ key: '1', library: 'TV Shows', reasons: ['no-summary'] }, false)).toBe('TV Shows');
    expect(itemDetail({ key: '1', reasons: ['no-summary'] }, true)).toBe('no summary');
    expect(itemDetail({ key: '1' }, true)).toBe('');
  });
});
