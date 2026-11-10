package assistant

// meetingKick tells the feeder of attended meetings (docs/assistant/meeting-minutes.md "Feeding the attending session")
// that recording id changed: an upload of it was stored, live sentences of it were appended, or one of its questions
// was answered. A stub, replaced by the feeder.
func (s *Service) meetingKick(id string) {}
