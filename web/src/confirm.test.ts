import type { ImperativeAlertDialogReturn } from '@astryxdesign/core/AlertDialog';
import { describe, expect, it, vi } from 'vitest';
import { askThen } from './confirm';

function fakeConfirm() {
  let lastAction: (() => void) | undefined;
  const hide = vi.fn();
  const show = vi.fn((opts: { onAction?: () => void }) => {
    lastAction = opts.onAction;
  });
  return {
    confirm: { show, hide } as unknown as ImperativeAlertDialogReturn,
    trigger: () => lastAction?.(),
    show,
    hide,
  };
}

describe('askThen', () => {
  it('ignores synchronous double clicks on the confirm action', async () => {
    const { confirm, trigger, hide } = fakeConfirm();
    let resolve!: () => void;
    const run = vi.fn(() => new Promise<void>(r => { resolve = r; }));

    askThen(confirm, { title: 'Confirm', description: 'Run?', actionLabel: 'OK', cancelLabel: 'Cancel' }, run);
    const firstTrigger = trigger;
    // Capture the original onAction before show replaces it, simulating two clicks in one tick.
    const onAction = (confirm.show as ReturnType<typeof vi.fn>).mock.calls[0][0].onAction as () => void;
    onAction();
    onAction();
    firstTrigger();
    expect(run).toHaveBeenCalledTimes(1);

    resolve();
    await Promise.resolve();
    await Promise.resolve();
    expect(hide).toHaveBeenCalledTimes(1);
  });

  it('does not hide a newer dialog when an older action settles', async () => {
    const { confirm, trigger, hide } = fakeConfirm();
    let resolveFirst!: () => void;
    const first = vi.fn(() => new Promise<void>(r => { resolveFirst = r; }));
    const second = vi.fn(async () => undefined);

    askThen(confirm, { title: 'First', description: '1', actionLabel: 'OK', cancelLabel: 'Cancel' }, first);
    trigger();
    askThen(confirm, { title: 'Second', description: '2', actionLabel: 'OK', cancelLabel: 'Cancel' }, second);

    resolveFirst();
    await Promise.resolve();
    await Promise.resolve();
    expect(hide).not.toHaveBeenCalled();

    trigger();
    await Promise.resolve();
    await Promise.resolve();
    expect(hide).toHaveBeenCalledTimes(1);
  });
});
