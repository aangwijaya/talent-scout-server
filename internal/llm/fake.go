package llm

import "context"

// Fake is the deterministic LLM used in tests and CI (LLM_MODE=fake). It never
// makes a network call and always succeeds.
type Fake struct {
	responses map[string]string
}

// NewFake returns a Fake client. responses maps a graph node id (preferred, so
// nodes sharing a model get distinct replies) or a model id to a canned reply;
// anything without an entry returns a deterministic default.
func NewFake(responses map[string]string) *Fake {
	return &Fake{responses: responses}
}

func (f *Fake) Chat(_ context.Context, req Request) (Response, error) {
	content := f.content(req)
	return Response{Content: content, Usage: fakeUsage(req, content)}, nil
}

func (f *Fake) Stream(_ context.Context, req Request, onDelta func(string) error) (Response, error) {
	content := f.content(req)
	if onDelta != nil {
		if err := onDelta(content); err != nil {
			return Response{}, err
		}
	}
	return Response{Content: content, Usage: fakeUsage(req, content)}, nil
}

// content returns the canned reply for the node (first), then the model, or a
// deterministic default. JSON nodes default to an empty object so callers that
// unmarshal succeed.
func (f *Fake) content(req Request) string {
	if f.responses != nil {
		if c, ok := f.responses[req.Node]; ok && req.Node != "" {
			return c
		}
		if c, ok := f.responses[req.Model]; ok {
			return c
		}
	}
	if req.ResponseFormat != nil {
		return "{}"
	}
	return "fake reply for " + req.Model
}

// fakeUsage derives deterministic token counts from the input and output so
// cost-recording tests are stable.
func fakeUsage(req Request, content string) Usage {
	prompt := 1
	for _, m := range req.Messages {
		prompt += len(m.Content)
	}
	completion := 1 + len(content)
	return Usage{
		PromptTokens:     (prompt + 3) / 4,
		CompletionTokens: (completion + 3) / 4,
	}
}

// Ensure Fake satisfies Client.
var _ Client = (*Fake)(nil)
