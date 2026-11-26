import { Badge } from '@astryxdesign/core/Badge';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { Drawer } from '@astryxdesign/core/Drawer';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { useMediaQuery } from '@astryxdesign/core/hooks';
import { Icon } from '@astryxdesign/core/Icon';
import { IconButton } from '@astryxdesign/core/IconButton';
import { HStack, Layout, LayoutContent, LayoutHeader, LayoutPanel, StackItem, VStack } from '@astryxdesign/core/Layout';
import { List, ListItem } from '@astryxdesign/core/List';
import { ResizeHandle, useResizable } from '@astryxdesign/core/Resizable';
import { SegmentedControl, SegmentedControlItem } from '@astryxdesign/core/SegmentedControl';
import { Selector } from '@astryxdesign/core/Selector';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { Switch } from '@astryxdesign/core/Switch';
import { Tab, TabList } from '@astryxdesign/core/TabList';
import { Heading, Text } from '@astryxdesign/core/Text';
import { TextInput } from '@astryxdesign/core/TextInput';
import { BarsArrowDownIcon, BarsArrowUpIcon, InboxIcon } from '@heroicons/react/24/outline';
import { useInfiniteQuery } from '@tanstack/react-query';
import { useEffect, useMemo, useRef, useState } from 'react';
import { api, itemID, useServers, type ItemPage, type ItemRecord, type ServerInfo } from './api';
import { isSearchTooLong, listParams, maxSearchBytes, type Filters, type Selection } from './bulk';
import { BulkBand, useBulkAction } from './bulkband';
import { useNow } from './clock';
import { ItemDetail } from './detail';
import { fixedBy, label, plural, queueState, relative } from './format';
import { ItemTable } from './itemtable';
import { Poster, Reasons } from './media';
import { useShown } from './presence';
import { itemHash, navigate, tabNames, type ItemKey, type TabName } from './route';
import { chooseSort, formatSort, parseSort, sortKeys } from './table';

const tabTitles: Record<TabName, string> = { attention: 'Attention', fixed: 'Fixed', stopped: 'Stopped' };
const reasonOptions = ['unmatched', 'no-summary', 'no-poster', 'no-art', 'episode-no-summary', 'episode-unmatched'];
const anyOption = { value: '', label: 'Any' };
const noReasons: string[] = [];

// Below this width the detail replaces the list instead of sitting beside it.
const twoPaneQuery = '(min-width: 1000px)';

type Mode = 'list' | 'table';

function storedMode(): Mode {
  try {
    return localStorage.getItem('autorefresh.items.mode') === 'table' ? 'table' : 'list';
  } catch {
    return 'list';
  }
}

function storeMode(mode: Mode) {
  try {
    localStorage.setItem('autorefresh.items.mode', mode);
  } catch {
    // Without storage the mode only lasts for this page.
  }
}

/** Returns the list parameters params without the sort. */
function unsorted(params: unknown): string {
  const p = new URLSearchParams(String(params));
  p.delete('sort');
  return p.toString();
}

/** Returns value once it has not changed for delay milliseconds. */
function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return settled;
}

/** The library filter belongs to the server it was chosen for. */
interface LibraryFilter {
  srv: number;
  section: string;
}

