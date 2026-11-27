import { describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { TablePlugin } from '@astryxdesign/core/Table';
import * as stylex from '@stylexjs/stylex';
import { useRowOpen } from './rowopen';

type Row = { id: string };

const styles = stylex.create({
  priorRowStyle: { opacity: 0.5 },
  pointer: { cursor: 'pointer' },
});

function getPlugin(onOpen: (row: Row) => void, openID?: string): TablePlugin<Row> {
  let plugin!: TablePlugin<Row>;
  function Harness() {
    plugin = useRowOpen(onOpen, openID);
    return null;
  }
  renderToStaticMarkup(<Harness />);
  return plugin;
}

describe('useRowOpen', () => {
  it('adds the details cursor only to body rows and preserves existing row styles (#155)', () => {
    const plugin = getPlugin(vi.fn());
    const body = plugin.transformBodyRow!(
      { htmlProps: {}, xstyle: [styles.priorRowStyle], children: null },
      { id: '1' },
      0,
    );

    expect(stylex.props(...body.xstyle)).toEqual(stylex.props(styles.priorRowStyle, styles.pointer));
    expect(plugin.rowModel).toBe('flat');
    expect(plugin.transformHeaderRow).toBeUndefined();
    expect(plugin.transformHeaderCell).toBeUndefined();
  });

  it('chains existing row click and keydown handlers and respects defaultPrevented', () => {
    const onOpen = vi.fn();
    const prevClick = vi.fn((event: { defaultPrevented: boolean; preventDefault: () => void }) => {
      event.preventDefault();
    });
    const prevKeyDown = vi.fn();
    const plugin = getPlugin(onOpen, '1');
    const target = { closest: () => null, contains: () => true } as unknown as HTMLElement;
    const transformed = plugin.transformBodyRow!(
      { htmlProps: { onClick: prevClick as never, onKeyDown: prevKeyDown as never }, xstyle: [] } as never,
      { id: '1' },
      0,
    );

    const clickEvent = {
      target,
      currentTarget: target,
      defaultPrevented: false,
      preventDefault() {
        this.defaultPrevented = true;
      },
    };
    transformed.htmlProps?.onClick?.(clickEvent as never);
    expect(prevClick).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();

    const keyEvent = {
      key: 'Enter',
      target,
      currentTarget: target,
      defaultPrevented: false,
      preventDefault() {
        this.defaultPrevented = true;
      },
    };
    transformed.htmlProps?.onKeyDown?.(keyEvent as never);
    expect(prevKeyDown).toHaveBeenCalledTimes(1);
    expect(onOpen).toHaveBeenCalledWith({ id: '1' });
  });
});
