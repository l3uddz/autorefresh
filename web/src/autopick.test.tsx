import type { UseQueryResult } from '@tanstack/react-query';
import { renderToString } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { TimelineEvent } from './api';
import { refreshNowTooltip } from './detail';
import { AutoMatchPanel } from './fixmatch';
import { DecisionPanel } from './history';

// The texts that say where Refresh now applies the automatic fix match.
describe('texts about the automatic pick of Refresh now', () => {
  it('the preview names Refresh now for an unmatched item or a mismatched movie', () => {
    // Without a decision, the panel inside renders no live clock, which
    // server rendering cannot run. The help text does not depend on it.
    const preview = { isFetching: false, error: null } as unknown as UseQueryResult<TimelineEvent>;
    const html = renderToString(
      <AutoMatchPanel isPreview title="Automatic fix match preview" hasHeading={false} preview={preview} canRun onRun={() => undefined} />,
    );
    expect(html).toContain(
      'Autorefresh searched as a run would, but recorded nothing and sent nothing to Plex. To apply its pick, click Match on that candidate. For an unmatched item or a mismatched movie in the queue, Refresh now in the item details also applies the automatic pick.',
    );
  });

  it('the empty decision panel names Refresh now for a mismatched movie', () => {
    const html = renderToString(<DecisionPanel />);
    expect(html).toContain('It tries one for unmatched items with IDs in their folder or file names');
    expect(html).toContain('Refresh now also tries one for a mismatched movie, unless mismatched is in ignore-reasons.');
  });

  it('the Refresh now tooltip no longer claims to act as a scheduled run', () => {
    expect(refreshNowTooltip).toBe(
      'Handle this item now: an unmatched item or a mismatched movie gets the automatic fix match, then Plex refreshes the item and autorefresh checks it again',
    );
  });
});
