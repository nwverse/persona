"""A meeting's final pass in progress, for shots of the minutes line (#13461, docs/assistant/meeting-minutes.md
"Gaps and the final pass").

MOCK_MEETING_FINISHING=<done>,<end> (recording seconds) shows every complete meeting recording with no transcription of
its own as a hub-mode meeting whose final pass has done <done> of <end>; its minutes stay as MOCK_RECORDING_MINUTES says.
mock_hub.py's transcription_view reads the pair from its MEETING_FINISHING global, which this domain sets.
"""
import os

VALUE = os.environ.get("MOCK_MEETING_FINISHING", "")


def register(d):
    d.help("MOCK_MEETING_FINISHING=<done>,<end>  complete meetings without a transcription of their own show a final "
           "pass at <done> of <end> recording seconds (#13461)")
    if not VALUE:
        return
    d.hub.MEETING_FINISHING = tuple(float(x) for x in VALUE.split(","))
