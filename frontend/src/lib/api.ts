// The frontend's single doorway to Go (§55): everything the UI can ask
// for is this interface. Components and state never import bindings
// directly — the seam is what lets navigation logic be tested against a
// fixture-backed fake, and where nullable wire shapes are normalised.

import {
  AppService,
  AuditService,
  BookmarkService,
  BrowseService,
  BenchmarkService,
  DeleteService,
  FindService,
  SettingsService,
  InspectorService,
  JobService,
  MutateService,
  TransferService,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";
import type {
  DownloadPreflightView,
  UploadPreflight,
} from "../../bindings/github.com/csmart/aeronautes/internal/transfer/local";
import type {
  AccountDTO,
  AuditEventDTO,
  BookmarkDTO,
  CredentialStatusDTO,
  RecoveryStatusDTO,
  CapabilitiesDTO,
  ContainerCreateDTO,
  ContainerDetailDTO,
  ContainerEditDTO,
  ContainerRowDTO,
  ContainerSettingsDTO,
  CopyPlanDTO,
  CopyRequestDTO,
  DLOSegmentsDTO,
  Info,
  JobDTO,
  JobHistoryDTO,
  JobItemDTO,
  JobItemLocationDTO,
  JobItemObservationDTO,
  JobItemPageDTO,
  JobItemTotalsDTO,
  ListingRowDTO,
  MutationResultDTO,
  ObjectDetailDTO,
  ObjectMetadataEditDTO,
  PrefixDetailDTO,
  SLOManifestDTO,
  PickedDirDTO,
  PickedFilesDTO,
  SelectionScopeDTO,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";

export type {
  AccountDTO,
  PickedDirDTO,
  PickedFilesDTO,
  SelectionScopeDTO,
  AuditEventDTO,
  BookmarkDTO,
  CredentialStatusDTO,
  RecoveryStatusDTO,
  CapabilitiesDTO,
  ContainerCreateDTO,
  ContainerDetailDTO,
  ContainerEditDTO,
  ContainerRowDTO,
  ContainerSettingsDTO,
  CopyPlanDTO,
  CopyRequestDTO,
  DLOSegmentsDTO,
  Info,
  JobDTO,
  JobHistoryDTO,
  JobItemDTO,
  JobItemLocationDTO,
  JobItemObservationDTO,
  JobItemPageDTO,
  JobItemTotalsDTO,
  ListingRowDTO,
  MutationResultDTO,
  ObjectDetailDTO,
  ObjectMetadataEditDTO,
  PrefixDetailDTO,
  SLOManifestDTO,
};
export type { DownloadPreflightView, UploadPreflight };
export type {
  DeletePreviewDTO,
  DeleteRequestDTO,
  DownloadRequestDTO,
  About,
  AppSettingsDTO,
  AuditQueryDTO,
  BenchmarkRequestDTO,
  BenchmarkResultDTO,
  FindStatusDTO,
  MirrorRequestDTO,
  SegmentOwnershipDTO,
  SegmentsDeleteRequestDTO,
  SegmentsDeleteResultDTO,
  UploadRequestDTO,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";
import type {
  About,
  AppSettingsDTO,
  AuditQueryDTO,
  BenchmarkRequestDTO,
  BenchmarkResultDTO,
  DeletePreviewDTO,
  DeleteRequestDTO,
  DownloadRequestDTO,
  FindStatusDTO,
  MirrorRequestDTO,
  SegmentOwnershipDTO,
  SegmentsDeleteRequestDTO,
  SegmentsDeleteResultDTO,
  UploadRequestDTO,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";

/** Which account produced a page (ADR 0049). A listing names no account
 * in the protocol, so the reply states it and the caller binds what it
 * displays to the account it asked for. */
export interface ListingScope {
  bookmarkId: string;
  generation: number;
}

export interface ContainerPage {
  rows: ContainerRowDTO[];
  nextMarker: string;
  scope: ListingScope;
}

export interface ObjectPage {
  rows: ListingRowDTO[];
  nextMarker: string;
  scope: ListingScope;
}

export interface Api {
  info(): Promise<Info>;
  about(): Promise<About>;
  listBookmarks(): Promise<BookmarkDTO[]>;
  credentialStatus(): Promise<CredentialStatusDTO>;
  saveBookmark(bookmark: BookmarkDTO, secret: string): Promise<BookmarkDTO>;
  deleteBookmark(id: string): Promise<void>;
  connect(bookmarkId: string): Promise<AccountDTO>;
  containers(bookmarkId: string, marker: string, limit: number): Promise<ContainerPage>;
  objects(
    bookmarkId: string,
    container: string,
    prefix: string,
    marker: string,
    limit: number,
  ): Promise<ObjectPage>;
  objectDetail(container: string, name: string, bookmarkId: string): Promise<ObjectDetailDTO>;
  prefixDetail(container: string, prefix: string, bookmarkId: string): Promise<PrefixDetailDTO>;
  containerDetail(name: string, bookmarkId: string): Promise<ContainerDetailDTO>;
  sloManifest(container: string, name: string, bookmarkId: string): Promise<SLOManifestDTO>;
  dloSegments(manifest: string, bookmarkId: string): Promise<DLOSegmentsDTO>;
  deleteManifest(container: string, name: string): Promise<ObjectDetailDTO>;
  capabilities(bookmarkId: string): Promise<CapabilitiesDTO>;
  updateObjectMetadata(
    container: string,
    name: string,
    baseFingerprint: string,
    edit: ObjectMetadataEditDTO,
  ): Promise<MutationResultDTO>;
  updateContainer(
    name: string,
    baseFingerprint: string,
    edit: ContainerEditDTO,
  ): Promise<MutationResultDTO>;
  createContainer(create: ContainerCreateDTO): Promise<ContainerDetailDTO>;
  createMarker(container: string, prefix: string): Promise<PrefixDetailDTO>;
  deleteMarker(container: string, prefix: string): Promise<void>;
  auditRecent(limit: number, bookmarkId: string): Promise<AuditEventDTO[]>;
  auditSearch(q: AuditQueryDTO): Promise<AuditEventDTO[]>;
  planCopy(req: CopyRequestDTO): Promise<CopyPlanDTO>;
  startCopy(req: CopyRequestDTO): Promise<string>;
  startMirror(req: MirrorRequestDTO): Promise<string>;
  cancelJob(jobId: string): Promise<void>;
  rerunJob(jobId: string): Promise<string>;
  jobs(bookmarkId: string, activeOnly: boolean, limit: number): Promise<JobDTO[]>;
  job(jobId: string, bookmarkId: string): Promise<JobDTO | null>;
  jobHistory(bookmarkId: string, cursor: string, limit: number): Promise<JobHistoryDTO>;
  jobItems(jobId: string, cursor: string, limit: number, bookmarkId: string): Promise<JobItemPageDTO>;
  jobItemTotals(jobId: string, bookmarkId: string): Promise<JobItemTotalsDTO>;
  recoveryStatus(): Promise<RecoveryStatusDTO>;
  pickUploadFiles(scope: SelectionScopeDTO): Promise<PickedFilesDTO | null>;
  pickDownloadDir(scope: SelectionScopeDTO): Promise<PickedDirDTO | null>;
  preflightUpload(req: UploadRequestDTO): Promise<UploadPreflight>;
  startUpload(req: UploadRequestDTO): Promise<string>;
  preflightDownload(req: DownloadRequestDTO): Promise<DownloadPreflightView>;
  startDownload(req: DownloadRequestDTO): Promise<string>;
  previewDelete(req: DeleteRequestDTO): Promise<DeletePreviewDTO>;
  startDelete(req: DeleteRequestDTO): Promise<string>;
  startBenchmark(req: BenchmarkRequestDTO): Promise<string>;
  latestBenchmark(bookmarkId: string): Promise<BenchmarkResultDTO | null>;
  appSettings(): Promise<AppSettingsDTO>;
  saveAppSettings(settings: AppSettingsDTO): Promise<AppSettingsDTO>;
  /** countOnly lists the scope and counts it without keeping matches (ADR 0072). */
  startFind(container: string, prefix: string, query: string, bookmarkId: string, countOnly?: boolean): Promise<string>;
  findStatus(id: string, bookmarkId: string): Promise<FindStatusDTO>;
  cancelFind(id: string, bookmarkId: string): Promise<void>;
  checkSegmentOwnership(req: SegmentsDeleteRequestDTO): Promise<SegmentOwnershipDTO>;
  deleteManifestWithSegments(req: SegmentsDeleteRequestDTO): Promise<SegmentsDeleteResultDTO>;
}

/** The live bridge. */
export const liveApi: Api = {
  info: () => AppService.Info(),
  about: async () => {
    const about = await AppService.About();
    if (!about) throw new Error("empty about");
    return about;
  },
  listBookmarks: async () => (await BookmarkService.List()) ?? [],
  credentialStatus: () => BookmarkService.CredentialStatus(),
  saveBookmark: (bookmark, secret) => BookmarkService.Save(bookmark, secret),
  deleteBookmark: (id) => BookmarkService.Delete(id),
  connect: (bookmarkId) => BrowseService.Connect(bookmarkId),
  containers: async (bookmarkId, marker, limit) => {
    const page = await BrowseService.Containers(bookmarkId, marker, limit);
    return { rows: page.rows ?? [], nextMarker: page.nextMarker, scope: page.scope };
  },
  objects: async (bookmarkId, container, prefix, marker, limit) => {
    const page = await BrowseService.Objects(bookmarkId, container, prefix, marker, limit);
    return { rows: page.rows ?? [], nextMarker: page.nextMarker, scope: page.scope };
  },
  objectDetail: (container, name, bookmarkId) => InspectorService.Object(bookmarkId, container, name),
  prefixDetail: (container, prefix, bookmarkId) => InspectorService.Prefix(bookmarkId, container, prefix),
  containerDetail: (name, bookmarkId) => InspectorService.Container(bookmarkId, name),
  sloManifest: (container, name, bookmarkId) => InspectorService.SLOManifest(bookmarkId, container, name),
  dloSegments: (manifest, bookmarkId) => InspectorService.DLOSegments(bookmarkId, manifest),
  deleteManifest: (container, name) => MutateService.DeleteManifest(container, name),
  capabilities: (bookmarkId) => BrowseService.Capabilities(bookmarkId),
  updateObjectMetadata: (container, name, baseFingerprint, edit) =>
    MutateService.UpdateObjectMetadata(container, name, baseFingerprint, edit),
  updateContainer: (name, baseFingerprint, edit) =>
    MutateService.UpdateContainer(name, baseFingerprint, edit),
  createContainer: (create) => MutateService.CreateContainer(create),
  createMarker: (container, prefix) => MutateService.CreateMarker(container, prefix),
  deleteMarker: (container, prefix) => MutateService.DeleteMarker(container, prefix),
  auditRecent: async (limit, bookmarkId) =>
    (await AuditService.RecentFor(bookmarkId, limit)) ?? [],
  auditSearch: async (q) => (await AuditService.Search(q)) ?? [],
  planCopy: async (req) => {
    const dto = await TransferService.PlanCopy(req);
    if (!dto) throw new Error("empty plan");
    return dto;
  },
  startCopy: (req) => TransferService.StartCopy(req),
  startMirror: (req) => TransferService.StartMirror(req),
  cancelJob: (jobId) => TransferService.CancelJob(jobId),
  rerunJob: (jobId) => TransferService.RerunJob(jobId),
  jobs: async (bookmarkId, activeOnly, limit) =>
    (await JobService.List(bookmarkId, activeOnly, limit)) ?? [],
  recoveryStatus: () => JobService.RecoveryStatus(),
  job: (jobId, bookmarkId) => JobService.Get(bookmarkId, jobId),
  jobHistory: (bookmarkId, cursor, limit) => JobService.History(bookmarkId, cursor, limit),
  jobItems: (jobId, cursor, limit, bookmarkId) => JobService.Items(bookmarkId, jobId, cursor, limit),
  jobItemTotals: (jobId, bookmarkId) => JobService.ItemTotals(bookmarkId, jobId),
  pickUploadFiles: (scope) => TransferService.PickUploadFiles(scope),
  pickDownloadDir: (scope) => TransferService.PickDownloadDir(scope),
  preflightUpload: async (req) => {
    const report = await TransferService.PreflightUpload(req);
    if (!report) throw new Error("empty preflight");
    return report;
  },
  startUpload: (req) => TransferService.StartUpload(req),
  preflightDownload: async (req) => {
    const report = await TransferService.PreflightDownload(req);
    if (!report) throw new Error("empty preflight");
    return report;
  },
  startDownload: (req) => TransferService.StartDownload(req),
  previewDelete: async (req) => {
    const preview = await DeleteService.PreviewDelete(req);
    if (!preview) throw new Error("empty preview");
    return preview;
  },
  startDelete: (req) => DeleteService.StartDelete(req),
  startBenchmark: (req) => BenchmarkService.StartBenchmark(req),
  latestBenchmark: (bookmarkId) => BenchmarkService.Latest(bookmarkId),
  appSettings: async () => {
    const settings = await SettingsService.Get();
    if (!settings) throw new Error("empty settings");
    return settings;
  },
  saveAppSettings: async (settings) => {
    const saved = await SettingsService.Set(settings);
    if (!saved) throw new Error("empty settings");
    return saved;
  },
  startFind: (container, prefix, query, bookmarkId, countOnly = false) =>
    FindService.StartFind(bookmarkId, container, prefix, query, countOnly),
  findStatus: async (id, bookmarkId) => {
    const status = await FindService.FindStatus(bookmarkId, id);
    if (!status) throw new Error("scan vanished");
    return status;
  },
  cancelFind: (id, bookmarkId) => FindService.CancelFind(bookmarkId, id),
  checkSegmentOwnership: async (req) => {
    const status = await DeleteService.CheckSegmentOwnership(req);
    if (!status) throw new Error("empty ownership status");
    return status;
  },
  deleteManifestWithSegments: async (req) => {
    const result = await DeleteService.DeleteManifestWithSegments(req);
    if (!result) throw new Error("empty result");
    return result;
  },
};
