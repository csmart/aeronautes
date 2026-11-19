package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
)

// RecentFor is the bound §50 newest-events read (ADR 0059). It returns
// only the named bookmark's events, each restating that bookmark.
// The unscoped Recent method still exists on this type but the store
// refuses it, so it cannot answer across accounts.
func (s *AuditService) RecentFor(ctx context.Context, bookmarkID string, limit int) ([]AuditEventDTO, error) {
	if bookmarkID == "" {
		return nil, fmt.Errorf("%w: name the bookmark the read is for", ErrAccountScopeChanged)
	}
	events, err := s.audit.List(ctx, audit.Query{BookmarkID: bookmarkID, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]AuditEventDTO, 0, len(events))
	for _, e := range events {
		dto := auditEventDTO(e)
		dto.BookmarkID = e.BookmarkID
		out = append(out, dto)
	}
	return out, nil
}

// setFilePicker wires the shell's native directory dialog for export.
func (s *AuditService) setFilePicker(picker FilePicker) {
	s.picker = picker
}

// ExportResultDTO reports a completed §51 archive: how many events were
// written and the file they went to, so the operator can find it. The
// directory is the one the native dialog returned, never a path the page
// chose (ADR 0011).
type ExportResultDTO struct {
	Count     int    `json:"count"`
	Directory string `json:"directory"`
	FileName  string `json:"fileName"`
}

// ExportAudit writes the §50 trail under the viewer's current filters to
// a JSON Lines file in a directory the operator chooses, so the record
// can be archived before retention compacts it (§51). It reads one
// consistent snapshot (ExportTo), so a concurrent prune cannot drop an
// unread row, and writes the whole matching trail newest first. Output
// goes to a .part file and is published by linkPublish only once the
// whole snapshot is on disk, so a failed export leaves at most a
// recognisably incomplete .part, never a complete-looking .jsonl, and an
// occupied final name is never replaced. The path stays in Go and only
// the chosen directory is reported back (ADR 0011). A headless shell has
// no dialog and says so; a cancelled dialog returns no result and no
// error. The trail never holds secrets (§50), so the archive is safe to
// keep.
func (s *AuditService) ExportAudit(ctx context.Context, q AuditQueryDTO) (*ExportResultDTO, error) {
	if s.picker == nil {
		return nil, errors.New("no file picker available (headless shell)")
	}
	dir, err := s.picker.PickDirectory("Choose a folder to save the exported audit log")
	if err != nil || dir == "" {
		return nil, err
	}
	base := "aeronautes-audit-" + time.Now().UTC().Format("20060102T150405Z")
	file, partPath, err := createExclusivePart(dir, base, ".jsonl")
	if err != nil {
		return nil, err
	}
	// Until linkPublish succeeds, remove the partial output on any error
	// so nothing half-written is left; a .part that survives a failed
	// removal is still plainly incomplete.
	committed := false
	defer func() {
		if committed {
			return
		}
		file.Close()
		os.Remove(partPath)
	}()
	encoder := json.NewEncoder(file)
	count := 0
	if err := s.audit.ExportTo(ctx, audit.Query{
		OperationPrefix: q.OperationPrefix,
		Result:          q.Result,
		BookmarkID:      q.BookmarkID,
	}, func(e audit.Event) error {
		if encErr := encoder.Encode(auditEventDTO(e)); encErr != nil {
			return encErr
		}
		count++
		return nil
	}); err != nil {
		return nil, err
	}
	// Finalise before publishing: the flush and the close must both land,
	// then publish the whole archive under its final name.
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	name, err := linkPublish(partPath, dir, base, ".jsonl")
	if err != nil {
		return nil, err
	}
	committed = true
	return &ExportResultDTO{Count: count, Directory: dir, FileName: name}, nil
}

// linkPublish publishes the written .part under a fresh name with the given
// extension by hard link, which atomically refuses an occupied destination:
// it never
// replaces a file or symlink that is present at the name, and there is no
// check-and-use gap for a late arrival to race through. A collision steps
// the suffix on. The .part is unlinked once the final name is in place.
func linkPublish(partPath, dir, base, ext string) (string, error) {
	for attempt := 0; attempt <= 1024; attempt++ {
		name := base + ext
		if attempt > 0 {
			name = fmt.Sprintf("%s-%d%s", base, attempt, ext)
		}
		err := os.Link(partPath, filepath.Join(dir, name))
		if err == nil {
			os.Remove(partPath)
			return name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not publish the export under a free name in the chosen folder")
}
