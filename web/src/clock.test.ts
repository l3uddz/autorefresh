import { describe, expect, it } from 'vitest';
import { wholeSecond } from './clock';
import { countdown } from './now';

describe('second clock', () => {
  it('rounds up so a countdown never shows more seconds than are left', () => {
    const start = Date.parse('2026-11-23T12:00:00Z');
    for (const late of [0, 1, 250, 999]) {
      const now = start + late;
      expect(countdown(new Date(now + 5000).toISOString(), wholeSecond(now))).toBe('in 5s');
    }
  });

  it('keeps one value within a second', () => {
    const start = Date.parse('2026-11-23T12:00:00Z');
    expect(wholeSecond(start + 1)).toBe(wholeSecond(start + 999));
    expect(wholeSecond(start)).toBe(start);
  });
});
