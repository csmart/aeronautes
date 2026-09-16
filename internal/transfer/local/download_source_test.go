package local

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func TestDownloadItemJSONOmitsSourceIdentity(t *testing.T) {
	item := DownloadItem{Name: "scope/file", RelPath: "file", Size: 8, Route: "native"}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "etag") || strings.Contains(string(raw), "source") {
		t.Fatalf("source identity leaked onto the bridge DTO: %s", raw)
	}
	report := DownloadPreflight{Items: []DownloadItem{item}, nativeSource: map[string]downloadSourceIdentity{
		item.Name: {captured: true, etag: "deadbeefdeadbeefdeadbeefdeadbeef"},
	}}
	raw, err = json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "deadbeef") || strings.Contains(string(raw), "nativeSource") {
		t.Fatalf("source identity leaked onto the bridge preflight: %s", raw)
	}
}

func TestDownloadSourceIdentityMatch(t *testing.T) {
	plain := downloadSourceIdentity{captured: true, length: 8, etag: "abc"}
	if err := plain.match(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassPlain, ContentLength: 8, ETag: `"abc"`}); err != nil {
		t.Fatalf("matching plain HEAD was refused: %v", err)
	}
	if err := plain.match(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassPlain, ContentLength: 8, ETag: "fff"}); err == nil {
		t.Fatal("etag substitution was accepted")
	}
	if err := plain.match(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassDLO, ContentLength: 8, ETag: "abc"}); err == nil {
		t.Fatal("class change was accepted")
	}
	headed := headSourceIdentity(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassDLO, ContentLength: 64, ETag: "etag", Manifest: "segs/p/"})
	if err := headed.match(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassDLO, ContentLength: 64, ETag: "etag", Manifest: "segs/p/"}); err != nil {
		t.Fatalf("matching DLO HEAD was refused: %v", err)
	}
	if err := headed.match(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassDLO, ContentLength: 64, ETag: "etag", Manifest: "other/p/"}); err == nil {
		t.Fatal("manifest substitution was accepted")
	}
	slo := downloadSourceIdentity{captured: true, class: swiftcontrol.ClassSLO, length: 10, sloETag: "slo"}
	if err := slo.match(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassSLO, ContentLength: 10, ETag: "slo"}); err != nil {
		t.Fatalf("matching SLO listing identity was refused: %v", err)
	}
	sloSizeOnly := downloadSourceIdentity{captured: true, class: swiftcontrol.ClassSLO, length: 10}
	if err := sloSizeOnly.match(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassSLO, ContentLength: 10, ETag: "other"}); err != nil {
		t.Fatalf("SLO without slo_etag used listing tag as content evidence: %v", err)
	}
}
