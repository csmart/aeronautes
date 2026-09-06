import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import { BrowserState } from './browserState.svelte';
import { newFakeApi } from './fakeApi';
import ContainerColumn from './ContainerColumn.svelte';
import WorkingList from './WorkingList.svelte';

type Kind = 'containers' | 'objects';
const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear(); document.body.innerHTML = ''; vi.restoreAllMocks();
});
async function settle() { for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); } }
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
async function fixture(kind: Kind) {
  const api = newFakeApi(); const state = new BrowserState(api);
  const bookmark = await state.saveBookmark({ id: '', displayName: 'Mock', authUrl: 'http://mock/auth/v1.0', username: 'test:tester', storageUrlOverride: '', tlsMode: 'system', caPem: '', hasCa: false, hasCredential: false }, 'testing');
  await state.connect(bookmark.id); await state.openContainer('fixtures');
  state.mode = 'all';
  state.containersMarker = 'page2'; state.entriesMarker = 'page2';
  const container = { ...state.containers[0], name: 'z-next' };
  const object = { ...state.entries.find((row) => row.kind === 'object')!, name: 'z-next' };
  const more = () => kind === 'containers' ? state.loadMoreContainers() : state.loadMoreEntries();
  const reload = () => kind === 'containers' ? state.loadContainers() : state.goTo('');
  const names = () => (kind === 'containers' ? state.containers : state.entries).map((row) => row.name);
  const marker = () => kind === 'containers' ? state.containersMarker : state.entriesMarker;
  const pageError = () => kind === 'containers' ? state.containersMoreError : state.entriesMoreError;
  const pageLoading = () => kind === 'containers' ? state.containersMoreLoading : state.entriesMoreLoading;
  const call = () => kind === 'containers' ? api.containers : api.objects;
  const setMarker = (value: string) => { if (kind === 'containers') state.containersMarker = value; else state.entriesMarker = value; };
  return { api, state, bookmark, container, object, more, reload, names, marker, pageError, pageLoading, call, setMarker };
}

