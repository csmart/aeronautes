// The frontend's single doorway to Go (§55): everything the UI can ask
// for is this interface. Components and state never import bindings
// directly — the seam is what lets navigation logic be tested against a
// fixture-backed fake, and where nullable wire shapes are normalised.

import {
  AppService,
  AuditService,
  BookmarkService,
  BrowseService,
  DeleteService,
  FindService,
  SettingsService,
  InspectorService,
  JobService,
  MutateService,
  TransferService,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";
import type {
  DownloadPreflight,
  UploadPreflight,
} from "../../bindings/github.com/csmart/aeronautes/internal/transfer/local";
import type {
  AccountDTO,
  AuditEventDTO,
  BookmarkDTO,
  CapabilitiesDTO,
  ContainerCreateDTO,
  ContainerDetailDTO,
  ContainerEditDTO,
  ContainerRowDTO,
  CopyPlanDTO,
  CopyRequestDTO,
  DLOSegmentsDTO,
  Info,
  JobDTO,
  ListingRowDTO,
  MutationResultDTO,
  ObjectDetailDTO,
  ObjectMetadataEditDTO,
  PrefixDetailDTO,
  SLOManifestDTO,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";

export type {
  AccountDTO,
  AuditEventDTO,
  BookmarkDTO,
  CapabilitiesDTO,
  ContainerCreateDTO,
  ContainerDetailDTO,
  ContainerEditDTO,
  ContainerRowDTO,
  CopyPlanDTO,
  CopyRequestDTO,
  DLOSegmentsDTO,
  Info,
  JobDTO,
  ListingRowDTO,
  MutationResultDTO,
  ObjectDetailDTO,
  ObjectMetadataEditDTO,
  PrefixDetailDTO,
  SLOManifestDTO,
};
export type { DownloadPreflight, UploadPreflight };
export type {
  DeletePreviewDTO,
  DeleteRequestDTO,
  DownloadRequestDTO,
  AppSettingsDTO,
  FindStatusDTO,
  MirrorRequestDTO,
  SegmentOwnershipDTO,
  SegmentsDeleteRequestDTO,
  SegmentsDeleteResultDTO,
  UploadRequestDTO,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";
import type {
  AppSettingsDTO,
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

export interface ContainerPage {
  rows: ContainerRowDTO[];
  nextMarker: string;
}

export interface ObjectPage {
  rows: ListingRowDTO[];
  nextMarker: string;
}

export interface Api {
  info(): Promise<Info>;
  listBookmarks(): Promise<BookmarkDTO[]>;
  saveBookmark(bookmark: BookmarkDTO, secret: string): Promise<BookmarkDTO>;
  deleteBookmark(id: string): Promise<void>;
  connect(bookmarkId: string): Promise<AccountDTO>;
  containers(marker: string, limit: number): Promise<ContainerPage>;
  objects(
    container: string,
    prefix: string,
    marker: string,
    limit: number,
  ): Promise<ObjectPage>;
  objectDetail(container: string, name: string): Promise<ObjectDetailDTO>;
  prefixDetail(container: string, prefix: string): Promise<PrefixDetailDTO>;
  containerDetail(name: string): Promise<ContainerDetailDTO>;
  sloManifest(container: string, name: string): Promise<SLOManifestDTO>;
  dloSegments(manifest: string): Promise<DLOSegmentsDTO>;
  deleteManifest(container: string, name: string): Promise<ObjectDetailDTO>;
  capabilities(): Promise<CapabilitiesDTO>;
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
  auditRecent(limit: number): Promise<AuditEventDTO[]>;
  planCopy(req: CopyRequestDTO): Promise<CopyPlanDTO>;
  startCopy(req: CopyRequestDTO): Promise<string>;
  startMirror(req: MirrorRequestDTO): Promise<string>;
  cancelJob(jobId: string): Promise<void>;
  jobs(bookmarkId: string, activeOnly: boolean, limit: number): Promise<JobDTO[]>;
  pickUploadFiles(): Promise<string[]>;
  pickDownloadDir(): Promise<string>;
  preflightUpload(req: UploadRequestDTO): Promise<UploadPreflight>;
  startUpload(req: UploadRequestDTO): Promise<string>;
  preflightDownload(req: DownloadRequestDTO): Promise<DownloadPreflight>;
  startDownload(req: DownloadRequestDTO): Promise<string>;
  previewDelete(req: DeleteRequestDTO): Promise<DeletePreviewDTO>;
  startDelete(req: DeleteRequestDTO): Promise<string>;
  appSettings(): Promise<AppSettingsDTO>;
  saveAppSettings(settings: AppSettingsDTO): Promise<AppSettingsDTO>;
  startFind(container: string, prefix: string, query: string): Promise<string>;
  findStatus(id: string): Promise<FindStatusDTO>;
  cancelFind(id: string): Promise<void>;
  checkSegmentOwnership(req: SegmentsDeleteRequestDTO): Promise<SegmentOwnershipDTO>;
  deleteManifestWithSegments(req: SegmentsDeleteRequestDTO): Promise<SegmentsDeleteResultDTO>;
}

/** The live bridge. */
export const liveApi: Api = {
  info: () => AppService.Info(),
  listBookmarks: async () => (await BookmarkService.List()) ?? [],
  saveBookmark: (bookmark, secret) => BookmarkService.Save(bookmark, secret),
  deleteBookmark: (id) => BookmarkService.Delete(id),
  connect: (bookmarkId) => BrowseService.Connect(bookmarkId),
  containers: async (marker, limit) => {
    const page = await BrowseService.Containers(marker, limit);
    return { rows: page.rows ?? [], nextMarker: page.nextMarker };
  },
  objects: async (container, prefix, marker, limit) => {
    const page = await BrowseService.Objects(container, prefix, marker, limit);
    return { rows: page.rows ?? [], nextMarker: page.nextMarker };
  },
  objectDetail: (container, name) => InspectorService.Object(container, name),
  prefixDetail: (container, prefix) => InspectorService.Prefix(container, prefix),
  containerDetail: (name) => InspectorService.Container(name),
  sloManifest: (container, name) => InspectorService.SLOManifest(container, name),
  dloSegments: (manifest) => InspectorService.DLOSegments(manifest),
  deleteManifest: (container, name) => MutateService.DeleteManifest(container, name),
  capabilities: () => BrowseService.Capabilities(),
  updateObjectMetadata: (container, name, baseFingerprint, edit) =>
    MutateService.UpdateObjectMetadata(container, name, baseFingerprint, edit),
  updateContainer: (name, baseFingerprint, edit) =>
    MutateService.UpdateContainer(name, baseFingerprint, edit),
  createContainer: (create) => MutateService.CreateContainer(create),
  createMarker: (container, prefix) => MutateService.CreateMarker(container, prefix),
  deleteMarker: (container, prefix) => MutateService.DeleteMarker(container, prefix),
  auditRecent: async (limit) => (await AuditService.Recent(limit)) ?? [],
  planCopy: async (req) => {
    const dto = await TransferService.PlanCopy(req);
    if (!dto) throw new Error("empty plan");
    return dto;
  },
  startCopy: (req) => TransferService.StartCopy(req),
  startMirror: (req) => TransferService.StartMirror(req),
  cancelJob: (jobId) => TransferService.CancelJob(jobId),
  jobs: async (bookmarkId, activeOnly, limit) =>
    (await JobService.List(bookmarkId, activeOnly, limit)) ?? [],
  pickUploadFiles: async () => (await TransferService.PickUploadFiles()) ?? [],
  pickDownloadDir: () => TransferService.PickDownloadDir(),
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
  startFind: (container, prefix, query) => FindService.StartFind(container, prefix, query),
  findStatus: async (id) => {
    const status = await FindService.FindStatus(id);
    if (!status) throw new Error("scan vanished");
    return status;
  },
  cancelFind: (id) => FindService.CancelFind(id),
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
