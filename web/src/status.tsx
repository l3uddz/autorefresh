import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { Card } from '@astryxdesign/core/Card';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Grid } from '@astryxdesign/core/Grid';
import { Icon } from '@astryxdesign/core/Icon';
import { HStack, StackItem, VStack } from '@astryxdesign/core/Layout';
import { Link } from '@astryxdesign/core/Link';
import { List, ListItem } from '@astryxdesign/core/List';
import { ProgressBar } from '@astryxdesign/core/ProgressBar';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { StatusDot } from '@astryxdesign/core/StatusDot';
import { Heading, Text } from '@astryxdesign/core/Text';
import { ClockIcon } from '@heroicons/react/24/outline';
import { useQuery } from '@tanstack/react-query';
import { api, useRuns, useServers, useStatus, type Health, type ItemPage, type RunProgress, type RunRecord, type ServerInfo, type ServerStatus, type Status } from './api';
import { useNow } from './clock';
import { dateTime, fixedBy, fullReadText, label, nextRunText, plural, relative, runDuration, runTitle } from './format';
import { Poster } from './media';
import { NowPanel } from './nowpanel';
import { cancelBulkQueue, isQueueActive, queueProgress, useBulkQueue } from './queue';
import { itemHash, routeHash, runHash } from './route';
import { useCancelRun, useRunNow } from './runs';

/** The Status page of every server, or of the server srv. */
export function StatusPage({ srv }: { srv?: number }) {
  const status = useStatus();
  const servers = useServers();
  const multi = (status.data?.servers.length ?? 0) > 1;
  const busy = Boolean(status.data?.servers.some(server => server.run));
  const shown = status.data?.servers.filter(server => srv === undefined || server.srv === srv);
  return (
    <VStack gap={8} padding={6}>
      <HStack hAlign="between" vAlign="center" wrap="wrap" gap={3}>
        <Heading level={1}>Status</Heading>
        <HStack gap={5} vAlign="center" wrap="wrap">
          <NextRun />
          {status.data && status.data.tmdb !== 'off' && (
            <HStack gap={2} vAlign="center">
              <StatusDot variant={healthDot(status.data.tmdb)} label={`TMDB ${status.data.tmdb}`} />
              <Text color="secondary">TMDB · {healthText(status.data.tmdb, 'TMDB')}</Text>
            </HStack>
          )}
        </HStack>
      </HStack>
      {status.error && <Banner status="error" title="The status is unavailable" description={status.error.message} />}
      <QueueBanner />
      {status.isPending ? (
        <Skeleton height={160} />
      ) : (
        <Grid columns={{ minWidth: 340, repeat: 'fit' }} gap={4}>
          {shown?.map(server => (
            <ServerCard key={server.srv} server={server} info={servers.data?.servers.find(entry => entry.srv === server.srv)} isMulti={multi} isBusy={busy} />
          ))}
        </Grid>
      )}
      <Grid columns={{ minWidth: 380, max: 2, repeat: 'fit' }} gap={6}>
        <RecentFixes isMulti={multi} srv={srv} />
        <RecentRuns isMulti={multi} srv={srv} />
      </Grid>
    </VStack>
  );
}

/** Shows a bulk refresh that works through several servers, one after
 * another, and cancels the rest of it. */
function QueueBanner() {
  const queue = useBulkQueue();
  if (!queue || !isQueueActive(queue) || queue.steps.length < 2) return null;
  return (
    <Banner
      status="info"
      title="Bulk refresh, one server at a time"
      description={queue.cancelError ? `${queueProgress(queue)}. The cancel failed: ${queue.cancelError}. Cancel the running job on its server card.` : queueProgress(queue)}
      endContent={queue.isCanceled ? undefined : <Button label="Cancel the rest" variant="secondary" onClick={() => void cancelBulkQueue()} />}
    />
  );
}

function healthDot(health: Health): 'success' | 'error' | 'neutral' {
  if (health === 'ok') return 'success';
  return health === 'unknown' ? 'neutral' : 'error';
}

