// The frontend's single doorway to Go (§55): everything the UI can ask
// for is this interface. Components and state never import bindings
// directly — the seam is what lets navigation logic be tested against a
// fixture-backed fake, and where nullable wire shapes are normalised.

import {
  AppService,
  BookmarkService,
  BrowseService,
  InspectorService,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";
import type {
  AccountDTO,
  BookmarkDTO,
  ContainerRowDTO,
  Info,
  ListingRowDTO,
  ObjectDetailDTO,
  PrefixDetailDTO,
} from "../../bindings/github.com/csmart/aeronautes/internal/app";

export type {
  AccountDTO,
  BookmarkDTO,
  ContainerRowDTO,
  Info,
  ListingRowDTO,
  ObjectDetailDTO,
  PrefixDetailDTO,
};

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
};
