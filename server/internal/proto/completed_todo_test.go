package proto

import (
	"encoding/json"
	"testing"
)

func TestCompletedTodoIsSparseAndCopyOnWrite(t *testing.T) {
	var original Session
	original.SetCompletedTodo(&TodoRef{ID: "done", Title: "Completed task"})
	copy := original
	copy.SetCompletedTodo(&TodoRef{ID: "other", Title: "Another completed task"})
	if original.CompletedTodo().ID != "done" || copy.CompletedTodo().ID != "other" || original.Todo != nil {
		t.Fatal("completed ownership changed another row or active membership")
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Session
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.CompletedTodo() == nil ||
		decoded.CompletedTodo().ID != "done" || decoded.Todo != nil {
		t.Fatalf("display-only ownership did not round trip: %s, %v", raw, err)
	}
	copy.SetCompletedTodo(nil)
	if copy.Hierarchy != nil || original.CompletedTodo().ID != "done" {
		t.Fatal("clearing the last sparse value left a carrier or changed the original")
	}
}
