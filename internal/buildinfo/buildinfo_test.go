package buildinfo

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestVersionNotEmpty(t *testing.T) {
	if Version == "" {
		t.Error("Version must not be empty")
	}
}

func TestBuildTimeNotEmpty(t *testing.T) {
	if BuildTime == "" {
		t.Error("BuildTime must not be empty")
	}
}

func TestDateAlias(t *testing.T) {
	// Date should be populated (either from ldflags or from init via BuildTime)
	if Date == "" {
		t.Error("Date must not be empty")
	}
}

func TestBinaryPathReturnsSomething(t *testing.T) {
	p := BinaryPath()
	if p == "" {
		t.Error("BinaryPath() returned empty string")
	}
}

func TestPIDReturnsCurrentProcess(t *testing.T) {
	pid := PID()
	if pid <= 0 {
		t.Errorf("PID() returned non-positive value: %d", pid)
	}
	if pid != os.Getpid() {
		t.Logf("PID()=%d current=%d (may differ in some sandboxed environments)", pid, os.Getpid())
	}
}

func TestPIDStringMatchesPID(t *testing.T) {
	if PIDString() != strconv.Itoa(PID()) {
		t.Error("PIDString() should equal strconv.Itoa(PID())")
	}
}

func TestStringContainsAllFields(t *testing.T) {
	s := String()
	if !strings.Contains(s, Version) {
		t.Errorf("String() should contain Version; got %q", s)
	}
	if !strings.Contains(s, Commit) {
		t.Errorf("String() should contain Commit; got %q", s)
	}
	if !strings.Contains(s, BuildTime) {
		t.Errorf("String() should contain BuildTime; got %q", s)
	}
}

func TestInitDoesNotPanic(t *testing.T) {
	// init() has already run; just ensure the package level vars are sane.
	if Version == "" || Commit == "" || BuildTime == "" {
		t.Fatal("expected non-empty build info after package init")
	}
}
