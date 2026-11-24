package recordingwire

// MinutesItem is a decision or an open question; At is seconds from the recording's start (a segment's start, or the
// photo's time when it cites photo Photo).
type MinutesItem struct {
	Text  string  `json:"text"`
	At    float64 `json:"at"`
	Photo int     `json:"photo,omitempty"`
}

// MinutesPoint is a point discussed; Who only when the transcript or the calendar names the person.
type MinutesPoint struct {
	Text  string  `json:"text"`
	At    float64 `json:"at"`
	Photo int     `json:"photo,omitempty"`
	Who   string  `json:"who,omitempty"`
}

// MinutesAction is an action item: a suggested to-do until the user confirms it (TodoID then).
type MinutesAction struct {
	ID      string  `json:"id"`
	Who     string  `json:"who,omitempty"`
	What    string  `json:"what"`
	By      string  `json:"by,omitempty"`       // the deadline as said
	DueDate string  `json:"due_date,omitempty"` // YYYY-MM-DD, when By is one date
	At      float64 `json:"at"`
	Photo   int     `json:"photo,omitempty"`
	TodoID  string  `json:"todo_id,omitempty"`
}
