import { AppShell } from '@astryxdesign/core/AppShell';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { ButtonGroup } from '@astryxdesign/core/ButtonGroup';
import { DropdownMenu, DropdownMenuDivider, DropdownMenuItem, DropdownMenuSubMenu } from '@astryxdesign/core/DropdownMenu';
import { useMediaQuery } from '@astryxdesign/core/hooks';
import { DEFAULT_WIDTH_BREAKPOINTS } from '@astryxdesign/core/theme';
import { Icon } from '@astryxdesign/core/Icon';
import { HStack, VStack } from '@astryxdesign/core/Layout';
import { Selector } from '@astryxdesign/core/Selector';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { StatusDot } from '@astryxdesign/core/StatusDot';
import { Text } from '@astryxdesign/core/Text';
import { TopNav, TopNavHeading, TopNavItem } from '@astryxdesign/core/TopNav';
import { ChevronDownIcon } from '@heroicons/react/24/outline';
import { lazy, Suspense, useEffect } from 'react';
import { useLiveStatus, useStatus, type Connection, type ServerStatus } from './api';
import { plural } from './format';
import { useNotify } from './notify';
import { onQueueDone, queueOutcome, queueText } from './queue';
import { navigate, routeHash, routeScope, useRoute, withScope, type Route } from './route';
import { useRunNow } from './runs';
import { StatusPage } from './status';

const ItemsPage = lazy(async () => ({ default: (await import('./items')).ItemsPage }));
const RunsPage = lazy(async () => ({ default: (await import('./runspage')).RunsPage }));

export function App() {
  const connection = useLiveStatus();
  const notify = useNotify();
  useEffect(() => onQueueDone(queue => notify(queueOutcome(queue), queueText(queue), { autoHideDuration: 10_000 })), [notify]);
  const route = useRoute();
  const status = useStatus();
  const srv = routeScope(route, status.data?.servers.length);
  // A scoped page waits for the servers, so its queries use a valid scope.
  const isScopePending = status.isPending && route.srv !== undefined;
  return (
    <AppShell
      height="fill"
      topNav={<TopBar route={route} srv={srv} />}
      banner={connectionBanner(connection)}>
      {isScopePending ? (
        <PageSkeleton />
      ) : route.page === 'items' ? (
        <Suspense fallback={<PageSkeleton />}>
          <ItemsPage tab={route.tab} item={route.item} srv={srv} />
        </Suspense>
      ) : route.page === 'runs' ? (
        <Suspense fallback={<PageSkeleton />}>
          <RunsPage srv={srv} run={route.run} />
        </Suspense>
      ) : (
        <StatusPage srv={srv} />
      )}
    </AppShell>
  );
}

/** Returns the banner of a stream that is down. AppShell keeps room for any
 * banner node, so a stream that is up gets undefined. */
function connectionBanner(connection: Connection) {
  if (connection === 'reconnecting') {
    return <Banner status="warning" container="section" collapsible={false} title="Reconnecting to autorefresh" description="Live status is paused until the connection returns." />;
  }
  if (connection === 'unavailable') {
    return <Banner status="warning" container="section" collapsible={false} title="Cannot reach the live status" description="Autorefresh tries again every few seconds. Until then, the status does not update by itself." />;
  }
  return undefined;
}

function PageSkeleton() {
  return (
    <VStack gap={3} padding={6}>
      <Skeleton height={40} />
      <Skeleton height={400} index={1} />
    </VStack>
  );
}

function TopBar({ route, srv }: { route: Route; srv?: number }) {
  const status = useStatus().data;
  return (
    <TopNav
      heading={<TopNavHeading heading="autorefresh" headingHref={routeHash({ page: 'status', srv })} />}
      startContent={
        <>
          <TopNavItem label="Status" href={routeHash({ page: 'status', srv })} isSelected={route.page === 'status'} />
          <TopNavItem label="Items" href={routeHash({ page: 'items', tab: 'attention', srv })} isSelected={route.page === 'items'} />
          <TopNavItem label="Runs" href={routeHash({ page: 'runs', srv })} isSelected={route.page === 'runs'} />
        </>
      }
      endContent={
        status && (
          <HStack gap={4} vAlign="center">
            <RunSummary servers={status.servers} />
            <ServerPicker servers={status.servers} route={route} srv={srv} />
            <RunNow servers={status.servers} />
          </HStack>
        )
      }
    />
  );
}

