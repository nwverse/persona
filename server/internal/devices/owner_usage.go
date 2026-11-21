package devices

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/managerconf"
	"github.com/nwverse/persona/server/internal/secretkey"
	"github.com/nwverse/persona/server/internal/usagesource"
)

var errUsageStorage = errors.New("usage source storage unavailable")
var usageSourceID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// A dedicated box uses the existing AES-GCM implementation. It is separate from the
// manager's database key: that key's rotation only re-seals values in Postgres.
func (d *Devices) usageKeyBox() *secretkey.Box {
	return secretkey.New(filepath.Join(filepath.Dir(d.opts.ConfigFile), "usage-source.key"))
}

func (d *Devices) openUsageKey(s *UsageSourceConfig) error {
	if s.SealedKey == "" {
		return nil
	} // legacy sources remain readable
	key, err := d.usageKeyBox().Open(s.SealedKey)
	if err != nil {
		return errUsageStorage
	}
	s.Key = key
	return nil
}

func (d *Devices) sealUsageKey(s *UsageSourceConfig) error {
	sealed, err := d.usageKeyBox().Seal(s.Key)
	if err != nil {
		return errUsageStorage
	}
	s.Key, s.SealedKey = "", sealed
	return nil
}

// ownerUsageRoutes accepts only the hub's assertion after checking the owner Bearer
// token. This opens test and validated upsert only, never any other admin operation.
// Keys travel in request bodies, are sealed before any config write, and are never
// included in responses, log messages, or storage errors.
func (d *Devices) ownerUsageRoutes(mux *http.ServeMux) {
	handle := func(pattern string, save bool) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(assistantwire.AdminHeader) != "1" {
				writeErrCode(w, http.StatusForbidden, "login_required", errAdminOnly)
				return
			}
			if save && !usageSourceID.MatchString(r.PathValue("id")) {
				writeErrCode(w, http.StatusBadRequest, "invalid_id", i18n.Err("hub-admin_usage.not_found"))
				return
			}
			var in usageSourceInput
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
			if err != nil || json.Unmarshal(data, &in) != nil {
				writeErrCode(w, http.StatusBadRequest, "bad_request", errors.New("invalid usage source request"))
				return
			}
			// Every operation supplies its own key; no caller can redirect a stored key.
			s, code, err := applyUsageInput(UsageSourceConfig{}, in, true)
			if err != nil {
				writeErrCode(w, http.StatusBadRequest, code, err)
				return
			}
			if !usageTesting.CompareAndSwap(false, true) {
				writeErrCode(w, http.StatusTooManyRequests, "busy", i18n.Err("hub-admin_usage.busy"))
				return
			}
			defer usageTesting.Store(false)
			ctx, cancel := context.WithTimeout(r.Context(), usageTestMost)
			defer cancel()
			fetch := d.usageGW.fetch
			if fetch == nil {
				fetch = usagesource.Fetch
			}
			res, err := fetch(ctx, usagesource.Config{Type: s.Type, URL: s.URL, Key: s.Key}, time.Now())
			if err != nil {
				writeJSON(w, map[string]any{"ok": false, "code": usagesource.CodeOf(err)})
				return
			}
			if save {
				s.ID = r.PathValue("id")
				if err := d.sealUsageKey(&s); err != nil {
					writeErrCode(w, http.StatusInternalServerError, "storage_error", errUsageStorage)
					return
				}
				tooMany := false
				err = d.editUsage(func(u *managerconf.Object, list []json.RawMessage) (*managerconf.Object, []json.RawMessage, error) {
					index := -1
					for i, raw := range list {
						if usageEntryID(raw) == s.ID {
							index = i
							break
						}
					}
					if index < 0 && len(list) >= usageSourcesMost {
						tooMany = true
						return u, list, nil
					}
					// Preserve fields this API does not own, as the admin editor does.
					entry, _ := managerconf.ParseObject(nil)
					if index >= 0 {
						var err error
						entry, err = managerconf.ParseObject(list[index])
						if err != nil {
							return u, list, errUsageStorage
						}
					}
					entry.Del("key")
					for k, v := range map[string]any{"id": s.ID, "name": s.Name, "type": s.Type, "url": s.URL, "sealed_key": s.SealedKey} {
						if err := entry.Set(k, v); err != nil {
							return u, list, errUsageStorage
						}
					}
					if s.Enabled != nil && !*s.Enabled {
						_ = entry.Set("enabled", false)
					} else {
						entry.Del("enabled")
					}
					list = append([]json.RawMessage(nil), list...)
					if index >= 0 {
						list[index] = entry.Marshal()
					} else {
						list = append(list, entry.Marshal())
					}
					return u, list, nil
				})
				if err != nil {
					writeErrCode(w, http.StatusInternalServerError, "storage_error", errUsageStorage)
					return
				}
				if tooMany {
					writeErrCode(w, http.StatusConflict, "too_many", i18n.Err("hub-admin_usage.too_many", i18n.Args{"n": usageSourcesMost}))
					return
				}
				// Counts only: even a malformed source name/address cannot echo a submitted key.
				d.logf("owner: usage source saved")
			}
			writeJSON(w, map[string]any{"ok": true, "accounts": res.Seen.Accounts, "windows": res.Seen.Windows})
		})
	}
	handle("POST /api/v1/usage-sources/test", false)
	handle("PUT /api/v1/usage-sources/{id}", true)
}
