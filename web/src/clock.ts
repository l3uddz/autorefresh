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
