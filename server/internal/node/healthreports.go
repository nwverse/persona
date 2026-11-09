package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// The reports the health page lists and deletes (docs/assistant/health.md"Health reports"): what is in reports/, an original with the
// .md record the health conversation wrote beside it, or a record alone (a photo sent in a conversation has no original).

const healthRecordExt = ".md"

// uploadStamp is the time an upload adds to a file's name (reserveName).
var uploadStamp = lazyre.New(`-\d{8}-\d{6}(?:-\d+)?$`)

// healthReportTitle is the name the user knows a report by: the file's, without the upload's time.
func healthReportTitle(name string) string {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if t := uploadStamp.ReplaceAllString(stem, ""); t != "" {
		return t
	}
	return stem
}

// reportFiles reads dir's plain files; a part file still uploading (dot-named), a link or a folder is not a report.
func reportFiles(dir string) (map[string]fs.FileInfo, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := map[string]fs.FileInfo{}
	for _, e := range ents {
		if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil { // removed since the listing
			continue
		}
		files[e.Name()] = info
	}
	return files, nil
}

// listHealthReports lists dir's reports, the newest first; a missing dir has none.
func listHealthReports(dir string) ([]proto.HealthReport, error) {
	files, err := reportFiles(dir)
	if err != nil {
		return nil, err
	}
	out := []proto.HealthReport{}
	paired := map[string]bool{}
	for name, info := range files {
		if filepath.Ext(name) == healthRecordExt {
			continue
		}
		r := proto.HealthReport{Name: name, Title: healthReportTitle(name), Path: filepath.Join(dir, name), Size: info.Size(), ModifiedAt: info.ModTime()}
		record := strings.TrimSuffix(name, filepath.Ext(name)) + healthRecordExt
		if _, ok := files[record]; ok {
			r.RecordPath = filepath.Join(dir, record)
			paired[record] = true
		}
		out = append(out, r)
	}
	for name, info := range files {
		if filepath.Ext(name) == healthRecordExt && !paired[name] {
			out = append(out, proto.HealthReport{Name: name, Title: healthReportTitle(name), RecordPath: filepath.Join(dir, name), Size: info.Size(), ModifiedAt: info.ModTime()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ModifiedAt.Equal(out[j].ModifiedAt) {
			return out[i].ModifiedAt.After(out[j].ModifiedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// deleteHealthReport removes a report (a Name from the list): for good, not to a trash, because the point is that it is
// gone. Its record goes with it, unless another original shares the stem. One already gone is not an error: another
// device may have deleted it first. name is one file in dir, never a path.
func deleteHealthReport(dir, name string) error {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("bad report name")
	}
	files, err := reportFiles(dir)
	if err != nil {
		return err
	}
	if _, ok := files[name]; !ok {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return fmt.Errorf("not a report") // a link or a folder, which the list never shows
		}
		return nil
	}
	gone := []string{name}
	if ext := filepath.Ext(name); ext != healthRecordExt {
		stem := strings.TrimSuffix(name, ext)
		shared := false
		for other := range files {
			if other != name && filepath.Ext(other) != healthRecordExt && strings.TrimSuffix(other, filepath.Ext(other)) == stem {
				shared = true
			}
		}
		if _, ok := files[stem+healthRecordExt]; ok && !shared {
			gone = append(gone, stem+healthRecordExt)
		}
	}
	for _, f := range gone {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// healthReportsCall serves health.reports.list and health.reports.delete, of the health space health_dir names (""
// the built-in manager's; another's is echoed back: healthReportsDirOf).
func (n *Node) healthReportsCall(method string, params json.RawMessage) (any, error) {
	var p struct {
		Name      string `json:"name"`
		HealthDir string `json:"health_dir"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("bad params: %w", err)
		}
	}
	dir, err := healthReportsDirOf(n.home, p.HealthDir)
	if err != nil {
		return nil, err
	}
	if method == "health.reports.list" {
		list, err := listHealthReports(dir)
		if err != nil {
			return nil, err
		}
		return proto.HealthReports{Reports: list, HealthDir: p.HealthDir}, nil
	}
	if err := deleteHealthReport(dir, p.Name); err != nil {
		return nil, err
	}
	n.logger.Printf("health report deleted")
	out := map[string]any{"ok": true}
	if p.HealthDir != "" {
		out["health_dir"] = p.HealthDir
	}
	return out, nil
}
