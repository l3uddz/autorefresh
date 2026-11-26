import type { ImperativeAlertDialogReturn } from '@astryxdesign/core/AlertDialog';

type Options = Omit<Parameters<ImperativeAlertDialogReturn['show']>[0], 'onAction' | 'isActionLoading'>;

const activeTokens = new WeakMap<ImperativeAlertDialogReturn, number>();

/** Asks with confirm before run. While run works, the action button shows a
 * spinner; the dialog closes when run settles unless a newer dialog took over.
 * run handles its own errors. */
export function askThen(confirm: ImperativeAlertDialogReturn, options: Options, run: () => Promise<unknown>) {
  const token = (activeTokens.get(confirm) ?? 0) + 1;
  activeTokens.set(confirm, token);
  let started = false;
  confirm.show({
    ...options,
    onAction: () => {
      if (started) return;
      started = true;
      confirm.show({ ...options, isActionLoading: true, onAction: () => undefined });
      void run()
        .catch(() => undefined)
        .finally(() => {
          if (activeTokens.get(confirm) === token) confirm.hide();
        });
    },
  });
}
