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
	TLSMode       string `json:"tlsMode"`
	HasCredential bool   `json:"hasCredential"`
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

// ContainerRowDTO is one container listing row.
type ContainerRowDTO struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
	Bytes int64  `json:"bytes"`
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
	Name          string            `json:"name"`
	Class         string            `json:"class"` // plain | dlo | slo
	ContentLength int64             `json:"contentLength"`
	ContentType   string            `json:"contentType"`
	ETag          string            `json:"etag"`
	LastModified  string            `json:"lastModified"`
	Manifest      string            `json:"manifest"`
	DeleteAt      string            `json:"deleteAt"`
	Meta          map[string]string `json:"meta"`
	RawHeaders    map[string]string `json:"rawHeaders"`
	TransID       string            `json:"transId"`
}
