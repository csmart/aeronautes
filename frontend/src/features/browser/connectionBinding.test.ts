import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import { SvelteMap } from 'svelte/reactivity';
import type { BookmarkDTO } from '../../lib/api';
import { BrowserState } from './browserState.svelte';
import { newFakeApi } from './fakeApi';
import BookmarkEditor from './BookmarkEditor.svelte';
import FindPanel from './FindPanel.svelte';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
// Save & connect crosses two sequential bridge workflows before its UI callback.
async function settle() { for (let i = 0; i < 24; i++) { await Promise.resolve(); flushSync(); } }
async function connected() {
  const api = newFakeApi(); const state = new BrowserState(api);
  const bookmark = await state.saveBookmark({ id: '', displayName: 'Mock', authUrl: 'http://mock/auth/v1.0', username: 'test:tester', storageUrlOverride: '', tlsMode: 'system', caPem: '', hasCa: false, hasCredential: false }, 'testing');
  await state.connect(bookmark.id); await state.openContainer('fixtures');
  return { api, state, bookmark };
}
const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear(); document.body.innerHTML = ''; vi.restoreAllMocks();
});

it('clear account information when the active bookmark is deleted', async () => {
  const {state,bookmark} = await connected();
  await state.deleteBookmark(bookmark.id);
  expect(state.account).toBeNull();
});

it('an active bookmark save immediately disconnects the old view', async () => {
  const f = await connected(); const held = deferred<typeof f.bookmark>();
  f.api.saveBookmark = vi.fn(() => held.promise);
  const saving = f.state.saveBookmark(f.bookmark, 'changed');
  try { expect(f.state.account).toBeNull(); expect(f.state.connectedBookmarkId).toBeNull(); }
  finally { held.resolve(f.bookmark); await saving; }
});

it('only one Connect bridge call may remain in flight', async () => {
  const f = await connected(); const account = f.state.account!;
  const held = deferred<typeof account>(); f.api.connect = vi.fn(() => held.promise);
  const first = f.state.connect(f.bookmark.id);
  const second = f.state.connect(f.bookmark.id).catch(() => undefined);
  try { expect(f.api.connect).toHaveBeenCalledOnce(); }
  finally { held.resolve(account); await Promise.allSettled([first, second]); }
});

it('a closed bookmark editor cannot continue Save and connect', async () => {
  const f = await connected(); const held = deferred<typeof f.bookmark>();
  f.api.saveBookmark = vi.fn(() => held.promise); f.api.connect = vi.fn(f.api.connect);
  const root = document.createElement('div'); document.body.append(root);
  const closed = vi.fn();
  const editor = mount(BookmarkEditor, { target: root, props: { browser: f.state, existing: f.bookmark, onClose: closed } });
  flushSync(); root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  flushSync(); await unmount(editor);
  held.resolve(f.bookmark); await settle();
  expect(f.api.connect).not.toHaveBeenCalled(); expect(closed).not.toHaveBeenCalled();
});

function expectDisconnected(state: BrowserState) {
  expect({
    bookmark: state.connectedBookmarkId, account: state.account, capabilities: state.capabilities,
    containers: state.containers, containersMarker: state.containersMarker, containersError: state.containersError,
    container: state.containerName, containerDetail: state.containerDetail, backstage: state.backstageFrom,
    prefix: state.prefix, filter: state.filter, entries: state.entries, marker: state.entriesMarker,
    entriesLoading: state.entriesLoading, entriesError: state.entriesError, selected: state.selectedIndex,
    identity: state.detailIdentity, object: state.objectDetail, prefixDetail: state.prefixDetail,
    slo: state.sloManifest, dlo: state.dloSegments, detailLoading: state.detailLoading, detailError: state.detailError,
    transfer: state.transferOpen, local: state.localTransfer, deletion: state.deleteOpen,
    findOpen: state.findOpen, findRunning: state.findRunning, findStatus: state.findStatus,
  }).toEqual({
    bookmark: null, account: null, capabilities: null, containers: [], containersMarker: '', containersError: null,
    container: null, containerDetail: null, backstage: null, prefix: '', filter: '', entries: [], marker: '',
    entriesLoading: false, entriesError: null, selected: -1, identity: null, object: null, prefixDetail: null,
    slo: null, dlo: null, detailLoading: false, detailError: null,
    transfer: false, local: null, deletion: false, findOpen: false, findRunning: false, findStatus: null,
  });
}
async function populate(state: BrowserState) {
  state.select(state.entries.findIndex((row) => row.name === 'external-dlo')); await settle();
  expect(state.dloSegments).not.toBeNull();
  state.containersMarker = 'more-containers'; state.entriesMarker = 'more-objects';
  state.entriesError = 'old list error'; state.detailError = 'old detail error';
  state.entriesLoading = true; state.detailLoading = true; state.backstageFrom = 'old manifest';
  state.filter = 'external'; state.prefix = 'old/';
  state.transferOpen = true; state.localTransfer = 'download'; state.deleteOpen = true; state.findOpen = true;
  state.findRunning = true;
  state.findStatus = { scanned: 1, matches: [{ name: 'old/match', bytes: 1 }], done: false, truncated: false, error: '', objectsPerSecond: 1 };
}

