import { useImperativeAlertDialog } from '@astryxdesign/core/AlertDialog';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Icon } from '@astryxdesign/core/Icon';
import { IconButton } from '@astryxdesign/core/IconButton';
import { HStack, StackItem, VStack } from '@astryxdesign/core/Layout';
import { MetadataList, MetadataListItem } from '@astryxdesign/core/MetadataList';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { Heading, Text } from '@astryxdesign/core/Text';
import { spacingVars } from '@astryxdesign/core/theme/tokens.stylex';
import { ArrowTopRightOnSquareIcon, QuestionMarkCircleIcon, XMarkIcon } from '@heroicons/react/24/outline';
import * as stylex from '@stylexjs/stylex';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { ApiError, api, invalidateLists, itemParams, post, type ItemDetail as Detail, type ItemRecord, type RefreshAnswer, type LiveItem, type ServerInfo, type TimelineEvent } from './api';
import { useNow } from './clock';
import { dateTime, fixedBy, lastAction, plexCounts, plural, queueState, relative, titleYear, typeName } from './format';
import { askThen } from './confirm';
import { Backdrop, Poster, Reasons } from './media';
import { actionError } from './errors';
import { FixMatchDialog } from './fixmatch';
import { ItemTimeline } from './history';
import { useNotify } from './notify';
import { disposition, refreshOutcome, refreshText } from './timeline';
import { WrongMatchWarning } from './wrongmatch';
import type { ItemKey } from './route';

const styles = stylex.create({
  // The head overlaps the bottom of the backdrop, as in the mockup.
  head: { marginBlockStart: `calc(-1 * ${spacingVars['--spacing-12']} * 2)`, position: 'relative' },
  close: { position: 'absolute', insetBlockStart: spacingVars['--spacing-3'], insetInlineEnd: spacingVars['--spacing-3'] },
  frame: { position: 'relative' },
});

interface Props {
  target: ItemKey;
  server?: ServerInfo;
  ignored: string[];
  /** Closes the detail. Without it, the detail has no close button. */
  onClose?: () => void;
}

type Operation = 'refresh' | 'stop' | 'resume';

type ActionResult =
  | { operation: 'refresh'; result: RefreshAnswer }
  | { operation: 'stop' | 'resume' };

export function ItemDetail({ target, server, ignored, onClose }: Props) {
  const params = itemParams(target);
  const detail = useQuery({
    queryKey: ['item', params],
    queryFn: ({ signal }) => api<Detail>(`item?${params}`, { signal }),
    retry: (count, error) => !(error instanceof ApiError && error.status === 404) && count < 1,
  });
  const isGone = detail.error instanceof ApiError && detail.error.status === 404;
  // An item that is no longer known shows no old details.
  const data = isGone ? undefined : detail.data;
  return (
    <VStack xstyle={styles.frame}>
      <Backdrop src={data?.item.art} />
      {onClose && <IconButton label="Close details" icon={<Icon icon={XMarkIcon} size="sm" />} variant="secondary" onClick={onClose} xstyle={styles.close} />}
      <VStack gap={6} paddingInline={6} paddingBlockEnd={6} xstyle={styles.head}>
        {detail.isPending && <DetailSkeleton />}
        {isGone && (
          <EmptyState title="This item is no longer known" description="Its history may have expired." icon={<Icon icon={QuestionMarkCircleIcon} size="lg" />} />
        )}
        {detail.error && !isGone && (
          <Banner
            status="error"
            title="The item is unavailable"
            description={detail.error.message}
            endContent={<Button label="Try again" onClick={() => void detail.refetch()} />}
          />
        )}
        {data && <DetailBody detail={data} server={server} ignored={ignored} params={params} />}
      </VStack>
    </VStack>
  );
}

function DetailSkeleton() {
  return (
    <HStack gap={5} vAlign="end">
      <Skeleton width={160} height={240} />
      <VStack gap={2} width="100%">
        <Skeleton height={32} width="60%" />
        <Skeleton height={16} width="40%" />
      </VStack>
    </HStack>
  );
}

interface BodyProps {
  detail: Detail;
  server?: ServerInfo;
  ignored: string[];
  params: string;
}

