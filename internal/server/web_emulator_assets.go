package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const webEmulatorAssetVersion = "4.2.3"

type webEmulatorAssetIdentity struct {
	Path   string
	Size   int64
	SHA256 string
}

type webEmulatorAssetReport struct {
	Version        string
	AssetsVerified int
	BytesVerified  int64
}

type webEmulatorReadiness struct {
	Enabled              bool                            `json:"enabled"`
	Mode                 string                          `json:"mode"`
	SameOrigin           bool                            `json:"same_origin"`
	IntegrityVerified    bool                            `json:"integrity_verified"`
	EmulatorJSVersion    string                          `json:"emulatorjs_version,omitempty"`
	AssetsVerified       int                             `json:"assets_verified"`
	BytesVerified        int64                           `json:"bytes_verified"`
	SupportedPlatforms   []string                        `json:"supported_platforms"`
	SupportedExtensions  []string                        `json:"supported_extensions"`
	PlatformCapabilities []webEmulatorPlatformCapability `json:"platform_capabilities"`
}

type webEmulatorPlatformCapability struct {
	PlatformID      string   `json:"platform_id"`
	Core            string   `json:"core"`
	Extensions      []string `json:"extensions"`
	MinimumROMBytes int64    `json:"minimum_rom_bytes"`
}

