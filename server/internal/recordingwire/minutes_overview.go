package recordingwire

// MinutesConclusion is a core conclusion: where the meeting landed, citing a line, or a photo a bullet cites.
type MinutesConclusion struct {
	Text   string   `json:"text"`
	Strong []string `json:"strong,omitempty"`
	At     float64  `json:"at"`
	Photo  int      `json:"photo,omitempty"`
}

// MinutesGroup is a heading over topics; Title is "" for the single untitled group.
type MinutesGroup struct {
	Title  string         `json:"title"`
	Topics []MinutesTopic `json:"topics"`
}

// MinutesTopic is a topic card: Icon one of minutesTopicIcons.
type MinutesTopic struct {
	Title   string          `json:"title"`
	Icon    string          `json:"icon"`
	Bullets []MinutesBullet `json:"bullets"`
}

// MinutesBullet is a point (Who as the point's) or a decision (Decision) of the minutes, with the same Text, At and
// Photo; Strong the places of Text set in bold (strongSpans).
type MinutesBullet struct {
	Text     string   `json:"text"`
	Strong   []string `json:"strong,omitempty"`
	At       float64  `json:"at"`
	Photo    int      `json:"photo,omitempty"`
	Who      string   `json:"who,omitempty"`
	Decision bool     `json:"decision,omitempty"`
}
