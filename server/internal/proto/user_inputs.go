package proto

// UserInputPosition identifies one genuine input and carries optional excerpts for navigation.
type UserInputPosition struct {
	ID          string `json:"id"`
	Seq         int    `json:"seq"`
	Preview     string `json:"preview,omitempty"`
	Answer      string `json:"answer,omitempty"`
	Attachments int    `json:"attachments,omitempty"`
}

// UserInputIndex is the temporary web count/position contract. The richer #2071 API supersedes it when merged.
type UserInputIndex struct {
	Total   int                 `json:"total"`
	Inputs  []UserInputPosition `json:"inputs"`
	History string              `json:"history"`
}