// This fixed manifest is mirrored by testdata/web-emulation/fixtures.json and
// deliberately covers every file required by the supported browser cores.
var webEmulatorAssetManifest = []webEmulatorAssetIdentity{
	{Path: "loader.js", Size: 7594, SHA256: "69e0903bf1e2f62ced78895e7e511fa26e11316f7eb734925c35e919ba1287b2"},
	{Path: "emulator.min.js", Size: 426343, SHA256: "6aec3fd7bb2721255801b0a6af02e47e78b05e28a1822b1f213aacbd348abaee"},
	{Path: "emulator.min.css", Size: 25630, SHA256: "16406c60b2dc3b04ae9b115e308613e6f567a0cc7068e21d9d0c1e5030fb395e"},
	{Path: "compression/extract7z.js", Size: 280155, SHA256: "4ac9933b995a516cb6b3ca4027db860278372a20c21c405d93ceb1998498853a"},
	{Path: "compression/extractzip.js", Size: 196199, SHA256: "3cc825428724213acd43d30552a78008e0869b2326de17dee240dbff9ee2f629"},
	{Path: "localization/zh-CN.json", Size: 10521, SHA256: "a20b7d3c3395d3cce4d697b45555e4228c076f07caba1c2bcf76bb66bedb79d2"},
	{Path: "cores/fceumm-legacy-wasm.data", Size: 1053006, SHA256: "f1054b094e7149fd6278485bc1b2e51ff75c5259048ddb1134171e53d651f239"},
	{Path: "cores/reports/fceumm.json", Size: 120, SHA256: "13dbbfba0ea1bea087c97c38f47dd49c3fb8f16c3f5ed5678dd75d352b737132"},
	{Path: "cores/gambatte-wasm.data", Size: 967156, SHA256: "ad67c7bf57f8f8b62606048e6ea498afac5b5abc76ad8de5f9dfc2a6719374bb"},
	{Path: "cores/gambatte-legacy-wasm.data", Size: 967022, SHA256: "940a7381d51223c0be0c0f98d1a988d42cc98d03c27b392cbd29cb9f357b5f62"},
	{Path: "cores/reports/gambatte.json", Size: 122, SHA256: "a240a47bd6b2a38a6c46ee63c80bdcd24befbd50474453df4898d49282bd5f57"},
	{Path: "cores/mgba-wasm.data", Size: 1055616, SHA256: "01fcaf6d4296ef1db6676e0c69400c4474e24572d0b2b99cc097e4ae885e02d7"},
	{Path: "cores/mgba-legacy-wasm.data", Size: 1054993, SHA256: "e300d2f368c82bf61d77c972c36e367cf16408ec3d50beede080337519a3d696"},
	{Path: "cores/reports/mgba.json", Size: 118, SHA256: "08219f6c855a9d996f04ed21169bb0c5ac64d469a8a536468b9876205b5c268d"},
	{Path: "cores/mupen64plus_next-wasm.data", Size: 1451795, SHA256: "2da1cbce9fda395e3ae83ca5787353baa159142d45ef3ea90f108b92524f76cc"},
	{Path: "cores/mupen64plus_next-legacy-wasm.data", Size: 1451870, SHA256: "425072f4bf94eec02633cbe9b84f47d6803dc0ec1b3c3b8de3ebbd2eb5617c3f"},
	{Path: "cores/reports/mupen64plus_next.json", Size: 153, SHA256: "270105553cec57fd1058c50e0541b8b89dbd30b2323fd70682036f6919b805cc"},
	{Path: "cores/mednafen_ngp-wasm.data", Size: 871904, SHA256: "cdfe377bd380e418507dccda50d8664eecb06ebe1d2e5fbf5f397be859d1c83d"},
	{Path: "cores/mednafen_ngp-legacy-wasm.data", Size: 872066, SHA256: "694089f04886d400b3672077ec9f32f408df26a8b204af6b4f3723197ea749c8"},
	{Path: "cores/reports/mednafen_ngp.json", Size: 126, SHA256: "c47ef44f074a6b67f9f328e8c39bb846fce0ceb0ead1053f59c44360fd722162"},
	{Path: "cores/snes9x-wasm.data", Size: 1093765, SHA256: "eaa0bcfce67673809886e50387a80a616b719502175db64c090d04c9d75958ee"},
	{Path: "cores/snes9x-legacy-wasm.data", Size: 1092437, SHA256: "7d427a575cefad98ff400493fa1d7e892da63fe7bab68979babd9cea0bfaaf3b"},
	{Path: "cores/reports/snes9x.json", Size: 120, SHA256: "dc7ac963eb7935a7ac78956235ac0b8912ec785c57026336825aa2ed8031b3ad"},
	{Path: "cores/genesis_plus_gx-wasm.data", Size: 1203661, SHA256: "190297a6f86757405090f1a2266f67dfe1a570a528c583434ed3641a5664f768"},
	{Path: "cores/genesis_plus_gx-legacy-wasm.data", Size: 1204803, SHA256: "2a1edeb68d7ec882149ed3cd5d3aa95a6ac231ebabaf82cdb56175a8b62967cc"},
	{Path: "cores/reports/genesis_plus_gx.json", Size: 129, SHA256: "5936a8ce8d7f010d5bfdd8c3bba2b2414f103b3a703121e56f2724b24dbe7ff3"},
	{Path: "cores/smsplus-wasm.data", Size: 855876, SHA256: "0f197c5e0000f17b2d072122a72b3f8fc1693514c4014fcd9694eec78584aa08"},
	{Path: "cores/smsplus-legacy-wasm.data", Size: 855316, SHA256: "9fcc16b3e4ad662c395542bc96a0142cded1d17cbd8e43f0d5b3205cf4f7d340"},
	{Path: "cores/reports/smsplus.json", Size: 121, SHA256: "1a3fc4e4ead90eb7f394b2498e63833ee5c35b7c8fa8e45445057b8bd9b81590"},
	{Path: "cores/stella2014-wasm.data", Size: 1051659, SHA256: "6c96c6b1746f3f05ca599066abe131a36c77ca61fc20a9e2a7560540457c487d"},
	{Path: "cores/stella2014-legacy-wasm.data", Size: 1051741, SHA256: "6dc81bb0c3c5833af69aa750491a6ecd547ec9b91c21bd55ed9a77d5b0a8dd08"},
	{Path: "cores/reports/stella2014.json", Size: 124, SHA256: "c9920b95db48f678294daa72a289c19846e74ba69ca6d6094a76fb9f560fb39a"},
}

func validateWebEmulatorDirectory(value string, manifest []webEmulatorAssetIdentity) (string, webEmulatorAssetReport, error) {
	report := webEmulatorAssetReport{Version: webEmulatorAssetVersion}
	abs, err := filepath.Abs(strings.TrimSpace(value))
	if err != nil {
		return "", report, errors.New("web emulator directory is invalid")
	}
	rootInfo, err := os.Lstat(abs)
	if err != nil {
		return "", report, errors.New("web emulator directory is unavailable")
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", report, errors.New("web emulator directory must not be a symbolic link")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", report, errors.New("web emulator directory is unavailable")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", report, errors.New("web emulator directory must be an existing directory")
	}
	root, err := os.OpenRoot(resolved)
	if err != nil {
		return "", report, errors.New("web emulator directory is unavailable")
	}
	defer root.Close()
	for _, asset := range manifest {
		if err = verifyWebEmulatorAsset(root, asset); err != nil {
			return "", report, err
		}
		report.AssetsVerified++
		report.BytesVerified += asset.Size
	}
	return resolved, report, nil
}

