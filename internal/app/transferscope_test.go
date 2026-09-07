package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestCopyRejectsSlashContainerNames(t *testing.T) {
	for _, field := range []string{"source", "destination"} {
		for index, name := range []string{"/container", "container/", "container/object", "container//object"} {
			t.Run(fmt.Sprintf("%s_%d", field, index), func(t *testing.T) {
				req := CopyRequestDTO{SourceBookmarkID: "source", DestBookmarkID: "destination", SourceContainer: "source", DestContainer: "destination"}
				if field == "source" {
					req.SourceContainer = name
				} else {
					req.DestContainer = name
				}
				if err := validateCopyRequest(req); err == nil || !strings.Contains(err.Error(), "container name") {
					t.Fatalf("slash-bearing %s container was not rejected: %v", field, err)
				}
			})
		}
	}
}

func TestCopyValidationAcceptsLiteralScopes(t *testing.T) {
	for index, prefix := range []string{"", " leading/", "trailing /", "   /", "a//b/", "日本語%2F +/"} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			req := CopyRequestDTO{
				SourceBookmarkID: "source", DestBookmarkID: "destination",
				SourceContainer: " source ", DestContainer: "   ", SourcePrefix: prefix, DestPrefix: prefix,
			}
			if err := validateCopyRequest(req); err != nil {
				t.Fatalf("literal scope refused: %v", err)
			}
		})
	}
}
