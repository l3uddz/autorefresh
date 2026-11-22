import { ApiError } from './api';

/** Returns the toast text of a failed item action. */
export function actionError(error: Error): string {
  if (error instanceof ApiError) {
    switch (error.status) {
      case 409:
        return error.message === 'item not cached' ? 'Autorefresh no longer tracks this item.' : 'This item is busy with another request. Try again in a moment.';
      case 404:
        return error.message === 'not found in Plex' ? 'Plex no longer has this item. It was deleted or moved.' : 'Autorefresh no longer knows this item. Its history may have expired.';
      case 502:
        return `${error.message}. Try again later.`;
      case 504:
        return 'Plex did not answer in time. The request may still have reached Plex.';
    }
  }
  return error.message;
}
