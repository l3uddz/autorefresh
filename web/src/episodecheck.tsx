import { Button } from '@astryxdesign/core/Button';
import { Collapsible } from '@astryxdesign/core/Collapsible';
import { Icon } from '@astryxdesign/core/Icon';
import { HStack, VStack } from '@astryxdesign/core/Layout';
import { Text } from '@astryxdesign/core/Text';
import { Token } from '@astryxdesign/core/Token';
import { CheckIcon, MinusIcon, PlusIcon } from '@heroicons/react/24/outline';
import { useMutation } from '@tanstack/react-query';
import { post, type EpisodeCheck } from './api';
import { actionError } from './errors';
import { comparisonNotes, episodeCheckText, seasonView, type EpisodePlace, type SeasonView } from './match';

/** Compares the episode numbers of the item with the TMDB series tmdbId.
 * It applies nothing. Its parent keys it by item and TMDB ID, and it shows
 * only an answer for the ID that it sent. A result with compared seasons
 * opens the comparison of each season. */
export function EpisodeCheckRow({ params, tmdbId }: { params: string; tmdbId: string }) {
  const check = useMutation({
    mutationFn: (id: string) => post<EpisodeCheck>(`item/episode-check?${params}&${new URLSearchParams({ tmdb: id })}`),
  });
  const answer = check.variables === tmdbId ? check.data : undefined;
  const hasSeasons = Boolean(answer?.seasons?.length);
  return (
    <VStack gap={2}>
      <HStack gap={3} wrap="wrap" vAlign="center">
        <Button label="Compare episodes" size="sm" variant="secondary" isLoading={check.isPending} onClick={() => check.mutate(tmdbId)} tooltip={`Compare the episode numbers of this item with TMDB tmdb-${tmdbId}`} />
        {answer && !hasSeasons && <Text type="supporting">{episodeCheckText(answer, tmdbId)}</Text>}
        {check.variables === tmdbId && check.error && <Text type="supporting">{`The episode check failed: ${actionError(check.error)}`}</Text>}
      </HStack>
      {answer && hasSeasons && (
        <Collapsible defaultIsOpen={false} chevronPosition="start" trigger={<Text type="supporting">{episodeCheckText(answer, tmdbId)}</Text>}>
          <EpisodeComparison check={answer} tmdbId={tmdbId} />
        </Collapsible>
      )}
    </VStack>
  );
}

const placeColors: Record<EpisodePlace, 'default' | 'yellow' | 'red'> = { both: 'default', missing: 'yellow', extra: 'red' };
// Each place has an icon too, so the tokens do not tell it by colour alone.
const placeIcons: Record<EpisodePlace, typeof CheckIcon> = { both: CheckIcon, missing: MinusIcon, extra: PlusIcon };
const placeNames: Record<EpisodePlace, string> = { both: 'Plex and TMDB have it', missing: 'only TMDB has it', extra: 'only Plex has it' };

/** Shows the episode numbers of each compared season, marked by the side
 * that has each one. */
function EpisodeComparison({ check, tmdbId }: { check: EpisodeCheck; tmdbId: string }) {
  return (
    <VStack gap={3} paddingBlockStart={2}>
      {comparisonNotes(check, tmdbId).map(note => <Text key={note} type="supporting" color="secondary">{note}</Text>)}
      <HStack gap={2} wrap="wrap" vAlign="center">
        <PlaceToken place="both" label="In both" />
        <PlaceToken place="missing" label="Only TMDB" />
        <PlaceToken place="extra" label="Only Plex" />
      </HStack>
      {(check.seasons ?? []).map(season => <SeasonRow key={season.season} view={seasonView(season)} />)}
    </VStack>
  );
}

function SeasonRow({ view }: { view: SeasonView }) {
  return (
    <VStack gap={1}>
      <Text type="supporting" weight="medium">{`Season ${view.season} · Plex ${view.plex.toLocaleString()} · TMDB ${view.tmdb.toLocaleString()}`}</Text>
      <HStack gap={1} wrap="wrap">
        {view.episodes.map(e => (
          <PlaceToken key={e.number} place={e.place} label={String(e.number)} description={`Episode ${e.number}: ${placeNames[e.place]}`} />
        ))}
      </HStack>
      {view.duplicates.length > 0 && <Text type="supporting" color="secondary">{`Plex lists episode ${view.duplicates.join(', ')} more than once.`}</Text>}
    </VStack>
  );
}

function PlaceToken({ place, label, description }: { place: EpisodePlace; label: string; description?: string }) {
  return <Token size="sm" color={placeColors[place]} label={label} description={description} icon={<Icon icon={placeIcons[place]} size="xsm" />} />;
}
