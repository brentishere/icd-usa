/**
 * ScriptCat background dispatcher — the single supported task-execution
 * runtime's scheduler half. Runs continuously in the userscript manager's
 * background context, claims tasks from the server, and dispatches each one
 * to a background tab opened at the rule's entry URL with a `#scrape=`
 * descriptor; the page-side executor userscript (aegiscrawler-*.user.js)
 * picks the descriptor up and executes the rule.
 *
 * Configuration lives in THIS script's GM storage (managers scope GM storage
 * per script — the executor script keeps its own trustedServerOrigins and
 * workerApiKey):
 *   GM_setValue('serverUrl', 'http://127.0.0.1:8080')
 *   GM_setValue('workerApiKey', '<WORKER_API_KEY>')
 *   GM_setValue('workerId', 'scriptcat-1')            // must match the
 *     workerId used by the executor script's descriptor flow (it does: this
 *     dispatcher writes its workerId into every descriptor it dispatches)
 *   GM_setValue('browserProfileId', 'profile-name')   // optional affinity
 *   GM_setValue('pollIntervalMs', 5000)               // optional, default 5s
 *
 * The executor script additionally requires the server origin in its own
 * trustedServerOrigins list before it will run a descriptor.
 */

import { interpolate, utf8ToBase64 } from './utils';

declare const GM_xmlhttpRequest: any;
declare const GM_openInTab: any;
declare const GM_getValue: (key: string) => any;
declare const GM_setValue: (key: string, value: any) => void;

const DEFAULT_POLL_INTERVAL_MS = 5_000;
/** Cap on waiting for a dispatched task to reach a terminal state before the
 *  dispatcher moves on; the executor's 30s heartbeats keep the lease alive,
 *  so this only guards a silently dead tab (lease expiry handles retries). */
const TASK_COMPLETION_TIMEOUT_MS = 30 * 60 * 1_000;
const TASK_STATUS_POLL_MS = 2_000;

interface DispatcherConfig {
  serverUrl: string;
  workerApiKey?: string;
  workerId: string;
  browserProfileId?: string;
  pollIntervalMs: number;
}

interface ClaimedTask {
  taskId: string;
  attemptId?: string;
  ruleId: string;
  rule?: RuleLike;
  variables?: Record<string, any>;
}

interface RuleLike {
  id?: string;
  entry?: string | { url?: string };
  domain?: string | string[];
  variables?: Record<string, any>;
}

class DispatcherHttpError extends Error {
  constructor(message: string, public status: number) {
    super(message);
    this.name = 'DispatcherHttpError';
  }
}

function log(level: 'info' | 'warn' | 'error', message: string): void {
  const tag = '[AegisCrawler Dispatcher]';
  if (level === 'error') console.error(tag, message);
  else if (level === 'warn') console.warn(tag, message);
  else console.log(tag, message);
}

function gmRequest(method: 'GET' | 'POST', url: string, data?: any, authKey?: string): Promise<any> {
  return new Promise((resolve, reject) => {
    const headers: Record<string, string> = {};
    if (authKey) headers['Authorization'] = `Bearer ${authKey}`;
    let body: string | undefined;
    if (data !== undefined) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(data);
    }
    GM_xmlhttpRequest({
      method,
      url,
      headers,
      data: body,
      timeout: 30_000,
      onload: (resp: any) => {
        if (resp.status >= 200 && resp.status < 300) {
          resolve(resp.responseText ? JSON.parse(resp.responseText) : null);
        } else if (resp.status === 204) {
          resolve(null);
        } else {
          reject(new DispatcherHttpError(`${method} ${url} -> ${resp.status}`, resp.status));
        }
      },
      onerror: () => reject(new Error(`${method} ${url} network error`)),
      ontimeout: () => reject(new Error(`${method} ${url} timeout`)),
    });
  });
}