function healthText(health: Health, service: string): string {
  switch (health) {
    case 'ok':
      return 'Connected';
    case 'unauthorized':
      return `${service} rejected the token`;
    case 'unreachable':
      return 'Unreachable';
    default:
      return 'Checking';
  }
}

/** Reads the tab counts of one server. */
function useServerCounts(srv: number) {
  return useQuery({
    queryKey: ['counts', srv],
    queryFn: ({ signal }) => api<ItemPage>(`items?${new URLSearchParams({ srv: String(srv), limit: '1' })}`, { signal }),
    select: page => page.counts,
  });
}

function ServerCard({ server, info, isMulti, isBusy }: { server: ServerStatus; info?: ServerInfo; isMulti: boolean; isBusy: boolean }) {
  const counts = useServerCounts(server.srv);
  const runNow = useRunNow();
  const cancel = useCancelRun();
  const { run, last } = server;
  return (
    <Card padding={5}>
      <VStack gap={4}>
        <HStack gap={3} vAlign="center">
          <StatusDot variant={healthDot(server.health)} isPulsing={Boolean(run)} label={healthText(server.health, 'Plex')} tooltip={healthText(server.health, 'Plex')} />
          <StackItem size="fill">
            <Heading level={2} accessibilityLevel={2}>{server.name}</Heading>
          </StackItem>
          <Text color="secondary" hasTabularNumbers>{run ? runHeadline(run) : 'Idle'}</Text>
        </HStack>
        {server.health === 'unauthorized' && <Banner status="error" title="Plex rejected the token" description="Check the token of this server in the configuration." />}
        {server.health === 'unreachable' && <Banner status="warning" title="Plex is unreachable" description="Autorefresh tries again on the next run." />}
        {run ? <RunLive run={run} /> : <LastRun last={last} />}
        <NextFullRead value={info?.nextFullRead} isDue={info?.fullReadDue ?? false} />
        <HStack gap={3} vAlign="center" wrap="wrap">
          <StackItem size="fill">
            <ServerCounts counts={counts.data} error={counts.error} isScanned={info?.scanned ?? true} srv={isMulti ? server.srv : undefined} />
          </StackItem>
          {run?.source === 'manual' && (
            <Button label="Cancel" variant="secondary" isLoading={cancel.isPending} onClick={() => cancel.mutate(run.id)} />
          )}
          {isMulti && !run && (
            <Button
              label="Run now"
              variant="secondary"
              isLoading={runNow.isPending}
              isDisabled={isBusy}
              tooltip={isBusy ? 'A run is busy. One run works at a time.' : undefined}
              onClick={() => runNow.mutate({ srv: server.srv, full: false })}
            />
          )}
        </HStack>
      </VStack>
    </Card>
  );
}

function runHeadline(run: RunProgress): string {
  const kind = run.kind === 'bulk-refresh' ? 'Bulk refresh' : 'Running';
  return run.selected ? `${kind} · ${run.done} / ${run.selected}` : `${kind} · reading libraries`;
}

function RunLive({ run }: { run: RunProgress }) {
  const reading = run.selected === 0;
  const readText = run.library ? `Read ${run.library}` : 'Reading libraries';
  return (
    <VStack gap={2}>
      <ProgressBar
        label="Run progress"
        isLabelHidden
        isIndeterminate={reading}
        value={run.done}
        max={Math.max(run.selected, 1)}
      />
      {reading ? <Text maxLines={1}>{readText}</Text> : <NowPanel run={run} />}
      <Text type="supporting" color="secondary" hasTabularNumbers>
        {[
          run.kind === 'run' && run.due ? `${run.selected} of ${run.due} due this run` : undefined,
          run.kind === 'run' ? `${plural(run.librariesRead, 'library', 'libraries')} read` : undefined,
          `${run.refreshed} refreshed`,
          `${run.matched} matched`,
          run.failed ? `${run.failed} failed` : undefined,
          run.skipped ? `${run.skipped} skipped` : undefined,
        ].filter(Boolean).join(' · ')}
      </Text>
    </VStack>
  );
}