describe.each(['save', 'delete'] as const)('bookmark %s invalidation', (operation) => {
  it.each(['success', 'failure'])('retires the full active view at admission and keeps it retired on %s', async (outcome) => {
    const f = await connected(); await populate(f.state);
    const gate = deferred<void>(); const failure = new Error('uncertain bookmark result');
    f.api.saveBookmark = vi.fn(async () => { await gate.promise; return f.bookmark; });
    f.api.deleteBookmark = vi.fn(() => gate.promise); f.api.credentialStatus = vi.fn(f.api.credentialStatus);
    const pending = (operation === 'save' ? f.state.saveBookmark(f.bookmark, 'changed') : f.state.deleteBookmark(f.bookmark.id))
      .then(() => null, (error: Error) => error);
    expectDisconnected(f.state);
    if (outcome === 'success') gate.resolve(); else gate.reject(failure);
    expect(await pending).toBe(outcome === 'success' ? null : failure); await settle();
    expectDisconnected(f.state); expect(f.api.credentialStatus).toHaveBeenCalledOnce();
  });

  it.each(['success', 'failure'])('an inactive bookmark %s preserves the active view', async (outcome) => {
    const f = await connected(); const account = f.state.account; const scope = f.state.scopeRevision;
    const gate = deferred<void>(); const failure = new Error('inactive failure');
    const other = { ...f.bookmark, id: 'inactive' };
    f.api.saveBookmark = vi.fn(async () => { await gate.promise; return other; });
    f.api.deleteBookmark = vi.fn(() => gate.promise);
    const pending = (operation === 'save' ? f.state.saveBookmark(other, 'changed') : f.state.deleteBookmark(other.id))
      .then(() => null, (error: Error) => error);
    expect(f.state.account).toBe(account); expect(f.state.scopeRevision).toBe(scope);
    if (outcome === 'success') gate.resolve(); else gate.reject(failure);
    expect(await pending).toBe(outcome === 'success' ? null : failure);
    expect(f.state.account).toBe(account); expect(f.state.connectedBookmarkId).toBe(f.bookmark.id);
    expect(f.state.containerName).toBe('fixtures'); expect(f.state.scopeRevision).toBe(scope);
  });

  it('cannot reconnect to a bookmark whose change has not settled', async () => {
    const f = await connected(); const gate = deferred<void>();
    f.api.saveBookmark = vi.fn(async () => { await gate.promise; return f.bookmark; });
    f.api.deleteBookmark = vi.fn(() => gate.promise); f.api.connect = vi.fn(f.api.connect);
    const pending = operation === 'save' ? f.state.saveBookmark(f.bookmark, 'changed') : f.state.deleteBookmark(f.bookmark.id);
    const attempt = f.state.connect(f.bookmark.id).catch((error: Error) => error);
    expect(f.api.connect).not.toHaveBeenCalled(); expect(await attempt).toBeInstanceOf(Error);
    expect(f.state.account).toBeNull(); gate.resolve(); await pending;
    expect(await f.state.connect(f.bookmark.id)).toBe(true); expect(f.api.connect).toHaveBeenCalledOnce();
  });
});

