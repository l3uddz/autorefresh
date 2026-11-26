import { useImperativeAlertDialog } from '@astryxdesign/core/AlertDialog';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { Collapsible } from '@astryxdesign/core/Collapsible';
import { Dialog, DialogHeader } from '@astryxdesign/core/Dialog';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { useMediaQuery } from '@astryxdesign/core/hooks';
import { DEFAULT_WIDTH_BREAKPOINTS } from '@astryxdesign/core/theme';
import { HStack, Layout, LayoutContent, LayoutPanel, VStack } from '@astryxdesign/core/Layout';
import { Link } from '@astryxdesign/core/Link';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { Heading, Text } from '@astryxdesign/core/Text';
import { useMutation, useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { ApiError, api, invalidateLists, post, useStatus, type Candidate, type ItemRecord, type LiveItem, type Matches, type MatchSearch, type Status, type TimelineEvent } from './api';
import { CandidateCard } from './candidatecard';
import { askThen } from './confirm';
import { actionError } from './errors';
import { plexCounts, plural, titleYear } from './format';
import { DecisionPanel } from './history';
import { groupCandidates, isCurrentMatch, listView, matchBody, matchesPath, newerDecision, previewQuery, previewSearches, sameSearch, type CandidateGroup, type TypedSearch } from './match';
import { useNotify } from './notify';
import { SearchForm } from './searchform';
import { lastDecision } from './timeline';

// The Fix match dialog of one item: candidates with their evidence on the
// left, the newer of the last automatic fix match and the preview on the
// right. Without a last one, a preview runs the automatic fix match on
// request and marks its pick. The dialog never applies a match without a
// confirmation that names the item and the candidate.

const wideQuery = `(min-width: ${DEFAULT_WIDTH_BREAKPOINTS.lg}px)`;
const selectBoot = (status: Status) => status.boot;
const selectTMDBOn = (status: Status) => status.tmdb !== 'off';

/** Match searches call Plex several times, so they run only when asked:
 * no refetch on focus, on a reconnect, or on a list refresh, no retry, and
 * no cache after the dialog closes. Their keys are outside the item
 * prefix, which invalidateLists refreshes. */
const searchOptions = { staleTime: Infinity, gcTime: 0, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false } as const;

interface Props {
  item: ItemRecord;
  params: string;
  timeline: TimelineEvent[];
  /** What Plex tells about the item now: its GUID and counts. */
  live?: LiveItem;
  isOpen: boolean;
  onClose: () => void;
}

export function FixMatchDialog({ isOpen, onClose, ...props }: Props) {
  const isWide = useMediaQuery(wideQuery);
  return (
    <Dialog isOpen={isOpen} onOpenChange={open => !open && onClose()} width={1120} maxHeight="90dvh" variant={isWide ? 'standard' : 'fullscreen'} purpose="form" padding={0}>
      {/* The body mounts only while the dialog is open, so closing it
          aborts its searches and forgets their answers. Only the
          preview stays in the cache. */}
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

function FixMatchBody({ item, params, timeline, live, isWide, onClose }: BodyProps) {
  const client = useQueryClient();
  const notify = useNotify();
  const confirm = useImperativeAlertDialog();
  const boot = useStatus(selectBoot).data;
  const isTMDBOn = useStatus(selectTMDBOn).data ?? false;
  const [typed, setTyped] = useState<TypedSearch>();

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
  // The preview searches like a run, so it runs only on request. A
  // preview that still runs is joined, not restarted.
  const preview = useQuery(previewQuery(params, boot));
  // Until the live status names the process, the cache key of the preview
  // would change under a running preview, so the preview waits for it.
  const canPreview = boot !== undefined;
  const runPreview = () => {
    if (canPreview) void preview.refetch({ cancelRefetch: false });
  };

  const apply = useMutation({
    mutationFn: (target: Applying) => post(`item/match?${target.params}`, matchBody(target.candidate)),
    onSuccess: (_, target) => {
      notify('success', `Plex accepted the match of ${target.title} to ${candidateTitle(target.candidate)}. Plex now refreshes its metadata.`);
      onClose();
    },
    onError: error => notify('error', actionError(error)),
    // A failed match is recorded too, and a Plex 404 removes the item.
    onSettled: () => invalidateLists(client),
  });

  // The newer of the preview and the last decision explains the item and
  // marks the candidates.
  const decision = newerDecision(preview.data, lastDecision(timeline));
  const isPreview = decision !== undefined && decision === preview.data;
  const decisionTitle = isPreview ? 'Automatic fix match preview' : 'Last automatic fix match';
  const decisionPanel = (hasHeading: boolean) => (
    <AutoMatchPanel decision={decision} isPreview={isPreview} title={decisionTitle} hasHeading={hasHeading} preview={preview} canRun={canPreview} onRun={runPreview} />
  );
  const openingSearches = opening.data?.searches;
  const typedSearches = search.data?.searches;
  const typedYear = typed?.year;
  const hasTyped = typed !== undefined;
  const groups = useMemo(
    () => groupCandidates([...(openingSearches ?? []), ...previewSearches(isPreview ? decision : undefined)], hasTyped ? typedSearches : [], typedYear),
    [openingSearches, isPreview, decision, hasTyped, typedSearches, typedYear],
  );
  const title = titleYear(item.title ?? `Item ${item.key}`, item.year);
  const counts = plexCounts(live);
  const subtitle = [item.library, counts.length ? `${counts.join(' · ')} in Plex` : undefined].filter(Boolean).join(' · ');
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
              isCurrent={isCurrentMatch(group, live?.guid)}
              itemEpisodes={live?.episodes}
              isTMDBOn={isTMDBOn}
              boot={boot}
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
      header={<DialogHeader title={`Fix match · ${title}`} subtitle={subtitle} onOpenChange={open => !open && onClose()} hasDivider={false} />}
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

interface AutoMatchProps {
  /** The newer of the preview and the last automatic decision. */
  decision?: TimelineEvent;
  /** The decision is the preview. */
  isPreview: boolean;
  title: string;
  hasHeading: boolean;
  preview: UseQueryResult<TimelineEvent>;
  /** The preview can run: the live status names the process. */
  canRun: boolean;
  onRun: () => void;
}

/** The last automatic fix match, or the preview that runs it now. The
 * preview records nothing and sends nothing to Plex. */
function AutoMatchPanel({ decision, isPreview, title, hasHeading, preview, canRun, onRun }: AutoMatchProps) {
  const run = (label: string, variant: 'primary' | 'secondary') => (
    <Button label={label} variant={variant} size="sm" isLoading={preview.isFetching} isDisabled={!canRun} tooltip={canRun ? undefined : 'Waiting for the status of autorefresh'} onClick={onRun} />
  );
  const failure = preview.error && (
    <Banner status="error" title="The preview failed" description={actionError(preview.error)} />
  );
  if (!isPreview) {
    return (
      <VStack gap={4}>
        <DecisionPanel decision={decision} hasHeading={hasHeading} emptyAction={run('Try automatic fix match', 'primary')} />
        {/* A recorded decision replaces a preview, also one that failed. */}
        {!decision && failure}
      </VStack>
    );
  }
  return (
    <VStack gap={4}>
      {hasHeading && <Heading level={3} accessibilityLevel={3}>{title}</Heading>}
      <Text type="supporting" color="secondary">
        Autorefresh searched as a run would, but recorded nothing and sent nothing to Plex. To apply its pick, click Match on that candidate. For an unmatched item in the queue, Refresh now in the item details also applies the automatic pick.
      </Text>
      <DecisionPanel decision={decision} hasHeading={false} />
      {failure}
      <HStack>{run('Run again', 'secondary')}</HStack>
    </VStack>
  );
}
