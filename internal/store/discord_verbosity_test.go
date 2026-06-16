package store_test

import (
	"testing"

	"tether/internal/store"
)

func TestDiscordVerbosityDefaultAndSet(t *testing.T) {
	d := openTestDB(t)

	// Unset → default.
	if got := store.GetDiscordVerbosity(d, 1); got != store.DiscordVerbosityDefault {
		t.Fatalf("expected default %q, got %q", store.DiscordVerbosityDefault, got)
	}

	if err := store.SetDiscordVerbosity(d, 1, store.DiscordVerbosityMessageOnly); err != nil {
		t.Fatal(err)
	}
	if got := store.GetDiscordVerbosity(d, 1); got != store.DiscordVerbosityMessageOnly {
		t.Fatalf("expected %q, got %q", store.DiscordVerbosityMessageOnly, got)
	}

	// Invalid value is rejected on write.
	if err := store.SetDiscordVerbosity(d, 1, "bogus"); err == nil {
		t.Fatal("expected error for invalid verbosity")
	}

	// A corrupt stored value falls back to the default on read.
	if err := store.SetUserSetting(d, 2, "discord_verbosity", "garbage"); err != nil {
		t.Fatal(err)
	}
	if got := store.GetDiscordVerbosity(d, 2); got != store.DiscordVerbosityDefault {
		t.Fatalf("expected default for corrupt value, got %q", got)
	}
}
