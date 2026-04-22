//go:build integration

package admin

import (
	"context"
	"os"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	password := "test-admin-password"
	hash, err := HashSecret(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if err := VerifySecret(hash, password); err != nil {
		t.Fatalf("verify correct password: %v", err)
	}

	if err := VerifySecret(hash, "wrong-password"); err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestCreateAdminAndLogin(t *testing.T) {
	dsn := os.Getenv("TEST_ADMIN_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_ADMIN_DATABASE_URL not set")
	}
	db, err := OpenAdmin(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewAuthStore(db)

	t.Cleanup(func() {
		_, _ = db.DB.Exec("DELETE FROM admin_sessions")
		_, _ = db.DB.Exec("DELETE FROM admin_users WHERE username = 'testadmin'")
	})

	hash, _ := HashSecret("password123")
	adminID, err := store.CreateAdmin(context.Background(), "testadmin", hash)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	// Verify correct login
	id, err := store.VerifyLogin(context.Background(), "testadmin", "password123")
	if err != nil {
		t.Fatalf("verify login: %v", err)
	}
	if id != adminID {
		t.Fatalf("expected %s, got %s", adminID, id)
	}

	// Wrong password
	_, err = store.VerifyLogin(context.Background(), "testadmin", "wrong")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}

	// Create and validate session
	token, err := store.CreateSession(context.Background(), adminID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	gotID, err := store.ValidateSession(context.Background(), token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if gotID != adminID {
		t.Fatalf("expected %s, got %s", adminID, gotID)
	}

	// Invalid token
	_, err = store.ValidateSession(context.Background(), "bogus")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}