function DetailBody({ detail, server, ignored, params }: BodyProps) {
  const { item, timeline } = detail;
  const now = useNow();
  const end = disposition(detail);
  const live = useQuery({
    queryKey: ['item', 'live', params],
    queryFn: ({ signal }) => api<LiveItem>(`item/live?${params}`, { signal }),
    retry: false,
    staleTime: 60_000,
  });
  const facts = [
    typeName(item.type),
    item.year,
    ...plexCounts(live.data),
    item.library,
  ].filter(Boolean);
  const path = live.data?.path || item.path;
  const match = lastMatch(timeline);
  return (
    <>
      <HStack gap={5} vAlign="end" wrap="wrap">
        <Poster src={item.thumb} title={item.title} year={item.year} type={item.type} size="lg" />
        <StackItem size="fill">
          <VStack gap={2}>
            <Heading level={2} accessibilityLevel={2} wordBreak="break-word">{item.title ?? `Item ${item.key}`}</Heading>
            <Text color="secondary">{facts.join(' · ')}</Text>
            <Reasons reasons={item.reasons} ignored={ignored} />
          </VStack>
        </StackItem>
      </HStack>
      {live.isPending ? (
        <Skeleton height={44} />
      ) : live.data?.summary ? (
        <Text as="p">{live.data.summary}</Text>
      ) : live.data ? (
        <Text as="p" color="secondary">Plex has no summary for this item.</Text>
      ) : null}
      {live.error && <LiveError error={live.error} />}
      <WrongMatchWarning live={live} />
      <MetadataList label={{ position: 'start', width: 128 }}>
        {path && (
          <MetadataListItem label="Path">
            <Text type="code" wordBreak="break-all">{path}</Text>
          </MetadataListItem>
        )}
        {item.cached && !item.stoppedAt && (
          <MetadataListItem label="Queue">{queueState(item.state, item.nextDue, now)}</MetadataListItem>
        )}
        {item.stoppedAt && <MetadataListItem label="Stopped">{`${relative(item.stoppedAt, now)} · ${dateTime(item.stoppedAt)}`}</MetadataListItem>}
        {end?.kind === 'fixed' && <MetadataListItem label="Fixed">{`${fixedBy(end.event.fixedAction, end.event.fixedMatch)} · ${relative(end.event.at, now)}`}</MetadataListItem>}
        {end?.kind === 'removed' && <MetadataListItem label="Removed">{`Plex no longer had it · ${relative(end.event.at, now)}`}</MetadataListItem>}
        {item.cached && (
          <MetadataListItem label="Attempts">{`${plural(item.attempts, 'attempt')} · ${plural(item.refreshes, 'refresh', 'refreshes')} accepted`}</MetadataListItem>
        )}
        {item.cached && (
          <MetadataListItem label="Last action">
            {item.lastAttempt ? `${lastAction(item.lastOutcome, item.lastMatchOutcome)} · ${relative(item.lastAttempt, now)}` : 'None yet'}
          </MetadataListItem>
        )}
        {match && <MetadataListItem label="Match">{match}</MetadataListItem>}
      </MetadataList>
      <DetailActions item={item} server={server} params={params} timeline={timeline} live={live.data} />
      <ItemTimeline timeline={timeline} />
    </>
  );
}

function LiveError({ error }: { error: Error }) {
  const gone = error instanceof ApiError && error.status === 404;
  return (
    <Banner
      status={gone ? 'warning' : 'info'}
      title={gone ? 'Plex no longer has this item' : 'Live details are unavailable'}
      description={gone ? 'It was deleted or moved. The cached details stay until the next read.' : `${error.message}. The cached details are shown.`}
    />
  );
}

/** Describes the last match that Plex accepted, from the timeline (newest
 * event first). */
function lastMatch(timeline: TimelineEvent[]): string | undefined {
  const event = timeline.find(e => e.kind === 'request' && e.accepted && ['match', 'tmdb-match', 'title-match'].includes(e.action ?? '') && e.match);
  if (!event?.match) return undefined;
  const name = event.match.title ? titleYear(event.match.title, event.match.year) : event.match.guid;
  const by = event.source === 'manual' ? 'matched by hand' : 'matched by autorefresh';
  return `${name} · ${by}, ${dateTime(event.at)}`;
}

