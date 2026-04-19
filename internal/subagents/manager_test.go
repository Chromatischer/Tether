package subagents

import (
	"context"
	"testing"
)

type runnerStub struct{}

func (runnerStub) Run(ctx context.Context, userID int64, req RunRequest, emit func(ProgressEvent)) (string, error) {
	emit(ProgressEvent{Type: "assistant", Text: "working through the task"})
	emit(ProgressEvent{Type: "tool_call", Text: "Tool calling: read"})
	emit(ProgressEvent{Type: "tool_result", Text: "Tool finished: read"})
	return "final answer", nil
}

func TestManagerCapturesProgressHistory(t *testing.T) {
	mgr := NewManager(runnerStub{})
	run := mgr.Spawn(7, RunRequest{Prompt: "do work"})

	got, ok := mgr.Wait(context.Background(), run.ID)
	if !ok || got == nil {
		t.Fatalf("wait failed: ok=%v run=%v", ok, got)
	}
	if got.Status != StatusDone {
		t.Fatalf("unexpected status: %s", got.Status)
	}
	if got.CurrentText != "final answer" {
		t.Fatalf("unexpected current text: %q", got.CurrentText)
	}
	if len(got.History) < 3 {
		t.Fatalf("expected progress history, got %#v", got.History)
	}
	if got.History[len(got.History)-1].Type != "done" {
		t.Fatalf("expected terminal done entry, got %#v", got.History[len(got.History)-1])
	}
}

func TestManagerGetForUserRejectsOtherUsersRuns(t *testing.T) {
	mgr := NewManager(runnerStub{})
	run := mgr.Spawn(7, RunRequest{Prompt: "do work"})

	if _, ok := mgr.Wait(context.Background(), run.ID); !ok {
		t.Fatal("wait failed")
	}
	if _, ok := mgr.GetForUser(8, run.ID); ok {
		t.Fatal("expected other user lookup to fail")
	}
	if got, ok := mgr.GetForUser(7, run.ID); !ok || got == nil {
		t.Fatal("expected owner lookup to succeed")
	}
}
