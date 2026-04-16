package store_test

import (
	"testing"

	"tether/internal/store"
)

func TestAddAuditEvent_UserAndNullUser(t *testing.T) {
	d := openTestDB(t)

	uid := int64(1)
	if err := store.AddAuditEvent(d, &uid, "t", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAuditEvent(d, nil, "t2", "{}"); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := d.QueryRow(`SELECT COUNT(1) FROM audit_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 rows, got %d", n)
	}
}