/** The Items page of every server, or of the server srv. */
export function ItemsPage({ tab, item, srv }: { tab: TabName; item?: ItemKey; srv?: number }) {
  const servers = useServers();
  const inventory = servers.data?.servers ?? [];
  const isMulti = inventory.length > 1;
  const ignored = servers.data?.ignoreReasons ?? noReasons;
  const isTwoPane = useMediaQuery(twoPaneQuery);

  const [mode, setMode] = useState<Mode>(storedMode);
  const [filters, setFilters] = useState<Pick<Filters, 'reason' | 'type'>>({});
  const [library, setLibrary] = useState<LibraryFilter>();
  // "All" means another filter on each tab, so each tab keeps its own.
  const [all, setAll] = useState<Partial<Record<TabName, boolean>>>({});
  const [search, setSearch] = useState('');
  // A search that the API would refuse is not applied: the list keeps the
  // last one that it takes.
  const [query, setQuery] = useState('');
  const isSearchInvalid = isSearchTooLong(search);
  const changeSearch = (value: string) => {
    setSearch(value);
    if (!isSearchTooLong(value)) setQuery(value);
  };
  const q = useDebounced(query, 250);
  const [sorts, setSorts] = useState<Partial<Record<TabName, string>>>({});
  const sort = sorts[tab] ?? '';
  // A single server still exposes its library filter, while keeping scope.srv
  // stable (undefined) until a library filter is chosen so initial inventory
  // load does not double-fetch or clear early selections.
  const singleServer = srv === undefined && inventory.length === 1 ? inventory[0] : undefined;
  const libraryServer = srv !== undefined ? inventory.find(entry => entry.srv === srv) : singleServer;
  const activeLibrary = library && libraryServer && library.srv === libraryServer.srv ? library.section : undefined;
  const scopeSrv = srv ?? (activeLibrary !== undefined ? libraryServer?.srv : undefined);
  const scope: Filters = {
    type: filters.type,
    // The Fixed tab has no reason filter. The Attention filter stays for
    // when the user returns.
    reason: tab === 'fixed' ? undefined : filters.reason,
    all: all[tab],
    q,
    srv: scopeSrv,
    library: activeLibrary,
  };
  const params = listParams(tab, scope, sort).toString();
  // A selection belongs to a tab and its filters. Sorting keeps it.
  const selectionScope = listParams(tab, scope, '').toString();
  const scanned = inventory.filter(entry => scope.srv === undefined || entry.srv === scope.srv).some(entry => entry.scanned);

  const [selection, setSelection] = useState<{ scope: string; value: Selection }>();
  const selected = mode === 'table' && selection?.scope === selectionScope ? selection.value : undefined;
  const select = (value?: Selection) => setSelection(value && { scope: selectionScope, value });
  // A bulk action clears the selection that it took, but not a newer one
  // that the user made while it ran.
  const bulkAction = useBulkAction(submitted => setSelection(current => (current?.value === submitted ? undefined : current)));

  const page = useInfiniteQuery({
    queryKey: ['items', params],
    initialPageParam: '',
    queryFn: ({ pageParam, signal }) => api<ItemPage>(`items?${params}${pageParam ? `&${new URLSearchParams({ cursor: pageParam })}` : ''}`, { signal }),
    getNextPageParam: data => data.next || undefined,
    // A new sort keeps the rows and counts on screen until it loads, so a
    // selection never shows a wrong count in the meantime.
    placeholderData: (previous, previousQuery) => (previousQuery && unsorted(previousQuery.queryKey[1]) === selectionScope ? previous : undefined),
  });
  // The windowed table keeps its first visible row in place whenever its
  // data changes identity, so the rows change only with the pages.
  const rows = useMemo(() => page.data?.pages.flatMap(p => p.items) ?? [], [page.data]);
  // The table key names the list whose rows it shows. Rows that stay on
  // screen while a new sort loads keep the old key, so the table remounts
  // once, when the new rows arrive.
  const [shownParams, setShownParams] = useState(params);
  if (!page.isPlaceholderData && shownParams !== params) setShownParams(params);
  const tableKey = page.isPlaceholderData ? shownParams : params;
  // List mode scrolls the content itself, so a new filter or sort scrolls
  // it back to the top once the new rows arrive, as the table remounts.
  const contentRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (mode !== 'table') contentRef.current?.scrollTo({ top: 0 });
  }, [tableKey, mode]);
  const counts = page.data?.pages[0]?.counts;
  const server = (key: ItemKey) => inventory.find(entry => entry.srv === key.srv);
  const close = () => navigate({ page: 'items', tab, srv });
  const detail = item && (
    <ItemDetail key={itemID(item)} target={item} server={server(item)} ignored={ignored} onClose={close} />
  );

  const changeMode = (value: string) => {
    setMode(value as Mode);
    storeMode(value as Mode);
  };
  const changeSort = (value: string) => setSorts(previous => ({ ...previous, [tab]: value }));

  const header = (
    <ItemsHeader
      tab={tab}
      counts={counts}
      mode={mode}
      onMode={changeMode}
      srv={srv}
      server={libraryServer}
      filters={scope}
      onFilters={change => {
        const { library: section, all: value, ...rest } = change;
        if ('library' in change) setLibrary(section !== undefined && libraryServer !== undefined ? { srv: libraryServer.srv, section } : undefined);
        if ('all' in change) setAll(previous => ({ ...previous, [tab]: value }));
        setFilters(previous => ({ ...previous, ...rest }));
      }}
      search={search}
      isSearchInvalid={isSearchInvalid}
      onSearch={changeSearch}
      sort={sort}
      onSort={changeSort}
      bulk={
        selected && (
          <BulkBand
            tab={tab}
            scope={scope}
            selection={selected}
            total={counts?.[tab] ?? 0}
            inventory={inventory}
            isInventoryKnown={servers.data !== undefined}
            isPending={bulkAction.isPending}
            pendingAction={bulkAction.pendingAction}
            onStart={bulkAction.start}
            onClear={() => select(undefined)}
          />
        )
      }
    />
  );

  const isTable = mode === 'table';
  const list = isTable ? (
    // The table scrolls inside the height that this item gives it, so
    // Astryx can window its rows.
    <StackItem size="fill">
      <ItemTable
        // A new filter or sort starts at the top: the new rows are another
        // table, so they do not scroll to where the old first row moved.
        key={tableKey}
        tab={tab}
        rows={rows}
        isMulti={isMulti}
        ignored={ignored}
        sort={sort}
        onSort={changeSort}
        selection={selected}
        onSelect={select}
        openItem={item}
        srv={srv}
      />
    </StackItem>
  ) : (
    <ItemList tab={tab} rows={rows} isMulti={isMulti} ignored={ignored} openItem={item} srv={srv} />
  );

  const content = (
    <LayoutContent ref={contentRef} padding={isTable ? 4 : 0} isScrollable={!isTable || (Boolean(item) && !isTwoPane)}>
      {item && !isTwoPane ? (
        detail
      ) : (
        <VStack gap={4} paddingBlockEnd={isTable ? 0 : 6} height={isTable ? '100%' : undefined}>
          {(page.error || servers.error) && (
            <VStack padding={4}>
              <Banner
                status="error"
                title="The items are unavailable"
                description={(page.error ?? servers.error)?.message}
                endContent={
                  <Button
                    label="Try again"
                    onClick={() => {
                      if (page.error) void page.refetch();
                      if (servers.error) void servers.refetch();
                    }}
                  />
                }
              />
            </VStack>
          )}
          {page.isPending ? (
            <ListSkeleton />
          ) : rows.length === 0 ? (
            !page.error && <NoItems tab={tab} isScanned={scanned || !inventory.length} isFiltered={Boolean(scope.q || scope.reason || scope.type || scope.library)} />
          ) : (
            list
          )}
          {page.hasNextPage && (
            <HStack hAlign="center" gap={3} vAlign="center">
              <Text type="supporting" color="secondary">
                Showing {rows.length.toLocaleString()} of {(counts?.[tab] ?? rows.length).toLocaleString()}
              </Text>
              <Button
                label="Load more"
                isLoading={page.isFetchingNextPage}
                isDisabled={page.isFetching}
                onClick={() => {
                  if (!page.isFetching) void page.fetchNextPage({ cancelRefetch: false });
                }}
              />
            </HStack>
          )}
        </VStack>
      )}
    </LayoutContent>
  );

  return (
    <>
      <Layout height="fill" header={item && !isTwoPane ? undefined : header} content={content} end={mode === 'list' && isTwoPane ? <DetailPane detail={detail} /> : undefined} />
      {mode === 'table' && isTwoPane && <ItemDrawer item={item} server={server} ignored={ignored} onClose={close} />}
      {bulkAction.confirmElement}
    </>
  );
}

