package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	// pastedPathStart is where Claude Code cuts a pasted line into pieces: a space before something that starts a path.
	pastedPathStart = regexp.MustCompile(`^["']?(?:/|[A-Za-z]:\\)`)
	pastedImageExt  = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp)$`)
)

// claudePaste is what Claude Code (2.1.316, its paste handler) makes of pasted text. Each line is cut at every space
// before a path; a line of several pieces that are not all paths stays as it is. Otherwise each piece that is a path
// with an image extension, to a file that exists, becomes an attached image and leaves the text; a blank line goes.
// A paste that attaches nothing is pasted as it was, blank lines included.
func claudePaste(text string) (kept string, attached []string) {
	notPath := func(p string) bool { return !pastedPathStart.MatchString(strings.TrimSpace(p)) }
	var out []string
	for _, line := range strings.Split(text, "\n") {
		var pieces []string
		start := 0
		for i := 0; i < len(line); i++ {
			if line[i] == ' ' && pastedPathStart.MatchString(line[i+1:]) {
				if strings.TrimSpace(line[start:i]) != "" {
					pieces = append(pieces, line[start:i])
				}
				start = i + 1
			}
		}
		if strings.TrimSpace(line[start:]) != "" {
			pieces = append(pieces, line[start:])
		}
		if len(pieces) > 1 && slices.ContainsFunc(pieces, notPath) {
			out = append(out, line)
			continue
		}
		for _, p := range pieces {
			path := strings.Trim(strings.TrimSpace(p), `"'`)
			if _, err := os.Stat(path); err == nil && pastedImageExt.MatchString(path) {
				attached = append(attached, path)
				continue
			}
			out = append(out, p)
		}
	}
	if len(attached) == 0 {
		return text, nil
	}
	return strings.Join(out, "\n"), attached
}

// appPhotos is the photos the apps show for a message: the lines of its <attached_images> block that start with "/" or
// "~" (iOS UserBubble.imagePaths, Android imagePaths, web parseUserText).
func appPhotos(text string) (photos []string) {
	for _, l := range strings.Split(attachedNote.FindString(text), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "/") || strings.HasPrefix(l, "~") {
			photos = append(photos, l)
		}
	}
	return photos
}

