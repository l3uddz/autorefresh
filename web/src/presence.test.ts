import { describe, expect, it } from 'vitest';
import { afterExit, closeAction, nextShown } from './presence';

describe('drawer presence', () => {
  const key = (value: { id: number }) => String(value.id);

  it('keeps the last open value while the drawer closes', () => {
    const one = { id: 1 };
    expect(nextShown(undefined, one, key)).toBe(one);
    expect(nextShown(one, undefined, key)).toBe(one);
  });

  it('takes a new value, but keeps the same one when only its identity changes', () => {
    const one = { id: 1 };
    const two = { id: 2 };
    expect(nextShown(one, two, key)).toBe(two);
    expect(nextShown(one, { id: 1 }, key)).toBe(one);
  });

  it('drops the last value once a closed drawer has slid out', () => {
    const one = { id: 1 };
    expect(afterExit(one, undefined)).toBeUndefined();
    // A drawer that opened again before its exit ended keeps its value.
    expect(afterExit(one, one)).toBe(one);
  });

  it('dismisses a selection whose dialog the browser closed without asking the page', () => {
    // A close request that the drawer did not pass on leaves the route on the
    // item, so the page must close it too, or the hidden content keeps loading.
    expect(closeAction(false, true)).toBe('dismiss');
    // The exit slide of a closed selection ended.
    expect(closeAction(false, false)).toBe('drop');
    // A close event queued before the drawer opened again is stale.
    expect(closeAction(true, true)).toBe('ignore');
    expect(closeAction(true, false)).toBe('ignore');
  });
});