describe.each(['containers', 'objects'] as Kind[])('%s pagination ownership', (kind) => {
  it('admits one pending page and appends it once', async () => {
    const f = await fixture(kind); const gate = deferred<void>();
    f.api.containers = vi.fn(async () => { await gate.promise; return { rows: [f.container], nextMarker: '' }; });
    f.api.objects = vi.fn(async () => { await gate.promise; return { rows: [f.object], nextMarker: '' }; });
    const first = f.more(); const second = f.more();
    try { expect(f.call()).toHaveBeenCalledOnce(); expect(f.pageLoading()).toBe(true); }
    finally { gate.resolve(); await Promise.allSettled([first, second]); }
    expect(f.names().filter((name) => name === 'z-next')).toHaveLength(1);
    expect(f.pageLoading()).toBe(false); expect(f.marker()).toBe('');
  });

  it('retires a page when the same initial list is reloaded with the same marker', async () => {
    const f = await fixture(kind); const gate = deferred<void>();
    f.api.containers = vi.fn(async (marker) => {
      if (marker !== '') { await gate.promise; return { rows: [f.container], nextMarker: '' }; }
      return { rows: [{ ...f.container, name: 'fresh' }], nextMarker: 'page2' };
    });
    f.api.objects = vi.fn(async (_container, _prefix, marker) => {
      if (marker !== '') { await gate.promise; return { rows: [f.object], nextMarker: '' }; }
      return { rows: [{ ...f.object, name: 'fresh' }], nextMarker: 'page2' };
    });
    const pending = f.more(); await f.reload(); gate.resolve(); await pending;
    expect(f.names()).toEqual(['fresh']); expect(f.marker()).toBe('page2');
  });

  it('keeps a current page error separate from usable rows and retries the marker', async () => {
    const f = await fixture(kind); const failure = new Error('page unavailable'); const names = f.names();
    f.api.containers = vi.fn().mockRejectedValueOnce(failure).mockResolvedValue({ rows: [f.container], nextMarker: '' });
    f.api.objects = vi.fn().mockRejectedValueOnce(failure).mockResolvedValue({ rows: [f.object], nextMarker: '' });
    await expect(f.more()).rejects.toBe(failure);
    expect(f.pageError()).toBe(failure.message); expect(f.names()).toEqual(names); expect(f.marker()).toBe('page2');
    expect(f.state.containersError).toBeNull(); expect(f.state.entriesError).toBeNull(); expect(f.pageLoading()).toBe(false);
    await f.more(); expect(f.pageError()).toBeNull(); expect(f.names()).toContain('z-next'); expect(f.marker()).toBe('');
    expect(f.call()).toHaveBeenCalledTimes(2);
  });

  it('rejects a non-advancing page before appending or consuming its marker', async () => {
    const f = await fixture(kind); const names = f.names();
    f.api.containers = vi.fn().mockResolvedValue({ rows: [f.container], nextMarker: 'page2' });
    f.api.objects = vi.fn().mockResolvedValue({ rows: [f.object], nextMarker: 'page2' });
    const outcome = await f.more().catch((error: Error) => error);
    expect(outcome).toBeInstanceOf(Error); expect(f.names()).toEqual(names); expect(f.marker()).toBe('page2');
    expect(f.pageError()).toContain('did not advance');
  });

  it.each(['success first', 'failure first', 'success last', 'failure last'])('a retired page finishes %s without changing a newer page', async (order) => {
    const f = await fixture(kind); const old = deferred<void>(); const current = deferred<void>();
    let pages = 0;
    const page = async () => { await (++pages === 1 ? old.promise : current.promise); return pages; };
    f.api.containers = vi.fn(async (marker) => {
      if (marker === '') return { rows: [{ ...f.container, name: 'fresh' }], nextMarker: 'page2' };
      const number = pages + 1; await page(); return { rows: [{ ...f.container, name: number === 1 ? 'obsolete' : 'current' }], nextMarker: '' };
    });
    f.api.objects = vi.fn(async (_container, _prefix, marker) => {
      if (marker === '') return { rows: [{ ...f.object, name: 'fresh' }], nextMarker: 'page2' };
      const number = pages + 1; await page(); return { rows: [{ ...f.object, name: number === 1 ? 'obsolete' : 'current' }], nextMarker: '' };
    });
    const previous = f.more().then(() => null, (error: Error) => error); await f.reload(); const next = f.more();
    const retire = () => order.startsWith('failure') ? old.reject(new Error('obsolete page')) : old.resolve();
    if (order.endsWith('first')) {
      retire(); expect(await previous).toBeNull();
      expect(f.pageLoading()).toBe(true); expect(f.pageError()).toBeNull(); expect(f.names()).toEqual(['fresh']);
      current.resolve(); await next;
    } else {
      current.resolve(); await next; retire(); expect(await previous).toBeNull();
    }
    expect(f.names()).toEqual(['fresh', 'current']); expect(f.marker()).toBe('');
    expect(f.pageLoading()).toBe(false); expect(f.pageError()).toBeNull();
  });

  it.each(['success', 'failure'])('retires a page whose requested marker is no longer current on %s', async (outcome) => {
    const f = await fixture(kind); const gate = deferred<void>(); const names = f.names();
    f.api.containers = vi.fn(async () => { await gate.promise; return { rows: [f.container], nextMarker: '' }; });
    f.api.objects = vi.fn(async () => { await gate.promise; return { rows: [f.object], nextMarker: '' }; });
    const pending = f.more().then(() => null, (error: Error) => error); f.setMarker('replacement');
    expect(f.pageLoading()).toBe(false);
    if (outcome === 'success') gate.resolve(); else gate.reject(new Error('old marker error'));
    expect(await pending).toBeNull(); expect(f.names()).toEqual(names); expect(f.marker()).toBe('replacement');
    expect(f.pageError()).toBeNull();
  });

  it.each(['account return', 'container return', 'prefix return', 'reconnect', 'bookmark save'])('retires page state across %s', async (change) => {
    const f = await fixture(kind); const gate = deferred<void>(); const names = f.names();
    const containers = f.api.containers; const objects = f.api.objects;
    f.api.containers = vi.fn(async (marker, limit) => {
      if (marker === '') return containers(marker, limit);
      await gate.promise; return { rows: [f.container], nextMarker: '' };
    });
    f.api.objects = vi.fn(async (container, prefix, marker, limit) => {
      if (marker === '') return objects(container, prefix, marker, limit);
      await gate.promise; return { rows: [f.object], nextMarker: '' };
    });
    const pending = f.more();
    if (change === 'account return') { f.state.connectedBookmarkId = 'different'; f.state.connectedBookmarkId = f.bookmark.id; }
    else if (change === 'container return') { f.state.containerName = 'other'; f.state.containerName = 'fixtures'; }
    else if (change === 'prefix return') { f.state.prefix = 'other/'; f.state.prefix = ''; }
    else if (change === 'reconnect') await f.state.connect(f.bookmark.id);
    else await f.state.saveBookmark(f.bookmark, 'changed');
    expect(f.pageLoading()).toBe(false); gate.resolve(); await pending;
    expect(f.names()).not.toContain('z-next'); expect(f.pageError()).toBeNull();
    if (change.endsWith('return')) expect(f.names()).toEqual(names);
  });

  it.each(['pending', 'failed'])('refuses page admission during an initial %s load even with an old marker', async (phase) => {
    const f = await fixture(kind); const gate = deferred<void>();
    f.api.containers = vi.fn(async () => { await gate.promise; return { rows: [f.container], nextMarker: '' }; });
    f.api.objects = vi.fn(async () => { await gate.promise; return { rows: [f.object], nextMarker: '' }; });
    const initial = f.reload(); f.setMarker('page2');
    if (phase === 'failed') { gate.reject(new Error('initial load failed')); await initial; }
    const more = f.more().catch((error: Error) => error);
    try { expect(f.call()).toHaveBeenCalledOnce(); }
    finally { if (phase === 'pending') gate.resolve(); await Promise.allSettled([initial, more]); }
  });

  it.each(['empty marker', 'disconnected', 'connecting'])('refuses page admission with %s', async (reason) => {
    const f = await fixture(kind); f.api.containers = vi.fn(f.api.containers); f.api.objects = vi.fn(f.api.objects);
    if (reason === 'empty marker') f.setMarker('');
    else if (reason === 'disconnected') f.state.connectedBookmarkId = null;
    else f.state.connecting = true;
    await f.more(); expect(f.call()).not.toHaveBeenCalled();
  });

  it('deduplicates overlapping rows while preserving the current selection', async () => {
    const f = await fixture(kind);
    f.state.select(f.state.entries.findIndex((row) => row.name === 'empty-object')); await settle();
    const selected = f.state.selectedEntry; const detail = f.state.objectDetail; const identity = f.state.detailIdentity;
    const firstContainer = f.state.containers[0]; const firstObject = f.state.entries[0];
    f.api.containers = vi.fn().mockResolvedValue({ rows: [firstContainer, { ...firstContainer, bytes: -1 }, f.container, f.container], nextMarker: '' });
    f.api.objects = vi.fn().mockResolvedValue({ rows: [firstObject, { ...firstObject, bytes: -1 }, f.object, f.object], nextMarker: '' });
    await f.more();
    expect(f.names().filter((name) => name === 'z-next')).toHaveLength(1);
    expect(f.names().filter((name) => name === (kind === 'containers' ? firstContainer : firstObject).name)).toHaveLength(1);
    expect(f.state.selectedEntry).toBe(selected); expect(f.state.objectDetail).toBe(detail); expect(f.state.detailIdentity).toBe(identity);
    expect(f.state.containers[0]).toBe(firstContainer); expect(f.state.entries[0]).toBe(firstObject);
  });

  it.each([
    ['a', 'b', true], ['a', 'a0', true], ['a0', 'a', false], ['z', 'a', false], ['z', 'aa', false],
    ['\uE000', '\u{10000}', true], ['\u{10000}', '\uE000', false], ['z', '', true],
  ] as const)('checks marker progression from %s to %s using UTF-8 order', async (before, after, accepted) => {
    const f = await fixture(kind); const names = f.names(); f.setMarker(before);
    f.api.containers = vi.fn().mockResolvedValue({ rows: [f.container], nextMarker: after });
    f.api.objects = vi.fn().mockResolvedValue({ rows: [f.object], nextMarker: after });
    const result = await f.more().catch((error: Error) => error);
    if (accepted) { expect(result).toBeUndefined(); expect(f.names()).toContain('z-next'); expect(f.marker()).toBe(after); }
    else { expect(result).toBeInstanceOf(Error); expect(f.names()).toEqual(names); expect(f.marker()).toBe(before); }
  });

  it('keeps the exact marker and namespace in the request', async () => {
    const f = await fixture(kind); f.setMarker('  marker ☃  '); f.state.prefix = '  prefix ☃  ';
    f.api.containers = vi.fn().mockResolvedValue({ rows: [f.container], nextMarker: '' });
    f.api.objects = vi.fn().mockResolvedValue({ rows: [f.object], nextMarker: '' });
    await f.more();
    if (kind === 'containers') expect(f.api.containers).toHaveBeenCalledExactlyOnceWith('  marker ☃  ', 1000);
    else expect(f.api.objects).toHaveBeenCalledExactlyOnceWith('fixtures', '  prefix ☃  ', '  marker ☃  ', 1000);
  });

  it('renders pending and retry controls without hiding loaded rows', async () => {
    const f = await fixture(kind); const gate = deferred<void>();
    f.api.containers = vi.fn().mockImplementationOnce(async () => { await gate.promise; return { rows: [f.container], nextMarker: '' }; }).mockResolvedValue({ rows: [f.container], nextMarker: '' });
    f.api.objects = vi.fn().mockImplementationOnce(async () => { await gate.promise; return { rows: [f.object], nextMarker: '' }; }).mockResolvedValue({ rows: [f.object], nextMarker: '' });
    const root = document.createElement('div'); document.body.append(root);
    mounted.add(kind === 'containers'
      ? mount(ContainerColumn, { target: root, props: { browser: f.state, onCreate: vi.fn() } })
      : mount(WorkingList, { target: root, props: { browser: f.state } }));
    flushSync();
    const selector = kind === 'containers' ? '.load-more' : '.more';
    const button = () => root.querySelector<HTMLButtonElement>(selector)!;
    const click = () => { button().dispatchEvent(new MouseEvent('click', { bubbles: true })); flushSync(); };
    click(); click();
    expect(f.call()).toHaveBeenCalledOnce(); expect(button().disabled).toBe(true);
    expect(button().textContent).toContain('Loading'); expect(button().getAttribute('aria-busy')).toBe('true');
    gate.reject(new Error('visible paging failure')); await settle();
    expect(root.querySelector('[role="alert"]')?.textContent).toContain('visible paging failure');
    expect(root.textContent).toContain(kind === 'containers' ? 'fixtures' : 'empty-object');
    expect(button().disabled).toBe(false); expect(button().textContent).toContain('Retry');
    click(); expect(root.querySelector('[role="alert"]')).toBeNull(); expect(button().disabled).toBe(true);
    await settle(); expect(f.call()).toHaveBeenCalledTimes(2);
    expect(root.querySelector('[role="alert"]')).toBeNull(); expect(root.textContent).toContain('z-next');
    expect(root.querySelector(selector)).toBeNull();
  });
});