it('keeps the bookmark change gate until every overlapping mutation settles', async () => {
  const f = await connected(); const gates = [deferred<void>(), deferred<void>()];
  f.api.saveBookmark = vi.fn().mockImplementationOnce(async () => { await gates[0].promise; return f.bookmark; })
    .mockImplementationOnce(async () => { await gates[1].promise; return f.bookmark; });
  f.api.connect = vi.fn(f.api.connect);
  const first = f.state.saveBookmark(f.bookmark, 'one'); const second = f.state.saveBookmark(f.bookmark, 'two');
  gates[0].resolve(); await first;
  const attempt = f.state.connect(f.bookmark.id).catch((error: Error) => error);
  expect(f.api.connect).not.toHaveBeenCalled(); expect(await attempt).toBeInstanceOf(Error);
  gates[1].resolve(); await second;
  expect(await f.state.connect(f.bookmark.id)).toBe(true); expect(f.api.connect).toHaveBeenCalledOnce();
});

type Stage = 'connect' | 'capabilities' | 'containers' | 'audit';
async function heldConnection(stage: Stage) {
  const f = await connected(); await populate(f.state);
  const gate = deferred<void>();
  const account = f.state.account!; const capabilities = f.state.capabilities!;
  const containers = await f.api.containers('', 1000);
  f.api.connect = vi.fn(async () => { if (stage === 'connect') await gate.promise; return account; });
  f.api.capabilities = vi.fn(async () => { if (stage === 'capabilities') await gate.promise; return capabilities; });
  f.api.containers = vi.fn(async () => { if (stage === 'containers') await gate.promise; return containers; });
  f.api.auditRecent = vi.fn(async () => { if (stage === 'audit') await gate.promise; return []; });
  const pending = f.state.connect(f.bookmark.id).then((value) => value, (error: Error) => error);
  await settle();
  return { ...f, gate, account, capabilities, containers, pending };
}

describe.each(['connect', 'capabilities', 'containers', 'audit'] as Stage[])('Connect at %s', (stage) => {
  it('holds one bridge slot and publishes one complete current snapshot', async () => {
    const f = await heldConnection(stage);
    expectDisconnected(f.state); expect(f.state.connecting).toBe(true);
    const duplicate = f.state.connect('other').catch((error: Error) => error);
    expect(f.api.connect).toHaveBeenCalledOnce(); expect(await duplicate).toBeInstanceOf(Error);
    f.gate.resolve(); expect(await f.pending).toBe(true);
    expect(f.state.account).toEqual(f.account); expect(f.state.capabilities).toEqual(f.capabilities);
    expect(f.state.containers).toEqual(f.containers.rows); expect(f.state.connectedBookmarkId).toBe(f.bookmark.id);
    expect(f.state.connecting).toBe(false); expect(f.state.connectError).toBeNull();
  });

  it.each(['save', 'delete'])('an active %s retires publication but cannot release the pending slot', async (operation) => {
    const f = await heldConnection(stage);
    if (operation === 'save') await f.state.saveBookmark(f.bookmark, 'changed');
    else await f.state.deleteBookmark(f.bookmark.id);
    const duplicate = f.state.connect('other').catch((error: Error) => error);
    expect(f.api.connect).toHaveBeenCalledOnce(); expect(await duplicate).toBeInstanceOf(Error);
    expectDisconnected(f.state); expect(f.state.connecting).toBe(true);
    f.gate.resolve(); expect(await f.pending).toBe(false);
    expectDisconnected(f.state); expect(f.state.connecting).toBe(false);
    expect(f.api.capabilities).toHaveBeenCalledTimes(stage === 'connect' ? 0 : 1);
    expect(f.api.containers).toHaveBeenCalledTimes(['containers', 'audit'].includes(stage) ? 1 : 0);
    expect(f.api.auditRecent).toHaveBeenCalledTimes(stage === 'audit' ? 1 : 0);
    expect(await f.state.connect('other')).toBe(true); expect(f.api.connect).toHaveBeenCalledTimes(2);
  });

  it('handles a current failure without inventing a successful optional read', async () => {
    const f = await heldConnection(stage); const failure = new Error('current read failed');
    f.gate.reject(failure);
    if (stage === 'connect') {
      expect(await f.pending).toBe(failure); expectDisconnected(f.state);
      expect(f.state.connectError).toBe(failure.message);
    } else {
      expect(await f.pending).toBe(true); expect(f.state.account).toEqual(f.account);
      if (stage === 'capabilities') expect(f.state.capabilities).toBeNull();
      if (stage === 'containers') { expect(f.state.containers).toEqual([]); expect(f.state.containersError).toBe(failure.message); }
    }
    expect(f.state.connecting).toBe(false);
  });
});

