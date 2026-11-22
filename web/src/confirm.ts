import type { ImperativeAlertDialogReturn } from '@astryxdesign/core/AlertDialog';

type Options = Omit<Parameters<ImperativeAlertDialogReturn['show']>[0], 'onAction' | 'isActionLoading'>;

/** Asks with confirm before run. While run works, the action button shows a
 * spinner; the dialog closes when run settles. run handles its own errors. */
export function askThen(confirm: ImperativeAlertDialogReturn, options: Options, run: () => Promise<unknown>) {
  confirm.show({
    ...options,
    onAction: () => {
      confirm.show({ ...options, isActionLoading: true, onAction: () => undefined });
      void run().catch(() => undefined).finally(() => confirm.hide());
    },
  });
}
