package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/compozy/compozy/pkg/compozy/events"
	"github.com/compozy/compozy/pkg/compozy/events/kinds"
)

func TestRenderObservedRunCompletedDisplaysTrimmedHandoffSummary(t *testing.T) {
	t.Parallel()

	event := encodeObservedEvent(t, events.EventKindRunCompleted, kinds.RunCompletedPayload{
		SummaryMessage: "  parallel handoff ready\nhandoff: /runs/parent/parallel-handoff.md\nprompt:\nReview worktrees.  ",
	})

	got := renderObservedRunCompleted(event)
	if strings.HasPrefix(got, "run completed |   ") {
		t.Fatalf("summary was not trimmed: %q", got)
	}
	if !strings.Contains(got, "handoff: /runs/parent/parallel-handoff.md") ||
		!strings.Contains(got, "prompt:\nReview worktrees.") {
		t.Fatalf("rendered summary missing expected handoff content: %q", got)
	}
}

func TestRenderObservedRunFailedDisplaysTrimmedHandoffSummary(t *testing.T) {
	t.Parallel()

	event := encodeObservedEvent(t, events.EventKindRunFailed, kinds.RunFailedPayload{
		Error:          "child task failed",
		SummaryMessage: "  parallel handoff ready\nhandoff: /runs/parent/parallel-handoff.md\nprompt:\nReview failed worktrees.  ",
	})

	got := renderObservedRunFailed(event)
	if strings.HasPrefix(got, "run failed |   ") {
		t.Fatalf("summary was not trimmed: %q", got)
	}
	if !strings.Contains(got, "run failed | child task failed") ||
		!strings.Contains(got, "handoff: /runs/parent/parallel-handoff.md") ||
		!strings.Contains(got, "prompt:\nReview failed worktrees.") {
		t.Fatalf("rendered failure missing expected handoff content: %q", got)
	}
}

func encodeObservedEvent[T any](t *testing.T, kind events.EventKind, payload T) events.Event {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return events.Event{RunID: "run-123", Kind: kind, Payload: encoded}
}
