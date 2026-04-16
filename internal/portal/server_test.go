package portal

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"tether/internal/config"
	"tether/internal/testutil"
)

func writeTempHostKey(t *testing.T, dir string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b})
	p := filepath.Join(dir, "ssh_host_ed25519")
	if err := os.WriteFile(p, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustDB(t *testing.T) *sql.DB {
	t.Helper()
	return testutil.OpenTestDB(t)
}

func TestNewServer_BindsAllInterfacesWhenHostEmpty(t *testing.T) {
	dir := t.TempDir()
	hostKey := writeTempHostKey(t, dir)
	h, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)

	cfg := &config.Config{}
	cfg.SSH.ListenAddr = ":2222"
	cfg.SSH.HostKeyPath = hostKey
	cfg.SSH.PortalPasswordHash = string(h)
	cfg.SSH.AuthorizedKeysPath = filepath.Join(dir, "missing_authorized_keys")

	srv, err := NewServer(cfg, mustDB(t), nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv.s.Addr != "0.0.0.0:2222" {
		t.Fatalf("expected 0.0.0.0:2222, got %q", srv.s.Addr)
	}
}

func TestNewServer_LeavesExplicitHostAlone(t *testing.T) {
	dir := t.TempDir()
	hostKey := writeTempHostKey(t, dir)
	h, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)

	cfg := &config.Config{}
	cfg.SSH.ListenAddr = "127.0.0.1:2222"
	cfg.SSH.HostKeyPath = hostKey
	cfg.SSH.PortalPasswordHash = string(h)
	cfg.SSH.AuthorizedKeysPath = filepath.Join(dir, "missing_authorized_keys")

	srv, err := NewServer(cfg, mustDB(t), nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv.s.Addr != "127.0.0.1:2222" {
		t.Fatalf("expected addr unchanged, got %q", srv.s.Addr)
	}
}
