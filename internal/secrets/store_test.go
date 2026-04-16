package secrets

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"tether/internal/testutil"
)

func mustKeyB64(t *testing.T, b byte) string {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = b
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestNewStore_ValidatesKeyAndTTLDefault(t *testing.T) {
	d := testutil.OpenTestDB(t)
	st, err := NewStore(d, mustKeyB64(t, 1), 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.TTL != 24*time.Hour {
		t.Fatalf("expected default TTL 24h, got %v", st.TTL)
	}

	if _, err := NewStore(d, "", time.Hour); err == nil {
		t.Fatalf("expected error for missing key")
	}
	if _, err := NewStore(d, base64.StdEncoding.EncodeToString([]byte("short")), time.Hour); err == nil {
		t.Fatalf("expected error for short key")
	}
}

func TestStore_PutGet_RoundTrip(t *testing.T) {
	d := testutil.OpenTestDB(t)
	st, err := NewStore(d, mustKeyB64(t, 2), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := st.Put(ctx, 1, " api ", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.Get(ctx, 1, "api")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != "s3cr3t" {
		t.Fatalf("expected secret, got %q ok=%v", got, ok)
	}

	// ciphertext should not be stored in plaintext.
	var ct []byte
	if err := d.QueryRow(`SELECT ciphertext FROM secrets WHERE user_id=1 AND label='api'`).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if string(ct) == "s3cr3t" {
		t.Fatalf("ciphertext unexpectedly equals plaintext")
	}
}

func TestStore_WrongKeyFailsDecrypt(t *testing.T) {
	d := testutil.OpenTestDB(t)
	st1, _ := NewStore(d, mustKeyB64(t, 3), time.Hour)
	st2, _ := NewStore(d, mustKeyB64(t, 4), time.Hour)
	ctx := context.Background()
	_ = st1.Put(ctx, 1, "l", "secret")
	_, _, err := st2.Get(ctx, 1, "l")
	if err == nil {
		t.Fatalf("expected decrypt error")
	}
}

func TestStore_ExpiryAndPrune(t *testing.T) {
	d := testutil.OpenTestDB(t)
	st, _ := NewStore(d, mustKeyB64(t, 5), time.Hour)
	ctx := context.Background()
	_ = st.Put(ctx, 1, "l", "secret")
	// Force expiry.
	if _, err := d.Exec(`UPDATE secrets SET expires_at=? WHERE user_id=1 AND label='l'`, time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	_, ok, err := st.Get(ctx, 1, "l")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("expected expired secret to be missing")
	}
}

func TestStore_ListDeleteClear(t *testing.T) {
	d := testutil.OpenTestDB(t)
	st, _ := NewStore(d, mustKeyB64(t, 6), time.Hour)
	ctx := context.Background()
	_ = st.Put(ctx, 1, "b", "1")
	_ = st.Put(ctx, 1, "a", "2")
	items, err := st.List(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Label != "a" || items[1].Label != "b" {
		t.Fatalf("expected sorted labels, got %+v", items)
	}
	if err := st.Delete(ctx, 1, "a"); err != nil {
		t.Fatal(err)
	}
	items, _ = st.List(ctx, 1)
	if len(items) != 1 || items[0].Label != "b" {
		t.Fatalf("unexpected after delete: %+v", items)
	}
	if err := st.Clear(ctx, 1); err != nil {
		t.Fatal(err)
	}
	items, _ = st.List(ctx, 1)
	if len(items) != 0 {
		t.Fatalf("expected clear")
	}
}
