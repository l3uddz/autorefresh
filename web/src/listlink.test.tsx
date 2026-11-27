import { List } from '@astryxdesign/core/List';
import { cloneElement, isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LinkListItem, type LinkListItemProps } from './listlink';

class FakeElement {
  parentElement: FakeElement | null = null;
  private readonly selectorMatches: Set<string>;

  constructor(selectorMatches: string[] = []) {
    this.selectorMatches = new Set(selectorMatches);
  }

  matches(selector: string): boolean {
    return selector.split(',').some(part => this.selectorMatches.has(part.trim()));
  }

  closest(selector: string): FakeElement | null {
    if (this.matches(selector)) return this;
    return this.parentElement?.closest(selector) ?? null;
  }

  hasAttribute(): boolean {
    return false;
  }

  contains(node: unknown): boolean {
    let current = node as FakeElement | null;
    while (current) {
      if (current === this) return true;
      current = current.parentElement;
    }
    return false;
  }

  append(child: FakeElement): FakeElement {
    child.parentElement = this;
    return child;
  }
}

interface CapturedRowProps {
  onClick?: (event: unknown) => void;
  onMouseDown?: (event: unknown) => void;
  onMouseLeave?: () => void;
  onMouseUp?: (event: unknown) => void;
}

/** Walks rendered React elements inside renderToStaticMarkup so List, ListItem,
 * and Item execute with real React hooks and contexts, capturing the real <li>
 * event handlers that Astryx attaches. */
function wrapNode(node: ReactNode, onLi: (props: CapturedRowProps) => void): ReactNode {
  if (Array.isArray(node)) return node.map((child, index) => <Probe key={index} node={child} onLi={onLi} />);
  if (!isValidElement(node)) return node;
  if (typeof node.type === 'function') return <Probe node={node} onLi={onLi} />;
  const props = node.props as { children?: ReactNode };
  if (node.type === 'li') onLi(props as CapturedRowProps);
  if (props.children === undefined) return node;
  return cloneElement(node as ReactElement<{ children?: ReactNode }>, undefined, wrapNode(props.children, onLi));
}

function Probe({ node, onLi }: { node: ReactNode; onLi: (props: CapturedRowProps) => void }): ReactNode {
  if (!isValidElement(node) || typeof node.type !== 'function') return wrapNode(node, onLi);
  const rendered = (node.type as (props: unknown) => ReactNode)(node.props);
  return wrapNode(rendered, onLi);
}

function renderRow(props: LinkListItemProps): { html: string; li: CapturedRowProps } {
  let li: CapturedRowProps | undefined;
  const html = renderToStaticMarkup(
    <Probe
      node={
        <List>
          <LinkListItem {...props} />
        </List>
      }
      onLi={captured => {
        li = captured;
      }}
    />,
  );
  if (!li) throw new Error('Expected LinkListItem to render a list item element');
  return { html, li };
}

function makeEvent(overrides: Record<string, unknown> = {}) {
  return {
    button: 0,
    detail: 1,
    altKey: false,
    ctrlKey: false,
    metaKey: false,
    shiftKey: false,
    defaultPrevented: false,
    preventDefault() {
      this.defaultPrevented = true;
    },
    ...overrides,
  };
}

