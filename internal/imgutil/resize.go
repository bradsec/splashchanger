package imgutil

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"os"

	"github.com/user/splashchanger/internal/config"
	"github.com/user/splashchanger/internal/fileutil"
	xdraw "golang.org/x/image/draw"
)

// maxDecodeSize limits the amount of data read when decoding images (100 MB).
const maxDecodeSize = 100 * 1024 * 1024

// ProcessImage reads srcPath, resizes/crops according to mode and dims, and writes to destPath as PNG.
// When mode is "none", the file is copied as-is.
func ProcessImage(srcPath, destPath string, dims config.Dimensions, mode config.ResizeMode) error {
	if dims.Width <= 0 || dims.Height <= 0 {
		return fmt.Errorf("invalid target dimensions %dx%d: must be positive", dims.Width, dims.Height)
	}

	if mode == config.ResizeNone {
		return fileutil.CopyFile(srcPath, destPath)
	}

	src, err := loadImage(srcPath)
	if err != nil {
		return err
	}

	srcW := src.Bounds().Dx()
	srcH := src.Bounds().Dy()
	if srcW <= 0 || srcH <= 0 {
		return fmt.Errorf("source image has invalid dimensions %dx%d", srcW, srcH)
	}

	var result image.Image
	switch mode {
	case config.ResizeFit:
		result = resizeFit(src, dims)
	case config.ResizeFill:
		result = resizeFill(src, dims)
	case config.ResizeCrop:
		result = resizeCrop(src, dims)
	default:
		return fmt.Errorf("unknown resize mode %q", mode)
	}

	return saveImage(result, destPath)
}

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open image: %w", err)
	}
	defer f.Close()

	// Limit the amount of data read to prevent OOM on huge files.
	lr := io.LimitReader(f, maxDecodeSize)
	img, _, err := image.Decode(lr)
	if err != nil {
		return nil, fmt.Errorf("could not decode image: %w", err)
	}
	return img, nil
}

// NormalizeForGrub decodes any supported image and re-encodes it as an 8-bit
// non-interlaced RGBA PNG — the exact format GRUB's PNG reader requires.
// It handles JPEG inputs and strips interlacing, 16-bit depth, or indexed
// color from PNGs, all of which cause silent GRUB display failures.
func NormalizeForGrub(srcPath, destPath string) error {
	img, err := loadImage(srcPath)
	if err != nil {
		return err
	}
	// Explicitly convert to 8-bit NRGBA. Go's png.Encode preserves 16-bit
	// depth for RGBA64/Gray16 source images, which GRUB may silently drop.
	nrgba := image.NewNRGBA(img.Bounds())
	draw.Draw(nrgba, nrgba.Bounds(), img, img.Bounds().Min, draw.Src)
	return saveImage(nrgba, destPath)
}

// saveImage writes an image to path atomically using write-to-temp-then-rename.
func saveImage(img image.Image, path string) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return fmt.Errorf("could not encode PNG: %w", err)
	}
	return fileutil.WriteFileAtomic(path, buf.Bytes(), 0644)
}

// resizeFit scales the image to fit within dims, letterboxing with black if needed.
func resizeFit(img image.Image, dims config.Dimensions) image.Image {
	srcW := img.Bounds().Dx()
	srcH := img.Bounds().Dy()

	scaleX := float64(dims.Width) / float64(srcW)
	scaleY := float64(dims.Height) / float64(srcH)
	scale := scaleX
	if scaleY < scale {
		scale = scaleY
	}

	newW := int(float64(srcW) * scale)
	newH := int(float64(srcH) * scale)

	// Create black canvas at target size.
	dst := image.NewRGBA(image.Rect(0, 0, dims.Width, dims.Height))

	// Center the scaled image on the canvas.
	offsetX := (dims.Width - newW) / 2
	offsetY := (dims.Height - newH) / 2

	xdraw.CatmullRom.Scale(dst, image.Rect(offsetX, offsetY, offsetX+newW, offsetY+newH), img, img.Bounds(), xdraw.Over, nil)
	return dst
}

// resizeFill scales the image to fill dims, center-cropping excess.
func resizeFill(img image.Image, dims config.Dimensions) image.Image {
	srcW := img.Bounds().Dx()
	srcH := img.Bounds().Dy()

	scaleX := float64(dims.Width) / float64(srcW)
	scaleY := float64(dims.Height) / float64(srcH)
	scale := scaleX
	if scaleY > scale {
		scale = scaleY
	}

	newW := int(float64(srcW) * scale)
	newH := int(float64(srcH) * scale)

	// Scale to intermediate size.
	scaled := image.NewRGBA(image.Rect(0, 0, newW, newH))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), img, img.Bounds(), xdraw.Over, nil)

	// Center-crop to target.
	offsetX := (newW - dims.Width) / 2
	offsetY := (newH - dims.Height) / 2

	dst := image.NewRGBA(image.Rect(0, 0, dims.Width, dims.Height))
	draw.Draw(dst, dst.Bounds(), scaled, image.Pt(offsetX, offsetY), draw.Src)
	return dst
}

// resizeCrop center-crops to target aspect ratio, then scales to target size.
func resizeCrop(img image.Image, dims config.Dimensions) image.Image {
	srcW := img.Bounds().Dx()
	srcH := img.Bounds().Dy()

	targetAspect := float64(dims.Width) / float64(dims.Height)
	srcAspect := float64(srcW) / float64(srcH)

	var cropRect image.Rectangle
	if srcAspect > targetAspect {
		// Source is wider — crop sides.
		cropW := int(float64(srcH) * targetAspect)
		offsetX := (srcW - cropW) / 2
		cropRect = image.Rect(offsetX, 0, offsetX+cropW, srcH)
	} else {
		// Source is taller — crop top/bottom.
		cropH := int(float64(srcW) / targetAspect)
		offsetY := (srcH - cropH) / 2
		cropRect = image.Rect(0, offsetY, srcW, offsetY+cropH)
	}

	// Crop.
	cropped := image.NewRGBA(image.Rect(0, 0, cropRect.Dx(), cropRect.Dy()))
	draw.Draw(cropped, cropped.Bounds(), img, cropRect.Min, draw.Src)

	// Scale to target.
	dst := image.NewRGBA(image.Rect(0, 0, dims.Width, dims.Height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), cropped, cropped.Bounds(), xdraw.Over, nil)
	return dst
}
