package mail

import (
	"strings"
	"testing"
)

func TestReadableText(t *testing.T) {
	got := ReadableText("<html><style>p{color:red}</style><body><p>Hi Bob,\n  thanks</p><p>Two&nbsp;things:</p><ul><li>one</li><li>two</li></ul>Bye<br>Ann</body></html>")
	want := "Hi Bob, thanks\nTwo things:\n\n• one\n• two\nBye\nAnn"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// stripHTML matched tags in strings.ToLower(s) at s's byte offsets, but lowering can change a string's length:
// U+0130 (2 bytes) and the Kelvin sign (3 bytes) lower to one byte, an invalid byte grows into U+FFFD's three. A
// shorter copy panicked (slice bounds out of range) inside the IMAP fetch goroutine; a longer one let a <style>
// block's CSS through as text.
func TestStripHTMLWhenLoweringChangesLength(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"<p>İstanbul</p><p>示例用户</p>", "İstanbul 示例用户 "},
		{"<p>5 K</p>", "5 K "},
		{"\xff<style>body{color:red}</style>Hello", "\xffHello"},
		{"<STYLE>p{}</STYLE>İ<SCRIPT>x()</SCRIPT>ok", "İok"},
	} {
		if got := stripHTML(c.in); got != c.want {
			t.Errorf("stripHTML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The text of an HTML mail separates what the HTML lays out as separate things: table cells and rows, list items,
// headings and block quotes. Table- and list-built mail (receipts, notifications) otherwise reads as invented words
// ("Order#123Total$9").
func TestStripHTMLSeparatesBlockElements(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"<ul><li>one</li><li>two</li></ul>", "one two"},
		{"<ol><li>one</li><li>two</li></ol>after", "one two after"},
		{"<table><tr><td>Order</td><td>#123</td></tr><tr><td>Total</td><td>$9</td></tr></table>", "Order #123 Total $9"},
		{"<table><tr><th>Item</th><th>Price</th></tr><tr><td>Tea</td><td>$3</td></tr></table>done", "Item Price Tea $3 done"},
		{"<h1>Title</h1>Body<h2>Next</h2>More", "Title Body Next More"},
		{"<blockquote>quoted</blockquote>reply", "quoted reply"},
		{"<TABLE><TR><TD>Upper</TD><TD>Case</TD></TR></TABLE>", "Upper Case"},
		// Inline tags do not break a word.
		{"in<b>line</b> <span>te</span>xt <a href=\"https://example.com\">link</a>s", "inline text links"},
	} {
		if got := PlainText(c.in); got != c.want {
			t.Errorf("PlainText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// An HTML-only message gets that text as its Text and its list preview.
func TestParseMessageHTMLOnlySeparatesBlockElements(t *testing.T) {
	raw := "From: a@example.com\r\nTo: b@example.com\r\nSubject: Receipt\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<table><tr><td>Order</td><td>#123</td></tr><tr><td>Total</td><td>$9</td></tr></table><ul><li>one</li><li>two</li></ul>"
	var msg MailMessage
	parseMessage([]byte(raw), &msg)
	want := "Order #123 Total $9 one two"
	if got := strings.Join(strings.Fields(msg.Text), " "); got != want {
		t.Fatalf("Text = %q, want %q", got, want)
	}
}

// Only a message's body is its text: the title in the head, a comment's body and what follows a ">" inside a quoted
// attribute value are not text, however much of it the tag scanner would otherwise take for markup. An unclosed
// comment, quoted value or title drops the rest, as an unclosed <script> or <style> does.
func TestStripHTMLKeepsTitleCommentsAndTagInsidesOut(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"title", "<html><head><title>Monthly newsletter</title><meta charset=utf-8></head><body>Hello Bob</body></html>", "Hello Bob"},
		{"title in capitals", "<HEAD><TITLE>News</TITLE></HEAD>Body", "Body"},
		{"comment with >", "<!-- preheader > hidden --><p>Hi</p>", "Hi"},
		{"comment with a script", "<p>a</p><!-- <script>x</script> -->b", "a b"},
		{"conditional comment", "<!--[if mso]><table><tr><td>Open in a browser<![endif]--><p>Hi</p><!--[if mso]></td></tr></table><![endif]-->", "Hi"},
		{"> in a double-quoted value", `<p title="a > b">text</p>`, "text"},
		{"> in a single-quoted value", `<a href='x' title='a > b'>link</a> after`, "link after"},
		{"quote that opens no value", `<p it's fine>text</p>`, "text"},
		{"unclosed comment", "<p>kept</p><!-- the rest is a comment", "kept"},
		{"unclosed title", "<title>no end, <p>hidden</p>", ""},
		{"a tag whose name starts with title", "<titled>kept</titled>", "kept"},
	} {
		if got := PlainText(c.in); got != c.want {
			t.Errorf("%s: PlainText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