/** The server scope of every page. One server needs no picker. */
function ServerPicker({ servers, route, srv }: { servers: ServerStatus[]; route: Route; srv?: number }) {
  if (servers.length <= 1) return null;
  return (
    <Selector
      label="Server"
      isLabelHidden
      variant="secondary"
      value={srv === undefined ? '' : String(srv)}
      options={[{ value: '', label: 'All servers' }, ...servers.map(server => ({ value: String(server.srv), label: server.name }))]}
      onChange={value => navigate(withScope(route, value ? Number(value) : undefined))}
    />
  );
}

// Below this width, with several servers, the top bar keeps its room for
// the server picker and Run now. The Status page shows each run.
const roomyTopBar = `(min-width: ${DEFAULT_WIDTH_BREAKPOINTS.sm}px)`;

/** Shows the progress of the running servers, combined when several run. */
function RunSummary({ servers }: { servers: ServerStatus[] }) {
  const isRoomy = useMediaQuery(roomyTopBar);
  const running = servers.filter(server => server.run);
  if (!running.length || (servers.length > 1 && !isRoomy)) return null;
  const done = running.reduce((sum, server) => sum + (server.run?.done ?? 0), 0);
  const selected = running.reduce((sum, server) => sum + (server.run?.selected ?? 0), 0);
  const who = running.length === 1 ? running[0].name : `${plural(running.length, 'server')} running`;
  return (
    <HStack gap={2} vAlign="center">
      <StatusDot variant="success" isPulsing label="Running" />
      <Text type="supporting" color="secondary" hasTabularNumbers>
        {who} {selected ? `${done} / ${selected}` : '· reading'}
      </Text>
    </HStack>
  );
}

function RunNow({ servers }: { servers: ServerStatus[] }) {
  const runNow = useRunNow();
  const busy = servers.some(server => server.run);
  const reason = busy ? 'A run is busy' : undefined;
  // One request at a time. An open menu disables its items when a run
  // starts or a request leaves, and a late choice does nothing.
  const isBlocked = busy || runNow.isPending;
  const start = (srv: number | null, full: boolean) => {
    if (!isBlocked) runNow.mutate({ srv, full });
  };
  if (servers.length <= 1) {
    const isDisabled = busy || !servers.length;
    return (
      <ButtonGroup label="Run now">
        <Button label="Run now" variant="primary" isLoading={runNow.isPending} isDisabled={isDisabled} tooltip={reason} onClick={() => start(null, false)} />
        <DropdownMenu
          button={{ label: 'More run options', variant: 'primary', isIconOnly: true, icon: <Icon icon={ChevronDownIcon} size="sm" />, isDisabled: isDisabled || runNow.isPending, tooltip: reason }}
          hasChevron={false}
          alignment="end"
        >
          <DropdownMenuItem label="Full cache refresh, then run" description={fullDescription} isDisabled={isBlocked} onClick={() => start(null, true)} />
        </DropdownMenu>
      </ButtonGroup>
    );
  }
  return (
    <DropdownMenu button={{ label: 'Run now', variant: 'primary', isLoading: runNow.isPending, isDisabled: busy, tooltip: reason }} alignment="end">
      <DropdownMenuItem label="All servers" isDisabled={isBlocked} onClick={() => start(null, false)} />
      {servers.map(server => (
        <DropdownMenuItem key={server.srv} label={server.name} isDisabled={isBlocked} onClick={() => start(server.srv, false)} />
      ))}
      <DropdownMenuDivider />
      <DropdownMenuSubMenu label="Full cache refresh, then run" description={fullDescription} isDisabled={isBlocked}>
        <DropdownMenuItem label="All servers" isDisabled={isBlocked} onClick={() => start(null, true)} />
        {servers.map(server => (
          <DropdownMenuItem key={server.srv} label={server.name} isDisabled={isBlocked} onClick={() => start(server.srv, true)} />
        ))}
      </DropdownMenuSubMenu>
    </DropdownMenu>
  );
}

const fullDescription = 'Reads every library in full, then refreshes the due items';
