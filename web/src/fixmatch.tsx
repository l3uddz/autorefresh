import { useImperativeAlertDialog } from '@astryxdesign/core/AlertDialog';
import { Badge } from '@astryxdesign/core/Badge';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { Card } from '@astryxdesign/core/Card';
import { Collapsible } from '@astryxdesign/core/Collapsible';
import { Dialog, DialogHeader } from '@astryxdesign/core/Dialog';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { useMediaQuery } from '@astryxdesign/core/hooks';
import { HStack, Layout, LayoutContent, LayoutPanel, StackItem, VStack } from '@astryxdesign/core/Layout';
import { Link } from '@astryxdesign/core/Link';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { Heading, Text } from '@astryxdesign/core/Text';
import { TextInput } from '@astryxdesign/core/TextInput';
import { useToast } from '@astryxdesign/core/Toast';
import { Token } from '@astryxdesign/core/Token';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { ApiError, api, invalidateLists, post, useStatus, type Candidate, type EpisodeCheck, type ItemRecord, type Matches, type MatchSearch, type TimelineEvent } from './api';
import { askThen } from './confirm';
import { actionError } from './errors';
import { plural, titleYear, typeName } from './format';
import { DecisionPanel } from './history';
import { autoEvidence, checkSearch, compareYear, groupCandidates, isCurrentMatch, isFolderEvidence, matchBody, matchesPath, offersEpisodeCheck, type CandidateGroup, type Evidence, type YearCheck } from './match';
import { RemotePoster } from './media';
import { lastDecision, verdictName } from './timeline';

// The Fix match dialog of one item: candidates with their evidence on the
// left, the last automatic fix match on the right. It never applies a
// match without a confirmation that names the item and the candidate.

const wideQuery = '(min-width: 900px)';

/** Match searches call Plex several times, so they run only when asked:
 * no refetch on focus or on a list refresh, no retry, and no cache after
 * the dialog closes. Their keys are outside the item prefix, which
 * invalidateLists refreshes. */
const searchOptions = { staleTime: Infinity, gcTime: 0, retry: false, refetchOnWindowFocus: false } as const;

interface Props {
  item: ItemRecord;
  params: string;
  timeline: TimelineEvent[];
  /** The current Plex GUID of the item, from the live view. */
  liveGuid?: string;
  isOpen: boolean;
  onClose: () => void;
}

export function FixMatchDialog({ isOpen, onClose, ...props }: Props) {
  const isWide = useMediaQuery(wideQuery);
  return (
    <Dialog isOpen={isOpen} onOpenChange={open => !open && onClose()} width={1120} maxHeight="90dvh" variant={isWide ? 'standard' : 'fullscreen'} purpose="form" padding={0}>
      {/* The body mounts only while the dialog is open, so closing it
          aborts its searches and forgets their answers. */}
      {isOpen && <FixMatchBody {...props} isWide={isWide} onClose={onClose} />}
    </Dialog>
  );
}

interface BodyProps extends Omit<Props, 'isOpen'> {
  isWide: boolean;
}

interface Applying {
  params: string;
  title: string;
  candidate: Candidate;
}

function candidateTitle(candidate: Candidate): string {
  return titleYear(candidate.name || candidate.guid, candidate.year);
}

