package server

import (
	"context"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"varkiv/internal/platforms"
)

const (
	importDiagnosticMaxEntries = 4096
	importDiagnosticMaxDepth   = 4
)

var splitArchiveName = regexp.MustCompile(`(?i)(?:\.(?:7z|zip)\.\d{3}|\.part\d+\.rar|\.r\d{2})$`)

// importSourceDiagnostic intentionally contains no filename, path, size, hash,
// or tool hint. It explains an aggregate source state without disclosing the
// user's library layout or encouraging the server to execute bundled tools.
type importSourceDiagnostic struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

func hasMissingImportCandidate(candidates []importCandidate) bool {
	for _, candidate := range candidates {
		if candidate.MissingArtifacts > 0 {
			return true
		}
	}
	return false
}

func (s *Server) importSourceDiagnostics(ctx context.Context, in importRequest, candidates []importCandidate) []importSourceDiagnostic {
	format := strings.ToLower(strings.TrimSpace(in.Format))
	if (format != "pegasus" && format != "es-de" && format != "esde") || !hasMissingImportCandidate(candidates) {
		return []importSourceDiagnostic{}
	}

	source, err := s.libraryFile(in.Source)
	if err != nil {
		return []importSourceDiagnostic{}
	}
	root := filepath.Dir(source)
	if strings.TrimSpace(in.ContentRoot) != "" {
		root, err = s.libraryEntry(in.ContentRoot)
		if err != nil {
			return []importSourceDiagnostic{}
		}
	}

	registry, registryErr := s.store.PlatformRegistry(ctx)
	target, targetKnown := registry.Resolve(in.Platform)
	if registryErr != nil {
		targetKnown = false
	}
	wrapped, split, matchingWrapped, matchingSplit, visited, limited := 0, 0, 0, 0, 0, false
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		visited++
		if visited > importDiagnosticMaxEntries {
			limited = true
			return fs.SkipAll
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		depth := strings.Count(filepath.Clean(relative), string(filepath.Separator)) + 1
		if entry.IsDir() {
			if entry.Type()&fs.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") || depth >= importDiagnosticMaxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() || depth > importDiagnosticMaxDepth {
			return nil
		}
		name := strings.ToLower(entry.Name())
		switch {
		case strings.HasSuffix(name, ".tkzlm"):
			wrapped++
			if targetKnown && containerMatchesPlatform(registry, target.ID, entry.Name()) {
				matchingWrapped++
			}
		case splitArchiveName.MatchString(name):
			split++
			if targetKnown && containerMatchesPlatform(registry, target.ID, entry.Name()) {
				matchingSplit++
			}
		}
		return nil
	})

	diagnostics := make([]importSourceDiagnostic, 0, 3)
	if wrapped > 0 {
		diagnostics = append(diagnostics, importSourceDiagnostic{Code: "wrapped_archives_detected", Count: wrapped})
	}
	if split > 0 {
		diagnostics = append(diagnostics, importSourceDiagnostic{Code: "split_archives_detected", Count: split})
	}
	if matchingWrapped > 0 {
		diagnostics = append(diagnostics, importSourceDiagnostic{Code: "platform_wrapped_archives_detected", Count: matchingWrapped})
	}
	if matchingSplit > 0 {
		diagnostics = append(diagnostics, importSourceDiagnostic{Code: "platform_split_archive_parts_detected", Count: matchingSplit})
	}
	if limited {
		diagnostics = append(diagnostics, importSourceDiagnostic{Code: "container_inspection_limited", Count: importDiagnosticMaxEntries})
	}
	return diagnostics
}

func containerMatchesPlatform(registry platforms.Registry, targetID, name string) bool {
	collection := strings.TrimSpace(name)
	for {
		lower := strings.ToLower(collection)
		trimmed := false
		for _, suffix := range []string{".tkzlm", ".7z", ".zip", ".rar", ".tar", ".gz"} {
			if strings.HasSuffix(lower, suffix) {
				collection = strings.TrimSpace(collection[:len(collection)-len(suffix)])
				trimmed = true
				break
			}
		}
		if match := splitArchiveName.FindStringIndex(collection); match != nil && match[1] == len(collection) {
			collection = strings.TrimSpace(collection[:match[0]])
			trimmed = true
		}
		if !trimmed {
			break
		}
	}
	resolved, ok := registry.ResolveCollectionDirectory(collection)
	return ok && resolved.ID == targetID
}