/** The item details of table mode in a drawer. It keeps the item while it
 * slides out, and drops it once the slide ends. */
function ItemDrawer({ item, server, ignored, onClose }: { item?: ItemKey; server: (key: ItemKey) => ServerInfo | undefined; ignored: string[]; onClose: () => void }) {
  const drawer = useShown(item, itemID, onClose);
  const shown = drawer.shown;
  return (
    <Drawer key={drawer.generation} isOpen={drawer.isOpen} onOpenChange={open => !open && onClose()} ref={drawer.ref} label="Item details" width={600} padding={0}>
      {shown ? <ItemDetail key={itemID(shown)} target={shown} server={server(shown)} ignored={ignored} onClose={onClose} /> : null}
    </Drawer>
  );
}

function DetailPane({ detail }: { detail?: React.ReactNode }) {
  const pane = useResizable({ defaultSize: '55%', minSize: 440, maxSize: 900, autoSaveId: 'autorefresh.items.pane' });
  return (
    <>
      <ResizeHandle direction="horizontal" isReversed hasDivider pillPlacement="end" resizable={pane.props} label="Resize item details" />
      <LayoutPanel resizable={pane.props} padding={0} role="complementary" label="Item details">
        {detail ?? (
          <VStack padding={10}>
            <EmptyState title="No item open" description="Choose an item in the list to see its details and actions." icon={<Icon icon={InboxIcon} size="lg" />} />
          </VStack>
        )}
      </LayoutPanel>
    </>
  );
}

