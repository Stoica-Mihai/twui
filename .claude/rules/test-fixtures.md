---
paths:
  - "**/*_test.go"
---

# Test fixture names

Do not use real Twitch streamer handles — especially well-known or popular ones — as test fixtures.

**Why:** Hardcoding identifiable people into unit-test scaffolding is distasteful, and real handles drift (retirement, rename, ban). Keep test data semantically neutral.

**How to apply:**
- If you need a Twitch-handle-shaped string (e.g. IRC `JOIN #name` parsing, login validation), use `MeNotSanta` — that's the repo owner's handle, safe to reference.
- Otherwise use generic placeholders: `chanA`, `chan1`, `a`, `b`, `alpha`, `beta`.
- **Exception:** tests that exercise the Twitch GQL/API layer can keep load-bearing handles if the specific handle is what the test is validating. Even there, prefer `MeNotSanta` when the handle isn't the point.
