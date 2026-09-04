package server

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"varkiv/internal/catalog"
)

const (
	maxThumbnailSourceBytes  = 64 << 20
	maxThumbnailSourcePixels = 20_000_000
	maxThumbnailCacheBytes   = 8 << 20
)

var (
	errMediaThumbnailUnsupported = errors.New("media type cannot be decoded for thumbnails")
	errMediaThumbnailUnverified  = errors.New("media identity is required for thumbnail caching")
	errMediaThumbnailTooLarge    = errors.New("media dimensions exceed thumbnail safety limits")
)

var thumbnailSizes = map[int]struct{}{128: {}, 256: {}, 480: {}, 768: {}}

func thumbnailSize(value string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 480, nil
	}
	size, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.New("thumbnail size must be one of 128, 256, 480, or 768")
	}
	if _, ok := thumbnailSizes[size]; !ok {
		return 0, errors.New("thumbnail size must be one of 128, 256, 480, or 768")
	}
	return size, nil
}

func (s *Server) downloadMediaThumbnail(w http.ResponseWriter, r *http.Request) {
	size, err := thumbnailSize(r.URL.Query().Get("size"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_thumbnail_size", err.Error())
		return
	}
	item, err := s.store.GetMedia(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}

	// Never allow a cache hit to mask a missing, changed, or unsafe original.
	source, err := s.storage.OpenVerifiedMedia(item)
	if err != nil {
		writeError(w, err)
		return
	}
	defer source.Close()
	if item.SHA256 == "" || !validSHA256(item.SHA256) {
		writeError(w, errMediaThumbnailUnverified)
		return
	}
	if item.Size < 0 || item.Size > maxThumbnailSourceBytes || !strings.HasPrefix(strings.ToLower(item.MIMEType), "image/") {
		writeError(w, errMediaThumbnailUnsupported)
		return
	}

	etag := `"thumbnail-v1:` + strings.ToLower(item.SHA256) + `:` + strconv.Itoa(size) + `"`
	s.thumbnailMu.Lock()
	data, err := s.loadOrCreateThumbnail(item, source, size)
	s.thumbnailMu.Unlock()
	if err != nil {
		writeError(w, err)
		return
	}
	if r.Header.Get("If-None-Match") == etag {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Disposition", `inline; filename="thumbnail.png"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func (s *Server) loadOrCreateThumbnail(item catalog.MediaAsset, source *os.File, size int) ([]byte, error) {
	cacheDirectory, err := s.thumbnailCacheDirectory(strings.ToLower(item.SHA256))
	if err != nil {
		return nil, err
	}
	cachePath := filepath.Join(cacheDirectory, strconv.Itoa(size)+".png")
	if data, ok := readValidThumbnail(cachePath, size); ok {
		return data, nil
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return nil, errMediaThumbnailUnsupported
	}
	config, format, err := image.DecodeConfig(source)
	if err != nil || (format != "png" && format != "jpeg" && format != "gif") {
		return nil, errMediaThumbnailUnsupported
	}
	if err = validateThumbnailSource(config.Width, config.Height); err != nil {
		return nil, err
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return nil, errMediaThumbnailUnsupported
	}
	decoded, _, err := image.Decode(source)
	if err != nil {
		return nil, errMediaThumbnailUnsupported
	}
	thumbnail := resizeThumbnail(decoded, size)
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, thumbnail); err != nil {
		return nil, fmt.Errorf("encode thumbnail: %w", err)
	}
	if encoded.Len() <= 0 || encoded.Len() > maxThumbnailCacheBytes {
		return nil, errMediaThumbnailTooLarge
	}
	if err = publishThumbnail(cacheDirectory, cachePath, encoded.Bytes()); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func validateThumbnailSource(width, height int) error {
	if width <= 0 || height <= 0 || width > 16_384 || height > 16_384 || int64(width)*int64(height) > maxThumbnailSourcePixels {
		return errMediaThumbnailTooLarge
	}
	return nil
}

func (s *Server) thumbnailCacheDirectory(hash string) (string, error) {
	if !validSHA256(hash) {
		return "", errMediaThumbnailUnverified
	}
	current := filepath.Join(s.stateRoot, "media")
	info, err := os.Lstat(current)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("media cache root is unsafe")
	}
	for _, component := range []string{"cache", "thumbnails-v1", hash[:2], hash} {
		current = filepath.Join(current, component)
		if err = os.Mkdir(current, 0o700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err = os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("media cache path is unsafe")
		}
		if err = os.Chmod(current, 0o700); err != nil {
			return "", err
		}
	}
	return current, nil
}

func readValidThumbnail(path string, size int) ([]byte, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxThumbnailCacheBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > size || config.Height > size {
		return nil, false
	}
	return data, true
}

func publishThumbnail(directory, target string, data []byte) error {
	temporary, err := os.CreateTemp(directory, ".thumbnail-*.png")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temporaryPath, target); err == nil {
		return nil
	}
	// Windows does not atomically replace an existing destination. Only an
	// exact, service-owned cache entry may be removed for the retry.
	info, statErr := os.Lstat(target)
	if statErr != nil || info.IsDir() {
		return err
	}
	if removeErr := os.Remove(target); removeErr != nil {
		return err
	}
	return os.Rename(temporaryPath, target)
}

func resizeThumbnail(source image.Image, maxEdge int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxEdge && height <= maxEdge {
		return source
	}
	scale := math.Min(float64(maxEdge)/float64(width), float64(maxEdge)/float64(height))
	targetWidth := max(1, int(math.Round(float64(width)*scale)))
	targetHeight := max(1, int(math.Round(float64(height)*scale)))
	target := image.NewRGBA64(image.Rect(0, 0, targetWidth, targetHeight))
	for y := range targetHeight {
		sy := (float64(y)+0.5)*float64(height)/float64(targetHeight) - 0.5
		y0 := max(0, min(height-1, int(math.Floor(sy))))
		y1 := min(height-1, y0+1)
		fy := sy - math.Floor(sy)
		for x := range targetWidth {
			sx := (float64(x)+0.5)*float64(width)/float64(targetWidth) - 0.5
			x0 := max(0, min(width-1, int(math.Floor(sx))))
			x1 := min(width-1, x0+1)
			fx := sx - math.Floor(sx)
			target.SetRGBA64(x, y, interpolateRGBA64(
				source.At(bounds.Min.X+x0, bounds.Min.Y+y0),
				source.At(bounds.Min.X+x1, bounds.Min.Y+y0),
				source.At(bounds.Min.X+x0, bounds.Min.Y+y1),
				source.At(bounds.Min.X+x1, bounds.Min.Y+y1), fx, fy,
			))
		}
	}
	return target
}

func interpolateRGBA64(topLeft, topRight, bottomLeft, bottomRight color.Color, fx, fy float64) color.RGBA64 {
	tlR, tlG, tlB, tlA := topLeft.RGBA()
	trR, trG, trB, trA := topRight.RGBA()
	blR, blG, blB, blA := bottomLeft.RGBA()
	brR, brG, brB, brA := bottomRight.RGBA()
	interpolate := func(tl, tr, bl, br uint32) uint16 {
		top := float64(tl)*(1-fx) + float64(tr)*fx
		bottom := float64(bl)*(1-fx) + float64(br)*fx
		return uint16(math.Round(top*(1-fy) + bottom*fy))
	}
	return color.RGBA64{R: interpolate(tlR, trR, blR, brR), G: interpolate(tlG, trG, blG, brG), B: interpolate(tlB, trB, blB, brB), A: interpolate(tlA, trA, blA, brA)}
}
