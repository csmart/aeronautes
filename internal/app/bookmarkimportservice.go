package app

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarkimport"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// BookmarkImportService imports bookmarks from rclone.conf or clouds.yaml
// (ADR 0114): a preview that holds every secret in Go behind a one-use
// token, a save through the ordinary bookmark save, and a verification
// after it that flags what did not verify.
type BookmarkImportService struct {
	bookmarks *BookmarkService
	store     *bookmarks.Store
	picker    FilePicker

	mu      sync.Mutex
	preview *importPreview
	run     *importRun
}

// importPreview is the one preview awaiting a save. A new preview replaces
// it, so secrets read from an abandoned file do not stay in memory.
type importPreview struct {
	token   string
	sources []bookmarkimport.Source
	rows    []ImportRowDTO
}

// importRun is the latest save and its verification, which goes on in the
// background after the save returns.
type importRun struct {
	token    string
	outcomes []ImportOutcomeDTO
	pending  int
}

// verifyAtOnce is how many imported bookmarks are verified at a time
// (ADR 0114 decision 8), so a long file does not open every account at
// once; verifyTimeout bounds each one.
const (
	verifyAtOnce  = 2
	verifyTimeout = 2 * time.Minute
)

// ImportRowDTO is one account the file describes, with no secret.
type ImportRowDTO struct {
	Index     int    `json:"index"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	AuthURL   string `json:"authUrl"`
	User      string `json:"user"`
	Project   string `json:"project"`
	Region    string `json:"region"`
	HasSecret bool   `json:"hasSecret"`
	// State is "importable", "duplicate" (an existing bookmark has the same
	// auth URL, user and project) or "skipped", with Reason saying why.
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// ImportPreviewDTO is what a picked file offers. An empty Token means no
// file was chosen.
type ImportPreviewDTO struct {
	Token    string         `json:"token"`
	FileName string         `json:"fileName"`
	Rows     []ImportRowDTO `json:"rows"`
}

// ImportOutcomeDTO is what happened to one chosen row. Verify is
// "not_requested", "pending", "verified", "failed", "no_secret" or
// "not_saved"; Reason explains a failed save or verification in plain
// words, never an error's own text.
type ImportOutcomeDTO struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	BookmarkID string `json:"bookmarkId"`
	Verify     string `json:"verify"`
	Reason     string `json:"reason"`
}

// ImportResultDTO is a save's outcomes so far; Done once no verification
// is pending.
type ImportResultDTO struct {
	Token    string             `json:"token"`
	Outcomes []ImportOutcomeDTO `json:"outcomes"`
	Done     bool               `json:"done"`
}

var errImportTokenSpent = errors.New("this preview has been used or replaced; choose the file again")

func NewBookmarkImportService(bookmarkService *BookmarkService, store *bookmarks.Store) *BookmarkImportService {
	return &BookmarkImportService{bookmarks: bookmarkService, store: store}
}

// WireImportFilePicker wires the shell's file dialog.
func WireImportFilePicker(s *BookmarkImportService, picker FilePicker) { s.picker = picker }

// PreviewImport asks for a file, reads it, and lists what it offers.
func (s *BookmarkImportService) PreviewImport(ctx context.Context) (ImportPreviewDTO, error) {
	if s.picker == nil {
		return ImportPreviewDTO{}, errors.New("no file picker is available")
	}
	picked, err := s.picker.PickFiles("Choose an rclone.conf or a clouds.yaml to import")
	if err != nil {
		return ImportPreviewDTO{}, err
	}
	if len(picked) == 0 {
		return ImportPreviewDTO{}, nil
	}
	if len(picked) > 1 {
		return ImportPreviewDTO{}, errors.New("choose one file to import")
	}
	file, err := readBounded(picked[0])
	if err != nil {
		return ImportPreviewDTO{}, err
	}
	// The OpenStack clients read secure.yaml from beside clouds.yaml; an
	// rclone config ignores it.
	var secure []byte
	if securePath := filepath.Join(filepath.Dir(picked[0]), "secure.yaml"); securePath != filepath.Clean(picked[0]) {
		if secure, err = readBounded(securePath); errors.Is(err, os.ErrNotExist) {
			secure, err = nil, nil
		}
		if err != nil {
			return ImportPreviewDTO{}, err
		}
	}
	sources, err := bookmarkimport.Read(file, secure)
	if err != nil {
		return ImportPreviewDTO{}, err
	}
	existing, err := s.store.List(ctx)
	if err != nil {
		return ImportPreviewDTO{}, err
	}
	rows := make([]ImportRowDTO, len(sources))
	for i, source := range sources {
		rows[i] = previewRow(i, source, existing)
	}
	token, err := newImportToken()
	if err != nil {
		return ImportPreviewDTO{}, err
	}
	s.mu.Lock()
	s.preview = &importPreview{token: token, sources: sources, rows: rows}
	s.mu.Unlock()
	return ImportPreviewDTO{Token: token, FileName: filepath.Base(picked[0]), Rows: rows}, nil
}

// SaveImport saves the chosen rows through the ordinary bookmark save and,
// when verify is set, verifies them in the background. The preview's token
// is spent, so a second save with it is refused.
func (s *BookmarkImportService) SaveImport(ctx context.Context, token string, chosen []int, verify bool) (ImportResultDTO, error) {
	s.mu.Lock()
	preview := s.preview
	if preview == nil || preview.token != token {
		s.mu.Unlock()
		return ImportResultDTO{}, errImportTokenSpent
	}
	// A choice that cannot be imported is refused before the token is
	// spent, so the operator can correct it without choosing the file again.
	for _, index := range chosen {
		if index < 0 || index >= len(preview.sources) || preview.rows[index].State == "skipped" {
			s.mu.Unlock()
			return ImportResultDTO{}, fmt.Errorf("row %d cannot be imported", index)
		}
	}
	s.preview = nil
	s.mu.Unlock()

	run := &importRun{token: token}
	var toVerify []int
	for _, index := range chosen {
		source := preview.sources[index]
		outcome := ImportOutcomeDTO{Index: index, Name: source.Name, Verify: "not_requested"}
		saved, err := s.bookmarks.Save(ctx, importDTO(source.Bookmark), source.Secret)
		switch {
		case err != nil:
			outcome.Verify, outcome.Reason = "not_saved", saveFailureReason(err)
		case source.Secret == "":
			outcome.BookmarkID = saved.ID
			if verify {
				outcome.Verify = "no_secret"
			}
		default:
			outcome.BookmarkID = saved.ID
			if verify {
				outcome.Verify = "pending"
				toVerify = append(toVerify, len(run.outcomes))
			}
		}
		run.outcomes = append(run.outcomes, outcome)
	}
	run.pending = len(toVerify)
	s.mu.Lock()
	s.run = run
	s.mu.Unlock()
	if len(toVerify) > 0 {
		go s.verifyImported(context.WithoutCancel(ctx), run, toVerify)
	}
	return s.result(run), nil
}

// ImportStatus is the latest save's outcomes, for the dialog to follow the
// verification as it goes.
func (s *BookmarkImportService) ImportStatus(token string) (ImportResultDTO, error) {
	s.mu.Lock()
	run := s.run
	s.mu.Unlock()
	if run == nil || run.token != token {
		return ImportResultDTO{}, errImportTokenSpent
	}
	return s.result(run), nil
}

func (s *BookmarkImportService) result(run *importRun) ImportResultDTO {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ImportResultDTO{Token: run.token, Outcomes: append([]ImportOutcomeDTO(nil), run.outcomes...), Done: run.pending == 0}
}

// verifyImported verifies each saved bookmark with its saved key, at most
// verifyAtOnce at a time, and flags each that does not verify.
func (s *BookmarkImportService) verifyImported(ctx context.Context, run *importRun, indexes []int) {
	slots := make(chan struct{}, verifyAtOnce)
	var wg sync.WaitGroup
	for _, i := range indexes {
		slots <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer func() { <-slots; wg.Done() }()
			s.mu.Lock()
			id := run.outcomes[i].BookmarkID
			s.mu.Unlock()
			state, reason := s.verifyOne(ctx, id)
			s.mu.Lock()
			run.outcomes[i].Verify, run.outcomes[i].Reason = state, reason
			run.pending--
			s.mu.Unlock()
		}(i)
	}
	wg.Wait()
}

func (s *BookmarkImportService) verifyOne(ctx context.Context, id string) (state, reason string) {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	saved, err := s.store.Get(ctx, id)
	if err == nil {
		_, err = s.bookmarks.Verify(ctx, toBookmarkDTO(saved), "")
	}
	if err == nil {
		return "verified", ""
	}
	reason = verifyFailureReason(err)
	if flagErr := s.store.SetVerifyFailure(context.WithoutCancel(ctx), id, reason); flagErr != nil {
		reason += " (the warning could not be saved on the bookmark)"
	}
	return "failed", reason
}

// previewRow describes a source for the preview, with no secret.
func previewRow(index int, source bookmarkimport.Source, existing []bookmarks.Bookmark) ImportRowDTO {
	b := source.Bookmark
	row := ImportRowDTO{Index: index, Name: source.Name, AuthURL: b.AuthURL, Region: b.Region,
		HasSecret: source.Secret != "", User: firstNonEmpty(b.Username, idLabel(b.UserID)),
		Project: firstNonEmpty(b.ProjectName, idLabel(b.ProjectID)), Kind: "Keystone v3", State: "importable"}
	if b.AuthVersion == 1 {
		row.Kind = "TempAuth"
	}
	switch {
	case source.Skip != "":
		row.State, row.Reason = "skipped", source.Skip
	case sameAccountAsAny(b, existing):
		row.State, row.Reason = "duplicate", "a bookmark already has this auth URL, user and project"
	}
	return row
}

// sameAccountAsAny reports whether an existing bookmark has the same auth
// URL, user and project (ADR 0114 decision 6).
func sameAccountAsAny(b bookmarks.Bookmark, existing []bookmarks.Bookmark) bool {
	for _, e := range existing {
		if e.AuthURL == b.AuthURL && e.Username == b.Username && e.UserID == b.UserID &&
			e.ProjectName == b.ProjectName && e.ProjectID == b.ProjectID {
			return true
		}
	}
	return false
}

func importDTO(b bookmarks.Bookmark) BookmarkDTO {
	return BookmarkDTO{
		DisplayName: b.DisplayName, AuthURL: b.AuthURL, Username: b.Username, StorageURLOverride: b.StorageURLOverride,
		AuthVersion: b.AuthVersion, UserDomain: b.UserDomain, ProjectName: b.ProjectName, ProjectDomain: b.ProjectDomain,
		UserID: b.UserID, ProjectID: b.ProjectID, Region: b.Region, TLSMode: string(swiftcontrol.TLSModeSystem),
	}
}

// verifyFailureReason is a plain reason chosen by the kind of failure. An
// error's own text can carry a server's words, so it is never shown.
func verifyFailureReason(err error) string {
	var noV3 *swiftcontrol.NoKeystoneV3Error
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificate *tls.CertificateVerificationError
	switch {
	case errors.As(err, &noV3):
		return "the auth URL does not offer Keystone v3 at its own address"
	case errors.As(err, &unknownAuthority), errors.As(err, &hostname), errors.As(err, &certificate):
		return "the endpoint's certificate is not trusted"
	}
	var se *swiftcontrol.SwiftError
	if errors.As(err, &se) {
		switch se.Kind {
		case swiftcontrol.KindUnauthorized:
			return "the identity service refused these credentials"
		case swiftcontrol.KindForbidden:
			return "these credentials may not use this account"
		case swiftcontrol.KindNotFound:
			return "the auth URL or the account was not found"
		case swiftcontrol.KindTimeout, swiftcontrol.KindNetwork:
			return "the endpoint could not be reached"
		case swiftcontrol.KindServer, swiftcontrol.KindRateLimited:
			return "the service did not answer; try again later"
		}
	}
	return "it did not verify; edit it to check the details"
}

// saveFailureReason is a save's own refusal, which names a field rather
// than a value, or a plain sentence when the store could not be reached.
func saveFailureReason(err error) string {
	if errors.Is(err, bookmarks.ErrPublicationUnresolved) {
		return "the save could not be confirmed; check the bookmark list"
	}
	return "it could not be saved: " + err.Error()
}

func idLabel(id string) string {
	if id == "" {
		return ""
	}
	return "ID " + id
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// readBounded reads a file the import was asked to read, refusing one too
// large to be a configuration before reading it whole.
func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, bookmarkimport.MaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > bookmarkimport.MaxFileSize {
		return nil, bookmarkimport.ErrTooLarge
	}
	return data, nil
}

func newImportToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
