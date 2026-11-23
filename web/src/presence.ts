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

/** Reports whether a cancel event of the dialog forces it closed. The
 * browser closes the dialog after a close request that cannot be canceled,
 * such as some Back gestures, also when the drawer does not pass it on, for
 * example while a nested layer is on top. A cancel that bubbles up from
 * inside is no close request. */
export function isForcedClose({ cancelable, fromDialog }: { cancelable: boolean; fromDialog: boolean }): boolean {
  return fromDialog && !cancelable;
}

/** Returns the key of the force-closed value that keeps the drawer closed:
 * forced while the selection still has that key, or none once the selection
 * clears or changes, so the same value can open the drawer again later. */
export function nextForced<T>(forced: string | undefined, value: T | undefined, key: (value: T) => string): string | undefined {
  return forced !== undefined && value !== undefined && key(value) === forced ? forced : undefined;
}

/** Returns the value that a drawer shows, as nextShown decides, and a ref
 * for the Drawer. The ref listens on its dialog:
 * - A cancel event that forces the dialog closed calls onDismiss at once, in
 *   the task of the request, so the page closes the selection. isOpen stays
 *   false for that value meanwhile, also when the location moves on before
 *   React sees the dismissal, so another value reopens the dialog.
 * - The close event comes when the exit slide ends, and drops the value as
 *   afterExit decides. A close event while the dialog is open again is
 *   stale: it was queued before the drawer opened again.
 * The value can also close before the dialog opened, or the browser can
 * close the dialog without a slide. Then a dialog that is closed after a
 * render drops the value too. The Drawer takes isOpen from this hook. */
export function useShown<T>(
  value: T | undefined,
  key: (value: T) => string,
  onDismiss: () => void,
): [shown: T | undefined, ref: (dialog: HTMLDialogElement | null) => (() => void) | undefined, isOpen: boolean] {
  const [shown, setShown] = useState(value);
  const next = nextShown(shown, value, key);
  if (next !== shown) setShown(next);
  const [forced, setForced] = useState<string>();
  const nextForce = nextForced(forced, value, key);
  if (nextForce !== forced) setForced(nextForce);
  // The listeners read the value and the callback of the latest render.
  const latest = useRef(value);
  const dismiss = useRef(onDismiss);
  const keyRef = useRef(key);
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  // This effect runs after the effect of the Drawer, which starts the slide.
  useEffect(() => {
    latest.current = value;
    dismiss.current = onDismiss;
    keyRef.current = key;
    const dialog = dialogRef.current;
    if (dialog && !dialog.open) setShown(current => afterExit(current, value));
  });
  const ref = useCallback((dialog: HTMLDialogElement | null) => {
    dialogRef.current = dialog;
    if (!dialog) return undefined;
    const onCancel = (event: Event) => {
      if (!isForcedClose({ cancelable: event.cancelable, fromDialog: event.target === dialog })) return;
      const closed = latest.current;
      if (closed !== undefined) setForced(keyRef.current(closed));
      dismiss.current();
    };
    const onClose = () => {
      if (!dialog.open) setShown(current => afterExit(current, latest.current));
    };
    dialog.addEventListener('cancel', onCancel);
    dialog.addEventListener('close', onClose);
    return () => {
      dialog.removeEventListener('cancel', onCancel);
      dialog.removeEventListener('close', onClose);
      dialogRef.current = null;
    };
  }, []);
  return [next, ref, value !== undefined && nextForce === undefined];
}
