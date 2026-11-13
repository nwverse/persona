# Terminal appearance font validation

Refs #5976; supplemental CI guard correction in PR #6300.

Source: ba630ab8f4b71d82ba0ce6e699dd617132c150f0. Real local Web app with an independent synthetic mock hub; no real account or conversation data. Appearance labels use the existing named typography. Desktop 1440 x 1000, narrow 390 x 844; screenshots inspected in light and dark.

The unchanged Mouse and Cursor style controls still truncate in the existing popover. This change does not claim to repair those controls. No native client or deployed environment was tested.

Maximum application text-size narrow captures are also included. These use the identical TerminalOptions source blob 86dbacf2e59bae8d498c7996926ea6afa7ea4917 from the integration branch.
