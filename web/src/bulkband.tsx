import { useImperativeAlertDialog } from '@astryxdesign/core/AlertDialog';
import { Button } from '@astryxdesign/core/Button';
import { HStack, StackItem, VStack } from '@astryxdesign/core/Layout';
import { Section } from '@astryxdesign/core/Section';
import { Text } from '@astryxdesign/core/Text';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { invalidateLists, type ServerInfo } from './api';
import { bulkError, bulkRequests, stopOrResume, tabActions, type BulkAction, type BulkRequest, type Filters, type Selection } from './bulk';
import { askThen } from './confirm';
import { plural } from './format';
import { useNotify } from './notify';
import { NothingMatched, startBulkRefresh, useIsRefreshBusy } from './queue';
import type { TabName } from './route';

const actionLabels: Record<BulkAction, string> = { refresh: 'Refresh', stop: 'Stop trying', resume: 'Resume' };

/** A bulk action as the user submitted it. It keeps the requests, the
 * server names, and the selection of that moment, so a selection change
 * while the confirmation is open or the request waits changes nothing. */
export interface SubmittedBulk {
  action: BulkAction;
  requests: BulkRequest[];
  names: Record<number, string>;
  selection: Selection;
  count: number;
}

/** Owns the bulk mutation and confirmation dialog at the ItemsPage level so
 * an in-flight bulk stop/resume completes and reports its result even if
 * filters or tabs change and unmount BulkBand. */
export function useBulkAction(onDone: (submitted: Selection) => void) {
  const client = useQueryClient();
  const notify = useNotify();
  const confirm = useImperativeAlertDialog();
  const run = useMutation({
    mutationFn: async ({ action, requests, names }: SubmittedBulk) => {
      if (action !== 'refresh') return stopOrResume(action, requests, names);
      const started = await startBulkRefresh(client, requests, names).catch((error: unknown) => {
        if (error instanceof NothingMatched) return undefined;
        throw error;
      });
      if (!started) return { text: 'No items match the filters.', outcome: 'notice' as const };
      const first = started.steps.find(step => step.state === 'running')!;
      const rest = started.steps.filter(step => step.state === 'waiting');
      const next = rest.length ? ` on ${first.server}. Then ${rest.map(step => step.server).join(', ')}, one server at a time` : started.steps.length > 1 ? ` on ${first.server}` : '';
      const text = `Bulk refresh started for ${plural(first.count ?? 0, 'item')}${next}. Progress shows on the Status page.`;
      return { text, outcome: 'success' as const };
    },
    onSuccess: ({ text, outcome }, submitted) => {
      notify(outcome, text);
      onDone(submitted.selection);
    },
    onError: error => notify('error', bulkError(error)),
    onSettled: () => invalidateLists(client),
  });

  const start = (submitted: SubmittedBulk) => {
    if (run.isPending) return;
    if (submitted.action === 'resume') {
      run.mutate(submitted);
      return;
    }
    const items = plural(submitted.count, 'item');
    const servers = submitted.requests.length;
    const order = servers > 1 ? ` The ${plural(servers, 'server')} take turns: each server starts after the previous one ends, and a cancel stops the rest.` : '';
    askThen(
      confirm,
      submitted.action === 'refresh'
        ? {
            title: `Refresh ${items}?`,
            description: `Autorefresh sends ${plural(submitted.count, 'refresh request')} to Plex in the background, one at a time with the configured refresh delay.${order} Progress shows on the Status page.`,
            actionLabel: `Refresh ${items}`,
            actionVariant: 'primary',
          }
        : {
            title: `Stop trying ${items}?`,
            description: `Autorefresh stops refreshing ${items} until you resume them. They move to Stopped.`,
            actionLabel: 'Stop trying',
            actionVariant: 'destructive',
          },
      () => run.mutateAsync(submitted),
    );
  };

  return {
    isPending: run.isPending,
    pendingAction: run.variables?.action,
    start,
    confirmElement: confirm.element,
  };
}

export interface BulkBandProps {
  tab: TabName;
  scope: Filters;
  selection: Selection;
  total: number;
  inventory: ServerInfo[];
  /** Whether the servers are loaded. A matching selection waits for them:
   * it sends one request for each server, to a server that they validate. */
  isInventoryKnown: boolean;
  isPending: boolean;
  pendingAction?: BulkAction;
  onStart: (submitted: SubmittedBulk) => void;
  onClear: () => void;
}

/** The band that replaces the tabs while rows are selected. */
export function BulkBand({ tab, scope, selection, total, inventory, isInventoryKnown, isPending, pendingAction, onStart, onClear }: BulkBandProps) {
  const isRefreshBusy = useIsRefreshBusy();
  const count = selection.kind === 'matching' ? total : selection.items.length;
  const requestsOf = (action: BulkAction) => bulkRequests(action, tab, scope, selection, inventory.map(entry => entry.srv));
  const submit = (action: BulkAction) => {
    onStart({
      action,
      requests: requestsOf(action),
      names: Object.fromEntries(inventory.map(entry => [entry.srv, entry.name])),
      selection,
      count,
    });
  };

  const isWaiting = selection.kind === 'matching' && !isInventoryKnown;
  const disabledReason = (action: BulkAction) => {
    if (isWaiting) return 'Loading the servers';
    if (action === 'refresh' && isRefreshBusy) return 'A bulk refresh is running. Wait until it ends.';
    return undefined;
  };
  return (
    <VStack paddingInline={3} paddingBlock={2}>
      <Section variant="muted" paddingInline={3} paddingBlock={2}>
        <HStack gap={3} vAlign="center" wrap="wrap">
          <StackItem size="fill">
            <Text>{selection.kind === 'matching' ? `All ${plural(count, 'matching item')} selected` : `${plural(count, 'item')} selected`}</Text>
          </StackItem>
          {tabActions[tab].map(action => (
            <Button
              key={action}
              label={actionLabels[action]}
              size="md"
              variant={action === 'stop' ? 'destructive' : 'secondary'}
              isLoading={isPending && pendingAction === action}
              isDisabled={count === 0 || (isPending && pendingAction !== action) || disabledReason(action) !== undefined}
              tooltip={disabledReason(action)}
              onClick={() => submit(action)}
            />
          ))}
          <Button label="Clear selection" variant="ghost" size="md" isDisabled={isPending} onClick={onClear} />
        </HStack>
      </Section>
    </VStack>
  );
}
