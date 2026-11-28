import { renderToString } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ApiError, type TimelineEvent } from './api';
import { LiveError, fixedFact, plexItemLink } from './detail';

const completed = (change: Partial<TimelineEvent> = {}): TimelineEvent => ({ id: 1, kind: 'completed', accepted: false, searches: [], ...change });

describe('merged fix', () => {
  it('names the merge in the Fixed fact', () => {
    expect(fixedFact(completed({ fixedAction: 'match', fixedMatch: 'match', mergedKey: '564849' }))).toBe('match · merged into another item');
    expect(fixedFact(completed({ fixedAction: 'refresh', fixedMatch: 'match' }))).toBe('match · then refresh');
  });

  it('shows the merge in place of the gone warning, with a link to the kept item', () => {
    const html = renderToString(<LiveError error={new ApiError(404, 'not found')} mergedKey="564849" machine="abc" />);
    expect(html).toContain('Plex merged this item into another item');
    expect(html).toContain('Open the merged item in Plex');
    expect(html).toContain(plexItemLink('abc', '564849').replaceAll('&', '&amp;'));
    expect(html).not.toContain('Plex no longer has this item');
  });

  it('shows no link without a server identity', () => {
    const html = renderToString(<LiveError error={new ApiError(404, 'not found')} mergedKey="564849" />);
    expect(html).toContain('Plex merged this item into another item');
    expect(html).not.toContain('Open the merged item in Plex');
  });

  it('keeps the gone warning after a removal', () => {
    const html = renderToString(<LiveError error={new ApiError(404, 'not found')} />);
    expect(html).toContain('Plex no longer has this item');
    expect(html).not.toContain('merged');
  });

  it('links to the item on app.plex.tv', () => {
    expect(plexItemLink('a b', '7')).toBe('https://app.plex.tv/desktop/#!/server/a%20b/details?key=%2Flibrary%2Fmetadata%2F7');
  });
});
