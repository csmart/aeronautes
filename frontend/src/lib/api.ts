// The frontend's single doorway to Go (§55): everything the UI can ask
// for is this interface. Components and state never import bindings
// directly — the seam is what lets navigation logic be tested against a
// fixture-backed fake, and where nullable wire shapes are normalised.

import {
  AppService,
  AuditService,
  BackupService,
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
  AccountEditDTO,
  TempURLKeyEditDTO,
  AuditEventDTO,
  BookmarkDTO,
  CredentialStatusDTO,
  VerifyResultDTO,
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
  InterruptedDisclosuresDTO,
  InterruptedDisclosureDTO,
  DismissInterruptedDTO,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";

export type {
  AccountDTO,
  AccountEditDTO,
  TempURLKeyEditDTO,
  PickedDirDTO,
  PickedFilesDTO,
  SelectionScopeDTO,
  AuditEventDTO,
  BookmarkDTO,
  CredentialStatusDTO,
  VerifyResultDTO,
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
  InterruptedDisclosuresDTO,
  InterruptedDisclosureDTO,
  DismissInterruptedDTO,
};
export type { DownloadPreflightView, UploadPreflight };
export type {
  DeletePreviewDTO,
  DeleteRequestDTO,
  DownloadRequestDTO,
  About,
  AppSettingsDTO,
  AuditQueryDTO,
  ExportResultDTO,
  BackupResultDTO,
  RestoreResultDTO,
  BenchmarkRequestDTO,
  BenchmarkResultDTO,
  FindStatusDTO,
  MirrorRequestDTO,
  SegmentOwnershipDTO,
  SegmentsDeleteRequestDTO,
  SelectedDeleteRequestDTO,
  TransferSettingsDTO,
  UploadRequestDTO,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";
import type {
  About,
  AppSettingsDTO,
  AuditQueryDTO,
  ExportResultDTO,
  BackupResultDTO,
  RestoreResultDTO,
  BenchmarkRequestDTO,
  BenchmarkResultDTO,
  DeletePreviewDTO,
  DeleteRequestDTO,
  DownloadRequestDTO,
  FindStatusDTO,
  MirrorRequestDTO,
  SegmentOwnershipDTO,
  SegmentsDeleteRequestDTO,
  SelectedDeleteRequestDTO,
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
  /** The page size the bridge applied, which is less than the ask when
   * the ask exceeded its clamp (ADR 0074 decision 1). */
  effectiveLimit: number;
  scope: ListingScope;
}

export interface ObjectPage {
  rows: ListingRowDTO[];
  nextMarker: string;
  effectiveLimit: number;
  scope: ListingScope;
}

export interface Api {
  info(): Promise<Info>;
  about(): Promise<About>;
  listBookmarks(): Promise<BookmarkDTO[]>;
  credentialStatus(): Promise<CredentialStatusDTO>;
  saveBookmark(bookmark: BookmarkDTO, secret: string): Promise<BookmarkDTO>;
  /** Authenticates the details as typed without saving them; the secret
   * crosses one way for that exchange, as on save. */
  verifyBookmark(bookmark: BookmarkDTO, secret: string): Promise<VerifyResultDTO>;
  deleteBookmark(id: string): Promise<void>;
  connect(bookmarkId: string): Promise<AccountDTO>;
  /** Re-read the connected account's portrait under the current scope
   * (ADR 0059), to refresh the working copy after a conflict. */
  account(bookmarkId: string): Promise<AccountDTO>;
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
  /** Deletes ONE manifest the operator was shown, manifest-only, under the
   * scope its detail was read under; contentFingerprint is that detail's
   * identity and the delete is refused when the name no longer holds it. A
   * manifest of the same kind still observed afterwards is unverified, and
   * any other successor is reported (ADR 0094). */
  deleteManifest(scope: ListingScope, container: string, name: string, contentFingerprint: string): Promise<MutationResultDTO>;
  /** Deletes ONE plain object the operator was shown, under the scope its
   * detail was read under; contentFingerprint is that detail's identity and
   * the delete is refused when the name no longer holds it. The result
   * carries the literal successor the verifying read observed, when any
   * (ADR 0092). */
  deleteObject(scope: ListingScope, container: string, name: string, contentFingerprint: string): Promise<MutationResultDTO>;
  capabilities(bookmarkId: string): Promise<CapabilitiesDTO>;
  updateObjectMetadata(
    scope: ListingScope,
    container: string,
    name: string,
    baseFingerprint: string,
    edit: ObjectMetadataEditDTO,
  ): Promise<MutationResultDTO>;
  updateContainer(
    scope: ListingScope,
    name: string,
    baseFingerprint: string,
    edit: ContainerEditDTO,
  ): Promise<MutationResultDTO>;
  updateAccountMetadata(
    scope: ListingScope,
    baseFingerprint: string,
    edit: AccountEditDTO,
  ): Promise<MutationResultDTO>;
  /** Set or clear one TempURL key slot of the account (no container) or of
   * a container; the secret crosses once and is recorded nowhere (ADR 0101). */
  updateTempUrlKey(scope: ListingScope, edit: TempURLKeyEditDTO): Promise<MutationResultDTO>;
  createContainer(scope: ListingScope, create: ContainerCreateDTO): Promise<ContainerDetailDTO>;
  createMarker(scope: ListingScope, container: string, prefix: string): Promise<PrefixDetailDTO>;
  /** Deletes ONE directory marker the operator was shown, the prefix
   * detail's own object, under the scope that detail was read under;
   * contentFingerprint is that object's identity, and the marker rule is
   * required beside it (ADR 0094). The objects under the prefix are untouched. */
  deleteMarker(scope: ListingScope, container: string, prefix: string, contentFingerprint: string): Promise<MutationResultDTO>;
  auditRecent(limit: number, bookmarkId: string): Promise<AuditEventDTO[]>;
  auditSearch(q: AuditQueryDTO): Promise<AuditEventDTO[]>;
  /** Archive the trail under these filters to a file the operator chooses
   * (§51); null when the operator cancels the dialog. */
  exportAudit(q: AuditQueryDTO): Promise<ExportResultDTO | null>;
  /** Write the operator's configuration (settings and bookmarks, no
   * credentials) to a file they choose (ADR 0098); null when cancelled. */
  exportSettings(): Promise<BackupResultDTO | null>;
  /** Restore settings and bookmarks from a backup file the operator chooses (ADR 0098); credentials are never restored, so passwords are re-entered on use; null when cancelled. */
  importSettings(): Promise<RestoreResultDTO | null>;
  planCopy(req: CopyRequestDTO): Promise<CopyPlanDTO>;
  startCopy(req: CopyRequestDTO): Promise<string>;
  startMirror(req: MirrorRequestDTO): Promise<string>;
  cancelJob(jobId: string): Promise<void>;
  /** Ask an upload, a download or a copy to pause at its next unit boundary (ADR 0104); the unit in hand finishes and nothing is killed. */
  pauseJob(jobId: string): Promise<void>;
  /** Withdraw a pause (ADR 0104): a job that has not reached its boundary carries on, and a paused one waits for a job slot. */
  resumeJob(jobId: string): Promise<void>;
  rerunJob(jobId: string): Promise<string>;
  jobs(bookmarkId: string, activeOnly: boolean, limit: number): Promise<JobDTO[]>;
  job(jobId: string, bookmarkId: string): Promise<JobDTO | null>;
  jobHistory(bookmarkId: string, cursor: string, limit: number): Promise<JobHistoryDTO>;
  jobItems(jobId: string, cursor: string, limit: number, bookmarkId: string): Promise<JobItemPageDTO>;
  jobItemTotals(jobId: string, bookmarkId: string): Promise<JobItemTotalsDTO>;
  recoveryStatus(): Promise<RecoveryStatusDTO>;
  interruptedDisclosures(limit: number): Promise<InterruptedDisclosuresDTO>;
  dismissInterrupted(jobIds: string[]): Promise<DismissInterruptedDTO>;
  pickUploadFiles(scope: SelectionScopeDTO): Promise<PickedFilesDTO | null>;
  /** Pick one folder for upload (ADR 0103): its files arrive under the folder's
   * own name, with the walk's counts beside the names; no token when it holds
   * no regular file. */
  pickUploadDirectory(scope: SelectionScopeDTO): Promise<PickedFilesDTO | null>;
  pickDownloadDir(scope: SelectionScopeDTO): Promise<PickedDirDTO | null>;
  preflightUpload(req: UploadRequestDTO): Promise<UploadPreflight>;
  startUpload(req: UploadRequestDTO): Promise<string>;
  preflightDownload(req: DownloadRequestDTO): Promise<DownloadPreflightView>;
  startDownload(req: DownloadRequestDTO): Promise<string>;
  previewDelete(req: DeleteRequestDTO): Promise<DeletePreviewDTO>;
  startDelete(req: DeleteRequestDTO): Promise<string>;
  /** Delete the selected objects as one job (ADR 0093): the members are the
   * listing rows as shown and the confirmation is their count, typed. */
  startSelectedDelete(req: SelectedDeleteRequestDTO): Promise<string>;
  startBenchmark(req: BenchmarkRequestDTO): Promise<string>;
  latestBenchmark(bookmarkId: string): Promise<BenchmarkResultDTO | null>;
  appSettings(): Promise<AppSettingsDTO>;
  saveAppSettings(settings: AppSettingsDTO): Promise<AppSettingsDTO>;
  /** countOnly lists the scope and counts it without keeping matches (ADR 0072). */
  startFind(container: string, prefix: string, query: string, bookmarkId: string, countOnly?: boolean): Promise<string>;
  findStatus(id: string, bookmarkId: string): Promise<FindStatusDTO>;
  cancelFind(id: string, bookmarkId: string): Promise<void>;
  checkSegmentOwnership(req: SegmentsDeleteRequestDTO): Promise<SegmentOwnershipDTO>;
  deleteManifestWithSegments(req: SegmentsDeleteRequestDTO): Promise<string>;
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
  verifyBookmark: async (bookmark, secret) => {
    const result = await BookmarkService.Verify(bookmark, secret);
    if (!result) throw new Error("empty verify result");
    return result;
  },
  deleteBookmark: (id) => BookmarkService.Delete(id),
  connect: (bookmarkId) => BrowseService.Connect(bookmarkId),
  account: (bookmarkId) => BrowseService.Account(bookmarkId),
  containers: async (bookmarkId, marker, limit) => {
    const page = await BrowseService.Containers(bookmarkId, marker, limit);
    return { rows: page.rows ?? [], nextMarker: page.nextMarker, effectiveLimit: page.effectiveLimit, scope: page.scope };
  },
  objects: async (bookmarkId, container, prefix, marker, limit) => {
    const page = await BrowseService.Objects(bookmarkId, container, prefix, marker, limit);
    return { rows: page.rows ?? [], nextMarker: page.nextMarker, effectiveLimit: page.effectiveLimit, scope: page.scope };
  },
  objectDetail: (container, name, bookmarkId) => InspectorService.Object(bookmarkId, container, name),
  prefixDetail: (container, prefix, bookmarkId) => InspectorService.Prefix(bookmarkId, container, prefix),
  containerDetail: (name, bookmarkId) => InspectorService.Container(bookmarkId, name),
  sloManifest: (container, name, bookmarkId) => InspectorService.SLOManifest(bookmarkId, container, name),
  dloSegments: (manifest, bookmarkId) => InspectorService.DLOSegments(bookmarkId, manifest),
  deleteManifest: (scope, container, name, contentFingerprint) =>
    MutateService.DeleteManifest(scope, container, name, contentFingerprint),
  deleteObject: (scope, container, name, contentFingerprint) =>
    MutateService.DeleteObject(scope, container, name, contentFingerprint),
  capabilities: (bookmarkId) => BrowseService.Capabilities(bookmarkId),
  updateObjectMetadata: (scope, container, name, baseFingerprint, edit) =>
    MutateService.UpdateObjectMetadata(scope, container, name, baseFingerprint, edit),
  updateContainer: (scope, name, baseFingerprint, edit) =>
    MutateService.UpdateContainer(scope, name, baseFingerprint, edit),
  updateAccountMetadata: (scope, baseFingerprint, edit) =>
    MutateService.UpdateAccountMetadata(scope, baseFingerprint, edit),
  updateTempUrlKey: (scope, edit) => MutateService.UpdateTempURLKey(scope, edit),
  createContainer: (scope, create) => MutateService.CreateContainer(scope, create),
  createMarker: (scope, container, prefix) => MutateService.CreateMarker(scope, container, prefix),
  deleteMarker: (scope, container, prefix, contentFingerprint) =>
    MutateService.DeleteMarker(scope, container, prefix, contentFingerprint),
  auditRecent: async (limit, bookmarkId) =>
    (await AuditService.RecentFor(bookmarkId, limit)) ?? [],
  auditSearch: async (q) => (await AuditService.Search(q)) ?? [],
  exportAudit: (q) => AuditService.ExportAudit(q),
  exportSettings: () => BackupService.ExportSettings(),
  importSettings: () => BackupService.ImportSettings(),
  planCopy: async (req) => {
    const dto = await TransferService.PlanCopy(req);
    if (!dto) throw new Error("empty plan");
    return dto;
  },
  startCopy: (req) => TransferService.StartCopy(req),
  startMirror: (req) => TransferService.StartMirror(req),
  cancelJob: (jobId) => TransferService.CancelJob(jobId),
  pauseJob: (jobId) => TransferService.PauseJob(jobId),
  resumeJob: (jobId) => TransferService.ResumeJob(jobId),
  rerunJob: (jobId) => TransferService.RerunJob(jobId),
  jobs: async (bookmarkId, activeOnly, limit) =>
    (await JobService.List(bookmarkId, activeOnly, limit)) ?? [],
  recoveryStatus: () => JobService.RecoveryStatus(),
  interruptedDisclosures: (limit) => JobService.InterruptedDisclosures(limit),
  dismissInterrupted: (jobIds) => JobService.DismissInterrupted(jobIds),
  job: (jobId, bookmarkId) => JobService.Get(bookmarkId, jobId),
  jobHistory: (bookmarkId, cursor, limit) => JobService.History(bookmarkId, cursor, limit),
  jobItems: (jobId, cursor, limit, bookmarkId) => JobService.Items(bookmarkId, jobId, cursor, limit),
  jobItemTotals: (jobId, bookmarkId) => JobService.ItemTotals(bookmarkId, jobId),
  pickUploadFiles: (scope) => TransferService.PickUploadFiles(scope),
  pickUploadDirectory: (scope) => TransferService.PickUploadDirectory(scope),
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
  startSelectedDelete: (req) => DeleteService.StartSelectedDelete(req),
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
  deleteManifestWithSegments: (req) => DeleteService.DeleteManifestWithSegments(req),
};