// sendToClaude sends text (and images) to an idle Claude through session.send and returns what was typed into it. The
// transcript then records the message as Claude does: the pictures it attached, then its text.
func sendToClaude(t *testing.T, text string, images []map[string]any) string {
	t.Helper()
	n, f, home, cwd := guideNode(t, true)
	guidePane(t, f, home, cwd, func(int) string { return "" })
	var mu sync.Mutex
	typed := ""
	f.after(func(c herdrCall) {
		if c.Method != "agent.prompt" {
			return
		}
		pasted := str(c.Params["text"])
		mu.Lock()
		typed = pasted
		mu.Unlock()
		kept, attached := claudePaste(pasted)
		var content []any
		chips := ""
		for i := range attached {
			content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}})
			chips += "[Image #" + strconv.Itoa(i+1) + "]"
		}
		content = append(content, map[string]any{"type": "text", "text": chips + kept})
		line, _ := json.Marshal(map[string]any{"type": "user", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "cwd": cwd,
			"message": map[string]any{"role": "user", "content": content}})
		appendTranscript(t, home, cwd, sidA, string(line))
	})
	params := map[string]any{"session_id": sidA, "text": text}
	if images != nil {
		params["images"] = images
	}
	if _, err := request(t, n, "session.send", params); err != nil {
		t.Fatalf("send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return typed
}

// Claude Code turns each bare image path in <attached_images> into an `[Image #n]` and drops the line (#32). Each
// picture is attached once, its path reaches the agent as text, and the apps show each photo once.
func TestSendKeepsTheImagePathForClaude(t *testing.T) {
	png := map[string]any{"mime": "image/png", "data": "iVBORw0KGgo="}
	jpg := map[string]any{"mime": "image/jpeg", "data": "/9j/4AAQ"}
	typed := sendToClaude(t, "  copy this into the docs ", []map[string]any{png, jpg})

	photos := appPhotos(typed)
	if len(photos) != 2 || !strings.HasPrefix(typed, "copy this into the docs\n\n<attached_images>\n") {
		t.Fatalf("typed %q: want the text, then a block with the two saved photos", typed)
	}
	kept, attached := claudePaste(typed)
	if !slices.Equal(attached, photos) {
		t.Errorf("Claude attaches %v, want each photo once: %v", attached, photos)
	}
	for _, p := range photos {
		if !strings.Contains(kept, p) {
			t.Errorf("the agent does not get the path %s; its text is %q", p, kept)
		}
	}
	// What Claude's transcript records (the bare lines gone) gives the apps no photo besides the attached pictures.
	if got := appPhotos(kept); len(got) != 0 {
		t.Errorf("the apps show %v from the transcript's text as well as the pictures", got)
	}
}

// Android's composer uploads the photos to the computer first and writes their paths in the block itself: those paths
// reach the agent too.
func TestSendKeepsPathsOfPhotosAClientUploaded(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shot.jpg")
	if err := os.WriteFile(p, []byte{0xff, 0xd8, 0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	typed := sendToClaude(t, "the button is off\n\n<attached_images>\n"+p+"\n</attached_images>", nil)
	kept, attached := claudePaste(typed)
	if !slices.Equal(attached, []string{p}) || !strings.Contains(kept, p) {
		t.Fatalf("Claude attaches %v and keeps %q: want the photo once and its path as text", attached, kept)
	}
	if got := appPhotos(typed); !slices.Equal(got, []string{p}) {
		t.Errorf("the apps show %v, want the photo once", got)
	}
}

func TestKeepImagePathsLeavesOtherTextAlone(t *testing.T) {
	block := "look\n\n<attached_images>\n/u/a.jpg\n/u/b.png\n</attached_images>"
	done := "look\n\n<attached_images>\n/u/a.jpg\npath: /u/a.jpg\n/u/b.png\npath: /u/b.png\n</attached_images>"
	for _, c := range []struct{ name, in, want string }{
		{"a path per photo", block, done},
		{"already has them", done, done},
		{"no block", "look at /u/a.jpg", "look at /u/a.jpg"},
		{"an empty block", "look\n<attached_images>\n</attached_images>", "look\n<attached_images>\n</attached_images>"},
		// a message that quotes the tag in prose is the user's words, not a list of photos
		{"prose in the tag", "the node writes <attached_images>\nhere\n</attached_images>", "the node writes <attached_images>\nhere\n</attached_images>"},
		{"block not at the end", "<attached_images>\n/u/a.jpg\n</attached_images>\nwhat is it?", "<attached_images>\n/u/a.jpg\n</attached_images>\nwhat is it?"},
	} {
		if got := keepImagePaths(c.in); got != c.want {
			t.Errorf("%s: keepImagePaths(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// A message with words and a picture, its path: lines added, is more lines than Claude Code (2.1.316) shows in its
// box: the box shows the picture's chip and a `[Pasted text #n …]` placeholder. Left there after the grace, it is the
// message still waiting and gets one Enter; the same placeholder after a short message without pictures is someone
// else's paste and gets none.
func TestEnsureSubmittedKnowsAPictureMessageBehindItsPlaceholder(t *testing.T) {
	withSubmitGrace(t, 100*time.Millisecond)
	placeholder := strings.Replace(screenFixture(t, "claude-2.1.312-typed-162.ansi"), "fix the flaky test", "[Image #1][Pasted text #2 +3 lines]", 1)
	empty := screenFixture(t, "claude-2.1.312-empty-162.ansi")
	text := keepImagePaths("fix the flaky test\n\n<attached_images>\n/tmp/a.jpg\n</attached_images>")

	stuck := &fakePane{screens: []string{placeholder, empty}}
	(&Node{hosts: herdrHosts(stuck.serve(t))}).ensureSubmitted(context.Background(), "w1:p1", text, time.Now(), nil)
	if len(stuck.keys) != 1 || stuck.keys[0][0] != "enter" {
		t.Fatalf("a picture message behind its placeholder: keys %v", stuck.keys)
	}

	other := &fakePane{screens: []string{placeholder}}
	(&Node{hosts: herdrHosts(other.serve(t))}).ensureSubmitted(context.Background(), "w1:p1", "fix the flaky test", time.Now(), nil)
	if len(other.keys) != 0 {
		t.Fatalf("pressed enter on someone else's paste: %v", other.keys)
	}
}
