import { Button } from '@astryxdesign/core/Button';
import { Card } from '@astryxdesign/core/Card';
import { HStack, StackItem, VStack } from '@astryxdesign/core/Layout';
import { Text } from '@astryxdesign/core/Text';
import { Token } from '@astryxdesign/core/Token';
import { useQuery } from '@tanstack/react-query';
import type { ItemRecord, TimelineEvent } from './api';
import { EpisodeCheckRow } from './episodecheck';
import { plexCounts, plural, typeName } from './format';
import { asksProvider, autoEvidence, autoEvidenceText, candidateQuery, compareEpisodeCount, compareYear, episodeCheckId, isFolderEvidence, type CandidateGroup, type EpisodeCountCheck, type Evidence, type YearCheck } from './match';
import { RemotePoster } from './media';

function yearToken(check: YearCheck) {
  switch (check.kind) {
    case 'same':
      return <Token size="sm" color="green" label={`year ${check.year}`} />;
    case 'off':
      return <Token size="sm" color="yellow" label={`year off by ${check.diff}`} />;
    case 'differs':
      return <Token size="sm" color="red" label={`year differs by ${check.diff}`} />;
    default:
      return null;
  }
}

function episodeToken(check: EpisodeCountCheck) {
  switch (check.kind) {
    case 'same':
      return <Token size="sm" color="green" label={`${plural(check.episodes, 'episode')}, same as yours`} />;
    case 'differs':
      return <Token size="sm" color="yellow" label={`${plural(check.episodes, 'episode')}, yours has ${check.yours.toLocaleString()}`} />;
    default:
      return null;
  }
}

function foundLabel(evidence: Evidence): string {
  const year = evidence.year ? ` (${evidence.year})` : '';
  return `Found by ${evidence.isTyped ? 'your' : 'the'} ${evidence.source === 'id' ? 'ID' : 'title'} search “${evidence.query}”${year}`;
}

function evidenceLabel(evidence: Evidence): string {
  if (evidence.source === 'tmdb' && evidence.fromId) return `matches folder ${evidence.fromId} via ${evidence.query}`;
  return `matches folder ${evidence.query}`;
}

export interface CandidateCardProps {
  group: CandidateGroup;
  item: ItemRecord;
  params: string;
  decision?: TimelineEvent;
  /** The decision is a preview, not the last automatic fix match. */
  isPreview: boolean;
  isCurrent: boolean;
  /** The episodes that Plex has for the item, from the live view. */
  itemEpisodes?: number;
  isTMDBOn: boolean;
  /** The process of the service, which scopes the provider cache. */
  boot?: string;
  isApplying: boolean;
  /** Puts the score and the button below the details, for a phone. */
  isCompact: boolean;
  onChoose: () => void;
}

export function CandidateCard({ group, item, params, decision, isPreview, isCurrent, itemEpisodes, isTMDBOn, boot, isApplying, isCompact, onChoose }: CandidateCardProps) {
  const { candidate } = group;
  // The metadata provider gives a series candidate its counts and TMDB ID.
  // A failed or unknown answer adds nothing to the card.
  const series = useQuery({ ...candidateQuery(params, group.guid, boot), enabled: asksProvider(group, item.type, boot) });
  const tmdbId = episodeCheckId(group, item.type, isTMDBOn, series.data?.tmdbId);
  const folder = group.foundBy.filter(isFolderEvidence);
  const found = group.foundBy.filter(e => !isFolderEvidence(e));
  const auto = autoEvidence(decision, group.guid);
  const facts = [typeName(candidate.type), candidate.year, ...plexCounts(series.data)].filter(Boolean).join(' · ');
  const reason = !group.guid ? 'Plex gave this candidate no GUID, so it cannot be applied' : isCurrent ? 'This is the current match' : undefined;
  const score = candidate.score > 0 && (
    <VStack gap={0} hAlign={isCompact ? 'start' : 'end'}>
      <Text weight="semibold" hasTabularNumbers>{candidate.score}</Text>
      <Text type="supporting" color="secondary">Plex score</Text>
    </VStack>
  );
  const match = <Button label="Match" variant={isCurrent ? 'secondary' : 'primary'} isDisabled={Boolean(reason) || isApplying} tooltip={reason} onClick={onChoose} />;
  const yearBadge = yearToken(compareYear(item.year, candidate.year));
  const episodeBadge = episodeToken(compareEpisodeCount(itemEpisodes, series.data?.episodes));
  const hasTokens = folder.length > 0 || yearBadge !== null || episodeBadge !== null;
  return (
    <Card padding={4}>
      <HStack gap={4} vAlign="start">
        <RemotePoster src={candidate.thumb} title={candidate.name} type={candidate.type} />
        <StackItem size="fill">
          <VStack gap={2}>
            <HStack gap={2} wrap="wrap" vAlign="center">
              <Text weight="semibold" wordBreak="break-word">{candidate.name || candidate.guid || 'Unnamed candidate'}</Text>
              {isCurrent && <Token size="sm" color="blue" label="currently matched" />}
            </HStack>
            {facts && <Text type="supporting" color="secondary">{facts}</Text>}
            {candidate.summary && <Text type="supporting" maxLines={3}>{candidate.summary}</Text>}
            {hasTokens && (
              <HStack gap={1} wrap="wrap">
                {folder.map((e, index) => <Token key={`${index}-${e.query}`} size="sm" color="green" label={evidenceLabel(e)} />)}
                {yearBadge}
                {episodeBadge}
              </HStack>
            )}
            {/* Longer evidence is text, so it wraps instead of being cut. */}
            {found.map((e, index) => (
              <Text key={`${index}-${e.query}`} type="supporting" color="secondary" wordBreak="break-word">
                {foundLabel(e)}
              </Text>
            ))}
            {auto.map((a, index) => (
              <Text key={`${index}-${a.query}`} type="supporting" wordBreak="break-word">
                {autoEvidenceText(a, isPreview)}
              </Text>
            ))}
            {group.guid && <Text type="code" color="secondary" wordBreak="break-all">{group.guid}</Text>}
            {/* The key ties the check to the item and the TMDB ID: when a
                search ties the candidate to another ID, the row starts
                again and drops the answer of the previous ID. */}
            {tmdbId && <EpisodeCheckRow key={`${params}|${tmdbId}`} params={params} tmdbId={tmdbId} />}
          </VStack>
        </StackItem>
        {!isCompact && <VStack gap={2} hAlign="end">{score}{match}</VStack>}
      </HStack>
      {isCompact && (
        <HStack gap={3} hAlign="between" vAlign="center" paddingBlockStart={3}>
          {score ?? <Text type="supporting" color="secondary">No Plex score</Text>}
          {match}
        </HStack>
      )}
    </Card>
  );
}
