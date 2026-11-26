import type { RunItem } from './api';
import { plural } from './format';
import type { Outcome } from './notify';

export type StepState = 'waiting' | 'running' | 'finished' | 'failed' | 'canceled' | 'interrupted' | 'unknown' | 'empty' | 'not-started';

export interface QueueStep {
  srv: number;
  server: string;
  state: StepState;
  /** The job (run) ID once the server admitted it. */
  job?: number;
  /** The number of items that the job took. */
  count?: number;
  /** The item results of an ended job. Undefined while it runs, and when
   * the results are unavailable. */
  items?: RunItem[];
  /** Why a step did not start. */
  note?: string;
}

export interface BulkQueue {
  steps: QueueStep[];
  isCanceled: boolean;
  isDone: boolean;
  /** Why the cancel of the running job failed. The job may still run;
   * its server card can cancel it. */
  cancelError?: string;
}

function outcomeText(items: RunItem[]): string {
  const count = (outcome: string) => items.filter(item => item.outcome === outcome).length;
  const accepted = count('accepted');
  const failed = count('failed');
  const other = items.length - accepted - failed;
  return [`${accepted.toLocaleString()} accepted`, failed ? `${failed.toLocaleString()} failed` : '', other ? `${other.toLocaleString()} skipped` : '']
    .filter(Boolean)
    .join(' · ');
}

const stateVerbs: Record<StepState, string> = {
  waiting: 'waiting',
  running: 'running',
  finished: 'finished',
  failed: 'failed',
  canceled: 'canceled',
  interrupted: 'interrupted',
  unknown: 'result unknown',
  empty: 'no matching items',
  'not-started': 'not started',
};

function stepText(step: QueueStep): string {
  const verb = stateVerbs[step.state];
  switch (step.state) {
    case 'not-started':
      return step.note ? `${verb}: ${step.note}` : verb;
    case 'waiting':
    case 'running':
    case 'unknown':
    case 'empty':
      return verb;
    default:
      return step.items ? `${verb}: ${outcomeText(step.items)}` : `${verb}, results unavailable`;
  }
}

/** Returns the toast text of an ended queue. */
export function queueText(queue: BulkQueue): string {
  if (queue.steps.length === 1) return `Bulk refresh ${stepText(queue.steps[0])}`;
  return `Bulk refresh of ${plural(queue.steps.length, 'server')}: ${queue.steps.map(step => `${step.server} ${stepText(step)}`).join('; ')}`;
}

/** Reports whether an ended queue needs an error toast: a job failed, was
 * interrupted, or ended unknown, or a server did not start for a reason
 * other than a cancel. A server without matching items is no failure. */
export function queueFailed(queue: BulkQueue): boolean {
  return queue.steps.some(
    step => step.state === 'failed' || step.state === 'interrupted' || step.state === 'unknown' || (step.state === 'not-started' && step.note !== 'canceled'),
  );
}

/** Returns the outcome of an ended queue: an error when it failed
 * (queueFailed). Otherwise, when every server had no matching items or
 * finished with known item results, and at least one finished, an error
 * when Plex refused every refresh and a success when it accepted every
 * refresh. Anything else is a notice, such as a cancel, a refused or
 * skipped item, or results that are unavailable or empty. */
export function queueOutcome(queue: BulkQueue): Outcome {
  if (queueFailed(queue)) return 'error';
  const finished = queue.steps.filter(step => step.state === 'finished');
  const isKnown = queue.steps.every(step => step.state === 'empty' || (step.state === 'finished' && step.items?.length));
  if (!isKnown || finished.length === 0) return 'notice';
  const items = finished.flatMap(step => step.items ?? []);
  if (items.every(item => item.outcome === 'failed')) return 'error';
  return items.every(item => item.outcome === 'accepted') ? 'success' : 'notice';
}

/** Returns the line that describes a running queue, such as "Cabin
 * running · Home waiting". */
export function queueProgress(queue: BulkQueue): string {
  return queue.steps.map(step => `${step.server} ${stepText(step)}`).join(' · ');
}