describe('reset-crossing reads', () => {
  it.each(['containers', 'objects'] as const)('retires a pending initial %s reply after an active save', async (kind) => {
    const f = await connected(); const gate = deferred<void>();
    const containers = await f.api.containers('', 1000); const objects = await f.api.objects('fixtures', '', '', 1000);
    f.api.containers = vi.fn(async () => { await gate.promise; return containers; });
    f.api.objects = vi.fn(async () => { await gate.promise; return objects; });
    const pending = kind === 'containers' ? f.state.loadContainers() : f.state.goTo('');
    await f.state.saveBookmark(f.bookmark, 'changed');
    gate.resolve(); await pending; expectDisconnected(f.state);
  });

  it.each(['containers', 'objects'] as const)('retires %s detail after an active save', async (kind) => {
    const f = await connected(); const gate = deferred<void>();
    const container = f.state.containerDetail!; const object = await f.api.objectDetail('fixtures', 'external-dlo');
    f.api.containerDetail = vi.fn(async () => { await gate.promise; return container; });
    f.api.objectDetail = vi.fn(async () => { await gate.promise; return object; });
    f.api.dloSegments = vi.fn(f.api.dloSegments);
    const pending = kind === 'containers' ? f.state.refreshContainerDetail() : Promise.resolve(f.state.select(f.state.entries.findIndex((row) => row.name === 'external-dlo')));
    await f.state.saveBookmark(f.bookmark, 'changed');
    gate.resolve(); await pending; await settle(); expectDisconnected(f.state);
    expect(f.api.dloSegments).not.toHaveBeenCalled();
  });

  it.each(['containers', 'objects'] as const)('retires pending %s pages on success or failure', async (kind) => {
    for (const failure of [false, true]) {
      const f = await connected(); const gate = deferred<void>();
      const containers = await f.api.containers('', 1000); const objects = await f.api.objects('fixtures', '', '', 1000);
      f.state.containersMarker = 'more'; f.state.entriesMarker = 'more';
      f.api.containers = vi.fn(async () => { await gate.promise; return containers; });
      f.api.objects = vi.fn(async () => { await gate.promise; return objects; });
      const pending = (kind === 'containers' ? f.state.loadMoreContainers() : f.state.loadMoreEntries()).then(() => null, (error: Error) => error);
      await f.state.saveBookmark(f.bookmark, 'changed');
      if (failure) gate.reject(new Error('obsolete page error')); else gate.resolve();
      expect(await pending).toBeNull(); expectDisconnected(f.state);
    }
  });

  it.each(['containers', 'objects'] as const)('preserves a current %s page failure for its existing caller', async (kind) => {
    const f = await connected(); const failure = new Error('current page error');
    f.state.containersMarker = 'more'; f.state.entriesMarker = 'more';
    f.api.containers = vi.fn().mockRejectedValue(failure); f.api.objects = vi.fn().mockRejectedValue(failure);
    await expect(kind === 'containers' ? f.state.loadMoreContainers() : f.state.loadMoreEntries()).rejects.toBe(failure);
  });

  it.each(['container', 'prefix'])('retires backstage navigation and its label during the %s load', async (stage) => {
    const f = await connected(); const gate = deferred<void>(); const objects = f.api.objects;
    f.api.objects = vi.fn(async (container, prefix, marker, limit) => {
      if (prefix === (stage === 'container' ? '' : 'segments/')) await gate.promise;
      return objects(container, prefix, marker, limit);
    });
    const pending = f.state.openBackstage('photos_segments', 'segments/', 'old manifest'); await settle();
    const calls = vi.mocked(f.api.objects).mock.calls.length;
    await f.state.saveBookmark(f.bookmark, 'changed'); gate.resolve(); await pending;
    expectDisconnected(f.state); expect(f.api.objects).toHaveBeenCalledTimes(calls);
  });
});

const finishedFind = { scanned: 1, matches: [{ name: 'old/match', bytes: 1 }], done: true, truncated: false, error: '', objectsPerSecond: 1 };

