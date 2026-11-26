import { Table, pixel, proportional, useTableRowsVirtualization, useTableSelection, useTableSortable, type TableColumn, type TablePlugin } from '@astryxdesign/core/Table';
import { colorVars } from '@astryxdesign/core/theme/tokens.stylex';
import * as stylex from '@stylexjs/stylex';
import { useMemo, useState } from 'react';
import { itemID, type ItemRecord } from './api';
import { tabActions, type Selection } from './bulk';
import { useNow } from './clock';
import { navigate, type ItemKey, type TabName } from './route';
import { useRowOpen } from './rowopen';
import { formatSort, parseSort, sortKeys, stableRows, tableRow, type TableRow } from './table';

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

export interface TableProps {
  tab: TabName;
  rows: ItemRecord[];
  isMulti: boolean;
  ignored: string[];
  openItem?: ItemKey;
  /** The server scope of the route, which item links keep. */
  srv?: number;
  sort: string;
  onSort: (sort: string) => void;
  selection?: Selection;
  onSelect: (selection?: Selection) => void;
}

/** The Space, Enter, or click on a control inside a row, such as the
 * selection checkbox, belongs to that control, not to the row. */
export function ItemTable({ tab, rows, isMulti, ignored, sort, onSort, selection, onSelect, openItem, srv }: TableProps) {
  const now = useNow();
  const built = useMemo(() => rows.map(item => tableRow(item, now, isMulti, ignored)), [rows, now, isMulti, ignored]);
  // A clock tick that changes no cell keeps the data, so the windowed
  // table does not move.
  const [data, setData] = useState(built);
  const next = stableRows(data, built);
  if (next !== data) setData(next);
  const keys = useMemo(() => new Set(selection?.kind === 'keys' ? selection.items.map(itemID) : []), [selection]);
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
  const sortValue = useMemo(() => {
    if (!sort) return [];
    const parsed = parseSort(sort);
    return [{ sortKey: parsed.key, direction: parsed.desc ? ('descending' as const) : ('ascending' as const) }];
  }, [sort]);
  const sortPlugin = useTableSortable<TableRow>({
    sort: sortValue,
    onSortChange: next => onSort(next[0] ? formatSort(next[0].sortKey, next[0].direction === 'descending') : ''),
  });

  // No column has a renderCell: Astryx windows only plain text cells.
  const columns = useMemo(() => {
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
    return byTab[tab];
  }, [tab, isMulti]);

  const plugins = useMemo((): Record<string, TablePlugin<TableRow>> => {
    if (tabActions[tab].length) return { sort: sortPlugin, selection: selectionPlugin, open, stickyHeader };
    return { sort: sortPlugin, open, stickyHeader };
  }, [tab, sortPlugin, selectionPlugin, open]);
  return (
    <Table<TableRow>
      data={data}
      columns={columns}
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