function readConfig(): DispatcherConfig | null {
  const serverUrl = String(GM_getValue?.('serverUrl') ?? '').trim().replace(/\/+$/, '');
  if (!serverUrl) return null;
  const workerId = String(GM_getValue?.('workerId') ?? '').trim();
  if (!workerId) return null;
  const apiKey = String(GM_getValue?.('workerApiKey') ?? '').trim();
  const profile = String(GM_getValue?.('browserProfileId') ?? '').trim();
  const pollRaw = Number(GM_getValue?.('pollIntervalMs'));
  return {
    serverUrl,
    workerApiKey: apiKey || undefined,
    workerId,
    browserProfileId: profile || undefined,
    pollIntervalMs: Number.isFinite(pollRaw) && pollRaw >= 1000 ? pollRaw : DEFAULT_POLL_INTERVAL_MS,
  };
}

/** Resolves and validates the entry URL exactly like the worker page driver:
 *  http(s) only, interpolated against rule+task variables, and the resulting
 *  host must be inside rule.domain before any tab is opened. */
function resolveEntryUrl(rule: RuleLike, variables: Record<string, any>): string {
  const raw = typeof rule.entry === 'string' ? rule.entry : rule.entry?.url;
  if (!raw) throw new Error(`Rule ${rule.id ?? '?'} has no entry URL`);
  const resolved = String(interpolate(raw, {
    variables: { ...(rule.variables ?? {}), ...variables },
    extracted: {},
    evaluated: {},
    captured: {},
  }));
  let parsed: URL;
  try {
    parsed = new URL(resolved);
  } catch {
    throw new Error(`Rule ${rule.id ?? '?'} entry URL is not absolute: ${resolved}`);
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error(`Rule ${rule.id ?? '?'} entry URL scheme ${parsed.protocol} is not http(s)`);
  }
  const domains = (Array.isArray(rule.domain) ? rule.domain : [rule.domain])
    .filter((d): d is string => typeof d === 'string' && d.length > 0);
  const host = parsed.hostname;
  const matched = domains.some((d: string) => host === d || host.endsWith('.' + d));
  if (!matched) {
    throw new Error(`entry URL host ${host} is outside rule.domain (${domains.join(', ')})`);
  }
  return parsed.toString();
}

function encodeDescriptor(task: ClaimedTask, cfg: DispatcherConfig): string {
  const descriptor = {
    taskId: task.taskId,
    ruleId: task.ruleId,
    serverUrl: cfg.serverUrl,
    workerId: cfg.workerId,
    attemptId: task.attemptId,
    closeOnDone: true,
    variables: task.variables ?? {},
  };
  // Mirror environment.ts: atob(decodeURIComponent(match[1]))) with UTF-8-safe
  // base64 — task variables carry arbitrary Unicode.
  return encodeURIComponent(utf8ToBase64(JSON.stringify(descriptor)));
}

async function claimTask(cfg: DispatcherConfig): Promise<ClaimedTask | null> {
  const body: Record<string, string> = { workerId: cfg.workerId };
  if (cfg.browserProfileId) body.browserProfileId = cfg.browserProfileId;
  return gmRequest('POST', `${cfg.serverUrl}/tasks/claim`, body, cfg.workerApiKey);
}

async function fetchRule(cfg: DispatcherConfig, ruleId: string): Promise<RuleLike> {
  return gmRequest('GET', `${cfg.serverUrl}/rules/${encodeURIComponent(ruleId)}`, undefined, cfg.workerApiKey);
}

async function sendStatus(cfg: DispatcherConfig, taskId: string, status: 'failed' | 'cancelled', message: string): Promise<void> {
  try {
    // Mirrors the executor transport: single root /status endpoint with the
    // taskId inside the body.
    await gmRequest('POST', `${cfg.serverUrl}/status`, {
      taskId,
      workerId: cfg.workerId,
      status,
      message,
      timestamp: Date.now(),
    }, cfg.workerApiKey);
  } catch (e) {
    log('error', `terminal status ${status} for ${taskId} failed: ${String((e as Error)?.message ?? e)}`);
  }
}

