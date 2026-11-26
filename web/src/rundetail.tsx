import { Badge } from '@astryxdesign/core/Badge';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { Card } from '@astryxdesign/core/Card';
import { Drawer } from '@astryxdesign/core/Drawer';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Grid } from '@astryxdesign/core/Grid';
import { Icon } from '@astryxdesign/core/Icon';
import { IconButton } from '@astryxdesign/core/IconButton';
import { HStack, Layout, LayoutContent, LayoutHeader, StackItem, VStack } from '@astryxdesign/core/Layout';
import { List, ListItem } from '@astryxdesign/core/List';
import { SelectableCard } from '@astryxdesign/core/SelectableCard';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { StatusDot } from '@astryxdesign/core/StatusDot';
import { Heading, Text } from '@astryxdesign/core/Text';
import { ClockIcon, InboxIcon, XMarkIcon } from '@heroicons/react/24/outline';
import { useQuery } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { ApiError, api, type RunDetail, type RunRecord } from './api';
import { dateTime, label, plural, runDuration, runTitle, titleYear } from './format';
import { Poster } from './media';
import { itemHash } from './route';
import { useShown } from './presence';
import { filterGroups, nextCountFilter, noEntries, runCounts, runGroups, runPollInterval, type RunCount, type RunEntry, type RunGroup } from './runs';
import { statusDots } from './status';

interface Props {
  /** The open run, or undefined for a closed drawer. */
  id?: number;
  isMulti: boolean;
  /** The server scope of the route, which item links keep. */
  srv?: number;
  onClose: () => void;
}

/** The details of one run in a drawer, as an item of the Items table opens.
 * A narrow screen shows RunDetails in place of the list instead, as the
 * Items page does. */
export function RunDrawer({ id, isMulti, srv, onClose }: Props) {
  // The drawer keeps the run while it slides out, and drops it once the
  // slide ends. A new run starts at the top of its items.
  const drawer = useShown(id, String, onClose);
  const shown = drawer.shown;
  return (
    <Drawer key={drawer.generation} isOpen={drawer.isOpen} onOpenChange={open => !open && onClose()} ref={drawer.ref} label="Run details" width={600} padding={0}>
      {shown !== undefined ? <RunDetails key={shown} id={shown} isMulti={isMulti} srv={srv} onClose={onClose} isPinned /> : null}
    </Drawer>
  );
}

/** What the run id was, its counts, and its items grouped by outcome. In a
 * drawer, isPinned keeps the head and its close button in view while the
 * items scroll. */
export function RunDetails({ id, isMulti, srv, onClose, isPinned = false }: Props & { id: number; isPinned?: boolean }) {
  const detail = useQuery({
    // Under 'runs', so the live status reloads it when a run starts or ends.
    queryKey: ['runs', 'detail', id],
    queryFn: ({ signal }) => api<RunDetail>(`runs/detail?${new URLSearchParams({ id: String(id) })}`, { signal }),
    retry: (count, error) => !(error instanceof ApiError && error.status === 404) && count < 1,
    refetchInterval: query => runPollInterval(query.state.data?.run, query.state.error),
  });
  const isGone = detail.error instanceof ApiError && detail.error.status === 404;
  // A run that retention removed while the drawer was open is gone: its
  // last details are not shown.
  const data = isGone ? undefined : detail.data;
  const head = (
    <HStack gap={3} vAlign="start">
      <StackItem size="fill">
        {data ? <RunHead run={data.run} isMulti={isMulti} /> : detail.isPending && <Skeleton height={32} width="60%" />}
      </StackItem>
      <IconButton label="Close details" icon={<Icon icon={XMarkIcon} size="sm" />} variant="secondary" onClick={onClose} />
    </HStack>
  );
  const body = (
    <>
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
      {data && <RunBody detail={data} scope={srv === undefined || srv === data.run.srv ? srv : undefined} />}
    </>
  );
  if (isPinned) {
    return (
      <Layout
        height="fill"
        header={
          <LayoutHeader padding={6} paddingBlockEnd={4} hasDivider>
            {head}
          </LayoutHeader>
        }
        content={
          <LayoutContent padding={6} label="Run items">
            <VStack gap={6}>{body}</VStack>
          </LayoutContent>
        }
      />
    );
  }
  return (
    <VStack gap={6} padding={6}>
      {head}
      {body}
    </VStack>
  );
}

