# Workspace naming: author visual inspection

Product source: `be6b2f25026caac50118e91e9b5312c48894c360`. Main adopted: `576970b459ee569997fbebab487cd4220dac67f8`. The manifest binds the server, web and spec trees and every image hash and dimension. This report covers the final review-fix product, not an independent approval.

The author opened all 20 images individually. Settings and task controls retain their existing layout; desktop options align horizontally and phone options stack. Preview paths and branches wrap without horizontal overflow. The four recovered states show the remaining repository, the retained unsaved branch draft and a successful result after the real manager rejected a stale repository selection with 404. The browser checked controls at the normal viewport before taking a full-editor crop in a 6000px-high viewport.

| Image | Inspection |
| --- | --- |
| `settings-light-1440.jpg` | Desktop fields and preview row align; complete preview visible. |
| `settings-light-390.jpg` | Phone controls stack and the full preview wraps; long native input placeholders truncate. |
| `settings-dark-1440.jpg` | Desktop fields and preview row align; complete preview visible. |
| `settings-dark-390.jpg` | Phone controls stack and the full preview wraps; long native input placeholders truncate. |
| `settings-200-light-1440.jpg` | Desktop text and inputs enlarge; preview output wraps; native single-line inputs/selects truncate. |
| `settings-200-light-390.jpg` | Labels, help and output enlarge and wrap; inputs remain 16px under the existing coarse-pointer rule; native controls truncate. |
| `settings-200-dark-1440.jpg` | Desktop text and inputs enlarge; preview output wraps; native single-line inputs/selects truncate. |
| `settings-200-dark-390.jpg` | Labels, help and output enlarge and wrap; inputs remain 16px under the existing coarse-pointer rule; native controls truncate. |
| `task-light-1440.jpg` | Desktop fields and preview row align; complete preview visible. |
| `task-light-390.jpg` | Phone controls stack and the full preview wraps; long native input placeholders truncate. |
| `task-dark-1440.jpg` | Desktop fields and preview row align; complete preview visible. |
| `task-dark-390.jpg` | Phone controls stack and the full preview wraps; long native input placeholders truncate. |
| `task-200-light-1440.jpg` | Desktop text and inputs enlarge; preview output wraps; native single-line inputs/selects truncate. |
| `task-200-light-390.jpg` | Labels, help and output enlarge and wrap; inputs remain 16px under the existing coarse-pointer rule; native controls truncate. |
| `task-200-dark-1440.jpg` | Desktop text and inputs enlarge; preview output wraps; native single-line inputs/selects truncate. |
| `task-200-dark-390.jpg` | Labels, help and output enlarge and wrap; inputs remain 16px under the existing coarse-pointer rule; native controls truncate. |
| `recovered-light-1440.jpg` | Remaining repository and unsaved draft visible; successful one-repository preview; no stale error. |
| `recovered-light-390.jpg` | Remaining repository and unsaved draft visible; successful one-repository preview; no stale error. |
| `recovered-dark-1440.jpg` | Remaining repository and unsaved draft visible; successful one-repository preview; no stale error. |
| `recovered-dark-390.jpg` | Remaining repository and unsaved draft visible; successful one-repository preview; no stale error. |

## Limits

No new visual defect was observed in these captured states. Application text scale 2 is not browser or OS zoom. On coarse-pointer phones, inputs remain 16px while labels/help/output scale; desktop inputs scale from 12.5px to 25px. Native single-line inputs, placeholders and closed selects can truncate long text. This does not claim universal 200% accessibility, measured contrast or full-value interaction accessibility.

The previous independent Agent report remains historical evidence for its own pinned images. The current 20 images were inspected by the author. Human/maintainer screenshot approval and the maintainer's configured `verify.ui_evidence_command` remain separate pending gates. No bypass or approval is asserted.
