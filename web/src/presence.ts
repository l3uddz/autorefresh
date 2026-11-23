import { useCallback, useEffect, useRef, useState } from 'react';

// A drawer slides out after its value closes. It keeps showing the last
// value meanwhile, instead of an empty panel, and drops it once the slide
// ends, so a closed drawer keeps nothing mounted that loads data.

/** Returns the value that a drawer shows: the open value, or the last one
 * while the drawer closes. A value with the key of shown keeps shown, so a
 * value rebuilt on each render causes no update. */
export function nextShown<T>(shown: T | undefined, value: T | undefined, key: (value: T) => string): T | undefined {
  if (value === undefined) return shown;
  return shown !== undefined && key(shown) === key(value) ? shown : value;
}

/** Returns the value that a drawer shows once its exit slide ended: none
 * when it is closed, or the open value when it opened again meanwhile. */
export function afterExit<T>(shown: T | undefined, value: T | undefined): T | undefined {
  return value === undefined ? undefined : shown;
}

/** Returns the value that a drawer shows, as nextShown decides, and a ref
 * for the Drawer. The ref listens for the close event of its dialog, which
 * comes when the exit slide ends, and then drops the value as afterExit
 * decides. The browser can also close the dialog by itself, as a Back
 * gesture does, or the value can close before the dialog opened. Then no
 * slide runs, so a dialog that is closed after a render drops the value
 * too. */
export function useShown<T>(value: T | undefined, key: (value: T) => string): [shown: T | undefined, ref: (dialog: HTMLDialogElement | null) => (() => void) | undefined] {
  const [shown, setShown] = useState(value);
  const next = nextShown(shown, value, key);
  if (next !== shown) setShown(next);
  // The close event reads the value of the latest render.
  const latest = useRef(value);
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  // This effect runs after the effect of the Drawer, which starts the slide.
  useEffect(() => {
    latest.current = value;
    const dialog = dialogRef.current;
    if (dialog && !dialog.open) setShown(current => afterExit(current, value));
  });
  const ref = useCallback((dialog: HTMLDialogElement | null) => {
    dialogRef.current = dialog;
    if (!dialog) return undefined;
    // A close event that comes after the dialog opened again is stale.
    const onClose = () => {
      if (!dialog.open) setShown(current => afterExit(current, latest.current));
    };
    dialog.addEventListener('close', onClose);
    return () => {
      dialog.removeEventListener('close', onClose);
      dialogRef.current = null;
    };
  }, []);
  return [next, ref];
}
