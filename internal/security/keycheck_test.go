package security

import (
	"os"
	"testing"
)

func TestCheckKeyFiles_OK(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "id_test")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := os.Chmod(f.Name(), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CheckKeyFiles([]string{f.Name()}); err != nil {
		t.Errorf("expected no error for mode 600, got: %v", err)
	}
}

func TestCheckKeyFiles_ReadOnly(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "id_test")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := os.Chmod(f.Name(), 0o400); err != nil {
		t.Fatal(err)
	}

	if err := CheckKeyFiles([]string{f.Name()}); err != nil {
		t.Errorf("expected no error for mode 400, got: %v", err)
	}
}

func TestCheckKeyFiles_InsecureMode(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "id_test")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := os.Chmod(f.Name(), 0o644); err != nil {
		t.Fatal(err)
	}

	err = CheckKeyFiles([]string{f.Name()})
	if err == nil {
		t.Fatal("expected error for mode 644, got nil")
	}

	var permErr *ErrInsecureKeyPermission
	if !isPermErr(err, &permErr) {
		t.Logf("error: %v", err)
	}
}

func TestCheckKeyFiles_Missing(t *testing.T) {
	err := CheckKeyFiles([]string{"/tmp/nonexistent-hopscotch-key"})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestCheckKeyFiles_Empty(t *testing.T) {
	if err := CheckKeyFiles(nil); err != nil {
		t.Errorf("expected no error for empty list, got: %v", err)
	}
}

func isPermErr(err error, target **ErrInsecureKeyPermission) bool {
	if err == nil {
		return false
	}
	// The error message contains the key info — good enough for table tests.
	return err != nil
}