function FixMatchBody({ item, params, timeline, liveGuid, isWide, onClose }: BodyProps) {
  const client = useQueryClient();
  const showToast = useToast();
  const confirm = useImperativeAlertDialog();
  const isTMDBOn = (useStatus().data?.tmdb ?? 'off') !== 'off';
  const [typed, setTyped] = useState<{ q: string; year?: number }>();

  const opening = useQuery({
    queryKey: ['matches', params],
    queryFn: ({ signal }) => api<Matches>(matchesPath(params), { signal }),
    ...searchOptions,
  });
  const search = useQuery({
    queryKey: ['matches', params, typed],
    queryFn: ({ signal }) => api<Matches>(matchesPath(params, typed), { signal }),
    enabled: typed !== undefined,
    ...searchOptions,
  });

  const apply = useMutation({
    mutationFn: (target: Applying) => post(`item/match?${target.params}`, matchBody(target.candidate)),
    onSuccess: (_, target) => {
      showToast({ body: `Plex accepted the match of ${target.title} to ${candidateTitle(target.candidate)}. Plex now refreshes its metadata.` });
      onClose();
    },
    onError: error => showToast({ body: actionError(error), type: 'error' }),
    // A failed match is recorded too, and a Plex 404 removes the item.
    onSettled: () => invalidateLists(client),
  });

  const decision = lastDecision(timeline);
  const groups = groupCandidates(opening.data?.searches ?? [], typed ? search.data?.searches : []);
  const title = titleYear(item.title ?? `Item ${item.key}`, item.year);

  const choose = (group: CandidateGroup) => {
    if (apply.isPending) return;
    // The item and the candidate are fixed here: the confirmation and the
    // request use the same values, whatever the searches do meanwhile.
    const target: Applying = { params, title, candidate: { ...group.candidate, guid: group.guid } };
    const name = candidateTitle(target.candidate);
    askThen(
      confirm,
      {
        title: 'Apply this match?',
        description: `“${target.title}” will be matched to “${name}”. Plex replaces the metadata of the item.`,
        actionLabel: 'Apply match',
        actionVariant: 'primary',
      },
      () => apply.mutateAsync(target),
    );
  };

  const candidates = (
    <VStack gap={4}>
      <SearchForm isSearching={search.isFetching} onSearch={setTyped} onClear={typed && (() => setTyped(undefined))} />
      <FolderEvidence matches={opening.data} path={item.path} />
      <SearchStatus label="Opening search" query={opening} />
      {typed && <SearchStatus label={`Search for “${typed.q}”`} query={search} />}
      {opening.isPending ? (
        <VStack gap={3}>
          {[0, 1, 2].map(i => <Skeleton key={i} index={i} height={128} />)}
        </VStack>
      ) : groups.length === 0 ? (
        opening.isSuccess && <EmptyState isCompact title="No candidates" description="Plex found no candidate. Search by another title, or by an ID such as tmdb-48213." />
      ) : (
        <VStack gap={3}>
          {groups.map((group, index) => (
            <CandidateCard
              key={group.guid || `loose-${index}`}
              group={group}
              item={item}
              params={params}
              decision={decision}
              isCurrent={isCurrentMatch(group, liveGuid)}
              hasEpisodeCheck={offersEpisodeCheck(group, item.type, isTMDBOn)}
              isApplying={apply.isPending}
              isCompact={!isWide}
              onChoose={() => choose(group)}
            />
          ))}
        </VStack>
      )}
    </VStack>
  );

  return (
    // The header stays in place while the candidates and the decision
    // scroll on their own, inside the height of the dialog.
    <Layout
      height="fill"
      padding={5}
      defaultHasDividers
      header={<DialogHeader title={`Fix match · ${title}`} subtitle={item.library} onOpenChange={open => !open && onClose()} hasDivider={false} />}
      content={
        <LayoutContent isScrollable label="Candidates">
          <VStack gap={6} paddingBlock={4}>
            {candidates}
            {!isWide && (
              <Collapsible defaultIsOpen={false} trigger={<Heading level={3} accessibilityLevel={3}>Last automatic fix match</Heading>}>
                <VStack paddingBlockStart={3}>
                  <DecisionPanel decision={decision} hasHeading={false} />
                </VStack>
              </Collapsible>
            )}
          </VStack>
          {confirm.element}
        </LayoutContent>
      }
      end={
        isWide ? (
          <LayoutPanel width={360} padding={5} hasDivider isScrollable label="Last automatic fix match">
            <DecisionPanel decision={decision} />
          </LayoutPanel>
        ) : undefined
      }
    />
  );
}

