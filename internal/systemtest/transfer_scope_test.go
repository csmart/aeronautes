package systemtest

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

func literalTransferScope(t *testing.T, h *Harness, sourcePrefix, destPrefix string, spacedContainer bool) {
	t.Helper()
	defer h.CloseFind()
	source := h.Container(h.Primary, "literal-source")
	dest := h.Container(h.Secondary, "literal-destination")
	if spacedContainer {
		for _, target := range []struct {
			account *Account
			name    *string
		}{{h.Primary, &source}, {h.Secondary, &dest}} {
			h.Seed(target.account, *target.name, "sentinel", []byte("trimmed-name decoy"), nil)
			*target.name += "  "
			name := *target.name
			h.rawPut(target.account, "/"+oracle.Escape(name), nil, nil)
			t.Cleanup(func() { h.purge(target.account, name) })
		}
	}
	outside := h.Container(h.Primary, "literal-outside")
	h.Seed(h.Primary, outside, "sentinel", []byte("source collateral"), nil)
	h.Seed(h.Secondary, dest, "outside", []byte("destination collateral"), nil)
	selected := map[string][]byte{"plain%2F +日本語": []byte("rclone"), "native\nname": []byte("bytes")}
	for name, body := range selected {
		h.Seed(h.Primary, source, sourcePrefix+name, body, nil)
	}
	if sourcePrefix != "" {
		h.Seed(h.Primary, source, "outside", []byte("unselected source"), nil)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	req := app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: source, SourcePrefix: sourcePrefix,
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dest, DestPrefix: destPrefix,
	}
	native := int64(1)
	if strings.Contains(sourcePrefix, "//") || strings.Contains(destPrefix, "//") {
		native = 2
	}
	preview, read := h.PlanCopy(t.Context(), req)
	if read.Err() != nil || preview == nil {
		t.Fatalf("literal preview: %v", read.Err())
	}
	if preview.Summary.New != 2 || preview.Summary.TransferObjects != 2 || preview.Summary.TransferBytes != 11 || preview.Summary.NativeRouted != native || preview.Summary.Changed != 0 || preview.Summary.Unknown != 0 {
		t.Fatalf("literal preview lost or retargeted selection: %+v", preview.Summary)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	read.CreditRead()

	receipt := h.StartCopy(t.Context(), req)
	if receipt.Err() != nil {
		t.Fatal(receipt.Err())
	}
	job := h.AwaitJob(receipt.JobID())
	want := jobs.Counters{ObjectsPlanned: 2, ObjectsCopied: 2, ObjectsVerified: 2, BytesPlanned: 11, BytesCopied: 11, NativeRouted: native}
	if job.State != "complete" || job.Error != "" || job.Counters != want || job.Source != source+"/"+sourcePrefix || job.Destination != dest+"/"+destPrefix {
		t.Fatalf("literal copy did not complete the intended scope: %+v", job)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA)
	allowed := make([]string, 0, len(selected))
	for name, body := range selected {
		got, found := afterB.Containers[dest].Objects[destPrefix+name]
		if !found || got.Size != int64(len(body)) || got.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) {
			t.Fatalf("literal destination %q missing or changed: %+v", destPrefix+name, got)
		}
		allowed = append(allowed, dest+"/"+destPrefix+name)
	}
	h.AssertNoCollateral(beforeB, afterB, allowed...)
	receipt.CreditHappy()
}

func literalTransferRefusal(t *testing.T, h *Harness, field, variant string) {
	t.Helper()
	defer h.CloseFind()
	source, dest := h.Container(h.Primary, "refuse-source"), h.Container(h.Secondary, "refuse-destination")
	h.Seed(h.Primary, source, "keep", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dest, "keep", []byte("destination sentinel"), nil)
	req := app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: source, DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dest}
	value, want := "   ", "prefix"
	if variant == "separator" {
		value = "scope/ "
	}
	switch field {
	case "source_prefix":
		req.SourcePrefix = value
	case "destination_prefix":
		req.DestPrefix = value
	case "source_container":
		value = source + "/keep"
		if variant == "separator" {
			value = source + "/"
		}
		req.SourceContainer, want = value, "container name"
	case "destination_container":
		value = dest + "/keep"
		if variant == "separator" {
			value = dest + "/"
		}
		req.DestContainer, want = value, "container name"
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	preview, read := h.PlanCopy(t.Context(), req)
	if preview != nil || read.Err() == nil || !strings.Contains(read.Err().Error(), want) {
		t.Fatalf("invalid literal preview admitted or wrong refusal: preview=%+v err=%v", preview, read.Err())
	}
	copy := h.StartCopy(t.Context(), req)
	if copy.JobID() != "" || copy.Err() == nil || !strings.Contains(copy.Err().Error(), want) {
		t.Fatalf("invalid literal copy admitted or wrong refusal: job=%q err=%v", copy.JobID(), copy.Err())
	}
	mirror := h.StartMirror(t.Context(), app.MirrorRequestDTO{CopyRequestDTO: req, Confirm: dest})
	if mirror.JobID() != "" || mirror.Err() == nil || !strings.Contains(mirror.Err().Error(), "Mirror is unavailable") {
		t.Fatalf("Mirror containment lost: job=%q err=%v", mirror.JobID(), mirror.Err())
	}
	jobs, err := h.Jobs.List(t.Context(), "", false, 50)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("refused requests created jobs: %+v err=%v", jobs, err)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	copy.CreditRefusal()
	mirror.CreditRefusal()
}

