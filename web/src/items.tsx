import { useImperativeAlertDialog } from '@astryxdesign/core/AlertDialog';
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
import { Section } from '@astryxdesign/core/Section';
import { SegmentedControl, SegmentedControlItem } from '@astryxdesign/core/SegmentedControl';
import { Selector } from '@astryxdesign/core/Selector';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { Switch } from '@astryxdesign/core/Switch';
import { Tab, TabList } from '@astryxdesign/core/TabList';
import { Table, pixel, proportional, useTableRowsVirtualization, useTableSelection, useTableSortable, type TableColumn, type TablePlugin } from '@astryxdesign/core/Table';
import { Text } from '@astryxdesign/core/Text';
import { TextInput } from '@astryxdesign/core/TextInput';
import { useToast } from '@astryxdesign/core/Toast';
import { colorVars } from '@astryxdesign/core/theme/tokens.stylex';
import { BarsArrowDownIcon, BarsArrowUpIcon, InboxIcon } from '@heroicons/react/24/outline';
import * as stylex from '@stylexjs/stylex';
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect, useMemo, useState } from 'react';
import { api, invalidateLists, itemID, useServers, type ItemPage, type ItemRecord, type ServerInfo } from './api';
import { bulkError, bulkRequests, isSearchTooLong, listParams, maxSearchBytes, stopOrResume, tabActions, type BulkAction, type BulkRequest, type Filters, type Selection } from './bulk';
import { useNow } from './clock';
import { askThen } from './confirm';
import { ItemDetail } from './detail';
import { fixedBy, label, plural, queueState, relative } from './format';
import { Poster, Reasons } from './media';
import { NothingMatched, startBulkRefresh, useIsRefreshBusy } from './queue';
import { itemHash, navigate, tabNames, type ItemKey, type TabName } from './route';
import { formatSort, parseSort, sortKeys, stableRows, tableRow, type TableRow } from './table';

const tabTitles: Record<TabName, string> = { attention: 'Attention', fixed: 'Fixed', stopped: 'Stopped' };
const reasonOptions = ['unmatched', 'no-summary', 'no-poster', 'no-art', 'episode-no-summary', 'episode-unmatched'];
const actionLabels: Record<BulkAction, string> = { refresh: 'Refresh', stop: 'Stop trying', resume: 'Resume' };
const anyOption = { value: '', label: 'Any' };
const noReasons: string[] = [];

// Below this width the detail replaces the list instead of sitting beside it.
const twoPaneQuery = '(min-width: 1000px)';

type Mode = 'list' | 'table';

// The pitch of a spacious single-line table row, measured in the browser.
// Astryx windows at the measured pitch when rows grow, as with zoom.
const tableRowHeight = 57;

const styles = stylex.create({
  // Astryx 0.7.17 has no sticky table header. The windowed table scrolls
  // inside its own area, so the header (sort and select all) stays on top.
  stickyHeader: { position: 'sticky', insetBlockStart: 0, zIndex: 1, backgroundColor: colorVars['--color-background-surface'] },
});

