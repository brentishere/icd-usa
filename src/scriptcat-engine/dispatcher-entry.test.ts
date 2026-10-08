import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { resolveEntryUrl, encodeDescriptor } from './dispatcher-entry';
import { base64ToUtf8 } from './utils';

// The dispatcher entry self-starts on import when GM APIs exist; stub them as
// absent so the module stays inert, then unit-test the pure helpers.
vi.stubGlobal('GM_getValue', undefined);
vi.stubGlobal('GM_setValue', undefined);
vi.stubGlobal('GM_xmlhttpRequest', undefined);
vi.stubGlobal('GM_openInTab', undefined);

describe('dispatcher helpers', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.stubGlobal('GM_getValue', (key: string) => {
      const store: Record<string, unknown> = {
        serverUrl: 'http://127.0.0.1:8080/',
        workerId: 'scriptcat-test',
        workerApiKey: 'secret',
        browserProfileId: 'profile-a',
        pollIntervalMs: 3000,
      };
      return store[key];
    });
    vi.stubGlobal('console', { log: vi.fn(), warn: vi.fn(), error: vi.fn() });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('resolves and validates entry URLs against rule.domain', () => {
    const rule = {
      id: 'r1',
      entry: 'https://example.test/search?q={{variables.keyword}}',
      domain: 'example.test',
      variables: { keyword: 'default' },
    };
    expect(resolveEntryUrl(rule, { keyword: 'hello' })).toBe('https://example.test/search?q=hello');
    // Rule defaults apply when the task omits the variable.
    expect(resolveEntryUrl(rule, {})).toBe('https://example.test/search?q=default');
  });

  it('rejects non-http schemes', () => {
    expect(() => resolveEntryUrl({ entry: 'javascript:alert(1)', domain: 'example.test' }, {})).toThrow(/http/);
    expect(() => resolveEntryUrl({ entry: 'file:///etc/passwd', domain: 'example.test' }, {})).toThrow(/http/);
  });

  it('rejects hosts outside rule.domain (exact or suffix)', () => {
    expect(() => resolveEntryUrl({ entry: 'https://evil.test/x', domain: 'example.test' }, {})).toThrow(/outside rule\.domain/);
    expect(() => resolveEntryUrl({ entry: 'https://example.test.evil.test/x', domain: 'example.test' }, {})).toThrow(/outside rule\.domain/);
    // Suffix subdomains are allowed; lookalikes are not.
    expect(resolveEntryUrl({ entry: 'https://sub.example.test/x', domain: 'example.test' }, {})).toBe('https://sub.example.test/x');
    expect(() => resolveEntryUrl({ entry: 'https://notexample.test/x', domain: 'example.test' }, {})).toThrow(/outside/);
  });

  it('supports array domains', () => {
    expect(resolveEntryUrl({ entry: 'https://b.test/x', domain: ['a.test', 'b.test'] }, {})).toBe('https://b.test/x');
    expect(() => resolveEntryUrl({ entry: 'https://c.test/x', domain: ['a.test', 'b.test'] }, {})).toThrow(/outside/);
  });

  it('rejects relative entry URLs', () => {
    expect(() => resolveEntryUrl({ entry: '/relative/path', domain: 'example.test' }, {})).toThrow(/absolute/);
  });

  it('encodes descriptors the executor can decode (including Unicode variables)', () => {
    const task = { taskId: 't1', ruleId: 'r1', variables: { keyword: '招投标' } };
    const cfg = { serverUrl: 'http://127.0.0.1:8080', workerId: 'w1', pollIntervalMs: 5000 };
    const encoded = encodeDescriptor(task, cfg);
    const decoded = JSON.parse(base64ToUtf8(decodeURIComponent(encoded)));
    expect(decoded).toMatchObject({
      taskId: 't1',
      ruleId: 'r1',
      serverUrl: 'http://127.0.0.1:8080',
      workerId: 'w1',
      closeOnDone: true,
      variables: { keyword: '招投标' },
    });
  });

  it('reads config with normalized server URL and defaults', async () => {
    const mod = await import('./dispatcher-entry');
    const cfg = (mod as unknown as { readConfig?: () => unknown }).readConfig?.();
    expect(cfg).toMatchObject({
      serverUrl: 'http://127.0.0.1:8080',
      workerId: 'scriptcat-test',
      workerApiKey: 'secret',
      browserProfileId: 'profile-a',
      pollIntervalMs: 3000,
    });
  });
});