func TestLiteralScopeRootMock(t *testing.T) {
	literalTransferScope(t, findMockHarness(t), "", "", false)
}

func TestLiteralScopeLeadingSourceMock(t *testing.T) {
	literalTransferScope(t, findMockHarness(t), " src/", "dest/", false)
}

func TestLiteralScopeLeadingDestinationMock(t *testing.T) {
	literalTransferScope(t, findMockHarness(t), "src/", " dest/", false)
}

func TestLiteralScopeSpaceComponentsMock(t *testing.T) {
	literalTransferScope(t, findMockHarness(t), "   /", "  /", false)
}

func TestLiteralScopeRepeatedSeparatorsMock(t *testing.T) {
	literalTransferScope(t, findMockHarness(t), "a//b/", " c//d /", false)
}

func TestLiteralScopeEscapesUnicodeMock(t *testing.T) {
	literalTransferScope(t, findMockHarness(t), "日本語%2F +/", " dst%2F +日本語/", false)
}

func TestLiteralScopeContainerSpacesMock(t *testing.T) {
	literalTransferScope(t, findMockHarness(t), "src/", "dest/", true)
}

func TestLiteralRefuseSourcePrefixFirstMock(t *testing.T) {
	literalTransferRefusal(t, findMockHarness(t), "source_prefix", "spaces")
}

func TestLiteralRefuseSourcePrefixSecondMock(t *testing.T) {
	literalTransferRefusal(t, findMockHarness(t), "source_prefix", "separator")
}

func TestLiteralRefuseDestinationPrefixFirstMock(t *testing.T) {
	literalTransferRefusal(t, findMockHarness(t), "destination_prefix", "spaces")
}

func TestLiteralRefuseDestinationPrefixSecondMock(t *testing.T) {
	literalTransferRefusal(t, findMockHarness(t), "destination_prefix", "separator")
}

func TestLiteralRefuseSourceContainerFirstMock(t *testing.T) {
	literalTransferRefusal(t, findMockHarness(t), "source_container", "spaces")
}

func TestLiteralRefuseSourceContainerSecondMock(t *testing.T) {
	literalTransferRefusal(t, findMockHarness(t), "source_container", "separator")
}

func TestLiteralRefuseDestinationContainerFirstMock(t *testing.T) {
	literalTransferRefusal(t, findMockHarness(t), "destination_container", "spaces")
}

func TestLiteralRefuseDestinationContainerSecondMock(t *testing.T) {
	literalTransferRefusal(t, findMockHarness(t), "destination_container", "separator")
}

func TestLiteralScopeRootRealSwift(t *testing.T) {
	literalTransferScope(t, New(t), "", "", false)
}

func TestLiteralScopeLeadingSourceRealSwift(t *testing.T) {
	literalTransferScope(t, New(t), " src/", "dest/", false)
}

func TestLiteralScopeLeadingDestinationRealSwift(t *testing.T) {
	literalTransferScope(t, New(t), "src/", " dest/", false)
}

func TestLiteralScopeSpaceComponentsRealSwift(t *testing.T) {
	literalTransferScope(t, New(t), "   /", "  /", false)
}

func TestLiteralScopeRepeatedSeparatorsRealSwift(t *testing.T) {
	literalTransferScope(t, New(t), "a//b/", " c//d /", false)
}

func TestLiteralScopeEscapesUnicodeRealSwift(t *testing.T) {
	literalTransferScope(t, New(t), "日本語%2F +/", " dst%2F +日本語/", false)
}

func TestLiteralScopeContainerSpacesRealSwift(t *testing.T) {
	literalTransferScope(t, New(t), "src/", "dest/", true)
}

func TestLiteralRefuseSourcePrefixFirstRealSwift(t *testing.T) {
	literalTransferRefusal(t, New(t), "source_prefix", "spaces")
}

func TestLiteralRefuseSourcePrefixSecondRealSwift(t *testing.T) {
	literalTransferRefusal(t, New(t), "source_prefix", "separator")
}

func TestLiteralRefuseDestinationPrefixFirstRealSwift(t *testing.T) {
	literalTransferRefusal(t, New(t), "destination_prefix", "spaces")
}

func TestLiteralRefuseDestinationPrefixSecondRealSwift(t *testing.T) {
	literalTransferRefusal(t, New(t), "destination_prefix", "separator")
}

func TestLiteralRefuseSourceContainerFirstRealSwift(t *testing.T) {
	literalTransferRefusal(t, New(t), "source_container", "spaces")
}

func TestLiteralRefuseSourceContainerSecondRealSwift(t *testing.T) {
	literalTransferRefusal(t, New(t), "source_container", "separator")
}

func TestLiteralRefuseDestinationContainerFirstRealSwift(t *testing.T) {
	literalTransferRefusal(t, New(t), "destination_container", "spaces")
}

func TestLiteralRefuseDestinationContainerSecondRealSwift(t *testing.T) {
	literalTransferRefusal(t, New(t), "destination_container", "separator")
}