function ListSkeleton() {
  return (
    <VStack gap={3} padding={4}>
      {[0, 1, 2, 3, 4].map(i => (
        <Skeleton key={i} index={i} height={66} />
      ))}
    </VStack>
  );
}

function NoItems({ tab, isScanned, isFiltered }: { tab: TabName; isScanned: boolean; isFiltered: boolean }) {
  if (!isScanned) {
    return (
      <VStack padding={6}>
        <EmptyState title="Waiting for the first scan" description="Items show up here after autorefresh reads the libraries of this server for the first time." icon={<Icon icon={InboxIcon} size="lg" />} />
      </VStack>
    );
  }
  if (isFiltered) return <EmptyState title="No items match" description="Change the search or the filters to see more items." />;
  const text: Record<TabName, [string, string]> = {
    attention: ['Nothing needs attention', 'Every item that autorefresh knows has complete metadata, or waits for a reason that you ignore.'],
    fixed: ['Nothing fixed yet', 'Items that become complete after a request show up here.'],
    stopped: ['No stopped items', 'Items that you stop trying show up here, so you can resume them.'],
  };
  return (
    <VStack padding={6}>
      <EmptyState title={text[tab][0]} description={text[tab][1]} icon={<Icon icon={InboxIcon} size="lg" />} />
    </VStack>
  );
}

interface HeaderProps {
  tab: TabName;
  counts?: ItemPage['counts'];
  mode: Mode;
  onMode: (mode: string) => void;
  /** The server scope of the route, which the tabs keep. */
  srv?: number;
  /** The server in scope, whose libraries can be filtered. */
  server?: ServerInfo;
  filters: Filters;
  /** Gets only the filters that changed. */
  onFilters: (change: Filters) => void;
  search: string;
  /** Whether search is too long for the API, so the list ignores it. */
  isSearchInvalid: boolean;
  onSearch: (value: string) => void;
  /** The sort of the tab, as the API reads it. Table mode sorts by its
   * column headers instead. */
  sort: string;
  onSort: (sort: string) => void;
  bulk?: React.ReactNode;
}

function ItemsHeader({ tab, counts, mode, onMode, srv, server, filters, onFilters, search, isSearchInvalid, onSearch, sort, onSort, bulk }: HeaderProps) {
  const set = onFilters;
  const optional = (value: string | null) => value || undefined;
  return (
    <LayoutHeader padding={0} hasDivider={false}>
      <VStack gap={3} paddingInline={4} paddingBlockStart={4}>
        <HStack gap={3} vAlign="center" hAlign="between" wrap="wrap">
          <Heading level={1}>Items</Heading>
          <SegmentedControl label="View" value={mode} onChange={onMode}>
            <SegmentedControlItem value="list" label="List" />
            <SegmentedControlItem value="table" label="Table" />
          </SegmentedControl>
        </HStack>
        <TextInput
          label="Search"
          isLabelHidden
          type="search"
          placeholder="Search title or path"
          startIcon="search"
          hasClear
          value={search}
          onChange={onSearch}
          status={isSearchInvalid ? { type: 'error', message: `The search is too long. Use at most ${maxSearchBytes} bytes; letters with accents and other scripts take more than one.` } : undefined}
        />
        <HStack gap={2} vAlign="center" wrap="wrap">
          {server && server.libraries.length > 1 && (
            <Selector
              label="Library"
              isLabelHidden
              variant="secondary"
              value={filters.library ?? ''}
              options={[{ value: '', label: 'All libraries' }, ...server.libraries.map(entry => ({ value: entry.section, label: entry.title || entry.section }))]}
              onChange={value => set({ library: optional(value) })}
            />
          )}
          {tab !== 'fixed' && (
            <Selector
              label="Reason"
              isLabelHidden
              variant="secondary"
              value={filters.reason ?? ''}
              options={[{ ...anyOption, label: 'Any reason' }, ...reasonOptions.map(value => ({ value, label: label(value) }))]}
              onChange={value => set({ reason: optional(value) })}
            />
          )}
          <Selector
            label="Type"
            isLabelHidden
            variant="secondary"
            value={filters.type ?? ''}
            options={[{ ...anyOption, label: 'Movies and series' }, { value: 'movie', label: 'Movies' }, { value: 'show', label: 'Series' }]}
            onChange={value => set({ type: optional(value) })}
          />
          {mode === 'list' && <SortControl tab={tab} sort={sort} onSort={onSort} />}
          {tab !== 'stopped' && (
            <Switch
              label={tab === 'fixed' ? 'Include fixed by Plex alone' : 'Include ignored reasons'}
              size="sm"
              value={Boolean(filters.all)}
              onChange={value => set({ all: value || undefined })}
            />
          )}
        </HStack>
      </VStack>
      {bulk ?? (
        <VStack paddingInline={3} paddingBlockStart={2}>
          <TabList hasDivider value={tab} onChange={value => navigate({ page: 'items', tab: value as TabName, srv })} aria-label="Item state">
            {tabNames.map(name => (
              <Tab key={name} value={name} label={tabTitles[name]} endContent={counts ? <Badge label={counts[name].toLocaleString()} /> : undefined} />
            ))}
          </TabList>
        </VStack>
      )}
    </LayoutHeader>
  );
}

