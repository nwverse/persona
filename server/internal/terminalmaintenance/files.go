package terminalmaintenance

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/nwverse/persona/server/internal/osproc"
)

const (
	maxRecoveryFile = 512 << 20
	maxHistoryFile  = 128 << 20
	maxSettingsFile = 4 << 20
)

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func snapshot(path string, r Record) (File, error) {
	return snapshotPrefix(path, r, 0)
}

func snapshotPrefix(path string, r Record, prefix int64) (File, error) {
	file, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return File{}, err
	}
	limit := int64(maxSettingsFile)
	if path == r.Executable {
		limit = maxRecoveryFile
	} else if path == r.History {
		limit = maxHistoryFile
	}
	if !before.Mode().IsRegular() || (path != r.Executable && !osproc.OwnedByMe(before)) || before.Size() == 0 || before.Size() > limit {
		return File{}, errors.New("recovery files must be nonempty bounded regular files; private history and settings must belong to this user")
	}
	h := sha256.New()
	var hash io.Writer = h
	if prefix > 0 {
		if prefix > before.Size() {
			return File{}, errors.New("native history was truncated")
		}
		hash = &prefixWriter{remaining: prefix, out: h}
	}
	read := io.TeeReader(io.LimitReader(file, limit+1), hash)
	if path == r.History {
		if err := historyID(read, r.Runtime, r.Session); err != nil {
			return File{}, err
		}
	} else if _, err := io.Copy(io.Discard, read); err != nil {
		return File{}, err
	}
	after, err := os.Stat(path)
	if err != nil {
		return File{}, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return File{}, errors.New("recovery file changed during preparation")
	}
	return File{Path: path, Size: before.Size(), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

type prefixWriter struct {
	remaining int64
	out       io.Writer
}

func (w *prefixWriter) Write(data []byte) (int, error) {
	n := min(int64(len(data)), w.remaining)
	if n > 0 {
		written, err := w.out.Write(data[:n])
		w.remaining -= int64(written)
		if err != nil {
			return written, err
		}
		if int64(written) != n {
			return written, io.ErrShortWrite
		}
	}
	return len(data), nil
}

func historyID(read io.Reader, runtime, id string) error {
	scan := bufio.NewScanner(read)
	scan.Buffer(make([]byte, 4096), 16<<20)
	found := false
	for scan.Scan() {
		if len(scan.Bytes()) == 0 {
			continue
		}
		var row struct {
			Session string `json:"sessionId"`
			Type    string `json:"type"`
			Payload struct {
				ID string `json:"id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
			return errors.New("native history contains an incomplete or invalid row")
		}
		candidate := row.Session
		if runtime == "codex" {
			candidate = ""
			if row.Type == "session_meta" {
				candidate = row.Payload.ID
			}
		}
		if candidate != "" {
			if candidate != id {
				return errors.New("native history session conflicts with the exact recovery ID")
			}
			found = true
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	if !found {
		return errors.New("native history does not establish the exact recovery ID")
	}
	return nil
}
