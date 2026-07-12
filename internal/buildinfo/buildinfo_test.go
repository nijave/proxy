package buildinfo

import (
	"strings"
	"testing"
)

func TestVersionDefaults(t *testing.T) {
	if Version == "" {
		t.Error("Version should not be empty")
	}
	if Commit == "" {
		t.Error("Commit should not be empty")
	}
	if Date == "" {
		t.Error("Date should not be empty")
	}
}

func TestString(t *testing.T) {
	s := String()
	if !strings.Contains(s, Version) {
		t.Errorf("String() should contain Version; got %q", s)
	}
	if !strings.Contains(s, Commit) {
		t.Errorf("String() should contain Commit; got %q", s)
	}
	if !strings.Contains(s, Date) {
		t.Errorf("String() should contain Date; got %q", s)
	}
}

func TestInitDoesNotPanic(t *testing.T) {
	// init() runs automatically; ensure calling it again (via re-assignment path) is safe.
	// We just verify that after package init, values are non-empty.
	if Version == "" || Commit == "" || Date == "" {
		t.Fatal("expected non-empty build info after init")
	}
}
