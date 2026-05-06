package loginmgr

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/user/splashchanger/internal/deps"
	"github.com/user/splashchanger/internal/detect"
	"github.com/user/splashchanger/internal/fileutil"
)

const destDir = "/usr/share/splashchanger"

// Apply routes to the correct login manager background handler.
func Apply(imgPath string, lm detect.LoginManager) error {
	// Copy image to a shared location we control.
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("could not create image staging dir: %w", err)
	}
	ext := filepath.Ext(imgPath)
	stagedPath := filepath.Join(destDir, "login-background"+ext)
	if err := fileutil.CopyFile(imgPath, stagedPath); err != nil {
		return fmt.Errorf("could not stage login image: %w", err)
	}

	switch lm {
	case detect.LMGDM:
		return applyGDM(stagedPath)
	case detect.LMLightDM:
		return applyLightDM(stagedPath)
	case detect.LMSDDM:
		return applySDDM(stagedPath)
	case detect.LMSlim:
		return applySlim(stagedPath)
	default:
		return fmt.Errorf("unsupported or undetected login manager: %s — please set the background manually", lm)
	}
}

// escapeDconfURI escapes a file path for use in a dconf picture-uri value.
// Ensures single quotes within the path don't break the INI format.
func escapeDconfURI(path string) string {
	// dconf values are wrapped in single quotes; escape any embedded single quotes
	return strings.ReplaceAll(path, "'", "%27")
}

// gdmSchemaFile is the path to the GNOME desktop background schema (injectable for testing).
var gdmSchemaFile = "/usr/share/glib-2.0/schemas/org.gnome.desktop.background.gschema.xml"

// gdmSupportsDarkURI checks if the installed GNOME schema supports picture-uri-dark.
// Returns false on Debian 11 (GNOME 3.38) where the key doesn't exist.
func gdmSupportsDarkURI() bool {
	data, err := os.ReadFile(gdmSchemaFile)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "picture-uri-dark")
}

// applyGDM sets the GDM3 background via dconf and GNOME Shell theme CSS.
func applyGDM(imgPath string) error {
	// Apply via GNOME Shell theme gresource (controls the actual login screen background
	// on GNOME 43+). This is the primary mechanism on modern Debian/GNOME.
	fmt.Println("  [loginmgr/gdm3] Patching GNOME Shell theme for login background...")
	if err := applyGDMShellTheme(imgPath); err != nil {
		fmt.Printf("  [loginmgr/gdm3] Warning: could not patch GNOME Shell theme: %v\n", err)
		fmt.Println("  [loginmgr/gdm3] Falling back to dconf-only approach (may not affect login screen on GNOME 43+)")
	}

	// Also apply via dconf (sets screensaver background and works on older GNOME).
	fmt.Println("  [loginmgr/gdm3] Applying background via dconf...")

	// Build the dconf content with background URIs and logo hiding.
	escaped := escapeDconfURI(imgPath)
	var dbContent string
	// Hide the distro logo on the login screen.
	logoSection := "\n[org/gnome/login-screen]\nlogo=''\n"

	if gdmSupportsDarkURI() {
		dbContent = fmt.Sprintf("[org/gnome/desktop/background]\npicture-uri='file://%s'\npicture-uri-dark='file://%s'\n\n"+
			"[org/gnome/desktop/screensaver]\npicture-uri='file://%s'\npicture-uri-dark='file://%s'\n",
			escaped, escaped, escaped, escaped)
	} else {
		dbContent = fmt.Sprintf("[org/gnome/desktop/background]\npicture-uri='file://%s'\n\n"+
			"[org/gnome/desktop/screensaver]\npicture-uri='file://%s'\n",
			escaped, escaped)
	}
	dbContent += logoSection

	// Debian/Ubuntu GDM uses its own dconf directory at /usr/share/gdm/dconf/
	// with priority files like 90-debian-settings. Write our override with
	// priority 91 so it takes precedence over the distro logo setting.
	gdmDconfDir := "/usr/share/gdm/dconf"
	if _, err := os.Stat(gdmDconfDir); err == nil {
		gdmOverride := filepath.Join(gdmDconfDir, "91-splashchanger")
		if err := fileutil.WriteFileAtomic(gdmOverride, []byte(dbContent), 0644); err != nil {
			fmt.Printf("  [loginmgr/gdm3] Warning: could not write GDM dconf override: %v\n", err)
		}
	}

	// Also write to the traditional /etc/dconf/db/gdm.d/ location as a
	// fallback for non-Debian GDM setups that use this path.
	profileDir := "/etc/dconf/profile"
	dbDir := "/etc/dconf/db/gdm.d"

	if err := os.MkdirAll(profileDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return err
	}

	// Ensure gdm dconf profile exists.
	profilePath := filepath.Join(profileDir, "gdm")
	profileContent := "user-db:user\nsystem-db:gdm\n"
	if err := fileutil.WriteFileAtomic(profilePath, []byte(profileContent), 0644); err != nil {
		return fmt.Errorf("could not write gdm dconf profile: %w", err)
	}

	dbFile := filepath.Join(dbDir, "00-splashchanger")
	if err := fileutil.WriteFileAtomic(dbFile, []byte(dbContent), 0644); err != nil {
		return fmt.Errorf("could not write gdm dconf db file: %w", err)
	}

	// Compile the dconf database.
	if err := deps.EnsureCommand("dconf"); err != nil {
		return err
	}
	out, err := exec.Command("dconf", "update").CombinedOutput()
	if err != nil {
		return fmt.Errorf("dconf update failed: %w\n%s", err, string(out))
	}

	fmt.Println("  [loginmgr/gdm3] Done. Restart GDM to see changes: sudo systemctl restart gdm3")
	return nil
}