interface ActionProps {
  item: ItemRecord;
  server?: ServerInfo;
  params: string;
  timeline: TimelineEvent[];
  /** What Plex tells about the item now, from the live view. */
  live?: LiveItem;
}

function DetailActions({ item, server, params, timeline, live }: ActionProps) {
  const [isMatching, setIsMatching] = useState(false);
  const client = useQueryClient();
  const notify = useNotify();
  const confirm = useImperativeAlertDialog();
  const title = item.title ?? `Item ${item.key}`;
  const action = useMutation({
    mutationFn: async (operation: Operation): Promise<ActionResult> => {
      if (operation === 'refresh') {
        return { operation, result: await post<RefreshAnswer>(`item/refresh?${params}`) };
      }
      await post<{ changed: boolean }>(`item/${operation}?${params}`);
      return { operation };
    },
    onSuccess: outcome => {
      if (outcome.operation === 'refresh') {
        const answer = outcome.result;
        if (answer.item && answer.timeline) client.setQueryData<Detail>(['item', params], { item: answer.item, timeline: answer.timeline });
        notify(refreshOutcome(answer), refreshText(answer, title));
      } else {
        notify('success', successText(outcome.operation, title));
      }
    },
    onError: error => {
      notify('error', actionError(error));
    },
    // A failed request can change the store too: a Plex 404 removes the
    // item and records that Plex no longer has it.
    onSettled: () => invalidateLists(client),
  });
  const busy = action.isPending;
  const running = action.variables;
  const machine = server?.machineIdentifier;
  const plexLink = machine
    ? `https://app.plex.tv/desktop/#!/server/${encodeURIComponent(machine)}/details?${new URLSearchParams({ key: `/library/metadata/${item.key}` })}`
    : undefined;

  const stop = () =>
    askThen(
      confirm,
      {
        title: `Stop trying “${title}”?`,
        description: `Autorefresh stops refreshing “${title}” until you resume it. The item moves to Stopped.`,
        actionLabel: 'Stop trying',
        actionVariant: 'destructive',
      },
      () => action.mutateAsync('stop'),
    );

  return (
    <VStack gap={3}>
      <HStack gap={2} wrap="wrap" vAlign="center">
        <Button
          label="Refresh now"
          variant="primary"
          tooltip="Handle this item now as a scheduled run does: an unmatched item gets the automatic fix match, then Plex refreshes it and autorefresh checks it again"
          isLoading={busy && running === 'refresh'}
          isDisabled={busy && running !== 'refresh'}
          onClick={() => action.mutate('refresh')}
        />
        <Button label="Fix match" isDisabled={busy} onClick={() => setIsMatching(true)} />
        {item.cached &&
          (item.stoppedAt ? (
            <Button label="Resume" isLoading={busy && running === 'resume'} isDisabled={busy && running !== 'resume'} onClick={() => action.mutate('resume')} />
          ) : (
            <Button label="Stop trying" variant="destructive" isDisabled={busy} onClick={stop} />
          ))}
        {plexLink && (
          <Button
            label="Open in Plex"
            variant="ghost"
            href={plexLink}
            target="_blank"
            rel="noopener noreferrer"
            endContent={<Icon icon={ArrowTopRightOnSquareIcon} size="sm" />}
          />
        )}
      </HStack>
      {!item.cached && (
        <Text type="supporting" color="secondary">
          Autorefresh no longer tracks this item. A refresh is recorded in its history, and the next read adds it back if it is still incomplete.
        </Text>
      )}
      {confirm.element}
      <FixMatchDialog isOpen={isMatching} onClose={() => setIsMatching(false)} item={item} params={params} timeline={timeline} live={live} />
    </VStack>
  );
}

function successText(operation: Operation, title: string): string {
  switch (operation) {
    case 'stop':
      return `Stopped trying ${title}`;
    case 'resume':
      return `${title} is back in the queue`;
    default:
      return 'Done';
  }
}
