import { Badge } from '@astryxdesign/core/Badge';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { Drawer } from '@astryxdesign/core/Drawer';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Grid } from '@astryxdesign/core/Grid';
import { Icon } from '@astryxdesign/core/Icon';
import { IconButton } from '@astryxdesign/core/IconButton';
import { HStack, StackItem, VStack } from '@astryxdesign/core/Layout';
import { List, ListItem } from '@astryxdesign/core/List';
import { Section } from '@astryxdesign/core/Section';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { StatusDot } from '@astryxdesign/core/StatusDot';
import { Heading, Text } from '@astryxdesign/core/Text';
import { ClockIcon, InboxIcon, XMarkIcon } from '@heroicons/react/24/outline';
import { useQuery } from '@tanstack/react-query';
import { ApiError, api, type RunDetail, type RunRecord } from './api';
import { dateTime, label, plural, runDuration, runTitle, titleYear } from './format';
import { Poster } from './media';
import { itemHash } from './route';
import { runCounts, runGroups, type RunEntry, type RunGroup } from './runs';
import { statusDots } from './status';

/** How often the details of a running run reload. */
const runningInterval = 5_000;

interface Props {
  /** The open run, or undefined for a closed drawer. */
  id?: number;
  isMulti: boolean;
  /** The server scope of the route, which item links keep. */
  srv?: number;
  onClose: () => void;
}

/** The details of one run in a drawer, as an item of the Items table opens:
 * what the run was, its counts, and its items grouped by outcome. */
export function RunDrawer({ id, isMulti, srv, onClose }: Props) {
  return (
    <Drawer isOpen={id !== undefined} onOpenChange={open => !open && onClose()} label="Run details" width={600} padding={0}>
      {id !== undefined ? <RunDetails id={id} isMulti={isMulti} srv={srv} onClose={onClose} /> : null}
    </Drawer>
  );
}

function RunDetails({ id, isMulti, srv, onClose }: Props & { id: number }) {
  const detail = useQuery({
    // Under 'runs', so the live status reloads it when a run starts or ends.
    queryKey: ['runs', 'detail', id],
    queryFn: ({ signal }) => api<RunDetail>(`runs/detail?${new URLSearchParams({ id: String(id) })}`, { signal }),
    retry: (count, error) => !(error instanceof ApiError && error.status === 404) && count < 1,
    refetchInterval: query => (query.state.status === 'success' && query.state.data?.run.status === 'running' ? runningInterval : false),
  });
  const isGone = detail.error instanceof ApiError && detail.error.status === 404;
  // A run that retention removed while the drawer was open is gone: its
  // last details are not shown.
  const data = isGone ? undefined : detail.data;
  return (
    <VStack gap={6} padding={6}>
      <HStack gap={3} vAlign="start">
        <StackItem size="fill">
          {data ? <RunHead run={data.run} isMulti={isMulti} /> : <Heading level={2} accessibilityLevel={2}>Run</Heading>}
        </StackItem>
        <IconButton label="Close details" icon={<Icon icon={XMarkIcon} size="sm" />} variant="secondary" onClick={onClose} />
      </HStack>
      {detail.isPending && <DetailSkeleton />}
      {isGone && (
        <EmptyState
          title="This run no longer exists"
          description="Autorefresh removed it with its old history, or its server is no longer in the configuration."
          icon={<Icon icon={ClockIcon} size="lg" />}
        />
      )}
      {detail.error && !isGone && (
        <Banner
          status="error"
          title="The run is unavailable"
          description={detail.error.message}
          endContent={<Button label="Try again" onClick={() => void detail.refetch()} />}
        />
      )}
      {data && <RunBody detail={data} srv={srv} />}
    </VStack>
  );
}

function DetailSkeleton() {
  return (
    <VStack gap={4}>
      <Skeleton height={72} />
      {[0, 1, 2].map(i => <Skeleton key={i} index={i} height={56} />)}
    </VStack>
  );
}

