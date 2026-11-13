# Conversation deletion Web validation

Refs #6172. Web source 2baf66b5e with no Web diff between integrated main 2e85b78e and 934a2c4e7.

Real local Web components with a separate synthetic mock API; no real accounts, conversations or services.
Desktop 1440 x 1000, narrow 390 x 844; standard and the application's largest text size. All screenshots inspected.

An actual synthetic send appeared in the open global history search; an external mock deletion removed it from both the panel and browser history. An archived former-main chat was deleted through the confirmation dialog; its old link shows a tombstone and has no composer. The independent PostgreSQL manager and HTTP race tests validate the backend separately. These screenshots do not claim a live Go backend browser end-to-end test or native client validation.
