package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestCleanAttachmentsRejectsRootTraversalAndNul(t *testing.T) {
	for _, bad := range []string{
		"/",
		"~/",
		"/tmp/",
		"/tmp/../etc/passwd",
		"~/docs/../../etc/passwd",
		"/tmp/bad\x00file.txt",
		`~/a\..\b`,
	} {
		if _, err := cleanAttachments([]string{bad}); err == nil {
			t.Errorf("cleanAttachments accepted unsafe path %q", bad)
		}
	}
}

func TestCheckAttachmentsBlocksSubstringAndTraversalExfiltration(t *testing.T) {
	s, f := newTaskService(t)
	ctx := context.Background()
	f.sessions["server"] = nil
	f.setItems(proto.Item{
		Role: "user",
		Kind: proto.KindText,
		Text: "Compare /home/me/.ssh/id_rsa.pub and /code/app/.env.example (yes/no) plus /tmp/report-20261109-120000.txt.bak",
	})

	// Substring/prefix matches must NEVER authorize exfiltrating a different file.
	for _, bad := range []string{
		"/home/me/.ssh/id_rsa",
		"/code/app/.env",
		"/code/app",
		"/",
		"/tmp/report-20261109-120000.txt",
		"/tmp/claude-1000/-home-me/conv1/images/",
		"/tmp/claude-1000/-home-me/conv1/images/../../secret.txt",
	} {
		if _, err := s.checkAttachments(ctx, "server", "conv1", []string{bad}); err == nil {
			t.Errorf("checkAttachments allowed exfiltration of %q via substring/traversal bypass", bad)
		}
	}

	// Exact mentioned paths (including sentence-ending punctuation) still work.
	if ok, err := s.checkAttachments(ctx, "server", "conv1", []string{"/home/me/.ssh/id_rsa.pub", "/code/app/.env.example"}); err != nil || len(ok) != 2 {
		t.Fatalf("checkAttachments rejected exact user paths: ok=%v err=%v", ok, err)
	}
}

func TestReadAttachmentRejectsNegativeSizeAndOversizedChunk(t *testing.T) {
	t.Run("negative head size returns error instead of panicking", func(t *testing.T) {
		s, f := newTaskService(t)
		f.sessions["server"] = nil
		f.answerNode("file.chunk", func(string, json.RawMessage) (any, error) {
			return proto.FileChunk{Path: "/tmp/a.txt", Name: "a.txt", Size: -10, Revision: "r1"}, nil
		})
		if _, _, _, err := s.readAttachment(context.Background(), "server", "/tmp/a.txt"); err == nil {
			t.Fatal("expected error for negative head.Size")
		}
	})

	t.Run("chunk exceeding declared head.Size or attachmentMostBytes is rejected", func(t *testing.T) {
		s, f := newTaskService(t)
		f.sessions["server"] = nil
		f.answerNode("file.chunk", func(_ string, raw json.RawMessage) (any, error) {
			var req proto.FileChunkRequest
			_ = json.Unmarshal(raw, &req)
			if req.Length == 0 {
				return proto.FileChunk{Path: req.Path, Name: "../../evil.txt", Size: 4, Revision: "r1"}, nil
			}
			return proto.FileChunk{Path: req.Path, Data: []byte("0123456789overflow")}, nil
		})
		if _, _, _, err := s.readAttachment(context.Background(), "server", "/tmp/a.txt"); err == nil {
			t.Fatal("expected error when chunk exceeds declared size")
		}
	})

	t.Run("head.Name path traversal is sanitized to base filename", func(t *testing.T) {
		s, f := newTaskService(t)
		f.sessions["server"] = nil
		f.answerNode("file.chunk", func(_ string, raw json.RawMessage) (any, error) {
			var req proto.FileChunkRequest
			_ = json.Unmarshal(raw, &req)
			if req.Length == 0 {
				return proto.FileChunk{Path: req.Path, Name: "../../evil.txt", Size: 2, Revision: "r1"}, nil
			}
			return proto.FileChunk{Path: req.Path, Data: []byte("ok")}, nil
		})
		name, _, data, err := s.readAttachment(context.Background(), "server", "/tmp/a.txt")
		if err != nil {
			t.Fatalf("readAttachment: %v", err)
		}
		if name != "evil.txt" || string(data) != "ok" {
			t.Fatalf("name=%q data=%q, want evil.txt / ok", name, data)
		}
	})
}

func TestWithAttachmentsDeterministicAndBoundarySafe(t *testing.T) {
	h := handedAttachments{
		Copied: true,
		Files:  []string{"/dest/a", "/dest/ab"},
		moved: map[string]string{
			"/tmp/a":     "/dest/a",
			"/tmp/a.bak": "/dest/a-bak",
		},
	}
	in := "Check /tmp/a and /tmp/a.bak and /tmp/a.other."
	got := withAttachments(in, h)
	wantPrefix := "Check /dest/a and /dest/a-bak and /tmp/a.other."
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("withAttachments corrupted boundary or order:\ngot:  %q\nwant: %q", got, wantPrefix)
	}
}
