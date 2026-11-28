import { QueryClient } from '@tanstack/react-query';
import { renderToString } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { LiveItem } from './api';
import { Reasons } from './media';
import { WrongMatchWarning } from './wrongmatch';

const live: LiveItem = {
  guid: 'plex://movie/5d77',
  locations: [],
  files: ['/movies/Alien {tmdb-100} {imdb-tt5}/Alien.mkv'],
  externalIds: ['imdb://tt6', 'tmdb://200'],
  mismatch: {
    result: 'mismatched',
    sources: [
      { source: 'tmdb', path: '100', plex: ['200'] },
      { source: 'imdb', path: 'tt5', plex: ['tt6', 'tt7'] },
    ],
  },
};

describe('wrong match warning', () => {
  it('names each disagreeing source and points to Fix match and Refresh now', () => {
    const html = renderToString(<WrongMatchWarning live={{ data: live, error: null }} />);
    expect(html).toContain('Plex matched this movie to a different entry than its path names');
    expect(html).toContain('Use Fix match to choose the right one, or Refresh now to apply the automatic pick.');
    expect(html).toContain('TMDB: path 100, Plex 200');
    expect(html).toContain('IMDb: path tt5, Plex tt6, tt7');
    expect(html).not.toContain('<button');
  });

  it('shows nothing without a live view', () => {
    expect(renderToString(<WrongMatchWarning live={{ error: null }} />)).toBe('');
  });

  it('shows nothing for another result or without a check', () => {
    for (const result of ['agrees', 'undecided', 'no-path-ids'] as const) {
      expect(renderToString(<WrongMatchWarning live={{ data: { ...live, mismatch: { result, sources: [] } }, error: null }} />)).toBe('');
    }
    expect(renderToString(<WrongMatchWarning live={{ data: { ...live, mismatch: undefined }, error: null }} />)).toBe('');
  });

  it('hides the warning of an older answer while a refetch fails', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const key = ['item', 'live', 'srv=0&key=1'];
    await client.fetchQuery({ queryKey: key, queryFn: () => live });
    await client.fetchQuery({ queryKey: key, queryFn: () => Promise.reject(new Error('Plex unavailable')), staleTime: 0 }).catch(() => undefined);
    const state = client.getQueryState<LiveItem, Error>(key);
    expect(state?.data).toBe(live);
    expect(state?.error?.message).toBe('Plex unavailable');
    expect(renderToString(<WrongMatchWarning live={{ data: state?.data, error: state?.error ?? null }} />)).toBe('');
  });
});

describe('wrong match badge', () => {
  it('labels the reason like the other reasons', () => {
    const html = renderToString(<Reasons reasons={['mismatched']} ignored={[]} />);
    expect(html).toContain('mismatched');
    expect(renderToString(<Reasons reasons={['mismatched']} ignored={['mismatched']} />)).toContain('mismatched (ignored)');
  });
});
