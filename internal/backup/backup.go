package backup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/user/splashchanger/internal/config"
	"github.com/user/splashchanger/internal/detect"
	"github.com/user/splashchanger/internal/fileutil"
)

// allowedCommands is the set of commands that execCommand may run.
var allowedCommands = map[string]bool{
	"update-grub":                true,
	"update-initramfs":           true,
	"plymouth-set-default-theme": true,
	"dconf":                      true,
}

// execCommand is an injectable package-level var for running post-restore commands.
var execCommand = func(name string, args ...string) error {
	if !allowedCommands[name] {
		return fmt.Errorf("command %q is not in the allowed command list", name)
	}
	fmt.Printf("  [exec] Running: %s %s\n", name, strings.Join(args, " "))
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", name, err, string(out))
	}
	return nil
}

// filesToBackup lists all files/dirs that may be modified by splashchanger.
var filesToBackup = []string{
	// GRUB
	"/etc/default/grub",
	// Plymouth
	"/etc/plymouth/plymouthd.conf",
	"/usr/share/plymouth/themes/splashchanger",
	// GDM (dconf-based)
	"/etc/dconf/profile/gdm",
	"/etc/dconf/db/gdm.d/00-splashchanger",
	"/usr/share/gdm/dconf/91-splashchanger",
	// GDM (GNOME Shell theme gresource)
	"/usr/share/gnome-shell/gnome-shell-theme.gresource",
	// LightDM
	"/etc/lightdm/lightdm-gtk-greeter.conf",
	"/etc/lightdm/lightdm.conf",
	// SDDM
	"/etc/sddm.conf",
	"/usr/share/sddm/themes/breeze/backgrounds",
}

// backupSet represents a backup directory with common save/restore operations.
type backupSet struct {
	dir    string
	label  string // display label: "backup" or "original"
	dryRun bool
}

// saveFiles copies all files from filesToBackup into the backup directory.
func (bs *backupSet) saveFiles() (int, []string) {
	backed := 0
	var backedFiles []string
	for _, src := range filesToBackup {
		info, err := os.Stat(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			fmt.Printf("  [%s] Warning: could not stat %s: %v\n", bs.label, src, err)
			continue
		}

		if bs.dryRun {
			fmt.Printf("  [%s] Would back up %s\n", bs.label, src)
			backed++
			continue
		}

		dest := filepath.Join(bs.dir, src)
		if info.IsDir() {
			if err := fileutil.CopyDir(src, dest); err != nil {
				fmt.Printf("  [%s] Warning: could not back up %s: %v\n", bs.label, src, err)
			} else {
				backed++
				backedFiles = append(backedFiles, src)
			}
		} else {
			if err := fileutil.CopyFile(src, dest); err != nil {
				fmt.Printf("  [%s] Warning: could not back up %s: %v\n", bs.label, src, err)
			} else {
				backed++
				backedFiles = append(backedFiles, src)
			}
		}
	}
	return backed, backedFiles
}

// restoreFiles walks the backup dir and copies files back to their original locations.
// Returns which types of files were restored for post-restore commands.
func (bs *backupSet) restoreFiles() (grub, plymouth, dconf bool, err error) {
	err = filepath.Walk(bs.dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == bs.dir || filepath.Base(path) == "manifest.txt" {
			return nil
		}

		rel, relErr := filepath.Rel(bs.dir, path)
		if relErr != nil {
			return relErr
		}
		dest := filepath.Join("/", rel)

		switch {
		case strings.HasPrefix(dest, "/etc/default/grub") || strings.HasPrefix(dest, "/boot/grub/"):
			grub = true
		case strings.HasPrefix(dest, "/usr/share/plymouth/") || strings.HasPrefix(dest, "/etc/plymouth/"):
			plymouth = true
		case strings.HasPrefix(dest, "/etc/dconf/") || strings.HasPrefix(dest, "/usr/share/gnome-shell/") || strings.HasPrefix(dest, "/usr/share/gdm/dconf/"):
			dconf = true
		}

		if bs.dryRun {
			fmt.Printf("  [%s] Would restore %s to %s\n", bs.label, path, dest)
			return nil
		}

		if info.IsDir() {
			return os.MkdirAll(dest, info.Mode())
		}
		return fileutil.CopyFile(path, dest)
	})
	return
}

