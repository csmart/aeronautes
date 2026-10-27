import type { ObjectDetailDTO } from "./api";

/** A plain object in every representation: not a manifest and not a symlink
 * in either of the two forms a HEAD can report (followed, or carrying its
 * target without being followed). Only such an object may be deleted by the
 * single-object deletion (ADR 0092 decision 2). */
export function isLiteralPlainObject(detail: ObjectDetailDTO | null | undefined): detail is ObjectDetailDTO {
  return !!detail && detail.class === "plain" && !detail.followedSymlink && !detail.symlinkTarget;
}
