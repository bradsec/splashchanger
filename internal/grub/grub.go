package grub

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/user/splashchanger/internal/deps"
	"github.com/user/splashchanger/internal/fileutil"
	"github.com/user/splashchanger/internal/imgutil"
)

const (
	grubConfig        = "/etc/default/grub"
	grubBgDest        = "/boot/grub/splashchanger-background"
	grubBgKey         = "GRUB_BACKGROUND"
	grubTerminalKey   = "GRUB_TERMINAL"
	grubGfxmodeKey    = "GRUB_GFXMODE"
	grubGfxpayloadKey = "GRUB_GFXPAYLOAD_LINUX"
	// grubGfxmode: "auto" lets GRUB select the best mode the firmware supports.
	// A hardcoded mode (e.g. 1024x768x32) causes GRUB to fall back to text mode
	// (plain blue screen) on any system that doesn't support that exact mode+depth.
	grubGfxmode = "auto"
)

// Apply sets the GRUB background image and regenerates grub.cfg.
//
// We do NOT set GRUB_BACKGROUND in /etc/default/grub. 05_debian_theme reads
// that key and calls set_background_image without colour arguments, which is
// identical to what happens when it auto-discovers a *.png in /boot/grub/ —
// so setting the key adds no value but would prevent the desktop-base colour
// fallback from ever being tried for other targets. Relying on auto-discovery
// is simpler and equally correct.
//
// We do NOT set GRUB_TERMINAL. Setting it adds "terminal_input gfxterm" to
// grub.cfg, which was absent in the original working Debian config.
func Apply(imgPath string) error {
	// Always write as PNG regardless of input format. This normalises the image
	// to 8-bit non-interlaced RGBA, which is the only format GRUB's PNG reader
	// reliably accepts. Interlaced PNGs and indexed-colour PNGs cause GRUB to
	// silently skip the background image with no on-screen error.
	destPath := grubBgDest + ".png"

	// Remove other-extension leftovers from previous runs. 05_debian_theme
	// scans /boot/grub/ for *.jpg before *.png, so a stale JPEG would be
	// picked up ahead of our normalised PNG.
	for _, ext := range []string{".jpg", ".jpeg", ".tga"} {
		old := grubBgDest + ext
		if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
			fmt.Printf("  [grub] Warning: could not remove old image %s: %v\n", old, err)
		}
	}

	fmt.Printf("  [grub] Normalising image to %s\n", destPath)
	if err := imgutil.NormalizeForGrub(imgPath, destPath); err != nil {
		return fmt.Errorf("failed to normalise image: %w", err)
	}

	fmt.Printf("  [grub] Updating %s\n", grubConfig)

	// Remove any GRUB_BACKGROUND we may have set in a previous run — Debian's
	// 05_debian_theme auto-discovers *.png files in /boot/grub/ when this key
	// is absent, which is the correct single-command path.
	if err := removeGrubKey(grubBgKey); err != nil {
		return fmt.Errorf("failed to remove %s: %w", grubBgKey, err)
	}

	// Remove any GRUB_TERMINAL we may have set in a previous run.
	if err := removeGrubKey(grubTerminalKey); err != nil {
		return fmt.Errorf("failed to remove %s: %w", grubTerminalKey, err)
	}

	if err := ensureGfxmode(); err != nil {
		return fmt.Errorf("failed to set gfxmode: %w", err)
	}

	if err := ensureGfxpayload(); err != nil {
		return fmt.Errorf("failed to set gfxpayload: %w", err)
	}

	if err := ensureBootSplash(); err != nil {
		return fmt.Errorf("failed to enable boot splash: %w", err)
	}

	fmt.Println("  [grub] Running update-grub...")
	if err := deps.EnsureCommand("update-grub"); err != nil {
		return err
	}
	out, err := exec.Command("update-grub").CombinedOutput()
	if err != nil {
		return fmt.Errorf("update-grub failed: %w\n%s", err, string(out))
	}

	fmt.Println("  [grub] Done.")
	return nil
}

// setGrubBackground edits /etc/default/grub to set GRUB_BACKGROUND.
func setGrubBackground(imagePath string) error {
	lines, err := readLines(grubConfig)
	if err != nil {
		return err
	}

	return writeLines(grubConfig, processGrubLines(lines, imagePath))
}

// processGrubLines replaces all GRUB_BACKGROUND lines (commented or not) with
// a single uncommented entry. If no existing line is found, one is appended.
func processGrubLines(lines []string, imagePath string) []string {
	newLine := fmt.Sprintf(`%s="%s"`, grubBgKey, imagePath)

	// Collect indices of all matching lines.
	var matchIndices []int
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, grubBgKey+"=") ||
			strings.HasPrefix(trimmed, "#"+grubBgKey+"=") ||
			strings.HasPrefix(trimmed, "# "+grubBgKey+"=") {
			matchIndices = append(matchIndices, i)
		}
	}

	if len(matchIndices) == 0 {
		return append(lines, newLine)
	}

	// Build set for O(1) lookup.
	matchSet := make(map[int]struct{}, len(matchIndices))
	for _, idx := range matchIndices {
		matchSet[idx] = struct{}{}
	}

	// Replace first match, skip all subsequent matches.
	result := make([]string, 0, len(lines))
	firstDone := false
	for i, line := range lines {
		_, isMatch := matchSet[i]
		if isMatch {
			if !firstDone {
				result = append(result, newLine)
				firstDone = true
			}
			// skip subsequent matches
		} else {
			result = append(result, line)
		}
	}

	return result
}

