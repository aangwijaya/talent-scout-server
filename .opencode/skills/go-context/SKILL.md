---
name: go-context
description: Go context.Context: signature placement, cancellation, deadlines/timeouts, request-scoped values. Use when adding timeouts or cancellation (LLM calls, SSE streams, DB queries) or passing context through layers.
license: Apache-2.0
---

> Adapted from [cxuu/golang-skills](https://github.com/cxuu/golang-skills) `skills/go-context` @ 91f0c2e (Apache-2.0). Modified: narrowed trigger, removed scripts/assets and links to skills not installed here. See `../LICENSE-THIRD-PARTY.md`.

# Go Context Usage

> Compatibility: `context` has been in the standard library since Go 1.7.

## Resource Routing

## Context as First Parameter

Functions that use a Context should accept it as their **first parameter**:

```go
func F(ctx context.Context, /* other arguments */) error
func ProcessRequest(ctx context.Context, req *Request) (*Response, error)
```

This is a strong convention in Go that makes context flow visible and consistent
across codebases.

---

## Don't Store Context in Structs

Do not add a Context member to a struct type. Instead, pass `ctx` as a parameter
to each method that needs it:

```go
// Bad: Context stored in struct
type Worker struct {
    ctx context.Context  // Don't do this
}

// Good: Context passed to methods
type Worker struct{ /* ... */ }

func (w *Worker) Process(ctx context.Context) error {
    // Context explicitly passed — lifetime clear
}
```

**Exception**: Methods whose signature must match an interface in the standard
library or a third-party library may need to work around this.

---

## Don't Create Custom Context Types

Do not create custom Context types or use interfaces other than `context.Context`
in function signatures:

```go
// Bad: Custom context type
type MyContext interface {
    context.Context
    GetUserID() string
}

// Good: Use standard context.Context with value extraction
func Process(ctx context.Context) error {
    userID := GetUserID(ctx)
}
```

---

## Where to Put Application Data

Consider these options in order of preference:

1. **Function parameters** — most explicit and type-safe
2. **Receiver** — for data that belongs to the type
3. **Globals** — for truly global configuration (use sparingly)
4. **Context value** — only for request-scoped data

Context values are appropriate for:
- Request IDs and trace IDs
- Authentication/authorization info that flows with requests
- Deadlines and cancellation signals

Context values are **not** appropriate for:
- Optional function parameters
- Data that could be passed explicitly
- Configuration that doesn't vary per-request

---

## Common Patterns

### Deriving Contexts

Always `defer cancel()` immediately after creating a derived context:

```go
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()
```

### Checking Cancellation

```go
select {
case <-ctx.Done():
    return ctx.Err()
default:
    // Do work
}
```

### Context Immutability

Contexts are immutable — it's safe to pass the same `ctx` to multiple
concurrent calls that share the same deadline and cancellation signal.

---

## Related Skills

- **Goroutine coordination**: See [go-concurrency](../go-concurrency/SKILL.md) when using context for goroutine cancellation, select-based timeouts, or errgroup
- **Error handling**: See [go-error-handling](../go-error-handling/SKILL.md) when deciding how to wrap or return `ctx.Err()` cancellation errors
