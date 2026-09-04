package deviceagent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
	"varkiv/internal/platforms"
	"varkiv/internal/portablepath"
)

const maxSyncBytes = portablepath.MaxSaveRevisionBytes
const maxSyncFiles = portablepath.MaxSaveRevisionFiles

func remainingSaveDownloadBudget(total, declared int64, fileCount int) (int64, error) {
	if fileCount < 0 || fileCount > maxSyncFiles || total < 0 || total > maxSyncBytes {
		return 0, errors.New("save download exceeds the aggregate size or file-count limit")
	}
	remaining := maxSyncBytes - total
	if declared >= 0 && declared > remaining {
		return 0, errors.New("save download exceeds the aggregate size limit")
	}
	return remaining, nil
}

const romCacheFullVerifyAge = 24 * time.Hour

// A small start/middle/end sample catches ordinary same-metadata drift while
// keeping periodic directory inventory practical on SD-card handhelds. The
// canonical identity remains a full SHA-256 and is refreshed at least daily.
const romSignalChunkSize int64 = 512

type bindingDescriptor struct {
	Binding        catalog.SaveBinding    `json:"binding"`
	Stream         catalog.SaveStream     `json:"stream"`
	EditionID      string                 `json:"edition_id"`
	SaveNamespace  string                 `json:"save_namespace"`
	PlatformID     string                 `json:"platform_id"`
	Serial         string                 `json:"serial,omitempty"`
	ProductCode    string                 `json:"product_code,omitempty"`
	TitleID        string                 `json:"title_id,omitempty"`
	TitleIDHigh    string                 `json:"title_id_high,omitempty"`
	TitleIDLow     string                 `json:"title_id_low,omitempty"`
	ROMMatchSHA256 string                 `json:"rom_match_sha256,omitempty"`
	ROMStem        string                 `json:"rom_stem,omitempty"`
	Driver         catalog.EmulatorDriver `json:"driver"`
}

type deviceConfigResponse struct {
	Device                         catalog.Device                          `json:"device"`
	DeviceProfile                  catalog.DeviceProfile                   `json:"device_profile"`
	Bindings                       []bindingDescriptor                     `json:"bindings"`
	Drivers                        []catalog.EmulatorDriver                `json:"drivers"`
	Cores                          []catalog.RetroArchCore                 `json:"retroarch_cores"`
	RuntimeAttestationRequirements []catalog.RuntimeAttestationRequirement `json:"runtime_attestation_requirements"`
	Platforms                      []platforms.Platform                    `json:"platforms"`
}

type saveStateRequest struct {
	StreamID       string `json:"stream_id"`
	BaseRevisionID string `json:"base_revision_id,omitempty"`
	ContentHash    string `json:"content_hash,omitempty"`
	HasLocalData   bool   `json:"has_local_data"`
}

type sessionRequest struct {
	DeviceID  string             `json:"device_id,omitempty"`
	Inventory []inventoryRequest `json:"inventory"`
	Saves     []saveStateRequest `json:"saves"`
}

