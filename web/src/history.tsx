import { Collapsible } from '@astryxdesign/core/Collapsible';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Icon } from '@astryxdesign/core/Icon';
import { HStack, StackItem, VStack } from '@astryxdesign/core/Layout';
import { StatusDot } from '@astryxdesign/core/StatusDot';
import { Heading, Text } from '@astryxdesign/core/Text';
import { borderVars, colorVars, spacingVars } from '@astryxdesign/core/theme/tokens.stylex';
import { ClockIcon } from '@heroicons/react/24/outline';
import * as stylex from '@stylexjs/stylex';
import type { ReactNode } from 'react';
import type { MatchSearch, TimelineEvent } from './api';
import { useNow } from './clock';
import { dateTime, plural, relative } from './format';
import { decisionView, eventView, rowVerdict, searchSource, verdictName, type EventView, type Tone } from './timeline';

// The history of an item: the full timeline of its detail, and the panel
// of the Fix match dialog that explains the last automatic decision. Both
// render the same searches and verdicts.

const toneLabel: Record<Tone, string> = { success: 'Succeeded', error: 'Failed', warning: 'Needs attention', neutral: 'Info' };

const styles = stylex.create({
  // The rail of the timeline: a thin line that joins each marker to the
  // next one, with the same gap above and below it. It is decoration only.
  connector: {
    flexGrow: 1,
    width: borderVars['--border-width'],
    marginBlockStart: spacingVars['--spacing-1-5'],
    backgroundColor: colorVars['--color-border-emphasized'],
  },
});

function EntryMarker({ tone }: { tone: Tone }) {
  return <StatusDot variant={tone} label={toneLabel[tone]} />;
}

function EntryText({ view, at }: { view: EventView; at?: string }) {
  const now = useNow();
  return (
    <VStack gap={0.5}>
      <Text weight="semibold" wordBreak="break-word">{view.title}</Text>
      <Text type="supporting" color="secondary">
        {[view.by, at ? `${relative(at, now)} · ${dateTime(at)}` : undefined].filter(Boolean).join(' · ')}
      </Text>
      {view.detail && <Text type="supporting" wordBreak="break-word">{view.detail}</Text>}
    </VStack>
  );
}

function EntryHead({ view, at }: { view: EventView; at?: string }) {
  return (
    <HStack gap={3} vAlign="start">
      <VStack paddingBlockStart={1.5}>
        <EntryMarker tone={view.tone} />
      </VStack>
      <StackItem size="fill">
        <EntryText view={view} at={at} />
      </StackItem>
    </HStack>
  );
}

/** The searches of a decision about an item of the year itemYear, each with
 * its candidates and verdicts. */
export function DecisionSearches({ searches, itemYear }: { searches: MatchSearch[]; itemYear?: number }) {
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
          {search.candidates.map((candidate, position) => {
            const verdict = verdictName(rowVerdict(candidate, itemYear), search);
            return (
              <Text key={`${position}-${candidate.guid}`} type="supporting" color="secondary" wordBreak="break-word">
                {`${candidate.name || candidate.guid || 'Candidate without a GUID'}${candidate.year ? ` (${candidate.year})` : ''}`}
                {verdict ? ` · ${verdict}` : ''}
              </Text>
            );
          })}
        </VStack>
      ))}
    </VStack>
  );
}

/** One event of the timeline. A line in the marker column joins its
 * marker to the marker of the next, older event. It grows with the row, so
 * wrapped text and open searches keep it whole. The last event has none. */
function TimelineEntry({ event, isLast }: { event: TimelineEvent; isLast: boolean }) {
  const view = eventView(event);
  return (
    <HStack gap={3}>
      <VStack hAlign="center" paddingBlockStart={1.5}>
        <EntryMarker tone={view.tone} />
        {!isLast && <VStack xstyle={styles.connector} aria-hidden="true" />}
      </VStack>
      <StackItem size="fill">
        <VStack gap={2} paddingBlockEnd={isLast ? 0 : 4}>
          <EntryText view={view} at={event.at} />
          {event.kind === 'match-decision' && event.searches.length > 0 && (
            <Collapsible defaultIsOpen={false} chevronPosition="start" trigger={<Text type="supporting">{plural(event.searches.length, 'search', 'searches')}</Text>}>
              <VStack paddingBlockStart={2}>
                <DecisionSearches searches={event.searches} itemYear={event.itemYear} />
              </VStack>
            </Collapsible>
          )}
        </VStack>
      </StackItem>
    </HStack>
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
        <VStack>
          {timeline.map((event, index) => (
            <TimelineEntry key={event.id} event={event} isLast={index === timeline.length - 1} />
          ))}
        </VStack>
      )}
    </VStack>
  );
}

/** Explains the last automatic fix match: its searches, its candidates, and
 * why it picked one or none. Under a heading of its own place, it has no
 * heading. */
interface DecisionPanelProps {
  decision?: TimelineEvent;
  hasHeading?: boolean;
  /** The action of the empty state, when there is no decision. */
  emptyAction?: ReactNode;
}

export function DecisionPanel({ decision, hasHeading = true, emptyAction }: DecisionPanelProps) {
  if (!decision) {
    return (
      <EmptyState
        isCompact
        icon={<Icon icon={ClockIcon} size="lg" />}
        title="No automatic fix match to explain"
        description="Autorefresh has not tried a fix match on this item within the history it keeps. It tries one for unmatched items with IDs in their folder or file names and, with a TMDB token, for unmatched series by their title. Refresh now also tries one for a mismatched movie, unless mismatched is in ignore-reasons."
        actions={emptyAction}
      />
    );
  }
  const view = decisionView(decision);
  return (
    <VStack gap={4}>
      {hasHeading && <Heading level={3} accessibilityLevel={3}>Last automatic fix match</Heading>}
      <EntryHead view={{ ...view, detail: undefined }} at={decision.at} />
      <DecisionSearches searches={decision.searches} itemYear={decision.itemYear} />
      {view.detail && <Text wordBreak="break-word">{view.detail}</Text>}
    </VStack>
  );
}
