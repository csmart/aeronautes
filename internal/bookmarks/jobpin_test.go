package bookmarks

import (
	"context"
	"errors"
	"testing"
)

func TestJobPinProtectsBindingUntilLastRelease(t *testing.T) {
	st, _, _ := newTestStore(t)
	b, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	before := storedRecord(t, st, b.ID)
	first, err := st.PinGeneration(t.Context(), b.ID, before.generation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first)
	second, err := st.PinGeneration(t.Context(), b.ID, before.generation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second)
	if st.leases[before.ref] != 2 {
		t.Fatal("job pins are missing from the live reference set")
	}
	cosmetic := b
	cosmetic.DisplayName = "renamed while pinned"
	if _, err := st.Save(t.Context(), cosmetic, nil); err != nil {
		t.Fatal(err)
	}
	for _, retarget := range []bool{false, true} {
		proposed := cosmetic
		if retarget {
			proposed.AuthURL = "https://other.example/auth/v1.0"
		}
		if _, err := st.Save(t.Context(), proposed, []byte("replacement")); !errors.Is(err, ErrInUse) {
			t.Fatalf("pinned replacement = %v; want in-use refusal", err)
		}
	}
	if work := pendingWork(t, st); len(work) != 0 {
		t.Fatal("refused replacement prepared an external write")
	}
	first()
	first()
	if st.leases[before.ref] != 1 || st.jobPins[b.ID] != 1 {
		t.Fatal("duplicate release removed another job's pin")
	}
	if err := st.Delete(t.Context(), b.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("last pin did not block deletion: %v", err)
	}
	second()
	second()
	if len(st.leases) != 0 || len(st.jobPins) != 0 {
		t.Fatal("finished jobs retained live-reference pins")
	}
	if _, err := st.Save(t.Context(), cosmetic, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if release, err := st.PinGeneration(t.Context(), b.ID, before.generation); release != nil || !errors.Is(err, ErrChanged) {
		t.Fatalf("obsolete generation obtained a pin: %v", err)
	}
	if err := st.Delete(t.Context(), b.ID); err != nil {
		t.Fatal(err)
	}
}

func TestJobPinRefusesUnusableGeneration(t *testing.T) {
	for _, condition := range []string{"cancelled", "deleted", "unresolved", "no-key"} {
		t.Run(condition, func(t *testing.T) {
			st, d, _ := newTestStore(t)
			key := []byte("key")
			if condition == "no-key" {
				key = nil
			}
			b, err := st.Save(t.Context(), sample(), key)
			if err != nil {
				t.Fatal(err)
			}
			generation := storedRecord(t, st, b.ID).generation
			ctx := t.Context()
			switch condition {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "deleted":
				if err := st.Delete(ctx, b.ID); err != nil {
					t.Fatal(err)
				}
			case "unresolved":
				st.unresolved[b.ID] = true
				st.db = &lostCommitReply{DB: d, failRead: true, closed: unavailablePublicationReader(t)}
			}
			if release, err := st.PinGeneration(ctx, b.ID, generation); release != nil || err == nil {
				t.Fatalf("unusable generation obtained a pin: %v", err)
			}
			if len(st.leases) != 0 || len(st.jobPins) != 0 {
				t.Fatal("failed pin acquisition retained authority")
			}
		})
	}
}

func TestAdmittedJobRefusesDelayedCredentialPublication(t *testing.T) {
	st, _, _ := newTestStore(t)
	b, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	before := storedRecord(t, st, b.ID)
	backend := st.secrets
	entered := make(chan string, 1)
	gate, releasePut := publicationGate(t)
	st.secrets = hookedSecrets{SecretStore: backend, put: func(ctx context.Context, ref string, value []byte) error {
		entered <- ref
		<-gate
		return backend.Put(ctx, ref, value)
	}}
	result := make(chan error, 1)
	go func() {
		_, err := st.Save(t.Context(), b, []byte("candidate"))
		result <- err
	}()
	candidate := awaitPublication(t, entered)
	unpin, err := st.PinGeneration(t.Context(), b.ID, before.generation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unpin)
	releasePut()
	if err := awaitPublication(t, result); !errors.Is(err, ErrInUse) {
		t.Fatalf("save replaced a newly admitted job's generation: %v", err)
	}
	if got := storedRecord(t, st, b.ID); !samePublication(before, got) {
		t.Fatal("refused publication changed the job's binding")
	}
	if key, err := backend.Get(t.Context(), before.ref); err != nil || string(key) != "original" {
		t.Fatal("refused publication lost the current key")
	}
	if key, err := backend.Get(t.Context(), candidate); err != nil || string(key) != "candidate" {
		t.Fatal("prepared candidate was removed despite an uncertain lifetime")
	}
	if work := pendingWork(t, st); len(work) != 1 || work[0].state != "prepared" || work[0].ref != candidate {
		t.Fatal("refused publication lost its candidate journal")
	}
	unpin()
	if _, err := st.Save(t.Context(), b, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
}
