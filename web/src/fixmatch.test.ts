import { QueryClient, QueryObserver } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { TimelineEvent } from './api';
import { candidateQuery, newerDecision, previewQuery } from './match';

// The Fix match dialog reads the preview through previewQuery. Mounting
// the dialog subscribes an observer, and closing it unsubscribes, so these
// tests drive the query cache the way the dialog does.

const params = 'srv=0&key=7';
const decision = (at: string, outcome = 'no-match'): TimelineEvent => ({ id: 0, kind: 'match-decision', at, outcome, accepted: false, searches: [] });

/** Opens the dialog: an observer of the preview, and how to close it. */
function open(client: QueryClient) {
  const observer = new QueryObserver(client, previewQuery(params));
  const close = observer.subscribe(() => {});
  return { observer, close };
}

describe('fix match preview', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('runs only on request and shows the last preview when the dialog reopens', async () => {
    const preview = decision('2026-11-23T10:00:00Z', 'matched');
    const fetch = vi.fn(async () => new Response(JSON.stringify(preview)));
    vi.stubGlobal('fetch', fetch);
    const client = new QueryClient();

    const first = open(client);
    expect(fetch).not.toHaveBeenCalled();
    await first.observer.refetch();
    expect(first.observer.getCurrentResult().data).toEqual(preview);
    first.close();

    const second = open(client);
    expect(second.observer.getCurrentResult().data).toEqual(preview);
    expect(fetch).toHaveBeenCalledTimes(1);
    second.close();
    client.clear();
  });

  it('does not run again when the dialog reopens after a failed preview', async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ error: 'Plex unavailable' }), { status: 502 }));
    vi.stubGlobal('fetch', fetch);
    const client = new QueryClient();

    const first = open(client);
    await first.observer.refetch();
    expect(first.observer.getCurrentResult().error).toBeTruthy();
    first.close();

    const second = open(client);
    expect(second.observer.getCurrentResult().data).toBeUndefined();
    expect(fetch).toHaveBeenCalledTimes(1);
    second.close();
    client.clear();
  });

  it('cancels a running preview when the dialog closes, and does not restart it on reopen', async () => {
    let aborted = false;
    const fetch = vi.fn((_: string, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => {
        aborted = true;
        reject(new DOMException('aborted', 'AbortError'));
      });
    }));
    vi.stubGlobal('fetch', fetch);
    const client = new QueryClient();

    const first = open(client);
    void first.observer.refetch();
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    first.close();
    await vi.waitFor(() => expect(aborted).toBe(true));

    const second = open(client);
    expect(second.observer.getCurrentResult().data).toBeUndefined();
    expect(second.observer.getCurrentResult().isFetching).toBe(false);
    expect(fetch).toHaveBeenCalledTimes(1);
    second.close();
    client.clear();
  });

  it('shows the newer of the preview and the last automatic decision', () => {
    const preview = decision('2026-11-23T10:00:00Z');
    const older = decision('2026-11-23T09:59:59Z');
    const newer = decision('2026-11-23T10:00:01Z');
    expect(newerDecision(preview, older)).toBe(preview);
    expect(newerDecision(preview, newer)).toBe(newer);
    // Both keep whole seconds, so the recorded decision wins a tie.
    const sameSecond = decision('2026-11-23T10:00:00Z');
    expect(newerDecision(preview, sameSecond)).toBe(sameSecond);
    expect(newerDecision(undefined, older)).toBe(older);
    expect(newerDecision(preview, undefined)).toBe(preview);
    expect(newerDecision(undefined, undefined)).toBeUndefined();
  });
});

// Each card of a series candidate reads candidateQuery. The provider is a
// remote service, so the dialog asks it once per GUID while the page is
// open, whatever happens to the dialog.
describe('candidate series', () => {
  afterEach(() => vi.unstubAllGlobals());
  const guid = 'plex://show/000000000000000000000001';

  function openCard(client: QueryClient) {
    const observer = new QueryObserver(client, candidateQuery(params, guid));
    const close = observer.subscribe(() => {});
    return { observer, close };
  }

  it('does not ask again when the dialog reopens after a failure', async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ error: 'Plex metadata provider unavailable' }), { status: 502 }));
    vi.stubGlobal('fetch', fetch);
    const client = new QueryClient();

    const first = openCard(client);
    await vi.waitFor(() => expect(first.observer.getCurrentResult().isError).toBe(true));
    first.close();

    const second = openCard(client);
    expect(second.observer.getCurrentResult().isFetching).toBe(false);
    expect(fetch).toHaveBeenCalledTimes(1);
    second.close();
    client.clear();
  });

  it('lets a lookup finish into the cache when the dialog closes mid-flight', async () => {
    let answer: (response: Response) => void = () => {};
    const fetch = vi.fn(() => new Promise<Response>(resolve => { answer = resolve; }));
    vi.stubGlobal('fetch', fetch);
    const client = new QueryClient();

    const first = openCard(client);
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    first.close();
    answer(new Response(JSON.stringify({ tmdbId: '48213', seasons: 1, episodes: 10 })));

    await vi.waitFor(() => expect(client.getQueryData(candidateQuery(params, guid).queryKey)).toEqual({ tmdbId: '48213', seasons: 1, episodes: 10 }));
    const second = openCard(client);
    expect(second.observer.getCurrentResult().data).toEqual({ tmdbId: '48213', seasons: 1, episodes: 10 });
    expect(fetch).toHaveBeenCalledTimes(1);
    second.close();
    client.clear();
  });
});
