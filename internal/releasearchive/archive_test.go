package releasearchive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestWriteIsDeterministicAcrossSourceMetadata(t *testing.T) {
	source := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(filepath.Join(source, "licenses"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "varkiv"), []byte("binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "licenses", "NOTICE"), []byte("notice\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, format := range []Format{FormatZip, FormatTarGz} {
		t.Run(string(format), func(t *testing.T) {
			outputRoot := t.TempDir()
			first := filepath.Join(outputRoot, "first."+string(format))
			second := filepath.Join(outputRoot, "second."+string(format))
			if err := Write(source, "varkiv-test", first, format); err != nil {
				t.Fatal(err)
			}
			future := time.Date(2037, 7, 9, 13, 12, 11, 0, time.UTC)
			for _, target := range []string{source, filepath.Join(source, "varkiv"), filepath.Join(source, "licenses"), filepath.Join(source, "licenses", "NOTICE")} {
				if err := os.Chtimes(target, future, future); err != nil {
					t.Fatal(err)
				}
			}
			if err := Write(source, "varkiv-test", second, format); err != nil {
				t.Fatal(err)
			}
			firstBytes, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			secondBytes, err := os.ReadFile(second)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(firstBytes, secondBytes) {
				t.Fatal("archive bytes changed after source metadata drift")
			}
			if mode := mustStat(t, first).Mode().Perm(); mode != 0o644 {
				t.Fatalf("output mode = %o, want 644", mode)
			}
			if names := archiveNames(t, first, format); !reflect.DeepEqual(names, []string{"varkiv-test/", "varkiv-test/licenses/", "varkiv-test/licenses/NOTICE", "varkiv-test/varkiv"}) {
				t.Fatalf("archive names = %#v", names)
			}
			assertArchiveMetadata(t, first, format)
		})
	}
}

func TestWriteRefusesOverwriteAndUnsafeInputs(t *testing.T) {
	source := filepath.Join(t.TempDir(), "package")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "release.zip")
	if err := os.WriteFile(output, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(source, "varkiv-test", output, FormatZip); err == nil || err.Error() != "release archive output already exists" {
		t.Fatalf("overwrite error = %v", err)
	}
	if got, err := os.ReadFile(output); err != nil || string(got) != "keep" {
		t.Fatalf("existing output changed: %q, %v", got, err)
	}
	if err := Write(source, "../escape", filepath.Join(t.TempDir(), "escape.zip"), FormatZip); err == nil {
		t.Fatal("unsafe prefix was accepted")
	}
	if err := Write(source, "varkiv-test", filepath.Join(source, "inside.zip"), FormatZip); err == nil {
		t.Fatal("output inside source was accepted")
	}
}

func TestWriteRejectsSymbolicLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows CI users cannot reliably create symbolic links")
	}
	source := filepath.Join(t.TempDir(), "package")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "release.zip")
	if err := Write(source, "varkiv-test", output, FormatZip); err == nil {
		t.Fatal("symbolic link was accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed archive left output behind: %v", err)
	}
}

func archiveNames(t *testing.T, archivePath string, format Format) []string {
	t.Helper()
	var names []string
	switch format {
	case FormatZip:
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		for _, file := range reader.File {
			names = append(names, file.Name)
		}
	case FormatTarGz:
		file, err := os.Open(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		defer gzipReader.Close()
		tarReader := tar.NewReader(gzipReader)
		for {
			header, err := tarReader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, header.Name)
		}
	default:
		t.Fatalf("unsupported format %q", format)
	}
	return names
}

func assertArchiveMetadata(t *testing.T, archivePath string, format Format) {
	t.Helper()
	wantModes := map[string]os.FileMode{
		"varkiv-test/":                0o755,
		"varkiv-test/licenses/":       0o755,
		"varkiv-test/licenses/NOTICE": 0o644,
		"varkiv-test/varkiv":          0o755,
	}
	switch format {
	case FormatZip:
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		for _, file := range reader.File {
			if !file.Modified.Equal(zipEpoch) {
				t.Fatalf("ZIP time for %s = %v", file.Name, file.Modified)
			}
			if got := file.Mode().Perm(); got != wantModes[file.Name] {
				t.Fatalf("ZIP mode for %s = %o, want %o", file.Name, got, wantModes[file.Name])
			}
		}
	case FormatTarGz:
		compressed, err := os.ReadFile(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		if len(compressed) < 10 || !bytes.Equal(compressed[4:8], []byte{0, 0, 0, 0}) || compressed[9] != 255 {
			t.Fatal("gzip header metadata is not normalized")
		}
		gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			t.Fatal(err)
		}
		defer gzipReader.Close()
		tarReader := tar.NewReader(gzipReader)
		for {
			header, err := tarReader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if !header.ModTime.Equal(tarEpoch) || header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" {
				t.Fatalf("TAR ownership/time metadata for %s is not normalized", header.Name)
			}
			if got := os.FileMode(header.Mode).Perm(); got != wantModes[header.Name] {
				t.Fatalf("TAR mode for %s = %o, want %o", header.Name, got, wantModes[header.Name])
			}
		}
	default:
		t.Fatalf("unsupported format %q", format)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