// GNOME Shell theme gresource paths and markers.
const (
	gnomeShellGresourcePrefix = "/org/gnome/shell/theme/"
	cssMarkerStart            = "/* splashchanger-start */"
	cssMarkerEnd              = "/* splashchanger-end */"
)

// Injectable seams for GDM shell theme testing.
var (
	gnomeShellGresourcePath = "/usr/share/gnome-shell/gnome-shell-theme.gresource"
	execCommandFn           = exec.Command
)

// applyGDMShellTheme modifies the GNOME Shell theme CSS to set a custom login
// background. On GNOME 43+, the GDM login screen background is controlled by
// the shell theme CSS (compiled into a gresource), not by dconf settings.
func applyGDMShellTheme(imgPath string) error {
	// Ensure required tools are available (auto-installs if missing).
	for _, tool := range []string{"gresource", "glib-compile-resources"} {
		if err := deps.EnsureCommand(tool); err != nil {
			return err
		}
	}

	if _, err := os.Stat(gnomeShellGresourcePath); err != nil {
		return fmt.Errorf("GNOME Shell theme not found at %s", gnomeShellGresourcePath)
	}

	// Back up the original gresource once (on first run only).
	backupPath := gnomeShellGresourcePath + ".splashchanger-backup"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		fmt.Println("  [loginmgr/gdm3] Backing up original GNOME Shell theme...")
		if err := fileutil.CopyFile(gnomeShellGresourcePath, backupPath); err != nil {
			return fmt.Errorf("could not back up gresource: %w", err)
		}
	}

	// Always extract from the backup to avoid compounding CSS changes.
	sourcePath := backupPath

	tmpDir, err := os.MkdirTemp("/var/tmp", "splashchanger-gdm-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// List all resources in the gresource file.
	out, err := execCommandFn("gresource", "list", sourcePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gresource list failed: %w\n%s", err, out)
	}

	resources := strings.Split(strings.TrimSpace(string(out)), "\n")
	themeDir := filepath.Join(tmpDir, "theme")
	var xmlFiles []string

	// Extract each resource file.
	for _, res := range resources {
		if res == "" {
			continue
		}

		data, err := execCommandFn("gresource", "extract", sourcePath, res).Output()
		if err != nil {
			return fmt.Errorf("failed to extract %s: %w", res, err)
		}

		relPath := strings.TrimPrefix(res, gnomeShellGresourcePrefix)
		localPath := filepath.Join(themeDir, relPath)
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(localPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", localPath, err)
		}
		xmlFiles = append(xmlFiles, relPath)
	}

	// Inject our background CSS into all shell CSS variant files.
	// GNOME 42 and earlier: gnome-shell.css
	// GNOME 43-46: gnome-shell.css + gnome-shell-dark.css
	// GNOME 47+: gnome-shell-light.css + gnome-shell-dark.css + gnome-shell-high-contrast.css
	for _, cssName := range []string{"gnome-shell.css", "gnome-shell-dark.css", "gnome-shell-light.css", "gnome-shell-high-contrast.css"} {
		cssPath := filepath.Join(themeDir, cssName)
		content, err := os.ReadFile(cssPath)
		if err != nil {
			continue // file may not exist (e.g., no dark variant)
		}
		newContent := injectBackgroundCSS(string(content), imgPath)
		if err := os.WriteFile(cssPath, []byte(newContent), 0644); err != nil {
			return fmt.Errorf("failed to write modified %s: %w", cssName, err)
		}
	}

	// Generate the gresource XML manifest.
	xmlContent := generateGresourceXML(xmlFiles)
	xmlPath := filepath.Join(tmpDir, "gnome-shell-theme.gresource.xml")
	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		return err
	}

	// Compile the modified theme.
	outputPath := filepath.Join(tmpDir, "gnome-shell-theme.gresource")
	cmd := execCommandFn("glib-compile-resources", "--sourcedir", themeDir, "--target", outputPath, xmlPath)
	if compileOut, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("glib-compile-resources failed: %w\n%s", err, compileOut)
	}

	// Replace the system gresource with our modified version.
	if err := fileutil.CopyFile(outputPath, gnomeShellGresourcePath); err != nil {
		return fmt.Errorf("failed to install modified gresource: %w", err)
	}

	fmt.Println("  [loginmgr/gdm3] GNOME Shell theme patched successfully.")
	return nil
}

