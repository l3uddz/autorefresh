import { useToast } from '@astryxdesign/core/Toast';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ApiError, post } from './api';
import { cancelQueueOfJob } from './queue';

/** Starts a manual run of the server srv, or of every server for null. A
 * busy run answers 409, which the toast explains. */
export function useRunNow() {
  const showToast = useToast();
  const client = useQueryClient();
  return useMutation({
    mutationFn: (srv: number | null) => post<{ ids: number[] }>('runs', { srv }),
    onSuccess: () => {
      showToast({ body: 'Run started', uniqueID: 'run-now' });
      void client.invalidateQueries({ queryKey: ['status'] });
    },
    onError: error => showToast({ body: runError(error), type: 'error', uniqueID: 'run-now' }),
  });
}

/** Cancels the manual run or bulk job id. It stops at the next item. A
 * job of the bulk refresh queue also cancels the rest of the queue, before
 * the request, so the rest stays canceled when the job ended first. */
export function useCancelRun() {
  const showToast = useToast();
  return useMutation({
    mutationFn: async (id: number) => {
      const isQueued = cancelQueueOfJob(id);
      try {
        await post(`runs/cancel?${new URLSearchParams({ id: String(id) })}`);
      } catch (error) {
        if (isQueued && error instanceof ApiError && error.status === 404) return { isQueued, hasEnded: true };
        throw error;
      }
      return { isQueued, hasEnded: false };
    },
    onSuccess: ({ isQueued, hasEnded }) => showToast({ body: cancelText(isQueued, hasEnded), uniqueID: 'cancel-run' }),
    onError: error => showToast({ body: runError(error), type: 'error', uniqueID: 'cancel-run' }),
  });
}

function cancelText(isQueued: boolean, hasEnded: boolean): string {
  if (hasEnded) return 'That job had already ended. The other servers of the bulk refresh do not start.';
  return isQueued ? 'Canceling at the next item. The other servers of the bulk refresh do not start.' : 'Canceling at the next item';
}

function runError(error: Error): string {
  if (error instanceof ApiError && error.status === 409) return 'A run is busy. Try again when it ends.';
  if (error instanceof ApiError && error.status === 404) return 'That run has already ended.';
  return error.message;
}
