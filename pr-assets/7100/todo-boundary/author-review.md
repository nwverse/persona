# Todo naming failure evidence

All twelve captures were individually inspected. The error occupies only the naming subtree; the same title and notes editors retain unsaved input. The regression checks their DOM identity, no prior PATCH, subsequent edits and actual Manager persistence. After an actual Reload, the naming editor opens and a new naming draft coexists with a new notes draft.

PageBoundary and lazyPage keep their existing recovery contract: one automatic full-page reload for a first chunk failure, then a visible Reload for persistent failure. This fixture starts in the latter state. Unsaved data surviving an arbitrary full-page reload is not claimed; the other fields are saved before clicking Reload.

The default phone images are viewport crops of a scrolling detail. Enlarged text uses application text scale 2, not browser or OS zoom. The recovered enlarged capture uses a 6000px viewport to paint virtualized content; its long blank remainder is fixture viewport space. Existing single-line title/placeholder truncation, coarse-pointer input sizing, and date/reminder overflow at enlarged application text remain visible. This change introduces no new visual tokens or layout rules and makes no general accessibility or contrast claim.

This bounded failure-isolation correction reuses existing components. Independent final-head review, human acceptance of the overall UI delivery and the maintainer visual/exact-head verification remain pending. These images are author evidence, not approval.