const stickyHeader: TablePlugin<TableRow> = {
  transformHeaderCell: props => ({ ...props, xstyle: [...props.xstyle, styles.stickyHeader] }),
};

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
  // A single server is always the scope, so its libraries can be filtered.
  const scopeSrv = srv ?? (inventory.length === 1 ? 0 : undefined);
  const scope: Filters = {
    type: filters.type,
    // The Fixed tab has no reason filter. The Attention filter stays for
    // when the user returns.
    reason: tab === 'fixed' ? undefined : filters.reason,
    all: all[tab],
    q,
    srv: scopeSrv,
    library: library && library.srv === scopeSrv ? library.section : undefined,
  };
  const params = listParams(tab, scope, sort).toString();
  // A selection belongs to a tab and its filters. Sorting keeps it.
  const selectionScope = listParams(tab, scope, '').toString();
  const scanned = inventory.filter(entry => scope.srv === undefined || entry.srv === scope.srv).some(entry => entry.scanned);

  const [selection, setSelection] = useState<{ scope: string; value: Selection }>();
  const selected = mode === 'table' && selection?.scope === selectionScope ? selection.value : undefined;
  const select = (value?: Selection) => setSelection(value && { scope: selectionScope, value });

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

  const header = (
    <ItemsHeader
      tab={tab}
      counts={counts}
      mode={mode}
      onMode={changeMode}
      srv={srv}
      server={inventory.find(entry => entry.srv === scopeSrv)}
      filters={scope}
      onFilters={change => {
        const { library: section, all: value, ...rest } = change;
        if ('library' in change) setLibrary(section !== undefined && scopeSrv !== undefined ? { srv: scopeSrv, section } : undefined);
        if ('all' in change) setAll(previous => ({ ...previous, [tab]: value }));
        setFilters(previous => ({ ...previous, ...rest }));
      }}
      search={search}
      isSearchInvalid={isSearchInvalid}
      onSearch={changeSearch}
      sort={sort}
      onSort={value => setSorts({ ...sorts, [tab]: value })}
      bulk={
        selected && (
          <BulkBand
            tab={tab}
            scope={scope}
            selection={selected}
            total={counts?.[tab] ?? 0}
            inventory={inventory}
            isInventoryKnown={servers.data !== undefined}
            onClear={() => select(undefined)}
            // A bulk action clears the selection that it took, but not a
            // newer one that the user made while it ran.
            onDone={submitted => setSelection(current => (current?.value === submitted ? undefined : current))}
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
        onSort={value => setSorts({ ...sorts, [tab]: value })}
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
    <LayoutContent padding={isTable ? 4 : 0} isScrollable={!isTable || (Boolean(item) && !isTwoPane)}>
      {item && !isTwoPane ? (
        detail
      ) : (
        <VStack gap={4} paddingBlockEnd={isTable ? 0 : 6} height={isTable ? '100%' : undefined}>
          {(page.error || servers.error) && (
            <VStack padding={4}>
              <Banner status="error" title="The items are unavailable" description={(page.error ?? servers.error)?.message} />
            </VStack>
          )}
          {page.isPending ? <ListSkeleton /> : rows.length === 0 && !page.error ? <NoItems tab={tab} isScanned={scanned || !inventory.length} isFiltered={Boolean(scope.q || scope.reason || scope.type || scope.library)} /> : list}
          {page.hasNextPage && (
            <HStack hAlign="center" gap={3} vAlign="center">
              <Text type="supporting" color="secondary">
                Showing {rows.length.toLocaleString()} of {(counts?.[tab] ?? rows.length).toLocaleString()}
              </Text>
              <Button label="Load more" isLoading={page.isFetchingNextPage} onClick={() => void page.fetchNextPage()} />
            </HStack>
          )}
        </VStack>
      )}
    </LayoutContent>
  );

  return (
    <>
      <Layout height="fill" header={item && !isTwoPane ? undefined : header} content={content} end={mode === 'list' && isTwoPane ? <DetailPane detail={detail} /> : undefined} />
      {mode === 'table' && isTwoPane && (
        <Drawer isOpen={Boolean(item)} onOpenChange={open => !open && close()} label="Item details" width={600} padding={0}>
          {detail ?? null}
        </Drawer>
      )}
    </>
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
        <HStack gap={3} vAlign="end" wrap="wrap">
          <StackItem size="fill">
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
          </StackItem>
          <SegmentedControl label="View" value={mode} onChange={onMode}>
            <SegmentedControlItem value="list" label="List" />
            <SegmentedControlItem value="table" label="Table" />
          </SegmentedControl>
        </HStack>
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

/** The sort of list mode: a sort key of the tab and its direction. A new
 * key starts in its natural direction, such as the most tries first. */
function SortControl({ tab, sort, onSort }: { tab: TabName; sort: string; onSort: (sort: string) => void }) {
  const { key, desc } = parseSort(sort);
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
        onChange={value => onSort(formatSort(value ?? '', sortKeys[tab].find(entry => entry.key === value)?.desc ?? false))}
      />
      {key && (
        <IconButton
          label={desc ? 'Descending, change to ascending' : 'Ascending, change to descending'}
          tooltip={desc ? 'Descending' : 'Ascending'}
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

interface TableProps extends ListProps {
  sort: string;
  onSort: (sort: string) => void;
  selection?: Selection;
  onSelect: (selection?: Selection) => void;
}

/** The Space, Enter, or click on a control inside a row, such as the
 * selection checkbox, belongs to that control, not to the row. */
function isRowItself(event: { target: EventTarget | null; currentTarget: EventTarget | null }): boolean {
  const control = (event.target as HTMLElement | null)?.closest('button, input, a, label, [role="checkbox"], [role="button"]');
  const row = event.currentTarget as HTMLElement;
  return !control || control === row || !row.contains(control);
}

/** Opens the detail of a row on click, Enter, or Space, as the Astryx
 * table-inbox template does. A table row cannot hold a link here: a cell
 * renderer stops the windowing. The plugin keeps the flat row model, which
 * windowing requires. */
function useRowOpen(onOpen: (row: TableRow) => void, openID?: string): TablePlugin<TableRow> {
  return useMemo(
    () => ({
      rowModel: 'flat',
      transformBodyRow: (props, row) => ({
        ...props,
        htmlProps: {
          ...props.htmlProps,
          tabIndex: 0,
          'aria-current': row.id === openID ? true : undefined,
          onClick: event => {
            if (isRowItself(event)) onOpen(row);
          },
          onKeyDown: event => {
            if ((event.key !== 'Enter' && event.key !== ' ') || !isRowItself(event)) return;
            event.preventDefault();
            onOpen(row);
          },
        },
      }),
    }),
    [onOpen, openID],
  );
}

function ItemTable({ tab, rows, isMulti, ignored, sort, onSort, selection, onSelect, openItem, srv }: TableProps) {
  const now = useNow();
  const built = useMemo(() => rows.map(item => tableRow(item, now, isMulti, ignored)), [rows, now, isMulti, ignored]);
  // A clock tick that changes no cell keeps the data, so the windowed
  // table does not move.
  const [data, setData] = useState(built);
  const next = stableRows(data, built);
  if (next !== data) setData(next);
  const keys = new Set(selection?.kind === 'keys' ? selection.items.map(itemID) : []);
  const isMatching = selection?.kind === 'matching';
  const virtualization = useTableRowsVirtualization({ rowHeight: tableRowHeight, initialViewportHeight: 640 });
  const open = useRowOpen(
    useMemo(() => (row: TableRow) => navigate({ page: 'items', tab, item: { srv: row.srv, key: row.key }, srv }), [tab, srv]),
    openItem && itemID(openItem),
  );

  const selectionPlugin = useTableSelection<TableRow>({
    getRowLabel: row => row.title,
    getIsItemSelected: row => isMatching || keys.has(row.id),
    // After "select all", rows stay checked until the selection is cleared,
    // because the API selects by filter and cannot leave rows out.
    getIsItemEnabled: () => !isMatching,
    onSelectItem: ({ item: row, isSelected }) => {
      const items = selection?.kind === 'keys' ? selection.items : [];
      const next = isSelected ? [...items, { srv: row.srv, key: row.key }] : items.filter(key => itemID(key) !== row.id);
      onSelect(next.length ? { kind: 'keys', items: next } : undefined);
    },
    onSelectAll: ({ isAllSelected }) => onSelect(isAllSelected ? { kind: 'matching' } : undefined),
    getIsAllSelected: () => isMatching,
    getIsIndeterminate: () => keys.size > 0,
  });
  const sortPlugin = useTableSortable<TableRow>({
    sort: sort ? [{ sortKey: parseSort(sort).key, direction: parseSort(sort).desc ? 'descending' : 'ascending' }] : [],
    onSortChange: next => onSort(next[0] ? formatSort(next[0].sortKey, next[0].direction === 'descending') : ''),
  });

  // No column has a renderCell: Astryx windows only plain text cells.
  const isSortable = (key: string) => sortKeys[tab].some(entry => entry.key === key);
  const title: TableColumn<TableRow> = { key: 'title', header: 'Item', sortable: isSortable('title'), width: proportional(2.4, { minWidth: 200 }) };
  const kind: TableColumn<TableRow> = { key: 'kind', header: 'Type', width: proportional(0.9, { minWidth: 110 }) };
  const library: TableColumn<TableRow> = { key: 'library', header: isMulti ? 'Server · library' : 'Library', width: proportional(1.2, { minWidth: 120 }) };
  const reasons: TableColumn<TableRow> = { key: 'reasons', header: 'Reasons', width: proportional(1.6, { minWidth: 160 }) };
  const tries: TableColumn<TableRow> = { key: 'tries', header: 'Tries', sortable: isSortable('tries'), align: 'end', width: pixel(88) };
  const byTab: Record<TabName, TableColumn<TableRow>[]> = {
    attention: [
      title,
      kind,
      library,
      reasons,
      tries,
      { key: 'last-attempt', header: 'Last action', sortable: isSortable('last-attempt'), width: proportional(1.2, { minWidth: 130 }) },
      { key: 'due', header: 'Next due', sortable: isSortable('due'), width: proportional(1, { minWidth: 120 }) },
    ],
    stopped: [title, kind, library, reasons, tries, { key: 'stopped', header: 'Stopped', sortable: isSortable('stopped'), width: proportional(1, { minWidth: 120 }) }],
    fixed: [
      title,
      kind,
      library,
      { key: 'fixed-by', header: 'Fixed by', width: proportional(1.4, { minWidth: 140 }) },
      { key: 'completed', header: 'Completed', sortable: isSortable('completed'), width: proportional(1, { minWidth: 120 }) },
    ],
  };
  const plugins: Record<string, TablePlugin<TableRow>> = tabActions[tab].length
    ? { sort: sortPlugin, selection: selectionPlugin, open, stickyHeader }
    : { sort: sortPlugin, open, stickyHeader };
  return (
    <Table<TableRow>
      data={data}
      columns={byTab[tab]}
      idKey="id"
      density="spacious"
      textOverflow="truncate"
      hasHover
      plugins={plugins}
      virtualization={virtualization}
      emptyState={false}
    />
  );
}

interface BulkProps {
  tab: TabName;
  scope: Filters;
  selection: Selection;
  total: number;
  inventory: ServerInfo[];
  /** Whether the servers are loaded. A matching selection waits for them:
   * it sends one request for each server, to a server that they validate. */
  isInventoryKnown: boolean;
  onClear: () => void;
  /** Gets the selection of a bulk action that succeeded. */
  onDone: (submitted: Selection) => void;
}

/** A bulk action as the user submitted it. It keeps the requests, the
 * server names, and the selection of that moment, so a selection change
 * while the confirmation is open or the request waits changes nothing. */
interface Submitted {
  action: BulkAction;
  requests: BulkRequest[];
  names: Record<number, string>;
  selection: Selection;
}

/** The band that replaces the tabs while rows are selected. */
function BulkBand({ tab, scope, selection, total, inventory, isInventoryKnown, onClear, onDone }: BulkProps) {
  const client = useQueryClient();
  const showToast = useToast();
  const confirm = useImperativeAlertDialog();
  const isRefreshBusy = useIsRefreshBusy();
  const count = selection.kind === 'matching' ? total : selection.items.length;
  const requestsOf = (action: BulkAction) => bulkRequests(action, tab, scope, selection, inventory.map(entry => entry.srv));
  const run = useMutation({
    mutationFn: async ({ action, requests, names }: Submitted) => {
      if (action !== 'refresh') return stopOrResume(action, requests, names);
      const started = await startBulkRefresh(client, requests, names).catch((error: unknown) => {
        if (error instanceof NothingMatched) return undefined;
        throw error;
      });
      if (!started) return { text: 'No items match the filters.', isError: false };
      const first = started.steps.find(step => step.state === 'running')!;
      const rest = started.steps.filter(step => step.state === 'waiting');
      const next = rest.length ? ` on ${first.server}. Then ${rest.map(step => step.server).join(', ')}, one server at a time` : started.steps.length > 1 ? ` on ${first.server}` : '';
      const text = `Bulk refresh started for ${plural(first.count ?? 0, 'item')}${next}. Progress shows on the Status page.`;
      return { text, isError: false };
    },
    onSuccess: ({ text, isError }, submitted) => {
      showToast({ body: text, type: isError ? 'error' : 'info' });
      onDone(submitted.selection);
    },
    onError: error => showToast({ body: bulkError(error), type: 'error' }),
    onSettled: () => invalidateLists(client),
  });

  const servers = requestsOf('refresh').length;
  const start = (action: BulkAction) => {
    const submitted: Submitted = { action, requests: requestsOf(action), names: Object.fromEntries(inventory.map(entry => [entry.srv, entry.name])), selection };
    if (action === 'resume') {
      run.mutate(submitted);
      return;
    }
    const items = plural(count, 'item');
    const order = servers > 1 ? ` The ${plural(servers, 'server')} take turns: each server starts after the previous one ends, and a cancel stops the rest.` : '';
    askThen(
      confirm,
      action === 'refresh'
        ? {
            title: `Refresh ${items}?`,
            description: `Autorefresh sends ${plural(count, 'refresh request')} to Plex in the background, one at a time with the configured refresh delay.${order} Progress shows on the Status page.`,
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
              isLoading={run.isPending && run.variables?.action === action}
              isDisabled={count === 0 || (run.isPending && run.variables?.action !== action) || disabledReason(action) !== undefined}
              tooltip={disabledReason(action)}
              onClick={() => start(action)}
            />
          ))}
          <Button label="Clear selection" variant="ghost" size="md" onClick={onClear} />
        </HStack>
      </Section>
      {confirm.element}
    </VStack>
  );
}
