package app

import (
	"context"
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/bookmarks"
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
			list, err := h.bookmarks.List(ctx)
			if err != nil || len(list) != 1 {
				t.Fatalf("bookmarks: count=%d err=%v", len(list), err)
			}
			var source transfercopy.RemoteSource
			factoryCalls := 0
			h.transfer.setEngineFactory(func(s transfercopy.RemoteSource, _ rclone.Options) (transfercopy.Engine, error) {
				factoryCalls++
				source = s
				return &httpEngine{}, nil
			})
			build := func() error {
				var err error
				if kind == "copy" {
					_, err = h.transfer.buildEngine(ctx, CopyRequestDTO{SourceBookmarkID: id, DestBookmarkID: id}, session, session)
				} else {
					_, err = h.transfer.localEngine(ctx, session, "aeronautes_dst", TransferSettingsDTO{})
				}
				return err
			}
			if err := build(); err != nil {
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
					t.Fatal("engine did not capture the original session trust")
				}
			}
			edited := list[0]
			edited.TLSMode = "insecure"
			if _, err := h.bookmarks.Save(ctx, edited, "testing"); err != nil {
				t.Fatal(err)
			}
			if stale, err := source(ctx); !errors.Is(err, bookmarks.ErrChanged) || len(stale) != 0 {
				t.Fatalf("captured engine source retained obsolete authority: count=%d, err=%v", len(stale), err)
			}
			if err := build(); !errors.Is(err, bookmarks.ErrChanged) || factoryCalls != 1 {
				t.Fatalf("obsolete session reached engine setup: calls=%d, err=%v", factoryCalls, err)
			}
			session, err = h.transfer.connections.SessionFor(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if err := build(); err != nil {
				t.Fatal(err)
			}
			fresh, err := source(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, remote := range fresh {
				if !remote.InsecureTLS || remote.CACertFile != "" {
					t.Fatal("fresh engine did not use the new session trust")
				}
			}
			for _, remote := range remotes {
				if remote.AdmitRequest == nil || !errors.Is(remote.AdmitRequest(ctx), bookmarks.ErrChanged) {
					t.Fatal("captured remote lost its generation admission check")
				}
				if remote.InsecureTLS || remote.CACertFile != "" {
					t.Fatal("editing trust changed a previously captured remote")
				}
			}
		})
	}
}
