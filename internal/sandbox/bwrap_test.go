package sandbox

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestLimitedBuffer_Truncates(t *testing.T) {
	b := &limitedBuffer{max: 5}
	_, _ = b.Write([]byte("hello"))
	if b.String() != "hello" || b.truncated {
		t.Fatalf("unexpected: %q truncated=%v", b.String(), b.truncated)
	}
	_, _ = b.Write([]byte("world"))
	if b.String() != "hello" || !b.truncated {
		t.Fatalf("expected truncated at max, got %q truncated=%v", b.String(), b.truncated)
	}
}

func TestBuildBwrapArgs_ContainsNoEtcBind(t *testing.T) {
	args, err := buildBwrapArgs("/tmp", []string{"bash", "-lc", "echo ok"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--unshare-net") {
		t.Fatalf("expected --unshare-net in %q", joined)
	}
	if strings.Contains(joined, " /etc ") || strings.Contains(joined, "--ro-bind /etc") {
		t.Fatalf("did not expect /etc to be bound: %q", joined)
	}
	if !strings.Contains(joined, "--clearenv") {
		t.Fatalf("expected --clearenv")
	}
}

func TestRunNoNet_EmptyCommand(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	_, err := RunNoNet(context.Background(), ".", nil)
	if err == nil {
		t.Fatalf("expected error")
	}
}
