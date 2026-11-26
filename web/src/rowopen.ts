import type { TablePlugin } from '@astryxdesign/core/Table';
import { useMemo } from 'react';

/** Tells whether an event came from the row itself rather than from a
 * control inside it, such as a checkbox or a link. */
function isRowItself(event: { target: EventTarget | null; currentTarget: EventTarget | null }): boolean {
  const control = (event.target as HTMLElement | null)?.closest('button, input, a, label, [role="checkbox"], [role="button"]');
  const row = event.currentTarget as HTMLElement;
  return !control || control === row || !row.contains(control);
}

/** Opens the detail of a row on click, Enter, or Space, as the Astryx
 * table-inbox template does. A table row cannot hold a link here: a cell
 * renderer stops the windowing. The plugin keeps the flat row model, which
 * windowing requires. openID marks the open row as current. */
export function useRowOpen<Row extends Record<string, unknown> & { id: string | number }>(onOpen: (row: Row) => void, openID?: string | number): TablePlugin<Row> {
  return useMemo(
    () => ({
      rowModel: 'flat',
      transformBodyRow: (props, row) => {
        const prevClick = props.htmlProps?.onClick;
        const prevKeyDown = props.htmlProps?.onKeyDown;
        return {
          ...props,
          htmlProps: {
            ...props.htmlProps,
            tabIndex: 0,
            'aria-current': row.id === openID ? true : undefined,
            onClick: event => {
              prevClick?.(event);
              if (event.defaultPrevented) return;
              if (isRowItself(event)) onOpen(row);
            },
            onKeyDown: event => {
              prevKeyDown?.(event);
              if (event.defaultPrevented) return;
              if ((event.key !== 'Enter' && event.key !== ' ') || !isRowItself(event)) return;
              event.preventDefault();
              onOpen(row);
            },
          },
        };
      },
    }),
    [onOpen, openID],
  );
}
