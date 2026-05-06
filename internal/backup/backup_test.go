package backup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/user/splashchanger/internal/config"
	"github.com/user/splashchanger/internal/detect"
)

// captureStdout captures stdout output during fn execution.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestTakeBackup_DryRun(t *testing.T) {
	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backups")

	cfg := &config.Config{
		BackupDir: backupDir,
		DryRun:    true,
	}
	env := &detect.Environment{
		Desktop:      detect.DEGNOME,
		LoginManager: detect.LMGDM,
		HasPlymouth:  true,
		HasGrub:      true,
	}

	output := captureStdout(t, func() {
		err := TakeBackup(cfg, env)
		if err != nil {
			t.Fatalf("TakeBackup dry-run failed: %v", err)
		}
	})

	// Backup directory should NOT have been created (dry-run).
	if _, err := os.Stat(backupDir); !os.IsNotExist(err) {
		t.Errorf("expected backup dir not to exist in dry-run mode, but it does")
	}

	// Output should contain "Would back up" for any files that exist on the system.
	// In a test environment, most files won't exist, but we verify no actual copies happen.
	if strings.Contains(output, "Warning: could not create") {
		t.Errorf("dry-run should not attempt to create directories, got: %s", output)
	}
}

func TestPruneOldBackups(t *testing.T) {
	tmpDir := t.TempDir()

	// Create 12 mock timestamped backup directories.
	for i := range 12 {
		dir := filepath.Join(tmpDir, fmt.Sprintf("2025-01-%02d_12-00-00", i+1))
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}

	output := captureStdout(t, func() {
		pruneOldBackups(tmpDir, 10)
	})

	// Should have pruned 2 oldest.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	dirCount := 0
	for _, e := range entries {
		if e.IsDir() {
			dirCount++
		}
	}
	if dirCount != 10 {
		t.Errorf("expected 10 backups after prune, got %d", dirCount)
	}

	// The two oldest should be gone.
	if _, err := os.Stat(filepath.Join(tmpDir, "2025-01-01_12-00-00")); !os.IsNotExist(err) {
		t.Error("expected oldest backup to be pruned")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "2025-01-02_12-00-00")); !os.IsNotExist(err) {
		t.Error("expected second-oldest backup to be pruned")
	}

	// The third should still exist.
	if _, err := os.Stat(filepath.Join(tmpDir, "2025-01-03_12-00-00")); err != nil {
		t.Error("expected third-oldest backup to still exist")
	}

	if !strings.Contains(output, "Pruned old backup") {
		t.Error("expected prune output message")
	}
}

func TestLatestBackup(t *testing.T) {
	tmpDir := t.TempDir()

	dirs := []string{
		"2025-01-01_12-00-00",
		"2025-01-15_08-30-00",
		"2025-02-01_09-00-00",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, d), 0700); err != nil {
			t.Fatal(err)
		}
	}

	latest, err := latestBackup(tmpDir)
	if err != nil {
		t.Fatalf("latestBackup failed: %v", err)
	}

	expected := filepath.Join(tmpDir, "2025-02-01_09-00-00")
	if latest != expected {
		t.Errorf("expected latest=%s, got %s", expected, latest)
	}
}

func TestLatestBackup_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := latestBackup(tmpDir)
	if err == nil {
		t.Error("expected error for empty backup dir")
	}
}

func TestFilesToBackup_NoGrubCfg(t *testing.T) {
	for _, f := range filesToBackup {
		if f == "/boot/grub/grub.cfg" {
			t.Error("/boot/grub/grub.cfg should NOT be in filesToBackup")
		}
	}
}

func TestFilesToBackup_HasGDMDconfPaths(t *testing.T) {
	found := map[string]bool{
		"/etc/dconf/profile/gdm":               false,
		"/etc/dconf/db/gdm.d/00-splashchanger": false,
	}
	for _, f := range filesToBackup {
		if _, ok := found[f]; ok {
			found[f] = true
		}
	}
	for path, present := range found {
		if !present {
			t.Errorf("expected %s to be in filesToBackup", path)
		}
	}
}

func TestFilesToBackup_HasPlymouthThemeDir(t *testing.T) {
	found := slices.Contains(filesToBackup, "/usr/share/plymouth/themes/splashchanger")
	if !found {
		t.Error("expected /usr/share/plymouth/themes/splashchanger in filesToBackup")
	}
}

