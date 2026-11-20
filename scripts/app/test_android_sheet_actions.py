#!/usr/bin/env python3
import unittest
from pathlib import Path

from android_sheet_actions import violations


class PresentedPageActionsTest(unittest.TestCase):
    def findings(self, body, name='Example.kt'):
        return [problem for _, problem in violations(Path(name), body)]

    def test_homegrown_close_even_when_leading_or_multiline(self):
        self.assertEqual(3, len(self.findings("""
            fun ModalPage() {
              JIconButton(
                PersonaIcons.close, stringResource(R.string.designsystem_close), onClose)
              JIconButton(PhIcons.X, description, onClick = onDismiss)
              IconButton(onClick = exit) { Icon(Icons.Default.Close, contentDescription = "Close") }
            }
        """)))

    def test_end_slots_and_box_alignment(self):
        for body in (
            'JTopBar(title, actions = { if (visible) { JSheetClose(exit) } })',
            'JSheet(onDismiss, pageActions = { JSheetClose(exit) })',
            'JSheetClose(exit, Modifier.align(Alignment.TopEnd))',
            'JTopBar(title, actions = { JTextButton(stringResource(R.string.feature_done), onDismiss) })',
            'JPage(title, trailing = { JTextButton(stringResource(R.string.page_cancel), onDismiss) })',
        ):
            with self.subTest(body=body):
                self.assertEqual(1, len(self.findings(body)))

    def test_close_after_row_title_or_spacer(self):
        for before in ('Text(title, Modifier.weight(1f))', 'Spacer(Modifier.weight(1f))', 'JButton(label, save)'):
            self.assertEqual(1, len(self.findings(f'Row(Modifier.fillMaxWidth()) {{ {before}; JSheetClose(exit) }}')))
        self.assertEqual(1, len(self.findings('Row(horizontalArrangement = Arrangement.End) { JSheetClose(exit) }')))

    def test_leading_close_pushed_back_questions_and_inline_item_controls(self):
        self.assertEqual([], self.findings("""
            JTopBar(title, onClose = exit, actions = { JSheetConfirm(saveLabel, save) })
            Row(Modifier.fillMaxWidth()) { JSheetClose(exit); Text(title, Modifier.weight(1f)); JButton(sendLabel, send) }
            JIconButton(PhIcons.X, stringResource(R.string.remove_attachment), onRemove)
            JTopBar(title, actions = { JButton(stringResource(R.string.mark_done), markDone) })
            DatePickerDialog(confirmButton = { JButton(stringResource(R.string.done), { onPick(date); onDismiss() }) })
            JBackButton(onClick = pop)
            JDialog(onDismiss, confirm = ok to accept, dismiss = cancel to onDismiss)
            Column { JTextButton(stringResource(R.string.question_cancel), onDismiss) }
            // JSheetClose(onClose, Modifier.align(Alignment.TopEnd))
        """))

    def test_exemptions_are_scoped_to_the_existing_inline_component(self):
        def source(name):
            return f'fun {name}() {{ JIconButton(PhIcons.X, stringResource(R.string.terminal_close), close) }}'
        self.assertEqual([], self.findings(source('PaneRow'), 'TerminalPanesScreen.kt'))
        self.assertEqual(1, len(self.findings(source('NewPage'), 'TerminalPanesScreen.kt')))


if __name__ == '__main__':
    unittest.main()
