# Scheduled task execution form

Synthetic browser evidence for the task editor's optional execution configuration.
The browser uses intercepted HTTP and WebSocket fixtures exclusively; no hub, real task or model is contacted.

Run against an owned loopback Vite server:

```sh
node web/e2e/task-execution.mjs http://127.0.0.1:5173 \
  --shots docs/web/screenshots/task-execution --chromium <chromium-executable>
```

The fixture selects an agent and its advertised model, saves a working directory, reopens the task, and checks the
saved values and model discovery. It captures light/dark at 1280 and 390 pixels, then doubles text size and checks
that form controls stay inside the dialog. Screenshots have been visually inspected. The largest-text form scrolls;
its footer remains available. Native selects and the shared model picker keep full values available even when their
collapsed text is truncated at the largest size.

The form reuses the existing dialog, fields, native selects, model picker and semantic tokens. Advanced options are
collapsed by default. This extends a secondary form and leaves primary screens and themes unchanged.
