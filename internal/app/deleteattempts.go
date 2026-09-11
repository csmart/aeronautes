package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/csmart/aeronautes/internal/deletion"
)

func retainDeletionAttempts(attempts []deletion.UnindexedAttempt) (string, error) {
	if len(attempts) == 0 {
		return "", nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve deletion attempt cache: %w", err)
	}
	if !filepath.IsAbs(cache) {
		return "", errors.New("resolve deletion attempt cache: location is not absolute")
	}
	dir := filepath.Join(cache, "aeronautes", "deletion-attempts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create deletion attempt directory: %w", err)
	}
	file, err := os.CreateTemp(dir, "attempts-*.jsonl")
	if err != nil {
		return "", fmt.Errorf("create deletion attempt evidence: %w", err)
	}
	if err := writeDeletionAttempts(file, attempts); err != nil {
		return "", err
	}
	return file.Name(), nil
}

// This owns the file through close, including after an encode or flush
// error: an accepted buffer does not establish that evidence was retained.
func writeDeletionAttempts(file *os.File, attempts []deletion.UnindexedAttempt) error {
	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	var problem error
	for _, attempt := range attempts {
		// JSON replaces invalid UTF-8. Refuse a lossy record rather than
		// advertise a different issued identity or provider detail.
		if !utf8.ValidString(attempt.Name) || !utf8.ValidString(attempt.Detail) {
			problem = errors.New("encode deletion attempt: invalid UTF-8 cannot be retained exactly as JSON")
			break
		}
		if err := encoder.Encode(attempt); err != nil {
			problem = fmt.Errorf("encode deletion attempt: %w", err)
			break
		}
	}
	if err := writer.Flush(); err != nil {
		problem = errors.Join(problem, fmt.Errorf("flush deletion attempts: %w", err))
	}
	if err := file.Close(); err != nil {
		problem = errors.Join(problem, fmt.Errorf("close deletion attempt evidence: %w", err))
	}
	return problem
}