// TakeBackup creates a timestamped backup of all relevant config files and images.
func TakeBackup(cfg *config.Config, env *detect.Environment) error {
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	backupDir := filepath.Join(cfg.BackupDir, timestamp)

	if !cfg.DryRun {
		if err := os.MkdirAll(backupDir, 0700); err != nil {
			return fmt.Errorf("could not create backup directory %s: %w", backupDir, err)
		}
	}

	bs := &backupSet{dir: backupDir, label: "backup", dryRun: cfg.DryRun}
	backed, backedFiles := bs.saveFiles()

	if !cfg.DryRun {
		manifest := filepath.Join(backupDir, "manifest.txt")
		if err := writeManifest(manifest, env, timestamp, backedFiles); err != nil {
			fmt.Printf("  [backup] Warning: could not write manifest: %v\n", err)
		}
	}

	fmt.Printf("  [backup] Backed up %d item(s) to %s\n", backed, backupDir)

	if !cfg.DryRun {
		pruneOldBackups(cfg.BackupDir, 10)
	}

	return nil
}

// Restore restores the most recent backup.
func Restore(cfg *config.Config, env *detect.Environment) error {
	latest, err := latestBackup(cfg.BackupDir)
	if err != nil {
		return fmt.Errorf("no backups found in %s: %w", cfg.BackupDir, err)
	}

	fmt.Printf("[splashchanger] Restoring from backup: %s\n", latest)
	checkManifestMismatch(latest, env)

	bs := &backupSet{dir: latest, label: "restore", dryRun: cfg.DryRun}
	restoredGrub, restoredPlymouth, restoredDconf, err := bs.restoreFiles()
	if err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}

	runPostRestoreCommands(cfg.DryRun, restoredGrub, restoredPlymouth, restoredDconf, false)
	fmt.Println("[splashchanger] Restore complete.")
	return nil
}

// runPostRestoreCommands runs system commands to regenerate configs after restore.
// When restoreOriginal is true, the Plymouth theme is read from the restored
// plymouthd.conf instead of being set to "splashchanger".
func runPostRestoreCommands(dryRun, grub, plymouth, dconf, restoreOriginal bool) {
	type cmd struct {
		name string
		args []string
	}
	var cmds []cmd
	if grub {
		cmds = append(cmds, cmd{"update-grub", nil})
	}
	if plymouth {
		cmds = append(cmds, cmd{"update-initramfs", []string{"-u"}})
		if !restoreOriginal {
			cmds = append(cmds, cmd{"plymouth-set-default-theme", []string{"splashchanger"}})
		} else if theme := readPlymouthTheme(); theme != "" {
			cmds = append(cmds, cmd{"plymouth-set-default-theme", []string{theme}})
		}
	}
	if dconf {
		cmds = append(cmds, cmd{"dconf", []string{"update"}})
	}

	for _, c := range cmds {
		full := c.name
		if len(c.args) > 0 {
			full += " " + strings.Join(c.args, " ")
		}
		if dryRun {
			fmt.Printf("  Would run: %s\n", full)
			continue
		}
		fmt.Printf("  Running: %s\n", full)
		if err := execCommand(c.name, c.args...); err != nil {
			fmt.Printf("  Warning: %v\n", err)
		}
	}
}

// checkManifestMismatch reads the manifest and warns if the environment differs.
func checkManifestMismatch(backupDir string, env *detect.Environment) {
	manifestPath := filepath.Join(backupDir, "manifest.txt")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return // no manifest, nothing to check
	}

	var manifestLoginMgr, manifestDesktop string
	for line := range strings.Lines(string(data)) {
		line = strings.TrimRight(line, "\r\n")
		if after, ok := strings.CutPrefix(line, "LoginMgr:"); ok {
			manifestLoginMgr = strings.TrimSpace(after)
		}
		if after, ok := strings.CutPrefix(line, "Desktop:"); ok {
			manifestDesktop = strings.TrimSpace(after)
		}
	}

	if manifestLoginMgr != "" && manifestLoginMgr != string(env.LoginManager) {
		fmt.Printf("  Warning: backup was taken with %s but current login manager is %s\n",
			manifestLoginMgr, env.LoginManager)
	}
	if manifestDesktop != "" && manifestDesktop != string(env.Desktop) {
		fmt.Printf("  Warning: backup was taken with %s but current desktop is %s\n",
			manifestDesktop, env.Desktop)
	}
}

// latestBackup finds the most recently created backup directory.
func latestBackup(backupDir string) (string, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no backups found")
	}
	// Entries are sorted alphabetically; since names are timestamps, last = newest.
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsDir() {
			return filepath.Join(backupDir, entries[i].Name()), nil
		}
	}
	return "", fmt.Errorf("no backup directories found")
}

// pruneOldBackups keeps only the `keep` most recent backups.
func pruneOldBackups(backupDir string, keep int) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}
	// Collect directory entries (each backup is a dir).
	var dirs []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		}
	}
	if len(dirs) <= keep {
		return
	}
	// Remove oldest (lowest timestamp = earliest in sort order).
	for _, d := range dirs[:len(dirs)-keep] {
		path := filepath.Join(backupDir, d.Name())
		if err := os.RemoveAll(path); err != nil {
			fmt.Printf("  [backup] Warning: could not prune old backup %s: %v\n", path, err)
		} else {
			fmt.Printf("  [backup] Pruned old backup: %s\n", path)
		}
	}
}

