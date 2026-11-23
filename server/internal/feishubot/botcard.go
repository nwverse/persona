package feishubot

// The bot's card and event model lives in internal/botcard (pure, no I/O); these names keep the package's callers
// compiling while they move to botcard (#13404).

import "github.com/nwverse/persona/server/internal/botcard"

type (
	APIError     = botcard.APIError
	Answer       = botcard.Answer
	AnswerPart   = botcard.AnswerPart
	Button       = botcard.Button
	Card         = botcard.Card
	CardAction   = botcard.CardAction
	CardPayload  = botcard.CardPayload
	CardResponse = botcard.CardResponse
	Content      = botcard.Content
	CotEvent     = botcard.CotEvent
	Envelope     = botcard.Envelope
	Item         = botcard.Item
	Media        = botcard.Media
	Mention      = botcard.Mention
	MenuItem     = botcard.MenuItem
	MessageEvent = botcard.MessageEvent
	Option       = botcard.Option
	P2PEntered   = botcard.P2PEntered
	Recalled     = botcard.Recalled
	Toast        = botcard.Toast
	UserID       = botcard.UserID
)

const (
	Blue            = botcard.Blue
	Danger          = botcard.Danger
	Default         = botcard.Default
	EventCardAction = botcard.EventCardAction
	EventMessage    = botcard.EventMessage
	EventP2PEntered = botcard.EventP2PEntered
	EventRecalled   = botcard.EventRecalled
	Green           = botcard.Green
	Grey            = botcard.Grey
	Neutral         = botcard.Neutral
	Orange          = botcard.Orange
	Primary         = botcard.Primary
	Purple          = botcard.Purple
	Red             = botcard.Red
	Wathet          = botcard.Wathet
)

var (
	AICommand            = botcard.AICommand
	Btn                  = botcard.Btn
	CardEntityContent    = botcard.CardEntityContent
	CardMarkdown         = botcard.CardMarkdown
	CardRejected         = botcard.CardRejected
	ChatRefused          = botcard.ChatRefused
	Code                 = botcard.Code
	Confirm              = botcard.Confirm
	CotFinish            = botcard.CotFinish
	CotParagraph         = botcard.CotParagraph
	CotStart             = botcard.CotStart
	CotToolCall          = botcard.CotToolCall
	CotToolResult        = botcard.CotToolResult
	ErrorCode            = botcard.ErrorCode
	EscapeMarkdown       = botcard.EscapeMarkdown
	InlineAnswer         = botcard.InlineAnswer
	InlineTag            = botcard.InlineTag
	Input                = botcard.Input
	LinkBtn              = botcard.LinkBtn
	LocalTime            = botcard.LocalTime
	MarkdownBlock        = botcard.MarkdownBlock
	MessageGone          = botcard.MessageGone
	MsTime               = botcard.MsTime
	NewCard              = botcard.NewCard
	Overflow             = botcard.Overflow
	PageBtn              = botcard.PageBtn
	ParseContent         = botcard.ParseContent
	ParseEnvelope        = botcard.ParseEnvelope
	PersonaCards         = botcard.PersonaCards
	PostMarkdown         = botcard.PostMarkdown
	RateLimited          = botcard.RateLimited
	ReadCommand          = botcard.ReadCommand
	Refused              = botcard.Refused
	RenderAnswer         = botcard.RenderAnswer
	RenderAnswerWith     = botcard.RenderAnswerWith
	Select               = botcard.Select
	SidebarURL           = botcard.SidebarURL
	Split                = botcard.Split
	StripLeadingMentions = botcard.StripLeadingMentions
	Submit               = botcard.Submit
	TextBtn              = botcard.TextBtn
	TextContent          = botcard.TextContent
	TokenGone            = botcard.TokenGone
	Transient            = botcard.Transient
)

// Unexported names of the moved helpers the client still uses.
var (
	tokenGone = botcard.TokenGone
	msTime    = botcard.MsTime
	errorCode = botcard.ErrorCode
)
