package assistant

import (
	"encoding/json"
	"errors"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// putSettings is PUT /api/v1/assistant: what the body sets changes ("-" as main clears it); a launcher is checked
// on the assistant's machine and goes into Model configuration › Main chat too.
func (s *Service) putSettings(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change settings"))
		return
	}
	ctx := r.Context()
	var in struct {
		Settings
		Permission json.RawMessage `json:"default_permission_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	if in.Permission != nil {
		if err := json.Unmarshal(in.Permission, &in.DefaultPermissionMode); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
	}
	if in.Permission != nil && !slices.Contains(proto.PermissionModes, in.DefaultPermissionMode) {
		apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-schedule.bad_permission_mode", i18n.Args{"mode": strconv.Quote(in.DefaultPermissionMode)}))
		return
	}
	if in.Permission != nil && !s.explicitSettingsTarget(w, r) {
		return
	}
	machine := s.settings(ctx).MachineID
	if in.MachineID != "" {
		if _, ok := s.device(ctx, in.MachineID); !ok {
			apierr.Write(w, http.StatusBadRequest, errors.New("unknown machine"))
			return
		}
		machine = in.MachineID
	}
	if s.isWorkMachine(machine) {
		apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-start.work_machine"))
		return
	}
	if in.Launcher != "" {
		if err := s.checkLauncher(ctx, machine, in.Launcher); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
	}
	if d := in.RecordingDays; d != nil && (*d < minRecordingDays || *d > maxRecordingDays) {
		apierr.Write(w, http.StatusBadRequest, errors.New("recording_days must be 1–365"))
		return
	}
	if in.Timezone != "" {
		if _, err := time.LoadLocation(in.Timezone); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
	}
	if in.Launcher != "" {
		rec, err := s.modelRecord(ctx, "assistant")
		if err == nil {
			rec.Binding = mergeModel(rec.Binding, ModelBinding{Provider: "launcher." + in.Launcher})
			err = s.saveModel(ctx, "assistant", rec)
		}
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
	}
	launcher := s.launcherNow(ctx)
	if _, err := s.updateSettings(ctx, func(v *Settings) error {
		*v = s.effective(ctx, *v) // saved as they are in effect, as the hub saved them
		v.MachineID, v.Launcher = machine, launcher
		if in.Launcher != "" {
			v.Launcher = in.Launcher
		}
		if in.Main == "-" {
			v.Main = ""
		} else if in.Main != "" {
			v.Main = in.Main
		}
		if in.Timezone != "" {
			v.Timezone = in.Timezone
		}
		if in.Notes != nil {
			v.Notes = in.Notes
		}
		if in.OwnerAliases != nil {
			v.OwnerAliases = ownerAliases(in.OwnerAliases)
		}
		if in.MainTasksQuiet != nil {
			v.MainTasksQuiet = in.MainTasksQuiet
		}
		if in.StartRequestedTasks != nil {
			v.StartRequestedTasks = in.StartRequestedTasks
		}
		if in.RecordingDays != nil {
			v.RecordingDays = in.RecordingDays
		}
		if in.DoneNeedsDelivery != nil {
			v.DoneNeedsDelivery = in.DoneNeedsDelivery
		}
		if in.DefaultPermissionMode != "" {
			v.DefaultPermissionMode = in.DefaultPermissionMode
		}
		return nil
	}); err != nil {
		apierr.Write(w, http.StatusInternalServerError, err)
		return
	}
	s.settingsChanged(ctx)
	apierr.WriteJSON(w, s.view(ctx, false))
}
