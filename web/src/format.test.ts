import { describe, expect, it } from 'vitest';
import { duration, fixedBy, lastAction, nextRunText, plural, queueState, relative, runDuration, typeName } from './format';

const now = Date.parse('2026-11-22T12:00:00Z');
const at = (seconds: number) => new Date(now + seconds * 1000).toISOString();

describe('format', () => {
  it('shortens durations', () => {
    expect(duration(45)).toBe('45s');
    expect(duration(12 * 60)).toBe('12m');
    expect(duration(3 * 3600 + 59)).toBe('3h');
    expect(duration(2 * 86400 + 4 * 3600)).toBe('2d 4h');
    expect(duration(86400)).toBe('1d');
    expect(duration(-5)).toBe('0s');
  });

  it('describes times relative to now', () => {
    expect(relative(undefined, now)).toBe('—');
    expect(relative(at(10), now)).toBe('just now');
    expect(relative(at(2 * 86400 + 4 * 3600), now)).toBe('in 2d 4h');
    expect(relative(at(-3 * 3600), now)).toBe('3h ago');
  });

  it('describes queue states', () => {
    expect(queueState('due', undefined, now)).toBe('Due');
    expect(queueState('waiting', at(3600), now)).toBe('Waiting · in 1h');
    expect(queueState('too-new', at(600), now)).toBe('Too new · in 10m');
    expect(queueState(undefined, undefined, now)).toBe('—');
  });

  it('combines the last outcome with a failed match evaluation', () => {
    expect(lastAction('refresh', 'no-match')).toBe('refresh · no match');
    expect(lastAction('match', 'matched')).toBe('match');
    expect(lastAction('match-failed', '')).toBe('match failed');
    expect(lastAction(undefined, undefined)).toBe('none yet');
  });

  it('names what fixed an item', () => {
    expect(fixedBy('refresh', undefined)).toBe('refresh');
    expect(fixedBy('tmdb-match', 'tmdb-match')).toBe('TMDB match');
    expect(fixedBy('refresh', 'match')).toBe('match · then refresh');
    expect(fixedBy(undefined, undefined)).toBe('Plex on its own');
  });

  it('counts with plurals and run durations', () => {
    expect(plural(1, 'item')).toBe('1 item');
    expect(plural(1200, 'item')).toBe(`${(1200).toLocaleString()} items`);
    expect(plural(2, 'library', 'libraries')).toBe('2 libraries');
    expect(runDuration(at(0), at(125))).toBe('2m');
    expect(runDuration(at(0), undefined)).toBe('—');
  });
});

describe('type names', () => {
  it('names Plex types', () => {
    expect(typeName('show')).toBe('Series');
    expect(typeName('movie')).toBe('Movie');
    expect(typeName(undefined)).toBe('—');
  });

  it('describes the next scheduled run', () => {
    expect(nextRunText(undefined, now)).toBeUndefined();
    expect(nextRunText(at(-5), now)).toBe('starting');
    expect(nextRunText(at(0), now)).toBe('starting');
    const time = new Date(at(45 * 60)).toLocaleTimeString(undefined, { timeStyle: 'short' });
    expect(nextRunText(at(45 * 60), now)).toBe(`in 45m · ${time}`);
    expect(nextRunText(at(12), now)).toMatch(/^in 12s · /);
  });
});
