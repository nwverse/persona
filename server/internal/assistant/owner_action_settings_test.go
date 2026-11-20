package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestOwnerApprovalSettingsDefaultOffAndReadBack(t *testing.T) {
	s := newTestService(t, newFakeHub().withMachine())
	view := s.view(context.Background(), false)
	if view.OwnerReplyApproval || view.PersistentOwnerActionCards || !reflect.DeepEqual(view.OwnerApprovalPhrases, []string{"发", "同意"}) {
		t.Fatalf("default exact-approval settings: %+v", view)
	}
	for _, body := range []string{
		`{"owner_reply_approval_phrases":[" 发"]}`,
		`{"owner_reply_approval_phrases":["发","发"]}`,
		`{"owner_reply_approval_phrases":["1","2","3","4","5","6","7","8","9"]}`,
		`{"owner_reply_approval_phrases":[""]}`,
	} {
		w, _ := permissionSettingsRequest(t, s, http.MethodPut, "/api/v1/assistant", "assistant", "full", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid phrases %s: %d %s", body, w.Code, w.Body)
		}
	}
	w, saved := permissionSettingsRequest(t, s, http.MethodPut, "/api/v1/assistant", "assistant", "full",
		`{"owner_reply_approval":true,"persistent_owner_action_cards":true,"owner_reply_approval_phrases":["发","Agree"]}`)
	if w.Code != http.StatusOK || !saved.OwnerReplyApproval || !saved.PersistentOwnerActionCards || !reflect.DeepEqual(saved.OwnerApprovalPhrases, []string{"发", "Agree"}) {
		t.Fatalf("save exact-approval settings: %d %+v %s", w.Code, saved, w.Body)
	}
	read := httptest.NewRecorder()
	s.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/v1/assistant", nil))
	var reloaded assistantView
	if err := json.Unmarshal(read.Body.Bytes(), &reloaded); err != nil || !reloaded.OwnerReplyApproval || !reloaded.PersistentOwnerActionCards ||
		!reflect.DeepEqual(reloaded.OwnerApprovalPhrases, []string{"发", "Agree"}) {
		t.Fatalf("read back exact-approval settings: %v %+v", err, reloaded)
	}
	w, saved = permissionSettingsRequest(t, s, http.MethodPut, "/api/v1/assistant", "assistant", "full",
		`{"owner_reply_approval":false,"persistent_owner_action_cards":false}`)
	if w.Code != http.StatusOK || saved.OwnerReplyApproval || saved.PersistentOwnerActionCards || !reflect.DeepEqual(saved.OwnerApprovalPhrases, []string{"发", "Agree"}) {
		t.Fatalf("reverse exact-approval switches: %d %+v", w.Code, saved)
	}
}
