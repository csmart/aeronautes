// Central UI state for live browsing (§65 commit 13): bookmarks →
// connect → containers → delimiter pages → selection-triggered HEAD.
// All data flows through the Api seam so this logic is tested against a
// fixture-backed fake; loading and error state live per pane.

import type {
  AccountDTO,
  CopyPlanDTO,
  CopyRequestDTO,
  DeletePreviewDTO,
  DeleteRequestDTO,
  SegmentOwnershipDTO,
  DownloadPreflight,
  JobDTO,
  UploadPreflight,
  Api,
  AuditEventDTO,
  BookmarkDTO,
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

export class BrowserState {
  #api: Api;

  constructor(api: Api) {
    this.#api = api;
  }

  // Shell environment.
  /** "keychain" or "session" (§12.1) — "" until Info arrives. */
  secretBackend = $state("");

  // Bookmarks and connection.
  bookmarks: BookmarkDTO[] = $state([]);
  connectedBookmarkId: string | null = $state(null);
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

  // Transfer dialog (§27.1 Copy / §27.2 Mirror preview).
  transferOpen = $state(false);
  // Local transfer dialog (§27.5); null = closed.
  localTransfer: "upload" | "download" | null = $state(null);
  // Destructive-jobs dialog (§31–§33).
  deleteOpen = $state(false);

  // Containers pane.
  mode: ContainerMode = $state("logical");
  containers: ContainerRowDTO[] = $state([]);
  containersMarker = $state("");
  containersError: string | null = $state(null);

  // Working list.
  containerName: string | null = $state(null);
  /** The open container's §53 HEAD detail; null while loading/closed. */
  containerDetail: ContainerDetailDTO | null = $state(null);
  /** Set while viewing a manifest's segment location (§23.1 backstage
   * jump): the label of where we came from; null in normal browsing. */
  backstageFrom: string | null = $state(null);
  prefix = $state("");
  filter = $state("");
  entries: ListingRowDTO[] = $state([]);
  entriesMarker = $state("");
  entriesLoading = $state(false);
  entriesError: string | null = $state(null);
  selectedIndex = $state(-1);

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
  #listSeq = 0;

  async loadBookmarks() {
    this.bookmarks = await this.#api.listBookmarks();
  }

  async saveBookmark(bookmark: BookmarkDTO, secret: string): Promise<BookmarkDTO> {
    const saved = await this.#api.saveBookmark(bookmark, secret);
    await this.loadBookmarks();
    return saved;
  }

  async connect(bookmarkId: string) {
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
    this.containersError = null;
    try {
      const page = await this.#api.containers("", pageLimit);
      this.containers = page.rows;
      this.containersMarker = page.nextMarker;
    } catch (err) {
      this.containersError = messageOf(err);
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
    // §14.2: opening IS selecting the container — one HEAD for the
    // inspector; failure falls back to the listing row's fields.
    this.#api
      .containerDetail(name)
      .then((detail) => {
        if (this.containerName === name) {
          this.containerDetail = detail;
        }
      })
      .catch(() => {});
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
    this.entriesLoading = true;
    this.entriesError = null;
    try {
      const page = await this.#api.objects(this.containerName ?? "", prefix, "", pageLimit);
      if (seq !== this.#listSeq) {
        return; // a newer navigation superseded this load
      }
      this.entries = page.rows;
      this.entriesMarker = page.nextMarker;
    } catch (err) {
      if (seq === this.#listSeq) {
        this.entries = [];
        this.entriesError = messageOf(err);
      }
    } finally {
      if (seq === this.#listSeq) {
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
    const fetch =
      entry.kind === "object"
        ? this.#api.objectDetail(container, entry.name).then(async (detail) => {
            if (seq !== this.#detailSeq) {
              return;
            }
            this.objectDetail = detail;
            if (detail.class === "slo") {
              // The exact segment table (§23.2) — still one selection.
              const manifest = await this.#api.sloManifest(container, entry.name);
              if (seq === this.#detailSeq) {
                this.sloManifest = manifest;
              }
            } else if (detail.class === "dlo" && detail.manifest !== "") {
              // The bounded §23.1 preview.
              const preview = await this.#api.dloSegments(detail.manifest);
              if (seq === this.#detailSeq) {
                this.dloSegments = preview;
              }
            }
          })
        : this.#api.prefixDetail(container, entry.name).then((detail) => {
            if (seq === this.#detailSeq) {
              this.prefixDetail = detail;
            }
          });
    fetch
      .catch((err) => {
        if (seq === this.#detailSeq) {
          this.detailError = messageOf(err);
        }
      })
      .finally(() => {
        if (seq === this.#detailSeq) {
          this.detailLoading = false;
        }
      });
  }

  // ---- Mutations (§65.2 items 6/7): thin, state-refreshing wrappers
  // over MutateService. Outcome handling — conflict banners, overwrite
  // choices — belongs to the editors; state here only stays coherent.

  async loadJobs() {
    try {
      this.jobs = await this.#api.jobs(this.jobsFilter, false, 50);
      this.jobsError = null;
    } catch (error) {
      this.jobsError = messageOf(error);
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

  async cancelJob(jobId: string) {
    await this.#api.cancelJob(jobId);
    await this.loadJobs();
  }

  async pickUploadFiles(): Promise<string[]> {
    return await this.#api.pickUploadFiles();
  }

  async pickDownloadDir(): Promise<string> {
    return await this.#api.pickDownloadDir();
  }

  #localScope() {
    return {
      bookmarkId: this.connectedBookmarkId ?? "",
      container: this.containerName ?? "",
      prefix: this.prefix,
      settings: { chunkSize: "", transfers: 0, checkers: 0, tpsLimit: 0, bwLimit: "" },
    };
  }

  async preflightUpload(paths: string[]): Promise<UploadPreflight> {
    return await this.#api.preflightUpload({ ...this.#localScope(), paths });
  }

  async startUpload(paths: string[]): Promise<string> {
    const id = await this.#api.startUpload({ ...this.#localScope(), paths });
    await this.loadJobs();
    return id;
  }

  async preflightDownload(destDir: string): Promise<DownloadPreflight> {
    return await this.#api.preflightDownload({ ...this.#localScope(), destDir });
  }

  async startDownload(destDir: string): Promise<string> {
    const id = await this.#api.startDownload({ ...this.#localScope(), destDir });
    await this.loadJobs();
    return id;
  }

  async previewDelete(req: DeleteRequestDTO): Promise<DeletePreviewDTO> {
    return await this.#api.previewDelete(req);
  }

  async startDelete(req: DeleteRequestDTO): Promise<string> {
    const id = await this.#api.startDelete(req);
    await this.loadJobs();
    return id;
  }

  async checkSegmentOwnership(container: string, name: string): Promise<SegmentOwnershipDTO> {
    return await this.#api.checkSegmentOwnership({
      bookmarkId: this.connectedBookmarkId ?? "",
      container,
      name,
      force: false,
      confirm: "",
    });
  }

  async deleteManifestWithSegments(container: string, name: string, force: boolean, confirm: string) {
    const result = await this.#api.deleteManifestWithSegments({
      bookmarkId: this.connectedBookmarkId ?? "",
      container,
      name,
      force,
      confirm,
    });
    // The inspector's selection is gone; refresh like deleteManifest.
    await this.loadAudit();
    return result;
  }

  async loadAudit() {
    this.auditEvents = await this.#api.auditRecent(50).catch(() => this.auditEvents);
  }

  /** Save an object metadata working copy. force skips the fingerprint
   * check — the deliberate "apply mine anyway" after a shown conflict. */
  async saveObjectMetadata(edit: ObjectMetadataEditDTO, force = false): Promise<MutationResultDTO> {
    const entry = this.selectedEntry;
    const container = this.containerName;
    const detail = this.objectDetail;
    if (!entry || entry.kind !== "object" || !container || !detail) {
      throw new Error("no object selected");
    }
    const base = force ? "" : detail.editableFingerprint;
    const result = await this.#api.updateObjectMetadata(container, entry.name, base, edit);
    if (result.outcome === "applied" && result.objectAfter) {
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
    if (!name || !detail) {
      throw new Error("no container open");
    }
    const base = force ? "" : detail.editableFingerprint;
    const result = await this.#api.updateContainer(name, base, edit);
    if (result.outcome === "applied" && result.containerAfter) {
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
    this.containerDetail = await this.#api.containerDetail(name).catch(() => null);
  }

  /** Create a container and open it. */
  async createContainer(create: ContainerCreateDTO) {
    await this.#api.createContainer(create);
    await this.loadContainers();
    await this.loadAudit();
    await this.openContainer(create.name);
  }

  /** Create the explicit marker for the selected implied prefix. */
  async createMarker(prefix: string) {
    const container = this.containerName;
    if (!container) {
      throw new Error("no container open");
    }
    this.prefixDetail = await this.#api.createMarker(container, prefix);
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

  /** Delete ONLY the selected manifest object (§60 Phase 4's low-risk
   * delete): segments survive; the working list refreshes. */
  async deleteManifest(): Promise<void> {
    const entry = this.selectedEntry;
    const container = this.containerName;
    if (!entry || entry.kind !== "object" || !container) {
      throw new Error("no object selected");
    }
    await this.#api.deleteManifest(container, entry.name);
    await this.loadAudit();
    await this.goTo(this.prefix);
  }

  /** Delete ONLY a verified marker; the working list refreshes. */
  async deleteMarker(prefix: string) {
    const container = this.containerName;
    if (!container) {
      throw new Error("no container open");
    }
    await this.#api.deleteMarker(container, prefix);
    await this.loadAudit();
    await this.goTo(this.prefix);
  }
}

function messageOf(err: unknown): string {
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}
