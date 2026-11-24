import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { useMediaQuery } from '@astryxdesign/core/hooks';
import { DEFAULT_WIDTH_BREAKPOINTS } from '@astryxdesign/core/theme';
import { Icon } from '@astryxdesign/core/Icon';
import { HStack, VStack } from '@astryxdesign/core/Layout';
import { List } from '@astryxdesign/core/List';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { StatusDot } from '@astryxdesign/core/StatusDot';
import { Table, pixel, proportional, type TableColumn } from '@astryxdesign/core/Table';
import { Heading, Text } from '@astryxdesign/core/Text';
import { ClockIcon } from '@heroicons/react/24/outline';
import { useInfiniteQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { api, useStatus, type RunRecord } from './api';
import { useNow } from './clock';
import { dateTime, label, relative, runDuration, runKind } from './format';
import { navigate, runHash } from './route';
import { RunDetails, RunDrawer } from './rundetail';
import { useRowOpen } from './rowopen';
import { NextRun, RunRow, statusDots } from './status';

const tableQuery = `(min-width: ${DEFAULT_WIDTH_BREAKPOINTS.lg}px)`;
const pageSize = 50;

interface RunPage {
  runs: RunRecord[];
  next?: string;
}

/** Returns the query of one page of runs. A cursor belongs to its scope:
 * the API refuses it for another server. */
function runsPath(srv: number | undefined, cursor: string): string {
  const params = new URLSearchParams({ limit: String(pageSize) });
  if (srv !== undefined) params.set('srv', String(srv));
  if (cursor) params.set('cursor', cursor);
  return `runs?${params}`;
}

/** The run history of every server, or of the server srv, newest first.
 * The run whose ID is run opens in a drawer. */
export function RunsPage({ srv, run }: { srv?: number; run?: number }) {
  const status = useStatus();
  const isMulti = (status.data?.servers.length ?? 0) > 1;
  const isTable = useMediaQuery(tableQuery);
  const pages = useInfiniteQuery({
    // The key is under 'runs', which the live status refreshes when a run
    // starts or ends, and holds the scope, so a new scope starts from the
    // first page: a cursor belongs to its scope.
    queryKey: ['runs', 'pages', srv ?? 'all'],
    initialPageParam: '',
    queryFn: ({ pageParam, signal }) => api<RunPage>(runsPath(srv, pageParam), { signal }),
    getNextPageParam: page => page.next || undefined,
  });
  const runs = useMemo(() => pages.data?.pages.flatMap(page => page.runs) ?? [], [pages.data]);
  const close = () => navigate({ page: 'runs', srv });
  // A narrow screen shows the open run on its own screen, as the Items page
  // shows an open item.
  // The key starts each run with its own state, such as its group filter.
  if (!isTable && run !== undefined) return <RunDetails key={run} id={run} isMulti={isMulti} srv={srv} onClose={close} />;
  return (
    <>
      <VStack gap={6} padding={6}>
        <HStack hAlign="between" vAlign="center" wrap="wrap" gap={3}>
          <Heading level={1}>Runs</Heading>
          <NextRun />
        </HStack>
        {pages.error && (
          <Banner
            status="error"
            title="The runs are unavailable"
            description={pages.error.message}
            endContent={<Button label="Try again" onClick={() => void pages.refetch()} />}
          />
        )}
        {pages.isPending ? (
          <VStack gap={3}>
            {[0, 1, 2, 3].map(i => <Skeleton key={i} index={i} height={48} />)}
          </VStack>
        ) : runs.length === 0 ? (
          !pages.error && <EmptyState title="No runs yet" description="Runs show here once autorefresh has run, on its schedule or by hand." icon={<Icon icon={ClockIcon} size="lg" />} />
        ) : isTable ? (
          <RunTable runs={runs} isMulti={isMulti} srv={srv} openRun={run} />
        ) : (
          <List density="balanced" hasDividers>
            {runs.map(entry => <RunRow key={entry.id} run={entry} isMulti={isMulti} href={runHash(entry.id, srv)} isSelected={entry.id === run} isDetailed />)}
          </List>
        )}
        {pages.hasNextPage && (
          <HStack hAlign="center">
            <Button
              label="Load more"
              isLoading={pages.isFetchingNextPage}
              isDisabled={pages.isFetching}
              onClick={() => {
                if (!pages.isFetching) void pages.fetchNextPage({ cancelRefetch: false });
              }}
            />
          </HStack>
        )}
      </VStack>
      {isTable && <RunDrawer id={run} isMulti={isMulti} srv={srv} onClose={close} />}
    </>
  );
}

interface Row extends Record<string, unknown> {
  run: RunRecord;
  id: number;
}

function RunTable({ runs, isMulti, srv, openRun }: { runs: RunRecord[]; isMulti: boolean; srv?: number; openRun?: number }) {
  const now = useNow();
  const open = useRowOpen(
    useMemo(() => (row: Row) => navigate({ page: 'runs', run: row.id, srv }), [srv]),
    openRun,
  );
  const rows = useMemo(() => runs.map(run => ({ id: run.id, run })), [runs]);
  const count = (key: keyof RunRecord, header: string): TableColumn<Row> => ({
    key,
    header,
    align: 'end',
    width: pixel(112),
    renderCell: ({ run }) => <Text hasTabularNumbers>{String(run[key] ?? 0)}</Text>,
  });
  const columns: TableColumn<Row>[] = [
    {
      key: 'startedAt',
      header: 'Started',
      width: proportional(1.4, { minWidth: 160 }),
      renderCell: ({ run }) => (
        <VStack gap={0}>
          <Text>{dateTime(run.startedAt)}</Text>
          <Text type="supporting" color="secondary">{relative(run.startedAt, now)}</Text>
        </VStack>
      ),
    },
    {
      key: 'duration',
      header: 'Duration',
      width: pixel(96),
      renderCell: ({ run }) => <Text hasTabularNumbers>{run.finishedAt ? runDuration(run.startedAt, run.finishedAt) : 'not ended'}</Text>,
    },
    ...(isMulti ? [{ key: 'server', header: 'Server', width: proportional(1, { minWidth: 120 }), renderCell: ({ run }: Row) => <Text>{run.server}</Text> }] : []),
    { key: 'source', header: 'Source', width: pixel(104), renderCell: ({ run }) => <Text>{run.source === 'manual' ? 'Manual' : 'Schedule'}</Text> },
    { key: 'kind', header: 'Kind', width: pixel(184), renderCell: ({ run }) => <Text>{runKind(run)}</Text> },
    {
      key: 'status',
      header: 'Status',
      width: pixel(128),
      renderCell: ({ run }) => (
        <HStack gap={2} vAlign="center">
          <StatusDot variant={statusDots[run.status] ?? 'neutral'} isPulsing={run.status === 'running'} label={label(run.status)} />
          <Text>{label(run.status)}</Text>
        </HStack>
      ),
    },
    count('readItems', 'Read'),
    count('refreshed', 'Refreshed'),
    count('matched', 'Matched'),
    count('failed', 'Failed'),
    count('fixed', 'Fixed'),
    count('due', 'Due'),
    {
      key: 'error',
      header: 'Error',
      width: proportional(1.4, { minWidth: 140 }),
      renderCell: ({ run }) => <Text type="supporting" wordBreak="break-word">{run.error ?? ''}</Text>,
    },
  ];
  return <Table data={rows} columns={columns} idKey="id" density="spacious" hasHover plugins={{ open }} />;
}