function DetailSkeleton() {
  return (
    <VStack gap={4}>
      <Skeleton height={16} width="40%" />
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
      <HStack gap={3} vAlign="center" wrap="wrap">
        <HStack gap={1.5} vAlign="center">
          <StatusDot variant={statusDots[run.status] ?? 'neutral'} isPulsing={run.status === 'running'} label={label(run.status)} />
          <Text>{label(run.status)}</Text>
        </HStack>
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

/** The body of the details of a run. scope is the server scope that item
 * links keep: undefined when the route names another server. */
function RunBody({ detail, scope }: { detail: RunDetail; scope?: number }) {
  const { run } = detail;
  const groups = useMemo(() => runGroups(detail), [detail]);
  const counts = useMemo(() => runCounts(run), [run]);
  const [filter, setFilter] = useState<string>();
  const active = counts.find(count => count.name === filter);
  const filtered = useMemo(() => (active?.shows ? filterGroups(groups, active.shows) : []), [groups, active]);
  // A filter whose items the run no longer lists shows every item.
  const isFiltered = filtered.length > 0;
  return (
    <>
      {run.error && <Banner status="error" title={`The run ${run.status === 'canceled' ? 'was canceled' : 'failed'}`} description={run.error} />}
      {run.status === 'running' ? (
        // The run writes its counts when it ends.
        <Text type="supporting" color="secondary">The counts show when the run ends.</Text>
      ) : (
        <RunCounts counts={counts} groups={groups} filter={isFiltered ? filter : undefined} onFilter={setFilter} />
      )}
      {isFiltered && <Text type="supporting" color="secondary">{`Only the listed items of ${filter} are shown. Click the count again to show every item.`}</Text>}
      {groups.length > 0 ? (
        (isFiltered ? filtered : groups).map(group => <EntryGroup key={group.id} group={group} srv={run.srv} scope={scope} />)
      ) : (
        <NoEntries run={run} />
      )}
    </>
  );
}

interface CountsProps {
  counts: RunCount[];
  groups: RunGroup[];
  /** The name of the count that filters the items. */
  filter?: string;
  onFilter: (name: string | undefined) => void;
}

/** The counts of a run. A click on a count whose items the run lists
 * shows only those items, and a second click shows every item again
 * (nextCountFilter). */
function RunCounts({ counts, groups, filter, onFilter }: CountsProps) {
  return (
    <Grid columns={{ minWidth: 112, repeat: 'fit' }} gap={2}>
      {counts.map(count => {
        const shows = count.shows;
        if (!shows || !groups.some(group => group.entries.some(entry => shows(group.id, entry)))) {
          return (
            <Card key={count.name} variant="muted" padding={3}>
              <CountText count={count} />
            </Card>
          );
        }
        return (
          <SelectableCard key={count.name} label={`Show only the items of ${count.name}, ${count.value.toLocaleString()}`} variant="muted" padding={3} isSelected={filter === count.name} onChange={() => onFilter(nextCountFilter(filter, count.name))}>
            <CountText count={count} />
          </SelectableCard>
        );
      })}
    </Grid>
  );
}

function CountText({ count }: { count: RunCount }) {
  return (
    <VStack gap={0.5}>
      <Text size="lg" weight="semibold" hasTabularNumbers>{count.value.toLocaleString()}</Text>
      <Text type="supporting" color="secondary">{count.name}</Text>
    </VStack>
  );
}

function EntryGroup({ group, srv, scope }: { group: RunGroup; srv: number; scope?: number }) {
  return (
    <List
      density="balanced"
      hasDividers
      edge="outset"
      header={
        <HStack gap={2} vAlign="center">
          <Heading level={3} accessibilityLevel={3}>{group.title}</Heading>
          <Badge label={group.entries.length.toLocaleString()} />
        </HStack>
      }>
      {group.entries.map(entry => <EntryRow key={entry.id} entry={entry} srv={srv} scope={scope} />)}
    </List>
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
          <Text type="supporting" color="secondary">{[entry.library, entry.summary].filter(Boolean).join(' · ')}</Text>
          {entry.detail && <Text type="supporting" wordBreak="break-word">{entry.detail}</Text>}
        </VStack>
      }
      endContent={entry.at ? <Text type="supporting" color="secondary" hasTabularNumbers>{new Date(entry.at).toLocaleTimeString(undefined, { timeStyle: 'short' })}</Text> : undefined}
    />
  );
}

/** Says why a run lists no item. */
function NoEntries({ run }: { run: RunRecord }) {
  const { title, description } = noEntries(run);
  return <EmptyState isCompact title={title} description={description} icon={<Icon icon={InboxIcon} size="lg" />} />;
}
