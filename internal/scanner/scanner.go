package scanner

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
	"varkiv/internal/platforms"
)

var supported = map[string]bool{
	".7z": true, ".3ds": true, ".a26": true, ".bin": true, ".chd": true, ".cia": true,
	".cue": true, ".cso": true, ".d64": true, ".dol": true, ".elf": true, ".fds": true,
	".gb": true, ".gba": true, ".gbc": true, ".gcm": true, ".gen": true, ".iso": true,
	".md": true, ".m3u": true, ".n64": true, ".nds": true, ".nes": true, ".ngc": true,
	".pbp": true, ".rvz": true, ".sfc": true, ".smc": true, ".wad": true, ".wbfs": true,
	".wua": true, ".wud": true, ".xci": true, ".zip": true, ".z64": true,
}

type Result struct{ Found, Imported, Skipped int }

type Candidate struct {
	Game      catalog.ImportedGame
	Duplicate bool
	Reason    string
}

func Discover(ctx context.Context, store *catalog.Store, libraryRoot, scanRoot, platform string) ([]Candidate, error) {
	registry, err := platforms.NewRegistry(platforms.All())
	if err != nil {
		return nil, err
	}
	return DiscoverWithRegistry(ctx, store, libraryRoot, scanRoot, platform, registry)
}

func DiscoverWithRegistry(ctx context.Context, store *catalog.Store, libraryRoot, scanRoot, platform string, registry platforms.Registry) ([]Candidate, error) {
	allowed := supported
	allowDirectories := false
	if preset, ok := registry.Resolve(platform); ok {
		platform = preset.ID
		allowed = make(map[string]bool)
		for _, extension := range preset.Extensions {
			if strings.HasPrefix(extension, ".") {
				allowed[strings.ToLower(extension)] = true
			} else if strings.EqualFold(extension, "directory") {
				allowDirectories = true
			}
		}
	}
	root, err := filepath.Abs(libraryRoot)
	if err != nil {
		return nil, err
	}
	target, err := filepath.Abs(scanRoot)
	if err != nil {
		return nil, err
	}
	if !within(root, target) {
		return nil, fmt.Errorf("scan root %q is outside library root %q", target, root)
	}
	paths := []string{}
	err = filepath.WalkDir(target, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("scan source contains a symbolic link")
		}
		if d.IsDir() {
			if path == target {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if allowDirectories && filepath.Dir(path) == target {
				paths = append(paths, path)
				return filepath.SkipDir
			}
			return nil
		}
		if !allowed[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	graph := map[string][]string{}
	referenced := map[string]bool{}
	for _, path := range paths {
		refs, refErr := fileReferences(path)
		if refErr != nil {
			return nil, refErr
		}
		for _, ref := range refs {
			if !within(root, ref) {
				return nil, fmt.Errorf("playlist reference %q is outside library root", ref)
			}
			graph[path] = append(graph[path], ref)
			referenced[ref] = true
		}
	}
	candidates := make([]Candidate, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if referenced[path] {
			continue
		}
		group := []string{path}
		collectReferences(path, graph, map[string]bool{path: true}, &group)
		artifacts := make([]catalog.NewArtifact, 0, len(group))
		candidate := Candidate{}
		for index, item := range group {
			rel, relErr := filepath.Rel(root, item)
			if relErr != nil {
				return nil, relErr
			}
			rel = filepath.ToSlash(rel)
			if existing, lookupErr := store.ArtifactByPath(ctx, rel); lookupErr == nil {
				candidate.Duplicate, candidate.Reason = true, "文件路径已被资料库收录："+existing.Path
			} else if !errors.Is(lookupErr, sql.ErrNoRows) {
				return nil, lookupErr
			}
			if existing, lookupErr := store.ArtifactBySourcePath(ctx, rel); lookupErr == nil {
				candidate.Duplicate, candidate.Reason = true, "来源文件已经导入："+existing.SourcePath
			} else if !errors.Is(lookupErr, sql.ErrNoRows) {
				return nil, lookupErr
			}
			artifact := catalog.NewArtifact{Path: rel, Role: "rom"}
			if index > 0 {
				artifact.Role, artifact.DiscIndex = "disc", index
			}
			info, statErr := os.Stat(item)
			if statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					artifact.Missing = true
				} else {
					return nil, statErr
				}
			} else {
				if info.IsDir() {
					artifact.SHA256, artifact.Size, err = filehash.Directory(item)
				} else {
					artifact.Size = info.Size()
					artifact.SHA256, err = fileHash(item)
				}
				if err != nil {
					return nil, err
				}
				if existing, lookupErr := store.ArtifactBySHA256(ctx, artifact.SHA256); lookupErr == nil {
					candidate.Duplicate, candidate.Reason = true, "相同内容已经收录："+existing.Path
				} else if !errors.Is(lookupErr, sql.ErrNoRows) {
					return nil, lookupErr
				}
			}
			artifacts = append(artifacts, artifact)
		}
		title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		candidate.Game = catalog.ImportedGame{Platform: platform, DefaultTitle: title, EditionTitle: title, EditionType: "original", Artifacts: artifacts}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func Scan(ctx context.Context, store *catalog.Store, libraryRoot, scanRoot, platform string) (Result, error) {
	candidates, err := Discover(ctx, store, libraryRoot, scanRoot, platform)
	return commitCandidates(ctx, store, candidates, err)
}

func ScanWithRegistry(ctx context.Context, store *catalog.Store, libraryRoot, scanRoot, platform string, registry platforms.Registry) (Result, error) {
	candidates, err := DiscoverWithRegistry(ctx, store, libraryRoot, scanRoot, platform, registry)
	return commitCandidates(ctx, store, candidates, err)
}

func commitCandidates(ctx context.Context, store *catalog.Store, candidates []Candidate, err error) (Result, error) {
	if err != nil {
		return Result{}, err
	}
	result := Result{Found: len(candidates)}
	for _, candidate := range candidates {
		if candidate.Duplicate {
			result.Skipped++
			continue
		}
		_, created, importErr := store.ImportGame(ctx, candidate.Game)
		if importErr != nil {
			return result, importErr
		}
		if created {
			result.Imported++
		} else {
			result.Skipped++
		}
	}
	return result, nil
}

func collectReferences(path string, graph map[string][]string, seen map[string]bool, output *[]string) {
	for _, ref := range graph[path] {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		*output = append(*output, ref)
		collectReferences(ref, graph, seen, output)
	}
}

func fileReferences(path string) ([]string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".m3u" && ext != ".cue" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	refs := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		value := line
		if ext == ".cue" {
			if !strings.HasPrefix(strings.ToUpper(line), "FILE ") {
				continue
			}
			value = strings.TrimSpace(line[5:])
			if strings.HasPrefix(value, "\"") {
				if end := strings.Index(value[1:], "\""); end >= 0 {
					value = value[1 : end+1]
				}
			} else if field := strings.Fields(value); len(field) > 0 {
				value = field[0]
			}
		}
		value = strings.TrimSpace(strings.Trim(value, "\""))
		if value == "" {
			continue
		}
		ref := filepath.FromSlash(value)
		if !filepath.IsAbs(ref) {
			ref = filepath.Join(filepath.Dir(path), ref)
		}
		abs, absErr := filepath.Abs(filepath.Clean(ref))
		if absErr != nil {
			return nil, absErr
		}
		refs = append(refs, abs)
	}
	return refs, scanner.Err()
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