// injectBackgroundCSS adds or replaces the splashchanger background rule in GNOME Shell CSS.
func injectBackgroundCSS(content, imgPath string) string {
	bgRule := fmt.Sprintf("%s\n#lockDialogGroup {\n  background: url('file://%s');\n  background-size: cover;\n  background-position: center;\n}\n.login-dialog-logo-bin {\n  display: none;\n}\n%s",
		cssMarkerStart, imgPath, cssMarkerEnd)

	// Remove any existing splashchanger block.
	if start := strings.Index(content, cssMarkerStart); start >= 0 {
		if end := strings.Index(content, cssMarkerEnd); end >= 0 {
			content = content[:start] + content[end+len(cssMarkerEnd):]
		}
	}

	return strings.TrimRight(content, " \t\n") + "\n\n" + bgRule + "\n"
}

// generateGresourceXML creates a gresource XML manifest for the extracted theme files.
func generateGresourceXML(files []string) string {
	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<gresources>\n")
	sb.WriteString("  <gresource prefix=\"/org/gnome/shell/theme\">\n")
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("    <file>%s</file>\n", html.EscapeString(f)))
	}
	sb.WriteString("  </gresource>\n")
	sb.WriteString("</gresources>\n")
	return sb.String()
}

// applyLightDM writes to the lightdm-gtk-greeter config.
func applyLightDM(imgPath string) error {
	fmt.Println("  [loginmgr/lightdm] Applying background...")

	configPath := "/etc/lightdm/lightdm-gtk-greeter.conf"

	content, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not read LightDM greeter config: %w", err)
	}

	newContent := setIniValue(string(content), "greeter", "background", imgPath)

	if err := fileutil.WriteFileAtomic(configPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("could not write LightDM greeter config: %w", err)
	}

	fmt.Println("  [loginmgr/lightdm] Done. Restart LightDM to see changes: sudo systemctl restart lightdm")
	return nil
}

// Injectable seams for SDDM testing.
var (
	readFileFn        = os.ReadFile
	writeFileAtomicFn = fileutil.WriteFileAtomic
	mkdirAllFn        = os.MkdirAll
	globFn            = filepath.Glob
)

// sddmFileExists returns true if path exists (used in SDDM theme lookup).
func sddmFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

var sddmFileExistsFn = sddmFileExists

// parseSDDMThemeName extracts the Current= value from the [Theme] section
// of sddm.conf content. Returns (themeName, fromConfig) where fromConfig
// indicates whether the theme was explicitly set in the config file.
// Defaults to "debian-breeze" if not found.
func parseSDDMThemeName(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	inTheme := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[Theme]" {
			inTheme = true
			continue
		}
		if len(trimmed) > 0 && trimmed[0] == '[' {
			inTheme = false
			continue
		}
		if inTheme && strings.HasPrefix(trimmed, "Current") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				val := strings.TrimSpace(parts[1])
				if val != "" {
					return val, true
				}
			}
		}
	}
	return "debian-breeze", false
}

// scanSDDMDropInDirs reads /etc/sddm.conf.d/*.conf for [Theme] overrides.
// Files are read in alphabetical order; later files override earlier ones.
func scanSDDMDropInDirs() (string, bool) {
	matches, err := globFn("/etc/sddm.conf.d/*.conf")
	if err != nil || len(matches) == 0 {
		return "", false
	}
	slices.Sort(matches)

	var lastTheme string
	found := false
	for _, path := range matches {
		data, err := readFileFn(path)
		if err != nil {
			continue
		}
		if name, ok := parseSDDMThemeName(string(data)); ok {
			lastTheme = name
			found = true
		}
	}
	return lastTheme, found
}

// escapeSDDMValue ensures a path doesn't contain characters that break
// INI-format SDDM config files.
func escapeSDDMValue(path string) string {
	// SDDM INI format: newlines or section headers would break parsing
	path = strings.ReplaceAll(path, "\n", "")
	path = strings.ReplaceAll(path, "\r", "")
	return path
}