// ensureBootSplash checks GRUB_CMDLINE_LINUX_DEFAULT for the "splash" keyword
// and adds it if missing, so Plymouth boot splash is displayed.
func ensureBootSplash() error {
	lines, err := readLines(grubConfig)
	if err != nil {
		return err
	}

	updated := ensureSplashInLines(lines)
	if updated == nil {
		return nil // already has splash
	}

	fmt.Println("  [grub] Enabling boot splash (adding 'splash' to GRUB_CMDLINE_LINUX_DEFAULT)...")
	return writeLines(grubConfig, updated)
}

// ensureGfxterm sets GRUB_TERMINAL="gfxterm" so GRUB uses the graphical terminal,
// which is required for background image support.
func ensureGfxterm() error {
	lines, err := readLines(grubConfig)
	if err != nil {
		return err
	}
	updated := setOrReplaceKey(lines, grubTerminalKey, "gfxterm")
	if updated == nil {
		return nil
	}
	fmt.Println("  [grub] Setting GRUB_TERMINAL=gfxterm (required for background support)...")
	return writeLines(grubConfig, updated)
}

// ensureGfxmode sets GRUB_GFXMODE=auto so GRUB picks the best mode the
// firmware supports. A hardcoded mode+depth (e.g. 1024x768x32) causes GRUB
// to fall back to text mode on any system that doesn't support it exactly.
func ensureGfxmode() error {
	lines, err := readLines(grubConfig)
	if err != nil {
		return err
	}
	updated := setOrReplaceKey(lines, grubGfxmodeKey, grubGfxmode)
	if updated == nil {
		return nil
	}
	fmt.Printf("  [grub] Setting GRUB_GFXMODE=%s...\n", grubGfxmode)
	return writeLines(grubConfig, updated)
}

// ensureGfxpayload sets GRUB_GFXPAYLOAD_LINUX=keep so the framebuffer is
// preserved when handing off to the kernel/Plymouth.
func ensureGfxpayload() error {
	lines, err := readLines(grubConfig)
	if err != nil {
		return err
	}
	updated := setOrReplaceKey(lines, grubGfxpayloadKey, "keep")
	if updated == nil {
		return nil
	}
	fmt.Println("  [grub] Setting GRUB_GFXPAYLOAD_LINUX=keep...")
	return writeLines(grubConfig, updated)
}

// removeGrubKey removes all uncommented lines that assign the given key in the
// GRUB config file (e.g. KEY="value"). Used to undo explicit settings so that
// Debian's default scripts can take over auto-detection.
func removeGrubKey(key string) error {
	lines, err := readLines(grubConfig)
	if err != nil {
		return err
	}
	var result []string
	changed := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+"=") {
			changed = true
			fmt.Printf("  [grub] Removing %s from %s\n", key, grubConfig)
			continue
		}
		result = append(result, line)
	}
	if !changed {
		return nil
	}
	return writeLines(grubConfig, result)
}

// setOrReplaceKey sets key="value" in the GRUB config lines, replacing any
// existing (commented or uncommented) entry. Returns nil if the value is
// already set correctly (no write needed).
func setOrReplaceKey(lines []string, key, value string) []string {
	newLine := fmt.Sprintf(`%s="%s"`, key, value)
	wantLine := newLine

	var matchIndices []int
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == wantLine {
			// Already set correctly — no change needed.
			return nil
		}
		if strings.HasPrefix(trimmed, key+"=") ||
			strings.HasPrefix(trimmed, "#"+key+"=") ||
			strings.HasPrefix(trimmed, "# "+key+"=") {
			matchIndices = append(matchIndices, i)
		}
	}

	if len(matchIndices) == 0 {
		return append(lines, newLine)
	}

	matchSet := make(map[int]struct{}, len(matchIndices))
	for _, idx := range matchIndices {
		matchSet[idx] = struct{}{}
	}

	result := make([]string, 0, len(lines))
	firstDone := false
	for i, line := range lines {
		_, isMatch := matchSet[i]
		if isMatch {
			if !firstDone {
				result = append(result, newLine)
				firstDone = true
			}
		} else {
			result = append(result, line)
		}
	}
	return result
}

// ensureSplashInLines adds "splash" to GRUB_CMDLINE_LINUX_DEFAULT if not present.
// Returns nil if no change is needed.
func ensureSplashInLines(lines []string) []string {
	const key = "GRUB_CMDLINE_LINUX_DEFAULT"

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key) {
			continue
		}

		// Extract the value between quotes.
		eqIdx := strings.Index(trimmed, "=")
		if eqIdx < 0 {
			continue
		}
		val := strings.TrimSpace(trimmed[eqIdx+1:])
		val = strings.Trim(val, `"`)

		// Check if "splash" is already present as a word.
		for w := range strings.SplitSeq(val, " ") {
			if w == "splash" {
				return nil
			}
		}

		// Add splash.
		if val == "" {
			val = "splash"
		} else {
			val = val + " splash"
		}
		result := make([]string, len(lines))
		copy(result, lines)
		result[i] = fmt.Sprintf(`%s="%s"`, key, val)
		return result
	}

	// No GRUB_CMDLINE_LINUX_DEFAULT line found — add one.
	result := make([]string, len(lines))
	copy(result, lines)
	return append(result, fmt.Sprintf(`%s="splash"`, key))
}

// readLines reads a file into a slice of strings.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// writeLines writes a slice of strings to a file atomically.
func writeLines(path string, lines []string) error {
	var buf strings.Builder
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return fileutil.WriteFileAtomic(path, []byte(buf.String()), 0644)
}