it('does not conflate an object and virtual prefix sharing a name', async () => {
  const f = await fixture('objects');
  const object = { ...f.object, name: 'same/', kind: 'object' };
  const prefix = { ...f.object, name: 'same/', kind: 'prefix' };
  f.state.entries = [object];
  f.api.objects = vi.fn().mockResolvedValue({ rows: [object, prefix, prefix], nextMarker: '' });
  await f.more(); expect(f.state.entries.map((row) => [row.kind, row.name])).toEqual([['object', 'same/'], ['prefix', 'same/']]);
});

it('an obsolete initial container load cannot leave a later scope or load busy', async () => {
  const f = await fixture('containers'); const old = deferred<void>(); const current = deferred<void>();
  f.api.containers = vi.fn().mockImplementationOnce(async () => { await old.promise; return { rows: [], nextMarker: '' }; })
    .mockImplementationOnce(async () => { await current.promise; return { rows: [f.container], nextMarker: '' }; });
  const previous = f.reload(); expect(f.state.containersLoading).toBe(true);
  f.state.prefix = 'other/'; expect(f.state.containersLoading).toBe(false);
  const next = f.reload(); old.resolve(); await previous; expect(f.state.containersLoading).toBe(true);
  current.resolve(); await next; expect(f.state.containersLoading).toBe(false); expect(f.names()).toEqual(['z-next']);
});
