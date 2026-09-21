import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import { SvelteMap } from 'svelte/reactivity';
import { BrowserState } from './browserState.svelte';
import { newFakeApi } from './fakeApi';
import WorkingList from './WorkingList.svelte';
import FindPanel from './FindPanel.svelte';
import Inspector from '../inspector/Inspector.svelte';
import type { FindStatusDTO, ObjectDetailDTO } from '../../lib/api';

const mounted = new Set<ReturnType<typeof mount>>();
const states = new Set<BrowserState>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  for (const state of states) { state.findOpen = false; await state.cancelFind(); }
  states.clear();
  if (vi.isFakeTimers()) await vi.runOnlyPendingTimersAsync();
  vi.useRealTimers();
  mounted.clear(); document.body.innerHTML = ''; vi.restoreAllMocks();
});
async function settle() { for (let i = 0; i < 24; i++) { await Promise.resolve(); flushSync(); } }
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const done = { scanned: 1, matches: [{ name: 'normal/hello.txt', bytes: 12 }], done: true, truncated: false, error: '', objectsPerSecond: 1, scope: { bookmarkId: 'bm-1', generation: 1 } };
async function fixture() {
  const api = newFakeApi(); const state = new BrowserState(api);
  states.add(state);
  const bookmark = await state.saveBookmark({ id: '', displayName: 'Mock', authUrl: 'http://mock/auth/v1.0', username: 'test:tester', storageUrlOverride: '', tlsMode: 'system', caPem: '', hasCa: false, hasCredential: false }, 'testing');
  await state.connect(bookmark.id); await state.openContainer('fixtures');
  api.findStatus = vi.fn().mockResolvedValue(done); api.cancelFind = vi.fn().mockResolvedValue(undefined);
  state.findOpen = true;
  const root = document.createElement('div'); document.body.append(root);
  const component = mount(WorkingList, { target: root, props: { browser: state } }); mounted.add(component); flushSync();
  const scope = { ...state.account!.scope };
  return { api, state, scope, bookmark, root, component };
}
function click(root: HTMLElement, selector: string) {
  const button = root.querySelector<HTMLButtonElement>(selector)!;
  expect(button).not.toBeNull(); button.dispatchEvent(new MouseEvent('click', { bubbles: true })); flushSync();
}

