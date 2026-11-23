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
// rest of the page keeps the slow clock above. It reads the time at each
// render, so a status event that brings a new countdown shows it at once.

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

/** Returns the time t in milliseconds, rounded up to a whole second. A
 * countdown that reads it never shows more seconds than are left, and the
 * value stays the same within a second, as useSyncExternalStore needs. */
export function wholeSecond(t: number): number {
  return Math.ceil(t / 1000) * 1000;
}

/** Returns the current time in milliseconds, rounded up to a whole second
 * (wholeSecond), and updated every second while isActive. */
export function useSecondClock(isActive: boolean): number {
  return useSyncExternalStore(isActive ? subscribeSeconds : subscribeNever, () => wholeSecond(Date.now()));
}