/** The sort of list mode: a sort key of the tab and its direction. */
function SortControl({ tab, sort, onSort }: { tab: TabName; sort: string; onSort: (sort: string) => void }) {
  const { key, desc } = parseSort(sort);
  const direction = desc ? 'Descending, change to ascending' : 'Ascending, change to descending';
  return (
    <HStack gap={1} vAlign="center">
      <Selector
        label="Sort"
        isLabelHidden
        variant="secondary"
        value={key}
        // Short option labels keep the menu's typeahead useful; the closed
        // control says what the value means.
        options={[{ value: '', label: 'Default order' }, ...sortKeys[tab].map(entry => ({ value: entry.key, label: entry.label }))]}
        renderValue={option => (option.value ? `Sort by ${(option.label ?? option.value).toLowerCase()}` : option.label)}
        onChange={value => onSort(chooseSort(tab, sort, value ?? ''))}
      />
      {key && (
        <IconButton
          label={direction}
          tooltip={direction}
          icon={<Icon icon={desc ? BarsArrowDownIcon : BarsArrowUpIcon} size="sm" />}
          variant="ghost"
          size="sm"
          onClick={() => onSort(formatSort(key, !desc))}
        />
      )}
    </HStack>
  );
}

interface ListProps {
  tab: TabName;
  rows: ItemRecord[];
  isMulti: boolean;
  ignored: string[];
  openItem?: ItemKey;
  /** The server scope of the route, which item links keep. */
  srv?: number;
}

function rowState(item: ItemRecord, tab: TabName, now: number): string {
  if (tab === 'fixed') return `${fixedBy(item.fixedAction, item.fixedMatch)} · ${relative(item.completedAt, now)}`;
  if (tab === 'stopped') return `Stopped ${relative(item.stoppedAt, now)}`;
  return queueState(item.state, item.nextDue, now);
}

function ItemList({ tab, rows, isMulti, ignored, openItem, srv }: ListProps) {
  const now = useNow();
  const open = openItem && itemID(openItem);
  return (
    <List density="balanced" hasDividers edge="padded">
      {rows.map(item => (
        <ListItem
          key={itemID(item)}
          itemKey={itemID(item)}
          href={itemHash(tab, item, srv)}
          isSelected={itemID(item) === open}
          align="start"
          startContent={<Poster src={item.thumb} title={item.title} year={item.year} type={item.type} size="md" />}
          label={item.title ?? `Item ${item.key}`}
          description={
            <VStack gap={1.5}>
              <Text type="supporting" color="secondary">
                {[item.year, isMulti && item.server, item.library, rowState(item, tab, now)].filter(Boolean).join(' · ')}
              </Text>
              <Reasons reasons={item.reasons} ignored={ignored} />
            </VStack>
          }
          endContent={tab === 'attention' && item.attempts ? <Text type="supporting" color="secondary">{plural(item.attempts, 'try', 'tries')}</Text> : undefined}
        />
      ))}
    </List>
  );
}
