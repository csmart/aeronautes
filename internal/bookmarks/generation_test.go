package bookmarks

import (
	"context"
	"errors"
	"testing"
)

func TestGenerationGuardCoordinatesCacheUse(t *testing.T) {
	for _, change := range []string{"none", "cosmetic", "replace", "delete", "cancel", "unavailable", "unresolved"} {
		t.Run(change, func(t *testing.T) {
			st, d, _ := newTestStore(t)
			b, err := st.Save(t.Context(), sample(), []byte("key"))
			if err != nil {
				t.Fatal(err)
			}
			_, generation, err := st.ConnectionConfig(t.Context(), b.ID)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			var wantErr error
			switch change {
			case "cosmetic":
				b.DisplayName = "renamed"
				_, err = st.Save(ctx, b, nil)
			case "replace":
				_, err = st.Save(ctx, b, []byte("replacement"))
				wantErr = ErrChanged
			case "delete":
				err = st.Delete(ctx, b.ID)
				wantErr = ErrNotFound
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				wantErr = context.Canceled
			case "unavailable", "unresolved":
				st.db = &lostCommitReply{DB: d, failRead: true, closed: unavailablePublicationReader(t)}
				if change == "unresolved" {
					st.unresolved[b.ID] = true
					wantErr = ErrPublicationUnresolved
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = st.WithCurrentGeneration(ctx, b.ID, generation, func() error {
				called = true
				// Publishing while this callback uses the checked generation
				// would reopen the check/install race even with correct tags.
				if st.mu.TryLock() {
					st.mu.Unlock()
					t.Error("cache use is not coordinated with publication")
				}
				return nil
			})
			if change == "unavailable" {
				if err == nil || called {
					t.Fatalf("unavailable generation allowed cache use: %v, called=%v", err, called)
				}
			} else if !errors.Is(err, wantErr) || called != (wantErr == nil) {
				t.Fatalf("generation guard = %v, called=%v; want %v", err, called, wantErr)
			}
			if change == "unresolved" {
				if !st.unresolved[b.ID] {
					t.Fatal("unreadable generation cleared the unresolved outcome")
				}
				st.db = d
				if err := st.WithCurrentGeneration(t.Context(), b.ID, generation, nil); err != nil || st.unresolved[b.ID] {
					t.Fatalf("known generation failed to restore availability: %v", err)
				}
			}
		})
	}
}