function SearchForm({ isSearching, onSearch, onClear }: { isSearching: boolean; onSearch: (search: { q: string; year?: number }) => void; onClear?: () => void }) {
  const [text, setText] = useState('');
  const [year, setYear] = useState('');
  const [error, setError] = useState<string>();
  const submit = () => {
    const input = checkSearch(text, year);
    if ('error' in input) {
      setError(input.error);
      return;
    }
    setError(undefined);
    onSearch(input);
  };
  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') submit();
  };
  return (
    <VStack gap={2}>
      <HStack gap={2} vAlign="end" wrap="wrap">
        <StackItem size="fill">
          <TextInput
            label="Search"
            type="search"
            value={text}
            placeholder="Title, or an ID such as tmdb-48213"
            onChange={value => setText(value)}
            onKeyDown={onKeyDown}
            status={error ? { type: 'error' } : undefined}
          />
        </StackItem>
        <TextInput label="Year" value={year} width={96} inputMode="numeric" placeholder="Any" onChange={value => setYear(value)} onKeyDown={onKeyDown} />
        <Button label="Search" variant="secondary" isLoading={isSearching} onClick={submit} />
        {onClear && <Button label="Clear search" variant="ghost" onClick={onClear} />}
      </HStack>
      <Text type="supporting" color={error ? 'primary' : 'secondary'}>
        {error ?? 'Search by title and an optional year, or by an ID such as tmdb-48213, tvdb-31207, or imdb-tt7712040. An ID search ignores the year.'}
      </Text>
    </VStack>
  );
}

/** The folder IDs of the item with their provider pages, and its files.
 * Only the opening search reads them. */
function FolderEvidence({ matches, path }: { matches?: Matches; path?: string }) {
  if (!matches) return null;
  const files = matches.files.length ? matches.files : matches.locations;
  return (
    <VStack gap={2}>
      {path && <Text type="code" wordBreak="break-all">{path}</Text>}
      <HStack gap={2} wrap="wrap" vAlign="center">
        <Text type="supporting" color="secondary">Folder IDs</Text>
        {matches.ids.length ? (
          matches.ids.map(id => (
            <Link key={id.id} href={id.link} isExternalLink referrerPolicy="no-referrer" type="supporting">
              {id.id}
            </Link>
          ))
        ) : (
          <Text type="supporting">none: add one, such as {'{tmdb-12345}'}, to the folder name</Text>
        )}
      </HStack>
      {files.length > 0 && (
        <Collapsible defaultIsOpen={false} chevronPosition="start" trigger={<Text type="supporting">{matches.files.length ? plural(files.length, 'file') : plural(files.length, 'folder')}</Text>}>
          <VStack gap={1} paddingBlockStart={2}>
            {files.map(file => (
              <Text key={file} type="code" wordBreak="break-all">{file}</Text>
            ))}
          </VStack>
        </Collapsible>
      )}
    </VStack>
  );
}

/** Reports a failed search request, failed searches, and a failed TMDB
 * step, which are no "0 candidates". */
function SearchStatus({ label, query }: { label: string; query: { error: Error | null; data?: Matches } }) {
  if (query.error) {
    const gone = query.error instanceof ApiError && query.error.status === 404 && query.error.message === 'not found in Plex';
    return <Banner status="error" title={gone ? 'Plex no longer has this item' : `${label} failed`} description={gone ? 'It was deleted or moved, so it cannot be matched.' : actionError(query.error)} />;
  }
  const failed = query.data?.searches.filter((s: MatchSearch) => s.failed) ?? [];
  return (
    <>
      {failed.length > 0 && (
        <Banner status="warning" title={`${plural(failed.length, 'search', 'searches')} failed`} description={`Plex answered ${failed.map(s => s.query).join(', ')} with an error. The other results are shown.`} />
      )}
      {query.data?.tmdbFailed && (
        <Banner status="warning" title="The TMDB step failed" description="Autorefresh could not translate the tvdb or imdb folder IDs to TMDB IDs, so those searches are missing." />
      )}
    </>
  );
}

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

function evidenceLabel(evidence: Evidence): string {
  if (evidence.source === 'tmdb' && evidence.fromId) return `matches folder ${evidence.fromId} via ${evidence.query}`;
  return `matches folder ${evidence.query}`;
}

interface CardProps {
  group: CandidateGroup;
  item: ItemRecord;
  params: string;
  decision?: TimelineEvent;
  isCurrent: boolean;
  hasEpisodeCheck: boolean;
  isApplying: boolean;
  /** Puts the score and the button below the details, for a phone. */
  isCompact: boolean;
  onChoose: () => void;
}

