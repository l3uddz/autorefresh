import { describe, expect, it } from 'vitest';
import { duration, fixedBy, fullReadText, lastAction, nextRunText, plexCounts, plural, queueState, relative, runDuration, runKind, runTitle, typeName } from './format';

const now = Date.parse('2026-11-22T12:00:00Z');
const at = (seconds: number) => new Date(now + seconds * 1000).toISOString();

describe('format', () => {
  it('names the season and episode counts that are known', () => {
    expect(plexCounts({ seasons: 1, episodes: 10 })).toEqual(['1 season', '10 episodes']);
    expect(plexCounts({ episodes: 0 })).toEqual(['0 episodes']);
    expect(plexCounts(undefined)).toEqual([]);
  });

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
    // Less than half a second left rounds to no time left.
    expect(nextRunText(at(0.3), now)).toBe('starting');
  });

  it('tells a full run from an incremental run', () => {
    const run = { kind: 'run', fullReads: 0, incrementalReads: 0 };
    expect(runKind({ ...run, kind: 'bulk-refresh' })).toBe('Bulk refresh');
    expect(runKind({ ...run, fullReads: 3 })).toBe('Full run');
    expect(runKind({ ...run, incrementalReads: 3 })).toBe('Incremental run');
    expect(runKind({ ...run, fullReads: 1, incrementalReads: 2 })).toBe('Full and incremental run');
    // Runs from before the read counts, and runs that read no library.
    expect(runKind(run)).toBe('Run');
    expect(runKind({ kind: 'run' })).toBe('Run');
  });

  it('names a run by its source and how it read', () => {
    const run = { kind: 'run', source: 'schedule', fullReads: 2, incrementalReads: 0 };
    expect(runTitle(run)).toBe('Scheduled · Full run');
    expect(runTitle({ ...run, source: 'manual', fullReads: 0, incrementalReads: 1 })).toBe('Manual · Incremental run');
    expect(runTitle({ ...run, kind: 'bulk-refresh', source: 'manual' })).toBe('Bulk refresh');
  });

  it('describes the next full cache refresh', () => {
    expect(fullReadText(undefined, false, now)).toBeUndefined();
    expect(fullReadText(at(-60), false, now)).toBe('with the next run');
    expect(fullReadText(at(0), false, now)).toBe('with the next run');
    expect(fullReadText(at(0.3), false, now)).toBe('with the next run');
    // The server says when a full read is due, also when the page clock is
    // behind it.
    expect(fullReadText(at(15), true, now)).toBe('with the next run');
    const soon = at(4 * 86400 + 4 * 3600);
    const when = new Date(soon).toLocaleString(undefined, { weekday: 'short', hour: 'numeric', minute: '2-digit' });
    expect(fullReadText(soon, false, now)).toBe(`in 4d 4h · ${when}`);
  });
});
