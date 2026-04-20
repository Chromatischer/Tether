package discord

import (
	"strings"
	"testing"
)

func TestDetectDiscordChoiceCount(t *testing.T) {
	msg := "Pick one:\n1. Alpha\n2. Beta\n3. Gamma\n\nReply with just the number."
	if got := detectDiscordChoiceCount(msg); got != 3 {
		t.Fatalf("expected 3 choices, got %d", got)
	}
}

func TestDetectDiscordChoiceCountRejectsNonContiguous(t *testing.T) {
	msg := "Pick one:\n1. Alpha\n3. Gamma\n\nReply with just the number."
	if got := detectDiscordChoiceCount(msg); got != 0 {
		t.Fatalf("expected 0 choices for non-contiguous list, got %d", got)
	}
}

func TestDetectDiscordChoiceCountRequiresReplyInstruction(t *testing.T) {
	msg := "Options:\n1. Alpha\n2. Beta"
	if got := detectDiscordChoiceCount(msg); got != 0 {
		t.Fatalf("expected 0 choices without reply instruction, got %d", got)
	}
}

func TestDiscordChoiceEmojiRoundTrip(t *testing.T) {
	for i := 1; i <= 10; i++ {
		emoji, ok := discordChoiceEmoji(i)
		if !ok {
			t.Fatalf("missing emoji for %d", i)
		}
		n, ok := discordChoiceNumberFromEmoji(emoji)
		if !ok || n != i {
			t.Fatalf("round trip failed for %d: %q -> %d %v", i, emoji, n, ok)
		}
	}
}

func TestRenderDiscordPreviewReasoningBeforeAnswer(t *testing.T) {
	got := renderDiscordPreview([]string{"web-fetch", "bash"}, "thinking...", "")
	want := "tool: web-fetch\ntool: bash\n\nthinking..."
	if got != want {
		t.Fatalf("preview mismatch\nwant: %q\ngot:  %q", want, got)
	}
}

func TestRenderDiscordPreviewHidesReasoningOnceAnswerStarts(t *testing.T) {
	got := renderDiscordPreview([]string{"bash"}, "internal reasoning", "final answer")
	want := "tool: bash\nfinal answer"
	if got != want {
		t.Fatalf("preview mismatch\nwant: %q\ngot:  %q", want, got)
	}
}

func TestSplitDiscordMessage(t *testing.T) {
	msg := strings.Repeat("a", 1500) + "\n" + strings.Repeat("b", 1500)
	chunks := splitDiscordMessage(msg, 1900)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if len(chunk) > 1900 {
			t.Fatalf("chunk %d too long: %d", i, len(chunk))
		}
	}
	if chunks[0] != strings.Repeat("a", 1500) {
		t.Fatalf("expected first chunk to break on newline")
	}
	if chunks[1] != strings.Repeat("b", 1500) {
		t.Fatalf("expected second chunk to contain remaining text")
	}
}

func TestTruncateDiscordPreview(t *testing.T) {
	msg := "tool: bash\n" + strings.Repeat("x", 3000)
	got := truncateDiscordPreview(msg, 1900)
	if len(got) > 1900 {
		t.Fatalf("preview too long: %d", len(got))
	}
	if !strings.HasSuffix(got, "\n…") {
		t.Fatalf("expected ellipsis suffix, got %q", got[len(got)-4:])
	}
}
