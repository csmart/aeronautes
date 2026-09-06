package app

import (
	"context"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestEngineCannotMixSessionCredentialsWithEditedTrust(t *testing.T) {
	for _, kind := range []string{"copy", "local"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			h.connect(t)
			id := h.transfer.connections.ActiveBookmarkID()
			session, err := h.transfer.connections.SessionFor(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			bookmarks, err := h.bookmarks.List(ctx)
			if err != nil || len(bookmarks) != 1 {
				t.Fatalf("bookmarks: count=%d err=%v", len(bookmarks), err)
			}
			edited := bookmarks[0]
			edited.TLSMode = "insecure"
			if _, err := h.bookmarks.Save(ctx, edited, "testing"); err != nil {
				t.Fatal(err)
			}
			var source transfercopy.RemoteSource
			h.transfer.setEngineFactory(func(s transfercopy.RemoteSource, _ rclone.Options) (transfercopy.Engine, error) {
				source = s
				return &httpEngine{}, nil
			})
			if kind == "copy" {
				_, err = h.transfer.buildEngine(ctx, CopyRequestDTO{SourceBookmarkID: id, DestBookmarkID: id}, session, session)
			} else {
				_, err = h.transfer.localEngine(ctx, session, "aeronautes_dst", TransferSettingsDTO{})
			}
			if err != nil {
				t.Fatal(err)
			}
			if source == nil {
				t.Fatal("engine did not receive a remote source")
			}
			remotes, err := source(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, remote := range remotes {
				if remote.InsecureTLS || remote.CACertFile != "" {
					t.Fatal("engine mixed the original session with edited bookmark trust")
				}
			}
		})
	}
}