describe('Find across connection reset', () => {
  it('cancels a late startup id without installing or polling it', async () => {
    const f = await connected(); const gate = deferred<string>();
    f.api.startFind = vi.fn(() => gate.promise); f.api.findStatus = vi.fn().mockResolvedValue(finishedFind); f.api.cancelFind = vi.fn().mockResolvedValue(undefined);
    const finding = f.state.startFind('old'); await settle(); expect(f.api.startFind).toHaveBeenCalledOnce();
    await f.state.saveBookmark(f.bookmark, 'changed'); gate.resolve('old-scan'); await finding;
    expect(f.api.findStatus).not.toHaveBeenCalled(); expect(f.api.cancelFind).toHaveBeenCalledExactlyOnceWith('old-scan');
    expectDisconnected(f.state);
  });

  it('does not start a scan after reset while previous-scan cancellation is pending', async () => {
    const f = await connected(); f.api.startFind = vi.fn().mockResolvedValue('old-scan');
    f.api.findStatus = vi.fn().mockResolvedValue(finishedFind); await f.state.startFind('old');
    const cancelled = deferred<void>(); f.api.cancelFind = vi.fn(() => cancelled.promise);
    const finding = f.state.startFind('next'); await settle();
    await f.state.saveBookmark(f.bookmark, 'changed'); cancelled.resolve(); await finding;
    expect(f.api.startFind).toHaveBeenCalledOnce(); expectDisconnected(f.state);
  });

  it.each(['startup reply', 'startup error', 'poll reply', 'poll error'])('an obsolete %s cannot alter a new connection scan', async (stage) => {
    const f = await connected(); const oldStart = deferred<string>(); const oldStatus = deferred<typeof finishedFind>();
    const newStatus = deferred<typeof finishedFind>(); const cancelled = deferred<void>();
    f.api.startFind = vi.fn().mockImplementationOnce(() => stage.startsWith('startup') ? oldStart.promise : Promise.resolve('old-scan')).mockResolvedValueOnce('new-scan');
    f.api.findStatus = vi.fn((id) => id === 'old-scan' ? oldStatus.promise : newStatus.promise);
    f.api.cancelFind = vi.fn((id) => id === 'old-scan' ? cancelled.promise : Promise.resolve());
    const old = f.state.startFind('old'); await settle();
    await f.state.saveBookmark(f.bookmark, 'changed'); await f.state.connect(f.bookmark.id); await f.state.openContainer('fixtures');
    const current = f.state.startFind('current'); await settle();
    if (stage === 'startup reply') oldStart.resolve('old-scan');
    else if (stage === 'startup error') oldStart.reject(new Error('obsolete startup'));
    else if (stage === 'poll reply') oldStatus.resolve(finishedFind);
    else oldStatus.reject(new Error('obsolete poll'));
    await settle();
    expect(f.state.findRunning).toBe(true); expect(f.state.findStatus).toBeNull();
    if (stage === 'startup reply') {
      expect(f.api.cancelFind).toHaveBeenCalledWith('old-scan');
      expect(vi.mocked(f.api.findStatus).mock.calls.filter(([id]) => id === 'old-scan')).toHaveLength(0);
    }
    cancelled.resolve(); await old; await settle();
    await f.state.cancelFind(); expect(f.api.cancelFind).toHaveBeenCalledWith('new-scan');
    newStatus.resolve({ ...finishedFind, matches: [] }); await current;
  });

  it('a pending match click cannot navigate a later connection after cancellation returns', async () => {
    const f = await connected(); f.api.findStatus = vi.fn().mockResolvedValue(finishedFind); await f.state.startFind('old');
    const root = document.createElement('div'); document.body.append(root); const closed = vi.fn();
    mounted.add(mount(FindPanel, { target: root, props: { browser: f.state, onClose: closed } })); flushSync();
    const cancelled = deferred<void>(); f.api.cancelFind = vi.fn(() => cancelled.promise);
    root.querySelector<HTMLButtonElement>('.match')!.click(); await settle();
    await f.state.saveBookmark(f.bookmark, 'changed'); await f.state.connect(f.bookmark.id); await f.state.openContainer('photos_segments');
    f.api.objects = vi.fn(f.api.objects); cancelled.resolve(); await settle();
    expect(f.api.objects).not.toHaveBeenCalled(); expect(closed).not.toHaveBeenCalled();
    expect(f.state.containerName).toBe('photos_segments'); expect(f.state.prefix).toBe('');
  });
});

