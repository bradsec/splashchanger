package deps

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestEnsureCommand_AlreadyAvailable(t *testing.T) {
	origLookPath := lookPathFn
	defer func() { lookPathFn = origLookPath }()

	lookPathFn = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}

	if err := EnsureCommand("gresource"); err != nil {
		t.Errorf("unexpected error for available command: %v", err)
	}
}

func TestEnsureCommand_UnknownPackage(t *testing.T) {
	origLookPath := lookPathFn
	defer func() { lookPathFn = origLookPath }()

	lookPathFn = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}

	err := EnsureCommand("unknown-tool-xyz")
	if err == nil {
		t.Error("expected error for unknown command, got nil")
	}
	if !strings.Contains(err.Error(), "no known package") {
		t.Errorf("expected 'no known package' error, got: %v", err)
	}
}

func TestEnsureCommand_InstallsPackage(t *testing.T) {
	origLookPath := lookPathFn
	origExecCommand := execCommandFn
	defer func() {
		lookPathFn = origLookPath
		execCommandFn = origExecCommand
	}()

	calls := 0
	lookPathFn = func(file string) (string, error) {
		calls++
		if calls <= 1 {
			return "", fmt.Errorf("not found")
		}
		return "/usr/bin/" + file, nil
	}

	var installedPkg string
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if name == "apt-get" {
			if len(args) >= 4 {
				installedPkg = args[3] // shifted by one due to --no-install-recommends
			}
			// Verify --no-install-recommends is present
			found := false
			for _, a := range args {
				if a == "--no-install-recommends" {
					found = true
				}
			}
			if !found {
				t.Error("expected --no-install-recommends flag")
			}
		}
		return exec.Command("true")
	}

	if err := EnsureCommand("gresource"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if installedPkg != "libglib2.0-dev-bin" {
		t.Errorf("expected libglib2.0-dev-bin, got %q", installedPkg)
	}
}

func TestEnsureCommand_AutoInstallDisabled(t *testing.T) {
	origLookPath := lookPathFn
	origAutoInstall := AutoInstallEnabled
	defer func() {
		lookPathFn = origLookPath
		AutoInstallEnabled = origAutoInstall
	}()

	lookPathFn = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	AutoInstallEnabled = false

	err := EnsureCommand("gresource")
	if err == nil {
		t.Error("expected error when auto-install disabled")
	}
	if !strings.Contains(err.Error(), "install it manually") {
		t.Errorf("expected manual install message, got: %v", err)
	}
}

func TestEnsureCommand_KnownMappings(t *testing.T) {
	for cmd, pkg := range commandToPackage {
		if pkg == "" {
			t.Errorf("empty package for command %q", cmd)
		}
		if cmd == "" {
			t.Errorf("empty command for package %q", pkg)
		}
	}
}