func TestRestore_ManifestMismatchWarning(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a backup directory with a manifest showing "gdm".
	backupName := "2025-03-01_12-00-00"
	backupPath := filepath.Join(tmpDir, backupName)
	if err := os.MkdirAll(backupPath, 0700); err != nil {
		t.Fatal(err)
	}

	manifest := filepath.Join(backupPath, "manifest.txt")
	content := "splashchanger backup\nTimestamp:   2025-03-01_12-00-00\nDesktop:     gnome\nLoginMgr:    gdm\nPlymouth:    true\nGRUB:        true\n"
	if err := os.WriteFile(manifest, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a dummy file so the walk has something.
	dummyDir := filepath.Join(backupPath, "etc", "lightdm")
	if err := os.MkdirAll(dummyDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dummyDir, "lightdm.conf"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		BackupDir: tmpDir,
		DryRun:    true, // dry-run so we don't actually write to /etc
	}
	env := &detect.Environment{
		Desktop:      detect.DEGNOME,
		LoginManager: detect.LMLightDM, // different from manifest "gdm"
		HasPlymouth:  true,
		HasGrub:      true,
	}

	output := captureStdout(t, func() {
		err := Restore(cfg, env)
		if err != nil {
			t.Fatalf("Restore failed: %v", err)
		}
	})

	if !strings.Contains(output, "backup was taken with gdm but current login manager is lightdm") {
		t.Errorf("expected manifest mismatch warning, got:\n%s", output)
	}
}

func TestRunPostRestoreCommands(t *testing.T) {
	// Track which commands were called.
	var calledCommands []string
	origExecCommand := execCommand
	execCommand = func(name string, args ...string) error {
		full := name
		if len(args) > 0 {
			full += " " + strings.Join(args, " ")
		}
		calledCommands = append(calledCommands, full)
		return nil
	}
	defer func() { execCommand = origExecCommand }()

	// Test with all three types restored.
	output := captureStdout(t, func() {
		runPostRestoreCommands(false, true, true, true, false)
	})

	expectedCmds := []string{
		"update-grub",
		"update-initramfs -u",
		"plymouth-set-default-theme splashchanger",
		"dconf update",
	}
	for _, exp := range expectedCmds {
		found := slices.Contains(calledCommands, exp)
		if !found {
			t.Errorf("expected post-restore command %q to be called, called: %v", exp, calledCommands)
		}
	}

	for _, exp := range expectedCmds {
		if !strings.Contains(output, "Running: "+exp) {
			t.Errorf("expected output to contain 'Running: %s', got:\n%s", exp, output)
		}
	}
}

func TestRunPostRestoreCommands_OnlyGrub(t *testing.T) {
	var calledCommands []string
	origExecCommand := execCommand
	execCommand = func(name string, args ...string) error {
		full := name
		if len(args) > 0 {
			full += " " + strings.Join(args, " ")
		}
		calledCommands = append(calledCommands, full)
		return nil
	}
	defer func() { execCommand = origExecCommand }()

	captureStdout(t, func() {
		runPostRestoreCommands(false, true, false, false, false)
	})

	if len(calledCommands) != 1 || calledCommands[0] != "update-grub" {
		t.Errorf("expected only update-grub, got: %v", calledCommands)
	}
}

func TestRunPostRestoreCommands_DryRun(t *testing.T) {
	origExecCommand := execCommand
	execCommand = func(name string, args ...string) error {
		t.Errorf("execCommand should not be called in dry-run mode, got: %s %v", name, args)
		return nil
	}
	defer func() { execCommand = origExecCommand }()

	output := captureStdout(t, func() {
		runPostRestoreCommands(true, true, true, true, false)
	})

	if !strings.Contains(output, "Would run: update-grub") {
		t.Errorf("expected 'Would run: update-grub' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Would run: dconf update") {
		t.Errorf("expected 'Would run: dconf update' in output, got:\n%s", output)
	}
}

func TestRestore_DryRun_PostRestoreCommands(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a backup with a grub file.
	backupName := "2025-03-01_12-00-00"
	backupPath := filepath.Join(tmpDir, backupName)
	grubDir := filepath.Join(backupPath, "etc", "default")
	if err := os.MkdirAll(grubDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grubDir, "grub"), []byte("GRUB_TIMEOUT=5"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupPath, "manifest.txt"), []byte("splashchanger backup\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Ensure execCommand is NOT called in dry-run.
	origExecCommand := execCommand
	execCommand = func(name string, args ...string) error {
		t.Errorf("execCommand should not be called in dry-run mode, got: %s %v", name, args)
		return nil
	}
	defer func() { execCommand = origExecCommand }()

	cfg := &config.Config{
		BackupDir: tmpDir,
		DryRun:    true,
	}
	env := &detect.Environment{
		Desktop:      detect.DEGNOME,
		LoginManager: detect.LMGDM,
	}

	output := captureStdout(t, func() {
		err := Restore(cfg, env)
		if err != nil {
			t.Fatalf("Restore dry-run failed: %v", err)
		}
	})

	if !strings.Contains(output, "Would run: update-grub") {
		t.Errorf("expected dry-run output to contain 'Would run: update-grub', got:\n%s", output)
	}
	if !strings.Contains(output, "Would restore") {
		t.Errorf("expected dry-run output to contain 'Would restore', got:\n%s", output)
	}
}