function button(root: HTMLElement, label: string) {
  const found = [...root.querySelectorAll('button')].find((item) => item.textContent?.trim() === label);
  if (!found) throw new Error('Missing button: ' + label);
  return found;
}
function input(root: HTMLElement, selector: string, value: string) {
  const field = root.querySelector<HTMLInputElement>(selector)!;
  field.value = value; field.dispatchEvent(new Event('input', { bubbles: true })); flushSync();
}
type EditorMode = 'save' | 'connect' | 'delete';
async function editorFixture(existing = true) {
  const f = await connected(); const closed = vi.fn();
  const root = document.createElement('div'); document.body.append(root);
  const props = new SvelteMap<string, BookmarkDTO | null>([['bookmark', existing ? f.bookmark : null]]);
  const component = mount(BookmarkEditor, { target: root,
    props: { browser: f.state, get existing() { return props.get('bookmark') ?? null; }, onClose: closed } });
  mounted.add(component); flushSync();
  const click = (label: string) => { button(root, label).dispatchEvent(new MouseEvent('click', { bubbles: true })); flushSync(); };
  const submit = () => { root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); flushSync(); };
  const press = (mode: EditorMode) => {
    if (mode === 'save') click('Save');
    else if (mode === 'connect') submit();
    else {
      if ([...root.querySelectorAll('button')].some((item) => item.textContent?.trim() === 'Delete…')) click('Delete…');
      click('Really delete');
    }
  };
  if (!existing) {
    input(root, 'input[placeholder="Prod AU"]', 'Created');
    input(root, 'input[placeholder="https://swift.example/auth/v1.0"]', 'http://mock/auth/v1.0');
    input(root, 'input[placeholder="account:user"]', 'test:tester');
    input(root, 'input[type="password"]', 'testing');
  }
  return { ...f, root, props, closed, component, click, submit, press };
}

describe.each(['save', 'connect', 'delete'] as EditorMode[])('bookmark editor %s', (mode) => {
  it('admits a current operation and closes its own editor', async () => {
    const f = await editorFixture(); f.api.connect = vi.fn(f.api.connect);
    f.press(mode);
    expect(f.root.querySelector<HTMLFieldSetElement>('fieldset')?.disabled).toBe(true);
    await settle(); expect(f.closed).toHaveBeenCalledOnce();
    expect(f.api.connect).toHaveBeenCalledTimes(mode === 'connect' ? 1 : 0);
    if (mode === 'connect') expect(f.state.connectedBookmarkId).toBe(f.bookmark.id);
    else expectDisconnected(f.state);
  });

  it('refuses duplicate submission and freezes pending values', async () => {
    const f = await editorFixture(); const gate = deferred<void>();
    f.api.saveBookmark = vi.fn(async () => { await gate.promise; return f.bookmark; });
    f.api.deleteBookmark = vi.fn(() => gate.promise); f.api.connect = vi.fn(f.api.connect);
    f.press(mode); f.press(mode);
    expect(mode === 'delete' ? f.api.deleteBookmark : f.api.saveBookmark).toHaveBeenCalledOnce();
    expect(f.root.querySelector<HTMLFieldSetElement>('fieldset')?.disabled).toBe(true);
    gate.resolve(); await settle(); expect(f.closed).toHaveBeenCalledOnce();
    expect(f.api.connect).toHaveBeenCalledTimes(mode === 'connect' ? 1 : 0);
  });

  it('keeps a current mutation failure visible and disconnected', async () => {
    const f = await editorFixture(); const failure = new Error('current mutation failure');
    f.api.saveBookmark = vi.fn().mockRejectedValue(failure); f.api.deleteBookmark = vi.fn().mockRejectedValue(failure);
    f.api.connect = vi.fn(f.api.connect); f.press(mode); await settle();
    expect(f.root.textContent).toContain(failure.message); expect(f.closed).not.toHaveBeenCalled();
    expect(f.root.querySelector<HTMLFieldSetElement>('fieldset')?.disabled).toBe(false);
    expect(f.api.connect).not.toHaveBeenCalled(); expectDisconnected(f.state);
  });

  it.each(['close success', 'close failure', 'unmount success', 'unmount failure'])('%s retires the pending callback', async (ending) => {
    const f = await editorFixture(); const gate = deferred<void>();
    f.api.saveBookmark = vi.fn(async () => { await gate.promise; return f.bookmark; });
    f.api.deleteBookmark = vi.fn(() => gate.promise); f.api.connect = vi.fn(f.api.connect);
    f.press(mode);
    if (ending.startsWith('close')) f.click('Cancel');
    else { await unmount(f.component); mounted.delete(f.component); }
    if (ending.endsWith('success')) gate.resolve(); else gate.reject(new Error('obsolete editor error'));
    await settle();
    expect(f.closed).toHaveBeenCalledTimes(ending.startsWith('close') ? 1 : 0);
    expect(f.root.textContent).not.toContain('obsolete editor error'); expect(f.api.connect).not.toHaveBeenCalled();
    if (ending.startsWith('close')) expect(f.root.querySelector<HTMLFieldSetElement>('fieldset')?.disabled).toBe(true);
  });
});

