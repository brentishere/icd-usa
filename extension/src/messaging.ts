export const MESSAGE_RETRY_ATTEMPTS = 6;
export const MESSAGE_RETRY_DELAY_MS = 250;
/** Cap the linear backoff: after extension reloads the MV3 worker cold
 *  start of the (large) background bundle can take a couple of seconds, and
 *  the popup's first GET_STATE must outlive it instead of surfacing
 *  "Could not establish connection. Receiving end does not exist." */
export const MESSAGE_RETRY_MAX_DELAY_MS = 1000;

const TRANSIENT_CONNECTION_ERRORS = [
  'could not establish connection',
  'receiving end does not exist',
  'the message port closed',
  'the frame was removed',
];

function isTransientConnectionError(err: unknown): boolean {
  const message = err instanceof Error ? err.message : String(err);
  const lower = message.toLowerCase();
  return TRANSIENT_CONNECTION_ERRORS.some((phrase) => lower.includes(phrase));
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Send a runtime message to the background service worker, retrying a few
 * times when the service worker is temporarily unavailable (common in MV3
 * when the worker has been terminated and needs to restart).
 */
function assertRuntimeAvailable(): void {
  if (
    typeof chrome === 'undefined' ||
    typeof chrome.runtime === 'undefined' ||
    typeof chrome.runtime.sendMessage !== 'function'
  ) {
    throw new Error('Chrome extension runtime is not available in this page. Ensure the page is opened from the extension.');
  }
}

export async function sendAction(action: string, payload?: unknown): Promise<unknown> {
  assertRuntimeAvailable();
  let lastErr: unknown;
  for (let attempt = 0; attempt < MESSAGE_RETRY_ATTEMPTS; attempt++) {
    try {
      return await chrome.runtime.sendMessage({ action, payload });
    } catch (err) {
      lastErr = err;
      if (!isTransientConnectionError(err) || attempt === MESSAGE_RETRY_ATTEMPTS - 1) {
        throw err;
      }
      await delay(Math.min(MESSAGE_RETRY_DELAY_MS * (attempt + 1), MESSAGE_RETRY_MAX_DELAY_MS));
    }
  }
  throw lastErr;
}
