import { hasInteractiveAncestor, hasTextSelection } from '@astryxdesign/core/hooks';
import { ListItem, type ListItemProps } from '@astryxdesign/core/List';
import { useRef, type MouseEvent } from 'react';

export interface LinkListItemProps
  extends Omit<ListItemProps, 'href' | 'onClick' | 'onMouseDown' | 'onMouseUp' | 'onMouseLeave' | 'interactiveRef' | 'role'> {
  href: string;
}

function isSafeHref(href: string): boolean {
  if (href.startsWith('#') || href.startsWith('./') || (href.startsWith('/') && !href.startsWith('//'))) return true;
  try {
    const protocol = new URL(href).protocol;
    return protocol === 'http:' || protocol === 'https:';
  } catch {
    return false;
  }
}

function canActivateSurface(href: string, isDisabled: boolean, event: MouseEvent): boolean {
  if (isDisabled || event.defaultPrevented || !isSafeHref(href)) return false;
  const row = event.currentTarget;
  if (!(row instanceof Element) || hasTextSelection(row)) return false;
  return !(event.target instanceof Element && event.target !== row && hasInteractiveAncestor(event.target, row));
}

function openInNewTab(href: string) {
  window.open(href, '_blank', 'noopener');
}

function navigateTo(href: string) {
  if (href.startsWith('#')) {
    window.location.hash = href.slice(1);
    return;
  }
  window.location.assign(href);
}

/** ListItem whose whole row surface opens its href on desktop and touch.
 * Astryx's ListItem wraps only label and description in its anchor and
 * ignores desktop clicks on the rest of the row when onClick is absent. */
export function LinkListItem({ href, target, isDisabled = false, ...rest }: LinkListItemProps) {
  const middlePressedRef = useRef(false);
  return (
    <ListItem
      {...rest}
      href={href}
      target={target}
      isDisabled={isDisabled}
      onClick={event => {
        if (event.button !== 0 || event.altKey || !canActivateSurface(href, isDisabled, event)) return;
        if (target === '_blank' || event.metaKey || event.ctrlKey || event.shiftKey) {
          openInNewTab(href);
          return;
        }
        navigateTo(href);
      }}
      onMouseDown={event => {
        if (event.button !== 1) return;
        const eligible = canActivateSurface(href, isDisabled, event);
        middlePressedRef.current = eligible;
        if (eligible) event.preventDefault();
      }}
      onMouseLeave={() => {
        middlePressedRef.current = false;
      }}
      onMouseUp={event => {
        if (event.button !== 1) return;
        const startedHere = middlePressedRef.current;
        middlePressedRef.current = false;
        if (!startedHere || !canActivateSurface(href, isDisabled, event)) return;
        event.preventDefault();
        openInNewTab(href);
      }}
    />
  );
}