async function heartbeatOnce(cfg: DispatcherConfig, taskId: string): Promise<void> {
  // Bridge the gap between dispatch and the executor's own 30s heartbeat
  // loop so a slow page load cannot expire the lease.
  try {
    await gmRequest('POST', `${cfg.serverUrl}/heartbeat`, {
      taskId,
      workerId: cfg.workerId,
      payload: { dispatcher: true },
      timestamp: Date.now(),
    }, cfg.workerApiKey);
  } catch { /* transient; the executor takes over heartbeats on boot */ }
}

async function fetchTaskStatus(cfg: DispatcherConfig, taskId: string): Promise<string | null> {
  try {
    const task = await gmRequest('GET', `${cfg.serverUrl}/tasks/${encodeURIComponent(taskId)}`, undefined, cfg.workerApiKey);
    return typeof task?.status === 'string' ? task.status : null;
  } catch {
    return null;
  }
}

async function waitTerminal(cfg: DispatcherConfig, taskId: string): Promise<void> {
  const deadline = Date.now() + TASK_COMPLETION_TIMEOUT_MS;
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, TASK_STATUS_POLL_MS));
    const status = await fetchTaskStatus(cfg, taskId);
    if (status === 'done' || status === 'failed' || status === 'cancelled') return;
  }
  log('warn', `task ${taskId} did not reach a terminal state within the wait cap; lease expiry will retry`);
}

async function dispatchTask(task: ClaimedTask, cfg: DispatcherConfig): Promise<void> {
  let rule = task.rule;
  if (!rule) {
    try {
      rule = await fetchRule(cfg, task.ruleId);
    } catch (e) {
      await sendStatus(cfg, task.taskId, 'failed', `rule fetch failed: ${String((e as Error)?.message ?? e)}`);
      return;
    }
  }
  let entryUrl: string;
  try {
    entryUrl = resolveEntryUrl(rule, task.variables ?? {});
  } catch (e) {
    await sendStatus(cfg, task.taskId, 'failed', String((e as Error)?.message ?? e));
    return;
  }
  const url = `${entryUrl}#scrape=${encodeDescriptor(task, cfg)}`;
  const tab = typeof GM_openInTab === 'function' ? GM_openInTab(url, { active: false }) : null;
  if (!tab) {
    await sendStatus(cfg, task.taskId, 'failed', 'dispatcher could not open an execution tab (GM_openInTab unavailable or blocked)');
    return;
  }
  log('info', `dispatched ${task.taskId} -> ${entryUrl}`);
  await heartbeatOnce(cfg, task.taskId);
  await waitTerminal(cfg, task.taskId);
}

async function runDispatcherLoop(): Promise<void> {
  for (;;) {
    const cfg = readConfig();
    if (!cfg) {
      log('warn', 'not configured (GM keys serverUrl/workerId missing); retrying in 30s');
      await new Promise((r) => setTimeout(r, 30_000));
      continue;
    }
    try {
      const task = await claimTask(cfg);
      if (!task) {
        await new Promise((r) => setTimeout(r, cfg.pollIntervalMs));
        continue;
      }
      log('info', `claimed ${task.taskId} (rule ${task.ruleId})`);
      await dispatchTask(task, cfg);
    } catch (e) {
      if (e instanceof DispatcherHttpError && e.status === 204) {
        await new Promise((r) => setTimeout(r, cfg!.pollIntervalMs));
        continue;
      }
      log('error', `claim loop error: ${String((e as Error)?.message ?? e)}`);
      await new Promise((r) => setTimeout(r, 10_000));
    }
  }
}

function bootDispatcher(): void {
  log('info', 'background dispatcher starting');
  runDispatcherLoop().catch((e) => {
    log('error', `dispatcher loop crashed: ${String((e as Error)?.message ?? e)}`);
  });
}

if (typeof GM_getValue === 'function') {
  bootDispatcher();
} else {
  console.error('[AegisCrawler Dispatcher] userscript manager GM APIs unavailable');
}

export { resolveEntryUrl, encodeDescriptor, readConfig };