/** When the next full cache refresh of the server is due. */
function NextFullRead({ value, isDue }: { value?: string; isDue: boolean }) {
  const now = useNow();
  const text = fullReadText(value, isDue, now);
  if (!text) return null;
  return (
    <Text type="supporting" color="secondary">
      Full cache refresh {text}
    </Text>
  );
}

function LastRun({ last }: { last?: RunProgress }) {
  const now = useNow();
  if (!last) return <Text color="secondary">No run since autorefresh started. The first run starts after the start checks.</Text>;
  const kind = last.kind === 'bulk-refresh' ? 'Last bulk refresh' : 'Last run';
  return (
    <VStack gap={1}>
      <Text>
        {kind} {label(last.status)} · {relative(last.finishedAt, now)}
      </Text>
      <Text type="supporting" color="secondary" hasTabularNumbers>
        {[`${last.refreshed} refreshed`, `${last.matched} matched`, `${last.failed} failed`, last.kind === 'run' && last.due ? `${last.selected} of ${last.due} due` : undefined]
          .filter(Boolean)
          .join(' · ')}
      </Text>
      {last.error && <Text type="supporting" color="secondary">{last.error}</Text>}
    </VStack>
  );
}

/** The counts of a server. Its link opens the items of that server: srv
 * is the scope, undefined on an install with one server. error is the
 * error of the count query, shown while no counts are loaded. */
function ServerCounts({ counts, error, isScanned, srv }: { counts?: ItemPage['counts']; error: Error | null; isScanned: boolean; srv?: number }) {
  if (!isScanned) return <Text type="supporting" color="secondary">Waiting for the first scan of this server</Text>;
  if (!counts && error) return <Text type="supporting" color="secondary">The counts are unavailable: {error.message}</Text>;
  if (!counts) return <Skeleton width={220} height={16} />;
  return (
    <HStack gap={3} wrap="wrap">
      <Link href={routeHash({ page: 'items', tab: 'attention', srv })}>
        {plural(counts.attention, 'item')} {counts.attention === 1 ? 'needs' : 'need'} attention
      </Link>
      <Text type="supporting" color="secondary">{counts.fixed.toLocaleString()} fixed recently · {counts.stopped.toLocaleString()} stopped</Text>
    </HStack>
  );
}

function RecentFixes({ isMulti, srv }: { isMulti: boolean; srv?: number }) {
  const now = useNow();
  const params = new URLSearchParams({ tab: 'fixed', limit: '6' });
  if (srv !== undefined) params.set('srv', String(srv));
  const fixes = useQuery({
    queryKey: ['items', 'recent-fixes', srv],
    queryFn: ({ signal }) => api<ItemPage>(`items?${params}`, { signal }),
  });
  return (
    <VStack gap={3}>
      <HStack hAlign="between" vAlign="center">
        <Heading level={3} accessibilityLevel={2}>Recently fixed</Heading>
        <Link href={routeHash({ page: 'items', tab: 'fixed', srv })}>All fixed items</Link>
      </HStack>
      {fixes.error && <Text color="secondary">{fixes.error.message}</Text>}
      {fixes.isPending && <Skeleton height={200} />}
      {fixes.data?.items.length === 0 && (
        <EmptyState isCompact title="Nothing fixed yet" description="Items that autorefresh fixes show up here." />
      )}
      {fixes.data && fixes.data.items.length > 0 && (
        <List density="balanced" hasDividers>
          {fixes.data.items.map(item => (
            <ListItem
              key={`${item.srv}/${item.key}`}
              href={itemHash('fixed', item, srv)}
              startContent={<Poster src={item.thumb} title={item.title} type={item.type} size="sm" />}
              label={item.title ?? item.key}
              description={[item.year, isMulti && item.server, item.library, fixedBy(item.fixedAction, item.fixedMatch)].filter(Boolean).join(' · ')}
              endContent={<Text type="supporting" color="secondary">{relative(item.completedAt, now)}</Text>}
            />
          ))}
        </List>
      )}
    </VStack>
  );
}

