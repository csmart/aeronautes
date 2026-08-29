// Central UI state for live browsing (§65 commit 13): bookmarks →
// connect → containers → delimiter pages → selection-triggered HEAD.
// All data flows through the Api seam so this logic is tested against a
// fixture-backed fake; loading and error state live per pane.

import type {
  AccountDTO,
  Api,
  BookmarkDTO,
  ContainerDetailDTO,
  ContainerRowDTO,
  ListingRowDTO,
  ObjectDetailDTO,
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

  // Containers pane.
  mode: ContainerMode = $state("logical");
  containers: ContainerRowDTO[] = $state([]);
  containersMarker = $state("");
  containersError: string | null = $state(null);

  // Working list.
  containerName: string | null = $state(null);
  /** The open container's §53 HEAD detail; null while loading/closed. */
  containerDetail: ContainerDetailDTO | null = $state(null);
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
      await this.loadContainers();
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
}

function messageOf(err: unknown): string {
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}
