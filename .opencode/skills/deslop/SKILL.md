---
name: deslop
description: Remove AI-generated code slop from the branch diff (needless comments, abnormal defensive code, type escapes, deep nesting) without changing behavior. Run before the green checks, before opening a PR.
license: MIT (adapted, see ../LICENSE-THIRD-PARTY.md)
compatibility: opencode
---

# Remove AI code slop

> Adapted from cursor/plugins (cursor-team-kit/skills/deslop), MIT License, Copyright (c) 2026 Cursor. See `../LICENSE-THIRD-PARTY.md`.

Check the diff against main and remove AI-generated slop introduced in the branch:
```bash
git fetch origin main && git diff origin/main...HEAD
```

## Focus Areas

- Extra comments that are unnecessary or inconsistent with local style (comments restating the code, "// Added for issue #n")
- Defensive checks or try/catch / recover blocks that are abnormal for trusted code paths
- `interface{}`/`any` or type assertions used only to silence the compiler; ignored `err`
- Deeply nested `if err != nil` pyramids that should be early returns
- Dead code, unused helpers, debug prints/logs left behind
- Other patterns inconsistent with the file and surrounding codebase

## Guardrails

- Only touch lines changed in this branch. Keep behavior unchanged unless fixing a clear bug.
- Prefer minimal, focused edits over broad rewrites.
- Re-run the green checks afterwards: `test -z "$(gofmt -l .)" && go build ./... && go vet ./... && go test ./...`
- Keep the final summary concise (1-3 sentences).