describe('LinkListItem', () => {
  const openSpy = vi.fn();
  const assignSpy = vi.fn();
  let selection: { isCollapsed: boolean; anchorNode: unknown } | null = null;
  let body: FakeElement;
  let locationMock: { hash: string; assign: typeof assignSpy };

  beforeEach(() => {
    openSpy.mockReset();
    assignSpy.mockReset();
    selection = null;
    body = new FakeElement();
    locationMock = { hash: '#/items/fixed', assign: assignSpy };
    vi.stubGlobal('Element', FakeElement);
    vi.stubGlobal('document', { body, getSelection: () => selection });
    vi.stubGlobal('window', { location: locationMock, open: openSpy });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders a native anchor inside the list item and attaches surface handlers on the row element', () => {
    const { html, li } = renderRow({ href: '#/items/fixed/0/42', label: 'Shaun the Sheep', isSelected: true });
    expect(html).toMatch(/<a\b/);
    expect(html).toContain('href="#/items/fixed/0/42"');
    expect(html).toContain('aria-current="true"');
    expect(html).not.toMatch(/<button\b/);
    expect(typeof li.onClick).toBe('function');
    expect(typeof li.onMouseDown).toBe('function');
    expect(typeof li.onMouseUp).toBe('function');
  });

  it('navigates through Astryx Item on plain click on the row surface or poster', () => {
    const { li } = renderRow({ href: '#/items/fixed/0/42', label: 'Shaun the Sheep' });
    const row = body.append(new FakeElement());
    const poster = row.append(new FakeElement());

    li.onClick?.(makeEvent({ currentTarget: row, target: poster }));
    expect(locationMock.hash).toBe('/items/fixed/0/42');
    expect(openSpy).not.toHaveBeenCalled();
  });

  it.each<[string, Record<string, unknown>, Partial<LinkListItemProps>]>([
    ['ctrlKey', { ctrlKey: true }, {}],
    ['metaKey', { metaKey: true }, {}],
    ['shiftKey', { shiftKey: true }, {}],
    ['target _blank', {}, { target: '_blank' }],
  ])('opens in a new tab on %s from the row surface', (_name, eventOverrides, rowOverrides) => {
    const { li } = renderRow({ href: '#/items/fixed/0/42', label: 'Shaun the Sheep', ...rowOverrides });
    const row = body.append(new FakeElement());

    li.onClick?.(makeEvent({ currentTarget: row, target: row, ...eventOverrides }));
    expect(openSpy).toHaveBeenCalledWith('#/items/fixed/0/42', '_blank', 'noopener');
    expect(locationMock.hash).toBe('#/items/fixed');
  });

  it('opens in a new tab on middle-click on the row surface and prevents autoscroll', () => {
    const { li } = renderRow({ href: '#/runs/7', label: 'Scheduled run' });
    const row = body.append(new FakeElement());

    const down = makeEvent({ button: 1, currentTarget: row, target: row });
    li.onMouseDown?.(down);
    expect(down.defaultPrevented).toBe(true);

    const up = makeEvent({ button: 1, currentTarget: row, target: row });
    li.onMouseUp?.(up);
    expect(up.defaultPrevented).toBe(true);
    expect(openSpy).toHaveBeenCalledWith('#/runs/7', '_blank', 'noopener');
    expect(locationMock.hash).toBe('#/items/fixed');
  });

  it('ignores middle-button release when press started outside the row, on a nested control, or left the row', () => {
    const { li } = renderRow({ href: '#/runs/7', label: 'Scheduled run' });
    const row = body.append(new FakeElement());
    const anchor = row.append(new FakeElement(['a']));

    // Release without a prior press on this row
    const releaseOnly = makeEvent({ button: 1, currentTarget: row, target: row });
    li.onMouseUp?.(releaseOnly);
    expect(releaseOnly.defaultPrevented).toBe(false);

    // Press on inner anchor, then release on row surface
    li.onMouseDown?.(makeEvent({ button: 1, currentTarget: row, target: anchor }));
    const releaseAfterAnchor = makeEvent({ button: 1, currentTarget: row, target: row });
    li.onMouseUp?.(releaseAfterAnchor);
    expect(releaseAfterAnchor.defaultPrevented).toBe(false);

    // Press on row surface, leave the row, then release
    li.onMouseDown?.(makeEvent({ button: 1, currentTarget: row, target: row }));
    li.onMouseLeave?.();
    const releaseAfterLeave = makeEvent({ button: 1, currentTarget: row, target: row });
    li.onMouseUp?.(releaseAfterLeave);
    expect(releaseAfterLeave.defaultPrevented).toBe(false);

    expect(openSpy).not.toHaveBeenCalled();
    expect(locationMock.hash).toBe('#/items/fixed');
  });

  it('ignores clicks on the inner anchor or nested interactive controls to prevent double navigation', () => {
    const { li } = renderRow({ href: '#/items/fixed/0/42', label: 'Shaun the Sheep' });
    const row = body.append(new FakeElement());
    const anchor = row.append(new FakeElement(['a']));
    const textInsideAnchor = anchor.append(new FakeElement());
    const nestedButton = row.append(new FakeElement(['[role="button"]']));

    li.onClick?.(makeEvent({ currentTarget: row, target: textInsideAnchor }));
    li.onClick?.(makeEvent({ currentTarget: row, target: nestedButton }));
    const up = makeEvent({ button: 1, currentTarget: row, target: textInsideAnchor });
    li.onMouseUp?.(up);

    expect(up.defaultPrevented).toBe(false);
    expect(locationMock.hash).toBe('#/items/fixed');
    expect(openSpy).not.toHaveBeenCalled();
  });

  it('ignores surface clicks when the user selected text inside the row', () => {
    const { li } = renderRow({ href: '#/items/fixed/0/42', label: 'Shaun the Sheep' });
    const row = body.append(new FakeElement());
    const textNode = row.append(new FakeElement());
    selection = { isCollapsed: false, anchorNode: textNode };

    li.onClick?.(makeEvent({ currentTarget: row, target: row }));
    expect(locationMock.hash).toBe('#/items/fixed');
    expect(openSpy).not.toHaveBeenCalled();
  });

  it.each<[string, Partial<LinkListItemProps>, Record<string, unknown>]>([
    ['disabled row', { isDisabled: true }, {}],
    ['defaultPrevented event', {}, { defaultPrevented: true }],
    ['altKey click', {}, { altKey: true }],
    ['non-primary button', {}, { button: 2 }],
    ['unsafe javascript href', { href: 'javascript:alert(1)' }, {}],
  ])('does not navigate for %s', (_name, rowOverrides, eventOverrides) => {
    const { li } = renderRow({ href: '#/items/fixed/0/42', label: 'Shaun the Sheep', ...rowOverrides });
    const row = body.append(new FakeElement());

    li.onClick?.(makeEvent({ currentTarget: row, target: row, ...eventOverrides }));
    expect(locationMock.hash).toBe('#/items/fixed');
    expect(openSpy).not.toHaveBeenCalled();
    expect(assignSpy).not.toHaveBeenCalled();
  });
});
