package local

import (
	"fmt"
	"strings"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// downloadSourceIdentity is the object the Start preflight named, used
// to refuse a same-size replacement before a temporary exists.
type downloadSourceIdentity struct {
	captured bool
	fromHEAD bool
	class    swiftcontrol.ObjectClass
	length   int64
	etag     string
	manifest string
	sloETag  string
}

func trimDownloadETag(etag string) string { return strings.Trim(etag, `"`) }

func listingSourceIdentity(object swiftcontrol.ObjectEntry) downloadSourceIdentity {
	id := downloadSourceIdentity{captured: true, length: object.Bytes, etag: trimDownloadETag(object.ETag)}
	if object.SLOListed {
		id.class = swiftcontrol.ClassSLO
		id.sloETag = trimDownloadETag(object.SLOETag)
	}
	return id
}

func headSourceIdentity(detail *swiftcontrol.ObjectDetail) downloadSourceIdentity {
	if detail == nil {
		return downloadSourceIdentity{}
	}
	return downloadSourceIdentity{
		captured: true, fromHEAD: true, class: detail.Class, length: detail.ContentLength,
		etag: trimDownloadETag(detail.ETag), manifest: detail.Manifest,
	}
}

func (id downloadSourceIdentity) match(detail *swiftcontrol.ObjectDetail) error {
	if !id.captured {
		return nil
	}
	if detail == nil {
		return fmt.Errorf("source HEAD was missing")
	}
	if detail.ContentLength != id.length {
		return fmt.Errorf("source length %d does not match the planned %d", detail.ContentLength, id.length)
	}
	live := trimDownloadETag(detail.ETag)
	if id.fromHEAD {
		if detail.Class != id.class || live != id.etag || detail.Manifest != id.manifest {
			return fmt.Errorf("source identity changed since the transfer set was built")
		}
		return nil
	}
	if id.class == swiftcontrol.ClassSLO {
		if detail.Class != swiftcontrol.ClassSLO {
			return fmt.Errorf("source class changed since the transfer set was built")
		}
		if id.sloETag != "" && live != id.sloETag {
			return fmt.Errorf("source identity changed since the transfer set was built")
		}
		return nil
	}
	if detail.Class != swiftcontrol.ClassPlain {
		return fmt.Errorf("source class changed since the transfer set was built")
	}
	if live != id.etag {
		return fmt.Errorf("source identity changed since the transfer set was built")
	}
	return nil
}
