import { useState } from 'react';

// A drawer slides out after its value closes. It keeps showing the last
// value meanwhile, instead of an empty panel.

/** Returns the value that a drawer shows: the open value, or the last one
 * while the drawer closes. A value with the key of shown keeps shown, so a
 * value rebuilt on each render causes no update. */
export function nextShown<T>(shown: T | undefined, value: T | undefined, key: (value: T) => string): T | undefined {
  if (value === undefined) return shown;
  return shown !== undefined && key(shown) === key(value) ? shown : value;
}

/** Returns the value that a drawer shows, as nextShown decides. */
export function useShown<T>(value: T | undefined, key: (value: T) => string): T | undefined {
  const [shown, setShown] = useState(value);
  const next = nextShown(shown, value, key);
  if (next !== shown) setShown(next);
  return next;
}
