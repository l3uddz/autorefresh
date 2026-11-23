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

/** Returns what a close event of the dialog asks for. A close event while
 * the dialog is open again is stale: it was queued before the drawer opened
 * again. Otherwise a selection that is still open closed without the page:
 * the browser closed the dialog, but the drawer did not pass the close
 * request on, so the page must dismiss the selection. Without a selection,
 * the exit slide ended and the drawer drops its value. */
export function closeAction(isDialogOpen: boolean, isSelected: boolean): 'ignore' | 'dismiss' | 'drop' {
  if (isDialogOpen) return 'ignore';
  return isSelected ? 'dismiss' : 'drop';
}

/** Returns the value that a drawer shows, as nextShown decides, and a ref
 * for the Drawer. The ref listens for the close event of its dialog and
 * acts as closeAction decides: the event comes when the exit slide ends,
 * or when the browser closes the dialog by itself, as a Back gesture does,
 * and then onDismiss closes the selection. The value can also close before
 * the dialog opened. Then no slide runs, so a dialog that is closed after a
 * render drops the value too. */
export function useShown<T>(
  value: T | undefined,
  key: (value: T) => string,
  onDismiss: () => void,
): [shown: T | undefined, ref: (dialog: HTMLDialogElement | null) => (() => void) | undefined] {
  const [shown, setShown] = useState(value);
  const next = nextShown(shown, value, key);
  if (next !== shown) setShown(next);
  // The close event reads the value and the callback of the latest render.
  const latest = useRef(value);
  const dismiss = useRef(onDismiss);
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  // This effect runs after the effect of the Drawer, which starts the slide.
  useEffect(() => {
    latest.current = value;
    dismiss.current = onDismiss;
    const dialog = dialogRef.current;
    if (dialog && !dialog.open) setShown(current => afterExit(current, value));
  });
  const ref = useCallback((dialog: HTMLDialogElement | null) => {
    dialogRef.current = dialog;
    if (!dialog) return undefined;
    const onClose = () => {
      const action = closeAction(dialog.open, latest.current !== undefined);
      if (action === 'dismiss') dismiss.current();
      if (action === 'drop') setShown(undefined);
    };
    dialog.addEventListener('close', onClose);
    return () => {
      dialog.removeEventListener('close', onClose);
      dialogRef.current = null;
    };
  }, []);
  return [next, ref];
}
