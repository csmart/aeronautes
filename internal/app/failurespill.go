package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// failureSpill streams a job's EVERY per-name failure to a JSONL file
// (K-DEL-002: the exact failed set survives, however large — the
// in-memory Failures slice is only a display sample). Returns the
// recorder and a closer that reports the path and count; a spill that
// cannot open degrades to counting only (the job still refuses to call
// itself clean).
func failureSpill(jobID string) (record func(name, detail string), close func() (path string, n int64)) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "aeronautes", "failures")
	var (
		mu        sync.Mutex
		count     int64
		file      *os.File
		writer    *bufio.Writer
		enc       *json.Encoder
		writeErrs int64
	)
	if err := os.MkdirAll(dir, 0o700); err == nil {
		if f, err := os.OpenFile(filepath.Join(dir, jobID+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
			file = f
			writer = bufio.NewWriter(f)
			enc = json.NewEncoder(writer)
		}
	}
	record = func(name, detail string) {
		mu.Lock()
		defer mu.Unlock()
		count++
		if enc != nil {
			if err := enc.Encode(map[string]string{"name": name, "error": detail}); err != nil {
				writeErrs++
			}
		}
	}
	close = func() (string, int64) {
		mu.Lock()
		defer mu.Unlock()
		if file == nil {
			return "", count
		}
		if err := writer.Flush(); err != nil {
			writeErrs++
		}
		path := file.Name()
		if err := file.Close(); err != nil {
			writeErrs++
		}
		file = nil
		if count == 0 {
			_ = os.Remove(path)
			return "", 0
		}
		if writeErrs > 0 {
			// The spill is INCOMPLETE: say so in the path callers embed
			// in the job error, rather than presenting a partial file
			// as the exact set (review F-10).
			return path + " (INCOMPLETE: " + fmt.Sprint(writeErrs) + " spill write failures)", count
		}
		return path, count
	}
	return record, close
}
