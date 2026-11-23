import { Card } from '@astryxdesign/core/Card';
import { Divider } from '@astryxdesign/core/Divider';
import { Icon } from '@astryxdesign/core/Icon';
import { HStack, StackItem, VStack } from '@astryxdesign/core/Layout';
import { StatusDot } from '@astryxdesign/core/StatusDot';
import { Text } from '@astryxdesign/core/Text';
import { ArrowsRightLeftIcon, CheckIcon, FilmIcon, MinusIcon, TvIcon, XMarkIcon } from '@heroicons/react/24/outline';
import type { NowItem, RunProgress } from './api';
import { useSecondClock } from './clock';
import { Poster } from './media';
import { countdown, itemDetail, outcomeView, since, type OutcomeTone } from './now';

// The now panel of a busy run: the item in work, or the next one with the
// countdown of the pace wait, and under it the last finished item. The top
// shows what comes next, the footer what just happened.

const toneIcons: Record<OutcomeTone, typeof CheckIcon> = {
  green: CheckIcon,
  purple: ArrowsRightLeftIcon,
  red: XMarkIcon,
  gray: MinusIcon,
};

/** Shows the now panel of run, or nothing before its first item. */
export function NowPanel({ run }: { run: RunProgress }) {
  const top = run.current ?? run.next;
  const now = useSecondClock(Boolean(run.next || run.last));
  if (!top && !run.last) return null;
  // A bulk refresh selects items by hand, so their reasons say little.
  const withReasons = run.kind === 'run';
  return (
    <Card variant="muted" padding={0}>
      <VStack>
        {top && (
          <TopRow item={top} isWorking={Boolean(run.current)} wait={countdown(run.nextAt, now)} withReasons={withReasons} />
        )}
        {top && run.last && <Divider />}
        {run.last && <LastRow item={run.last} ago={since(run.last.finishedAt, now)} />}
      </VStack>
    </Card>
  );
}

function TopRow({ item, isWorking, wait, withReasons }: { item: NowItem; isWorking: boolean; wait: string; withReasons: boolean }) {
  const detail = itemDetail(item, withReasons);
  return (
    <HStack gap={3} vAlign="center" padding={3}>
      <Poster src={item.thumb} title={item.title} type={item.type} size="sm" />
      <StackItem size="fill">
        <VStack gap={0.5}>
          <HStack gap={2} vAlign="center">
            <Text weight="semibold" maxLines={1}>
              {item.title ?? `Item ${item.key}`}
            </Text>
            {item.year ? <Text color="secondary">{item.year}</Text> : null}
          </HStack>
          {detail && (
            <HStack gap={1} vAlign="center">
              <Icon icon={item.type === 'show' ? TvIcon : FilmIcon} size="xsm" color="secondary" />
              <Text type="supporting" maxLines={1}>
                {detail}
              </Text>
            </HStack>
          )}
        </VStack>
      </StackItem>
      <VStack gap={1} hAlign="end">
        {isWorking ? (
          <HStack gap={1.5} vAlign="center">
            <StatusDot variant="accent" isPulsing label="Working" />
            <Text type="supporting" weight="medium" color="primary">
              Working
            </Text>
          </HStack>
        ) : (
          <Text type="supporting" weight="medium" color="primary">
            Up next
          </Text>
        )}
        {!isWorking && wait && (
          <Text type="supporting" hasTabularNumbers>
            {wait}
          </Text>
        )}
      </VStack>
    </HStack>
  );
}

function LastRow({ item, ago }: { item: NowItem; ago: string }) {
  const outcome = outcomeView(item.outcome);
  const detail = [itemDetail(item, false), outcome.text].filter(Boolean).join(' · ');
  return (
    <HStack gap={2} vAlign="center" padding={2}>
      <Icon icon={toneIcons[outcome.tone]} size="sm" color={outcome.tone} label={outcome.text || undefined} />
      <StackItem size="fill">
        <Text type="supporting" maxLines={1}>
          Last{' '}
          <Text type="inherit" weight="medium" color="primary">
            {item.title ?? `Item ${item.key}`}
          </Text>
          {item.year ? ` ${item.year}` : ''}
          {detail ? ` · ${detail}` : ''}
        </Text>
      </StackItem>
      <Text type="supporting" hasTabularNumbers>
        {ago}
      </Text>
    </HStack>
  );
}
