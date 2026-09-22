package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

func (h *Harness) InspectObject(ctx context.Context, bookmarkID, container, name string) (app.ObjectDetailDTO, *Receipt) {
	detail, err := h.Inspector.Object(ctx, bookmarkID, container, name)
	return detail, &Receipt{h: h, action: "InspectorService.Object", err: err}
}

func (h *Harness) InspectPrefix(ctx context.Context, bookmarkID, container, prefix string) (app.PrefixDetailDTO, *Receipt) {
	detail, err := h.Inspector.Prefix(ctx, bookmarkID, container, prefix)
	return detail, &Receipt{h: h, action: "InspectorService.Prefix", err: err}
}