// SaveOriginal saves the factory/original system files on first run only.
// If the original backup directory already exists, this is a no-op.
func SaveOriginal(cfg *config.Config, env *detect.Environment) error {
	originalDir := config.OriginalBackupDir

	if cfg.DryRun {
		// In dry-run mode, check if it already exists.
		if _, err := os.Stat(originalDir); err == nil {
			return nil
		}
		fmt.Printf("  [original] Would save original system files to %s\n", originalDir)
		return nil
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(originalDir), 0700); err != nil {
		return fmt.Errorf("could not create parent directory: %w", err)
	}

	// Atomic create-or-fail: if another process already created it, we skip.
	if err := os.Mkdir(originalDir, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("could not create original backup directory: %w", err)
	}

	fmt.Println("  [original] First run detected — saving original system files...")

	bs := &backupSet{dir: originalDir, label: "original", dryRun: false}
	backed, backedFiles := bs.saveFiles()

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	manifest := filepath.Join(originalDir, "manifest.txt")
	if err := writeManifest(manifest, env, timestamp, backedFiles); err != nil {
		fmt.Printf("  [original] Warning: could not write manifest: %v\n", err)
	}

	fmt.Printf("  [original] Saved %d original item(s) to %s\n", backed, originalDir)
	return nil
}

// RestoreOriginal restores the factory/original system files.
func RestoreOriginal(cfg *config.Config, env *detect.Environment) error {
	originalDir := config.OriginalBackupDir

	if _, err := os.Stat(originalDir); os.IsNotExist(err) {
		return fmt.Errorf("no original backup found at %s — was splashchanger run before?", originalDir)
	}

	fmt.Printf("[splashchanger] Restoring original system files from %s\n", originalDir)
	checkManifestMismatch(originalDir, env)

	bs := &backupSet{dir: originalDir, label: "restore-original", dryRun: cfg.DryRun}
	restoredGrub, restoredPlymouth, restoredDconf, err := bs.restoreFiles()
	if err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}

	cleanupSplashchangerFiles(cfg.DryRun)
	runPostRestoreCommands(cfg.DryRun, restoredGrub, restoredPlymouth, restoredDconf, true)
	fmt.Println("[splashchanger] Original system files restored.")
	return nil
}

// cleanupSplashchangerFiles removes files created by splashchanger that would
// not have existed on a fresh install.
func cleanupSplashchangerFiles(dryRun bool) {
	cleanup := []string{
		"/etc/dconf/db/gdm.d/00-splashchanger",
		"/etc/dconf/profile/gdm",
		"/usr/share/gdm/dconf/91-splashchanger",
		"/usr/share/splashchanger",
		"/usr/share/plymouth/themes/splashchanger",
		"/usr/share/gnome-shell/gnome-shell-theme.gresource.splashchanger-backup",
	}

	// Remove any GRUB background images placed by splashchanger.
	grubBgGlob, _ := filepath.Glob("/boot/grub/splashchanger-background*")
	cleanup = append(cleanup, grubBgGlob...)
	for _, path := range cleanup {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		if dryRun {
			fmt.Printf("  [restore-original] Would remove splashchanger file: %s\n", path)
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			fmt.Printf("  [restore-original] Warning: could not remove %s: %v\n", path, err)
		} else {
			fmt.Printf("  [restore-original] Removed: %s\n", path)
		}
	}
}

// readPlymouthTheme reads the current Plymouth theme name from plymouthd.conf.
func readPlymouthTheme() string {
	data, err := os.ReadFile("/etc/plymouth/plymouthd.conf")
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "Theme="); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// writeManifest writes a human-readable summary of the backup.
func writeManifest(path string, env *detect.Environment, timestamp string, backedFiles []string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "splashchanger backup\n")
	fmt.Fprintf(f, "Timestamp:   %s\n", timestamp)
	fmt.Fprintf(f, "Desktop:     %s\n", env.Desktop)
	fmt.Fprintf(f, "LoginMgr:    %s\n", env.LoginManager)
	fmt.Fprintf(f, "Plymouth:    %v\n", env.HasPlymouth)
	fmt.Fprintf(f, "GRUB:        %v\n", env.HasGrub)
	if len(backedFiles) > 0 {
		fmt.Fprintf(f, "\nFiles backed up:\n")
		for _, file := range backedFiles {
			fmt.Fprintf(f, "  %s\n", file)
		}
	}
	return nil
}