type inventoryRequest struct {
	ClientItemID string `json:"client_item_id"`
	PlatformID   string `json:"platform_id"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
}

// localROMName is intentionally process-local. Only the opaque client item ID,
// platform, digest and size are serialized to the Central Hub.
type localROMName struct {
	PlatformID string
	SHA256     string
	Stem       string
}

type sessionResponse struct {
	Session   catalog.SyncSession     `json:"session"`
	Inventory []catalog.InventoryItem `json:"inventory"`
}

type operationUploadResponse struct {
	Session  catalog.SyncSession  `json:"session"`
	Revision catalog.SaveRevision `json:"revision"`
	Created  bool                 `json:"created"`
}

type LocalFile struct {
	LogicalPath string
	Path        string
	Checksum    string
	Size        int64
	MTimeNS     int64
	Mode        int64
}

type LocalSet struct {
	Descriptor    bindingDescriptor
	RenderedPaths []string
	Files         []LocalFile
	ContentHash   string
	TotalSize     int64
}

type SyncResult struct {
	SessionID  string
	Status     string
	Uploaded   int
	Downloaded int
	Conflicts  int
}

var ErrSyncConflict = errors.New("sync completed with save conflicts; no conflicting local data was overwritten")
var ErrSyncInProgress = errors.New("agent sync is already in progress")
var ErrInconsistentSharedSaveBinding = errors.New("shared save stream bindings resolve to different local targets or snapshots")
var ErrDeviceTargetUnavailable = errors.New("paired device profile target is unavailable")
var ErrDeviceTargetDrift = errors.New("paired device profile target changed; review and pair the device again")

func randomKey() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func hashValue(value any) string {
	data, _ := json.Marshal(value)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func renderBindingPath(template string, config Config, remote deviceConfigResponse, descriptor bindingDescriptor) (string, error) {
	resolveProfilePath := func(key string) string {
		value := config.PathOverrides[key]
		if value == "" {
			value = remote.DeviceProfile.Paths[key]
		}
		if value == "" {
			return ""
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(config.RootDir, filepath.FromSlash(value))
		}
		return filepath.Clean(value)
	}
	values := map[string]string{
		"edition.id": descriptor.EditionID, "edition.save_namespace": descriptor.SaveNamespace,
		"edition.serial": descriptor.Serial, "edition.product_code": descriptor.ProductCode,
		"edition.title_id": descriptor.TitleID, "edition.title_id_high": descriptor.TitleIDHigh, "edition.title_id_low": descriptor.TitleIDLow,
		"platform.id": descriptor.PlatformID, "rom.stem": descriptor.ROMStem, "driver.id": descriptor.Driver.ID,
		"driver.user_dir": config.DriverRoots[descriptor.Driver.ID],
		"device.id":       remote.Device.ID, "device.target": remote.DeviceProfile.Target,
		"device.config_dir": resolveProfilePath("config_dir"), "device.save_dir": resolveProfilePath("save_dir"),
		"device.core_dir": resolveProfilePath("core_dir"), "device.emulator_dir": resolveProfilePath("emulator_dir"),
	}
	rendered := template
	for _, name := range templateVariables(template) {
		if strings.TrimSpace(values[name]) == "" {
			return "", fmt.Errorf("save binding requires unavailable %s", name)
		}
	}
	for name, value := range values {
		rendered = strings.ReplaceAll(rendered, "{{"+name+"}}", value)
	}
	if strings.ContainsAny(rendered, "{}\x00\r\n") || strings.TrimSpace(rendered) == "" {
		return "", errors.New("save binding contains an unresolved or unsafe path variable")
	}
	rendered = filepath.Clean(filepath.FromSlash(rendered))
	if !filepath.IsAbs(rendered) {
		rendered = filepath.Join(config.RootDir, rendered)
	}
	abs, err := filepath.Abs(rendered)
	if err != nil {
		return "", err
	}
	allowedRoots := []string{config.RootDir}
	for _, value := range config.PathOverrides {
		if filepath.IsAbs(value) {
			allowedRoots = append(allowedRoots, filepath.Clean(value))
		}
	}
	for _, value := range config.DriverRoots {
		if filepath.IsAbs(value) {
			allowedRoots = append(allowedRoots, filepath.Clean(value))
		}
	}
	allowed := false
	matchedRoot := ""
	for _, root := range allowedRoots {
		rel, relErr := filepath.Rel(root, abs)
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			allowed = true
			matchedRoot = root
			break
		}
	}
	if !allowed {
		return "", errors.New("rendered save path is outside configured agent roots")
	}
	if filepath.Clean(abs) == filepath.Clean(config.RootDir) {
		return "", errors.New("save binding must not target the entire agent root")
	}
	if err = rejectSymlinkTraversal(matchedRoot, abs); err != nil {
		return "", err
	}
	return abs, nil
}

func templateVariables(value string) []string {
	result := []string{}
	for start := 0; ; {
		left := strings.Index(value[start:], "{{")
		if left < 0 {
			return result
		}
		left += start
		right := strings.Index(value[left+2:], "}}")
		if right < 0 {
			return result
		}
		right += left + 2
		result = append(result, value[left+2:right])
		start = right + 2
	}
}

func rejectSymlinkTraversal(root, target string) error {
	root, target = filepath.Clean(root), filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("save path escaped its configured root")
	}
	current := root
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("save path must not traverse a symbolic link")
		}
	}
	return nil
}

func hashFile(path string) (string, int64, error) {
	handle, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer handle.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, handle)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func contentHash(files []LocalFile) string {
	ordered := append([]LocalFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].LogicalPath < ordered[j].LogicalPath })
	hash := sha256.New()
	for _, file := range ordered {
		hash.Write([]byte(file.LogicalPath))
		hash.Write([]byte{0})
		hash.Write([]byte(file.Checksum))
		hash.Write([]byte{0})
		hash.Write([]byte(fmt.Sprint(file.Size)))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func addLocalFile(set *LocalSet, path, logical string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return nil
	}
	var err error
	logical, err = portableLogicalPath(logical)
	if err != nil {
		return err
	}
	for _, existing := range set.Files {
		if existing.LogicalPath == logical {
			return errors.New("local save set contains a duplicate logical path")
		}
	}
	checksum, size, err := hashFile(path)
	if err != nil {
		return err
	}
	set.TotalSize += size
	if set.TotalSize > maxSyncBytes || len(set.Files) >= maxSyncFiles {
		return errors.New("local save set exceeds the sync size or file-count limit")
	}
	set.Files = append(set.Files, LocalFile{LogicalPath: logical, Path: path, Checksum: checksum, Size: size, MTimeNS: info.ModTime().UnixNano(), Mode: int64(info.Mode().Perm())})
	return nil
}

func stableSingleFileLogicalPath(localPath string) string {
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(localPath)), ".")
	if len(extension) == 0 || len(extension) > 16 {
		return "primary"
	}
	for _, character := range extension {
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')) {
			return "primary"
		}
	}
	return "primary." + extension
}

func enumerateBinding(config Config, remote deviceConfigResponse, descriptor bindingDescriptor) (LocalSet, error) {
	set := LocalSet{Descriptor: descriptor}
	for _, template := range descriptor.Binding.LocalPaths {
		resolved, err := renderBindingPath(template, config, remote, descriptor)
		if err != nil {
			return LocalSet{}, err
		}
		set.RenderedPaths = append(set.RenderedPaths, resolved)
		info, err := os.Lstat(resolved)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return LocalSet{}, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return LocalSet{}, errors.New("save binding target must not be a symbolic link")
		}
		if info.IsDir() {
			err = filepath.Walk(resolved, func(path string, item os.FileInfo, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if item.Mode()&os.ModeSymlink != 0 {
					if item.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if item.IsDir() {
					return nil
				}
				rel, relErr := filepath.Rel(resolved, path)
				if relErr != nil {
					return relErr
				}
				return addLocalFile(&set, path, rel, item)
			})
			if err != nil {
				return LocalSet{}, err
			}
			continue
		}
		logical := filepath.Base(resolved)
		mode, _ := descriptor.Binding.Discovery["mode"].(string)
		if mode == "file" || (mode == "" && descriptor.Driver.Save.Layout == "single-file") {
			// A single-file binding already carries the exact local destination.
			// Use a stable role name in the self-hosted revision instead of
			// disclosing the device-local ROM/save basename.
			logical = stableSingleFileLogicalPath(resolved)
		}
		if err = addLocalFile(&set, resolved, logical, info); err != nil {
			return LocalSet{}, err
		}
	}
	if len(set.Files) > 0 {
		set.ContentHash = contentHash(set.Files)
	}
	return set, nil
}

func localSetBindingIdentity(set LocalSet) string {
	paths := append([]string(nil), set.RenderedPaths...)
	sort.Strings(paths)
	type fileIdentity struct {
		LogicalPath string `json:"logical_path"`
		Path        string `json:"path"`
		Checksum    string `json:"checksum"`
		Size        int64  `json:"size"`
		Mode        int64  `json:"mode"`
	}
	files := make([]fileIdentity, 0, len(set.Files))
	for _, file := range set.Files {
		files = append(files, fileIdentity{LogicalPath: file.LogicalPath, Path: file.Path, Checksum: file.Checksum, Size: file.Size, Mode: file.Mode})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].LogicalPath != files[j].LogicalPath {
			return files[i].LogicalPath < files[j].LogicalPath
		}
		return files[i].Path < files[j].Path
	})
	return hashValue(struct {
		Paths []string       `json:"paths"`
		Files []fileIdentity `json:"files"`
	}{Paths: paths, Files: files})
}

// collectLocalSets collapses multiple Edition bindings for one platform or
// container stream into one device-side state. A shared stream is only safe
// when every binding resolves to the same local target and exact snapshot;
// otherwise choosing one descriptor by return order would risk synchronizing
// or replacing the wrong container.
func collectLocalSets(config Config, remote deviceConfigResponse) (map[string]LocalSet, error) {
	sets := map[string]LocalSet{}
	identities := map[string]string{}
	for _, descriptor := range remote.Bindings {
		streamID := strings.TrimSpace(descriptor.Stream.ID)
		if streamID == "" {
			return nil, errors.New("save binding references a stream without an id")
		}
		set, err := enumerateBinding(config, remote, descriptor)
		if err != nil {
			return nil, fmt.Errorf("save binding %s: %w", descriptor.Binding.ID, err)
		}
		identity := localSetBindingIdentity(set)
		if previous, exists := identities[streamID]; exists {
			if previous != identity {
				return nil, ErrInconsistentSharedSaveBinding
			}
			continue
		}
		sets[streamID] = set
		identities[streamID] = identity
	}
	return sets, nil
}

func quickFileSignal(path string, info os.FileInfo) (string, error) {
	if !info.Mode().IsRegular() {
		return "", errors.New("ROM inventory item must be a regular file")
	}
	handle, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	hash := sha256.New()
	_, _ = io.WriteString(hash, strconv.FormatInt(info.Size(), 10)+"\x00"+strconv.FormatInt(info.ModTime().UnixNano(), 10)+"\x00")
	offsets := []int64{0}
	if info.Size() > romSignalChunkSize {
		offsets = append(offsets, max(0, info.Size()/2-romSignalChunkSize/2), max(0, info.Size()-romSignalChunkSize))
	}
	seen := map[int64]bool{}
	buffer := make([]byte, romSignalChunkSize)
	for _, offset := range offsets {
		if seen[offset] {
			continue
		}
		seen[offset] = true
		count, readErr := handle.ReadAt(buffer, offset)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return "", readErr
		}
		_, _ = io.WriteString(hash, strconv.FormatInt(offset, 10)+"\x00")
		_, _ = hash.Write(buffer[:count])
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func quickDirectorySignal(ctx context.Context, root string) (string, int64, error) {
	hash := sha256.New()
	var total int64
	fileCount := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("directory ROM inventory contains a symbolic link")
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("directory ROM inventory contains a non-regular file")
		}
		fileCount++
		if fileCount > 100000 {
			return errors.New("directory ROM inventory exceeds the 100000 file safety limit")
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		signal, signalErr := quickFileSignal(path, info)
		if signalErr != nil {
			return signalErr
		}
		total += info.Size()
		_, _ = io.WriteString(hash, filepath.ToSlash(relative)+"\x00"+signal+"\x00")
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), total, nil
}

func cachedROMFingerprint(ctx context.Context, cache map[string]ROMCacheEntry, cacheKey, path, kind string, info os.FileInfo) (ROMCacheEntry, error) {
	var signal string
	var size int64
	var err error
	if kind == "directory" {
		signal, size, err = quickDirectorySignal(ctx, path)
	} else {
		signal, err = quickFileSignal(path, info)
		size = info.Size()
	}
	if err != nil {
		return ROMCacheEntry{}, err
	}
	now := time.Now().UTC()
	entry, cached := cache[cacheKey]
	verifiedAge := now.Sub(time.Unix(entry.VerifiedAt, 0))
	decodedHash, hashErr := hex.DecodeString(entry.SHA256)
	if cached && entry.Kind == kind && entry.Size == size && entry.Signal == signal && hashErr == nil && len(decodedHash) == sha256.Size && entry.VerifiedAt > 0 && verifiedAge >= 0 && verifiedAge < romCacheFullVerifyAge {
		return entry, nil
	}
	var checksum string
	if kind == "directory" {
		checksum, size, err = filehash.Directory(path)
	} else {
		checksum, size, err = filehash.File(path)
	}
	if err != nil {
		return ROMCacheEntry{}, err
	}
	postInfo, err := os.Lstat(path)
	if err != nil || (kind == "directory" && !postInfo.IsDir()) || (kind == "file" && !postInfo.Mode().IsRegular()) || postInfo.Mode()&os.ModeSymlink != 0 {
		return ROMCacheEntry{}, errors.New("ROM inventory item changed type during hashing")
	}
	var postSignal string
	var postSize int64
	if kind == "directory" {
		postSignal, postSize, err = quickDirectorySignal(ctx, path)
	} else {
		postSignal, err = quickFileSignal(path, postInfo)
		postSize = postInfo.Size()
	}
	if err != nil {
		return ROMCacheEntry{}, err
	}
	if postSignal != signal || postSize != size {
		return ROMCacheEntry{}, errors.New("ROM inventory item changed while its identity was being calculated")
	}
	return ROMCacheEntry{Kind: kind, Size: size, MTimeNS: info.ModTime().UnixNano(), Signal: signal, SHA256: checksum, VerifiedAt: now.Unix()}, nil
}

func appendROMInventoryItem(ctx context.Context, items *[]inventoryRequest, cache, previous map[string]ROMCacheEntry, platformID, root, path, kind string, info os.FileInfo) error {
	if len(*items) >= 10000 {
		return errors.New("ROM inventory exceeds the 10000 item session limit")
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	relative = filepath.ToSlash(relative)
	identityDigest := sha256.Sum256([]byte(platformID + "\x00" + relative))
	clientItemID := hex.EncodeToString(identityDigest[:])
	entry, err := cachedROMFingerprint(ctx, previous, clientItemID, path, kind, info)
	if err != nil {
		return err
	}
	cache[clientItemID] = entry
	*items = append(*items, inventoryRequest{ClientItemID: clientItemID, PlatformID: platformID, SHA256: entry.SHA256, Size: entry.Size})
	return nil
}

func enumerateROMInventory(ctx context.Context, config *Config) ([]inventoryRequest, error) {
	items, _, err := enumerateROMInventoryWithLocalNames(ctx, config)
	return items, err
}

func enumerateROMInventoryWithLocalNames(ctx context.Context, config *Config) ([]inventoryRequest, []localROMName, error) {
	registry, err := platforms.NewRegistry(platforms.All())
	if err != nil {
		return nil, nil, err
	}
	return enumerateROMInventoryWithRegistry(ctx, config, registry)
}

func enumerateROMInventoryWithRegistry(ctx context.Context, config *Config, registry platforms.Registry) ([]inventoryRequest, []localROMName, error) {
	items := []inventoryRequest{}
	localNames := []localROMName{}
	cache := map[string]ROMCacheEntry{}
	platformIDs := make([]string, 0, len(config.ROMRoots))
	for platformID := range config.ROMRoots {
		platformIDs = append(platformIDs, platformID)
	}
	sort.Strings(platformIDs)
	for _, requestedPlatform := range platformIDs {
		preset, ok := registry.Resolve(requestedPlatform)
		if !ok {
			return nil, nil, fmt.Errorf("ROM inventory platform %q is not registered", requestedPlatform)
		}
		root, err := filepath.Abs(config.ROMRoots[requestedPlatform])
		if err != nil {
			return nil, nil, err
		}
		rootInfo, err := os.Lstat(root)
		if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("ROM inventory root for %s must be an existing non-symlink directory", preset.ID)
		}
		extensions := map[string]bool{}
		allowDirectories := false
		for _, value := range preset.Extensions {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "directory" {
				allowDirectories = true
				continue
			}
			if value != "" {
				if !strings.HasPrefix(value, ".") {
					value = "." + value
				}
				extensions[value] = true
			}
		}
		err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if path == root {
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info.IsDir() {
				if strings.HasPrefix(info.Name(), ".") {
					return filepath.SkipDir
				}
				if allowDirectories && filepath.Dir(path) == root {
					if err := appendROMInventoryItem(ctx, &items, cache, config.ROMCache, preset.ID, root, path, "directory", info); err != nil {
						return err
					}
					localNames = append(localNames, localROMName{PlatformID: preset.ID, SHA256: items[len(items)-1].SHA256, Stem: info.Name()})
					return filepath.SkipDir
				}
				return nil
			}
			if !info.Mode().IsRegular() || !extensions[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			if err := appendROMInventoryItem(ctx, &items, cache, config.ROMCache, preset.ID, root, path, "file", info); err != nil {
				return err
			}
			localNames = append(localNames, localROMName{PlatformID: preset.ID, SHA256: items[len(items)-1].SHA256, Stem: strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))})
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ClientItemID < items[j].ClientItemID })
	config.ROMCache = cache
	return items, localNames, nil
}

func resolveDeviceLocalROMStems(bindings []bindingDescriptor, names []localROMName) ([]bindingDescriptor, error) {
	resolved := append([]bindingDescriptor(nil), bindings...)
	for index := range resolved {
		descriptor := &resolved[index]
		if !strings.Contains(strings.Join(descriptor.Binding.LocalPaths, "\x00"), "{{rom.stem}}") {
			continue
		}
		matchHash := strings.ToLower(strings.TrimSpace(descriptor.ROMMatchSHA256))
		if matchHash == "" {
			continue
		}
		stems := map[string]bool{}
		for _, name := range names {
			if name.PlatformID == descriptor.PlatformID && name.SHA256 == matchHash && strings.TrimSpace(name.Stem) != "" {
				stems[name.Stem] = true
			}
		}
		if len(stems) > 1 {
			return nil, errors.New("ROM identity matches multiple device-local basenames")
		}
		for stem := range stems {
			descriptor.ROMStem = stem
		}
	}
	return resolved, nil
}

func fetchDeviceConfig(ctx context.Context, client *http.Client, config Config) (deviceConfigResponse, error) {
	var response deviceConfigResponse
	err := doJSON(ctx, client, http.MethodGet, endpoint(config.ServerURL, "/api/v1/sync/config"), config.AccessToken, "", nil, &response)
	return response, err
}

func reconcileDeviceTarget(configPath string, config *Config, remote deviceConfigResponse) error {
	target := strings.ToLower(strings.TrimSpace(remote.DeviceProfile.Target))
	if target == "" {
		return ErrDeviceTargetUnavailable
	}
	if config.DeviceTarget != "" && config.DeviceTarget != target {
		return ErrDeviceTargetDrift
	}
	if config.DeviceTarget == "" {
		config.DeviceTarget = target
		if err := UpdateConfig(configPath, *config); err != nil {
			return fmt.Errorf("record paired device target: %w", err)
		}
	}
	return nil
}

func createSession(ctx context.Context, client *http.Client, config Config, key string, request sessionRequest) (sessionResponse, error) {
	var response sessionResponse
	err := doJSON(ctx, client, http.MethodPost, endpoint(config.ServerURL, "/api/v1/sync/sessions"), config.AccessToken, key, request, &response)
	return response, err
}

func uploadOperation(ctx context.Context, client *http.Client, config Config, sessionID, operationID string, set LocalSet) (operationUploadResponse, error) {
	var err error
	if sessionID, err = protocolPathSegment(sessionID); err != nil {
		return operationUploadResponse{}, err
	}
	if operationID, err = protocolPathSegment(operationID); err != nil {
		return operationUploadResponse{}, err
	}
	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	errCh := make(chan error, 1)
	go func() {
		var writeErr error
		defer func() {
			if writeErr == nil {
				writeErr = writer.Close()
			} else {
				_ = writer.Close()
			}
			_ = pipeWriter.CloseWithError(writeErr)
			errCh <- writeErr
		}()
		manifest := struct {
			EditionID string           `json:"edition_id"`
			Files     []map[string]any `json:"files"`
		}{EditionID: set.Descriptor.EditionID, Files: make([]map[string]any, 0, len(set.Files))}
		for _, file := range set.Files {
			manifest.Files = append(manifest.Files, map[string]any{"logical_path": file.LogicalPath, "mtime_ns": file.MTimeNS, "mode": file.Mode})
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			writeErr = err
			return
		}
		field, err := writer.CreateFormField("manifest")
		if err != nil {
			writeErr = err
			return
		}
		if _, err = field.Write(data); err != nil {
			writeErr = err
			return
		}
		for _, file := range set.Files {
			part, err := writer.CreateFormFile("files", filepath.Base(file.LogicalPath))
			if err != nil {
				writeErr = err
				return
			}
			handle, err := os.Open(file.Path)
			if err != nil {
				writeErr = err
				return
			}
			_, copyErr := io.Copy(part, handle)
			closeErr := handle.Close()
			if copyErr != nil {
				writeErr = copyErr
				return
			}
			if closeErr != nil {
				writeErr = closeErr
				return
			}
		}
	}()
	target := endpoint(config.ServerURL, "/api/v1/sync/sessions/"+sessionID+"/operations/"+operationID+"/upload")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, pipeReader)
	if err != nil {
		return operationUploadResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+config.AccessToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	writerErr := <-errCh
	if err != nil {
		return operationUploadResponse{}, err
	}
	defer resp.Body.Close()
	if writerErr != nil {
		return operationUploadResponse{}, writerErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure apiFailure
		data, readErr := readAgentResponse(resp.Body, 1<<20)
		if readErr != nil {
			return operationUploadResponse{}, errors.New("server error response exceeded the size limit")
		}
		_ = json.Unmarshal(data, &failure)
		if protocolIDPattern.MatchString(failure.Error.Code) {
			return operationUploadResponse{}, fmt.Errorf("server rejected request: %s", failure.Error.Code)
		}
		return operationUploadResponse{}, fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}
	var output operationUploadResponse
	data, err := readAgentResponse(resp.Body, maxAgentJSONResponse)
	if err != nil {
		return operationUploadResponse{}, err
	}
	if err = json.Unmarshal(data, &output); err != nil {
		return operationUploadResponse{}, errors.New("server returned invalid JSON")
	}
	return output, nil
}

func portableLogicalPath(value string) (string, error) {
	value, err := portablepath.CleanSaveLogical(value)
	if err != nil {
		return "", errors.New("server returned an unsafe save logical path")
	}
	return value, nil
}

func downloadRevision(ctx context.Context, client *http.Client, config Config, remote deviceConfigResponse, session catalog.SyncSession, op catalog.SyncOperation, set LocalSet) (catalog.SaveRevision, error) {
	var revision catalog.SaveRevision
	sessionID, err := protocolPathSegment(session.ID)
	if err != nil {
		return catalog.SaveRevision{}, err
	}
	operationID, err := protocolPathSegment(op.ID)
	if err != nil {
		return catalog.SaveRevision{}, err
	}
	revisionID, err := protocolPathSegment(op.TargetRevisionID)
	if err != nil {
		return catalog.SaveRevision{}, err
	}
	if err := doJSON(ctx, client, http.MethodGet, endpoint(config.ServerURL, "/api/v1/save-revisions/"+revisionID), config.AccessToken, "", nil, &revision); err != nil {
		return catalog.SaveRevision{}, err
	}
	if revision.ID != revisionID {
		return catalog.SaveRevision{}, errors.New("server returned a different save revision")
	}
	if _, err = remainingSaveDownloadBudget(0, -1, len(revision.Files)); err != nil {
		return catalog.SaveRevision{}, err
	}
	stagingRoot := filepath.Join(config.RootDir, ".varkiv", "staging")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return catalog.SaveRevision{}, err
	}
	stage, err := os.MkdirTemp(stagingRoot, ".download-*")
	if err != nil {
		return catalog.SaveRevision{}, err
	}
	defer os.RemoveAll(stage)
	downloaded := []LocalFile{}
	var totalSize int64
	for _, metadata := range revision.Files {
		fileID, idErr := protocolPathSegment(metadata.ID)
		if idErr != nil {
			return catalog.SaveRevision{}, idErr
		}
		logical, pathErr := portableLogicalPath(metadata.LogicalPath)
		if pathErr != nil {
			return catalog.SaveRevision{}, pathErr
		}
		stagedPath := filepath.Join(stage, filepath.FromSlash(logical))
		if err = os.MkdirAll(filepath.Dir(stagedPath), 0o700); err != nil {
			return catalog.SaveRevision{}, err
		}
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(config.ServerURL, "/api/v1/sync/sessions/"+sessionID+"/operations/"+operationID+"/files/"+fileID+"/content"), nil)
		if requestErr != nil {
			return catalog.SaveRevision{}, requestErr
		}
		req.Header.Set("Authorization", "Bearer "+config.AccessToken)
		resp, requestErr := client.Do(req)
		if requestErr != nil {
			return catalog.SaveRevision{}, requestErr
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return catalog.SaveRevision{}, fmt.Errorf("save download returned HTTP %d", resp.StatusCode)
		}
		remaining, budgetErr := remainingSaveDownloadBudget(totalSize, resp.ContentLength, len(revision.Files))
		if budgetErr != nil {
			resp.Body.Close()
			return catalog.SaveRevision{}, budgetErr
		}
		temp, createErr := os.OpenFile(stagedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			resp.Body.Close()
			return catalog.SaveRevision{}, createErr
		}
		hash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(resp.Body, remaining+1))
		bodyCloseErr := resp.Body.Close()
		fileCloseErr := temp.Close()
		if copyErr != nil || bodyCloseErr != nil || fileCloseErr != nil || size > remaining {
			return catalog.SaveRevision{}, errors.New("save download failed or exceeded the size limit")
		}
		totalSize += size
		downloaded = append(downloaded, LocalFile{LogicalPath: logical, Path: stagedPath, Checksum: hex.EncodeToString(hash.Sum(nil)), Size: size, Mode: metadata.Mode})
	}
	if actual := contentHash(downloaded); actual != revision.ContentHash || actual != op.ExpectedHash {
		return catalog.SaveRevision{}, errors.New("downloaded save set failed content hash verification")
	}
	if len(set.RenderedPaths) != 1 {
		return catalog.SaveRevision{}, errors.New("download requires exactly one configured local path root")
	}
	target := set.RenderedPaths[0]
	targetExists := false
	if _, statErr := os.Lstat(target); statErr == nil {
		targetExists = true
		if set.ContentHash == "" {
			return catalog.SaveRevision{}, errors.New("refusing to replace an untracked local save target")
		}
		current, currentErr := enumerateBinding(config, remote, set.Descriptor)
		if currentErr != nil || current.ContentHash != set.ContentHash {
			return catalog.SaveRevision{}, errors.New("local save changed during download; refusing to overwrite it")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return catalog.SaveRevision{}, statErr
	}
	mode, _ := set.Descriptor.Binding.Discovery["mode"].(string)
	singleFile := mode == "file" || (mode == "" && set.Descriptor.Driver.Save.Layout == "single-file")
	if err = installDownloadedSave(config, session, op, set, downloaded, target, targetExists, singleFile, defaultSaveInstallOps()); err != nil {
		return catalog.SaveRevision{}, err
	}
	return revision, nil
}

type saveInstallOps struct {
	copyFile         func(string, string, os.FileMode) error
	installDirectory func(string, []LocalFile) error
	rename           func(string, string) error
	replaceFile      func(string, string) error
	removeAll        func(string) error
}

func defaultSaveInstallOps() saveInstallOps {
	return saveInstallOps{
		copyFile: copyExclusive, installDirectory: installDirectoryExclusive,
		rename: os.Rename, replaceFile: replaceFile, removeAll: os.RemoveAll,
	}
}

func verifyInstalledSave(path string, files []LocalFile, singleFile bool) error {
	verified := make([]LocalFile, 0, len(files))
	for _, file := range files {
		candidate := filepath.Join(path, filepath.FromSlash(file.LogicalPath))
		if singleFile {
			candidate = path
		}
		checksum, size, err := hashFile(candidate)
		if err != nil {
			return err
		}
		if checksum != file.Checksum || size != file.Size {
			return errors.New("local save copy failed checksum verification")
		}
		verified = append(verified, LocalFile{LogicalPath: file.LogicalPath, Checksum: checksum, Size: size})
	}
	if contentHash(verified) != contentHash(files) {
		return errors.New("local save copy failed set verification")
	}
	return nil
}

func installDownloadedSave(config Config, session catalog.SyncSession, op catalog.SyncOperation, current LocalSet, downloaded []LocalFile, target string, targetExists, singleFile bool, ops saveInstallOps) error {
	if singleFile && len(downloaded) != 1 {
		return errors.New("single-file save binding received a multi-file revision")
	}
	if targetExists {
		backupRoot := filepath.Join(config.RootDir, ".varkiv", "backups", op.StreamID)
		if err := os.MkdirAll(backupRoot, 0o700); err != nil {
			return fmt.Errorf("prepare recoverable save backup: %w", err)
		}
		suffix, err := randomKey()
		if err != nil {
			return err
		}
		backupPath := filepath.Join(backupRoot, session.ID+"-"+suffix)
		if err = ops.installDirectory(backupPath, current.Files); err != nil {
			return fmt.Errorf("create recoverable save backup: %w", err)
		}
		if err = verifyInstalledSave(backupPath, current.Files, false); err != nil {
			return fmt.Errorf("verify recoverable save backup: %w", err)
		}
	}

	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("prepare save destination: %w", err)
	}
	stageRoot, err := os.MkdirTemp(parent, ".varkiv-install-*")
	if err != nil {
		return fmt.Errorf("prepare same-volume save staging: %w", err)
	}
	defer ops.removeAll(stageRoot)
	staged := filepath.Join(stageRoot, "payload")
	if singleFile {
		err = ops.copyFile(downloaded[0].Path, staged, os.FileMode(downloaded[0].Mode))
	} else {
		err = ops.installDirectory(staged, downloaded)
	}
	if err != nil {
		return fmt.Errorf("stage downloaded save: %w", err)
	}
	if err = verifyInstalledSave(staged, downloaded, singleFile); err != nil {
		return fmt.Errorf("verify staged save: %w", err)
	}

	if !targetExists {
		if err = ops.rename(staged, target); err != nil {
			return fmt.Errorf("install downloaded save: %w", err)
		}
		return nil
	}
	if singleFile {
		if err = ops.replaceFile(staged, target); err != nil {
			return fmt.Errorf("atomically replace downloaded save: %w", err)
		}
		return nil
	}

	suffix, err := randomKey()
	if err != nil {
		return err
	}
	previous := filepath.Join(parent, ".varkiv-previous-"+suffix)
	if err = ops.rename(target, previous); err != nil {
		return fmt.Errorf("prepare recoverable directory replacement: %w", err)
	}
	if err = ops.rename(staged, target); err != nil {
		if restoreErr := ops.rename(previous, target); restoreErr != nil {
			return errors.New("install failed and automatic restore failed; the verified backup and original recovery directory were preserved")
		}
		return fmt.Errorf("install downloaded save directory: %w", err)
	}
	// previous is an exact path created by the successful rename above. The
	// verified copy in .varkiv/backups remains the recoverable history.
	_ = ops.removeAll(previous)
	return nil
}

func copyExclusive(source, target string, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o600
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return fmt.Errorf("refusing to overwrite local save: %w", err)
	}
	created := true
	defer func() {
		_ = output.Close()
		if created {
			_ = os.Remove(target)
		}
	}()
	if _, err = io.Copy(output, input); err != nil {
		return err
	}
	if err = output.Sync(); err != nil {
		return err
	}
	if err = output.Close(); err != nil {
		return err
	}
	created = false
	return nil
}

func installDirectoryExclusive(target string, files []LocalFile) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return fmt.Errorf("refusing to overwrite local save directory: %w", err)
	}
	createdFiles := []string{}
	createdDirs := []string{target}
	cleanup := func() {
		for index := len(createdFiles) - 1; index >= 0; index-- {
			_ = os.Remove(createdFiles[index])
		}
		for index := len(createdDirs) - 1; index >= 0; index-- {
			_ = os.Remove(createdDirs[index])
		}
	}
	for _, file := range files {
		destination := filepath.Join(target, filepath.FromSlash(file.LogicalPath))
		parent := filepath.Dir(destination)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			cleanup()
			return err
		}
		for current := parent; current != target && current != filepath.Dir(current); current = filepath.Dir(current) {
			createdDirs = append(createdDirs, current)
		}
		if err := copyExclusive(file.Path, destination, os.FileMode(file.Mode)); err != nil {
			cleanup()
			return err
		}
		createdFiles = append(createdFiles, destination)
	}
	return nil
}

func acknowledgeDownload(ctx context.Context, client *http.Client, config Config, sessionID, operationID, actualHash string) error {
	var err error
	if sessionID, err = protocolPathSegment(sessionID); err != nil {
		return err
	}
	if operationID, err = protocolPathSegment(operationID); err != nil {
		return err
	}
	return doJSON(ctx, client, http.MethodPost, endpoint(config.ServerURL, "/api/v1/sync/sessions/"+sessionID+"/operations/"+operationID+"/ack"), config.AccessToken, "", map[string]string{"actual_hash": actualHash}, nil)
}

func heartbeat(ctx context.Context, client *http.Client, config Config, capabilities map[string]bool, attestations []catalog.RuntimeAttestationReport) error {
	deviceID, err := protocolPathSegment(config.DeviceID)
	if err != nil {
		return err
	}
	return doJSON(ctx, client, http.MethodPost, endpoint(config.ServerURL, "/api/v1/devices/"+deviceID+"/heartbeat"), config.AccessToken, "", map[string]any{"capabilities": capabilities, "runtime_attestations": attestations}, nil)
}

func recordSyncStart(configPath string, attemptedAt time.Time) error {
	config, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	lastSuccess := ""
	if config.LastSync != nil {
		lastSuccess = config.LastSync.LastSuccessAt
	}
	config.LastSync = &AgentSyncStatus{State: "running", AttemptedAt: attemptedAt.UTC().Format(time.RFC3339Nano), LastSuccessAt: lastSuccess}
	return UpdateConfig(configPath, config)
}

func recordSyncFinish(configPath string, attemptedAt time.Time, result SyncResult, syncErr error) error {
	config, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	lastSuccess := ""
	if config.LastSync != nil {
		lastSuccess = config.LastSync.LastSuccessAt
	}
	state := "complete"
	errorCode := ""
	if errors.Is(syncErr, ErrSyncConflict) {
		state = "conflict"
		errorCode = "sync_conflict"
	} else if syncErr != nil {
		state = "failed"
		errorCode = "sync_failed"
	}
	finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if state == "complete" {
		lastSuccess = finishedAt
	}
	config.LastSync = &AgentSyncStatus{
		State:           state,
		AttemptedAt:     attemptedAt.UTC().Format(time.RFC3339Nano),
		FinishedAt:      finishedAt,
		LastSuccessAt:   lastSuccess,
		SessionRecorded: result.SessionID != "",
		Uploaded:        result.Uploaded,
		Downloaded:      result.Downloaded,
		Conflicts:       result.Conflicts,
		ErrorCode:       errorCode,
	}
	return UpdateConfig(configPath, config)
}

func SyncOnce(ctx context.Context, configPath string) (SyncResult, error) {
	release, err := acquireSyncLock(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	defer release()
	attemptedAt := time.Now().UTC()
	if err = recordSyncStart(configPath, attemptedAt); err != nil {
		return SyncResult{}, err
	}
	result, syncErr := syncOnce(ctx, configPath)
	if statusErr := recordSyncFinish(configPath, attemptedAt, result, syncErr); statusErr != nil {
		if syncErr == nil {
			return result, fmt.Errorf("record agent sync status: %w", statusErr)
		}
		return result, errors.Join(syncErr, fmt.Errorf("record agent sync status: %w", statusErr))
	}
	return result, syncErr
}

func syncOnce(ctx context.Context, configPath string) (SyncResult, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	client := defaultClient()
	remote, err := fetchDeviceConfig(ctx, client, config)
	if err != nil {
		return SyncResult{}, err
	}
	if err = reconcileDeviceTarget(configPath, &config, remote); err != nil {
		return SyncResult{}, err
	}
	probe, err := probeRuntime(config, remote)
	if err != nil {
		return SyncResult{}, err
	}
	capabilities := map[string]bool{"runtime_probe": true, "emulator_dir_configured": probe.EmulatorDirConfigured, "core_dir_configured": probe.CoreDirConfigured, "emulator_installed": probe.InstalledDrivers > 0, "retroarch_core_installed": probe.InstalledCores > 0}
	if err = heartbeat(ctx, client, config, capabilities, probe.Attestations); err != nil {
		return SyncResult{}, err
	}
	// The first config response supplies the current attestation requirements.
	// Fetch it again only after the atomic heartbeat so the server can include
	// newly authorized cross-driver bindings, or revoke a binding immediately
	// when an exact runtime file disappeared or drifted.
	remote, err = fetchDeviceConfig(ctx, client, config)
	if err != nil {
		return SyncResult{}, err
	}
	platformItems := remote.Platforms
	if len(platformItems) == 0 {
		platformItems = platforms.All()
	}
	registry, err := platforms.NewRegistry(platformItems)
	if err != nil {
		return SyncResult{}, fmt.Errorf("server platform registry: %w", err)
	}
	inventory, localROMNames, err := enumerateROMInventoryWithRegistry(ctx, &config, registry)
	if err != nil {
		return SyncResult{}, err
	}
	remote.Bindings, err = resolveDeviceLocalROMStems(remote.Bindings, localROMNames)
	if err != nil {
		return SyncResult{}, err
	}
	sets, err := collectLocalSets(config, remote)
	if err != nil {
		return SyncResult{}, err
	}
	request := sessionRequest{DeviceID: config.DeviceID, Inventory: inventory, Saves: []saveStateRequest{}}
	for streamID, set := range sets {
		state := config.Streams[streamID]
		request.Saves = append(request.Saves, saveStateRequest{StreamID: streamID, BaseRevisionID: state.RevisionID, ContentHash: set.ContentHash, HasLocalData: len(set.Files) > 0})
	}
	sort.Slice(request.Saves, func(i, j int) bool { return request.Saves[i].StreamID < request.Saves[j].StreamID })
	fingerprint := hashValue(request)
	key := ""
	if config.Pending != nil && config.Pending.Fingerprint == fingerprint {
		key = config.Pending.IdempotencyKey
	} else {
		key, err = randomKey()
		if err != nil {
			return SyncResult{}, err
		}
		config.Pending = &PendingSync{IdempotencyKey: key, Fingerprint: fingerprint}
		if err = UpdateConfig(configPath, config); err != nil {
			return SyncResult{}, err
		}
	}
	response, err := createSession(ctx, client, config, key, request)
	if err != nil {
		return SyncResult{}, err
	}
	sessionID, err := protocolPathSegment(response.Session.ID)
	if err != nil {
		return SyncResult{}, err
	}
	result := SyncResult{SessionID: sessionID, Status: response.Session.Status}
	for _, operation := range response.Session.Operations {
		operationID, idErr := protocolPathSegment(operation.ID)
		if idErr != nil {
			return result, idErr
		}
		set, ok := sets[operation.StreamID]
		if !ok {
			return result, errors.New("server planned an operation for an unconfigured save stream")
		}
		switch operation.Action {
		case "upload":
			uploaded, uploadErr := uploadOperation(ctx, client, config, sessionID, operationID, set)
			if uploadErr != nil {
				return result, uploadErr
			}
			revisionID, idErr := protocolPathSegment(uploaded.Revision.ID)
			if idErr != nil {
				return result, idErr
			}
			revisionHash, hashErr := protocolSHA256(uploaded.Revision.ContentHash)
			if hashErr != nil || revisionHash != set.ContentHash {
				return result, errors.New("server returned an inconsistent uploaded revision")
			}
			config.Streams[operation.StreamID] = StreamState{RevisionID: revisionID, ContentHash: revisionHash}
			result.Uploaded++
			result.Status = uploaded.Session.Status
		case "download":
			response.Session.ID = sessionID
			operation.ID = operationID
			revision, downloadErr := downloadRevision(ctx, client, config, remote, response.Session, operation, set)
			if downloadErr != nil {
				return result, downloadErr
			}
			if ackErr := acknowledgeDownload(ctx, client, config, sessionID, operationID, revision.ContentHash); ackErr != nil {
				return result, ackErr
			}
			config.Streams[operation.StreamID] = StreamState{RevisionID: revision.ID, ContentHash: revision.ContentHash}
			result.Downloaded++
		case "noop":
			if operation.TargetRevisionID != "" {
				revisionID, idErr := protocolPathSegment(operation.TargetRevisionID)
				revisionHash, hashErr := protocolSHA256(operation.ExpectedHash)
				if idErr != nil || hashErr != nil {
					return result, errors.New("server returned an invalid noop revision")
				}
				config.Streams[operation.StreamID] = StreamState{RevisionID: revisionID, ContentHash: revisionHash}
			}
		case "conflict":
			result.Conflicts++
		default:
			return result, errors.New("server returned an unknown sync action")
		}
	}
	config.Pending = nil
	if err = UpdateConfig(configPath, config); err != nil {
		return result, err
	}
	if result.Conflicts > 0 {
		return result, ErrSyncConflict
	}
	result.Status = "complete"
	return result, nil
}
