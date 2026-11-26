import { useState, useSyncExternalStore } from 'react';

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
  if (listeners.size === 0) now = Date.now();
  listeners.add(listener);
  timer ??= setInterval(() => {
    now = Date.now();
    listeners.forEach(notify => notify());
  }, tick);
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      clearInterval(timer);
      timer = undefined;
    }
  };
}

/** Returns the current time in milliseconds, updated every 30 seconds. */
export function useNow(): number {
  return useSyncExternalStore(subscribe, () => now);
}

// A second clock for countdowns, such as the wait before the next item of
// a run. It ticks only while a component listens, so the rest of the page
// keeps the slow clock above. The tick changes the time, so each render
// reads one stored time, as useSyncExternalStore needs.

const secondListeners = new Set<() => void>();
let secondNow = Date.now();
let secondTimer: ReturnType<typeof setInterval> | undefined;

function subscribeSeconds(listener: () => void) {
  secondListeners.add(listener);
  secondTimer ??= setInterval(() => {
    secondNow = Date.now();
    secondListeners.forEach(notify => notify());
  }, 1000);
  return () => {
    secondListeners.delete(listener);
    if (secondListeners.size === 0) {
      clearInterval(secondTimer);
      secondTimer = undefined;
    }
  };
}

const readSeconds = () => secondNow;

/** Returns the current time in milliseconds, updated every second. It is
 * never earlier than the mount of the component, so a component that a
 * status event mounts (give it the time it shows as key) starts from the
 * time of the event, not from the last tick, which can be up to a second old,
 * or older after the clock stood still. */
export function useSecondClock(): number {
  const [mounted] = useState(Date.now);
  return Math.max(mounted, useSyncExternalStore(subscribeSeconds, readSeconds, readSeconds));
}
