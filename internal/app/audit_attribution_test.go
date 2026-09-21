package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
)

func TestAuditSearchRetainsBookmarkIdentity(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	at := time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC)
	events := []audit.Event{
		{BookmarkID: "alpha", Operation: "object.metadata.update", Result: audit.ResultApplied},
		{BookmarkID: "beta", Operation: "object.metadata.update", Result: audit.ResultApplied},
		{Operation: "application.start", Result: audit.ResultApplied},
		{BookmarkID: "unavailable-bookmark", Operation: "object.metadata.update", Result: audit.ResultFailed},
		{BookmarkID: "alpha", Operation: "object.metadata.update", Result: audit.ResultFailed},
		{BookmarkID: "beta", Operation: "object.metadata.update", Result: audit.ResultApplied},
		{BookmarkID: "alpha", Operation: "object.metadata.other", Result: audit.ResultApplied},
		{BookmarkID: "alpha", Operation: "literal%_.update", Result: audit.ResultConflict},
		{BookmarkID: " alpha%_ ", Operation: "object.metadata.update", Result: audit.ResultApplied},
	}
	for i := range events {
		events[i].At = at
		events[i].Resource = "same-container/same-object"
		events[i].Summary = "same recorded summary"
		events[i].TransIDs = []string{"tx-observed"}
		if err := h.auditSvc.audit.Append(ctx, events[i]); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("store control retains distinct identities", func(t *testing.T) {
		rows, err := h.auditSvc.audit.List(ctx, audit.Query{Limit: 20})
		if err != nil || len(rows) != len(events) {
			t.Fatalf("store rows = %+v, error %v", rows, err)
		}
		for i, row := range rows {
			if row.BookmarkID != events[len(events)-1-i].BookmarkID {
				t.Fatalf("store row %d bookmark = %q", row.ID, row.BookmarkID)
			}
		}
	})

	cases := []struct {
		name string
		q    AuditQueryDTO
		ids  []int64
	}{
		{"all accounts", AuditQueryDTO{Limit: 20}, []int64{9, 8, 7, 6, 5, 4, 3, 2, 1}},
		{"named alpha", AuditQueryDTO{BookmarkID: "alpha"}, []int64{8, 7, 5, 1}},
		{"named beta", AuditQueryDTO{BookmarkID: "beta"}, []int64{6, 2}},
		{"combined filters", AuditQueryDTO{BookmarkID: "alpha", OperationPrefix: "object.metadata.update", Result: "applied"}, []int64{1}},
		{"first page", AuditQueryDTO{BookmarkID: "alpha", Limit: 2}, []int64{8, 7}},
		{"continuation", AuditQueryDTO{BookmarkID: "alpha", Limit: 2, BeforeID: 7}, []int64{5, 1}},
		{"exhausted", AuditQueryDTO{BookmarkID: "alpha", BeforeID: 1}, nil},
		{"unavailable bookmark", AuditQueryDTO{BookmarkID: "unavailable-bookmark"}, []int64{4}},
		{"unknown bookmark", AuditQueryDTO{BookmarkID: "unknown"}, nil},
		{"literal bookmark", AuditQueryDTO{BookmarkID: " alpha%_ "}, []int64{9}},
		{"literal operation", AuditQueryDTO{BookmarkID: "alpha", OperationPrefix: "literal%_"}, []int64{8}},
		{"accountless control", AuditQueryDTO{OperationPrefix: "application."}, []int64{3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.auditSvc.Search(ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			want := make([]AuditEventDTO, 0, len(tc.ids))
			for _, id := range tc.ids {
				e := events[id-1]
				want = append(want, AuditEventDTO{
					ID: id, At: at.Format(time.RFC3339), BookmarkID: e.BookmarkID,
					Operation: e.Operation, Resource: e.Resource, Summary: e.Summary,
					Result: string(e.Result), TransIDs: e.TransIDs,
				})
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Search(%+v) = %+v, want %+v", tc.q, got, want)
			}
		})
	}

	for _, connected := range []bool{false, true} {
		name := "disconnected history"
		if connected {
			h.connect(t)
			name = "another active bookmark"
		}
		t.Run(name, func(t *testing.T) {
			active := h.browse.connections.ActiveBookmarkID()
			if (active != "") != connected || active == "alpha" {
				t.Fatalf("unexpected active bookmark %q", active)
			}
			rows, err := h.auditSvc.Search(ctx, AuditQueryDTO{BookmarkID: "alpha"})
			if err != nil || len(rows) != 4 {
				t.Fatalf("historical Search = %+v, error %v", rows, err)
			}
			for _, row := range rows {
				if row.BookmarkID != "alpha" {
					t.Fatalf("history attributed to %q while active is %q", row.BookmarkID, active)
				}
			}
		})
	}
}
