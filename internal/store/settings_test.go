package store_test

import (
	"testing"

	"tether/internal/store"
)

func TestGetSetUserSetting(t *testing.T) {
	d := openTestDB(t)
	_, ok, err := store.GetUserSetting(d, 1, "confirm_strictness")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no value before set")
	}

	if err := store.SetUserSetting(d, 1, "confirm_strictness", "always"); err != nil {
		t.Fatal(err)
	}
	val, ok, err := store.GetUserSetting(d, 1, "confirm_strictness")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || val != "always" {
		t.Fatalf("expected 'always', got %q ok=%v", val, ok)
	}
}

func TestGetAllUserSettings(t *testing.T) {
	d := openTestDB(t)
	_ = store.SetUserSetting(d, 1, "k1", "v1")
	_ = store.SetUserSetting(d, 1, "k2", "v2")
	m, err := store.GetAllUserSettings(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if m["k1"] != "v1" || m["k2"] != "v2" {
		t.Fatalf("unexpected settings: %v", m)
	}
}