func verifyWebEmulatorAsset(root *os.Root, asset webEmulatorAssetIdentity) error {
	_, _, err := readVerifiedWebEmulatorAsset(root, asset)
	return err
}

func readVerifiedWebEmulatorAsset(root *os.Root, asset webEmulatorAssetIdentity) ([]byte, time.Time, error) {
	if asset.Path == "" || !fs.ValidPath(asset.Path) || strings.Contains(asset.Path, "\\") || path.Clean(asset.Path) != asset.Path {
		return nil, time.Time{}, errors.New("web emulator asset manifest contains an invalid path")
	}
	parts := strings.Split(asset.Path, "/")
	for i := range parts {
		component := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(component)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("web emulator asset %q is missing", asset.Path)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, time.Time{}, fmt.Errorf("web emulator asset %q contains a symbolic link", asset.Path)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return nil, time.Time{}, fmt.Errorf("web emulator asset %q has an invalid directory component", asset.Path)
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return nil, time.Time{}, fmt.Errorf("web emulator asset %q must be a regular file", asset.Path)
		}
	}
	file, err := root.Open(asset.Path)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("web emulator asset %q is unavailable", asset.Path)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, time.Time{}, fmt.Errorf("web emulator asset %q must be a regular file", asset.Path)
	}
	if info.Size() != asset.Size {
		return nil, time.Time{}, fmt.Errorf("web emulator asset %q size does not match the pinned manifest", asset.Path)
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("web emulator asset %q could not be verified", asset.Path)
	}
	digest := sha256.Sum256(content)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), asset.SHA256) {
		return nil, time.Time{}, fmt.Errorf("web emulator asset %q hash does not match the pinned manifest", asset.Path)
	}
	return content, info.ModTime(), nil
}

func webEmulatorManifestAsset(manifest []webEmulatorAssetIdentity, name string) (webEmulatorAssetIdentity, bool) {
	for _, asset := range manifest {
		if asset.Path == name {
			return asset, true
		}
	}
	return webEmulatorAssetIdentity{}, false
}

func (s *Server) webEmulatorReadiness(w http.ResponseWriter, _ *http.Request) {
	platforms := make([]string, 0, len(webEmulationCores))
	for platform := range webEmulationCores {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	extensionSet := make(map[string]bool)
	capabilities := make([]webEmulatorPlatformCapability, 0, len(platforms))
	for _, platform := range platforms {
		platformExtensions := make([]string, 0, len(webEmulationPlatformExtensions[platform]))
		for extension := range webEmulationPlatformExtensions[platform] {
			platformExtensions = append(platformExtensions, extension)
			extensionSet[extension] = true
		}
		sort.Strings(platformExtensions)
		capabilities = append(capabilities, webEmulatorPlatformCapability{PlatformID: platform, Core: webEmulationCores[platform], Extensions: platformExtensions, MinimumROMBytes: webEmulationPlatformMinimumBytes[platform]})
	}
	extensions := make([]string, 0, len(extensionSet))
	for extension := range extensionSet {
		extensions = append(extensions, extension)
	}
	sort.Strings(extensions)
	result := webEmulatorReadiness{
		Enabled:              s.webEmulatorAssets != "",
		Mode:                 "disabled",
		AssetsVerified:       s.webEmulatorReport.AssetsVerified,
		BytesVerified:        s.webEmulatorReport.BytesVerified,
		SupportedPlatforms:   platforms,
		SupportedExtensions:  extensions,
		PlatformCapabilities: capabilities,
	}
	if s.webEmulatorDir != "" {
		result.Mode = "self-hosted-verified"
		result.SameOrigin = true
		result.IntegrityVerified = true
		result.EmulatorJSVersion = s.webEmulatorReport.Version
	} else if s.webEmulatorAssets != "" {
		result.Mode = "external-unverified"
		result.SameOrigin = strings.HasPrefix(s.webEmulatorAssets, "/")
	}
	writeJSON(w, http.StatusOK, result)
}
