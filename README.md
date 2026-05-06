# splashchanger

A Debian Linux tool to change splash screen images for GRUB, Plymouth (encryption/boot),
and the desktop login screen — all from a single command. Supports automatic backup and restore.

> **Warning:** This tool modifies critical system files including GRUB bootloader configuration,
> Plymouth initramfs themes, GNOME Shell theme resources, and display manager configs. Incorrect
> changes to these files can result in an unbootable system, a broken login screen, or loss of
> access to an encrypted disk prompt. While automatic backups are taken before every change,
> always ensure you have a separate recovery method available (e.g. a live USB) before use.
> This tool must be run as root and will install system packages via apt if missing.
> Use at your own risk.

## Supported Login Managers

| Login Manager | Desktop Environments          |
|---------------|-------------------------------|
| GDM3          | GNOME                         |
| LightDM       | XFCE, LXDE, MATE, Cinnamon    |
| SDDM          | KDE Plasma                    |
| SLiM          | Openbox, i3, legacy setups    |

## Requirements

- Debian Linux (tested on Debian 11/12/13)
- Go 1.26+ (to build from source)

Runtime dependencies (`grub-pc`, `plymouth`, `libglib2.0-dev-bin`, `dconf-cli`, `initramfs-tools`)
are **automatically installed via apt** when needed — no manual setup required.

## Build & Install

```bash
git clone https://github.com/bradsec/splashchanger.git
cd splashchanger
sudo make install
```

To uninstall:

```bash
sudo make uninstall
```

## Usage

```bash
# Show detected environment
sudo splashchanger status

# Apply one image to ALL splash screens at once
sudo splashchanger apply /path/to/image.png

# Change individual targets
sudo splashchanger grub    /path/to/image.png   # GRUB bootloader
sudo splashchanger encrypt /path/to/image.png   # Plymouth boot/encryption screen
sudo splashchanger login   /path/to/image.png   # Desktop login screen

# Backup current settings manually
sudo splashchanger backup

# Restore the most recent backup
sudo splashchanger restore

# Restore to original system state (fresh install)
sudo splashchanger restore-original
```

## Backup & Restore

Every `apply`, `grub`, `encrypt`, or `login` command automatically takes a backup **before**
making any changes. Backups are stored in:

```
/var/lib/splashchanger/backups/<timestamp>/
```

Each backup contains:
- A copy of all modified config files (GRUB, Plymouth, login manager)
- Original background images
- A `manifest.txt` describing the system state at backup time

The 10 most recent backups are kept; older ones are pruned automatically.

To restore at any time:

```bash
# Restore the most recent backup
sudo splashchanger restore

# Restore to the original system state (before splashchanger was first used)
sudo splashchanger restore-original
```

The first time splashchanger modifies any splash screen, it saves a snapshot of the original
system files to `/var/lib/splashchanger/original/`. This snapshot is never overwritten by
subsequent runs, so `restore-original` always returns the system to its pre-splashchanger state.

## Image Requirements

### Format

**PNG is strongly recommended.** All resized output is saved as PNG regardless of input format,
and GRUB has compatibility issues with certain JPEG variants. If you must use JPEG, ensure it is
baseline (non-progressive) with 8-bit color depth — progressive JPEGs and indexed/paletted PNGs
are not supported by GRUB.

### Resolution

For best results, use a **3840×2160 (4K) or higher** source image. The application only
downscales, so a high-resolution source ensures clean output across all targets without
upscaling artifacts.

| Target            | Default Resolution |
|-------------------|--------------------|
| GRUB              | 1024×768           |
| Plymouth (boot)   | 1920×1080          |
| Login screen      | 1920×1080          |

You can override the target resolution with `--resolution WIDTHxHEIGHT` (min 320×240,
max 7680×4320).

### Resize Modes

Use `--resize` to control how the image is adapted to the target resolution:

- **none** — copies the image as-is with no processing
- **fit** — scales to fit within the target dimensions, adding black letterbox bars to fill any remaining space
- **fill** (default) — scales to cover the entire target, center-cropping any overflow
- **crop** — crops to the target aspect ratio first, then scales to exact dimensions

## Automatic Configuration

- **Boot splash**: If `splash` is not present in `GRUB_CMDLINE_LINUX_DEFAULT`, it is added automatically when applying a GRUB background, so Plymouth boot splash is displayed.
- **Missing packages**: Required system tools are installed via `apt-get` on first use if not already present.

## Project Structure

```
splashchanger/
├── main.go                      # Entry point
├── cmd/                         # CLI command routing
├── internal/
│   ├── backup/                  # Backup and restore logic
│   ├── config/                  # Paths and runtime config
│   ├── deps/                    # Auto-install missing system packages
│   ├── detect/                  # DE and login manager detection
│   ├── fileutil/                # Atomic file operations
│   ├── grub/                    # GRUB background handler
│   ├── imgutil/                 # Image resize/crop/validate
│   ├── lockfile/                # Concurrency lock for system-wide operations
│   ├── loginmgr/                # Login manager handler (GDM/LightDM/SDDM/SLiM)
│   ├── plymouth/                # Plymouth theme handler
│   └── safepath/                # Path validation and sanitization
├── Makefile
└── README.md
```
