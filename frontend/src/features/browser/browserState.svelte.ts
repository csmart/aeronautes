// Central UI state for live browsing (§65 commit 13): bookmarks →
// connect → containers → delimiter pages → selection-triggered HEAD.
// All data flows through the Api seam so this logic is tested against a
// fixture-backed fake; loading and error state live per pane.

import { isLiteralPlainObject } from "../../lib/objectKind";
import { messageOf } from "../../lib/messageOf";
import type {
  AccountDTO,
  AccountEditDTO,
  TempURLKeyEditDTO,
  CopyPlanDTO,
  CopyRequestDTO,
  DeletePreviewDTO,
  MirrorRequestDTO,
  DeleteRequestDTO,
  SegmentOwnershipDTO,
  SegmentsDeleteRequestDTO,
  SelectedDeleteRequestDTO,
  DownloadPreflightView,
  DownloadRequestDTO,
  FindStatusDTO,
  JobDTO,
  UploadPreflight,
  UploadRequestDTO,
  Api,
  BookmarkDTO,
  CredentialStatusDTO,
  VerifyResultDTO,
  CapabilitiesDTO,
  ContainerCreateDTO,
  ContainerDetailDTO,
  ContainerSettingsDTO,
  ContainerEditDTO,
  ContainerRowDTO,
  DLOSegmentsDTO,
  ListingRowDTO,
  MutationResultDTO,
  ObjectDetailDTO,
  ObjectMetadataEditDTO,
  PickedDirDTO,
  PickedFilesDTO,
  PrefixDetailDTO,
  SLOManifestDTO,
  SelectionScopeDTO,
  ListingScope,
} from "../../lib/api";
import { AuditState } from "./auditState.svelte";
import { SettingsState } from "./settingsState.svelte";
import { JobsState } from "./jobsState.svelte";
import { parentPrefix } from "../../lib/listing";

export type ContainerMode = "logical" | "all" | "backstage";

/** Bridge page size for both listing levels. */
const pageLimit = 1000;
/** The rows a column keeps before it stops loading pages, until the
 * operator's setting is read (ADR 0074 decision 1); mirrors
 * defaultBrowserRetentionCeiling in internal/app/settingsservice.go. */
const defaultBrowserRetentionCeiling = 100_000;
/** The most objects one selection holds, until the operator's setting is
 * read (ADR 0072 decision 4, reused by ADR 0093 decision 2); mirrors
 * defaultDownloadObjectCeiling in internal/app/settingsservice.go. */
const defaultDownloadObjectCeiling = 250_000;
/** The job states after which nothing more changes (§46). */
const terminalJobStates = ["complete", "partial", "cancelled", "failed"];
/** How often a job this client started is re-read until it ends: the
 * drawer's list cadence. */
const jobPollMs = 1500;

export interface ContainerGroups {
  main: ContainerRowDTO[];
  /** Suspected stores, grouped but never hidden (§24.5). */
  suspected: ContainerRowDTO[];
  /** Confirmed/Declared stores hidden by Logical mode (§24.5). */
  hiddenBackstage: number;
}

interface DetailRequest {
  scope: number;
  seq: number;
  container: string;
  name: string;
}

interface PageRequest {
  scope: number;
  seq: number;
  marker: string;
}

/** What a watched job changed: the account and container it wrote to, and
 * whether it deletes that container. */
interface WatchedChange {
  readonly bookmarkId: string;
  readonly container: string;
  readonly deletesContainer?: boolean;
}

interface FindRequest {
  readonly scope: number;
  readonly seq: number;
  readonly bookmark: string;
  /** The portrait held when the scan started; every status must name its
   * bookmark and generation (ADR 0075). */
  readonly portrait: ListingScope | null;
  readonly container: string;
  readonly prefix: string;
  started: boolean;
  id: string | null;
}

/** Every fact a listing row carries about its object, compared whole: a
 * metadata write changes the content type while the ETag, the size and
 * the second-precision timestamp stay equal, so no subset of these can
 * stand in for the others. */
function sameListingFacts(a: ListingRowDTO, b: ListingRowDTO): boolean {
  return a.etag === b.etag && a.bytes === b.bytes && a.lastModified === b.lastModified
    && a.contentType === b.contentType && a.sloListed === b.sloListed;
}

/** A row's identity within a listing: a prefix and an object can share a
 * name, so the kind is part of it. */
function rowIdentity(row: ListingRowDTO): string {
  return row.kind + ":" + row.name;
}

function requireAdvancingMarker(previous: string, next: string) {
  if (next === "") return;
  // Swift orders UTF-8 names; JavaScript's UTF-16 comparison differs for
  // supplementary characters and would reject valid continuation markers.
  const encoder = new TextEncoder();
  const before = encoder.encode(previous);
  const after = encoder.encode(next);
  for (let i = 0; i < Math.min(before.length, after.length); i++) {
    if (after[i] > before[i]) return;
    if (after[i] < before[i]) throw new Error("The page marker did not advance. Retry the page.");
  }
  if (after.length <= before.length) throw new Error("The page marker did not advance. Retry the page.");
}

/** A symbolic link in either representation: a followed link describes its
 * target, and a cross-account link arrives unfollowed with its target named.
 * No deletion flow opens for one (ADR 0092 decision 2, ADR 0094 decision 5). */
export function isLink(detail: ObjectDetailDTO): boolean {
  return detail.followedSymlink || !!detail.symlinkTarget;
}

/** What a copy or download acts on (ADR 0108). */
export interface TransferSource {
  kind: "selected" | "object" | "folder" | "container";
  /** The source prefix: the browsed folder, or the selected folder. */
  prefix: string;
  /** Full names of the objects acted on; empty means the whole prefix. */
  members: string[];
  /** Folder rows in a multi-selection, which are not members. */
  excludedFolders: number;
}

/** Which shown-object deletion an operation is (ADR 0092, ADR 0094). */
export type ShownDeleteKind = "object" | "marker" | "manifest";

/** One shown-object delete from opening to its settled result: a plain
 * object (ADR 0092), a directory marker or a manifest (ADR 0094). */
export interface ObjectDeleteOperation {
  kind: ShownDeleteKind;
  container: string;
  name: string;
  /** The identity the dialog opened for: the inspector's detail, or for a
   * marker the prefix detail's own object. */
  detail: ObjectDetailDTO;
  /** The scope revision and selection identity the dialog was opened from. */
  scope: number;
  selection: { container: string; name: string } | null;
  status: "pending" | "running" | "done";
  result: MutationResultDTO | null;
  error: string | null;
}

/** One selected-set deletion from opening to its start (ADR 0093 decision
 * 5): a snapshot captured when the dialog opened of the scope the rows were
 * listed under, the container, the object rows with the facts they were
 * listed with, unique by name, and the prefix rows left out. The request
 * is built from this snapshot alone, never from the live rows. */
export interface SelectedDeleteOperation {
  container: string;
  /** The scope the listing the rows came from was read under (ADR 0079). */
  listingScope: ListingScope;
  /** The scope revision the dialog was opened from. */
  scope: number;
  members: ListingRowDTO[];
  excludedPrefixes: string[];
  /** The most members one selection admits (ADR 0072 decision 4, the
   * download object ceiling setting). */
  ceiling: number;
  status: "pending" | "running" | "done";
  jobId: string | null;
  error: string | null;
}

export class BrowserState {
  #api: Api;

