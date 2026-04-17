---
paths:
  - "**/*_test.go"
---

# Test fixture names

Do not use popular/famous Twitch streamer handles (shroud, xqc, zackrawrr, pokimane, forsen, lirik, insym, trick2g, eslcs, etc.) as test fixtures.

**Why:** Hardcoding real people's identities into unit-test scaffolding is distasteful, and popular handles drift (retirement, rename, ban). Keep test data semantically neutral.

**How to apply:**
- If you need a Twitch-handle-shaped string (e.g. IRC `JOIN #name` parsing, login validation), use `MeNotSanta` — that's the repo owner's handle, safe to reference.
- Otherwise use generic placeholders: `chanA`, `chan1`, `a`, `b`, `alpha`, `beta`.
- **Exception:** tests that exercise the Twitch GQL/API layer can keep load-bearing handles if the specific handle is what the test is validating. Even there, prefer `MeNotSanta` when the handle isn't the point.
