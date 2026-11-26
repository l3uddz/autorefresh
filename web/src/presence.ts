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

/** What a drawer takes from useShown. */
export interface DrawerPresence<T> {
  /** The value that the drawer shows. */
  shown: T | undefined;
  /** The ref of the Drawer, which listens on its dialog. */
  ref: (dialog: HTMLDialogElement | null) => (() => void) | undefined;
  /** Whether the Drawer is open. */
  isOpen: boolean;
  /** The key of the Drawer. It changes after a forced close, so the Drawer
   * mounts a new dialog, which opens when isOpen is true. */
  generation: number;
}

/** Returns the value that a drawer shows, as nextShown decides, and the
 * rest that its Drawer takes. The ref listens on its dialog:
 * - A cancel event that forces the dialog closed calls onDismiss at once, in
 *   the task of the request, so the page closes the selection. The browser
 *   closes the dialog itself, and Astryx would open it again only when
 *   isOpen changes. So the Drawer mounts a new dialog, and isOpen stays false
 *   for the closed value meanwhile, also when the location moves on before
 *   React sees the dismissal. The new dialog stays closed, or opens for
 *   another value.
 * - The close event comes when the exit slide ends, and drops the value as
 *   afterExit decides. A close event while the dialog is open again is
 *   stale: it was queued before the drawer opened again.
 * The value can also close before the dialog opened, or the browser can
 * close the dialog without a slide. Then a dialog that is closed after a
 * render drops the value too. */
export function useShown<T>(
  value: T | undefined,
  key: (value: T) => string,
  onDismiss: () => void,
): DrawerPresence<T> {
  const [forced, setForced] = useState<string>();
  const [generation, setGeneration] = useState(0);
  const nextForce = nextForced(forced, value, key);
  if (nextForce !== forced) setForced(nextForce);
  // A force-closed value counts as closed, so nothing of it mounts again in
  // the new dialog.
  const open = nextForce === undefined ? value : undefined;
  const [shown, setShown] = useState(open);
  const next = nextShown(shown, open, key);
  if (next !== shown) setShown(next);
  // The listeners read the value and the callback of the latest render.
  const latest = useRef(open);
  const dismiss = useRef(onDismiss);
  const keyRef = useRef(key);
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  // This effect runs after the effect of the Drawer, which starts the slide.
  useEffect(() => {
    latest.current = open;
    dismiss.current = onDismiss;
    keyRef.current = key;
    const dialog = dialogRef.current;
    if (dialog && !dialog.open) setShown(current => afterExit(current, open));
  }, [open, onDismiss, key]);
  const ref = useCallback((dialog: HTMLDialogElement | null) => {
    dialogRef.current = dialog;
    if (!dialog) return undefined;
    const onCancel = (event: Event) => {
      if (!isForcedClose({ cancelable: event.cancelable, fromDialog: event.target === dialog })) return;
      const closed = latest.current;
      setShown(undefined);
      setGeneration(current => current + 1);
      // A drawer that was already closing has no selection to dismiss.
      if (closed === undefined) return;
      setForced(keyRef.current(closed));
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
  return { shown: next, ref, isOpen: open !== undefined, generation };
}
