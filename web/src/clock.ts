import { useSyncExternalStore } from 'react';

// One shared clock for relative times ("in 2d", "3m ago"), so every label
// on the page moves together and no component runs its own timer.
//
// The time changes only on the tick. A page that mounts never gets a new
// time at once: the windowed item table would move its rows while it still
// measures them (Astryx re-anchors its scroll on new data).

const tick = 30_000;
const listeners = new Set<() => void>();
let now = Date.now();
let timer: ReturnType<typeof setInterval> | undefined;

function subscribe(listener: () => void) {
  listeners.add(listener);
  timer ??= setInterval(() => {
    now = Date.now();
    listeners.forEach(notify => notify());
  }, tick);
  return () => listeners.delete(listener);
}

/** Returns the current time in milliseconds, updated every 30 seconds. */
export function useNow(): number {
  return useSyncExternalStore(subscribe, () => now);
}

// A second clock for countdowns, such as the wait before the next item of
// a run. It ticks only while a component that is active listens, so the
// rest of the page keeps the slow clock above. Each render reads the time,
// so a status event that brings a new countdown starts it from the time of
// the event, not from the last tick.

/** freshAge is how long, in milliseconds, a read time stays the time of the
 * clock, so that all reads of one render get the same time, as
 * useSyncExternalStore needs. */
const freshAge = 50;

/** Returns a reader of the clock time. The reader gives the time of clock,
 * or the time of its last read if that is less than freshAge old. */
export function freshReader(clock: () => number): () => number {
  let last = -Infinity;
  return () => {
    const time = clock();
    if (time - last >= freshAge) last = time;
    return last;
  };
}

const readSeconds = freshReader(Date.now);
const secondListeners = new Set<() => void>();
let secondTimer: ReturnType<typeof setInterval> | undefined;

function subscribeSeconds(listener: () => void) {
  secondListeners.add(listener);
  secondTimer ??= setInterval(() => secondListeners.forEach(notify => notify()), 1000);
  return () => {
    secondListeners.delete(listener);
    if (secondListeners.size === 0) {
      clearInterval(secondTimer);
      secondTimer = undefined;
    }
  };
}

const subscribeNever = () => () => {};

/** Returns the current time in milliseconds, updated every second while
 * isActive. */
export function useSecondClock(isActive: boolean): number {
  return useSyncExternalStore(isActive ? subscribeSeconds : subscribeNever, readSeconds);
}
