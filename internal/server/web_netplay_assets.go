package server

import (
	"bytes"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	webNetplayEmulatorVersion = "4.3.0-pre"
	webNetplayCoreVersion     = "sha256:ea7b9e5b899e13c463feac73470a13b9afa621083d38671f0c91928a9dcbea22"
)

// The experiment intentionally pins one core. Expanding platform coverage is
// a separate compatibility decision, not an accidental consequence of what a
// moving EmulatorJS preview happens to publish.
var webNetplayEmulatorAssetManifest = []webEmulatorAssetIdentity{
	{Path: "loader.js", Size: 8935, SHA256: "b9c06630025602e0826e32d40d76336bfcee1a96a4cbddfc2e1d7551096bc8a1"},
	{Path: "emulator.min.js", Size: 483434, SHA256: "b5f88bdfc57c11b76e20e7393c51e2257efde7637e48565be702e0d6590fb5ea"},
	{Path: "emulator.min.css", Size: 28854, SHA256: "6c7b0ae656c00b8f5c2aabd662fa19c5fbf30119634a63782ad7567f9fb95627"},
	{Path: "compression/extract7z.js", Size: 280155, SHA256: "4ac9933b995a516cb6b3ca4027db860278372a20c21c405d93ceb1998498853a"},
	{Path: "compression/extractzip.js", Size: 196199, SHA256: "3cc825428724213acd43d30552a78008e0869b2326de17dee240dbff9ee2f629"},
	{Path: "localization/zh.json", Size: 10545, SHA256: "eb7cecac255d0a539fc062448d97ae2002649338b3aff4351b5e60b1986107b9"},
	{Path: "localization/ja.json", Size: 9621, SHA256: "ec0c86621867a7af615c4d845e11420e54006a777358a3fc4d327357d4a9f0d3"},
	{Path: "cores/fceumm-wasm.data", Size: 896662, SHA256: "ea7b9e5b899e13c463feac73470a13b9afa621083d38671f0c91928a9dcbea22"},
	{Path: "cores/fceumm-legacy-wasm.data", Size: 895872, SHA256: "839e324bf96a9b3d46e6b2af385d7d4ab302abe8460a9731b94924f289041f15"},
	{Path: "cores/reports/fceumm.json", Size: 120, SHA256: "e082ff9bf4cf0b42cdf5d204d301c3fbe106e3dcae551b0aadb1e47236ce0cc7"},
}

func (s *Server) webNetplayEmulatorAsset(w http.ResponseWriter, r *http.Request) {
	relative := r.PathValue("path")
	asset, found := webEmulatorManifestAsset(webNetplayEmulatorAssetManifest, relative)
	if !found {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(s.webNetplayDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	content, modified, err := readVerifiedWebEmulatorAsset(root, asset)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(relative)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(relative), modified, bytes.NewReader(content))
}
