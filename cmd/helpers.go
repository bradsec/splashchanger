package cmd

import (
	"fmt"
	"regexp"

	"github.com/user/splashchanger/internal/backup"
	"github.com/user/splashchanger/internal/config"
	"github.com/user/splashchanger/internal/detect"
	"github.com/user/splashchanger/internal/imgutil"
	"github.com/user/splashchanger/internal/safepath"
)

func validateEncryptFlags() error {
	if err := config.ValidateHexColor(flags.encrypt.boxColor); err != nil {
		return fmt.Errorf("invalid --box-color: %w", err)
	}
	if err := config.ValidateHexColor(flags.encrypt.textColor); err != nil {
		return fmt.Errorf("invalid --text-color: %w", err)
	}
	switch flags.encrypt.style {
	case "minimal", "boxed", "floating":
	default:
		return fmt.Errorf("invalid --style %q: must be minimal, boxed, or floating", flags.encrypt.style)
	}
	return nil
}

func buildConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.DryRun = flags.dryRun
	cfg.EncryptScreen.HAlign = clamp01(flags.encrypt.halign)
	cfg.EncryptScreen.VAlign = clamp01(flags.encrypt.valign)
	cfg.EncryptScreen.BoxColor = flags.encrypt.boxColor
	cfg.EncryptScreen.BoxOpacity = clamp01(flags.encrypt.boxOpacity)
	cfg.EncryptScreen.TextColor = flags.encrypt.textColor
	cfg.EncryptScreen.FontSize = flags.encrypt.fontSize
	cfg.EncryptScreen.Style = flags.encrypt.style

	cfg.Resize = config.ResizeMode(flags.resize)

	// Apply resolution override to all targets.
	if flags.resWidth > 0 && flags.resHeight > 0 {
		cfg.TargetDimensions[config.TargetGrub] = config.Dimensions{Width: flags.resWidth, Height: flags.resHeight}
		cfg.TargetDimensions[config.TargetPlymouth] = config.Dimensions{Width: flags.resWidth, Height: flags.resHeight}
		cfg.TargetDimensions[config.TargetLogin] = config.Dimensions{Width: flags.resWidth, Height: flags.resHeight}
	}

	return cfg
}

// Resolution bounds for --resolution flag.
const (
	minResWidth  = 320
	minResHeight = 240
	maxResWidth  = 7680
	maxResHeight = 4320
)

// resolutionRe matches strict WIDTHxHEIGHT format.
var resolutionRe = regexp.MustCompile(`^\d{1,5}x\d{1,5}$`)

// parseResolution parses a "WIDTHxHEIGHT" string strictly.
func parseResolution(s string) (int, int, error) {
	if !resolutionRe.MatchString(s) {
		return 0, 0, fmt.Errorf("invalid resolution %q: must be WIDTHxHEIGHT (e.g. 1920x1080)", s)
	}
	var w, h int
	fmt.Sscanf(s, "%dx%d", &w, &h)
	return w, h, nil
}

// validateGlobalFlags validates global CLI flags before command execution.
func validateGlobalFlags() error {
	// Validate resize mode.
	switch config.ResizeMode(flags.resize) {
	case config.ResizeNone, config.ResizeFit, config.ResizeFill, config.ResizeCrop:
	default:
		return fmt.Errorf("invalid --resize mode %q: must be none, fit, fill, or crop", flags.resize)
	}

	// Validate resolution format and bounds.
	if flags.resolution != "" {
		w, h, err := parseResolution(flags.resolution)
		if err != nil {
			return err
		}
		if w < minResWidth || h < minResHeight {
			return fmt.Errorf("--resolution %dx%d is below minimum %dx%d", w, h, minResWidth, minResHeight)
		}
		if w > maxResWidth || h > maxResHeight {
			return fmt.Errorf("--resolution %dx%d exceeds maximum %dx%d", w, h, maxResWidth, maxResHeight)
		}
		flags.resWidth = w
		flags.resHeight = h
	}

	return nil
}

// validateAndResolveImage sanitizes the path, validates the image, and prints info.
func validateAndResolveImage(rawPath string) (string, error) {
	imgPath, err := safepath.ValidateImagePath(rawPath)
	if err != nil {
		return "", fmt.Errorf("unsafe image path: %w", err)
	}

	if err := safepath.ValidateForInterpolation(imgPath); err != nil {
		return "", fmt.Errorf("unsafe characters in image path: %w", err)
	}

	info, warnings, err := imgutil.Validate(imgPath)
	if err != nil {
		return "", fmt.Errorf("invalid image: %w", err)
	}
	fmt.Printf("  [validate] Image: %s (%s, %dx%d)\n", imgPath, info.Format, info.Width, info.Height)
	for _, w := range warnings {
		fmt.Printf("  [validate] Warning: %s\n", w)
	}
	if flags.resize != "" && flags.resize != "none" {
		fmt.Printf("  [validate] Resize mode: %s\n", flags.resize)
	}

	return imgPath, nil
}

// detectAndBackup runs environment detection and backup with the given error policy.
// Returns the detected environment (may be nil if detection fails and !requireEnv).
func detectAndBackup(cfg *config.Config, policy backupPolicy) (*detect.Environment, error) {
	fmt.Println("[splashchanger] Detecting environment...")
	env, err := detect.DetectEnvironment()
	if err != nil {
		if policy.requireEnv {
			return nil, fmt.Errorf("environment detection failed: %w", err)
		}
		fmt.Printf("  [warning] Environment detection failed: %v (continuing anyway)\n", err)
	}

	// Save original system files on first run (never overwrites).
	if err := backup.SaveOriginal(cfg, env); err != nil {
		fmt.Printf("  [warning] Could not save original files: %v\n", err)
	}

	fmt.Println("[splashchanger] Taking automatic backup...")
	if err := backup.TakeBackup(cfg, env); err != nil {
		if policy.requireBackup {
			return env, fmt.Errorf("backup failed: %w", err)
		}
		fmt.Printf("  [warning] Backup failed: %v (continuing without backup)\n", err)
	}

	return env, nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
