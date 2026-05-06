package grub

import (
	"strings"
	"testing"
)

func TestProcessGrubLines_SingleUncommented(t *testing.T) {
	lines := []string{
		"GRUB_TIMEOUT=5",
		`GRUB_BACKGROUND="/old/path.png"`,
		"GRUB_CMDLINE_LINUX_DEFAULT=quiet",
	}
	result := processGrubLines(lines, "/new/image.png")
	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(result), result)
	}
	if result[1] != `GRUB_BACKGROUND="/new/image.png"` {
		t.Errorf("expected replaced line, got %q", result[1])
	}
}

func TestProcessGrubLines_SingleCommented(t *testing.T) {
	lines := []string{
		"GRUB_TIMEOUT=5",
		`#GRUB_BACKGROUND="/old/path.png"`,
	}
	result := processGrubLines(lines, "/new/image.png")
	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(result), result)
	}
	if result[1] != `GRUB_BACKGROUND="/new/image.png"` {
		t.Errorf("expected uncommented replacement, got %q", result[1])
	}
}

func TestProcessGrubLines_MultipleLines(t *testing.T) {
	lines := []string{
		"GRUB_TIMEOUT=5",
		`#GRUB_BACKGROUND="/old/path.png"`,
		"GRUB_CMDLINE_LINUX_DEFAULT=quiet",
		`GRUB_BACKGROUND="/another/path.png"`,
		"GRUB_DEFAULT=0",
	}
	result := processGrubLines(lines, "/new/image.png")

	// Should have 4 lines (one duplicate removed)
	if len(result) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(result), result)
	}

	// Count GRUB_BACKGROUND occurrences
	count := 0
	for _, line := range result {
		if strings.Contains(line, "GRUB_BACKGROUND") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 GRUB_BACKGROUND line, got %d", count)
	}

	// First match position should have the replacement
	if result[1] != `GRUB_BACKGROUND="/new/image.png"` {
		t.Errorf("expected replacement at index 1, got %q", result[1])
	}
}

func TestProcessGrubLines_CommentWithSpace(t *testing.T) {
	lines := []string{
		"GRUB_TIMEOUT=5",
		`# GRUB_BACKGROUND="/old/path.png"`,
	}
	result := processGrubLines(lines, "/new/image.png")
	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	if result[1] != `GRUB_BACKGROUND="/new/image.png"` {
		t.Errorf("expected uncommented replacement, got %q", result[1])
	}
}

func TestProcessGrubLines_NoExisting(t *testing.T) {
	lines := []string{
		"GRUB_TIMEOUT=5",
		"GRUB_DEFAULT=0",
	}
	result := processGrubLines(lines, "/new/image.png")
	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(result), result)
	}
	if result[2] != `GRUB_BACKGROUND="/new/image.png"` {
		t.Errorf("expected appended line, got %q", result[2])
	}
}

func TestEnsureSplashInLines_AlreadyPresent(t *testing.T) {
	lines := []string{
		`GRUB_CMDLINE_LINUX_DEFAULT="quiet splash"`,
	}
	result := ensureSplashInLines(lines)
	if result != nil {
		t.Error("expected nil (no change needed) when splash already present")
	}
}

func TestEnsureSplashInLines_AddsSplash(t *testing.T) {
	lines := []string{
		"GRUB_TIMEOUT=5",
		`GRUB_CMDLINE_LINUX_DEFAULT="quiet"`,
		"GRUB_DEFAULT=0",
	}
	result := ensureSplashInLines(lines)
	if result == nil {
		t.Fatal("expected updated lines, got nil")
	}
	if result[1] != `GRUB_CMDLINE_LINUX_DEFAULT="quiet splash"` {
		t.Errorf("expected splash added, got %q", result[1])
	}
	// Other lines should be unchanged.
	if result[0] != "GRUB_TIMEOUT=5" || result[2] != "GRUB_DEFAULT=0" {
		t.Errorf("other lines were modified: %v", result)
	}
}

func TestEnsureSplashInLines_EmptyValue(t *testing.T) {
	lines := []string{
		`GRUB_CMDLINE_LINUX_DEFAULT=""`,
	}
	result := ensureSplashInLines(lines)
	if result == nil {
		t.Fatal("expected updated lines, got nil")
	}
	if result[0] != `GRUB_CMDLINE_LINUX_DEFAULT="splash"` {
		t.Errorf("expected splash added to empty value, got %q", result[0])
	}
}

func TestEnsureSplashInLines_NoLine(t *testing.T) {
	lines := []string{
		"GRUB_TIMEOUT=5",
	}
	result := ensureSplashInLines(lines)
	if result == nil {
		t.Fatal("expected updated lines, got nil")
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	if result[1] != `GRUB_CMDLINE_LINUX_DEFAULT="splash"` {
		t.Errorf("expected appended line, got %q", result[1])
	}
}

func TestEnsureSplashInLines_SplashOnly(t *testing.T) {
	lines := []string{
		`GRUB_CMDLINE_LINUX_DEFAULT="splash"`,
	}
	result := ensureSplashInLines(lines)
	if result != nil {
		t.Error("expected nil (no change needed) when splash is the only value")
	}
}

func TestEnsureSplashInLines_DoesNotMatchSubstring(t *testing.T) {
	// "nosplash" should not count as "splash"
	lines := []string{
		`GRUB_CMDLINE_LINUX_DEFAULT="quiet nosplash"`,
	}
	result := ensureSplashInLines(lines)
	if result == nil {
		t.Fatal("expected updated lines — 'nosplash' is not 'splash'")
	}
	if result[0] != `GRUB_CMDLINE_LINUX_DEFAULT="quiet nosplash splash"` {
		t.Errorf("expected splash appended, got %q", result[0])
	}
}
