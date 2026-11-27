import type { ToastOptions } from '@astryxdesign/core/Toast';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useNotify, type Outcome } from './notify';

const showToastSpy = vi.fn<(options: ToastOptions) => () => void>();

vi.mock('@astryxdesign/core/Toast', () => ({
  useToast: () => showToastSpy,
}));

function getNotify() {
  let notify!: ReturnType<typeof useNotify>;
  function Harness() {
    notify = useNotify();
    return null;
  }
  renderToStaticMarkup(<Harness />);
  return notify;
}

describe('useNotify', () => {
  beforeEach(() => {
    showToastSpy.mockReset();
  });

  it.each<[Outcome, 'info' | 'error']>([
    ['success', 'info'],
    ['notice', 'info'],
    ['error', 'error'],
  ])('defaults isAutoHide to true and keeps stock duration for %s toasts', (outcome, expectedType) => {
    const notify = getNotify();
    notify(outcome, 'Action finished');

    expect(showToastSpy).toHaveBeenCalledTimes(1);
    const call = showToastSpy.mock.calls[0][0];
    expect(call.type).toBe(expectedType);
    expect(call.isAutoHide).toBe(true);
    expect(call.autoHideDuration).toBeUndefined();
    expect(renderToStaticMarkup(call.body as React.ReactElement)).toContain('Action finished');
  });

  it('preserves caller overrides such as custom duration or sticky error toasts', () => {
    const notify = getNotify();
    notify('error', 'Bulk refresh failed', { autoHideDuration: 10_000 });
    notify('error', 'Manual dismiss required', { isAutoHide: false });

    expect(showToastSpy).toHaveBeenCalledTimes(2);
    expect(showToastSpy.mock.calls[0][0]).toMatchObject({
      type: 'error',
      isAutoHide: true,
      autoHideDuration: 10_000,
    });
    expect(showToastSpy.mock.calls[1][0]).toMatchObject({
      type: 'error',
      isAutoHide: false,
    });
  });
});
