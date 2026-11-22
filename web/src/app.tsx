import { AppShell } from '@astryxdesign/core/AppShell';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { DropdownMenu, DropdownMenuItem } from '@astryxdesign/core/DropdownMenu';
import { HStack, VStack } from '@astryxdesign/core/Layout';
import { Selector } from '@astryxdesign/core/Selector';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { StatusDot } from '@astryxdesign/core/StatusDot';
import { Text } from '@astryxdesign/core/Text';
import { useToast } from '@astryxdesign/core/Toast';
import { TopNav, TopNavHeading, TopNavItem } from '@astryxdesign/core/TopNav';
import { lazy, Suspense, useEffect } from 'react';
import { useLiveStatus, useStatus, type ServerStatus } from './api';
import { plural } from './format';
import { onQueueDone, queueFailed, queueText } from './queue';
import { navigate, routeHash, routeScope, useRoute, withScope, type Route } from './route';
import { useRunNow } from './runs';
import { StatusPage } from './status';

const ItemsPage = lazy(async () => ({ default: (await import('./items')).ItemsPage }));

export function App() {
  const connection = useLiveStatus();
  const showToast = useToast();
  useEffect(() => onQueueDone(queue => showToast({ body: queueText(queue), type: queueFailed(queue) ? 'error' : 'info', autoHideDuration: 10_000 })), [showToast]);
  const route = useRoute();
  const serverCount = useStatus().data?.servers.length ?? 0;
  const srv = routeScope(route, serverCount);
  return (
    <AppShell
      height="fill"
      topNav={<TopBar route={route} srv={srv} />}
      banner={
        connection === 'reconnecting' ? (
          <Banner status="warning" container="section" collapsible={false} title="Reconnecting to autorefresh" description="Live status is paused until the connection returns." />
        ) : undefined
      }>
      {route.page === 'items' ? (
        <Suspense fallback={<PageSkeleton />}>
          <ItemsPage tab={route.tab} item={route.item} srv={srv} />
        </Suspense>
      ) : (
        <StatusPage srv={srv} />
      )}
    </AppShell>
  );
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

/** Shows the progress of the running servers, combined when several run. */
function RunSummary({ servers }: { servers: ServerStatus[] }) {
  const running = servers.filter(server => server.run);
  if (!running.length) return null;
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
  if (servers.length <= 1) {
    return (
      <Button
        label="Run now"
        variant="primary"
        isLoading={runNow.isPending}
        isDisabled={busy || !servers.length}
        tooltip={reason}
        onClick={() => runNow.mutate(null)}
      />
    );
  }
  return (
    <DropdownMenu button={{ label: 'Run now', variant: 'primary', isLoading: runNow.isPending, isDisabled: busy, tooltip: reason }} alignment="end">
      <DropdownMenuItem label="All servers" onClick={() => runNow.mutate(null)} />
      {servers.map(server => (
        <DropdownMenuItem key={server.srv} label={server.name} onClick={() => runNow.mutate(server.srv)} />
      ))}
    </DropdownMenu>
  );
}
