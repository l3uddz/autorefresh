import { renderToString } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Countdown } from './nowpanel';

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