// applySDDM writes a SDDM theme config pointing to the background image.
// It reads the active theme from /etc/sddm.conf and drop-in dirs, and writes
// theme.conf.user in the corresponding theme directory.
func applySDDM(imgPath string) error {
	fmt.Println("  [loginmgr/sddm] Applying background...")

	sddmConf := "/etc/sddm.conf"
	content, err := readFileFn(sddmConf)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not read sddm.conf: %w", err)
	}

	themeName, fromConfig := parseSDDMThemeName(string(content))

	// Check drop-in directories for overrides (Debian 13+ / SDDM 0.20+).
	if dropInTheme, ok := scanSDDMDropInDirs(); ok {
		themeName = dropInTheme
		fromConfig = true
	}

	if !fromConfig {
		fmt.Printf("  [loginmgr/sddm] No theme configured in sddm.conf; assuming %q\n", themeName)
	}

	// Search for the theme directory, checking both debian-breeze and breeze.
	var themeDir string
	searchNames := []string{themeName}
	if themeName == "breeze" {
		searchNames = append(searchNames, "debian-breeze")
	} else if themeName == "debian-breeze" {
		searchNames = append(searchNames, "breeze")
	}

	for _, name := range searchNames {
		primary := filepath.Join("/usr/share/sddm/themes", name)
		fallback := filepath.Join("/usr/local/share/sddm/themes", name)

		if sddmFileExistsFn(primary) {
			themeDir = primary
			break
		}
		if sddmFileExistsFn(fallback) {
			themeDir = fallback
			break
		}
	}

	if themeDir == "" {
		// Create primary location if nothing found.
		themeDir = filepath.Join("/usr/share/sddm/themes", themeName)
		fmt.Printf("  [loginmgr/sddm] Warning: no SDDM theme directory found for %q — creating %s\n", themeName, themeDir)
		if err := mkdirAllFn(themeDir, 0755); err != nil {
			return fmt.Errorf("could not create theme dir %s: %w", themeDir, err)
		}
	}

	themeConfUser := filepath.Join(themeDir, "theme.conf.user")
	themeContent := fmt.Sprintf("[General]\nbackground=%s\n", escapeSDDMValue(imgPath))
	if err := writeFileAtomicFn(themeConfUser, []byte(themeContent), 0644); err != nil {
		return fmt.Errorf("could not write theme.conf.user: %w", err)
	}

	fmt.Println("  [loginmgr/sddm] Done. Restart SDDM to see changes: sudo systemctl restart sddm")
	return nil
}

// applySlim writes the background to the SLiM theme config.
func applySlim(imgPath string) error {
	fmt.Println("  [loginmgr/slim] Applying background...")

	slimConf := "/etc/slim.conf"
	content, err := os.ReadFile(slimConf)
	if err != nil {
		return fmt.Errorf("could not read /etc/slim.conf: %w", err)
	}

	newContent := setKeyValue(string(content), "background", imgPath)
	if err := fileutil.WriteFileAtomic(slimConf, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("could not write slim.conf: %w", err)
	}

	fmt.Println("  [loginmgr/slim] Done. Restart SLiM to see changes: sudo systemctl restart slim")
	return nil
}

// setIniValue sets key=value under [section] in an INI-style string.
// Matches exact key names (not prefixes) to avoid clobbering similar keys.
func setIniValue(content, section, key, value string) string {
	lines := strings.Split(content, "\n")
	sectionHeader := "[" + section + "]"
	inSection := false
	found := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == sectionHeader {
			inSection = true
			continue
		}
		if len(trimmed) > 0 && trimmed[0] == '[' {
			inSection = false
		}
		if inSection && matchesKey(trimmed, key, "=") {
			lines[i] = key + " = " + value
			found = true
			inSection = false
		}
	}

	if !found {
		lines = append(lines, "", sectionHeader, key+" = "+value)
	}

	return strings.Join(lines, "\n")
}

// setKeyValue sets a space-separated key value in a flat config (like slim.conf).
func setKeyValue(content, key, value string) string {
	lines := strings.Split(content, "\n")
	found := false
	for i, line := range lines {
		if matchesKey(strings.TrimSpace(line), key, " ") {
			lines[i] = key + " " + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, key+" "+value)
	}
	return strings.Join(lines, "\n")
}

// matchesKey checks if a line starts with the exact key followed by the separator
// (or whitespace then the separator). This prevents "background" from matching
// "background_color".
func matchesKey(line, key, sep string) bool {
	if !strings.HasPrefix(line, key) {
		return false
	}
	rest := line[len(key):]
	if len(rest) == 0 {
		return true // line is exactly the key (no value yet)
	}
	// The character after the key must be whitespace or the separator.
	first := rest[0]
	return first == ' ' || first == '\t' || string(first) == sep
}