function RunHead({ run, isMulti }: { run: RunRecord; isMulti: boolean }) {
  const reads = (run.fullReads ?? 0) + (run.incrementalReads ?? 0);
  return (
    <VStack gap={2}>
      <Heading level={2} accessibilityLevel={2} wordBreak="break-word">{runTitle(run)}</Heading>
      <HStack gap={2} vAlign="center" wrap="wrap">
        <StatusDot variant={statusDots[run.status] ?? 'neutral'} isPulsing={run.status === 'running'} label={label(run.status)} />
        <Text>{label(run.status)}</Text>
        <Text color="secondary" hasTabularNumbers>
          {[dateTime(run.startedAt), run.finishedAt ? runDuration(run.startedAt, run.finishedAt) : 'not ended', isMulti ? run.server : undefined].filter(Boolean).join(' · ')}
        </Text>
      </HStack>
      {reads > 0 && (
        <Text type="supporting" color="secondary">
          {`${plural(reads, 'library', 'libraries')} read · ${run.fullReads ?? 0} full, ${run.incrementalReads ?? 0} incremental`}
        </Text>
      )}
    </VStack>
  );
}

function RunBody({ detail, srv }: { detail: RunDetail; srv?: number }) {
  const { run } = detail;
  const groups = runGroups(detail);
  return (
    <>
      {run.error && <Banner status="error" title={`The run ${run.status === 'canceled' ? 'was canceled' : 'failed'}`} description={run.error} />}
      <RunCounts run={run} />
      {groups.length > 0 ? groups.map(group => <EntryGroup key={group.id} group={group} srv={run.srv} scope={srv} />) : <NoEntries run={run} />}
    </>
  );
}

function RunCounts({ run }: { run: RunRecord }) {
  const counts = runCounts(run);
  return (
    <Grid columns={{ minWidth: 80, repeat: 'fit' }} gap={2}>
      {counts.map(([name, value]) => (
        <Section key={name} variant="muted" paddingInline={3} paddingBlock={2}>
          <VStack gap={0.5}>
            <Text size="lg" weight="semibold" hasTabularNumbers>{value.toLocaleString()}</Text>
            <Text type="supporting" color="secondary">{name}</Text>
          </VStack>
        </Section>
      ))}
    </Grid>
  );
}

function EntryGroup({ group, srv, scope }: { group: RunGroup; srv: number; scope?: number }) {
  return (
    <VStack gap={2}>
      <HStack gap={2} vAlign="center">
        <Heading level={3} accessibilityLevel={3}>{group.title}</Heading>
        <Badge label={group.entries.length.toLocaleString()} />
      </HStack>
      <List density="balanced" hasDividers>
        {group.entries.map(entry => <EntryRow key={entry.id} entry={entry} srv={srv} scope={scope} />)}
      </List>
    </VStack>
  );
}

function EntryRow({ entry, srv, scope }: { entry: RunEntry; srv: number; scope?: number }) {
  return (
    <ListItem
      href={itemHash(entry.tab, { srv, key: entry.key }, scope)}
      align="start"
      startContent={<Poster src={entry.thumb} title={entry.title} type={entry.type} size="sm" />}
      label={entry.title ? titleYear(entry.title, entry.year) : `Item ${entry.key}`}
      description={
        <VStack gap={0.5}>
          <Text type="supporting" color="secondary">{entry.summary}</Text>
          {entry.detail && <Text type="supporting" wordBreak="break-word">{entry.detail}</Text>}
        </VStack>
      }
      endContent={entry.at ? <Text type="supporting" color="secondary" hasTabularNumbers>{new Date(entry.at).toLocaleTimeString(undefined, { timeStyle: 'short' })}</Text> : undefined}
    />
  );
}

/** Says why a run lists no item: it is still at work, it changed none, or
 * its items were not recorded. */
function NoEntries({ run }: { run: RunRecord }) {
  const icon = <Icon icon={InboxIcon} size="lg" />;
  if (run.status === 'running') return <EmptyState isCompact title="No item changed yet" description="The items show here as the run works on them." icon={icon} />;
  const changed = run.kind === 'bulk-refresh' ? run.selected : run.refreshed + run.matched + run.failed + run.completed;
  if (changed > 0) {
    return (
      <EmptyState
        isCompact
        title="Item details were not recorded for this run"
        description="The run is from before autorefresh recorded them, or its history expired."
        icon={icon}
      />
    );
  }
  return <EmptyState isCompact title="No item changed in this run" description={`It read ${plural(run.readItems, 'item')} and refreshed none.`} icon={icon} />;
}