  constructor(api: Api) {
    this.#api = api;
    // A recent-changes read names the account being connected, else the one
    // connected, else none, as the drawer's recent slice follows the account.
    this.audit = new AuditState(api, () => this.#connectingBookmarkId ?? this.connectedBookmarkId ?? "");
    // A restore may add bookmarks, so the rail reloads when one finishes.
    this.settings = new SettingsState(api, () => void this.loadBookmarks());
    this.jobs = new JobsState(api);
  }

  // Shell environment.
  /** "keychain" or "session" (§12.1) — "" until Info arrives. */
  secretBackend = $state("");

  credentialStatus = $state<CredentialStatusDTO | null>(null);
  credentialStatusLoading = $state(false);
  credentialStatusError = $state(false);
  #credentialStatusSeq = 0;

  // Bookmarks and connection.
  bookmarks: BookmarkDTO[] = $state([]);
  bookmarksLoaded = $state(false);
  bookmarksLoading = $state(false);
  bookmarksError: string | null = $state(null);
  bookmarksStale = $state(false);
  #bookmarksSeq = 0;
  // UI previews must notice a scope change even if its names are then restored.
  // This revision does not replace the backend's credential generation.
  #scopeRevision = $state(0);
  #connectionRevision = 0;
  get scopeRevision(): number { return this.#scopeRevision; }

  #connectedBookmarkId: string | null = $state(null);
  get connectedBookmarkId(): string | null { return this.#connectedBookmarkId; }
  set connectedBookmarkId(value: string | null) {
    if (value !== this.#connectedBookmarkId) {
      this.#clearFind();
      this.#scopeRevision++;
      this.#connectionRevision++;
    }
    this.#connectedBookmarkId = value;
  }
  connecting = $state(false);
  #connectingBookmarkId: string | null = null;

  /** The name of the bookmark a connection is being made to, for the screen
   * to say so while it is made; "" when none is. */
  get connectingName(): string {
    if (!this.connecting || this.#connectingBookmarkId === null) return "";
    return this.bookmarks.find((b) => b.id === this.#connectingBookmarkId)?.displayName ?? "the bookmark";
  }
  #bookmarkChanges = new Map<string, number>();
  connectError: string | null = $state(null);
  account: AccountDTO | null = $state(null);
  /** The last account reload's failure, surfaced by the editor's reload
   * path; cleared on a successful account read or save (mirrors
   * containerDetailError). */
  accountError: string | null = $state(null);
  /** Connect-time §10 capability snapshot; null until connected. */
  capabilities: CapabilitiesDTO | null = $state(null);
  /** The §50 audit trail as the drawer shows it: search, export, recent slice. */
  readonly audit: AuditState;
  /** The settings document (§38/§40), the rail width it persists, and its backup and restore (ADR 0098). */
  readonly settings: SettingsState;
  /** The jobs drawer's data (§46/§47): the filtered list, the recovery status and the interrupted-job notice. */
  readonly jobs: JobsState;

  // Jobs drawer (§46/§47).

  // Transfer dialog (§27.1 Copy / §27.2 Mirror preview).
  transferOpen = $state(false);
  // Local transfer dialog (§27.5); null = closed.
  #localTransfer: "upload" | "download" | null = $state(null);
  #localTransferRevision = $state(0);
  get localTransferRevision(): number { return this.#localTransferRevision; }
  get localTransfer(): "upload" | "download" | null { return this.#localTransfer; }
  set localTransfer(value: "upload" | "download" | null) {
    // Closing/reopening or changing modes must retire pending picker replies,
    // including changes that happen before the parent can remount the dialog.
    if (value !== this.#localTransfer) this.#localTransferRevision++;
    this.#localTransfer = value;
  }
  // Destructive-jobs dialog (§31–§33).
  deleteOpen = $state(false);
  /** The single-object delete the operator opened (ADR 0092), owned here so its
   * result outlives the listing refresh that follows its success; the dialog
   * renders it and never the live inspector detail. */
  objectDelete: ObjectDeleteOperation | null = $state(null);
  selectedDelete: SelectedDeleteOperation | null = $state(null);

  // Find in container (§16): explicit scan, never per-keystroke.
  #findOpen = $state(false);
  #findPanelRevision = $state(0);
  get findPanelRevision(): number { return this.#findPanelRevision; }
  get findOpen(): boolean { return this.#findOpen; }
  set findOpen(value: boolean) {
    if (!value) this.closeFind();
    else if (!this.#findOpen) {
      this.#findPanelRevision++;
      this.#findOpen = true;
    }
  }
  findStatus: FindStatusDTO | null = $state(null);
  findRunning = $state(false);
  /** The count of objects under the open prefix (ADR 0072 decision 2): a
   * count-only scan's status while it streams, then its final count with
   * the time it was taken. It belongs to the scope it was started in. */
  countStatus: FindStatusDTO | null = $state(null);
  countRunning = $state(false);
  countedAt: string | null = $state(null);
  #countSeq = 0;
  #countRequest: FindRequest | null = null;
  findNotice: string | null = $state(null);
  /** What the last COMPLETED metadata save did not establish. It outlives
   * the editor, which closes itself on success, so the qualification does
   * not vanish with the panel that earned it (ADR 0046). */
  mutationNotice: string | null = $state(null);
  #findSeq = 0;
  #findRequest: FindRequest | null = null;
  #findResults: FindRequest | null = null;

  // Containers pane.
  mode: ContainerMode = $state("logical");
  containers: ContainerRowDTO[] = $state([]);
  containersMarker = $state("");
  containersError: string | null = $state(null);
  #containersSeq = 0;
  #containersLoadingScope = $state<number | null>(null);
  #containersPage = $state.raw<PageRequest | null>(null);
  #containersPageError = $state.raw<(PageRequest & { message: string }) | null>(null);

  get containersLoading(): boolean { return this.#containersLoadingScope === this.#scopeRevision; }
  get containersMoreLoading(): boolean { return this.#containersPage !== null && this.#currentContainersPage(this.#containersPage); }
  get containersMoreError(): string | null {
    return this.#containersPageError && this.#currentContainersPage(this.#containersPageError) ? this.#containersPageError.message : null;
  }

  #currentContainersPage(request: PageRequest): boolean {
    return !this.connecting && this.connectedBookmarkId !== null && request.scope === this.#scopeRevision
      && request.seq === this.#containersSeq && request.marker === this.containersMarker;
  }

  // Working list.
  #containerName: string | null = $state(null);
  get containerName(): string | null { return this.#containerName; }
  set containerName(value: string | null) {
    if (value !== this.#containerName) {
      this.#clearFind();
      this.#scopeRevision++;
    }
    this.#containerName = value;
  }
  /** The open container's §53 HEAD detail; null while loading/closed. */
  containerDetail: ContainerDetailDTO | null = $state(null);
  /** Why the open container's HEAD failed, so the absence of a settings
   * banner is never silent (ADR 0071 decision 6); null when it succeeded
   * or has not been asked. */
  containerDetailError: string | null = $state(null);
  /** The operator-started settings check over the loaded rows (ADR 0071
   * decision 5): progress, and whether it is running or was stopped. */
  settingsCheck: { running: boolean; checked: number; total: number; stopped: boolean } | null = $state(null);
  #settingsCheckSeq = 0;
  /** Rows whose HEAD failed during a check, by name, with the reason. */
  settingsErrors: Record<string, string> = $state({});
  /** Set while viewing a manifest's segment location (§23.1 backstage
   * jump): the label of where we came from; null in normal browsing. */
  backstageFrom: string | null = $state(null);
  #prefix = $state("");
  get prefix(): string { return this.#prefix; }
  set prefix(value: string) {
    if (value !== this.#prefix) {
      this.#clearFind();
      this.#scopeRevision++;
    }
    this.#prefix = value;
  }
  #filter = $state("");

  /** Loaded-row filter. Setting it re-anchors the selection BY IDENTITY:
   * the selected identity survives a filter change, and when it is
   * filtered out the selection (and dependent detail state) clears —
   * an index can never silently point at a different object (the
   * review wave's C-04). */
  get filter(): string {
    return this.#filter;
  }

  set filter(value: string) {
    const selected = this.selectedEntry;
    this.#filter = value;
    this.#followSelection(selected);
  }

  /** After the visible rows changed, the selection follows its identity
   * (the review wave's C-04): the same row at a new position moves the
   * index without re-running selection side effects; a row whose listing
   * facts changed is selected again so the inspector HEADs it afresh
   * (§14.2); a row no longer visible clears the selection and the state
   * that depended on it. */
  #followSelection(selected: ListingRowDTO | null) {
    if (this.#chosen.size > 0) {
      this.#followChosen(selected);
      return;
    }
    if (selected === null) {
      return;
    }
    const idx = this.visibleEntries.findIndex((e) => e.name === selected.name && e.kind === selected.kind);
    if (idx < 0) {
      this.select(-1);
      return;
    }
    if (sameListingFacts(this.visibleEntries[idx], selected)) {
      this.selectedIndex = idx;
    } else {
      this.select(idx);
    }
  }

  /** A multi-selection follows its identities too (ADR 0093 decision 1):
   * rows no longer visible leave it, the focused row keeps its place, and
   * a selection left with one row becomes that row's plain selection.
   * Changed facts are not re-read here, because no row is inspected; the
   * selected-set dialog compares them against its own snapshot. */
  #followChosen(focused: ListingRowDTO | null) {
    const visible = this.visibleEntries;
    const present = new Set(visible.map(rowIdentity));
    const kept = new Set([...this.#chosen].filter((key) => present.has(key)));
    if (kept.size >= 2) {
      this.#chosen = kept;
      const focus = focused ? visible.findIndex((e) => rowIdentity(e) === rowIdentity(focused)) : -1;
      this.selectedIndex = focus >= 0 ? focus : visible.findIndex((e) => kept.has(rowIdentity(e)));
      return;
    }
    this.#chosen = new Set();
    const only = [...kept][0];
    this.#inspect(only === undefined ? -1 : visible.findIndex((e) => rowIdentity(e) === only));
  }
  entries: ListingRowDTO[] = $state([]);
  entriesMarker = $state("");
  /** The scope the loaded rows were read under, for a request that names
   * the portrait its view was built under (ADR 0079). */
  #entriesScope: ListingScope | null = null;
  entriesLoading = $state(false);
  entriesError: string | null = $state(null);
  /** An in-place re-read of the loaded listing is in flight; the rows stay
   * on screen meanwhile, unlike a navigation's load. */
  entriesRefreshing = $state(false);
  selectedIndex = $state(-1);
  /** The rows a multi-selection holds, by identity (ADR 0093 decision 1):
   * presentation only, and empty unless at least two rows are chosen, so a
   * single choice is always the inspected plain selection. */
  #chosen = $state<ReadonlySet<string>>(new Set());
  /** The row a Shift click extends from, by identity: the last row plainly
   * chosen or toggled, so a refresh or a filter moves the anchor with its
   * row rather than to whatever sits at its old index. */
  #anchorKey: string | null = null;
  /** The exact identity the current detail state was fetched for —
   * every mutation binds THIS, never the live index. */
  detailIdentity = $state<{ container: string; name: string } | null>(null);

  // Inspector detail (selection-triggered HEAD, §14.2): an object row
  // fetches its HEAD (and, for an SLO, its exact manifest), a prefix row
  // its PrefixDetail classification.
  objectDetail: ObjectDetailDTO | null = $state(null);
  prefixDetail: PrefixDetailDTO | null = $state(null);
  sloManifest: SLOManifestDTO | null = $state(null);
  dloSegments: DLOSegmentsDTO | null = $state(null);
  detailLoading = $state(false);
  detailError: string | null = $state(null);
  #detailSeq = 0;
  #detailScope = -1;
  #containerDetailSeq = 0;
  #containerDetailConnection = -1;
  #accountSeq = 0;
  #listSeq = 0;
  #entriesPage = $state.raw<PageRequest | null>(null);
  #entriesPageError = $state.raw<(PageRequest & { message: string }) | null>(null);

  get entriesMoreLoading(): boolean { return this.#entriesPage !== null && this.#currentEntriesPage(this.#entriesPage); }
  get entriesMoreError(): string | null {
    return this.#entriesPageError && this.#currentEntriesPage(this.#entriesPageError) ? this.#entriesPageError.message : null;
  }

  #currentEntriesPage(request: PageRequest): boolean {
    return !this.connecting && this.connectedBookmarkId !== null && request.scope === this.#scopeRevision
      && request.seq === this.#listSeq && request.marker === this.entriesMarker;
  }

  #currentDetail(request: DetailRequest): boolean {
    return !this.connecting && request.scope === this.#scopeRevision && request.seq === this.#detailSeq
      && request.container === this.containerName
      && request.container === this.detailIdentity?.container && request.name === this.detailIdentity?.name;
  }

  #currentContainerDetail(scope: number, seq: number, name: string): boolean {
    return !this.connecting && scope === this.#scopeRevision && seq === this.#containerDetailSeq
      && name === this.containerName;
  }

  async loadBookmarks(): Promise<void> {
    const seq = ++this.#bookmarksSeq;
    this.bookmarksLoading = true;
    this.bookmarksError = null;
    this.bookmarksStale = this.bookmarksLoaded;
    try {
      const bookmarks = await this.#api.listBookmarks();
      if (seq === this.#bookmarksSeq) {
        this.bookmarks = bookmarks;
        this.bookmarksLoaded = true;
        this.bookmarksStale = false;
      }
    } catch {
      if (seq === this.#bookmarksSeq) {
        this.bookmarksError = "Bookmarks unavailable. Refresh the list.";
        this.bookmarksStale = true;
      }
    } finally {
      if (seq === this.#bookmarksSeq) this.bookmarksLoading = false;
    }
  }

  #retireBookmarkList() {
    this.#bookmarksSeq++;
    this.bookmarksLoading = false;
    this.bookmarksStale = true;
  }

  #resetConnection() {
    this.#scopeRevision++;
    this.#connectionRevision++;
    this.connectedBookmarkId = null;
    this.account = null;
    this.accountError = null;
    this.#accountSeq++;
    this.capabilities = null;
    this.connectError = null;
    this.#containersSeq++;
    this.#containersLoadingScope = null;
    this.#containersPage = null;
    this.#containersPageError = null;
    this.containers = [];
    this.settingsErrors = {};
    this.settingsCheck = null;
    this.containerDetailError = null;
    this.containersMarker = "";
    this.containersError = null;
    this.#containerDetailSeq++;
    this.containerName = null;
    this.containerDetail = null;
    this.containerDetailError = null;
    this.#containerDetailConnection = -1;
    this.backstageFrom = null;
    this.prefix = "";
    this.#listSeq++;
    this.#entriesPage = null;
    this.#entriesPageError = null;
    this.entries = [];
    this.entriesMarker = "";
    this.#entriesScope = null;
    this.entriesLoading = false;
    this.entriesError = null;
    this.filter = "";
    this.select(-1);
    this.transferOpen = false;
    this.localTransfer = null;
    this.deleteOpen = false;
    this.objectDelete = null;
    this.selectedDelete = null;
    this.findOpen = false;
  }

  #beginBookmarkChange(id: string) {
    if (id !== "") this.#bookmarkChanges.set(id, (this.#bookmarkChanges.get(id) ?? 0) + 1);
    if (id === this.connectedBookmarkId || id === this.#connectingBookmarkId) this.#resetConnection();
  }

  #endBookmarkChange(id: string) {
    const remaining = (this.#bookmarkChanges.get(id) ?? 0) - 1;
    if (remaining > 0) this.#bookmarkChanges.set(id, remaining);
    else this.#bookmarkChanges.delete(id);
  }

  #invalidateCredentialStatus() {
    this.#credentialStatusSeq++;
    this.credentialStatus = null;
    this.credentialStatusError = false;
    this.credentialStatusLoading = true;
  }

  async loadCredentialStatus(): Promise<void> {
    this.#invalidateCredentialStatus();
    const seq = this.#credentialStatusSeq;
    try {
      const status = await this.#api.credentialStatus();
      if (seq === this.#credentialStatusSeq) {
        this.credentialStatus = status;
      }
    } catch {
      if (seq === this.#credentialStatusSeq) {
        this.credentialStatusError = true;
      }
    } finally {
      if (seq === this.#credentialStatusSeq) {
        this.credentialStatusLoading = false;
      }
    }
  }

  /** Checks typed bookmark details against the endpoint without saving. */
  async verifyBookmark(bookmark: BookmarkDTO, secret: string): Promise<VerifyResultDTO> {
    return await this.#api.verifyBookmark(bookmark, secret);
  }

  async copyText(text: string): Promise<void> {
    await this.#api.copyText(text);
  }

  async deleteBookmark(id: string): Promise<void> {
    this.#beginBookmarkChange(id);
    this.#retireBookmarkList();
    this.#invalidateCredentialStatus();
    try {
      try {
        await this.#api.deleteBookmark(id);
      } finally {
        // A read started during the mutation can still predate its effect,
        // including an uncertain effect reported as an error.
        this.#retireBookmarkList();
      }
      this.bookmarks = this.bookmarks.filter((b) => b.id !== id);
    } finally {
      this.#endBookmarkChange(id);
      // Refresh even after an uncertain result; status must not change the
      // mutation's outcome or delay reporting it while the bridge is busy.
      void this.loadCredentialStatus();
    }
  }

  async saveBookmark(bookmark: BookmarkDTO, secret: string): Promise<BookmarkDTO> {
    const id = bookmark.id;
    this.#beginBookmarkChange(id);
    this.#retireBookmarkList();
    this.#invalidateCredentialStatus();
    try {
      let saved: BookmarkDTO;
      try {
        saved = await this.#api.saveBookmark(bookmark, secret);
      } finally {
        // Retire reads across the possible effect before the acknowledged
        // Save's own refresh, not after that refresh has settled.
        this.#retireBookmarkList();
      }
      await this.loadBookmarks();
      return saved;
    } finally {
      this.#endBookmarkChange(id);
      // A failed save can leave a prepared key even without a bookmark row.
      void this.loadCredentialStatus();
    }
  }

  async connect(bookmarkId: string, ownsView = () => true): Promise<boolean> {
    // Ignoring a stale reply cannot undo the backend's implicit active session.
    // Hold this slot through all dependent reads, even after UI invalidation.
    if (this.#connectingBookmarkId !== null) throw new Error("A connection attempt is already in progress");
    if (!ownsView()) return false;
    if (this.#bookmarkChanges.has(bookmarkId)) {
      this.connectError = "This bookmark is still being changed. Try connecting when it finishes.";
      throw new Error(this.connectError);
    }
    this.#resetConnection();
    const scope = this.#scopeRevision;
    const current = () => scope === this.#scopeRevision && ownsView();
    this.#connectingBookmarkId = bookmarkId;
    this.connecting = true;
    try {
      if (bookmarkId === "") throw new Error("Choose a bookmark before connecting.");
      const account = await this.#api.connect(bookmarkId);
      if (!current()) return false;
      if (!this.#answeredBy(account.scope, bookmarkId) ||
        !Number.isSafeInteger(account.scope.generation) || account.scope.generation <= 0) {
        throw new Error("The account portrait does not match this connection. Reconnect and try again.");
      }
      const generation = account.scope.generation;
      const capabilities = await this.#api.capabilities(bookmarkId);
      if (!current()) return false;
      if (!this.#answeredBy(capabilities.scope, bookmarkId) || capabilities.scope.generation !== generation) {
        throw new Error("The capabilities do not match this connection. Reconnect and try again.");
      }
      let containersError: string | null = null;
      const containers = await this.#api.containers(bookmarkId, "", pageLimit)
        .then((page) => {
          if (!this.#answeredBy(page.scope, bookmarkId) || page.scope.generation !== generation) {
            throw new Error("The container listing does not match this connection and was not shown. Reconnect and try again.");
          }
          return page;
        }).catch((err) => {
          containersError = messageOf(err);
          return { rows: [], nextMarker: "", effectiveLimit: pageLimit, scope: { bookmarkId, generation } };
        });
      if (!current()) return false;
      await this.audit.loadRecent();
      if (!current()) return false;
      this.account = account;
      this.capabilities = capabilities;
      this.containers = containers.rows;
      this.containersMarker = containers.nextMarker;
      this.containersError = containersError;
      this.connectedBookmarkId = bookmarkId;
      return true;
    } catch (err) {
      if (current()) this.connectError = messageOf(err);
      throw err;
    } finally {
      this.#connectingBookmarkId = null;
      this.connecting = false;
    }
  }

  /** A page is applied only when the account that answered is the one this
   * client asked for. The scope counter above covers transitions this
   * client makes; it cannot see a connection moved through the bridge by
   * anything else, because it tracks intent rather than the backend's
   * state. The reply states who answered, so that gap closes here
   * (ADR 0049). */
  #answeredBy(scope: ListingScope | null | undefined, bookmarkId: string): boolean {
    return scope != null && scope.bookmarkId === bookmarkId;
  }

  #requireListingScope(scope: ListingScope | null | undefined, bookmarkId: string, portrait: ListingScope | null) {
    if (portrait === null || portrait.bookmarkId !== bookmarkId ||
      !Number.isSafeInteger(portrait.generation) || portrait.generation <= 0 ||
      scope == null || scope.bookmarkId !== portrait.bookmarkId || scope.generation !== portrait.generation) {
      throw new Error("The listing does not match this connection and was not shown. Reconnect and try again.");
    }
  }

  /** The portrait a live read captures when it starts: a copy of this
   * account's scope, or null when nothing is connected. */
  #portrait(): ListingScope | null {
    const scope = this.account?.scope;
    return scope ? { ...scope } : null;
  }

  /** The six live readers (object, prefix and container detail, manifest,
   * segments, Find status) accept a reply only under the bookmark and
   * generation of the portrait captured when the read started (ADR 0075
   * decision 1). #answeredBy stays bookmark-only for the callers that keep
   * it (ADR 0067 decision 4). */
  #readerScopeMatches(scope: ListingScope | null | undefined, bookmarkId: string, portrait: ListingScope | null): boolean {
    return portrait !== null && portrait.bookmarkId === bookmarkId &&
      Number.isSafeInteger(portrait.generation) && portrait.generation > 0 &&
      scope != null && scope.bookmarkId === portrait.bookmarkId && scope.generation === portrait.generation;
  }

  #requireReaderScope(scope: ListingScope | null | undefined, bookmarkId: string, portrait: ListingScope | null, what: string) {
    if (!this.#readerScopeMatches(scope, bookmarkId, portrait)) {
      throw new Error(`The ${what} does not match this connection and was not shown. Reconnect and try again.`);
    }
  }

  async loadContainers() {
    const bookmarkId = this.connectedBookmarkId;
    if (bookmarkId === null) return;
    const portrait = this.account ? { ...this.account.scope } : null;
    const scope = this.#scopeRevision;
    const seq = ++this.#containersSeq;
    this.#containersLoadingScope = scope;
    this.#containersPage = null;
    this.#containersPageError = null;
    this.containersMarker = "";
    this.containersError = null;
    try {
      const page = await this.#api.containers(bookmarkId, "", pageLimit);
      if (scope !== this.#scopeRevision || seq !== this.#containersSeq) return;
      this.#requireListingScope(page.scope, bookmarkId, portrait);
      this.containers = page.rows;
      this.containersMarker = page.nextMarker;
    } catch (err) {
      if (scope === this.#scopeRevision && seq === this.#containersSeq) this.containersError = messageOf(err);
    } finally {
      if (seq === this.#containersSeq) this.#containersLoadingScope = null;
    }
  }

  /** The retention ceiling in force (ADR 0074 decision 1): the operator's
   * setting once read, otherwise its default. A column at the ceiling
   * keeps every row it holds and loads no further page. */
  /** The most members one selected-set deletion admits: the download
   * object ceiling setting, which ADR 0093 decision 2 reuses. */
  get selectionCeiling(): number {
    return this.settings.document?.downloadObjectCeiling ?? defaultDownloadObjectCeiling;
  }

  get retentionCeiling(): number {
    return this.settings.document?.browserRetentionCeiling ?? defaultBrowserRetentionCeiling;
  }

  get containersAtCeiling(): boolean {
    return this.containersMarker !== "" && this.containers.length >= this.retentionCeiling;
  }

  get entriesAtCeiling(): boolean {
    return this.entriesMarker !== "" && this.entries.length >= this.retentionCeiling;
  }

  /** A column that reaches the default ceiling before the settings were
   * shown reads them, so the stop reflects the operator's ceiling rather
   * than the default. */
  #readCeilingWhenReached(rows: number) {
    if (this.settings.document === null && rows >= this.retentionCeiling) void this.settings.load();
  }

  async loadMoreContainers() {
    if (this.containersMarker === "" || this.connecting || !this.connectedBookmarkId
      || this.containersLoading || this.containersError || this.containersMoreLoading
      || this.containersAtCeiling) {
      return;
    }
    const bookmarkId = this.connectedBookmarkId;
    if (bookmarkId === null) return;
    const portrait = this.account ? { ...this.account.scope } : null;
    const request = { scope: this.#scopeRevision, seq: this.#containersSeq, marker: this.containersMarker };
    this.#containersPage = request;
    this.#containersPageError = null;
    try {
      const page = await this.#api.containers(bookmarkId, request.marker, pageLimit);
      if (!this.#currentContainersPage(request)) return;
      this.#requireListingScope(page.scope, bookmarkId, portrait);
      requireAdvancingMarker(request.marker, page.nextMarker);
      const names = new Set(this.containers.map((row) => row.name));
      const rows = page.rows.filter((row) => {
        if (names.has(row.name)) return false;
        names.add(row.name);
        return true;
      });
      this.containers = [...this.containers, ...rows];
      this.containersMarker = page.nextMarker;
      this.#readCeilingWhenReached(this.containers.length);
    } catch (err) {
      if (!this.#currentContainersPage(request)) return;
      this.#containersPageError = { ...request, message: messageOf(err) };
      throw err;
    } finally {
      if (this.#containersPage === request) this.#containersPage = null;
    }
  }

  /** The §52 modes over the service's §24 classification. */
  get containerGroups(): ContainerGroups {
    if (this.mode === "logical") {
      return {
        main: this.containers.filter((c) => c.class === "normal"),
        suspected: this.containers.filter((c) => c.class === "suspected"),
        hiddenBackstage: this.containers.filter(
          (c) => c.class === "confirmed" || c.class === "declared",
        ).length,
      };
    }
    return { main: this.containers, suspected: [], hiddenBackstage: 0 };
  }

  get container(): ContainerRowDTO | null {
    return this.containers.find((c) => c.name === this.containerName) ?? null;
  }

  get visibleEntries(): ListingRowDTO[] {
    if (this.filter === "") {
      return this.entries;
    }
    const needle = this.filter.toLowerCase();
    return this.entries.filter((e) => e.name.toLowerCase().includes(needle));
  }

  get selectedEntry(): ListingRowDTO | null {
    return this.visibleEntries[this.selectedIndex] ?? null;
  }

  /** What Copy and Download act on (ADR 0108 decision 1): the chosen
   * objects, the one selected object, the one selected folder, or else the
   * folder being browsed (the container at its top). Folder rows in a
   * multi-selection are not members; a multi-selection of folder rows only
   * has no members, which callers must refuse rather than send, because no
   * members means the whole scope. */
  get transferSource(): TransferSource {
    const chosen = this.chosenRows;
    if (chosen.length >= 2) {
      const members = [...new Set(chosen.filter((row) => row.kind === "object").map((row) => row.name))];
      const excludedFolders = chosen.filter((row) => row.kind === "prefix").length;
      return { kind: "selected", prefix: this.prefix, members, excludedFolders };
    }
    const entry = this.selectedEntry;
    if (entry?.kind === "object") return { kind: "object", prefix: this.prefix, members: [entry.name], excludedFolders: 0 };
    if (entry?.kind === "prefix") return { kind: "folder", prefix: entry.name, members: [], excludedFolders: 0 };
    return { kind: this.prefix === "" ? "container" : "folder", prefix: this.prefix, members: [], excludedFolders: 0 };
  }

  /** The selected row's prefix when exactly one row is selected and it is a
   * prefix, otherwise "": the folder a deletion offers (ADR 0095 decision 7). */
  get selectedPrefix(): string {
    const entry = this.chosenRows.length < 2 ? this.selectedEntry : null;
    return entry?.kind === "prefix" ? entry.name : "";
  }

  /** The rows a multi-selection holds, in listing order; empty unless at
   * least two rows are chosen (ADR 0093 decision 1). */
  get chosenRows(): ListingRowDTO[] {
    const chosen = this.#chosen;
    if (chosen.size === 0) return [];
    return this.visibleEntries.filter((e) => chosen.has(rowIdentity(e)));
  }

  /** How many rows are selected: the multi-selection's, or the one row of
   * a plain selection. */
  get selectionCount(): number {
    const chosen = this.chosenRows.length;
    return chosen > 0 ? chosen : this.selectedIndex >= 0 ? 1 : 0;
  }

  /** Whether a row is selected: a member of the multi-selection, or the
   * one row of a plain selection. Focus is not membership: with several
   * rows chosen the focused row may have been toggled off. */
  isChosen(row: ListingRowDTO): boolean {
    if (this.#chosen.size > 0) return this.#chosen.has(rowIdentity(row));
    const selected = this.selectedEntry;
    return selected !== null && rowIdentity(selected) === rowIdentity(row);
  }

  async openContainer(name: string) {
    if (this.connecting || !this.connectedBookmarkId) return;
    this.containerName = name;
    this.containerDetail = null;
    this.containerDetailError = null;
    this.backstageFrom = null;
    // Prefix reset belongs to this navigation, before its HEAD captures scope.
    this.prefix = "";
    // §14.2: opening IS selecting the container — one HEAD for the
    // inspector; failure falls back to the listing row's fields.
    void this.refreshContainerDetail();
    await this.#loadEntries("");
  }

  /** Enter/double-click: prefixes drill in, objects just stay selected. */
  async activate(entry: ListingRowDTO) {
    if (entry.kind === "prefix") {
      await this.goTo(entry.name);
    }
  }

  async goTo(prefix: string) {
    if (!this.containerName || this.connecting || !this.connectedBookmarkId) {
      return;
    }
    await this.#loadEntries(prefix);
  }

  /** Backspace / Alt+↑ (§4.4): parent prefix, then out of the container. */
  async up() {
    if (!this.containerName || this.connecting || !this.connectedBookmarkId) {
      return;
    }
    if (this.prefix === "") {
      await this.#leaveContainer();
      return;
    }
    await this.#loadEntries(parentPrefix(this.prefix));
  }

  /** Return from the open container to the account view. */
  async #leaveContainer() {
    this.containerName = null;
    this.prefix = "";
    this.containerDetail = null;
    this.containerDetailError = null;
    this.entries = [];
    this.filter = "";
    this.select(-1);
    // Inspection may have taught the evidence engine (§24): returning
    // to the account view re-reads classifications.
    await this.loadContainers();
  }

  async #loadEntries(prefix: string) {
    this.#clearFind();
    const seq = ++this.#listSeq;
    // A navigation supersedes an in-place refresh; its rows arrive here.
    this.entriesRefreshing = false;
    this.#entriesPage = null;
    this.#entriesPageError = null;
    this.entriesMarker = "";
    this.prefix = prefix;
    this.filter = "";
    this.select(-1);
    const scope = this.#scopeRevision;
    const bookmarkId = this.connectedBookmarkId;
    const portrait = this.account ? { ...this.account.scope } : null;
    this.entriesLoading = true;
    this.entriesError = null;
    try {
      if (bookmarkId === null) return;
      const page = await this.#api.objects(bookmarkId, this.containerName ?? "", prefix, "", pageLimit);
      if (seq !== this.#listSeq || scope !== this.#scopeRevision) {
        return; // a newer navigation superseded this load
      }
      this.#requireListingScope(page.scope, bookmarkId, portrait);
      this.#entriesScope = page.scope;
      this.entries = page.rows;
      this.entriesMarker = page.nextMarker;
    } catch (err) {
      if (seq === this.#listSeq && scope === this.#scopeRevision) {
        this.entries = [];
        this.entriesError = messageOf(err);
      }
    } finally {
      if (seq === this.#listSeq && scope === this.#scopeRevision) {
        this.entriesLoading = false;
      }
    }
  }

  /** Re-read the open listing in place: the first page of the current
   * prefix replaces the loaded rows, the filter stays and the selection
   * follows its identity. Navigation is `goTo`, which starts clean; this
   * is for a listing that changed underneath the operator, by a job they
   * started or when they ask. Pages beyond the first are dropped, as a
   * navigation drops them. The listing stays advisory (§15.3): a row's
   * absence here proves nothing.
   *
   * The newest read owns the listing's lifecycle. A refresh that starts
   * while a navigation is still loading reads the prefix that navigation
   * set, so it finishes that navigation too: the rows stay hidden until
   * it settles and it clears the loading flag the superseded read can no
   * longer clear. A failed read leaves the same state a failed navigation
   * leaves: no rows, the error, and no selection, so a later read cannot
   * pair an old identity's detail with a row that happens to sit at the
   * old index. */
  async refreshEntries() {
    const container = this.containerName;
    const bookmarkId = this.connectedBookmarkId;
    if (!container || this.connecting || !bookmarkId) return;
    const seq = ++this.#listSeq;
    this.#entriesPage = null;
    this.#entriesPageError = null;
    const scope = this.#scopeRevision;
    const portrait = this.account ? { ...this.account.scope } : null;
    this.entriesRefreshing = true;
    try {
      const page = await this.#api.objects(bookmarkId, container, this.prefix, "", pageLimit);
      if (seq !== this.#listSeq || scope !== this.#scopeRevision) {
        return; // a navigation or a later refresh superseded this read
      }
      this.#requireListingScope(page.scope, bookmarkId, portrait);
      this.#entriesScope = page.scope;
      const selected = this.selectedEntry;
      this.entries = page.rows;
      this.entriesMarker = page.nextMarker;
      this.entriesError = null;
      this.#followSelection(selected);
    } catch (err) {
      if (seq === this.#listSeq && scope === this.#scopeRevision) {
        this.entries = [];
        this.entriesError = messageOf(err);
        this.select(-1);
      }
    } finally {
      if (seq === this.#listSeq && scope === this.#scopeRevision) {
        this.entriesRefreshing = false;
        this.entriesLoading = false;
      }
    }
  }

  /** Everything the open view shows that a finished job may have changed:
   * the listing, the container column's counts and the open container's
   * HEAD. */
  async refreshView() {
    await Promise.all([this.refreshEntries(), this.loadContainers(), this.refreshContainerDetail()]);
  }

  async loadMoreEntries() {
    if (this.entriesMarker === "" || !this.containerName || this.connecting || !this.connectedBookmarkId
      || this.entriesLoading || this.entriesError || this.entriesMoreLoading
      || this.entriesAtCeiling) {
      return;
    }
    const bookmarkId = this.connectedBookmarkId;
    const portrait = this.account ? { ...this.account.scope } : null;
    const request = { scope: this.#scopeRevision, seq: this.#listSeq, marker: this.entriesMarker };
    this.#entriesPage = request;
    this.#entriesPageError = null;
    try {
      const page = await this.#api.objects(bookmarkId, this.containerName, this.prefix, request.marker, pageLimit);
      if (!this.#currentEntriesPage(request)) return;
      this.#requireListingScope(page.scope, bookmarkId, portrait);
      this.#entriesScope = page.scope;
      requireAdvancingMarker(request.marker, page.nextMarker);
      const identity = (row: ListingRowDTO) => row.kind + ":" + row.name;
      const names = new Set(this.entries.map(identity));
      const rows = page.rows.filter((row) => {
        const name = identity(row);
        if (names.has(name)) return false;
        names.add(name);
        return true;
      });
      this.entries = [...this.entries, ...rows];
      this.entriesMarker = page.nextMarker;
      this.#readCeilingWhenReached(this.entries.length);
    } catch (err) {
      if (!this.#currentEntriesPage(request)) return;
      this.#entriesPageError = { ...request, message: messageOf(err) };
      throw err;
    } finally {
      if (this.#entriesPage === request) this.#entriesPage = null;
    }
  }

  /** A plain selection: one row, which triggers its authoritative HEAD
   * (§14.2), object detail or prefix classification, and which ends any
   * multi-selection (ADR 0093 decision 1). */
  select(index: number) {
    this.#chosen = new Set();
    const row = this.visibleEntries[index];
    this.#anchorKey = row ? rowIdentity(row) : null;
    this.#inspect(index);
  }

  /** Toggle a row's membership, a Ctrl or Cmd click (ADR 0093 decision 1).
   * The plain selection is a member too: toggling another row beside it
   * keeps both, and toggling it alone empties the selection; a selection
   * left with one row collapses to that row's plain selection. */
  toggleRow(index: number) {
    const visible = this.visibleEntries;
    const row = visible[index];
    if (!row) return;
    const chosen = new Set(this.#chosen);
    if (chosen.size === 0 && this.selectedIndex >= 0) {
      const focused = visible[this.selectedIndex];
      if (focused) chosen.add(rowIdentity(focused));
    }
    const key = rowIdentity(row);
    if (chosen.has(key)) chosen.delete(key);
    else chosen.add(key);
    this.#anchorKey = key;
    this.#applyChosen(chosen, index);
  }

  /** Select the rows from the anchor to this one, replacing the
   * multi-selection: a Shift click (ADR 0093 decision 1). The anchor is
   * found by identity; when its row is gone the focused row stands in. */
  extendTo(index: number) {
    const visible = this.visibleEntries;
    if (!visible[index]) return;
    let anchor = this.#anchorKey === null ? -1 : visible.findIndex((e) => rowIdentity(e) === this.#anchorKey);
    if (anchor < 0) anchor = this.selectedIndex >= 0 && this.selectedIndex < visible.length ? this.selectedIndex : index;
    this.#anchorKey = rowIdentity(visible[anchor]);
    const [from, to] = anchor <= index ? [anchor, index] : [index, anchor];
    this.#applyChosen(new Set(visible.slice(from, to + 1).map(rowIdentity)), index);
  }

  /** Select every loaded row on screen and nothing beyond them (ADR 0072
   * decision 3, ADR 0093 decision 1): the filter narrows what is on screen,
   * so it narrows this too, and rows the server still holds are not here. */
  selectAll() {
    const visible = this.visibleEntries;
    if (visible.length === 0) return;
    const focus = this.selectedIndex >= 0 && this.selectedIndex < visible.length ? this.selectedIndex : 0;
    this.#applyChosen(new Set(visible.map(rowIdentity)), focus);
  }

  /** Nothing selected: Escape (ADR 0093 decision 1). */
  clearSelection() {
    this.select(-1);
  }

  #applyChosen(chosen: Set<string>, focus: number) {
    if (chosen.size >= 2) {
      this.#chosen = chosen;
      this.#focus(focus);
      return;
    }
    this.#chosen = new Set();
    const only = [...chosen][0];
    // The plain selection a multi-selection collapses to is the anchor too.
    this.#anchorKey = only ?? null;
    this.#inspect(only === undefined ? -1 : this.visibleEntries.findIndex((e) => rowIdentity(e) === only));
  }

  /** With several rows selected no row is inspected (ADR 0093 decision 1):
   * the focused row keeps the list's keyboard position only, and the detail
   * state is cleared so no mutation can bind a row the inspector no longer
   * shows and no HEAD is made for the summary. */
  #focus(index: number) {
    this.selectedIndex = index;
    this.mutationNotice = null;
    ++this.#detailSeq;
    this.detailIdentity = null;
    this.objectDetail = null;
    this.prefixDetail = null;
    this.sloManifest = null;
    this.dloSegments = null;
    this.detailError = null;
    this.detailLoading = false;
  }

  /** The inspected row: its authoritative HEAD (§14.2), object detail or
   * prefix classification. */
  #inspect(index: number) {
    this.selectedIndex = index;
    // The qualification belongs to the object it was earned on.
    this.mutationNotice = null;
    const entry = this.selectedEntry;
    const seq = ++this.#detailSeq;
    this.#detailScope = this.#scopeRevision;
    this.detailIdentity =
      entry && this.containerName
        ? { container: this.containerName, name: entry.name }
        : null;
    this.objectDetail = null;
    this.prefixDetail = null;
    this.sloManifest = null;
    this.dloSegments = null;
    this.detailError = null;
    const container = this.containerName;
    const bookmarkId = this.connectedBookmarkId;
    const portrait = this.#portrait();
    if (!entry || !container || !bookmarkId || this.connecting) {
      this.detailLoading = false;
      return;
    }
    this.detailLoading = true;
    const request = { scope: this.#scopeRevision, seq, container, name: entry.name };
    const fetch =
      entry.kind === "object"
        ? this.#api.objectDetail(container, entry.name, bookmarkId).then(async (detail) => {
            if (!this.#currentDetail(request)) {
              return;
            }
            this.#requireReaderScope(detail.scope, bookmarkId, portrait, "object detail");
            if (detail.name !== request.name) throw new Error("object detail does not match the selected name");
            this.objectDetail = detail;
            if (detail.class === "slo") {
              // The exact segment table (§23.2) — still one selection.
              const manifest = await this.#api.sloManifest(container, entry.name, bookmarkId);
              if (this.#currentDetail(request)) {
                this.#requireReaderScope(manifest.scope, bookmarkId, portrait, "manifest");
                this.sloManifest = manifest;
              }
            } else if (detail.class === "dlo" && detail.manifest !== "") {
              // The bounded §23.1 preview.
              const preview = await this.#api.dloSegments(detail.manifest, bookmarkId);
              if (this.#currentDetail(request)) {
                this.#requireReaderScope(preview.scope, bookmarkId, portrait, "segment preview");
                this.dloSegments = preview;
              }
            }
          })
        : this.#api.prefixDetail(container, entry.name, bookmarkId).then((detail) => {
            if (this.#currentDetail(request)) {
              this.#requireReaderScope(detail.scope, bookmarkId, portrait, "prefix detail");
              if (detail.prefix !== request.name) throw new Error("prefix detail does not match the selected name");
              this.prefixDetail = detail;
            }
          });
    fetch
      .catch((err) => {
        if (this.#currentDetail(request)) {
          this.detailError = messageOf(err);
        }
      })
      .finally(() => {
        if (this.#currentDetail(request)) {
          this.detailLoading = false;
        }
      });
  }

  // ---- Mutations (§65.2 items 6/7): thin, state-refreshing wrappers
  // over MutateService. Outcome handling — conflict banners, overwrite
  // choices — belongs to the editors; state here only stays coherent.


  /** Jobs this client started whose end should refresh the view they
   * wrote to, by id, with the account and container they changed. */
  #watchedJobs = new Map<string, WatchedChange>();

  /** Follow one job this client started until it ends, then re-read what
   * it changed if that is still on screen: the open container's view, or
   * only the container column when the job wrote elsewhere in the same
   * account. The drawer's list poll cannot serve here because the operator
   * filters it and it is capped at fifty rows, so a job can end outside
   * it; the item page polls the job itself for the same reason (ADR 0030). */
  #watchJob(id: string, changed: WatchedChange) {
    if (this.#watchedJobs.has(id)) return;
    this.#watchedJobs.set(id, changed);
    let misses = 0;
    const tick = async () => {
      let job: JobDTO | null = null;
      try {
        job = await this.#api.job(id, changed.bookmarkId);
      } catch {
        job = null;
      }
      if (job === null) {
        // Three unreadable polls in a row: the job or its account is gone.
        if (++misses >= 3) {
          this.#watchedJobs.delete(id);
          return;
        }
      } else if (terminalJobStates.includes(job.state)) {
        this.#watchedJobs.delete(id);
        if (changed.bookmarkId === this.connectedBookmarkId) {
          if (changed.container !== this.containerName) {
            await this.loadContainers();
          } else if (changed.deletesContainer && job.state === "complete") {
            // The container is gone: re-listing it would only fail. A job
            // that ended otherwise may have left it, so that case re-reads.
            await this.#leaveContainer();
          } else {
            await this.refreshView();
          }
        }
        return;
      } else {
        misses = 0;
      }
      setTimeout(() => void tick(), jobPollMs);
    };
    setTimeout(() => void tick(), jobPollMs);
  }


  async startBenchmark(confirm: string): Promise<string> {
    const id = await this.#api.startBenchmark({
      bookmarkId: this.connectedBookmarkId ?? "",
      confirm,
    });
    await this.jobs.load();
    return id;
  }


  /** Active (non-terminal) jobs — the drawer badge. */
  get activeJobs(): JobDTO[] {
    return this.jobs.list.filter((j) => !terminalJobStates.includes(j.state));
  }

  async planCopy(req: CopyRequestDTO): Promise<CopyPlanDTO> {
    return await this.#api.planCopy(req);
  }

  async startCopy(req: CopyRequestDTO): Promise<string> {
    const id = await this.#api.startCopy(req);
    this.#watchJob(id, { bookmarkId: req.destBookmarkId, container: req.destContainer });
    await this.jobs.load();
    return id;
  }

  async startMirror(req: MirrorRequestDTO): Promise<string> {
    const id = await this.#api.startMirror(req);
    await this.jobs.load();
    return id;
  }

  async cancelJob(jobId: string) {
    await this.#api.cancelJob(jobId);
    await this.jobs.load();
  }

  async pauseJob(jobId: string) {
    await this.#api.pauseJob(jobId);
    await this.jobs.load();
  }

  async resumeJob(jobId: string) {
    await this.#api.resumeJob(jobId);
    await this.jobs.load();
  }

  async rerunJob(jobId: string): Promise<string> {
    const id = await this.#api.rerunJob(jobId);
    await this.jobs.load();
    return id;
  }

  async pickUploadFiles(scope: SelectionScopeDTO): Promise<PickedFilesDTO | null> {
    return await this.#api.pickUploadFiles(scope);
  }

  async pickUploadDirectory(scope: SelectionScopeDTO): Promise<PickedFilesDTO | null> {
    return await this.#api.pickUploadDirectory(scope);
  }

  async pickDownloadDir(scope: SelectionScopeDTO): Promise<PickedDirDTO | null> {
    return await this.#api.pickDownloadDir(scope);
  }

  async preflightUpload(req: UploadRequestDTO): Promise<UploadPreflight> {
    return await this.#api.preflightUpload(req);
  }

  async startUpload(req: UploadRequestDTO): Promise<string> {
    const id = await this.#api.startUpload(req);
    this.#watchJob(id, { bookmarkId: req.bookmarkId, container: req.container });
    await this.jobs.load();
    return id;
  }

  async preflightDownload(req: DownloadRequestDTO): Promise<DownloadPreflightView> {
    return await this.#api.preflightDownload(req);
  }

  async startDownload(req: DownloadRequestDTO): Promise<string> {
    const id = await this.#api.startDownload(req);
    await this.jobs.load();
    return id;
  }

  async startFind(query: string) {
    if (this.containerName === null || this.connecting || !this.connectedBookmarkId || query.trim() === "") {
      return;
    }
    const cancellation = this.cancelFind();
    const request: FindRequest = {
      scope: this.#scopeRevision, seq: ++this.#findSeq, bookmark: this.connectedBookmarkId, portrait: this.#portrait(),
      container: this.containerName, prefix: this.prefix, started: false, id: null,
    };
    this.#findRequest = request;
    this.#findResults = request;
    this.findStatus = null;
    this.findNotice = null;
    this.findRunning = true;
    try {
      await cancellation;
      if (!this.#currentFind(request)) return;
      request.started = true;
      request.id = await this.#api.startFind(request.container, request.prefix, query, request.bookmark);
      if (!this.#currentFind(request)) {
        await this.#cancelFindRequest(request);
        return;
      }
      while (this.#currentFind(request)) {
        const status = await this.#api.findStatus(request.id, request.bookmark);
        if (!this.#currentFind(request)) return;
        this.#requireReaderScope(status.scope, request.bookmark, request.portrait, "scan");
        this.findStatus = status;
        if (status.done) {
          break;
        }
        await new Promise((resolve) => setTimeout(resolve, 400));
      }
    } catch (error) {
      if (this.#currentFind(request)) {
        this.findStatus = {
          scanned: this.findStatus?.scanned ?? 0,
          matches: this.findStatus?.matches ?? [],
          truncated: this.findStatus?.truncated ?? false,
          done: true,
          error: messageOf(error),
          objectsPerSecond: 0,
        };
      } else if (request.id === null && this.#currentFindResults(request)) {
        this.findNotice = "Scan startup did not return an id; cancellation could not be confirmed.";
      }
    } finally {
      if (this.#currentFind(request)) this.findRunning = false;
    }
  }

  async cancelFind() {
    this.#findSeq++;
    this.findRunning = false;
    const request = this.#findRequest;
    this.#findRequest = null;
    if (request === null) return;
    this.findNotice = !request.started ? "Scan start cancelled."
      : request.id === null ? "Stop requested; waiting for scan startup." : "Requesting cancellation…";
    await this.#cancelFindRequest(request);
  }

  /** The count shown for the open prefix, or null when none was taken in
   * this scope; a navigation leaves an old count behind rather than
   * showing it against another prefix. */
  get prefixCount(): FindStatusDTO | null {
    // Read the reactive status first: a getter that returns before touching
    // any state registers no dependency, and the view would never learn
    // that a count arrived.
    const status = this.countStatus;
    const request = this.#countRequest;
    return status !== null && request !== null && this.#currentFindScope(request) ? status : null;
  }

  /** Count the objects under the open prefix: a count-only scan that
   * streams the listing, keeps no name, and can be stopped (ADR 0072). */
  async startCount() {
    if (this.containerName === null || this.connecting || !this.connectedBookmarkId) return;
    await this.cancelCount();
    const request: FindRequest = {
      scope: this.#scopeRevision, seq: ++this.#countSeq, bookmark: this.connectedBookmarkId, portrait: this.#portrait(),
      container: this.containerName, prefix: this.prefix, started: false, id: null,
    };
    this.#countRequest = request;
    this.countStatus = null;
    this.countedAt = null;
    this.countRunning = true;
    const current = () => request === this.#countRequest && request.seq === this.#countSeq && this.#currentFindScope(request);
    try {
      request.started = true;
      request.id = await this.#api.startFind(request.container, request.prefix, "", request.bookmark, true);
      if (!current()) {
        await this.#api.cancelFind(request.id, request.bookmark).catch(() => undefined);
        return;
      }
      while (current()) {
        const status = await this.#api.findStatus(request.id, request.bookmark);
        if (!current()) return;
        this.#requireReaderScope(status.scope, request.bookmark, request.portrait, "count");
        this.countStatus = status;
        if (status.done) {
          this.countedAt = new Date().toISOString();
          break;
        }
        await new Promise((resolve) => setTimeout(resolve, 400));
      }
    } catch (error) {
      if (current()) {
        this.countStatus = { scanned: this.countStatus?.scanned ?? 0, matches: [], truncated: false, done: true,
          error: messageOf(error), objectsPerSecond: 0, scope: this.countStatus?.scope ?? null };
      }
    } finally {
      if (current()) this.countRunning = false;
    }
  }

  async cancelCount() {
    this.#countSeq++;
    this.countRunning = false;
    const request = this.#countRequest;
    if (request === null || request.id === null) return;
    await this.#api.cancelFind(request.id, request.bookmark).catch(() => undefined);
  }

  #currentFindScope(request: FindRequest): boolean {
    return !this.connecting && request.scope === this.#scopeRevision
      && request.bookmark === this.connectedBookmarkId && request.container === this.containerName && request.prefix === this.prefix;
  }

  #currentFind(request: FindRequest): boolean {
    return request === this.#findRequest && request.seq === this.#findSeq && this.#currentFindScope(request);
  }

  #currentFindResults(request: FindRequest): boolean {
    return request === this.#findResults && this.#currentFindScope(request);
  }

  async #cancelFindRequest(request: FindRequest) {
    if (request.id === null) return;
    try {
      await this.#api.cancelFind(request.id, request.bookmark);
      if (this.#currentFindResults(request)) this.findNotice = "Cancellation requested.";
    } catch (error) {
      if (this.#currentFindResults(request)) this.findNotice = "Could not confirm that the scan stopped: " + messageOf(error);
    }
  }

  #clearFind() {
    void this.cancelFind();
    this.#findResults = null;
    this.findStatus = null;
    this.findNotice = null;
  }

  closeFind(panelRevision = this.#findPanelRevision) {
    if (panelRevision !== this.#findPanelRevision) return;
    this.#findPanelRevision++;
    this.#findOpen = false;
    this.#clearFind();
  }

  async openFindMatch(name: string, ownsPanel = () => true): Promise<boolean> {
    const origin = this.#findResults;
    const match = this.findStatus?.matches?.find((row) => row.name === name);
    if (!origin || !match || !this.#currentFindResults(origin) || !ownsPanel()) return false;
    const selected = { ...match };
    const selection = this.#detailSeq;
    const cancellation = this.cancelFind();
    const stopping = this.#findSeq;
    await cancellation;
    if (!this.#currentFindResults(origin) || !ownsPanel() || stopping !== this.#findSeq || selection !== this.#detailSeq) return false;
    const listing = this.goTo(parentPrefix(selected.name));
    const scope = this.#scopeRevision;
    const list = this.#listSeq;
    const intent = this.#findSeq;
    const detail = this.#detailSeq;
    await listing;
    if (!ownsPanel() || this.connecting || scope !== this.#scopeRevision || list !== this.#listSeq
      || intent !== this.#findSeq || detail !== this.#detailSeq) return false;
    if (this.entriesError) throw new Error(this.entriesError);
    let index = this.entries.findIndex((row) => row.kind === "object" && row.name === selected.name);
    if (index < 0) {
      // A delimiter page can omit this exact object or fold it into a prefix.
      // The match is advisory; normal selection still performs its own HEAD.
      index = this.entries.length;
      this.entries = [...this.entries, { kind: "object", name: selected.name, bytes: selected.bytes,
        etag: "", lastModified: "", contentType: "", sloListed: false }];
    }
    this.filter = "";
    this.select(index);
    return true;
  }

  async previewDelete(req: DeleteRequestDTO): Promise<DeletePreviewDTO> {
    return await this.#api.previewDelete(req);
  }

  async startDelete(req: DeleteRequestDTO): Promise<string> {
    const id = await this.#api.startDelete(req);
    this.#watchJob(id, { bookmarkId: req.bookmarkId, container: req.container, deletesContainer: req.mode === "delete_container" });
    await this.jobs.load();
    return id;
  }

  async checkSegmentOwnership(req: SegmentsDeleteRequestDTO): Promise<SegmentOwnershipDTO> {
    return await this.#api.checkSegmentOwnership(req);
  }

  // Deleting a manifest with its segments is a job now (ADR 0095 decision 8):
  // it starts, is watched, and refreshes the view when it settles, like the
  // selected-set deletion. The reviewed membership the dialog carries back is
  // on the request.
  async deleteManifestWithSegments(req: SegmentsDeleteRequestDTO): Promise<string> {
    const id = await this.#api.deleteManifestWithSegments(req);
    this.#watchJob(id, { bookmarkId: req.bookmarkId, container: req.container });
    await this.jobs.load();
    await this.audit.loadRecent();
    return id;
  }

  // A reopened dialog can share this identity, so dialog callers also supply
  // their lifetime check before follow-up navigation.
  #currentView(scope: number, selection: typeof this.detailIdentity): boolean {
    return !this.connecting && scope === this.#scopeRevision && selection === this.detailIdentity;
  }


  /** Save an object metadata working copy. force skips the fingerprint
   * check — the deliberate "apply mine anyway" after a shown conflict. */
  async saveObjectMetadata(edit: ObjectMetadataEditDTO, force = false): Promise<MutationResultDTO> {
    const identity = this.detailIdentity;
    const detail = this.objectDetail;
    if (!identity || !detail || detail.name !== identity.name || identity.container !== this.containerName
      || this.#detailScope !== this.#scopeRevision || this.connecting || !this.connectedBookmarkId) {
      throw new Error("no current object detail selected — reload before editing");
    }
    const request = { scope: this.#scopeRevision, seq: ++this.#detailSeq, ...identity };
    this.detailLoading = false;
    this.detailError = null;
    const base = force ? "" : detail.editableFingerprint;
    this.mutationNotice = null;
    // The write names the scope the detail was read under (ADR 0079): a
    // connection that moved since is refused before anything is written.
    const result = await this.#api.updateObjectMetadata(detail.scope, identity.container, identity.name, base, edit);
    if (this.#currentDetail(request) && result.outcome === "applied" && result.objectAfter
      && result.objectAfter.name === identity.name) {
      this.objectDetail = result.objectAfter;
    }
    // A save that wrote carries its limit whatever the outcome; a refused
    // one wrote nothing and has nothing to qualify.
    if (this.#currentDetail(request) && result.limitation) {
      this.mutationNotice = result.limitation;
    }
    await this.audit.loadRecent();
    return result;
  }

  /** True while the connection and the account-request sequence still name
   * the operation that started: a later save or reload supersedes an
   * in-flight one, so its stale reply cannot replace the current
   * working-copy baseline (the sequence check the container reader uses).
   * The account belongs to the connection, not to the container or prefix
   * being browsed, so moving between those while a write is pending does
   * not drop its verified after-state. */
  #currentAccount(connection: number, seq: number): boolean {
    return !this.connecting && connection === this.#connectionRevision && seq === this.#accountSeq;
  }

  /** Save an account metadata and quota edit for the connected account.
   * The write names the account's own scope (ADR 0079) and hands back the
   * fingerprint the portrait was read under, so a change since the read is
   * refused as a conflict rather than overwritten (§18). It takes the
   * account-request sequence, so a reload still in flight cannot overwrite
   * the verified after-state once it resolves, and adopts the after-state
   * only when it names this connection's generation. */
  async saveAccountMetadata(edit: AccountEditDTO, force = false): Promise<MutationResultDTO> {
    const account = this.account;
    const bookmarkId = this.connectedBookmarkId;
    if (!account || this.connecting || !bookmarkId) {
      throw new Error("no connected account — reconnect before editing");
    }
    const connection = this.#connectionRevision;
    const seq = ++this.#accountSeq;
    const portrait = this.#portrait();
    const base = force ? "" : account.editableFingerprint;
    const result = await this.#api.updateAccountMetadata(account.scope, base, edit);
    this.#adoptAccountAfter(result, connection, seq, bookmarkId, portrait);
    await this.audit.loadRecent();
    return result;
  }

  /** Adopt a verified account after-state while it still names this
   * connection's generation and nothing newer has started since. */
  #adoptAccountAfter(result: MutationResultDTO, connection: number, seq: number, bookmarkId: string, portrait: ListingScope | null) {
    if (this.#currentAccount(connection, seq) && result.outcome === "applied" && result.accountAfter
      && this.#readerScopeMatches(result.accountAfter.scope, bookmarkId, portrait)) {
      this.account = result.accountAfter;
      this.accountError = null;
    }
  }

  /** Adopt a verified container after-state while that container is still
   * the open one and nothing newer has started since; its settings fact is
   * folded into the loaded row when it belongs to this connection. */
  #adoptContainerAfter(result: MutationResultDTO, scope: number, seq: number, name: string) {
    if (this.#currentContainerDetail(scope, seq, name) && result.outcome === "applied" && result.containerAfter
      && result.containerAfter.name === name) {
      this.containerDetail = result.containerAfter;
      if (this.#factCurrent(result.containerAfter)) this.#applySettings(name, result.containerAfter.settings);
    }
  }

  /** Set or clear one TempURL key slot of the connected account (an empty
   * container) or of the open container (§22, ADR 0101). The request carries
   * the slot presence the view showed as its conflict basis, and the verified
   * after-state is adopted the way the account and container saves adopt
   * theirs. The secret passes through to the bridge and is kept nowhere. */
  async updateTempUrlKey(edit: TempURLKeyEditDTO): Promise<MutationResultDTO> {
    const bookmarkId = this.connectedBookmarkId;
    if (!bookmarkId || this.connecting) {
      throw new Error("no connected account — reconnect before changing a key");
    }
    if (edit.container === "") {
      const account = this.account;
      if (!account) throw new Error("no connected account — reconnect before changing a key");
      const connection = this.#connectionRevision;
      const seq = ++this.#accountSeq;
      const portrait = this.#portrait();
      const result = await this.#api.updateTempUrlKey(account.scope, edit);
      this.#adoptAccountAfter(result, connection, seq, bookmarkId, portrait);
      await this.audit.loadRecent();
      return result;
    }
    const detail = this.containerDetail;
    if (!detail || detail.name !== edit.container || this.containerName !== edit.container
      || this.#containerDetailConnection !== this.#connectionRevision) {
      throw new Error("no current container detail open — reload before changing a key");
    }
    const scope = this.#scopeRevision;
    const seq = ++this.#containerDetailSeq;
    const result = await this.#api.updateTempUrlKey(detail.scope, edit);
    this.#adoptContainerAfter(result, scope, seq, edit.container);
    await this.audit.loadRecent();
    return result;
  }

  /** Re-read the connected account's portrait to refresh the working copy
   * after a conflict. The reply must name the same connection generation
   * the read started under — a reconnect moved through the bridge elsewhere
   * is reported, not adopted beside the older capabilities and listings —
   * and a save or newer reload that started since supersedes it. Returns
   * whether the portrait was refreshed; a failure is kept in accountError
   * for the editor to show. */
  async refreshAccount(): Promise<boolean> {
    const bookmarkId = this.connectedBookmarkId;
    if (!bookmarkId || this.connecting) return false;
    const connection = this.#connectionRevision;
    const seq = ++this.#accountSeq;
    const portrait = this.#portrait();
    let failure: string | null = null;
    const fresh = await this.#api.account(bookmarkId).catch((err) => {
      failure = messageOf(err);
      return null;
    });
    if (!this.#currentAccount(connection, seq)) return false;
    if (fresh && this.#readerScopeMatches(fresh.scope, bookmarkId, portrait)) {
      this.account = fresh;
      this.accountError = null;
      return true;
    }
    this.accountError = failure ?? "the account portrait does not match this connection and was not shown; reconnect and try again";
    return false;
  }

  /** Re-fetch the selected row's detail (after a conflict, say). */
  refreshDetail() {
    this.select(this.selectedIndex);
  }

  /** Save a container edit for the open container. */
  async saveContainer(edit: ContainerEditDTO, force = false): Promise<MutationResultDTO> {
    const name = this.containerName;
    const detail = this.containerDetail;
    if (!name || !detail || detail.name !== name || this.connecting || !this.connectedBookmarkId
      || this.#containerDetailConnection !== this.#connectionRevision) {
      throw new Error("no current container detail open — reload before editing");
    }
    const scope = this.#scopeRevision;
    const seq = ++this.#containerDetailSeq;
    const base = force ? "" : detail.editableFingerprint;
    const result = await this.#api.updateContainer(detail.scope, name, base, edit);
    this.#adoptContainerAfter(result, scope, seq, name);
    await this.audit.loadRecent();
    return result;
  }

  /** Re-read the open container's detail. Returns whether it was refreshed,
   * as refreshAccount does: false for a read that failed, did not match
   * this connection, or was superseded before it answered. */
  async refreshContainerDetail(): Promise<boolean> {
    const name = this.containerName;
    const bookmarkId = this.connectedBookmarkId;
    if (!name || !bookmarkId || this.connecting) {
      return false;
    }
    const scope = this.#scopeRevision;
    const seq = ++this.#containerDetailSeq;
    const connection = this.#connectionRevision;
    const portrait = this.#portrait();
    let failure: string | null = null;
    const detail = await this.#api.containerDetail(name, bookmarkId).catch((err) => {
      failure = messageOf(err);
      return null;
    });
    if (this.#currentContainerDetail(scope, seq, name)) {
      const accepted = detail?.name === name && this.#readerScopeMatches(detail.scope, bookmarkId, portrait) ? detail : null;
      this.containerDetail = accepted;
      this.containerDetailError = accepted ? null : (failure ?? "the container detail does not match this connection and was not shown; reconnect and try again");
      // The banner and the badge take only a fact of this connection's
      // generation; a detail answered under another one is shown as the
      // inspector's numbers but not as a settings fact.
      if (accepted && this.#factCurrent(accepted)) this.#applySettings(name, accepted.settings);
      else if (accepted) this.containerDetailError = "the settings fact belongs to another connection generation and was not shown";
      // A prefix change does not change this container's working-copy identity.
      this.#containerDetailConnection = connection;
      return this.containerDetailError === null;
    }
    return false;
  }

  /** The open container's settings fact, only when it belongs to this
   * connection generation; the banner and the inspector sentence read this,
   * never the detail's settings directly. */
  get openContainerSettings(): ContainerSettingsDTO | null {
    const detail = this.containerDetail;
    return detail && this.#factCurrent(detail) ? detail.settings : null;
  }

  /** A settings fact is shown only under the connection generation it was
   * observed in, as the backend store already keeps it (ADR 0071 decision
   * 3): the reply's scope must be this account's current generation and the
   * fact must carry that same generation. */
  #factCurrent(detail: ContainerDetailDTO): boolean {
    const generation = this.account?.scope.generation;
    return generation !== undefined && detail.scope.generation === generation && detail.settings.generation === generation;
  }

  /** Fold one HEAD's facts into the loaded row for that container, so a
   * badge appears the moment the container has been selected, checked or
   * edited (ADR 0071 decision 2). */
  #applySettings(name: string, settings: ContainerSettingsDTO) {
    const index = this.containers.findIndex((row) => row.name === name);
    if (index >= 0) {
      this.containers[index] = { ...this.containers[index], settings };
    }
    if (name in this.settingsErrors) {
      const { [name]: _dropped, ...rest } = this.settingsErrors;
      this.settingsErrors = rest;
    }
  }

  /** HEAD every loaded container once, in listing order with at most four
   * in flight, through the same read selection makes; stops on request
   * or when the scope changes (ADR 0071 decision 5). */
  async checkSettings() {
    const bookmarkId = this.connectedBookmarkId;
    if (!bookmarkId || this.connecting || this.settingsCheck?.running) return;
    const names = this.containers.map((row) => row.name);
    const scope = this.#scopeRevision;
    const connection = this.#connectionRevision;
    const seq = ++this.#settingsCheckSeq;
    const check = { running: true, checked: 0, total: names.length, stopped: false };
    this.settingsCheck = { ...check };
    // Identity is the sequence number, never the reactive object: a stop or a
    // newer check bumps it, and so does nothing else.
    const live = () => seq === this.#settingsCheckSeq
      && connection === this.#connectionRevision && this.connectedBookmarkId === bookmarkId;
    let next = 0;
    const worker = async () => {
      while (live() && next < names.length) {
        const name = names[next++];
        try {
          const detail = await this.#api.containerDetail(name, bookmarkId);
          if (!live()) return;
          if (detail.name === name && this.#answeredBy(detail.scope, bookmarkId) && this.#factCurrent(detail)) {
            this.#applySettings(name, detail.settings);
          } else {
            this.settingsErrors = { ...this.settingsErrors, [name]: "the reply did not match this container and connection generation" };
          }
        } catch (err) {
          if (!live()) return;
          this.settingsErrors = { ...this.settingsErrors, [name]: messageOf(err) };
        }
        check.checked++;
        this.settingsCheck = { ...check };
      }
    };
    await Promise.all([worker(), worker(), worker(), worker()]);
    if (seq === this.#settingsCheckSeq) {
      check.running = false;
      if (scope !== this.#scopeRevision && check.checked < check.total) check.stopped = true;
      this.settingsCheck = { ...check };
    }
  }

  stopSettingsCheck() {
    const check = this.settingsCheck;
    if (check?.running) {
      this.settingsCheck = { ...check, running: false, stopped: true };
      this.#settingsCheckSeq++;
    }
  }

  /** The portrait the connected view was built under, for a write that
   * names its scope (ADR 0079); null when nothing is connected. */
  get portrait(): ListingScope | null {
    return this.#portrait();
  }

  /** Create a container and open it. The portrait is the one captured
   * when the dialog opened (ADR 0079). */
  async createContainer(create: ContainerCreateDTO, portrait: ListingScope | null, ownsView = () => true) {
    const scope = this.#scopeRevision;
    const selection = this.detailIdentity;
    if (!portrait) throw new Error("The connection changed since the dialog opened. Reconnect and try again.");
    await this.#api.createContainer(portrait, create);
    await this.audit.loadRecent();
    if (!ownsView() || !this.#currentView(scope, selection)) return;
    await this.loadContainers();
    if (!ownsView() || !this.#currentView(scope, selection)) return;
    await this.openContainer(create.name);
  }

  /** Create the explicit marker for the selected implied prefix. */
  async createMarker(prefix: string) {
    const container = this.containerName;
    const identity = this.detailIdentity;
    if (!container || !identity || identity.container !== container || identity.name !== prefix
      || this.prefixDetail?.prefix !== prefix || this.#detailScope !== this.#scopeRevision
      || this.connecting || !this.connectedBookmarkId) {
      throw new Error("no current prefix detail selected — reload before creating a marker");
    }
    const request = { scope: this.#scopeRevision, seq: ++this.#detailSeq, container, name: prefix };
    this.detailLoading = false;
    this.detailError = null;
    const detail = await this.#api.createMarker(this.prefixDetail.scope, container, prefix);
    if (this.#currentDetail(request) && detail.prefix === prefix) this.prefixDetail = detail;
    await this.audit.loadRecent();
  }

  /** Jump backstage to a manifest's segment location (§23.1): browse
   * the segment container under its prefix, with a banner naming the
   * manifest we came from. */
  async openBackstage(container: string, prefix: string, fromLabel: string) {
    if (this.connecting || !this.connectedBookmarkId) return;
    const opening = this.openContainer(container);
    let scope = this.#scopeRevision;
    await opening;
    if (scope !== this.#scopeRevision) return;
    if (prefix !== "") {
      const loading = this.goTo(prefix);
      scope = this.#scopeRevision;
      await loading;
      if (scope !== this.#scopeRevision) return;
    }
    this.backstageFrom = fromLabel;
  }

  /** Delete ONLY the named manifest object (§60 Phase 4's low-risk
   * delete): segments survive; the working list refreshes. The dialog
   * passes the exact identity it displayed — never the live index.
   *
   * baseContent extends that principle from the name to what the name held:
   * it is the ContentFingerprint of the detail this view showed, and the
   * service refuses the delete when the object no longer matches it
   * (ADR 0085). It narrows the window between being shown an object and
   * destroying it; it does not close it, and nothing here may be presented as
   * proof that no one else is writing. */
  async deleteManifest(container: string, name: string, detail: ObjectDetailDTO, ownsView = () => true): Promise<MutationResultDTO> {
    if (!container || !name) {
      throw new Error("no object identity");
    }
    if (!detail.contentFingerprint) {
      throw new Error("no content identity for the object being deleted");
    }
    const scope = this.#scopeRevision;
    const selection = this.detailIdentity;
    const prefix = this.prefix;
    const displayed = container === selection?.container && name === selection.name;
    const result = await this.#api.deleteManifest(detail.scope, container, name, detail.contentFingerprint);
    await this.audit.loadRecent();
    if (displayed && ownsView() && this.#currentView(scope, selection)) await this.goTo(prefix);
    return result;
  }

  /** Delete ONE plain object the operator was shown (ADR 0092). The request
   * names the scope the detail was read under (ADR 0079) and carries that
   * detail's content identity; the listing refreshes afterwards, and the
   * caller shows any successor the result reports. */
  async deleteObject(container: string, name: string, detail: ObjectDetailDTO, ownsView = () => true): Promise<MutationResultDTO> {
    if (!container || !name) {
      throw new Error("no object identity");
    }
    if (!detail.contentFingerprint) {
      throw new Error("no content identity for the object being deleted");
    }
    const scope = this.#scopeRevision;
    const selection = this.detailIdentity;
    const prefix = this.prefix;
    const displayed = container === selection?.container && name === selection.name;
    const result = await this.#api.deleteObject(detail.scope, container, name, detail.contentFingerprint);
    await this.audit.loadRecent();
    if (displayed && ownsView() && this.#currentView(scope, selection)) await this.goTo(prefix);
    return result;
  }

  /** Open the single-object delete for the inspected plain object, capturing
   * the detail, the container and the view it was chosen from. False when
   * nothing deletable is inspected. */
  openObjectDelete(): boolean {
    const detail = this.objectDetail;
    const identity = this.detailIdentity;
    const container = this.containerName;
    if (!detail || !identity || !container || this.connecting || identity.container !== container
      || identity.name !== detail.name || !isLiteralPlainObject(detail)) {
      return false;
    }
    this.objectDelete = {
      kind: "object", container, name: detail.name, detail, scope: this.#scopeRevision, selection: identity,
      status: "pending", result: null, error: null,
    };
    return true;
  }

  /** Open the marker delete for the inspected marker-backed prefix (ADR 0094
   * decision 2), capturing the prefix detail's own object as the identity.
   * False when no marker is inspected. */
  openMarkerDelete(): boolean {
    const pd = this.prefixDetail;
    const identity = this.detailIdentity;
    const container = this.containerName;
    if (!pd?.selfObject || !pd.markerBacked || !identity || !container || this.connecting
      || identity.container !== container || identity.name !== pd.prefix || this.#detailScope !== this.#scopeRevision
      || isLink(pd.selfObject)) {
      return false;
    }
    this.objectDelete = {
      kind: "marker", container, name: pd.prefix, detail: pd.selfObject, scope: this.#scopeRevision, selection: identity,
      status: "pending", result: null, error: null,
    };
    return true;
  }

  /** Open the manifest-only delete for the inspected manifest (ADR 0094
   * decision 3). False when the inspected object is not a manifest or is a
   * link in either representation. */
  openManifestDelete(): boolean {
    const detail = this.objectDetail;
    const identity = this.detailIdentity;
    const container = this.containerName;
    if (!detail || !identity || !container || this.connecting || identity.container !== container
      || identity.name !== detail.name || detail.class === "plain" || isLink(detail)) {
      return false;
    }
    this.objectDelete = {
      kind: "manifest", container, name: detail.name, detail, scope: this.#scopeRevision, selection: identity,
      status: "pending", result: null, error: null,
    };
    return true;
  }

  /** The deletion the Delete key would open for the inspected row (ADR 0094
   * decision 5): its own kind, or null for a link, an implied prefix, a real
   * object at a prefix name, or nothing inspected. */
  deletableSelectionKind(): ShownDeleteKind | null {
    if (this.#chosen.size > 0) return null;
    const identity = this.detailIdentity;
    const detail = this.objectDetail;
    if (detail && identity?.name === detail.name) {
      if (isLink(detail)) return null;
      if (detail.class !== "plain") return "manifest";
      if (isLiteralPlainObject(detail)) return "object";
      return null;
    }
    const pd = this.prefixDetail;
    if (pd?.markerBacked && pd.selfObject && !isLink(pd.selfObject) && identity?.name === pd.prefix) return "marker";
    return null;
  }

  closeObjectDelete() {
    this.objectDelete = null;
  }

  /** True while the view the operation was opened from is still the one shown:
   * a user who moves the selection or the scope before confirming abandons it;
   * the refresh that follows a success is this state's own and does not. */
  objectDeleteCurrent(): boolean {
    const op = this.objectDelete;
    if (!op) return false;
    const shown = op.kind === "marker" ? this.prefixDetail?.selfObject : this.objectDetail;
    return !this.connecting && this.#scopeRevision === op.scope
      && this.detailIdentity === op.selection && shown === op.detail;
  }

  /** Run the opened delete once. The result or the refusal lands on the
   * operation, which survives the listing refresh, so the dialog can show an
   * observed successor rather than a plain success. */
  async runObjectDelete(): Promise<void> {
    const op = this.objectDelete;
    if (!op || op.status !== "pending" || !this.objectDeleteCurrent()) return;
    op.status = "running";
    try {
      const owns = () => this.objectDelete === op;
      const result = op.kind === "object"
        ? await this.deleteObject(op.container, op.name, op.detail, owns)
        : op.kind === "marker"
          ? await this.deleteMarker(op.name, owns)
          : await this.deleteManifest(op.container, op.name, op.detail, owns);
      if (this.objectDelete !== op) return;
      op.result = result;
      op.status = "done";
    } catch (err) {
      if (this.objectDelete !== op) return;
      op.error = err instanceof Error ? err.message : String(err);
      op.status = "pending";
    }
  }

  /** Open the selected-set deletion (ADR 0093 decision 5) around a snapshot
   * of what is selected now: the scope the rows were listed under, the
   * container, the object rows with the facts they were listed with, unique
   * by name, and the prefix rows left out. False when fewer than two rows
   * are selected or none of them is an object. */
  openSelectedDelete(): boolean {
    const container = this.containerName;
    const listingScope = this.#entriesScope;
    const rows = this.chosenRows;
    if (!container || !listingScope || this.connecting || rows.length < 2) return false;
    const members: ListingRowDTO[] = [];
    const seen = new Set<string>();
    const excludedPrefixes: string[] = [];
    for (const row of rows) {
      if (row.kind !== "object") {
        excludedPrefixes.push(row.name);
      } else if (!seen.has(row.name)) {
        seen.add(row.name);
        members.push({ ...row });
      }
    }
    if (members.length === 0) return false;
    this.selectedDelete = {
      container, listingScope: { ...listingScope }, scope: this.#scopeRevision, members, excludedPrefixes,
      ceiling: this.selectionCeiling, status: "pending", jobId: null, error: null,
    };
    return true;
  }

  closeSelectedDelete() {
    this.selectedDelete = null;
  }

  /** Why the open selected-set deletion no longer matches the view, or null
   * while it still does (ADR 0093 decision 5): the view moved, the
   * selection changed, a member left the listing, or a member's listed facts
   * differ from the snapshot. A count that still matches is never enough. */
  selectedDeleteProblem(): string | null {
    const op = this.selectedDelete;
    if (!op) return null;
    if (this.connecting || this.#scopeRevision !== op.scope || this.containerName !== op.container) {
      return "the view moved, so the selection it was made from is gone";
    }
    const live = new Map(this.chosenRows.map((row) => [rowIdentity(row), row]));
    if (live.size !== op.members.length + op.excludedPrefixes.length) return "the selection changed";
    for (const prefix of op.excludedPrefixes) {
      if (!live.has("prefix:" + prefix)) return "the selection changed";
    }
    for (const member of op.members) {
      const row = live.get("object:" + member.name);
      if (!row) return "the selection changed";
      if (!sameListingFacts(row, member)) return `${member.name} changed since it was listed`;
    }
    return null;
  }

  /** Start the opened selected-set deletion once, from the snapshot alone,
   * with the count the operator typed; the job id lands on the operation
   * and the selection it was made from is released. */
  async runSelectedDelete(confirm: string): Promise<void> {
    const op = this.selectedDelete;
    if (!op || op.status !== "pending" || this.selectedDeleteProblem() !== null) return;
    op.status = "running";
    try {
      const jobId = await this.startSelectedDelete({
        scope: { ...op.listingScope }, container: op.container, members: op.members.map((m) => ({ ...m })), confirm,
      });
      if (this.selectedDelete !== op) return;
      op.jobId = jobId;
      op.status = "done";
      this.clearSelection();
    } catch (err) {
      if (this.selectedDelete !== op) return;
      op.error = err instanceof Error ? err.message : String(err);
      op.status = "pending";
    }
  }

  async startSelectedDelete(req: SelectedDeleteRequestDTO): Promise<string> {
    const id = await this.#api.startSelectedDelete(req);
    this.#watchJob(id, { bookmarkId: req.scope.bookmarkId, container: req.container });
    await this.jobs.load();
    return id;
  }

  /** The open container's settings fact from its loaded row, or one HEAD
   * folded into that row when no row holds it yet; null when the container
   * cannot answer, so a caller says the mode is unknown rather than guesses. */
  async containerFacts(container: string): Promise<ContainerSettingsDTO | null> {
    const row = this.containers.find((r) => r.name === container);
    if (row?.settings) return row.settings;
    const bookmarkId = this.connectedBookmarkId;
    if (!bookmarkId) return null;
    try {
      const detail = await this.#api.containerDetail(container, bookmarkId);
      if (detail.name === container && this.#answeredBy(detail.scope, bookmarkId) && this.#factCurrent(detail)) {
        this.#applySettings(container, detail.settings);
        return detail.settings;
      }
    } catch {
      // The dialog states that the mode is unknown; nothing else depends on it.
    }
    return null;
  }

  /** Delete ONLY the marker object the current prefix detail showed (ADR
   * 0094): the request names the scope that detail was read under and the
   * identity of its own object, the marker rule is required beside it, and
   * the working list refreshes. */
  async deleteMarker(prefix: string, ownsView = () => true): Promise<MutationResultDTO> {
    const container = this.containerName;
    const identity = this.detailIdentity;
    const detail = this.prefixDetail;
    if (!container || !identity || identity.container !== container || identity.name !== prefix
      || detail?.prefix !== prefix || this.#detailScope !== this.#scopeRevision
      || this.connecting || !this.connectedBookmarkId) {
      throw new Error("no current prefix detail selected — reload before deleting a marker");
    }
    if (!detail.selfObject?.contentFingerprint) {
      throw new Error("no marker object was shown at this prefix");
    }
    const scope = this.#scopeRevision;
    const selection = this.detailIdentity;
    const parent = this.prefix;
    const result = await this.#api.deleteMarker(detail.scope, container, prefix, detail.selfObject.contentFingerprint);
    await this.audit.loadRecent();
    if (ownsView() && this.#currentView(scope, selection)) await this.goTo(parent);
    return result;
  }
}
