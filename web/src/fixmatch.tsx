import { useImperativeAlertDialog } from '@astryxdesign/core/AlertDialog';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { Card } from '@astryxdesign/core/Card';
import { Collapsible } from '@astryxdesign/core/Collapsible';
import { Dialog, DialogHeader } from '@astryxdesign/core/Dialog';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { useMediaQuery } from '@astryxdesign/core/hooks';
import { DEFAULT_WIDTH_BREAKPOINTS } from '@astryxdesign/core/theme';
import { HStack, Layout, LayoutContent, LayoutPanel, StackItem, VStack } from '@astryxdesign/core/Layout';
import { Link } from '@astryxdesign/core/Link';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { Heading, Text } from '@astryxdesign/core/Text';
import { TextInput } from '@astryxdesign/core/TextInput';
import { useToast } from '@astryxdesign/core/Toast';
import { Token } from '@astryxdesign/core/Token';
import { useMutation, useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query';
import { useState } from 'react';
import { ApiError, api, invalidateLists, post, useStatus, type Candidate, type EpisodeCheck, type ItemRecord, type Matches, type MatchSearch, type TimelineEvent } from './api';
import { askThen } from './confirm';
import { actionError } from './errors';
import { plural, titleYear, typeName } from './format';
import { DecisionPanel } from './history';
import { autoEvidence, autoEvidenceText, autoMatchPath, checkSearch, compareYear, groupCandidates, isCurrentMatch, isFolderEvidence, listView, matchBody, matchesPath, offersEpisodeCheck, sameSearch, type CandidateGroup, type Evidence, type TypedSearch, type YearCheck } from './match';
import { RemotePoster } from './media';
import { lastDecision } from './timeline';

// The Fix match dialog of one item: candidates with their evidence on the
// left, the last automatic fix match on the right. Without a last one, a
// preview runs the automatic fix match on request and marks its pick. The
// dialog never applies a match without a confirmation that names the item
// and the candidate.

const wideQuery = `(min-width: ${DEFAULT_WIDTH_BREAKPOINTS.lg}px)`;

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
      {/* Another item gets a new body, so no search or episode check of
          the previous item carries over. */}
      {isOpen && <FixMatchBody key={props.params} {...props} isWide={isWide} onClose={onClose} />}
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

/** Reports whether a search failed because Plex no longer has the item. */
function isGoneError(error: Error | null): boolean {
  return error instanceof ApiError && error.status === 404 && error.message === 'not found in Plex';
}

function candidateTitle(candidate: Candidate): string {
  return titleYear(candidate.name || candidate.guid, candidate.year);
}

function FixMatchBody({ item, params, timeline, liveGuid, isWide, onClose }: BodyProps) {
  const client = useQueryClient();
  const showToast = useToast();
  const confirm = useImperativeAlertDialog();
  const isTMDBOn = (useStatus().data?.tmdb ?? 'off') !== 'off';
  const [typed, setTyped] = useState<TypedSearch>();
  const [isPreviewAsked, setPreviewAsked] = useState(false);

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
  // The preview searches like a run, so it runs only on request.
  const preview = useQuery({
    queryKey: ['auto-match', params],
    queryFn: ({ signal }) => api<TimelineEvent>(autoMatchPath(params), { signal }),
    enabled: isPreviewAsked,
    ...searchOptions,
  });
  const runPreview = () => {
    if (isPreviewAsked) void preview.refetch({ cancelRefetch: false });
    else setPreviewAsked(true);
  };

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

  // A preview exists only without a last decision, and then marks the
  // candidates instead.
  const isPreview = preview.data !== undefined;
  const decision = preview.data ?? lastDecision(timeline);
  const decisionTitle = isPreview ? 'Automatic fix match preview' : 'Last automatic fix match';
  const decisionPanel = (hasHeading: boolean) => (
    <AutoMatchPanel decision={decision} title={decisionTitle} hasHeading={hasHeading} preview={preview} onRun={runPreview} />
  );
  const groups = groupCandidates(opening.data?.searches ?? [], typed ? search.data?.searches : [], typed?.year);
  const title = titleYear(item.title ?? `Item ${item.key}`, item.year);
  const isTypedPending = typed !== undefined && search.isPending;
  const view = listView(groups.length, opening.isPending || isTypedPending, opening.isSuccess || (typed !== undefined && search.isSuccess));

  // The same search again is a retry: its key is unchanged, so it runs
  // only when asked. A search that still runs is joined, not restarted.
  const onSearch = (input: TypedSearch) => {
    if (sameSearch(typed, input)) void search.refetch({ cancelRefetch: false });
    else setTyped(input);
  };

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
      <SearchForm isSearching={search.isFetching} onSearch={onSearch} onClear={typed && (() => setTyped(undefined))} />
      <FolderEvidence matches={opening.data} path={item.path} />
      <SearchStatus label="Opening search" query={opening} isGone={isGoneError(opening.error)} onRetry={() => void opening.refetch()} />
      {typed && <SearchStatus label={`Search for “${typed.q}”`} query={search} isGone={isGoneError(search.error)} onRetry={() => void search.refetch()} />}
      {view === 'list' && opening.isPending && <Text type="supporting" color="secondary">The opening search is still running. Its candidates join the list when it ends.</Text>}
      {view === 'loading' && (
        <VStack gap={3}>
          {[0, 1, 2].map(i => <Skeleton key={i} index={i} height={128} />)}
        </VStack>
      )}
      {view === 'empty' && <EmptyState isCompact title="No candidates" description="Plex found no candidate. Search by another title, or by an ID such as tmdb-48213." />}
      {view === 'list' && (
        <VStack gap={3}>
          {groups.map(group => (
            <CandidateCard
              key={group.key}
              group={group}
              item={item}
              params={params}
              decision={decision}
              isPreview={isPreview}
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
          {/* The inline end padding keeps the cards off the scrollbar. */}
          <VStack gap={6} paddingBlock={4} paddingInlineEnd={4}>
            {candidates}
            {!isWide && (
              <Collapsible defaultIsOpen={false} trigger={<Heading level={3} accessibilityLevel={3}>{decisionTitle}</Heading>}>
                <VStack paddingBlockStart={3}>{decisionPanel(false)}</VStack>
              </Collapsible>
            )}
          </VStack>
          {confirm.element}
        </LayoutContent>
      }
      end={
        isWide ? (
          <LayoutPanel width={360} padding={5} hasDivider isScrollable label={decisionTitle}>
            {decisionPanel(true)}
          </LayoutPanel>
        ) : undefined
      }
    />
  );
}

function SearchForm({ isSearching, onSearch, onClear }: { isSearching: boolean; onSearch: (search: TypedSearch) => void; onClear?: () => void }) {
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
            {files.map((file, index) => (
              <Text key={`${index}-${file}`} type="code" wordBreak="break-all">{file}</Text>
            ))}
          </VStack>
        </Collapsible>
      )}
    </VStack>
  );
}

interface StatusProps {
  label: string;
  query: { error: Error | null; data?: Matches };
  /** Plex no longer has the item, so a retry cannot help. */
  isGone: boolean;
  onRetry: () => void;
}

/** Reports a failed search request, failed searches, and a failed TMDB
 * step, which are no "0 candidates". A failed request offers a retry. */
function SearchStatus({ label, query, isGone, onRetry }: StatusProps) {
  if (query.error) {
    if (isGone) return <Banner status="error" title="Plex no longer has this item" description="It was deleted or moved, so it cannot be matched." />;
    return <Banner status="error" title={`${label} failed`} description={actionError(query.error)} endContent={<Button label="Retry" size="sm" variant="secondary" onClick={onRetry} />} />;
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

function foundLabel(evidence: Evidence): string {
  const year = evidence.year ? ` (${evidence.year})` : '';
  return `Found by ${evidence.isTyped ? 'your' : 'the'} ${evidence.source === 'id' ? 'ID' : 'title'} search “${evidence.query}”${year}`;
}

function evidenceLabel(evidence: Evidence): string {
  if (evidence.source === 'tmdb' && evidence.fromId) return `matches folder ${evidence.fromId} via ${evidence.query}`;
  return `matches folder ${evidence.query}`;
}

interface AutoMatchProps {
  /** The preview, or else the last automatic decision. */
  decision?: TimelineEvent;
  title: string;
  hasHeading: boolean;
  preview: UseQueryResult<TimelineEvent>;
  onRun: () => void;
}

/** The last automatic fix match, or the preview that runs it now. The
 * preview records nothing and sends nothing to Plex. */
function AutoMatchPanel({ decision, title, hasHeading, preview, onRun }: AutoMatchProps) {
  const run = (label: string, variant: 'primary' | 'secondary') => (
    <Button label={label} variant={variant} size="sm" isLoading={preview.isFetching} onClick={onRun} />
  );
  const failure = preview.error && (
    <Banner status="error" title="The preview failed" description={actionError(preview.error)} />
  );
  if (!preview.data) {
    return (
      <VStack gap={4}>
        <DecisionPanel decision={decision} hasHeading={hasHeading} emptyAction={run('Try automatic fix match', 'primary')} />
        {failure}
      </VStack>
    );
  }
  return (
    <VStack gap={4}>
      {hasHeading && <Heading level={3} accessibilityLevel={3}>{title}</Heading>}
      <Text type="supporting" color="secondary">
        Autorefresh searched as a run would, but recorded nothing and sent nothing to Plex. To apply its pick, click Match on that candidate.
      </Text>
      <DecisionPanel decision={decision} hasHeading={false} />
      {failure}
      <HStack>{run('Run again', 'secondary')}</HStack>
    </VStack>
  );
}

interface CardProps {
  group: CandidateGroup;
  item: ItemRecord;
  params: string;
  decision?: TimelineEvent;
  /** The decision is a preview, not the last automatic fix match. */
  isPreview: boolean;
  isCurrent: boolean;
  hasEpisodeCheck: boolean;
  isApplying: boolean;
  /** Puts the score and the button below the details, for a phone. */
  isCompact: boolean;
  onChoose: () => void;
}

function CandidateCard({ group, item, params, decision, isPreview, isCurrent, hasEpisodeCheck, isApplying, isCompact, onChoose }: CardProps) {
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
              {isCurrent && <Token size="sm" color="blue" label="currently matched" />}
            </HStack>
            {facts && <Text type="supporting" color="secondary">{facts}</Text>}
            {candidate.summary && <Text type="supporting" maxLines={3}>{candidate.summary}</Text>}
            <HStack gap={1} wrap="wrap">
              {folder.map((e, index) => <Token key={`${index}-${e.query}`} size="sm" color="green" label={evidenceLabel(e)} />)}
              {yearToken(compareYear(item.year, candidate.year))}
            </HStack>
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
            {hasEpisodeCheck && group.tmdbId && <EpisodeCheckRow key={`${params}|${group.tmdbId}`} params={params} tmdbId={group.tmdbId} />}
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
 * It applies nothing. Its parent keys it by item and TMDB ID, and it shows
 * only an answer for the ID that it sent. */
function EpisodeCheckRow({ params, tmdbId }: { params: string; tmdbId: string }) {
  const check = useMutation({
    mutationFn: (id: string) => post<EpisodeCheck>(`item/episode-check?${params}&${new URLSearchParams({ tmdb: id })}`),
  });
  const isThisID = check.variables === tmdbId;
  return (
    <HStack gap={3} wrap="wrap" vAlign="center">
      <Button label="Compare episodes" size="sm" variant="secondary" isLoading={check.isPending} onClick={() => check.mutate(tmdbId)} tooltip={`Compare the episode numbers of this item with TMDB tmdb-${tmdbId}`} />
      {isThisID && check.data && (
        <Text type="supporting">
          {check.data.match ? `The episode numbers match tmdb-${check.variables} (${plural(check.data.episodes, 'episode')} compared).` : `The episode numbers do not match tmdb-${check.variables}.`}
        </Text>
      )}
      {isThisID && check.error && <Text type="supporting">{`The episode check failed: ${actionError(check.error)}`}</Text>}
    </HStack>
  );
}