describe('Find intent lifetime', () => {
  it.each(['Stop', 'panel close', 'main toggle', 'unmount', 'container', 'prefix', 'container property return', 'prefix property return', 'account return', 'identical reload'])('%s retires a startup before its id arrives', async (ending) => {
    const f = await fixture(); const gate = deferred<string>(); f.api.startFind = vi.fn(() => gate.promise);
    const finding = f.state.startFind('match'); await settle();
    try {
      expect(f.api.startFind).toHaveBeenCalledOnce();
      if (ending === 'Stop') await f.state.cancelFind();
      else if (ending === 'panel close') click(f.root, 'button[aria-label="Close find"]');
      else if (ending === 'main toggle') {
        const toggle = [...f.root.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Find in container')!;
        toggle.click(); flushSync();
      } else if (ending === 'unmount') { await unmount(f.component); mounted.delete(f.component); }
      else if (ending === 'container') await f.state.openContainer('photos_segments');
      else if (ending === 'prefix') await f.state.goTo('normal/');
      else if (ending === 'container property return') { f.state.containerName = 'other'; f.state.containerName = 'fixtures'; }
      else if (ending === 'prefix property return') { f.state.prefix = 'other/'; f.state.prefix = ''; }
      else if (ending === 'account return') { f.state.connectedBookmarkId = 'other'; f.state.connectedBookmarkId = f.bookmark.id; }
      else await f.state.goTo('');
      expect(f.state.findRunning).toBe(false);
    } finally { gate.resolve('retired-scan'); await finding; await settle(); }
    expect(f.api.findStatus).not.toHaveBeenCalled();
    expect(f.api.cancelFind).toHaveBeenCalledExactlyOnceWith('retired-scan', f.bookmark.id);
    expect(f.state.findStatus).toBeNull();
  });

  it('a late replaced startup cannot poll or clear the current scan', async () => {
    const f = await fixture(); const old = deferred<string>(); const current = deferred<typeof done>();
    f.api.startFind = vi.fn().mockImplementationOnce(() => old.promise).mockResolvedValueOnce('current');
    f.api.findStatus = vi.fn((id) => id === 'current' ? current.promise : Promise.resolve(done));
    const first = f.state.startFind('old'); await settle();
    const second = f.state.startFind('new'); await settle();
    try {
      old.resolve('obsolete'); await first;
      expect(f.api.cancelFind).toHaveBeenCalledWith('obsolete', f.bookmark.id);
      expect(vi.mocked(f.api.findStatus).mock.calls).toEqual([['current', f.bookmark.id]]);
      expect(f.state.findRunning).toBe(true); expect(f.state.findStatus).toBeNull();
    } finally { current.resolve(done); await second; }
    expect(f.state.findStatus).toEqual(done);
  });

  it('selects the exact clicked object beyond the first listing page', async () => {
    const f = await fixture(); await f.state.startFind('hello');
    const row = { ...f.state.entries.find((item) => item.kind === 'object')!, name: 'normal/earlier.txt' };
    f.api.objects = vi.fn().mockResolvedValue({ rows: [row], nextMarker: 'normal/earlier.txt', scope: f.scope });
    f.api.objectDetail = vi.fn(f.api.objectDetail); f.api.prefixDetail = vi.fn(f.api.prefixDetail);
    click(f.root, '.match'); await settle();
    expect(f.state.containerName).toBe('fixtures'); expect(f.state.prefix).toBe('normal/');
    expect(f.state.selectedEntry?.name).toBe('normal/hello.txt'); expect(f.state.selectedEntry?.kind).toBe('object');
    expect(f.state.objectDetail?.name).toBe('normal/hello.txt');
    expect(f.api.objects).toHaveBeenCalledExactlyOnceWith(f.state.connectedBookmarkId!, 'fixtures', 'normal/', '', 1000);
    expect(f.api.objectDetail).toHaveBeenCalledExactlyOnceWith('fixtures', 'normal/hello.txt', f.bookmark.id);
    expect(f.api.prefixDetail).not.toHaveBeenCalled();
    expect(f.root.querySelector('[aria-selected="true"]')?.textContent).toContain('hello.txt');
  });
});

describe('Find response ownership', () => {
  it.each(['success', 'failure'])('a known-id cancellation %s stays detached from a newer scan', async (outcome) => {
    const f = await fixture(); const oldPoll = deferred<FindStatusDTO>(); const newPoll = deferred<FindStatusDTO>(); const cancellation = deferred<void>();
    f.api.startFind = vi.fn().mockResolvedValueOnce('old').mockResolvedValueOnce('new');
    f.api.findStatus = vi.fn((id) => id === 'old' ? oldPoll.promise : newPoll.promise);
    f.api.cancelFind = vi.fn((id) => id === 'old' ? cancellation.promise : Promise.resolve());
    const old = f.state.startFind('old'); await settle(); const stopping = f.state.cancelFind();
    const current = f.state.startFind('new'); await settle();
    try {
      expect(f.api.startFind).toHaveBeenCalledTimes(2); expect(f.state.findRunning).toBe(true);
      if (outcome === 'success') cancellation.resolve(); else cancellation.reject(new Error('old cancellation failed'));
      await stopping; oldPoll.resolve(done); await old; await settle();
      expect(f.state.findRunning).toBe(true); expect(f.state.findStatus).toBeNull(); expect(f.state.findNotice).toBeFalsy();
      await f.state.cancelFind(); expect(f.api.cancelFind).toHaveBeenCalledWith('new', f.bookmark.id);
    } finally { cancellation.resolve(); oldPoll.resolve(done); newPoll.resolve(done); await Promise.allSettled([old, current, stopping]); }
  });

  describe.each(['startup reply', 'startup error', 'poll reply', 'poll error'])('%s', (stage) => {
    it.each(['before', 'after'])('an old completion %s the new scan cannot change its result', async (order) => {
      const f = await fixture(); const oldStart = deferred<string>(); const oldPoll = deferred<FindStatusDTO>();
      const newPoll = deferred<FindStatusDTO>(); const current = { ...done, matches: [{ name: 'new/match', bytes: 2 }] };
      f.api.startFind = vi.fn().mockImplementationOnce(() => stage.startsWith('startup') ? oldStart.promise : Promise.resolve('old')).mockResolvedValueOnce('new');
      f.api.findStatus = vi.fn((id) => id === 'old' ? (stage.startsWith('startup') ? Promise.resolve(done) : oldPoll.promise) : newPoll.promise);
      const first = f.state.startFind('old'); await settle(); const second = f.state.startFind('new'); await settle();
      const finishOld = () => {
        if (stage === 'startup reply') oldStart.resolve('old');
        else if (stage === 'startup error') oldStart.reject(new Error('obsolete startup'));
        else if (stage === 'poll reply') oldPoll.resolve(done);
        else oldPoll.reject(new Error('obsolete poll'));
      };
      try {
        if (order === 'before') {
          finishOld(); await first;
          expect(f.state.findRunning).toBe(true); expect(f.state.findStatus).toBeNull();
          newPoll.resolve(current); await second;
        } else { newPoll.resolve(current); await second; finishOld(); await first; }
        expect(f.state.findStatus).toEqual(current); expect(f.state.findRunning).toBe(false); expect(f.state.findNotice).toBeFalsy();
        if (stage === 'startup reply') {
          expect(f.api.cancelFind).toHaveBeenCalledExactlyOnceWith('old', f.bookmark.id);
          expect(vi.mocked(f.api.findStatus).mock.calls).toEqual([['new', f.bookmark.id]]);
        }
      } finally { oldStart.resolve('old'); oldPoll.resolve(done); newPoll.resolve(current); await Promise.allSettled([first, second]); }
    });
  });

  it.each(['container return', 'prefix return', 'identical reload', 'reconnect', 'active save', 'inactive save', 'filter change'])('a pending poll observes %s scope ownership', async (change) => {
    const f = await fixture(); const poll = deferred<FindStatusDTO>();
    f.api.startFind = vi.fn().mockResolvedValue('scan'); f.api.findStatus = vi.fn(() => poll.promise);
    const finding = f.state.startFind('hello'); await settle();
    const survives = change === 'inactive save' || change === 'filter change';
    try {
      if (change === 'container return') { await f.state.openContainer('photos_segments'); await f.state.openContainer('fixtures'); }
      else if (change === 'prefix return') { await f.state.goTo('normal/'); await f.state.goTo(''); }
      else if (change === 'identical reload') await f.state.goTo('');
      else if (change === 'reconnect') { await f.state.connect(f.bookmark.id); await f.state.openContainer('fixtures'); }
      else if (change === 'active save') await f.state.saveBookmark(f.bookmark, 'changed');
      else if (change === 'inactive save') await f.state.saveBookmark({ ...f.bookmark, id: '', displayName: 'Other' }, 'other');
      else f.state.filter = 'hello';
      expect(f.state.findRunning).toBe(survives);
      if (survives) expect(f.api.cancelFind).not.toHaveBeenCalled();
      else expect(f.api.cancelFind).toHaveBeenCalledExactlyOnceWith('scan', f.bookmark.id);
    } finally { poll.resolve(done); await finding; }
    expect(f.state.findStatus).toEqual(survives ? done : null);
  });

  it.each(['success', 'failure'])('a late old cancellation %s cannot alter a newer scan', async (outcome) => {
    const f = await fixture(); const firstStart = deferred<string>(); const cancellation = deferred<void>();
    const currentPoll = deferred<FindStatusDTO>();
    f.api.startFind = vi.fn().mockImplementationOnce(() => firstStart.promise).mockResolvedValueOnce('current');
    f.api.cancelFind = vi.fn((id) => id === 'old' ? cancellation.promise : Promise.resolve());
    f.api.findStatus = vi.fn(() => currentPoll.promise);
    const old = f.state.startFind('old'); await settle(); await f.state.cancelFind();
    const current = f.state.startFind('current'); await settle(); firstStart.resolve('old'); await settle();
    try {
      expect(f.api.cancelFind).toHaveBeenCalledWith('old', f.bookmark.id);
      if (outcome === 'success') cancellation.resolve(); else cancellation.reject(new Error('old cancellation failed'));
      await old; expect(f.state.findRunning).toBe(true); expect(f.state.findNotice).toBeFalsy(); expect(f.state.findStatus).toBeNull();
    } finally { cancellation.resolve(); currentPoll.resolve(done); await Promise.allSettled([old, current]); }
  });

  it.each(['Stop', 'close', 'navigation', 'replacement'])('%s retires a start waiting for the old cancellation', async (ending) => {
    const f = await fixture(); f.api.startFind = vi.fn().mockResolvedValue('old'); await f.state.startFind('old');
    const cancellation = deferred<void>(); f.api.cancelFind = vi.fn(() => cancellation.promise);
    const pending = f.state.startFind('retired'); await settle();
    let replacement: Promise<void> | undefined;
    try {
      if (ending === 'Stop') await f.state.cancelFind();
      else if (ending === 'close') f.state.findOpen = false;
      else if (ending === 'navigation') await f.state.goTo('normal/');
      else replacement = f.state.startFind('replacement');
      cancellation.resolve(); await pending; await replacement;
      expect(vi.mocked(f.api.startFind).mock.calls.map((args) => args[2])).toEqual(ending === 'replacement' ? ['old', 'replacement'] : ['old']);
    } finally { cancellation.resolve(); await pending; await replacement; }
  });
});

describe('Find controls and truthful status', () => {
  it('Stop before bridge admission does not wait for an id that was never requested', async () => {
    const f = await fixture(); f.api.startFind = vi.fn().mockResolvedValue('old'); await f.state.startFind('old');
    const cancellation = deferred<void>(); f.api.cancelFind = vi.fn(() => cancellation.promise);
    const pending = f.state.startFind('never admitted'); await settle(); await f.state.cancelFind(); await settle();
    try { expect(f.root.textContent).toContain('Scan start cancelled.'); expect(f.api.startFind).toHaveBeenCalledOnce(); }
    finally { cancellation.resolve(); await pending; }
  });

  it('an empty replacement query cannot cancel a current scan', async () => {
    const f = await fixture(); const poll = deferred<FindStatusDTO>();
    f.api.startFind = vi.fn().mockResolvedValue('current'); f.api.findStatus = vi.fn(() => poll.promise);
    const current = f.state.startFind('current'); await settle();
    let replacement: Promise<void> | undefined;
    try {
      replacement = f.state.startFind(' \t '); await settle(); expect(f.api.startFind).toHaveBeenCalledOnce();
      expect(f.api.cancelFind).not.toHaveBeenCalled(); expect(f.state.findRunning).toBe(true);
    } finally { poll.resolve(done); await Promise.allSettled([current, replacement]); }
  });

  it.each(['success', 'failure'])('keeps stopped results and reports current cancellation %s truthfully', async (outcome) => {
    vi.useFakeTimers(); const f = await fixture(); const cancellation = deferred<void>();
    f.api.startFind = vi.fn().mockResolvedValue('scan');
    f.api.findStatus = vi.fn().mockResolvedValue({ ...done, done: false, objectsPerSecond: 456 });
    f.api.cancelFind = vi.fn(() => cancellation.promise);
    const finding = f.state.startFind('hello'); await settle();
    expect(f.root.textContent).toContain('456/s'); expect(f.root.textContent).toContain('scanning…');
    const stopping = f.state.cancelFind(); await settle();
    try {
      expect(f.state.findRunning).toBe(false); expect(f.state.findStatus?.matches).toEqual(done.matches);
      expect(f.root.textContent).toContain('updates stopped'); expect(f.root.textContent).not.toContain('scanning…');
      expect(f.root.textContent).not.toContain('456/s'); expect(f.root.textContent).toContain('Requesting cancellation');
      if (outcome === 'success') cancellation.resolve(); else cancellation.reject(new Error('bridge unavailable'));
      await stopping; await settle();
      expect(f.root.textContent).toContain(outcome === 'success' ? 'Cancellation requested.' : 'Could not confirm that the scan stopped: bridge unavailable');
      expect(f.state.findStatus?.done).toBe(false); expect(f.state.findStatus?.matches).toEqual(done.matches);
    } finally { cancellation.resolve(); await stopping; await vi.advanceTimersByTimeAsync(400); await finding; }
    expect(f.api.findStatus).toHaveBeenCalledOnce();
    await f.state.goTo('normal/'); expect(f.state.findStatus).toBeNull(); expect(f.state.findNotice).toBeFalsy();
  });

  it.each(['success', 'failure'])('reports cancellation %s for an id arriving after Stop', async (outcome) => {
    const f = await fixture(); const startup = deferred<string>();
    f.api.startFind = vi.fn(() => startup.promise);
    f.api.cancelFind = outcome === 'success' ? vi.fn().mockResolvedValue(undefined) : vi.fn().mockRejectedValue(new Error('late cancel failed'));
    const finding = f.state.startFind('hello'); await settle(); await f.state.cancelFind(); await settle();
    expect(f.root.textContent).toContain('Stop requested; waiting for scan startup.');
    startup.resolve('late'); await finding; await settle();
    expect(f.api.findStatus).not.toHaveBeenCalled(); expect(f.state.findRunning).toBe(false);
    expect(f.root.textContent).toContain(outcome === 'success' ? 'Cancellation requested.' : 'Could not confirm that the scan stopped: late cancel failed');
  });

  it('does not claim cancellation was confirmed when a retired startup fails without an id', async () => {
    const f = await fixture(); const startup = deferred<string>(); f.api.startFind = vi.fn(() => startup.promise);
    const finding = f.state.startFind('hello'); await settle(); await f.state.cancelFind();
    startup.reject(new Error('start response lost')); await finding; await settle();
    expect(f.root.textContent).toContain('Scan startup did not return an id; cancellation could not be confirmed.');
    expect(f.api.cancelFind).not.toHaveBeenCalled(); expect(f.api.findStatus).not.toHaveBeenCalled();
  });

  it.each(['startup', 'first poll', 'later poll'])('shows a current %s failure without borrowing an earlier result', async (stage) => {
    vi.useFakeTimers(); const f = await fixture(); const failure = new Error('no such scan id');
    f.api.startFind = stage === 'startup' ? vi.fn().mockRejectedValue(failure) : vi.fn().mockResolvedValue('scan');
    f.api.findStatus = stage === 'later poll'
      ? vi.fn().mockResolvedValueOnce({ ...done, done: false, scanned: 123, truncated: true }).mockRejectedValueOnce(failure)
      : vi.fn().mockRejectedValue(failure);
    const finding = f.state.startFind('hello'); await settle();
    if (stage === 'later poll') await vi.advanceTimersByTimeAsync(400);
    await finding; await settle();
    expect(f.state.findRunning).toBe(false); expect(f.state.findStatus?.error).toBe(failure.message);
    expect(f.state.findStatus?.scanned).toBe(stage === 'later poll' ? 123 : 0);
    expect(f.state.findStatus?.matches).toEqual(stage === 'later poll' ? done.matches : []);
    expect(f.state.findStatus?.truncated).toBe(stage === 'later poll');
    expect(f.root.textContent).toContain(failure.message);
  });

  it.each(['no container', 'disconnected', 'connecting', 'blank query'])('refuses Find with %s before the bridge', async (invalid) => {
    const f = await fixture(); f.api.startFind = vi.fn().mockResolvedValue('unexpected');
    if (invalid === 'no container') f.state.containerName = null;
    else if (invalid === 'disconnected') f.state.connectedBookmarkId = null;
    else if (invalid === 'connecting') f.state.connecting = true;
    await f.state.startFind(invalid === 'blank query' ? ' \t\n ' : 'hello');
    expect(f.api.startFind).not.toHaveBeenCalled(); expect(f.state.findRunning).toBe(false);
  });

  it('preserves exact query and namespace strings at admission', async () => {
    const f = await fixture(); f.state.containerName = ' c/😀 '; f.state.prefix = ' \tpréfix/';
    f.api.startFind = vi.fn().mockResolvedValue('exact'); await f.state.startFind('  hé😀 \t');
    expect(f.api.startFind).toHaveBeenCalledExactlyOnceWith(' c/😀 ', ' \tpréfix/', '  hé😀 \t', f.bookmark.id);
  });

  it.each(['same turn', 'pending startup'])('refuses a synthetic duplicate form submission during %s', async (timing) => {
    const f = await fixture(); const gate = deferred<string>(); f.api.startFind = vi.fn(() => gate.promise);
    const input = f.root.querySelector<HTMLInputElement>('.controls input')!;
    input.value = 'hello'; input.dispatchEvent(new Event('input', { bubbles: true })); flushSync();
    const submit = () => f.root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    submit(); if (timing === 'pending startup') await settle(); submit(); await settle();
    try { expect(f.api.startFind).toHaveBeenCalledOnce(); expect(f.root.querySelector<HTMLButtonElement>('button[type="submit"]')?.disabled).toBe(true); }
    finally { gate.resolve('one'); await settle(); }
  });
});

describe('Find panel ownership', () => {
  it('retires its original scan when the BrowserState prop changes without cancelling the new one', async () => {
    const old = await fixture(); const current = await fixture(); const oldPoll = deferred<FindStatusDTO>(); const newPoll = deferred<FindStatusDTO>();
    old.api.startFind = vi.fn().mockResolvedValue('old'); old.api.findStatus = vi.fn(() => oldPoll.promise);
    current.api.startFind = vi.fn().mockResolvedValue('new'); current.api.findStatus = vi.fn(() => newPoll.promise);
    const props = new SvelteMap([['browser', old.state]]); const root = document.createElement('div'); document.body.append(root);
    const panel = mount(FindPanel, { target: root, props: { get browser() { return props.get('browser')!; }, onClose: vi.fn() } }); mounted.add(panel); flushSync();
    const before = old.state.startFind('old'); const after = current.state.startFind('new'); await settle();
    try {
      props.set('browser', current.state); await settle();
      expect(old.api.cancelFind).toHaveBeenCalledExactlyOnceWith('old', old.bookmark.id); expect(old.state.findRunning).toBe(false);
      click(root, 'button[aria-label="Close find"]');
      await unmount(panel); mounted.delete(panel); await settle();
      expect(current.api.cancelFind).not.toHaveBeenCalled(); expect(current.state.findRunning).toBe(true); expect(current.state.findOpen).toBe(true);
    } finally { oldPoll.resolve(done); newPoll.resolve(done); await Promise.allSettled([before, after]); }
  });

  it.each(['success', 'failure'])('an old unmount cannot cancel a reopened panel after late startup %s', async (outcome) => {
    const f = await fixture(); await unmount(f.component); mounted.delete(f.component); f.state.findOpen = true;
    const oldRoot = document.createElement('div'); document.body.append(oldRoot);
    const closed = vi.fn(); const oldPanel = mount(FindPanel, { target: oldRoot, props: { browser: f.state, onClose: closed } }); mounted.add(oldPanel); flushSync();
    const startup = deferred<string>(); const poll = deferred<FindStatusDTO>();
    f.api.startFind = vi.fn().mockImplementationOnce(() => startup.promise).mockResolvedValueOnce('new');
    f.api.findStatus = vi.fn((id) => id === 'new' ? poll.promise : Promise.resolve(done));
    const old = f.state.startFind('old'); await settle();
    f.state.findOpen = false; f.state.findOpen = true;
    const current = f.state.startFind('new'); await settle();
    try {
      await unmount(oldPanel); mounted.delete(oldPanel);
      if (outcome === 'success') startup.resolve('old'); else startup.reject(new Error('old startup failed'));
      await old;
      expect(f.state.findOpen).toBe(true); expect(f.state.findRunning).toBe(true);
      expect(vi.mocked(f.api.cancelFind).mock.calls.some(([id]) => id === 'new')).toBe(false); expect(closed).not.toHaveBeenCalled();
      expect(f.state.findStatus).toBeNull(); expect(f.state.findNotice).toBeFalsy();
    } finally { startup.resolve('old'); poll.resolve(done); await Promise.allSettled([old, current]); }
  });

  it.each(['panel close', 'main toggle', 'unmount'])('%s cancels a known id and retires its held poll', async (ending) => {
    const f = await fixture(); const poll = deferred<FindStatusDTO>();
    f.api.startFind = vi.fn().mockResolvedValue('scan'); f.api.findStatus = vi.fn(() => poll.promise);
    const finding = f.state.startFind('hello'); await settle();
    try {
      if (ending === 'panel close') click(f.root, 'button[aria-label="Close find"]');
      else if (ending === 'main toggle') [...f.root.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Find in container')!.click();
      else { await unmount(f.component); mounted.delete(f.component); }
      await settle(); expect(f.api.cancelFind).toHaveBeenCalledExactlyOnceWith('scan', f.bookmark.id); expect(f.state.findRunning).toBe(false);
    } finally { poll.resolve(done); await finding; }
    expect(f.state.findStatus).toBeNull(); expect(f.state.findOpen).toBe(false);
  });
});

async function resultFixture(name = 'normal/hello.txt') {
  const f = await fixture(); const detail = await f.api.objectDetail('fixtures', 'normal/hello.txt', f.bookmark.id);
  f.api.findStatus = vi.fn().mockResolvedValue({ ...done, matches: [{ name, bytes: 12 }] });
  await f.state.startFind('match');
  const row = { ...f.state.entries.find((item) => item.kind === 'object')!, name: 'earlier' };
  f.api.objects = vi.fn().mockResolvedValue({ rows: [row], nextMarker: 'earlier', scope: f.scope });
  f.api.objectDetail = vi.fn().mockResolvedValue({ ...detail, name }); f.api.prefixDetail = vi.fn(f.api.prefixDetail);
  return { ...f, detail, row };
}

describe('Find exact result selection', () => {
  it.each(['external-slo', 'external-dlo'])('uses the existing detail and segment path for %s', async (name) => {
    const f = await resultFixture(name); const detail = await newFakeApi().objectDetail('fixtures', name, f.bookmark.id);
    f.api.objectDetail = vi.fn().mockResolvedValue(detail);
    f.api.sloManifest = vi.fn(f.api.sloManifest); f.api.dloSegments = vi.fn(f.api.dloSegments);
    click(f.root, '.match'); await settle();
    expect(f.state.objectDetail?.class).toBe(detail.class); expect(f.api.objectDetail).toHaveBeenCalledExactlyOnceWith('fixtures', name, f.bookmark.id);
    if (detail.class === 'slo') {
      expect(f.api.sloManifest).toHaveBeenCalledExactlyOnceWith('fixtures', name, f.bookmark.id); expect(f.state.sloManifest?.entries).toHaveLength(3);
      expect(f.api.dloSegments).not.toHaveBeenCalled();
    } else {
      expect(f.api.dloSegments).toHaveBeenCalledExactlyOnceWith(detail.manifest, f.bookmark.id); expect(f.state.dloSegments).not.toBeNull();
      expect(f.api.sloManifest).not.toHaveBeenCalled();
    }
  });

  it.each([
    ['normal/hello.txt', 'normal/'], ['a/../é ?#%😀.txt ', 'a/../'], ['marker/', ''],
    ['prefix//', 'prefix/'], ['/', ''], [' back\\slash.txt ', ''], ['a/\tname\n.txt', 'a/'],
  ])('preserves the literal object %j and parent %j', async (name, prefix) => {
    const f = await resultFixture(name); click(f.root, '.match'); await settle();
    expect(f.state.prefix).toBe(prefix); expect(f.state.detailIdentity).toEqual({ container: 'fixtures', name });
    expect(f.state.selectedEntry?.kind).toBe('object'); expect(f.state.objectDetail?.name).toBe(name);
    expect(f.api.objects).toHaveBeenCalledExactlyOnceWith(f.state.connectedBookmarkId!, 'fixtures', prefix, '', 1000);
    expect(f.api.objectDetail).toHaveBeenCalledExactlyOnceWith('fixtures', name, f.bookmark.id); expect(f.api.prefixDetail).not.toHaveBeenCalled();
  });

  it('selects an existing exact object row once without duplicating it', async () => {
    const f = await resultFixture(); const target = { ...f.row, name: 'normal/hello.txt' };
    f.api.objects = vi.fn().mockResolvedValue({ rows: [target, f.row], nextMarker: 'later', scope: f.scope });
    click(f.root, '.match'); await settle();
    expect(f.state.selectedIndex).toBe(0); expect(f.state.entries.filter((r) => r.name === target.name)).toHaveLength(1);
    expect(f.api.objectDetail).toHaveBeenCalledOnce(); expect(f.state.entriesMarker).toBe('later');
  });

  it('keeps a same-name object and virtual prefix distinct through filtering', async () => {
    const f = await resultFixture('marker/'); const prefix = { ...f.row, kind: 'prefix', name: 'marker/' };
    f.api.objects = vi.fn().mockResolvedValue({ rows: [f.row, prefix], nextMarker: '', scope: f.scope });
    click(f.root, '.match'); await settle();
    expect(f.state.entries.filter((row) => row.name === 'marker/').map((row) => row.kind)).toEqual(['prefix', 'object']);
    const identity = f.state.detailIdentity; f.state.filter = 'marker';
    expect(f.state.selectedEntry?.kind).toBe('object'); expect(f.state.selectedEntry?.name).toBe('marker/');
    expect(f.state.detailIdentity).toBe(identity); expect(f.state.objectDetail?.name).toBe('marker/');
    expect(f.api.prefixDetail).not.toHaveBeenCalled(); expect(f.api.objectDetail).toHaveBeenCalledOnce();
  });

  it('makes a selected result beyond a full page visible without walking earlier pages', async () => {
    const f = await resultFixture(); const rows = Array.from({ length: 1000 }, (_, i) => ({ ...f.row, name: 'normal/a-' + String(i).padStart(4, '0') }));
    f.api.objects = vi.fn().mockResolvedValue({ rows, nextMarker: rows[999].name, scope: f.scope });
    click(f.root, '.match'); await settle();
    expect(f.state.selectedIndex).toBe(1000); expect(f.state.entries).toHaveLength(1001);
    expect(f.root.querySelector('[aria-selected="true"]')?.textContent).toContain('hello.txt');
    const viewport = f.root.querySelector<HTMLElement>('[role="listbox"]')!;
    expect(viewport.scrollTop).toBeGreaterThan(0); expect(f.api.objects).toHaveBeenCalledOnce(); expect(f.api.objectDetail).toHaveBeenCalledOnce();
    viewport.scrollTop = 0; viewport.dispatchEvent(new Event('scroll')); flushSync();
    expect(viewport.scrollTop).toBe(0); expect(f.root.querySelector('[aria-selected="true"]')).toBeNull();
    expect(f.state.selectedEntry?.name).toBe('normal/hello.txt');
  });

  it('reports a disappeared result through HEAD failure and keeps edits unavailable', async () => {
    const f = await resultFixture(); f.api.objectDetail = vi.fn().mockRejectedValue(new Error('object not found'));
    f.api.updateObjectMetadata = vi.fn(f.api.updateObjectMetadata);
    const inspector = document.createElement('div'); document.body.append(inspector);
    mounted.add(mount(Inspector, { target: inspector, props: { browser: f.state } }));
    click(f.root, '.match'); await settle();
    expect(f.state.selectedEntry?.name).toBe('normal/hello.txt'); expect(f.state.objectDetail).toBeNull();
    expect(inspector.textContent).toContain('HEAD failed: object not found'); expect(inspector.textContent).not.toContain('Edit metadata');
    await expect(f.state.saveObjectMetadata({ meta: {}, contentType: '', deleteAt: '' })).rejects.toThrow('no current object detail');
    expect(f.api.updateObjectMetadata).not.toHaveBeenCalled();
  });

  it('shows a failed parent listing without selecting an unrelated row', async () => {
    const f = await resultFixture(); f.api.objects = vi.fn().mockRejectedValue(new Error('listing unavailable'));
    click(f.root, '.match'); await settle();
    expect(f.root.querySelector('[role="alert"]')?.textContent).toContain('Could not open the result: listing unavailable');
    expect(f.state.selectedEntry).toBeNull(); expect(f.state.objectDetail).toBeNull(); expect(f.api.objectDetail).not.toHaveBeenCalled();
    expect(f.state.findOpen).toBe(true);
  });
});

describe('Find result navigation lifetime', () => {
  describe.each(['navigate', 'selection', 'reconnect', 'active save'])('%s after result selection', (ending) => {
    it.each(['success', 'failure'])('retires the late selected HEAD %s', async (outcome) => {
      const f = await resultFixture(); const head = deferred<ObjectDetailDTO>();
      f.api.objectDetail = vi.fn().mockImplementationOnce(() => head.promise).mockImplementation(async (_container, name) => ({ ...f.detail, name }));
      click(f.root, '.match'); await settle();
      try {
        expect(f.api.objectDetail).toHaveBeenCalledExactlyOnceWith('fixtures', 'normal/hello.txt', f.bookmark.id);
        if (ending === 'navigate') await f.state.goTo('next/');
        else if (ending === 'selection') f.state.select(0);
        else if (ending === 'reconnect') { await f.state.connect(f.bookmark.id); await f.state.openContainer('fixtures'); }
        else await f.state.saveBookmark(f.bookmark, 'changed');
        await settle(); const detail = f.state.objectDetail; const error = f.state.detailError;
        if (outcome === 'success') head.resolve({ ...f.detail, name: 'normal/hello.txt' }); else head.reject(new Error('obsolete HEAD'));
        await settle(); expect(f.state.objectDetail).toBe(detail); expect(f.state.detailError).toBe(error);
        expect(f.state.objectDetail?.name).not.toBe('normal/hello.txt');
      } finally { head.resolve({ ...f.detail, name: 'normal/hello.txt' }); await settle(); }
    });
  });

  it('refuses mismatched HEAD identity for the selected Find result', async () => {
    const f = await resultFixture(); f.api.objectDetail = vi.fn().mockResolvedValue({ ...f.detail, name: 'wrong object' });
    click(f.root, '.match'); await settle();
    expect(f.state.objectDetail).toBeNull(); expect(f.state.detailError).toContain('does not match the selected name');
    expect(f.state.detailIdentity).toEqual({ container: 'fixtures', name: 'normal/hello.txt' });
  });

  it.each(['Stop', 'panel close', 'main toggle', 'unmount', 'prefix return', 'container return', 'replacement', 'selection'])('%s retires navigation waiting for cancellation', async (ending) => {
    const f = await resultFixture(); const cancellation = deferred<void>(); f.api.cancelFind = vi.fn(() => cancellation.promise);
    click(f.root, '.match'); await settle();
    let stopping: Promise<void> | undefined;
    let replacement: Promise<void> | undefined;
    try {
      if (ending === 'Stop') { stopping = f.state.cancelFind(); expect(f.api.cancelFind).toHaveBeenCalledOnce(); }
      else if (ending === 'panel close') click(f.root, 'button[aria-label="Close find"]');
      else if (ending === 'main toggle') [...f.root.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Find in container')!.click();
      else if (ending === 'unmount') { await unmount(f.component); mounted.delete(f.component); }
      else if (ending === 'prefix return') { f.state.prefix = 'other/'; f.state.prefix = ''; }
      else if (ending === 'container return') { f.state.containerName = 'other'; f.state.containerName = 'fixtures'; }
      else if (ending === 'replacement') {
        f.api.startFind = vi.fn().mockResolvedValue('new'); replacement = f.state.startFind('new'); await settle();
        expect(f.api.startFind).toHaveBeenCalledOnce();
      }
      else f.state.select(f.state.entries.findIndex((row) => row.kind === 'object'));
    } finally { cancellation.resolve(); await Promise.allSettled([stopping, replacement]); await settle(); }
    expect(f.api.objects).not.toHaveBeenCalled();
    expect(vi.mocked(f.api.objectDetail).mock.calls.some((args) => args[1] === 'normal/hello.txt')).toBe(false);
    expect(f.state.selectedEntry?.name).not.toBe('normal/hello.txt');
  });

  it.each(['Stop', 'close', 'unmount', 'prefix return', 'container return', 'identical reload', 'replacement', 'selection', 'reconnect', 'active save'])('%s retires selection waiting for the parent listing', async (ending) => {
    const f = await resultFixture(); const listing = deferred<{ rows: typeof f.row[]; nextMarker: string }>();
    const next = { ...f.row, name: 'fresh' };
    f.api.objects = vi.fn().mockImplementationOnce(() => listing.promise).mockResolvedValue({ rows: [next], nextMarker: '', scope: f.scope });
    f.api.objectDetail = vi.fn(async (_container, name) => ({ ...f.detail, name }));
    click(f.root, '.match'); await settle();
    try {
      expect(f.api.objects).toHaveBeenCalledOnce();
      if (ending === 'Stop') await f.state.cancelFind();
      else if (ending === 'close') click(f.root, 'button[aria-label="Close find"]');
      else if (ending === 'unmount') { await unmount(f.component); mounted.delete(f.component); }
      else if (ending === 'prefix return') { await f.state.goTo('other/'); await f.state.goTo('normal/'); }
      else if (ending === 'container return') { await f.state.openContainer('other'); await f.state.openContainer('fixtures'); await f.state.goTo('normal/'); }
      else if (ending === 'identical reload') await f.state.goTo('normal/');
      else if (ending === 'replacement') { f.api.startFind = vi.fn().mockResolvedValue('new'); await f.state.startFind('new'); }
      else if (ending === 'selection') f.state.select(f.state.entries.findIndex((row) => row.kind === 'object'));
      else if (ending === 'reconnect') { await f.state.connect(f.bookmark.id); await f.state.openContainer('fixtures'); }
      else await f.state.saveBookmark(f.bookmark, 'changed');
    } finally { listing.resolve({ rows: [f.row], nextMarker: '' }); await settle(); }
    expect(f.state.selectedEntry?.name).not.toBe('normal/hello.txt');
    expect(vi.mocked(f.api.objectDetail).mock.calls.some((args) => args[1] === 'normal/hello.txt')).toBe(false);
  });

  it('admits one result navigation for synthetic duplicate clicks', async () => {
    const f = await resultFixture(); const cancellation = deferred<void>(); f.api.cancelFind = vi.fn(() => cancellation.promise);
    click(f.root, '.match'); click(f.root, '.match'); await settle();
    try {
      expect(f.api.cancelFind).toHaveBeenCalledOnce(); expect(f.api.objects).not.toHaveBeenCalled();
      expect(f.root.querySelector<HTMLButtonElement>('.match')?.disabled).toBe(true);
    }
    finally { cancellation.resolve(); await settle(); }
    expect(f.api.objects).toHaveBeenCalledOnce(); expect(f.api.objectDetail).toHaveBeenCalledOnce();
  });
});
