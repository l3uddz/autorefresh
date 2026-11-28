import { Banner } from '@astryxdesign/core/Banner';
import { VStack } from '@astryxdesign/core/Layout';
import { Text } from '@astryxdesign/core/Text';
import type { ComparedSource, LiveItem } from './api';

const sourceNames: Record<string, string> = { tmdb: 'TMDB', imdb: 'IMDb', tvdb: 'TVDB' };

/** Returns one line for each compared source, such as "TMDB: path 100,
 * Plex 200". */
export function sourceLines(sources: ComparedSource[]): string[] {
  return sources.map(s => `${sourceNames[s.source] ?? s.source}: path ${s.path}, Plex ${s.plex.join(', ')}`);
}

/** The state of the live view query. After a failed refetch, TanStack
 * Query keeps the data of the last success beside the error. */
interface LiveState {
  data?: LiveItem;
  error: Error | null;
}

/** Warns that Plex matched a movie to another entry than its path names.
 * It shows nothing without a live view, while the live view fails, or for
 * another result. Fix match or Refresh now in the detail fixes it, so the
 * banner has no button. */
export function WrongMatchWarning({ live: { data, error } }: { live: LiveState }) {
  const mismatch = error ? undefined : data?.mismatch;
  if (mismatch?.result !== 'mismatched') return null;
  return (
    <Banner
      status="warning"
      title="Plex matched this movie to a different entry than its path names"
      description={
        <VStack gap={1}>
          <Text color="inherit">Use Fix match to choose the right one, or Refresh now to try the automatic fix match.</Text>
          {sourceLines(mismatch.sources).map(line => (
            <Text key={line} type="code" color="inherit">{line}</Text>
          ))}
        </VStack>
      }
    />
  );
}