it('a changed bookmark prop cannot borrow the existing form for submission', async () => {
  const f = await editorFixture(); f.api.saveBookmark = vi.fn(f.api.saveBookmark);
  f.props.set('bookmark', { ...f.bookmark, id: 'different', displayName: 'Different' });
  f.press('save'); await settle();
  expect(f.api.saveBookmark).not.toHaveBeenCalled(); expect(f.closed).toHaveBeenCalledOnce();
});

it('a different connection intent stops automatic Connect after the editor save', async () => {
  const f = await editorFixture();
  const other = await f.state.saveBookmark({ ...f.bookmark, id: '', displayName: 'Other' }, 'testing');
  const gate = deferred<BookmarkDTO>(); f.api.saveBookmark = vi.fn(() => gate.promise); f.api.connect = vi.fn(f.api.connect);
  f.submit(); await f.state.connect(other.id); gate.resolve(f.bookmark); await settle();
  expect(f.api.connect).toHaveBeenCalledExactlyOnceWith(other.id); expect(f.state.connectedBookmarkId).toBe(other.id);
  expect(f.root.textContent).toContain('The connection changed'); expect(f.closed).not.toHaveBeenCalled();
});

it.each(['close', 'unmount'])('editor %s during Connect retires publication but holds the bridge slot', async (ending) => {
  const f = await editorFixture(); const account = f.state.account!; const gate = deferred<typeof account>();
  f.api.connect = vi.fn(() => gate.promise); f.submit(); await settle();
  expect(f.api.connect).toHaveBeenCalledOnce(); expect(f.state.connecting).toBe(true);
  if (ending === 'close') f.click('Cancel'); else { await unmount(f.component); mounted.delete(f.component); }
  const duplicate = f.state.connect('other').catch((error: Error) => error);
  expect(f.api.connect).toHaveBeenCalledOnce(); expect(await duplicate).toBeInstanceOf(Error);
  gate.resolve(account); await settle(); expectDisconnected(f.state); expect(f.state.connecting).toBe(false);
  expect(f.closed).toHaveBeenCalledTimes(ending === 'close' ? 1 : 0);
});

it('a failed Connect after creation retries the saved bookmark instead of creating another', async () => {
  const f = await editorFixture(false); const save = vi.spyOn(f.api, 'saveBookmark');
  const connect = f.api.connect; f.api.connect = vi.fn().mockRejectedValueOnce(new Error('current connect failure')).mockImplementation(connect);
  f.submit(); await settle();
  expect(f.root.textContent).toContain('current connect failure'); expectDisconnected(f.state); expect(f.closed).not.toHaveBeenCalled();
  expect(f.state.bookmarks).toHaveLength(2); expect(save).toHaveBeenCalledOnce(); expect(save.mock.calls[0][0].id).toBe('');
  const created = await save.mock.results[0].value;
  f.submit(); await settle();
  expect(save).toHaveBeenCalledTimes(2); expect(save.mock.calls[1][0].id).toBe(created.id);
  expect(f.state.bookmarks).toHaveLength(2); expect(f.state.connectedBookmarkId).toBe(created.id); expect(f.closed).toHaveBeenCalledOnce();
});
