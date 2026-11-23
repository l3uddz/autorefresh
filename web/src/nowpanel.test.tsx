import { renderToString } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { RunProgress } from './api';
import { Countdown, NowPanel } from './nowpanel';

// The second clock ticks once a second. A countdown that a status event
// brings must start from the time of the event, not from the last tick,
// which the shared clock took long before these fake times.

afterEach(() => vi.useRealTimers());

describe('now panel times', () => {
  it('counts down from the time that the countdown appears', () => {
    vi.useFakeTimers({ now: Date.parse('2099-11-23T12:00:04.100Z') });
    expect(renderToString(<Countdown at="2099-11-23T12:00:04.900Z" />)).toContain('in 1s');
  });
});

const baseRun: RunProgress = {
  id: 1, kind: 'run', source: 'schedule', status: 'running', startedAt: '2026-11-23T12:00:00Z', recorded: true,
  librariesRead: 1, selected: 2, due: 2, done: 1, refreshed: 1, matched: 0, failed: 0, skipped: 0,
};
const expanse = { key: '7', title: 'The Expanse', year: 2015, type: 'show', library: 'TV Shows', reasons: ['no-summary'] };
const dune = { key: '8', title: 'Dune', year: 2021, library: 'Movies', outcome: 'refresh', finishedAt: '2026-11-23T12:00:01Z' };

describe('now panel', () => {
  it('shows nothing before the first item', () => {
    expect(renderToString(<NowPanel run={baseRun} />)).toBe('');
  });

  it('marks the item in work, with the reasons of a run', () => {
    const html = renderToString(<NowPanel run={{ ...baseRun, current: expanse }} />);
    expect(html).toContain('Working');
    expect(html).toContain('TV Shows · no summary');
    expect(html).not.toContain('Up next');
  });

  it('leaves the reasons out of a bulk refresh', () => {
    const html = renderToString(<NowPanel run={{ ...baseRun, kind: 'bulk-refresh', current: expanse }} />);
    expect(html).toContain('TV Shows');
    expect(html).not.toContain('no summary');
  });

  it('shows the next item with its countdown', () => {
    vi.useFakeTimers({ now: Date.parse('2099-11-23T12:00:00.000Z') });
    const html = renderToString(<NowPanel run={{ ...baseRun, next: expanse, nextAt: '2099-11-23T12:00:04.500Z' }} />);
    expect(html).toContain('Up next');
    expect(html).toContain('in 5s');
  });

  it('shows only the last item once no item waits', () => {
    const html = renderToString(<NowPanel run={{ ...baseRun, last: dune }} />);
    expect(html).toContain('Last');
    expect(html).toContain('Dune');
    expect(html).toContain('refreshed');
    expect(html).not.toContain('Working');
    expect(html).not.toContain('Up next');
  });
});