export const statusDots: Record<string, 'success' | 'error' | 'warning' | 'neutral' | 'accent'> = {
  finished: 'success',
  failed: 'error',
  canceled: 'warning',
  interrupted: 'warning',
  running: 'accent',
};

function RecentRuns({ isMulti, srv }: { isMulti: boolean; srv?: number }) {
  const runs = useRuns(6, srv);
  return (
    <VStack gap={3}>
      <HStack hAlign="between" vAlign="center">
        <Heading level={3} accessibilityLevel={2}>Recent runs</Heading>
        <Link href={routeHash({ page: 'runs', srv })}>All runs</Link>
      </HStack>
      {runs.error && <Text color="secondary">{runs.error.message}</Text>}
      {runs.isPending && <Skeleton height={200} />}
      {runs.data?.runs.length === 0 && <EmptyState isCompact title="No runs yet" description="Each scheduled or manual run shows up here." />}
      {runs.data && runs.data.runs.length > 0 && (
        <List density="balanced" hasDividers>
          {runs.data.runs.map(run => <RunRow key={run.id} run={run} isMulti={isMulti} href={runHash(run.id, srv)} />)}
        </List>
      )}
    </VStack>
  );
}

const selectNextRun = (status: Status) => status.nextRun;

/** When the next scheduled run starts, from the live status. It shows
 * nothing until the status is known. */
export function NextRun() {
  const nextRun = useStatus(selectNextRun).data;
  const now = useNow();
  const text = nextRunText(nextRun, now);
  if (!text) return null;
  return (
    <HStack gap={2} vAlign="center">
      <Icon icon={ClockIcon} size="sm" color="secondary" />
      <Text color="secondary">Next run {text}</Text>
    </HStack>
  );
}

/** One run as a list row. A detailed row, as on the Runs page of a phone,
 * shows every count and wraps them, with the error below. */
export function RunRow({ run, isMulti, href, isSelected, isDetailed = false }: { run: RunRecord; isMulti: boolean; href: string; isSelected?: boolean; isDetailed?: boolean }) {
  // The Runs page names how the run read; the Status page keeps it short.
  const source = run.kind === 'bulk-refresh' ? 'Bulk refresh' : run.source === 'manual' ? 'Manual run' : 'Scheduled run';
  const kind = isDetailed ? runTitle(run) : source;
  const duration = run.finishedAt ? runDuration(run.startedAt, run.finishedAt) : 'not ended';
  const counts = isDetailed
    ? [`${run.readItems} read`, `${run.refreshed} refreshed`, `${run.matched} matched`, `${run.failed} failed`, `${run.fixed} fixed`, `${run.due} due`, duration].join(' · ')
    : run.error
      ? run.error
      : [`${run.refreshed} refreshed`, `${run.matched} matched`, run.failed ? `${run.failed} failed` : undefined, `${run.fixed} fixed`, runDuration(run.startedAt, run.finishedAt)]
          .filter(Boolean)
          .join(' · ');
  return (
    <ListItem
      href={href}
      isSelected={isSelected}
      label={isMulti ? `${run.server} · ${kind}` : kind}
      description={
        isDetailed ? (
          <VStack gap={0.5}>
            <Text type="supporting" color="secondary">{counts}</Text>
            {run.error && <Text type="supporting" wordBreak="break-word">{run.error}</Text>}
          </VStack>
        ) : (
          counts
        )
      }
      endContent={
        <VStack gap={1} hAlign="end">
          <HStack gap={2} vAlign="center">
            <StatusDot variant={statusDots[run.status] ?? 'neutral'} label={label(run.status)} />
            <Text type="supporting">{label(run.status)}</Text>
          </HStack>
          <Text type="supporting" color="secondary">{dateTime(run.startedAt)}</Text>
        </VStack>
      }
    />
  );
}
