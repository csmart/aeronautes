package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// recordingPicker keeps every title the backend hands the native chooser.
type recordingPicker struct {
	mu     sync.Mutex
	dir    string
	titles []string
}

func (p *recordingPicker) PickFiles(title string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.titles = append(p.titles, title)
	return nil, nil
}

func (p *recordingPicker) PickDirectory(title string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.titles = append(p.titles, title)
	return p.dir, nil
}

// ADR 0073 option B: the chooser's title states the account, container and
// prefix the backend resolved for the pick, on a surface the page cannot
// draw, and stays legible whatever the prefix contains.
func TestPickDownloadDirTitlesTheChooserFromTheBackend(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	picker := &recordingPicker{dir: t.TempDir()}
	h.transfer.setFilePicker(picker)
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: h.bookmarkID(), Container: "fixtures", Prefix: "photos/2026\x01\n/"})
	if err != nil || picked == nil {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	if len(picker.titles) != 1 {
		t.Fatalf("titles handed to the chooser = %v", picker.titles)
	}
	title := picker.titles[0]
	account := h.browse.connections.AccountName()
	if account == "" {
		t.Fatal("the harness has no account name")
	}
	list, err := h.bookmarks.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("bookmark fixture: %+v, %v", list, err)
	}
	from := "Download from " + list[0].DisplayName + " (" + account + ")"
	for _, want := range []string{from, "fixtures/photos/2026??/"} {
		if !strings.Contains(title, want) {
			t.Errorf("title %q lacks %q", title, want)
		}
	}
	for _, r := range title {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("title carries a control character: %q", title)
		}
	}

	long := strings.Repeat("a", 300) + "/"
	if _, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: h.bookmarkID(), Container: "fixtures", Prefix: long}); err != nil {
		t.Fatal(err)
	}
	cut := picker.titles[1]
	if !strings.Contains(cut, "…") || utf8.RuneCountInString(cut) > 240 {
		t.Fatalf("a long prefix was not cut: %d runes, %q", utf8.RuneCountInString(cut), cut)
	}
	if !strings.HasPrefix(cut, from+", fixtures/aaaa") {
		t.Fatalf("the cut title lost the backend-resolved scope: %q", cut)
	}
}

// ADR 0073 option B: a world-readable read ACL is written only after the
// shell's native confirmation answered yes; the page's own acknowledgement
// stays the first gate, and a container that is already public is not
// asked again.
func TestPublicReadACLNeedsNativeConfirmation(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	h.mock.AddContainer("private", "standard")
	readACL := func() string {
		detail, err := h.inspector.Container(ctx, h.bookmarkID(), "private")
		if err != nil {
			t.Fatal(err)
		}
		return detail.ReadACL
	}
	type asked struct{ container, account string }
	var calls []asked
	answer := func(ok bool, fail error) confirmerFunc {
		return func(_ context.Context, container, account string) (bool, error) {
			calls = append(calls, asked{container, account})
			return ok, fail
		}
	}

	// The page's acknowledgement is still the first gate; the native
	// dialog is never reached without it.
	h.mutate.setConfirmer(answer(true, nil))
	if _, err := h.mutate.UpdateContainer(ctx, "private", "", ContainerEditDTO{ReadACL: ".r:*"}); err == nil || !strings.Contains(err.Error(), "set ackPublic") {
		t.Fatalf("missing page acknowledgement = %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("the native dialog was asked before the page acknowledgement: %+v", calls)
	}

	// No shell dialog: refused, nothing written.
	h.mutate.setConfirmer(nil)
	_, err := h.mutate.UpdateContainer(ctx, "private", "", ContainerEditDTO{ReadACL: ".r:*", AckPublic: true})
	if err == nil || !strings.Contains(err.Error(), "native confirmation this shell cannot show") || readACL() == ".r:*" {
		t.Fatalf("no confirmer = %v, acl %q", err, readACL())
	}

	// The operator kept it private: refused, nothing written.
	h.mutate.setConfirmer(answer(false, nil))
	_, err = h.mutate.UpdateContainer(ctx, "private", "", ContainerEditDTO{ReadACL: ".r:*", AckPublic: true})
	if err == nil || !strings.Contains(err.Error(), "was not confirmed") || readACL() == ".r:*" {
		t.Fatalf("declined confirmation = %v, acl %q", err, readACL())
	}
	if len(calls) != 1 || calls[0].container != "private" || calls[0].account != h.browse.connections.AccountName() {
		t.Fatalf("the dialog was not asked about the right container and account: %+v", calls)
	}

	// The dialog did not complete (closed, cancelled context): refused.
	h.mutate.setConfirmer(answer(false, errors.New("dialog closed")))
	_, err = h.mutate.UpdateContainer(ctx, "private", "", ContainerEditDTO{ReadACL: ".r:*", AckPublic: true})
	if err == nil || !strings.Contains(err.Error(), "did not complete") || readACL() == ".r:*" {
		t.Fatalf("failed confirmation = %v, acl %q", err, readACL())
	}

	// Confirmed: written.
	h.mutate.setConfirmer(answer(true, nil))
	result, err := h.mutate.UpdateContainer(ctx, "private", "", ContainerEditDTO{ReadACL: ".r:*", AckPublic: true})
	if err != nil || result.Outcome != "applied" || readACL() != ".r:*" {
		t.Fatalf("confirmed public ACL = %+v, %v, acl %q", result, err, readACL())
	}
	asks := len(calls)

	// Already public: a further edit keeping the ACL asks nothing new.
	if _, err := h.mutate.UpdateContainer(ctx, "private", "", ContainerEditDTO{ReadACL: ".r:*", AckPublic: true, Meta: map[string]string{"Owner": "ops"}}); err != nil {
		t.Fatalf("edit of an already public container = %v", err)
	}
	if len(calls) != asks {
		t.Fatalf("an already public container was asked again: %+v", calls)
	}

	// A private ACL never asks.
	h.mutate.setConfirmer(answer(false, nil))
	if _, err := h.mutate.UpdateContainer(ctx, "private", "", ContainerEditDTO{ReadACL: ".r:example.com"}); err != nil {
		t.Fatalf("non-public ACL refused: %v", err)
	}
	if len(calls) != asks {
		t.Fatalf("a private ACL asked the dialog: %+v", calls)
	}
}
