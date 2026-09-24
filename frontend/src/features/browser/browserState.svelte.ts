// Central UI state for live browsing (§65 commit 13): bookmarks →
// connect → containers → delimiter pages → selection-triggered HEAD.
// All data flows through the Api seam so this logic is tested against a
// fixture-backed fake; loading and error state live per pane.

import type {
  AccountDTO,
  AppSettingsDTO,
  CopyPlanDTO,
  CopyRequestDTO,
  DeletePreviewDTO,
  MirrorRequestDTO,
  DeleteRequestDTO,
  SegmentOwnershipDTO,
  SegmentsDeleteRequestDTO,
  DownloadPreflightView,
  DownloadRequestDTO,
  FindStatusDTO,
  JobDTO,
  UploadPreflight,
  UploadRequestDTO,
  Api,
  AuditEventDTO,
  BookmarkDTO,
  CredentialStatusDTO,
  RecoveryStatusDTO,
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
import { parentPrefix } from "../../lib/listing";

export type ContainerMode = "logical" | "all" | "backstage";

/** Bridge page size for both listing levels. */
const pageLimit = 1000;
/** The rows a column keeps before it stops loading pages, until the
 * operator's setting is read (ADR 0074 decision 1); mirrors
 * defaultBrowserRetentionCeiling in internal/app/settingsservice.go. */
const defaultBrowserRetentionCeiling = 100_000;

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

export class BrowserState {
  #api: Api;

  constructor(api: Api) {
    this.#api = api;
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
  #bookmarkChanges = new Map<string, number>();
  connectError: string | null = $state(null);
  account: AccountDTO | null = $state(null);
  /** Connect-time §10 capability snapshot; null until connected. */
  capabilities: CapabilitiesDTO | null = $state(null);
  /** Selected §50 query results; background Recent reads stay separate. */
  auditEvents: AuditEventDTO[] = $state([]);

  // Jobs drawer (§46/§47).
  jobs: JobDTO[] = $state([]);
  /** Bookmark filter for the drawer; "" = all accounts (§47 owner input). */
  jobsFilter = $state("");
  jobsError: string | null = $state(null);
  recoveryStatus = $state<RecoveryStatusDTO | null>(null);
  recoveryStatusLoading = $state(false);
  recoveryStatusError = $state(false);
  #recoveryStatusSeq = 0;

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
  // Application settings (§38/§40).
  appSettings: AppSettingsDTO | null = $state(null);
  appSettingsStatus = $state<"unavailable" | "current" | "stale" | "unconfirmed">("unavailable");
  appSettingsOperation = $state<"get" | "set" | null>(null);
  appSettingsReadError = $state(false);
  appSettingsAttempt = $state<{ value: number; state: "pending" | "acknowledged" | "unconfirmed" } | null>(null);
  get canSaveAppSettings(): boolean {
    return this.appSettingsOperation === null && this.appSettingsStatus === "current" && this.appSettings !== null;
  }
  // Per-job throughput (bytes/s) computed from successive poll deltas.
  jobRates: Record<string, number> = $state({});
  #lastPoll: Record<string, { bytes: number; at: number }> = {};

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
    if (selected === null) {
      return;
    }
    const idx = this.visibleEntries.findIndex((e) => e.name === selected.name && e.kind === selected.kind);
    if (idx === this.selectedIndex) {
      return;
    }
    if (idx >= 0) {
      // Same identity, new position: move the index WITHOUT re-running
      // selection side effects.
      this.selectedIndex = idx;
    } else {
      this.select(-1);
    }
  }
  entries: ListingRowDTO[] = $state([]);
  entriesMarker = $state("");
  entriesLoading = $state(false);
  entriesError: string | null = $state(null);
  selectedIndex = $state(-1);
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
    this.entriesLoading = false;
    this.entriesError = null;
    this.filter = "";
    this.select(-1);
    this.transferOpen = false;
    this.localTransfer = null;
    this.deleteOpen = false;
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
      await this.loadAudit();
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
  get retentionCeiling(): number {
    return this.appSettings?.browserRetentionCeiling ?? defaultBrowserRetentionCeiling;
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
    if (this.appSettings === null && rows >= this.retentionCeiling) void this.loadAppSettings();
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
      this.containerName = null;
      this.containerDetail = null;
      this.containerDetailError = null;
      this.entries = [];
      this.filter = "";
      this.select(-1);
      // Inspection may have taught the evidence engine (§24): returning
      // to the account view re-reads classifications.
      await this.loadContainers();
      return;
    }
    await this.#loadEntries(parentPrefix(this.prefix));
  }

  async #loadEntries(prefix: string) {
    this.#clearFind();
    const seq = ++this.#listSeq;
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

  /** Selection moves; the selected row triggers its authoritative HEAD
   * (§14.2) — object detail or prefix classification. */
  select(index: number) {
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

  async loadRecoveryStatus(): Promise<void> {
    const seq = ++this.#recoveryStatusSeq;
    this.recoveryStatusLoading = true;
    try {
      const status = await this.#api.recoveryStatus();
      if (seq === this.#recoveryStatusSeq) {
        this.recoveryStatus = status;
        this.recoveryStatusError = false;
      }
    } catch {
      if (seq === this.#recoveryStatusSeq) {
        this.recoveryStatus = null;
        this.recoveryStatusError = true;
      }
    } finally {
      if (seq === this.#recoveryStatusSeq) {
        this.recoveryStatusLoading = false;
      }
    }
  }

  #jobsSeq = 0;
  #jobsLoadedFilter: string | null = null;

  async loadJobs() {
    const seq = ++this.#jobsSeq;
    const filter = this.jobsFilter;
    if (filter !== this.#jobsLoadedFilter) {
      this.jobs = [];
      this.jobRates = {};
      this.jobsError = null;
      this.#jobsLoadedFilter = filter;
    }
    try {
      const jobs = await this.#api.jobs(filter, false, 50);
      if (seq !== this.#jobsSeq || filter !== this.jobsFilter) return;
      const now = Date.now();
      const rates: Record<string, number> = {};
      for (const job of jobs) {
        const moved = job.counters.bytesCopied;
        const last = this.#lastPoll[job.id];
        if (last && now > last.at && moved >= last.bytes) {
          rates[job.id] = ((moved - last.bytes) * 1000) / (now - last.at);
        }
        this.#lastPoll[job.id] = { bytes: moved, at: now };
      }
      this.jobs = jobs;
      this.jobRates = rates;
      this.jobsError = null;
    } catch (error) {
      if (seq === this.#jobsSeq && filter === this.jobsFilter) this.jobsError = messageOf(error);
    }
  }

  jobHistory(bookmarkId: string, cursor: string, limit: number) {
    return this.#api.jobHistory(bookmarkId, cursor, limit);
  }

  async job(jobId: string, bookmarkId: string) {
    const job = await this.#api.job(jobId, bookmarkId);
    if (job && (job.id !== jobId || !bookmarkId
      || (job.sourceBookmarkId !== bookmarkId && job.destBookmarkId !== bookmarkId))) {
      throw new Error("job does not match the requested account and identity");
    }
    return job;
  }

  jobItems(jobId: string, cursor: string, limit: number, bookmarkId: string) {
    return this.#api.jobItems(jobId, cursor, limit, bookmarkId);
  }

  jobItemTotals(jobId: string, bookmarkId: string) {
    return this.#api.jobItemTotals(jobId, bookmarkId);
  }

  async startBenchmark(confirm: string): Promise<string> {
    const id = await this.#api.startBenchmark({
      bookmarkId: this.connectedBookmarkId ?? "",
      confirm,
    });
    await this.loadJobs();
    return id;
  }

  async loadAppSettings() {
    // A late read must never replace a newer acknowledged settings write.
    if (this.appSettingsOperation !== null) return;
    this.appSettingsOperation = "get";
    this.appSettingsReadError = false;
    try {
      this.appSettings = await this.#api.appSettings();
      this.appSettingsStatus = "current";
    } catch {
      this.appSettingsReadError = true;
      if (this.appSettingsStatus !== "unconfirmed") {
        this.appSettingsStatus = this.appSettings ? "stale" : "unavailable";
      }
    } finally {
      this.appSettingsOperation = null;
    }
  }

  async saveAppSettings(settings: AppSettingsDTO) {
    if (!this.canSaveAppSettings) {
      throw new Error("Refresh settings before making another change.");
    }
    const request = { ...settings };
    this.appSettingsOperation = "set";
    this.appSettingsAttempt = { value: request.maxActiveJobs, state: "pending" };
    try {
      this.appSettings = await this.#api.saveAppSettings(request);
      this.appSettingsStatus = "current";
      this.appSettingsAttempt = { value: request.maxActiveJobs, state: "acknowledged" };
    } catch (error) {
      this.appSettingsStatus = "unconfirmed";
      this.appSettingsAttempt = { value: request.maxActiveJobs, state: "unconfirmed" };
      throw error;
    } finally {
      this.appSettingsOperation = null;
    }
  }

  /** Active (non-terminal) jobs — the drawer badge. */
  get activeJobs(): JobDTO[] {
    return this.jobs.filter(
      (j) => !["complete", "partial", "cancelled", "failed"].includes(j.state),
    );
  }

  async planCopy(req: CopyRequestDTO): Promise<CopyPlanDTO> {
    return await this.#api.planCopy(req);
  }

  async startCopy(req: CopyRequestDTO): Promise<string> {
    const id = await this.#api.startCopy(req);
    await this.loadJobs();
    return id;
  }

  async startMirror(req: MirrorRequestDTO): Promise<string> {
    const id = await this.#api.startMirror(req);
    await this.loadJobs();
    return id;
  }

  async cancelJob(jobId: string) {
    await this.#api.cancelJob(jobId);
    await this.loadJobs();
  }

  async rerunJob(jobId: string): Promise<string> {
    const id = await this.#api.rerunJob(jobId);
    await this.loadJobs();
    return id;
  }

  async pickUploadFiles(scope: SelectionScopeDTO): Promise<PickedFilesDTO | null> {
    return await this.#api.pickUploadFiles(scope);
  }

  async pickDownloadDir(scope: SelectionScopeDTO): Promise<PickedDirDTO | null> {
    return await this.#api.pickDownloadDir(scope);
  }

  async preflightUpload(req: UploadRequestDTO): Promise<UploadPreflight> {
    return await this.#api.preflightUpload(req);
  }

  async startUpload(req: UploadRequestDTO): Promise<string> {
    const id = await this.#api.startUpload(req);
    await this.loadJobs();
    return id;
  }

  async preflightDownload(req: DownloadRequestDTO): Promise<DownloadPreflightView> {
    return await this.#api.preflightDownload(req);
  }

  async startDownload(req: DownloadRequestDTO): Promise<string> {
    const id = await this.#api.startDownload(req);
    await this.loadJobs();
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
    await this.loadJobs();
    return id;
  }

  async checkSegmentOwnership(req: SegmentsDeleteRequestDTO): Promise<SegmentOwnershipDTO> {
    return await this.#api.checkSegmentOwnership(req);
  }

  async deleteManifestWithSegments(req: SegmentsDeleteRequestDTO, ownsView = () => true) {
    const scope = this.#scopeRevision;
    const selection = this.detailIdentity;
    const prefix = this.prefix;
    const displayed = req.bookmarkId === this.connectedBookmarkId && req.container === selection?.container && req.name === selection.name;
    const result = await this.#api.deleteManifestWithSegments(req);
    await this.loadAudit();
    if (displayed && result.manifestDeleted && ownsView() && this.#currentView(scope, selection)) await this.goTo(prefix);
    return result;
  }

  // A reopened dialog can share this identity, so dialog callers also supply
  // their lifetime check before follow-up navigation.
  #currentView(scope: number, selection: typeof this.detailIdentity): boolean {
    return !this.connecting && scope === this.#scopeRevision && selection === this.detailIdentity;
  }

  // §50 viewer filters + paging over the drawer's Changes tab.
  #auditFilterBookmark = $state("");
  get auditFilterBookmark(): string { return this.#auditFilterBookmark; }
  set auditFilterBookmark(value: string) {
    if (value !== this.#auditFilterBookmark) this.#clearAuditQuery();
    this.#auditFilterBookmark = value;
  }
  #auditFilterOp = $state("");
  get auditFilterOp(): string { return this.#auditFilterOp; }
  set auditFilterOp(value: string) {
    if (value !== this.#auditFilterOp) this.#clearAuditQuery();
    this.#auditFilterOp = value;
  }
  #auditFilterResult = $state("");
  get auditFilterResult(): string { return this.#auditFilterResult; }
  set auditFilterResult(value: string) {
    if (value !== this.#auditFilterResult) this.#clearAuditQuery();
    this.#auditFilterResult = value;
  }
  #auditSeq = 0;
  auditLoaded = $state(false);
  auditLoading = $state(false);
  auditMoreLoading = $state(false);
  auditError: string | null = $state(null);
  auditMoreError: string | null = $state(null);
  auditExhausted = $state(false);

  get auditCanMore(): boolean {
    const last = this.auditEvents.at(-1)?.id ?? 0;
    return this.auditLoaded && !this.auditLoading && !this.auditMoreLoading
      && this.auditError === null && !this.auditExhausted
      && Number.isSafeInteger(last) && last > 0;
  }

  #clearAuditQuery() {
    // Filter edits must retire replies even when values change back before
    // another search or a Svelte update can observe the intermediate value.
    this.#auditSeq++;
    this.auditEvents = [];
    this.auditLoaded = false;
    this.auditLoading = false;
    this.auditMoreLoading = false;
    this.auditError = null;
    this.auditMoreError = null;
    this.auditExhausted = false;
  }

  #requireAuditPage(page: AuditEventDTO[], beforeId = 0, bookmarkId = "") {
    if (page.length > 50) throw new Error("audit page exceeds its limit");
    let previous = beforeId || Infinity;
    for (const event of page) {
      if (bookmarkId !== "" && event.bookmarkId !== bookmarkId) {
        throw new Error("audit page names another bookmark");
      }
      if (!Number.isSafeInteger(event.id) || event.id <= 0 || event.id >= previous) {
        throw new Error("audit page IDs do not advance");
      }
      previous = event.id;
    }
  }

  async searchAudit(more = false) {
    if (more && !this.auditCanMore) return;
    const seq = ++this.#auditSeq;
    const bookmarkId = this.auditFilterBookmark;
    const operationPrefix = this.auditFilterOp;
    const result = this.auditFilterResult;
    const beforeId = more ? this.auditEvents[this.auditEvents.length - 1].id : 0;
    const ownsQuery = () => seq === this.#auditSeq
      && bookmarkId === this.auditFilterBookmark
      && operationPrefix === this.auditFilterOp && result === this.auditFilterResult;
    this.auditLoading = !more;
    this.auditMoreLoading = more;
    this.auditError = null;
    this.auditMoreError = null;
    try {
      const page = await this.#api.auditSearch({ operationPrefix, result, bookmarkId, beforeId, limit: 50 });
      if (!ownsQuery()) return;
      this.#requireAuditPage(page, beforeId, bookmarkId);
      this.auditEvents = more ? [...this.auditEvents, ...page] : page;
      this.auditLoaded = true;
      this.auditExhausted = page.length < 50;
    } catch {
      if (!ownsQuery()) return;
      if (more) this.auditMoreError = "More audit events are unavailable. Previously loaded results are shown.";
      else this.auditError = "Audit events are unavailable. Retry this search.";
    } finally {
      if (ownsQuery()) {
        this.auditLoading = false;
        this.auditMoreLoading = false;
      }
    }
  }

  recentAuditEvents: AuditEventDTO[] = $state([]);
  recentAuditLoaded = $state(false);
  recentAuditLoading = $state(false);
  recentAuditError = $state(false);
  #recentAuditSeq = 0;

  async loadAudit() {
    const seq = ++this.#recentAuditSeq;
    const bookmarkId = this.#connectingBookmarkId ?? this.connectedBookmarkId ?? "";
    this.recentAuditLoading = true;
    this.recentAuditError = false;
    try {
      const page = await this.#api.auditRecent(50, bookmarkId);
      if (seq !== this.#recentAuditSeq) return;
      this.#requireAuditPage(page);
      this.recentAuditEvents = page;
      this.recentAuditLoaded = true;
    } catch {
      // Mutations await this observation after their effects; a read failure
      // must not reject the already returned mutation or bypass its view guard.
      if (seq === this.#recentAuditSeq) this.recentAuditError = true;
    } finally {
      if (seq === this.#recentAuditSeq) this.recentAuditLoading = false;
    }
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
    await this.loadAudit();
    return result;
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
    if (this.#currentContainerDetail(scope, seq, name) && result.outcome === "applied" && result.containerAfter
      && result.containerAfter.name === name) {
      this.containerDetail = result.containerAfter;
      if (this.#factCurrent(result.containerAfter)) this.#applySettings(name, result.containerAfter.settings);
    }
    await this.loadAudit();
    return result;
  }

  async refreshContainerDetail() {
    const name = this.containerName;
    const bookmarkId = this.connectedBookmarkId;
    if (!name || !bookmarkId || this.connecting) {
      return;
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
    }
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
    await this.loadAudit();
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
    await this.loadAudit();
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
   * passes the exact identity it displayed — never the live index. */
  async deleteManifest(container: string, name: string, ownsView = () => true): Promise<void> {
    if (!container || !name) {
      throw new Error("no object identity");
    }
    const scope = this.#scopeRevision;
    const selection = this.detailIdentity;
    const prefix = this.prefix;
    const displayed = container === selection?.container && name === selection.name;
    await this.#api.deleteManifest(container, name);
    await this.loadAudit();
    if (displayed && ownsView() && this.#currentView(scope, selection)) await this.goTo(prefix);
  }

  /** Delete ONLY a verified marker; the working list refreshes. */
  async deleteMarker(prefix: string, ownsView = () => true) {
    const container = this.containerName;
    const identity = this.detailIdentity;
    if (!container || !identity || identity.container !== container || identity.name !== prefix
      || this.prefixDetail?.prefix !== prefix || this.#detailScope !== this.#scopeRevision
      || this.connecting || !this.connectedBookmarkId) {
      throw new Error("no current prefix detail selected — reload before deleting a marker");
    }
    const scope = this.#scopeRevision;
    const selection = this.detailIdentity;
    const parent = this.prefix;
    await this.#api.deleteMarker(container, prefix);
    await this.loadAudit();
    if (ownsView() && this.#currentView(scope, selection)) await this.goTo(parent);
  }
}

function messageOf(err: unknown): string {
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}
