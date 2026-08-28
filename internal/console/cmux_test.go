package console

import (
	"errors"
	"testing"
)

func TestCmuxInfoParsesAMatch(t *testing.T) {
	orig := CmuxRunner
	defer func() { CmuxRunner = orig }()
	CmuxRunner = func(sessionID string) ([]byte, error) {
		if sessionID != "abc-123" {
			t.Fatalf("sessionID = %q, want abc-123", sessionID)
		}
		return []byte(`{"sessions":[{"pid":4242,"stored_pid_exists":true,"agent_lifecycle":"running"}]}`), nil
	}

	info, found, err := CmuxInfo("abc-123")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected a match")
	}
	if info.PID != 4242 || !info.StoredPIDExists || info.AgentLifecycle != "running" {
		t.Errorf("info = %+v, unexpected fields", info)
	}
}

func TestCmuxInfoNoMatchIsNotAnError(t *testing.T) {
	orig := CmuxRunner
	defer func() { CmuxRunner = orig }()
	CmuxRunner = func(sessionID string) ([]byte, error) {
		return []byte(`{"sessions":[]}`), nil
	}

	_, found, err := CmuxInfo("no-such-session")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("expected found=false for an empty sessions array")
	}
}

func TestCmuxInfoRunnerFailureIsAnError(t *testing.T) {
	orig := CmuxRunner
	defer func() { CmuxRunner = orig }()
	CmuxRunner = func(sessionID string) ([]byte, error) {
		return nil, errors.New("cmux: command not found")
	}

	_, found, err := CmuxInfo("abc-123")
	if err == nil {
		t.Error("expected an error when cmux itself cannot run")
	}
	if found {
		t.Error("found should be false alongside an error")
	}
}

func TestCmuxInfoEmptySessionIDIsANoop(t *testing.T) {
	called := false
	orig := CmuxRunner
	defer func() { CmuxRunner = orig }()
	CmuxRunner = func(sessionID string) ([]byte, error) {
		called = true
		return nil, nil
	}

	_, found, err := CmuxInfo("")
	if err != nil || found {
		t.Errorf("info=%v found=%v, want no-op", err, found)
	}
	if called {
		t.Error("CmuxRunner should not be invoked for an empty sessionID")
	}
}
