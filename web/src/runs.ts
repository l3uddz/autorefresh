import { useToast } from '@astryxdesign/core/Toast';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ApiError, post } from './api';

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

/** Cancels the manual run or bulk job id. It stops at the next item. */
export function useCancelRun() {
  const showToast = useToast();
  return useMutation({
    mutationFn: (id: number) => post(`runs/cancel?${new URLSearchParams({ id: String(id) })}`),
    onSuccess: () => showToast({ body: 'Canceling at the next item', uniqueID: 'cancel-run' }),
    onError: error => showToast({ body: runError(error), type: 'error', uniqueID: 'cancel-run' }),
  });
}

function runError(error: Error): string {
  if (error instanceof ApiError && error.status === 409) return 'A run is busy. Try again when it ends.';
  if (error instanceof ApiError && error.status === 404) return 'That run has already ended.';
  return error.message;
}
