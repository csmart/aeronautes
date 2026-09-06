package app

// Bridge DTOs (§55, ADR 0011): everything that crosses to the webview is
// declared here, so "what can the frontend see" is one reviewable file.
// No secret, token, or credential field may ever appear — bookmarks are
// summarised with HasCredential only, and a credential crosses one way
// (frontend → Go) as a Save argument.

// BookmarkDTO is the frontend's view of a saved endpoint.
type BookmarkDTO struct {
	ID                 string `json:"id"`
	DisplayName        string `json:"displayName"`
	AuthURL            string `json:"authUrl"`
	Username           string `json:"username"`
	StorageURLOverride string `json:"storageUrlOverride"`
	// TLSMode is "system", "custom_ca", or "insecure".
	TLSMode string `json:"tlsMode"`
	// CAPem carries a custom CA bundle one way (frontend → Go) on save;
	// reads report HasCA only. Certificates are public material — the
	// one-way shape just keeps DTOs small and the update semantics
	// explicit ("" keeps the stored bundle).
	CAPem         string `json:"caPem"`
	HasCA         bool   `json:"hasCa"`
	HasCredential bool   `json:"hasCredential"`
}

// Counts disclose retained work without exposing credential references or
// backend-instance handles through the bridge.
type CredentialStatusDTO struct {
	Retired    int64 `json:"retired"`
	Unfinished int64 `json:"unfinished"`
	Saving     int64 `json:"saving"`
}

// AccountDTO is the connected account's portrait (§53).
type AccountDTO struct {
	// Name is the storage-URL account suffix (e.g. AUTH_demo).
	Name            string            `json:"name"`
	Containers      int64             `json:"containers"`
	Objects         int64             `json:"objects"`
	BytesUsed       int64             `json:"bytesUsed"`
	QuotaBytes      int64             `json:"quotaBytes"`
	Meta            map[string]string `json:"meta"`
	TempURLKeySlots []string          `json:"tempUrlKeySlots"`
}

// ContainerRowDTO is one container listing row. Class is the §24
// backstage classification driving the §52 modes: normal | suspected |
// declared | confirmed.
type ContainerRowDTO struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
	Bytes int64  `json:"bytes"`
	Class string `json:"class"`
}

// ContainerDetailDTO is the container inspector's §53 section.
type ContainerDetailDTO struct {
	Name          string `json:"name"`
	ObjectCount   int64  `json:"objectCount"`
	BytesUsed     int64  `json:"bytesUsed"`
	StoragePolicy string `json:"storagePolicy"`
	ReadACL       string `json:"readAcl"`
	WriteACL      string `json:"writeAcl"`
	// VersioningMode: off | versions_location | history_location (§21
	// display profile).
	VersioningMode    string            `json:"versioningMode"`
	VersioningArchive string            `json:"versioningArchive"`
	QuotaBytes        int64             `json:"quotaBytes"`
	QuotaCount        int64             `json:"quotaCount"`
	CORS              bool              `json:"cors"`
	StaticWeb         bool              `json:"staticWeb"`
	TempURLKeySlots   []string          `json:"tempUrlKeySlots"`
	Meta              map[string]string `json:"meta"`
	RawHeaders        map[string]string `json:"rawHeaders"`
	TransID           string            `json:"transId"`
	// EditableFingerprint is the working-copy base for §18 conflict
	// detection: hand it back with the edit.
	EditableFingerprint string `json:"editableFingerprint"`
	// Class is the §24 classification (with this HEAD folded in).
	Class string `json:"class"`
}

// SLOSegmentDTO is one row of the inspector's SLO segment table (§23.2):
// kind "object" | "data" | "unknown".
type SLOSegmentDTO struct {
	Kind      string `json:"kind"`
	Container string `json:"container"`
	Object    string `json:"object"`
	ETag      string `json:"etag"`
	Size      int64  `json:"size"`
	// Range is the raw inclusive byte range ("10-109"), "" for whole.
	Range     string `json:"range"`
	NestedSLO bool   `json:"nestedSlo"`
	// DataSize is the decoded inline-data length for kind "data".
	DataSize int64 `json:"dataSize"`
}

// SLOManifestDTO is the exact manifest, order preserved (§23).
type SLOManifestDTO struct {
	Entries []SLOSegmentDTO `json:"entries"`
}

// DLOSegmentsDTO is the bounded §23.1 segment preview: the first rows a
// DLO manifest would assemble, never all of them.
type DLOSegmentsDTO struct {
	Container    string          `json:"container"`
	Prefix       string          `json:"prefix"`
	Segments     []ListingRowDTO `json:"segments"`
	HasMore      bool            `json:"hasMore"`
	PreviewBytes int64           `json:"previewBytes"`
}

// ContainerPageDTO is one page of containers; an empty NextMarker means
// the listing is exhausted.
type ContainerPageDTO struct {
	Rows       []ContainerRowDTO `json:"rows"`
	NextMarker string            `json:"nextMarker"`
}

