package feishubot

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	mdtext "github.com/yuin/goldmark/text"
)

type codeRange struct{ start, end int }
type codeLine struct{ code, opening bool }

// CodeFence walks the original lines of Markdown, identifying fenced code without
// rendering it. NewCodeFence supplies the ranges, including container prefixes.
type CodeFence struct {
	lines   []codeLine
	next    int
	opening bool
}

// NewCodeFence uses the existing CommonMark parser to find fences, including
// blockquotes and lists. Only source ranges are kept; no rendered text is used.
func NewCodeFence(md string) CodeFence {
	p := &fenceRangeParser{BlockParser: parser.NewFencedCodeBlockParser(), source: []byte(md), active: map[ast.Node]int{}}
	blocks := parser.DefaultBlockParsers()
	for i := range blocks {
		if blocks[i].Value == p.BlockParser {
			blocks[i].Value = p
		}
	}
	parser.NewParser(parser.WithBlockParsers(blocks...)).Parse(mdtext.NewReader(p.source))
	var lines []codeLine
	at, next := 0, 0
	for _, line := range strings.SplitAfter(md, "\n") {
		for next < len(p.ranges) && p.ranges[next].end <= at {
			next++
		}
		code, opening := false, false
		if next < len(p.ranges) {
			code = p.ranges[next].start <= at && at < p.ranges[next].end
			opening = p.ranges[next].start == at
		}
		lines = append(lines, codeLine{code, opening})
		at += len(line)
	}
	return CodeFence{lines: lines}
}

// Next reports whether the next line belongs to a fence, including delimiters.
// The line map also survives list indentation changes, which retain line count.
func (f *CodeFence) Next() bool {
	f.opening = false
	if f.next >= len(f.lines) {
		return false
	}
	line := f.lines[f.next]
	f.next++
	f.opening = line.opening
	return line.code
}

// Wrap the parser's fence callbacks to retain complete raw lines, including the
// delimiters, which FencedCodeBlock.Lines intentionally leaves out.
type fenceRangeParser struct {
	parser.BlockParser
	source []byte
	ranges []codeRange
	active map[ast.Node]int
}

func (p *fenceRangeParser) Open(parent ast.Node, r mdtext.Reader, pc parser.Context) (ast.Node, parser.State) {
	_, segment := r.PeekLine()
	node, state := p.BlockParser.Open(parent, r, pc)
	if node != nil {
		start := bytes.LastIndexByte(p.source[:segment.Start], '\n') + 1
		p.active[node] = len(p.ranges)
		p.ranges = append(p.ranges, codeRange{start, segment.Stop})
	}
	return node, state
}

func (p *fenceRangeParser) Continue(node ast.Node, r mdtext.Reader, pc parser.Context) parser.State {
	_, segment := r.PeekLine()
	state := p.BlockParser.Continue(node, r, pc)
	p.ranges[p.active[node]].end = segment.Stop
	return state
}

func (p *fenceRangeParser) Close(node ast.Node, r mdtext.Reader, pc parser.Context) {
	p.BlockParser.Close(node, r, pc)
	delete(p.active, node)
}
