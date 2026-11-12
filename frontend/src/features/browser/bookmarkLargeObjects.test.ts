import { afterEach, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { BookmarkDTO } from '../../lib/api';
import { BrowserState } from './browserState.svelte';
import { newFakeApi } from './fakeApi';
import BookmarkEditor from './BookmarkEditor.svelte';

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear(); document.body.innerHTML = '';
});

async function settle() { for (let i = 0; i < 24; i++) { await Promise.resolve(); flushSync(); } }

// The editor shows a bookmark's large-object kind and saves the one chosen,
// static being the default an unset bookmark reads as (ADR 0111).
it("edits and saves a bookmark's large-object kind", async () => {
  const api = newFakeApi(); const state = new BrowserState(api);
  const existing = await state.saveBookmark({ id: '', displayName: 'Mock', authUrl: 'http://mock/auth/v1.0', username: 'test:tester', storageUrlOverride: '', authVersion: 1, userDomain: '', projectName: '', projectDomain: '', region: '', tlsMode: 'system', caPem: '', hasCa: false, hasCredential: false, largeObjects: 'dlo' }, 'testing');
  const saves: BookmarkDTO[] = [];
  const save = api.saveBookmark;
  api.saveBookmark = vi.fn((dto: BookmarkDTO, secret: string) => { saves.push(dto); return save(dto, secret); });
  const root = document.createElement('div'); document.body.append(root);
  mounted.add(mount(BookmarkEditor, { target: root, props: { browser: state, existing, onClose: () => {} } }));
  flushSync();
  const select = [...root.querySelectorAll<HTMLSelectElement>('select')].find((s) => [...s.options].some((o) => o.value === 'dlo'))!;
  expect(select.value).toBe('dlo');
  select.value = ''; select.dispatchEvent(new Event('change')); flushSync();
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  await settle();
  expect(saves.at(-1)?.largeObjects).toBe('');
});