// ListingRowDTO is one working-list row: kind "object" or "prefix".
// Prefix rows use Name for the prefix (with trailing delimiter); whether
// a marker object backs a prefix is not knowable from a listing (§14.1)
// — InspectorService.Prefix answers on selection.
type ListingRowDTO struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Bytes        int64  `json:"bytes"`
	ETag         string `json:"etag"`
	LastModified string `json:"lastModified"`
	ContentType  string `json:"contentType"`
	SLOListed    bool   `json:"sloListed"`
}

// ObjectPageDTO is one page of the working list; an empty NextMarker
// means the listing is exhausted.
type ObjectPageDTO struct {
	Rows       []ListingRowDTO `json:"rows"`
	NextMarker string          `json:"nextMarker"`
}

// StoragePolicyDTO is one advertised storage policy (§20).
type StoragePolicyDTO struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

// CapabilitiesDTO is the connect-time capability snapshot (§10) the UI
// gates on. Source "advertised" means /info answered; "unknown" means it
// could not be read — and Unknown is never treated as Unsupported.
type CapabilitiesDTO struct {
	Source          string             `json:"source"`
	MaxFileSize     int64              `json:"maxFileSize"`
	StoragePolicies []StoragePolicyDTO `json:"storagePolicies"`
	// VersioningFlags is versioned_writes.allowed_flags (§21).
	VersioningFlags []string `json:"versioningFlags"`
	SLOSupported    bool     `json:"sloSupported"`
}

// ObjectMetadataEditDTO states the COMPLETE intended editable outcome
// for an object (§17: replace-all semantics — a working copy, never a
// delta).
type ObjectMetadataEditDTO struct {
	Meta        map[string]string `json:"meta"`
	ContentType string            `json:"contentType"`
	// DeleteAt RFC3339, "" for no scheduled expiry.
	DeleteAt string `json:"deleteAt"`
}

// ContainerEditDTO states the complete intended editable surface of a
// container (§18, §19, §21).
type ContainerEditDTO struct {
	Meta              map[string]string `json:"meta"`
	ReadACL           string            `json:"readAcl"`
	WriteACL          string            `json:"writeAcl"`
	VersioningMode    string            `json:"versioningMode"` // off | versions_location | history_location
	VersioningArchive string            `json:"versioningArchive"`
	QuotaBytes        int64             `json:"quotaBytes"`
	QuotaCount        int64             `json:"quotaCount"`
	// AckPublic must be true to set a world-readable (.r:*) read ACL
	// — the operator explicitly acknowledged public exposure (§19.1).
	AckPublic bool `json:"ackPublic"`
}

// ContainerCreateDTO describes a new container (§20).
type ContainerCreateDTO struct {
	Name   string            `json:"name"`
	Policy string            `json:"policy"`
	Meta   map[string]string `json:"meta"`
}

// MutationResultDTO reports one mutation: outcome applied | conflict |
// verify_mismatch, the mismatch description when relevant, the refreshed
// detail, and the transaction ids for the audit trail.
type MutationResultDTO struct {
	Outcome  string   `json:"outcome"`
	Mismatch string   `json:"mismatch"`
	TransIDs []string `json:"transIds"`
	// ObjectAfter / ContainerAfter carry the verified post-write state
	// for whichever resource was edited (nil otherwise, and nil on
	// conflict).
	ObjectAfter    *ObjectDetailDTO    `json:"objectAfter"`
	ContainerAfter *ContainerDetailDTO `json:"containerAfter"`
}

// AuditEventDTO is one §50 audit-trail row.
type AuditEventDTO struct {
	ID        int64    `json:"id"`
	At        string   `json:"at"`
	Operation string   `json:"operation"`
	Resource  string   `json:"resource"`
	Summary   string   `json:"summary"`
	Result    string   `json:"result"`
	TransIDs  []string `json:"transIds"`
}

// PrefixDetailDTO is the selection-triggered prefix classification
// (§14.1): marker-backed vs implied, and any real object sitting at the
// prefix name that the delimiter view folded away.
type PrefixDetailDTO struct {
	Prefix       string `json:"prefix"`
	MarkerBacked bool   `json:"markerBacked"`
	// SelfObject is nil for an implied prefix.
	SelfObject *ObjectDetailDTO `json:"selfObject"`
}

// ObjectDetailDTO is the authoritative HEAD detail (§53).
type ObjectDetailDTO struct {
	Name          string `json:"name"`
	Class         string `json:"class"` // plain | dlo | slo
	ContentLength int64  `json:"contentLength"`
	ContentType   string `json:"contentType"`
	ETag          string `json:"etag"`
	LastModified  string `json:"lastModified"`
	Manifest      string `json:"manifest"`
	// FollowedSymlink reports the HEAD chased a Swift symlink — the
	// inspector must show a symlink, not a plain object, and the editor
	// refuses (§17.1, F-05).
	FollowedSymlink bool              `json:"followedSymlink"`
	SymlinkTarget   string            `json:"symlinkTarget"`
	DeleteAt        string            `json:"deleteAt"`
	Meta            map[string]string `json:"meta"`
	RawHeaders      map[string]string `json:"rawHeaders"`
	TransID         string            `json:"transId"`
	// EditableFingerprint is the working-copy base for §18 conflict
	// detection: hand it back with the edit.
	EditableFingerprint string `json:"editableFingerprint"`
}
