package main

import "strings"

// Lang is a language the page is written in: a closed set, the hub's three (docs/protocol/i18n.md).
type Lang uint8

const (
	LangEN Lang = iota
	LangZhHans
	LangJA
)

// langOf reads the app's locale (ui.render's locale: "zh-Hans", "ja-JP", "en"…); anything else is English.
func langOf(locale string) Lang {
	l := strings.ToLower(locale)
	switch {
	case strings.HasPrefix(l, "zh"):
		return LangZhHans
	case strings.HasPrefix(l, "ja"):
		return LangJA
	}
	return LangEN
}

// Text is one string the page shows: a closed set, each in every language (TestEveryTextIsTranslated).
type Text uint8

const (
	TextTitle Text = iota
	TextNewNote
	TextPlaceholder
	TextAdd
	TextNotes
	TextEmpty
	TextImportant
	TextTapToMark
	TextStoredFooter
	textCount
)

var texts = [textCount][3]string{
	TextTitle:        {"Quick notes", "速记", "クイックメモ"},
	TextNewNote:      {"New note", "新速记", "新しいメモ"},
	TextPlaceholder:  {"Write something short", "写点什么", "短いメモを書く"},
	TextAdd:          {"Add", "添加", "追加"},
	TextNotes:        {"Notes", "速记", "メモ"},
	TextEmpty:        {"No notes yet", "还没有速记", "メモはまだありません"},
	TextImportant:    {"Important", "重要", "重要"},
	TextTapToMark:    {"Tap a note to mark it important or plain.", "点一条速记，在重要和普通之间切换。", "メモをタップすると、重要と通常を切り替えます。"},
	TextStoredFooter: {"Notes stay stored when this extension is switched off.", "关掉这个扩展后，速记仍然保存着。", "この拡張機能をオフにしても、メモは保存されたままです。"},
}

func (t Text) In(l Lang) string { return texts[t][l] }