function CandidateCard({ group, item, params, decision, isCurrent, hasEpisodeCheck, isApplying, isCompact, onChoose }: CardProps) {
  const { candidate } = group;
  const folder = group.foundBy.filter(isFolderEvidence);
  const found = group.foundBy.filter(e => !isFolderEvidence(e));
  const auto = autoEvidence(decision, group.guid);
  const facts = [typeName(candidate.type), candidate.year].filter(Boolean).join(' · ');
  const reason = !group.guid ? 'Plex gave this candidate no GUID, so it cannot be applied' : isCurrent ? 'This is the current match' : undefined;
  const score = candidate.score > 0 && (
    <VStack gap={0} hAlign={isCompact ? 'start' : 'end'}>
      <Text weight="semibold" hasTabularNumbers>{candidate.score}</Text>
      <Text type="supporting" color="secondary">Plex score</Text>
    </VStack>
  );
  const match = <Button label="Match" variant={isCurrent ? 'secondary' : 'primary'} isDisabled={Boolean(reason) || isApplying} tooltip={reason} onClick={onChoose} />;
  return (
    <Card padding={4}>
      <HStack gap={4} vAlign="start">
        <RemotePoster src={candidate.thumb} title={candidate.name} type={candidate.type} />
        <StackItem size="fill">
          <VStack gap={2}>
            <HStack gap={2} wrap="wrap" vAlign="center">
              <Text weight="semibold" wordBreak="break-word">{candidate.name || candidate.guid || 'Unnamed candidate'}</Text>
              {isCurrent && <Badge variant="info" label="currently matched" />}
            </HStack>
            {facts && <Text type="supporting" color="secondary">{facts}</Text>}
            {candidate.summary && <Text type="supporting" maxLines={3}>{candidate.summary}</Text>}
            <HStack gap={1} wrap="wrap">
              {folder.map(e => <Token key={`${e.query}-${e.fromId}`} size="sm" color="green" label={evidenceLabel(e)} />)}
              {yearToken(compareYear(item.year, candidate.year))}
            </HStack>
            {/* Longer evidence is text, so it wraps instead of being cut. */}
            {found.map(e => (
              <Text key={`${e.isTyped}-${e.query}`} type="supporting" color="secondary" wordBreak="break-word">
                {`Found by ${e.isTyped ? 'your' : 'the'} ${e.source === 'id' ? 'ID' : 'title'} search “${e.query}”`}
              </Text>
            ))}
            {auto.map(a => (
              <Text key={a.query} type="supporting" wordBreak="break-word">
                {`The last automatic fix match found it via ${a.query}${verdictName(a.verdict) ? `: ${verdictName(a.verdict)}` : ''}`}
              </Text>
            ))}
            {group.guid && <Text type="code" color="secondary" wordBreak="break-all">{group.guid}</Text>}
            {hasEpisodeCheck && group.tmdbId && <EpisodeCheckRow params={params} tmdbId={group.tmdbId} />}
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

/** Compares the episode numbers of the item with the TMDB series tmdbId.
 * It applies nothing. The result belongs to this candidate. */
function EpisodeCheckRow({ params, tmdbId }: { params: string; tmdbId: string }) {
  const check = useMutation({
    mutationFn: () => post<EpisodeCheck>(`item/episode-check?${params}&${new URLSearchParams({ tmdb: tmdbId })}`),
  });
  return (
    <HStack gap={3} wrap="wrap" vAlign="center">
      <Button label="Compare episodes" size="sm" variant="secondary" isLoading={check.isPending} onClick={() => check.mutate()} tooltip={`Compare the episode numbers of this item with TMDB tmdb-${tmdbId}`} />
      {check.data && (
        <Text type="supporting">
          {check.data.match ? `The episode numbers match tmdb-${tmdbId} (${plural(check.data.episodes, 'episode')} compared).` : `The episode numbers do not match tmdb-${tmdbId}.`}
        </Text>
      )}
      {check.error && <Text type="supporting">{`The episode check failed: ${actionError(check.error)}`}</Text>}
    </HStack>
  );
}
