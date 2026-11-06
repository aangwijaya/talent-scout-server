---
name: Task
about: One node for an agent or a human
title: ""
labels: ["status:todo"]
assignees: []
---

<!-- Labels: agent:claude | agent:opencode | agent:human, type:feat | type:bug | type:chore | type:gap. Add contract-change for docs/API.md, docs/EVENTS.md, or docs/PRD.md. -->

## Goal
<!-- 1–3 sentences: the desired outcome and why -->

## Acceptance criteria
- [ ] ...

## Docs to read
- `AGENTS.md`
- `docs/LESSONS.md`

## Out of scope
- ...

## Node
```yaml
fence: [internal/example/*]
blocked_by: []
checks: [go test ./...]
```
