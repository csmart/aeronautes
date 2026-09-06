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
  DownloadPreflight,
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
} from "../../lib/api";
import { parentPrefix } from "../../lib/listing";

export type ContainerMode = "logical" | "all" | "backstage";

/** Bridge page size for both listing levels. */
const pageLimit = 1000;

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
  // UI previews must notice a scope change even if its names are then restored.
  // This revision does not replace the backend's credential generation.
  #scopeRevision = $state(0);
  #connectionRevision = 0;
  get scopeRevision(): number { return this.#scopeRevision; }

  #connectedBookmarkId: string | null = $state(null);
  get connectedBookmarkId(): string | null { return this.#connectedBookmarkId; }
  set connectedBookmarkId(value: string | null) {
    if (value !== this.#connectedBookmarkId) {
      this.#scopeRevision++;
      this.#connectionRevision++;
    }
    this.#connectedBookmarkId = value;
  }
  connecting = $state(false);
  connectError: string | null = $state(null);
  account: AccountDTO | null = $state(null);
  /** Connect-time §10 capability snapshot; null until connected. */
  capabilities: CapabilitiesDTO | null = $state(null);
  /** Recent §50 audit events for the drawer, newest first. */
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
  // Per-job throughput (bytes/s) computed from successive poll deltas.
  jobRates: Record<string, number> = $state({});
  #lastPoll: Record<string, { bytes: number; at: number }> = {};

  // Find in container (§16): explicit scan, never per-keystroke.
  findOpen = $state(false);
  findStatus: FindStatusDTO | null = $state(null);
  findRunning = $state(false);
  #findId: string | null = null;

  // Containers pane.
  mode: ContainerMode = $state("logical");
  containers: ContainerRowDTO[] = $state([]);
  containersMarker = $state("");
  containersError: string | null = $state(null);
  #containersSeq = 0;

  // Working list.
  #containerName: string | null = $state(null);
  get containerName(): string | null { return this.#containerName; }
  set containerName(value: string | null) {
    if (value !== this.#containerName) this.#scopeRevision++;
    this.#containerName = value;
  }
  /** The open container's §53 HEAD detail; null while loading/closed. */
  containerDetail: ContainerDetailDTO | null = $state(null);
  /** Set while viewing a manifest's segment location (§23.1 backstage
   * jump): the label of where we came from; null in normal browsing. */
  backstageFrom: string | null = $state(null);
  #prefix = $state("");
  get prefix(): string { return this.#prefix; }
  set prefix(value: string) {
    if (value !== this.#prefix) this.#scopeRevision++;
    this.#prefix = value;
  }
  #filter = $state("");

  /** Loaded-row filter. Setting it re-anchors the selection BY NAME:
   * the selected identity survives a filter change, and when it is
   * filtered out the selection (and dependent detail state) clears —
   * an index can never silently point at a different object (the
   * review wave's C-04). */
  get filter(): string {
    return this.#filter;
  }

  set filter(value: string) {
    const selectedName = this.selectedEntry?.name ?? null;
    this.#filter = value;
    if (selectedName === null) {
      return;
    }
    const idx = this.visibleEntries.findIndex((e) => e.name === selectedName);
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

  #currentDetail(request: DetailRequest): boolean {
    return !this.connecting && request.scope === this.#scopeRevision && request.seq === this.#detailSeq
      && request.container === this.containerName
      && request.container === this.detailIdentity?.container && request.name === this.detailIdentity?.name;
  }

  #currentContainerDetail(scope: number, seq: number, name: string): boolean {
    return !this.connecting && scope === this.#scopeRevision && seq === this.#containerDetailSeq
      && name === this.containerName;
  }

  async loadBookmarks() {
    this.bookmarks = await this.#api.listBookmarks();
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
    this.#invalidateCredentialStatus();
    try {
      await this.#api.deleteBookmark(id);
      this.bookmarks = this.bookmarks.filter((b) => b.id !== id);
      if (this.connectedBookmarkId === id) {
        this.connectedBookmarkId = null;
        this.containers = [];
        this.containerName = null;
      }
    } finally {
      // Refresh even after an uncertain result; status must not change the
      // mutation's outcome or delay reporting it while the bridge is busy.
      void this.loadCredentialStatus();
    }
  }

  async saveBookmark(bookmark: BookmarkDTO, secret: string): Promise<BookmarkDTO> {
    this.#invalidateCredentialStatus();
    try {
      const saved = await this.#api.saveBookmark(bookmark, secret);
      await this.loadBookmarks();
      return saved;
    } finally {
      // A failed save can leave a prepared key even without a bookmark row.
      void this.loadCredentialStatus();
    }
  }

  async connect(bookmarkId: string) {
    // Reconnecting may change authority even when all visible names stay equal.
    this.#scopeRevision++;
    this.#connectionRevision++;
    this.connecting = true;
    this.connectError = null;
    try {
      this.account = await this.#api.connect(bookmarkId);
      this.connectedBookmarkId = bookmarkId;
      this.containerName = null;
      this.prefix = "";
      this.entries = [];
      this.selectedIndex = -1;
      this.capabilities = await this.#api.capabilities().catch(() => null);
      await this.loadContainers();
      await this.loadAudit();
    } catch (err) {
      this.connectError = messageOf(err);
      throw err;
    } finally {
      this.connecting = false;
    }
  }

  async loadContainers() {
    const scope = this.#scopeRevision;
    const seq = ++this.#containersSeq;
    this.containersError = null;
    try {
      const page = await this.#api.containers("", pageLimit);
      if (scope === this.#scopeRevision && seq === this.#containersSeq) {
        this.containers = page.rows;
        this.containersMarker = page.nextMarker;
      }
    } catch (err) {
      if (scope === this.#scopeRevision && seq === this.#containersSeq) this.containersError = messageOf(err);
    }
  }

  async loadMoreContainers() {
    if (this.containersMarker === "") {
      return;
    }
    const page = await this.#api.containers(this.containersMarker, pageLimit);
    this.containers = [...this.containers, ...page.rows];
    this.containersMarker = page.nextMarker;
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
    this.containerName = name;
    this.containerDetail = null;
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
    if (!this.containerName) {
      return;
    }
    await this.#loadEntries(prefix);
  }

  /** Backspace / Alt+↑ (§4.4): parent prefix, then out of the container. */
  async up() {
    if (!this.containerName) {
      return;
    }
    if (this.prefix === "") {
      this.containerName = null;
      this.containerDetail = null;
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
    const seq = ++this.#listSeq;
    this.prefix = prefix;
    this.filter = "";
    this.select(-1);
    const scope = this.#scopeRevision;
    this.entriesLoading = true;
    this.entriesError = null;
    try {
      const page = await this.#api.objects(this.containerName ?? "", prefix, "", pageLimit);
      if (seq !== this.#listSeq || scope !== this.#scopeRevision) {
        return; // a newer navigation superseded this load
      }
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
    if (this.entriesMarker === "" || !this.containerName) {
      return;
    }
    const page = await this.#api.objects(
      this.containerName,
      this.prefix,
      this.entriesMarker,
      pageLimit,
    );
    this.entries = [...this.entries, ...page.rows];
    this.entriesMarker = page.nextMarker;
  }

  /** Selection moves; the selected row triggers its authoritative HEAD
   * (§14.2) — object detail or prefix classification. */
  select(index: number) {
    this.selectedIndex = index;
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
    if (!entry || !container) {
      this.detailLoading = false;
      return;
    }
    this.detailLoading = true;
    const request = { scope: this.#scopeRevision, seq, container, name: entry.name };
    const fetch =
      entry.kind === "object"
        ? this.#api.objectDetail(container, entry.name).then(async (detail) => {
            if (!this.#currentDetail(request)) {
              return;
            }
            if (detail.name !== request.name) throw new Error("object detail does not match the selected name");
            this.objectDetail = detail;
            if (detail.class === "slo") {
              // The exact segment table (§23.2) — still one selection.
              const manifest = await this.#api.sloManifest(container, entry.name);
              if (this.#currentDetail(request)) {
                this.sloManifest = manifest;
              }
            } else if (detail.class === "dlo" && detail.manifest !== "") {
              // The bounded §23.1 preview.
              const preview = await this.#api.dloSegments(detail.manifest);
              if (this.#currentDetail(request)) {
                this.dloSegments = preview;
              }
            }
          })
        : this.#api.prefixDetail(container, entry.name).then((detail) => {
            if (this.#currentDetail(request)) {
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

  async loadJobs() {
    try {
      const jobs = await this.#api.jobs(this.jobsFilter, false, 50);
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
      this.jobsError = messageOf(error);
    }
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
    this.appSettings = await this.#api.appSettings().catch(() => this.appSettings);
  }

  async saveAppSettings(settings: AppSettingsDTO) {
    this.appSettings = await this.#api.saveAppSettings(settings);
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

  async pickUploadFiles(): Promise<PickedFilesDTO | null> {
    return await this.#api.pickUploadFiles();
  }

  async pickDownloadDir(): Promise<PickedDirDTO | null> {
    return await this.#api.pickDownloadDir();
  }

  async preflightUpload(req: UploadRequestDTO): Promise<UploadPreflight> {
    return await this.#api.preflightUpload(req);
  }

  async startUpload(req: UploadRequestDTO): Promise<string> {
    const id = await this.#api.startUpload(req);
    await this.loadJobs();
    return id;
  }

  async preflightDownload(req: DownloadRequestDTO): Promise<DownloadPreflight> {
    return await this.#api.preflightDownload(req);
  }

  async startDownload(req: DownloadRequestDTO): Promise<string> {
    const id = await this.#api.startDownload(req);
    await this.loadJobs();
    return id;
  }

  async startFind(query: string) {
    if (this.containerName === null) {
      return;
    }
    await this.cancelFind();
    this.findStatus = null;
    this.findRunning = true;
    try {
      this.#findId = await this.#api.startFind(this.containerName, this.prefix, query);
      while (this.findRunning && this.#findId !== null) {
        const status = await this.#api.findStatus(this.#findId);
        this.findStatus = status;
        if (status.done) {
          break;
        }
        await new Promise((resolve) => setTimeout(resolve, 400));
      }
    } catch (error) {
      this.findStatus = {
        scanned: this.findStatus?.scanned ?? 0,
        matches: this.findStatus?.matches ?? [],
        truncated: false,
        done: true,
        error: messageOf(error),
        objectsPerSecond: 0,
      };
    } finally {
      this.findRunning = false;
    }
  }

  async cancelFind() {
    this.findRunning = false;
    if (this.#findId !== null) {
      await this.#api.cancelFind(this.#findId).catch(() => {});
      this.#findId = null;
    }
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
  auditFilterOp = $state("");
  auditFilterResult = $state("");
  auditExhausted = $state(false);

  async searchAudit(more = false) {
    const beforeId = more && this.auditEvents.length > 0
      ? this.auditEvents[this.auditEvents.length - 1].id
      : 0;
    const page = await this.#api.auditSearch({
      operationPrefix: this.auditFilterOp,
      result: this.auditFilterResult,
      bookmarkId: "",
      beforeId,
      limit: 50,
    });
    this.auditExhausted = page.length < 50;
    this.auditEvents = more ? [...this.auditEvents, ...page] : page;
  }

  async loadAudit() {
    this.auditEvents = await this.#api.auditRecent(50).catch(() => this.auditEvents);
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
    const result = await this.#api.updateObjectMetadata(identity.container, identity.name, base, edit);
    if (this.#currentDetail(request) && result.outcome === "applied" && result.objectAfter
      && result.objectAfter.name === identity.name) {
      this.objectDetail = result.objectAfter;
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
    const result = await this.#api.updateContainer(name, base, edit);
    if (this.#currentContainerDetail(scope, seq, name) && result.outcome === "applied" && result.containerAfter
      && result.containerAfter.name === name) {
      this.containerDetail = result.containerAfter;
    }
    await this.loadAudit();
    return result;
  }

  async refreshContainerDetail() {
    const name = this.containerName;
    if (!name) {
      return;
    }
    const scope = this.#scopeRevision;
    const seq = ++this.#containerDetailSeq;
    const connection = this.#connectionRevision;
    const detail = await this.#api.containerDetail(name).catch(() => null);
    if (this.#currentContainerDetail(scope, seq, name)) {
      this.containerDetail = detail?.name === name ? detail : null;
      // A prefix change does not change this container's working-copy identity.
      this.#containerDetailConnection = connection;
    }
  }

  /** Create a container and open it. */
  async createContainer(create: ContainerCreateDTO, ownsView = () => true) {
    const scope = this.#scopeRevision;
    const selection = this.detailIdentity;
    await this.#api.createContainer(create);
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
    const detail = await this.#api.createMarker(container, prefix);
    if (this.#currentDetail(request) && detail.prefix === prefix) this.prefixDetail = detail;
    await this.loadAudit();
  }

  /** Jump backstage to a manifest's segment location (§23.1): browse
   * the segment container under its prefix, with a banner naming the
   * manifest we came from. */
  async openBackstage(container: string, prefix: string, fromLabel: string) {
    await this.openContainer(container);
    if (prefix !== "") {
      await this.goTo(prefix);
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
