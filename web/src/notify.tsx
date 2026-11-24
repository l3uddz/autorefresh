import { Icon } from '@astryxdesign/core/Icon';
import { HStack, StackItem } from '@astryxdesign/core/Layout';
import { Text } from '@astryxdesign/core/Text';
import { useToast, type ToastOptions } from '@astryxdesign/core/Toast';
import { CheckCircleIcon, ExclamationCircleIcon, InformationCircleIcon } from '@heroicons/react/24/outline';
import { useCallback } from 'react';

/** The outcome of an action that a toast reports: it succeeded, it ended
 * without a failure but did not do what was asked (nothing matched, the
 * item is still missing metadata, a job had already ended), or it failed. */
export type Outcome = 'success' | 'notice' | 'error';

const outcomeIcons = { success: CheckCircleIcon, notice: InformationCircleIcon, error: ExclamationCircleIcon };

type NotifyOptions = Omit<ToastOptions, 'body' | 'type' | 'renderContent'>;

/** Returns a function that shows the toast of an action with an icon of
 * its outcome. An error toast keeps the error colours of Astryx. */
export function useNotify() {
  const showToast = useToast();
  return useCallback(
    (outcome: Outcome, text: string, options?: NotifyOptions) =>
      showToast({ ...options, type: outcome === 'error' ? 'error' : 'info', body: <ToastBody outcome={outcome} text={text} /> }),
    [showToast],
  );
}

function ToastBody({ outcome, text }: { outcome: Outcome; text: string }) {
  return (
    <HStack gap={2} vAlign="start">
      <Icon icon={outcomeIcons[outcome]} size="sm" />
      <StackItem size="fill">
        <Text type="inherit" color="inherit">{text}</Text>
      </StackItem>
    </HStack>
  );
}
