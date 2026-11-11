# Native HarmonyOS views

These screenshots come from the running API 26 HAP, using only the built-in synthetic Demo workspace. They contain no pairing credentials or private Hub content. They are visual evidence, not a substitute for the behavior tests in [the client guide](../harmony.md#verification).

|Device/window|Views|Expected structure|
|---|---|---|
|Phone, 1256 × 2760|[Agents](phone-agents-light.png), [detail](phone-agent-detail-light.png), [200% text with IME](phone-large-text-ime.png)|Native stack; route title and return on detail; primary tabs only at root|
|Conventional foldable, cover 1080 × 2504|[Cover list](fold-cover-agents.png), [cover detail](fold-cover-agent.png)|Native stack|
|Conventional foldable, inner 2224 × 2496 / rotated 2496 × 2224|[Expanded](fold-expanded-agent.png), [landscape](fold-landscape-agent.png)|Density 3.125 gives roughly 712 / 799 vp: both MD windows use stack|
|Wide foldable, 2584 × 1828|[Glass light](wide-agents-light.png), [Glass dark](wide-agents-dark.png), [Brief](wide-brief-light.png), [Board dark](wide-board-dark.png)|Density 2.75 gives roughly 940 vp: LG uses native Navigation split and drag bar|
|Wide foldable, title conditions|[Collapsed pane](wide-collapsed-title.png), [selected row excluded by filter](wide-filter-title.png)|Restore the native detail title whenever the left row no longer identifies the current Agent|
|Wide foldable, child action|[Inbox synchronized](wide-inbox-synchronized.png)|One native detail title; the parent row reflects the acknowledged child mutation|

Todo evidence: [renamed native title](wide-todo-title-updated.png) and [completed item on return](wide-todo-completed-parent.png). Knowledge evidence: [saved tag visible on return](wide-knowledge-parent-updated.png). These flows require no manual refresh.

The title, primary tabs, controls, menu surfaces and material treatments belong to ArkUI. In compact English layouts the native capsule can abbreviate a visual label; its full accessibility label remains available. Do not infer a layout breakpoint from the foldable model name.

Capture provenance: phone list/detail, expanded/rotated fold detail, cover detail, wide light and Todo use Release142 (HAP SHA-256 `ddbf23dc5517b6d19259afb49856e0cac93b4332f8a8132ae5a607a432211443`). The cover list, dark/style and title-condition views use Release134; their relevant layout/style source is unchanged. The 200% IME view uses Release135, Inbox synchronization Release138, and Knowledge return synchronization Release141. Build numbers are local evidence identifiers, not product release versions.

The current conventional-fold emulator omits crease geometry. These images validate ordinary native layout and target continuity; they do not claim horizontal-hover or hinge-avoidance acceptance. Physical-device material, accessibility and performance still need device qualification.
