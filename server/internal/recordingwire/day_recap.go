package recordingwire

// DayRecapEntry is one thing of the day's timeline: a meeting (one the hub knows of when Meeting is set) or what was
// said outside them, with its points.
type DayRecapEntry struct {
	From    float64        `json:"from"`
	To      float64        `json:"to"`
	Kind    string         `json:"kind"`
	Title   string         `json:"title"`
	Meeting *MeetingRef    `json:"meeting,omitempty"`
	Points  []MinutesPoint `json:"points"`
}

// RecapTodo is a to-do made of a recap's action, remembered whatever the version: the action's who and what
// (recapTaskKey) and when it was said.
type RecapTodo struct {
	Task string  `json:"task"`
	At   float64 `json:"at"`
	Todo string  `json:"todo"`
}

// Why a recap became due (RecapTarget.Reason).
const (
	RecapDueEvening  = "evening"  // the recap time of its day
	RecapDueComplete = "complete" // the recording became complete: the final recap
	RecapDueCutoff   = "cutoff"   // 6 hours after its day ended, not complete: the final recap of what arrived
	RecapDueAsked    = "asked"    // POST …/minutes
	RecapDueRepair   = "repair"   // a PUT set a new base while a recap existed
)

// Why a final recap is final (DayRecap.FinalReason).
const (
	FinalComplete = "complete"
	FinalCutoff   = "cutoff"
)

// Kinds of a recap's timeline entry.
const (
	RecapMeeting = "meeting"
	RecapOutside = "outside"
)
