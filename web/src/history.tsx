import { Collapsible } from '@astryxdesign/core/Collapsible';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Icon } from '@astryxdesign/core/Icon';
import { HStack, StackItem, VStack } from '@astryxdesign/core/Layout';
import { StatusDot } from '@astryxdesign/core/StatusDot';
import { Heading, Text } from '@astryxdesign/core/Text';
import { ClockIcon } from '@heroicons/react/24/outline';
import type { MatchSearch, TimelineEvent } from './api';
import { useNow } from './clock';
import { dateTime, plural, relative } from './format';
import { decisionView, eventView, searchSource, verdictName, type EventView, type Tone } from './timeline';

// The history of an item: the full timeline of its detail, and the panel
// of the Fix match dialog that explains the last automatic decision. Both
// render the same searches and verdicts.

const toneLabel: Record<Tone, string> = { success: 'Succeeded', error: 'Failed', warning: 'Needs attention', neutral: 'Info' };

function EntryHead({ view, at }: { view: EventView; at?: string }) {
  const now = useNow();
  return (
    <HStack gap={3} vAlign="start">
      <VStack paddingBlockStart={1.5}>
        <StatusDot variant={view.tone} label={toneLabel[view.tone]} />
      </VStack>
      <StackItem size="fill">
        <VStack gap={0.5}>
          <Text weight="semibold" wordBreak="break-word">{view.title}</Text>
          <Text type="supporting" color="secondary">
            {[view.by, at ? `${relative(at, now)} · ${dateTime(at)}` : undefined].filter(Boolean).join(' · ')}
          </Text>
          {view.detail && <Text type="supporting" wordBreak="break-word">{view.detail}</Text>}
        </VStack>
      </StackItem>
    </HStack>
  );
}

/** The searches of a decision, each with its candidates and verdicts. */
export function DecisionSearches({ searches }: { searches: MatchSearch[] }) {
  if (!searches.length) return <Text type="supporting" color="secondary">No search was made.</Text>;
  return (
    <VStack gap={3}>
      {searches.map((search, index) => (
        <VStack key={`${index}-${search.query}`} gap={1}>
          <Text type="supporting" wordBreak="break-word">
            <Text type="code">{search.query}</Text>
            {` · ${searchSource(search)} · `}
            {search.failed ? 'search failed' : plural(search.candidates.length, 'candidate')}
          </Text>
          {search.candidates.map((candidate, position) => (
            <Text key={`${position}-${candidate.guid}`} type="supporting" color="secondary" wordBreak="break-word">
              {`${candidate.name || candidate.guid || 'Candidate without a GUID'}${candidate.year ? ` (${candidate.year})` : ''}`}
              {verdictName(candidate.verdict) ? ` · ${verdictName(candidate.verdict)}` : ''}
            </Text>
          ))}
        </VStack>
      ))}
    </VStack>
  );
}

/** The full timeline of an item, newest event first. A match decision
 * opens its searches on request. */
export function ItemTimeline({ timeline }: { timeline: TimelineEvent[] }) {
  return (
    <VStack gap={3}>
      <Heading level={3} accessibilityLevel={3}>History</Heading>
      {timeline.length === 0 ? (
        <Text color="secondary">Nothing happened to this item yet.</Text>
      ) : (
        <VStack gap={4}>
          {timeline.map(event => (
            <VStack key={event.id} gap={2}>
              <EntryHead view={eventView(event)} at={event.at} />
              {event.kind === 'match-decision' && event.searches.length > 0 && (
                <VStack paddingInlineStart={6}>
                  <Collapsible defaultIsOpen={false} chevronPosition="start" trigger={<Text type="supporting">{plural(event.searches.length, 'search', 'searches')}</Text>}>
                    <VStack paddingBlockStart={2}>
                      <DecisionSearches searches={event.searches} />
                    </VStack>
                  </Collapsible>
                </VStack>
              )}
            </VStack>
          ))}
        </VStack>
      )}
    </VStack>
  );
}

/** Explains the last automatic fix match: its searches, its candidates, and
 * why it picked one or none. Under a heading of its own place, it has no
 * heading. */
export function DecisionPanel({ decision, hasHeading = true }: { decision?: TimelineEvent; hasHeading?: boolean }) {
  if (!decision) {
    return (
      <EmptyState
        isCompact
        icon={<Icon icon={ClockIcon} size="lg" />}
        title="No automatic fix match to explain"
        description="Autorefresh has not tried a fix match on this item within the history it keeps. It tries one only for unmatched items with IDs in their folder or file names."
      />
    );
  }
  const view = decisionView(decision);
  return (
    <VStack gap={4}>
      {hasHeading && <Heading level={3} accessibilityLevel={3}>Last automatic fix match</Heading>}
      <EntryHead view={{ ...view, detail: undefined }} at={decision.at} />
      <DecisionSearches searches={decision.searches} />
      {view.detail && <Text wordBreak="break-word">{view.detail}</Text>}
    </VStack>
  );
}
