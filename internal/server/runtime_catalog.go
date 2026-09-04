package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"varkiv/internal/catalog"
)

const (
	pegasusAdapterID               = "builtin-frontend-pegasus"
	esdeAdapterID                  = "builtin-frontend-esde"
	snesRawSRMCompatibilityGroupID = "builtin-save-compat-snes9x-raw-srm-v1"
)

func boolPointer(value bool) *bool { return &value }

func (s *Server) ensureRuntimeCatalog(ctx context.Context) error {
	for _, item := range builtinSourceAdapters() {
		if current, err := s.store.GetSourceAdapter(ctx, item.ID); err == nil {
			if current.ContractVersion < item.ContractVersion {
				if _, err = s.store.ReconcileBuiltinSourceAdapter(ctx, item); err != nil {
					return fmt.Errorf("upgrade source adapter %s: %w", item.ID, err)
				}
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := s.store.CreateSourceAdapter(ctx, item); err != nil {
			return fmt.Errorf("seed source adapter %s: %w", item.ID, err)
		}
	}
	for _, item := range builtinFrontendAdapters() {
		if current, err := s.store.GetFrontendAdapter(ctx, item.ID); err == nil {
			if current.ContractVersion < item.ContractVersion {
				if _, err = s.store.ReconcileBuiltinFrontendAdapter(ctx, item); err != nil {
					return fmt.Errorf("upgrade frontend adapter %s: %w", item.ID, err)
				}
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := s.store.CreateFrontendAdapter(ctx, item); err != nil {
			return fmt.Errorf("seed frontend adapter %s: %w", item.ID, err)
		}
	}
	for _, item := range builtinDeviceProfiles() {
		if current, err := s.store.GetDeviceProfile(ctx, item.ID); err == nil {
			if current.ContractVersion < item.ContractVersion {
				if _, err = s.store.ReconcileBuiltinDeviceProfile(ctx, item); err != nil {
					return fmt.Errorf("upgrade device profile %s: %w", item.ID, err)
				}
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := s.store.CreateDeviceProfile(ctx, item); err != nil {
			return fmt.Errorf("seed device profile %s: %w", item.ID, err)
		}
	}
	for _, item := range builtinEmulatorDrivers() {
		if current, err := s.store.GetEmulatorDriver(ctx, item.ID); err == nil {
			if current.ContractVersion < item.ContractVersion {
				if _, err = s.store.ReconcileBuiltinEmulatorDriver(ctx, item); err != nil {
					return fmt.Errorf("upgrade emulator driver %s: %w", item.ID, err)
				}
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := s.store.CreateEmulatorDriver(ctx, item); err != nil {
			return fmt.Errorf("seed emulator driver %s: %w", item.ID, err)
		}
	}
	for _, item := range builtinRetroArchCores() {
		if current, err := s.store.GetRetroArchCore(ctx, item.ID); err == nil {
			if current.ContractVersion < item.ContractVersion {
				if _, err = s.store.ReconcileBuiltinRetroArchCore(ctx, item); err != nil {
					return fmt.Errorf("upgrade RetroArch core %s: %w", item.ID, err)
				}
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := s.store.CreateRetroArchCore(ctx, item); err != nil {
			return fmt.Errorf("seed RetroArch core %s: %w", item.ID, err)
		}
	}
	for _, item := range builtinCoreMappings() {
		if _, err := s.store.GetCoreMapping(ctx, item.ID); err == nil {
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := s.store.CreateCoreMapping(ctx, item); err != nil {
			return fmt.Errorf("seed core mapping %s: %w", item.ID, err)
		}
	}
	for _, item := range builtinSaveCompatibilityGroups() {
		if _, err := s.store.ReconcileBuiltinSaveCompatibilityGroup(ctx, item); err != nil {
			return fmt.Errorf("reconcile save compatibility group %s: %w", item.ID, err)
		}
		for _, member := range item.Members {
			if member.RuntimeKind != "server" {
				continue
			}
			if _, err := s.store.AttachSaveCompatibilityGroup(ctx, item.ID, member.DriverID); err != nil {
				return fmt.Errorf("attach save compatibility group %s: %w", item.ID, err)
			}
		}
	}
	if _, err := s.store.SuggestPendingRuntimeHints(ctx); err != nil {
		return fmt.Errorf("refresh pending runtime suggestions: %w", err)
	}
	return nil
}

func builtinSaveCompatibilityGroups() []catalog.NewSaveCompatibilityGroup {
	return []catalog.NewSaveCompatibilityGroup{{
		ID: snesRawSRMCompatibilityGroupID, Name: "Snes9x 1.63 raw libretro SRAM", Format: "raw-libretro-srm", ContractVersion: 1,
		Evidence: map[string]any{
			"scope": "isolated-roundtrip", "verified_at": "2026-08-29", "result": "passed",
			"web_driver_id": "builtin-driver-emulatorjs-snes9x", "native_driver_id": "builtin-driver-retroarch", "native_core_id": "builtin-core-snes9x",
			"note": "The two-stage 2 KiB SRAM handshake proves only these exact runtime contracts and Linux arm64 binaries. A device binding remains inactive until the Agent reports both matching SHA-256 identities; names and filenames are insufficient.",
		},
		Members: []catalog.SaveCompatibilityMember{
			{DriverID: "builtin-driver-emulatorjs-snes9x", RuntimeKind: "server", DriverContractVersion: 5},
			{DriverID: "builtin-driver-retroarch", CoreID: "builtin-core-snes9x", RuntimeKind: "device", DriverContractVersion: 10, CoreContractVersion: 3, OSFamily: "linux", Architecture: "arm64", DriverSHA256: "484621fe4675e3cf9a0d47ec9f63d611540dfe98db0d7799d9c8d14e5881b080", DriverSize: 14705288, CoreSHA256: "52a3ceadeb4798cc323094c614eff20456fad7cf2287a5add8a475c677c3939b", CoreSize: 2436288},
		},
		Builtin: true, Enabled: boolPointer(true),
	}}
}

func builtinSourceAdapters() []catalog.NewSourceAdapter {
	return []catalog.NewSourceAdapter{
		{ID: "builtin-source-direct-rom", Name: "Direct ROM scanner", Format: "direct-rom", Handler: "rom_directory", ContractVersion: 3, Capabilities: map[string]bool{"files": true, "directories": true, "directory_platforms": true, "multi_disc": true, "preview": true, "managed_copy": true}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-27", "note": "Signed preview and atomic import tests cover files, CUE/BIN, M3U groups, and one top-level directory per game on directory-declared platforms."}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-source-pegasus", Name: "Pegasus metadata", Format: "pegasus", Handler: "pegasus", ContractVersion: 3, Capabilities: map[string]bool{"metadata": true, "media": true, "multi_file_games": true, "runtime_hints": true, "separate_content_root": true, "preview": true}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-28", "note": "Metadata stored separately from ROM content is resolved through an explicit library-contained root covered by signed preview and atomic commit tests."}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-source-esde", Name: "ES-DE metadata", Format: "es-de", Handler: "esde", ContractVersion: 3, Capabilities: map[string]bool{"metadata": true, "media": true, "custom_systems": true, "runtime_hints": true, "separate_content_root": true, "preview": true}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-28", "note": "Gamelists can resolve ROMs from an explicit separate library-contained root while media remains relative to the metadata tree; missing ROMs are skipped."}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-source-varkiv", Name: "Neutral recovery manifest", Format: "varkiv", Handler: "varkiv", ContractVersion: 4, Capabilities: map[string]bool{"metadata": true, "series": true, "media": true, "runtime_hints": true, "stable_ids": true, "artifact_roles": true, "artifact_integrity": true, "neutral_manifest_v6": true, "portable_custom_platforms": true, "preview": true}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-27", "note": "Manifest v6 preserves v5 game and Artifact semantics and atomically restores referenced custom platform definitions; v4/v5 remain readable."}, Builtin: true, Enabled: boolPointer(true)},
	}
}

func builtinFrontendAdapters() []catalog.NewFrontendAdapter {
	return []catalog.NewFrontendAdapter{
		{ID: pegasusAdapterID, Name: "Pegasus", Format: "pegasus", Handler: "pegasus", ContractVersion: 5, Capabilities: map[string]bool{"import": true, "export": true, "multi_file_games": true, "custom_launch_commands": true, "neutral_manifest_v6": true, "series_native": false}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-27", "note": "Generated metadata.pegasus.txt packages through the audited Pegasus handler with a v6 recovery sidecar preserving Artifact semantics and referenced custom platforms; device launch remains driver-specific."}, Builtin: true, Enabled: boolPointer(true)},
		{ID: esdeAdapterID, Name: "ES-DE", Format: "es-de", Handler: "es-de", ContractVersion: 5, Capabilities: map[string]bool{"import": true, "export": true, "multi_file_games": false, "custom_systems": true, "neutral_manifest_v6": true, "series_native": false}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-27", "note": "Generated gamelist.xml packages through the audited ES-DE handler with a v6 recovery sidecar preserving Artifact semantics and referenced custom platforms; custom system launch remains driver-specific."}, Builtin: true, Enabled: boolPointer(true)},
	}
}

func builtinDeviceProfiles() []catalog.NewDeviceProfile {
	posixIllegal := "\\:*?\"<>|"
	return []catalog.NewDeviceProfile{
		{ID: "builtin-device-windows-handheld", Name: "Windows handheld", ContractVersion: 4, Target: "windows", OSFamily: "windows", Architecture: "x86_64", PathStyle: "windows", CaseSensitive: boolPointer(false), MaxPath: 260, IllegalCharacters: `<>:"/\|?*`, SupportsHardlink: true, DefaultFrontendID: pegasusAdapterID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves", "rom_dir": "roms"}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-30", "note": "Two current Windows amd64 PE Agents ran under pinned Wine 10 with explicit QEMU user-mode emulation: the generated seven-file package was verified, installed into a fresh prefix, and its exact parsed Task XML argv completed downloads, backup, and non-destructive conflict handling. Windows hardware, Task Scheduler, tray, sleep, and upgrade behavior remain unverified."}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-device-steamos-bazzite", Name: "SteamOS / Bazzite handheld", ContractVersion: 3, Target: "steamos-bazzite", OSFamily: "handheld-linux", Distribution: "steamos-bazzite", Architecture: "x86_64", PathStyle: "posix", CaseSensitive: boolPointer(true), MaxPath: 4096, IllegalCharacters: posixIllegal, SupportsHardlink: true, SupportsHooks: true, DefaultFrontendID: esdeAdapterID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves", "rom_dir": "roms"}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-26", "note": "Generated a private x86-64 user-home package with a fixed-argv systemd user unit; real hardware behavior remains unverified."}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-device-android-handheld", Name: "Android handheld", ContractVersion: 4, Target: "android", OSFamily: "android", Architecture: "aarch64", PathStyle: "android-uri", CaseSensitive: boolPointer(true), MaxPath: 1024, IllegalCharacters: posixIllegal, DefaultFrontendID: pegasusAdapterID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves", "rom_dir": "roms"}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "android-emulator", "verified_at": "2026-08-30", "note": "The current Agent-lite APK passed API 35 ARM64 AVD launch and sync gates. Official RetroArch, PPSSPP, Azahar vanilla, and Azahar Google Play packages each opened a Varkiv-granted public homebrew fixture; this is emulator software evidence, not Android handheld hardware, OEM background-policy, controller, or sleep/resume acceptance."}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-device-rocknix", Name: "ROCKNIX handheld", ContractVersion: 3, Target: "rocknix", OSFamily: "handheld-linux", Distribution: "rocknix", Architecture: "aarch64", PathStyle: "posix", CaseSensitive: boolPointer(true), MaxPath: 255, IllegalCharacters: posixIllegal, SupportsHooks: true, DefaultFrontendID: esdeAdapterID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves", "rom_dir": "roms"}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-26", "note": "Generated a private ARM64 Tools package; launch and sync remain unverified on hardware."}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-device-darkos", Name: "dArkOS handheld", ContractVersion: 3, Target: "darkos", OSFamily: "handheld-linux", Distribution: "darkos", Architecture: "aarch64", PathStyle: "posix", CaseSensitive: boolPointer(true), MaxPath: 255, IllegalCharacters: posixIllegal, SupportsHooks: true, DefaultFrontendID: esdeAdapterID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves", "rom_dir": "roms"}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-27", "note": "Generated a private ARM64 Tools package under roms/tools using the current dArkOS contract; launch and sync remain unverified on hardware.", "sources": []string{"https://github.com/christianhaitian/dArkOS/wiki", "https://github.com/christianhaitian/darkos-updates/releases"}}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-device-arkos", Name: "ArkOS handheld (legacy)", ContractVersion: 4, Target: "arkos", OSFamily: "handheld-linux", Distribution: "arkos", Architecture: "aarch64", PathStyle: "posix", CaseSensitive: boolPointer(true), MaxPath: 255, IllegalCharacters: posixIllegal, SupportsHooks: true, DefaultFrontendID: esdeAdapterID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves", "rom_dir": "roms"}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-27", "note": "Legacy compatibility only: upstream ArkOS was retired on 2025-12-30 and replaced by dArkOS. Existing Tools packages remain buildable without claiming current hardware support.", "sources": []string{"https://github.com/christianhaitian/arkos/wiki", "https://github.com/christianhaitian/dArkOS/wiki"}}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-device-knulli", Name: "KNULLI handheld", ContractVersion: 4, Target: "knulli", OSFamily: "handheld-linux", Distribution: "knulli", Architecture: "aarch64", PathStyle: "posix", CaseSensitive: boolPointer(true), MaxPath: 255, IllegalCharacters: posixIllegal, SupportsHooks: true, DefaultFrontendID: esdeAdapterID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves", "rom_dir": "roms"}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-29", "note": "Generated and installed the private ARM64 package in an isolated KNULLI-like user environment; the package service and gameStop hook completed downloads, backup, and non-destructive conflict handling. Hardware behavior remains unverified."}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-device-muos", Name: "muOS handheld", ContractVersion: 4, Target: "muos", OSFamily: "handheld-linux", Distribution: "muos", Architecture: "aarch64", PathStyle: "posix", CaseSensitive: boolPointer(true), MaxPath: 255, IllegalCharacters: posixIllegal, SupportsHooks: true, DefaultFrontendID: pegasusAdapterID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves", "rom_dir": "roms"}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-29", "note": "Generated and installed the private ARM64 application package in isolated muOS-like persistent storage; its sync/start/stop entries completed downloads, backup, idempotent polling control, and non-destructive conflict handling. Hardware behavior remains unverified.", "sources": []string{"https://muos.dev/tour/modules/muxapp", "https://muos.dev/tour/modules/muxtweakadv", "https://github.com/MustardOS/internal"}}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-device-onionos", Name: "OnionOS handheld", ContractVersion: 4, Target: "onionos", OSFamily: "handheld-linux", Distribution: "onionos", Architecture: "arm", PathStyle: "posix", CaseSensitive: boolPointer(false), MaxPath: 255, IllegalCharacters: posixIllegal, SupportsHooks: true, DefaultFrontendID: pegasusAdapterID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves", "rom_dir": "roms"}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-29", "note": "Generated and installed the private ARMv7 App package in an isolated OnionOS-like SD-card environment; its sync/start/stop entries ran under an actual armv7l user-mode process and completed downloads, backup, idempotent polling control, and non-destructive conflict handling. Hardware behavior remains unverified.", "sources": []string{"https://onionui.github.io/docs/included-apps", "https://github.com/OnionUI/Onion/tree/main/static/packages/Apps"}}, Builtin: true, Enabled: boolPointer(true)},
		{ID: "builtin-device-portable", Name: "Portable folder", ContractVersion: 3, Target: "portable", OSFamily: "portable", PathStyle: "posix", CaseSensitive: boolPointer(true), MaxPath: 255, IllegalCharacters: posixIllegal, DefaultFrontendID: esdeAdapterID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves", "rom_dir": "roms"}, SupportLevel: "package-tested", Evidence: map[string]any{"scope": "fixture", "verified_at": "2026-08-26", "note": "Generated portable relative-path packages and reimported their neutral manifests."}, Builtin: true, Enabled: boolPointer(true)},
	}
}

func builtinEmulatorDrivers() []catalog.NewEmulatorDriver {
	allTargets := []string{"windows", "steamos-bazzite", "android", "rocknix", "darkos", "arkos", "knulli", "muos", "onionos"}
	nonAndroidTargets := []string{"windows", "steamos-bazzite", "rocknix", "darkos", "arkos", "knulli", "muos", "onionos"}
	allClassic := []string{"2600", "5200", "7800", "3do", "amiga", "amigacd32", "amstradcpc", "apple2", "arcade", "atari8bit", "atarist", "atomiswave", "c64", "cdi", "colecovision", "dos", "dreamcast", "famicomdisk", "gameandwatch", "gamegear", "gb", "gba", "gbc", "intellivision", "jaguar", "lynx", "mastersystem", "megadrive", "msx", "msx2", "n64", "n64dd", "naomi", "nds", "neogeo", "neogeocd", "nes", "ngpc", "pc88", "pc98", "pcengine", "pcenginecd", "pcfx", "pico8", "pokemini", "psp", "psx", "saturn", "scummvm", "segacd", "sega32x", "sg1000", "snes", "supergrafx", "vectrex", "virtualboy", "wonderswan", "wonderswancolor", "x68000", "zxspectrum"}
	standalone := func(id, name string, platforms, targets []string, executables map[string][]string, arguments []string, save catalog.DriverSaveSpec, configPaths map[string]string, sources []string, note string) catalog.NewEmulatorDriver {
		save.Refresh, save.Portability = "process-exit", "same-driver"
		return catalog.NewEmulatorDriver{ID: id, Name: name, Family: id[len("builtin-driver-"):], ContractVersion: 4, Platforms: platforms, Targets: targets, Launch: catalog.DriverLaunchSpec{Executables: executables, Arguments: arguments}, Save: save, ConfigPaths: configPaths, SupportLevel: "catalogued", Evidence: map[string]any{"note": note, "sources": sources, "driver_root": "Paths are relative to an emulator user directory explicitly authorized with Agent --driver-root; the server never searches host profiles."}, Builtin: true, Enabled: boolPointer(true)}
	}
	ppsspp := standalone("builtin-driver-ppsspp", "PPSSPP", []string{"psp"}, []string{"windows", "steamos-bazzite", "android", "rocknix"}, map[string][]string{"windows": {"PPSSPPWindows64.exe", "PPSSPPWindows.exe"}, "steamos-bazzite": {"PPSSPPSDL", "ppsspp"}, "rocknix": {"PPSSPPSDL", "ppsspp"}}, []string{"{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "directory", Patterns: []string{"{{driver.user_dir}}/PSP/SAVEDATA/{{edition.product_code}}"}}, map[string]string{"settings": "PSP/SYSTEM", "savedata": "PSP/SAVEDATA"}, []string{"https://github.com/hrydgard/ppsspp/wiki/Help%3A-My-saves-won%27t-load", "https://github.com/hrydgard/ppsspp/issues/17416"}, "PSP savedata is a multi-file directory. Product Code is required; games that append a slot suffix need the binding path reviewed and adjusted. Android uses the explicit official package/activity VIEW contract and still requires the emulator to retain access to the selected ROM tree.")
	ppsspp.Launch.AndroidIntent = &catalog.AndroidIntentSpec{Action: "android.intent.action.VIEW", Package: "org.ppsspp.ppsspp", Activity: ".PpssppActivity", Data: "{{rom.uri}}", MIMEType: "application/octet-stream", Categories: []string{"android.intent.category.DEFAULT"}, Flags: []string{"grant-read-uri", "new-task", "clear-top"}}
	drivers := []catalog.NewEmulatorDriver{{ID: "builtin-driver-retroarch", Name: "RetroArch", Family: "retroarch", ContractVersion: 10, Platforms: allClassic, Targets: allTargets, Launch: catalog.DriverLaunchSpec{RequiresCore: true, Executables: map[string][]string{"windows": {"retroarch.exe"}, "steamos-bazzite": {"retroarch"}, "rocknix": {"retroarch"}, "darkos": {"retroarch"}, "arkos": {"retroarch"}, "knulli": {"retroarch"}, "muos": {"retroarch"}, "onionos": {"retroarch"}}, AndroidIntent: &catalog.AndroidIntentSpec{Action: "android.intent.action.MAIN", Package: "com.retroarch.aarch64", Activity: "com.retroarch.browser.retroactivity.RetroActivityFuture", StringExtras: map[string]string{"ROM": "{{rom.uri}}", "LIBRETRO": "{{android.package_data}}/cores/{{core.library}}_android.so"}, BooleanExtras: map[string]bool{"QUITFOCUS": true}, Flags: []string{"grant-read-uri", "new-task"}}, Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"}}, Save: catalog.DriverSaveSpec{Scope: "game", Layout: "single-file", Patterns: []string{"{{rom.stem}}.srm"}, PatternsByPlatform: map[string][]string{"5200": {}, "7800": {}, "dos": {}, "gameandwatch": {}, "lynx": {}, "ngpc": {"{{rom.stem}}.flash"}, "scummvm": {}}, Refresh: "process-exit", Portability: "core-dependent"}, SupportLevel: "catalogued", Evidence: map[string]any{"note": "RetroArch derives the default .srm name from the loaded content basename. Device Agent resolves that basename locally by the selected launch artifact SHA-256; compressed archives require a reviewed custom binding because RetroArch may use the inner entry name. Platforms whose selected core has no native save interface, or whose saves are directory-managed, explicitly override the generic pattern with an empty set; Beetle NeoPop uses .flash. Desktop/core candidates use explicit device roots. Android remains catalogued until verified on-device. Cross-driver save sharing is never inferred from a core name; the only current native/Web proof is the exact Snes9x combination recorded below.", "verified_save_combinations": []map[string]any{{"platform": "snes", "format": "raw-libretro-srm", "retroarch_version": "1.22.2", "retroarch_commit": "69a4f0ea1e8aaf442ae4858f2e7f2b31a1776576", "core_version": "1.63", "core_commit": "6ca2343e5f3b0acbea49ca958251e3a0af58a81d", "web_driver_id": "builtin-driver-emulatorjs-snes9x", "status": "isolated-roundtrip-passed"}}, "sources": []string{"https://github.com/libretro/RetroArch/blob/master/retroarch.c", "https://github.com/libretro/RetroArch/blob/master/runloop.c", "https://github.com/libretro/RetroArch/blob/master/retroarch.cfg", "https://docs.libretro.com/library/gw/", "https://docs.libretro.com/library/beetle_neopop/", "https://docs.libretro.com/library/dosbox_pure/", "https://docs.libretro.com/library/scummvm/"}}, Builtin: true, Enabled: boolPointer(true)}}
	webDrivers := []struct {
		core            string
		name            string
		platforms       []string
		contractVersion int
		supportLevel    string
		evidence        map[string]any
	}{
		{"fceumm", "FCEUmm", []string{"nes"}, 2, "package-tested", map[string]any{"scope": "real-browser", "verified_at": "2026-08-28", "result": "passed", "scenarios": []string{"signed direct-ROM import", "same-origin core start", "SHA-256 ROM streaming"}, "note": "Pinned EmulatorJS 4.2.3 FCEUmm rendered the pinned MIT nes-starter-kit ROM in Chromium. This does not claim native RetroArch save compatibility."}},
		{"snes9x", "Snes9x", []string{"snes"}, 5, "package-tested", map[string]any{"scope": "cross-runtime", "verified_at": "2026-08-29", "result": "passed", "scenarios": []string{"pinned release archive verification", "deterministic two-stage SRAM handshake", "signed direct-ROM import", "same-origin core start", "SPC-700 instruction suite terminal success", "terminal canvas SHA-256 gate", "2 KiB Web SRAM upload", "fresh-session byte-exact Web restore", "fixed RetroArch and same-commit native core build", "network-disabled non-root native run", "Web to native sentinel advance", "native to Web byte-exact restore and deduplication"}, "observed_rom_result": "gilyon spctest reached Success at terminal test 0557 under EmulatorJS 4.2.3 Snes9x", "observed_save_result": "2 KiB SRAM Web 0x5A -> RetroArch 0xA5 -> Web byte-exact roundtrip passed", "web_core_identity": "Snes9x 1.63 6ca2343e5f3b0acbea49ca958251e3a0af58a81d", "native_compatibility": map[string]any{"status": "verified-exact", "format": "raw-libretro-srm", "retroarch_version": "1.22.2", "retroarch_commit": "69a4f0ea1e8aaf442ae4858f2e7f2b31a1776576", "snes9x_version": "1.63", "snes9x_commit": "6ca2343e5f3b0acbea49ca958251e3a0af58a81d", "web_save_sha256": "48878c969caa13651d00cf0cab230da32e5d1fdd0bdf6217489af87a8f40a3d7", "native_save_sha256": "17f7c19ea1ad7f71dc8ddcb6b1a5c5af489448febcfc0a57ef43d88f81c6e2d8"}, "note": "The fixed MIT SPC-700 ROM still reaches its exact terminal screen. The hash-locked transform now performs a two-stage handshake: Web writes 0x5A, the exact native core can write 0xA5 only after loading that Web save, and a new Web run restores and re-emits the native bytes. This does not make arbitrary Snes9x builds compatible or automatically merge existing driver-specific SaveStreams; device-side promotion still requires exact core identity evidence."}},
		{"gambatte", "Gambatte", []string{"gb", "gbc"}, 2, "package-tested", map[string]any{"scope": "real-browser", "verified_at": "2026-08-28", "result": "passed", "scenarios": []string{"signed direct-ROM import", "DMG pass-pattern render", "CGB-enhanced title render", "32 KiB battery save upload", "fresh-session save restore"}, "note": "Pinned EmulatorJS 4.2.3 Gambatte ran pinned MIT DMG and authorial CGB-enhanced ROMs. CINZA produced a 32 KiB immutable battery revision and the exact revision restored into a visibly healthy fresh Chromium session. The stream remains isolated from native Gambatte and RetroArch cores."}},
		{"mgba", "mGBA", []string{"gba"}, 2, "package-tested", map[string]any{"scope": "real-browser", "verified_at": "2026-08-28", "result": "passed", "scenarios": []string{"signed direct-ROM import", "same-origin core start", "32 KiB SRAM upload", "fresh-session save restore"}, "note": "Pinned EmulatorJS 4.2.3 mGBA ran pinned MIT gba-tests ROMs and restored the exact immutable SaveRevision in a fresh Chromium session. The stream remains isolated from native mGBA and RetroArch cores."}},
		{"mupen64plus-next", "Mupen64Plus-Next", []string{"n64"}, 2, "package-tested", map[string]any{"scope": "real-browser", "verified_at": "2026-08-30", "result": "passed", "scenarios": []string{"pinned Unlicense ROM verification", "signed direct-ROM import", "same-origin core start", "save-type table visual probe"}, "observed_rom_result": "public-domain SaveTest-N64 rendered its cartridge capability table under EmulatorJS 4.2.3 Mupen64Plus-Next", "note": "Pinned EmulatorJS 4.2.3 Mupen64Plus-Next ran the fixed public-domain LibDragon SaveTest-N64 ROM in Chromium. The launch proof does not claim browser save emission or compatibility with native Mupen64Plus cores."}},
		{"mednafen-ngp", "Beetle NeoPop", []string{"ngpc"}, 1, "catalogued", map[string]any{"scope": "verified-assets-and-contract", "verified_at": "2026-08-31", "result": "catalogued", "scenarios": []string{"pinned EmulatorJS 4.2.3 core assets", "single-ROM ZIP boundary", "platform-specific extensions"}, "note": "EmulatorJS documents mednafen_ngp as its Neo Geo Pocket core, and the pinned Web/legacy core assets are hash-locked. A reproducible public-ROM browser fixture is still required before package-tested status; native RetroArch save compatibility is not inferred.", "sources": []string{"https://emulatorjs.org/docs4devs/cores/", "https://github.com/libretro/libretro-core-info/blob/master/mednafen_ngp_libretro.info"}}},
		{"genesis-plus-gx", "Genesis Plus GX", []string{"megadrive", "gamegear"}, 4, "package-tested", map[string]any{"scope": "real-browser", "verified_at": "2026-08-28", "result": "passed", "scenarios": []string{"pinned MIT source and ROM verification", "signed direct-ROM import", "same-origin Mega Drive and Game Gear core start", "live VDP smiley-sprite render", "Game Gear SMSGGDJ post-splash keyboard interaction", "Game Gear in-ROM SAVE and byte-exact fresh-session restore"}, "note": "Pinned EmulatorJS 4.2.3 Genesis Plus GX ran pinned MIT Mega Drive and Game Gear author ROMs in Chromium. Game Gear reached the SMSGGDJ editor, accepted mapped keyboard input, performed the ROM's SAVE gesture, and restored the exact 16,234-byte browser upload in a fresh session. This does not prove Mega Drive saves or compatibility with native RetroArch cores."}},
		{"smsplus", "SMS Plus GX", []string{"mastersystem"}, 3, "package-tested", map[string]any{"scope": "real-browser", "verified_at": "2026-08-28", "result": "passed", "scenarios": []string{"pinned MIT release ROM verification", "signed direct-ROM import", "same-origin core start", "Master System SMSGGDJ post-splash keyboard interaction", "Master System in-ROM SAVE and byte-exact fresh-session restore"}, "note": "Pinned EmulatorJS 4.2.3 SMS Plus GX reached the SMSGGDJ editor, accepted mapped keyboard input, performed the ROM's SAVE gesture, and restored the exact 32,768-byte browser upload in a fresh session. This does not prove compatibility with native RetroArch cores."}},
		{"stella2014", "Stella 2014", []string{"2600"}, 2, "package-tested", map[string]any{"scope": "real-browser", "verified_at": "2026-08-28", "result": "passed", "scenarios": []string{"deterministic Apache-2.0 4 KiB ROM generation", "signed direct-ROM import", "same-origin core start", "fixed TIA background render"}, "note": "Pinned EmulatorJS 4.2.3 Stella 2014 ran the deterministic Varkiv Atari 2600 frame-loop fixture in Chromium with a center-region visual probe. Controller behavior, save behavior, and native-core compatibility remain unverified."}},
	}
	for _, item := range webDrivers {
		evidence := item.evidence
		if evidence == nil {
			evidence = map[string]any{"note": "Pinned EmulatorJS 4.2.3 browser core. Save streams remain isolated by exact core identity and are not assumed compatible with native RetroArch cores."}
		}
		drivers = append(drivers, catalog.NewEmulatorDriver{
			ID: "builtin-driver-emulatorjs-" + item.core, Name: "EmulatorJS 4.2.3 · " + item.name,
			Family: "emulatorjs", ContractVersion: item.contractVersion, Platforms: item.platforms, Targets: []string{"web"},
			Launch:       catalog.DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}},
			Save:         catalog.DriverSaveSpec{Scope: "game", Layout: "single-file", Patterns: []string{"battery.sav"}, Refresh: "interval", Portability: "core-dependent"},
			SupportLevel: item.supportLevel, Evidence: evidence,
			Builtin: true, Enabled: boolPointer(true),
		})
	}
	drivers = append(drivers,
		standalone("builtin-driver-eden", "Eden", []string{"switch"}, []string{"windows", "steamos-bazzite"}, map[string][]string{"windows": {"eden.exe", "eden-cli.exe"}, "steamos-bazzite": {"eden", "eden-cli"}}, []string{"-g", "{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "directory", Patterns: []string{"{{driver.user_dir}}/{{edition.title_id_high}}{{edition.title_id_low}}"}}, nil, []string{"https://github.com/eden-emulator/mirror/blob/master/docs/user/CommandLine.md", "https://github.com/eden-emulator/mirror/blob/master/docs/user/SyncthingGuide.md", "https://github.com/eden-emulator/mirror/blob/master/docs/user/ImportingSaves.md", "https://git.eden-emu.dev/eden-emu/eden/releases"}, "Launch/catalog contract only: the user supplies Eden, legally obtained keys and firmware. Varkiv never discovers or downloads them. Set --driver-root to the exact selected Eden save-profile directory whose immediate children are 16-hex game Title IDs; do not select the NAND root or the whole user-data tree. A 16-hex Title ID is mandatory. Save conflicts and deletions remain non-destructive. Android is deliberately undeclared because the official save-import guide still marks Android as TBD."),
		standalone("builtin-driver-pcsx2", "PCSX2", []string{"ps2"}, []string{"windows", "steamos-bazzite"}, map[string][]string{"windows": {"pcsx2-qt.exe"}, "steamos-bazzite": {"pcsx2-qt", "pcsx2"}}, []string{"-batch", "{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "container", Layout: "memory-card", Patterns: []string{"{{driver.user_dir}}/memcards"}}, map[string]string{"settings": "inis", "memory_cards": "memcards"}, []string{"https://github.com/PCSX2/pcsx2/blob/master/pcsx2-qt/QtHost.cpp", "https://github.com/PCSX2/pcsx2-net-www/blob/main/docs/configuration/memcards.md"}, "File and folder memory cards are shared containers. Bind a per-game card only after PCSX2 is configured to use one; otherwise preserve the whole reviewed container."),
		standalone("builtin-driver-azahar", "Azahar / Nintendo 3DS", []string{"3ds"}, []string{"windows", "steamos-bazzite", "android"}, map[string][]string{"windows": {"azahar.exe"}, "steamos-bazzite": {"azahar"}}, []string{"{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "directory", Patterns: []string{"{{driver.user_dir}}/title/{{edition.title_id_high}}/{{edition.title_id_low}}/data/00000001"}}, map[string]string{"sd_identity_root": "sdmc/Nintendo 3DS/<ID0>/<ID1>", "nand": "nand"}, []string{"https://github.com/azahar-emu/azahar/blob/master/src/android/app/src/main/AndroidManifest.xml", "https://github.com/azahar-emu/azahar/blob/master/src/android/app/src/main/java/org/citra/citra_emu/fragments/EmulationFragment.kt", "https://github.com/azahar-emu/azahar/blob/master/src/android/app/build.gradle.kts"}, "Android uses Azahar's exported explicit VIEW activity with a granted SAF content URI. The vanilla and Google Play application IDs are both declared and selected only when installed. Save sync still requires the user to authorize Azahar's selected user-data tree and set --driver-root to the exact SD identity root below Nintendo 3DS/ID0/ID1. A 16-hex Title ID is mandatory for title saves; 3DSX title ID 0 does not get a fabricated per-title save binding."),
		standalone("builtin-driver-dolphin", "Dolphin", []string{"gamecube", "wii"}, []string{"windows", "steamos-bazzite", "rocknix"}, map[string][]string{"windows": {"Dolphin.exe"}, "steamos-bazzite": {"dolphin-emu", "dolphin"}, "rocknix": {"dolphin-emu", "dolphin"}}, []string{"--batch", "--exec", "{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "directory", ScopeByPlatform: map[string]string{"gamecube": "container", "wii": "game"}, LayoutByPlatform: map[string]string{"gamecube": "memory-card", "wii": "directory"}, PatternsByPlatform: map[string][]string{"gamecube": {"{{driver.user_dir}}/GC"}, "wii": {"{{driver.user_dir}}/Wii/title/{{edition.title_id_high}}/{{edition.title_id_low}}/data"}}}, map[string]string{"gamecube_saves": "GC", "wii_saves": "Wii/title"}, []string{"https://github.com/dolphin-emu/dolphin/blob/master/Source/Core/UICommon/CommandLineParse.cpp", "https://github.com/dolphin-emu/dolphin/blob/master/Source/Android/app/src/main/AndroidManifest.xml", "https://github.com/dolphin-emu/dolphin/blob/master/Source/Android/app/src/main/java/org/dolphinemu/dolphinemu/activities/AppLinkActivity.kt"}, "GameCube memory cards are containers; Wii saves use canonical platform wii and title directories. A 16-hex Title ID is required for Wii paths. Current Android Dolphin does not expose its emulation activity or a raw content-URI launch contract: the only exported app link resolves a pre-indexed Game ID from Dolphin's own cache. Android therefore remains deliberately undeclared until library registration plus stable Game-ID discovery can be completed without private APIs."),
		ppsspp,
		standalone("builtin-driver-rpcs3", "RPCS3", []string{"ps3"}, []string{"windows", "steamos-bazzite"}, map[string][]string{"windows": {"rpcs3.exe"}, "steamos-bazzite": {"rpcs3"}}, []string{"--no-gui", "{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "directory", Patterns: []string{"{{driver.user_dir}}/dev_hdd0/home/00000001/savedata/{{edition.product_code}}"}}, map[string]string{"settings": "config.yml", "savedata": "dev_hdd0/home/00000001/savedata"}, []string{"https://github.com/RPCS3/rpcs3/blob/master/rpcs3/Emu/System.cpp", "https://github.com/RPCS3/rpcs3"}, "Product Code is required. Games with multiple suffixed save directories need explicit additional binding paths; no wildcard is expanded implicitly."),
		standalone("builtin-driver-vita3k", "Vita3K", []string{"psvita"}, []string{"windows", "steamos-bazzite"}, map[string][]string{"windows": {"Vita3K.exe"}, "steamos-bazzite": {"Vita3K"}}, []string{"--installed-path", "{{edition.title_id}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "directory", Patterns: []string{"{{driver.user_dir}}/ux0/user/00/savedata/{{edition.title_id}}"}}, map[string]string{"settings": "config.yml", "savedata": "ux0/user/00/savedata"}, []string{"https://github.com/Vita3K/Vita3K/blob/master/vita3k/config/src/config.cpp", "https://github.com/Vita3K/Vita3K/blob/master/vita3k/main.cpp"}, "Vita3K launches installed titles and stores a directory per Title ID. Title ID is mandatory. Android is not advertised until its explicit installed-title Intent is fixture-tested."),
		standalone("builtin-driver-cemu", "Cemu", []string{"wiiu"}, []string{"windows", "steamos-bazzite"}, map[string][]string{"windows": {"Cemu.exe"}, "steamos-bazzite": {"cemu"}}, []string{"--game", "{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "directory", Patterns: []string{"{{driver.user_dir}}/mlc01/usr/save/{{edition.title_id_high}}/{{edition.title_id_low}}/user"}}, map[string]string{"settings": "settings.xml", "savedata": "mlc01/usr/save"}, []string{"https://github.com/cemu-project/Cemu/blob/main/src/config/LaunchSettings.cpp", "https://github.com/cemu-project/Cemu"}, "Cemu saves are title directories below the configured MLC. A 16-hex Title ID is mandatory."),
		standalone("builtin-driver-flycast", "Flycast", []string{"atomiswave", "dreamcast", "naomi"}, []string{"windows", "steamos-bazzite", "rocknix"}, map[string][]string{"windows": {"flycast.exe"}, "steamos-bazzite": {"flycast"}, "rocknix": {"flycast"}}, []string{"{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "container", Layout: "vmu", Patterns: []string{"{{driver.user_dir}}/data"}}, map[string]string{"settings": "emu.cfg", "data": "data"}, []string{"https://github.com/flyinghead/flycast/blob/master/core/cfg/option.cpp"}, "VMU data may be shared or per-game depending on Flycast configuration. The reviewed data directory is therefore a container stream by default. Android is not advertised until an explicit package/activity contract is fixture-tested."),
		standalone("builtin-driver-xemu", "xemu", []string{"xbox"}, []string{"windows", "steamos-bazzite"}, map[string][]string{"windows": {"xemu.exe"}, "steamos-bazzite": {"xemu"}}, []string{"-dvd_path", "{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "container", Layout: "driver-defined"}, map[string]string{"settings": "xemu.toml"}, []string{"https://xemu.app/docs/cli/", "https://github.com/xemu-project/xemu"}, "Original Xbox storage is a shared virtual-disk container. Varkiv therefore does not infer a per-game save path; synchronize only an explicitly reviewed xemu profile or custom binding."),
		standalone("builtin-driver-xenia", "Xenia", []string{"xbox360"}, []string{"windows"}, map[string][]string{"windows": {"xenia.exe", "xenia_canary.exe"}}, []string{"{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "container", Layout: "driver-defined"}, map[string]string{"settings": "xenia.config.toml", "content": "content"}, []string{"https://github.com/xenia-project/xenia/blob/master/docs/quickstart/faq.md", "https://github.com/xenia-canary/xenia-canary"}, "Xbox 360 content and profiles may be shared across titles. Automatic per-game save discovery remains disabled until a title-aware binding is reviewed on the selected Xenia build. Linux frontends may wrap the Windows build with Wine or Proton, but that wrapper is device-specific and is not advertised as a native executable contract."),
		standalone("builtin-driver-bigpemu", "BigPEmu", []string{"jaguarcd"}, []string{"windows", "steamos-bazzite"}, map[string][]string{"windows": {"BigPEmu.exe"}, "steamos-bazzite": {"BigPEmu"}}, []string{"{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "driver-defined"}, nil, []string{"https://www.richwhitehouse.com/jaguar/"}, "Jaguar CD launch is catalogued for user-supplied BigPEmu builds. Save locations vary by build and configuration, so synchronization requires an explicit reviewed binding."),
		standalone("builtin-driver-tsugaru", "Tsugaru", []string{"fmtowns"}, []string{"windows", "steamos-bazzite"}, map[string][]string{"windows": {"Tsugaru_CUI.exe", "Tsugaru_GUI.exe"}, "steamos-bazzite": {"Tsugaru_CUI"}}, []string{"-CD", "{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "driver-defined"}, nil, []string{"https://github.com/captainys/TOWNSEMU"}, "FM Towns launch is catalogued for user-supplied Tsugaru builds. Machine ROMs and per-game storage remain outside Varkiv; save synchronization requires an explicit reviewed binding."),
		standalone("builtin-driver-mame", "MAME", []string{"arcade", "gameandwatch"}, nonAndroidTargets, map[string][]string{"windows": {"mame.exe"}, "steamos-bazzite": {"mame"}, "rocknix": {"mame"}, "darkos": {"mame"}, "arkos": {"mame"}, "knulli": {"mame"}, "muos": {"mame"}, "onionos": {"mame"}}, []string{"{{rom.stem}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "directory", Patterns: []string{"{{driver.user_dir}}/nvram/{{rom.stem}}"}}, map[string]string{"settings": "mame.ini", "nvram": "nvram"}, []string{"https://github.com/mamedev/mame/blob/master/docs/source/commandline/commandline-all.rst"}, "MAME uses canonical platform arcade or gameandwatch and the machine/ROM-set name. NVRAM is synchronized only when the exact per-machine directory exists."),
		standalone("builtin-driver-fbneo", "FinalBurn Neo", []string{"arcade", "neogeo"}, nonAndroidTargets, map[string][]string{"windows": {"fbneo.exe"}, "steamos-bazzite": {"fbneo"}, "rocknix": {"fbneo"}, "darkos": {"fbneo"}, "arkos": {"fbneo"}, "knulli": {"fbneo"}, "muos": {"fbneo"}, "onionos": {"fbneo"}}, []string{"{{rom.path}}"}, catalog.DriverSaveSpec{Scope: "game", Layout: "driver-defined"}, map[string]string{"settings": "config"}, []string{"https://github.com/finalburnneo/FBNeo", "https://docs.libretro.com/library/fbneo/"}, "Standalone save layout varies by build. Canonical platform neogeo replaces the collection alias fbneo. No automatic save path is proposed until the user supplies a reviewed custom binding; libretro users should choose the RetroArch driver instead."),
	)
	for index := range drivers {
		if drivers[index].ID == "builtin-driver-eden" {
			drivers[index].ContractVersion = 5
		}
		if drivers[index].ID == "builtin-driver-azahar" {
			drivers[index].ContractVersion = 5
			drivers[index].Launch.AndroidIntent = &catalog.AndroidIntentSpec{Action: "android.intent.action.VIEW", Package: "org.azahar_emu.azahar", PackageCandidates: []string{"io.github.lime3ds.android"}, Activity: "org.citra.citra_emu.activities.EmulationActivity", Data: "{{rom.uri}}", MIMEType: "application/octet-stream", Categories: []string{"android.intent.category.DEFAULT"}, Flags: []string{"grant-read-uri", "new-task", "clear-top"}}
			drivers[index].SupportLevel = "package-tested"
			drivers[index].Evidence["scope"] = "real-android-emulator"
			drivers[index].Evidence["verified_at"] = "2026-08-30"
			drivers[index].Evidence["result"] = "passed"
			drivers[index].Evidence["verified_release"] = "2126.0"
			drivers[index].Evidence["verified_packages"] = []string{"org.azahar_emu.azahar", "io.github.lime3ds.android"}
			drivers[index].Evidence["fixture"] = "deterministic Apache-2.0 3DSX"
			drivers[index].Evidence["boundary"] = "API 35 ARM64 AVD only; real handheld hardware, controllers, OEM lifecycle behavior, and title-save synchronization remain unverified."
		}
		if drivers[index].ID == "builtin-driver-dolphin" {
			drivers[index].ContractVersion = 5
		}
		if drivers[index].ID == "builtin-driver-mame" {
			drivers[index].ContractVersion = 7
		}
		if drivers[index].ID == "builtin-driver-fbneo" {
			drivers[index].ContractVersion = 6
		}
	}
	return drivers
}

func builtinRetroArchCores() []catalog.NewRetroArchCore {
	core := func(id, name, library string, platforms ...string) catalog.NewRetroArchCore {
		return catalog.NewRetroArchCore{ID: "builtin-core-" + id, Name: name, ContractVersion: 2, LibraryNames: []string{library}, Platforms: platforms, SupportLevel: "catalogued", Evidence: map[string]any{"note": "Registry entry only; Device Agent must verify the installed core binary and version."}, Builtin: true, Enabled: boolPointer(true)}
	}
	cores := []catalog.NewRetroArchCore{
		core("mesen", "Mesen", "mesen_libretro", "famicomdisk", "nes"), core("snes9x", "Snes9x", "snes9x_libretro", "snes"), core("mgba", "mGBA", "mgba_libretro", "gb", "gbc", "gba"), core("gambatte", "Gambatte", "gambatte_libretro", "gb", "gbc"), core("beetle-vb", "Beetle VB", "mednafen_vb_libretro", "virtualboy"), core("gw", "Handheld Electronic (GW)", "gw_libretro", "gameandwatch"), core("pokemini", "PokeMini", "pokemini_libretro", "pokemini"), core("mupen64plus-next", "Mupen64Plus-Next", "mupen64plus_next_libretro", "n64", "n64dd"), core("melonds-ds", "melonDS DS", "melondsds_libretro", "nds"), core("genesis-plus-gx", "Genesis Plus GX", "genesis_plus_gx_libretro", "gamegear", "mastersystem", "megadrive", "segacd", "sg1000"), core("flycast", "Flycast", "flycast_libretro", "atomiswave", "dreamcast", "naomi"), core("pcsx-rearmed", "PCSX-ReARMed", "pcsx_rearmed_libretro", "psx"), core("beetle-psx-hw", "Beetle PSX HW", "mednafen_psx_hw_libretro", "psx"), core("ppsspp", "PPSSPP", "ppsspp_libretro", "psp"), core("fbneo", "FinalBurn Neo", "fbneo_libretro", "arcade", "neogeo"), core("mame", "MAME", "mame_libretro", "arcade", "gameandwatch"),
		core("applewin", "AppleWin", "applewin_libretro", "apple2"), core("puae", "PUAE", "puae_libretro", "amiga", "amigacd32"), core("caprice32", "Caprice32", "cap32_libretro", "amstradcpc"), core("atari800", "Atari800", "atari800_libretro", "atari8bit"), core("hatari", "Hatari", "hatari_libretro", "atarist"), core("vice-x64sc", "VICE x64sc", "vice_x64sc_libretro", "c64"), core("same-cdi", "SAME CDi", "same_cdi_libretro", "cdi"), core("bluemsx", "blueMSX", "bluemsx_libretro", "colecovision", "msx", "msx2"), core("freeintv", "FreeIntv", "freeintv_libretro", "intellivision"), core("quasi88", "QUASI88", "quasi88_libretro", "pc88"), core("np2kai", "Neko Project II Kai", "np2kai_libretro", "pc98"), core("retro8", "Retro8", "retro8_libretro", "pico8"), core("beetle-supergrafx", "Beetle SuperGrafx", "mednafen_supergrafx_libretro", "supergrafx"), core("vecx", "vecx", "vecx_libretro", "vectrex"), core("px68k", "PX68k", "px68k_libretro", "x68000"), core("fuse", "Fuse", "fuse_libretro", "zxspectrum"),
		core("stella2014", "Stella 2014", "stella2014_libretro", "2600"), core("a5200", "a5200", "a5200_libretro", "5200"), core("prosystem", "ProSystem", "prosystem_libretro", "7800"), core("virtualjaguar", "Virtual Jaguar", "virtualjaguar_libretro", "jaguar"), core("handy", "Handy", "handy_libretro", "lynx"), core("beetle-wswan", "Beetle WonderSwan", "mednafen_wswan_libretro", "wonderswan", "wonderswancolor"), core("dosbox-pure", "DOSBox Pure", "dosbox_pure_libretro", "dos"), core("scummvm", "ScummVM", "scummvm_libretro", "scummvm"), core("beetle-pcfx", "Beetle PC-FX", "mednafen_pcfx_libretro", "pcfx"), core("beetle-pce-fast", "Beetle PCE FAST", "mednafen_pce_fast_libretro", "pcengine", "pcenginecd"), core("beetle-neopop", "Beetle NeoPop", "mednafen_ngp_libretro", "ngpc"), core("neocd", "NeoCD", "neocd_libretro", "neogeocd"), core("picodrive", "PicoDrive", "picodrive_libretro", "sega32x"), core("opera", "Opera", "opera_libretro", "3do"), core("beetle-saturn", "Beetle Saturn", "mednafen_saturn_libretro", "saturn"),
	}
	coreSources := map[string][]string{
		"builtin-core-stella2014":      {"https://docs.libretro.com/library/stella/", "https://github.com/libretro/libretro-super/blob/master/dist/info/stella2014_libretro.info"},
		"builtin-core-a5200":           {"https://github.com/libretro/libretro-super/blob/master/dist/info/a5200_libretro.info"},
		"builtin-core-prosystem":       {"https://docs.libretro.com/library/prosystem/", "https://github.com/libretro/libretro-super/blob/master/dist/info/prosystem_libretro.info"},
		"builtin-core-virtualjaguar":   {"https://docs.libretro.com/library/virtual_jaguar/", "https://github.com/libretro/libretro-super/blob/master/dist/info/virtualjaguar_libretro.info"},
		"builtin-core-handy":           {"https://docs.libretro.com/library/handy/", "https://github.com/libretro/libretro-super/blob/master/dist/info/handy_libretro.info"},
		"builtin-core-beetle-wswan":    {"https://docs.libretro.com/library/beetle_cygne/", "https://github.com/libretro/libretro-super/blob/master/dist/info/mednafen_wswan_libretro.info"},
		"builtin-core-dosbox-pure":     {"https://docs.libretro.com/library/dosbox_pure/", "https://github.com/libretro/libretro-super/blob/master/dist/info/dosbox_pure_libretro.info"},
		"builtin-core-scummvm":         {"https://docs.libretro.com/library/scummvm/", "https://github.com/libretro/libretro-super/blob/master/dist/info/scummvm_libretro.info"},
		"builtin-core-beetle-pcfx":     {"https://docs.libretro.com/library/beetle_pc_fx/", "https://github.com/libretro/libretro-super/blob/master/dist/info/mednafen_pcfx_libretro.info"},
		"builtin-core-beetle-pce-fast": {"https://docs.libretro.com/library/beetle_pce_fast/", "https://github.com/libretro/libretro-super/blob/master/dist/info/mednafen_pce_fast_libretro.info"},
		"builtin-core-beetle-neopop":   {"https://docs.libretro.com/library/beetle_neopop/", "https://github.com/libretro/libretro-super/blob/master/dist/info/mednafen_ngp_libretro.info"},
		"builtin-core-neocd":           {"https://github.com/libretro/libretro-super/blob/master/dist/info/neocd_libretro.info"},
		"builtin-core-picodrive":       {"https://docs.libretro.com/library/picodrive/", "https://github.com/libretro/libretro-super/blob/master/dist/info/picodrive_libretro.info"},
		"builtin-core-opera":           {"https://github.com/libretro/libretro-super/blob/master/dist/info/opera_libretro.info"},
		"builtin-core-beetle-saturn":   {"https://github.com/libretro/libretro-super/blob/master/dist/info/mednafen_saturn_libretro.info"},
	}
	for index := range cores {
		if sources, ok := coreSources[cores[index].ID]; ok {
			cores[index].Evidence = map[string]any{"note": "Catalogued from the official Libretro core documentation and info manifest; Device Agent must still verify the installed binary and required firmware on the target device.", "sources": sources}
		}
		if cores[index].ID == "builtin-core-handy" || cores[index].ID == "builtin-core-prosystem" {
			cores[index].Evidence["save_support"] = false
		}
		if cores[index].ID == "builtin-core-beetle-neopop" {
			cores[index].Evidence["save_pattern"] = "{{rom.stem}}.flash"
		}
		if cores[index].ID == "builtin-core-dosbox-pure" || cores[index].ID == "builtin-core-scummvm" {
			cores[index].Evidence["save_binding"] = "manual-required"
		}
		if cores[index].ID == "builtin-core-fbneo" || cores[index].ID == "builtin-core-mame" {
			cores[index].ContractVersion = 3
		}
		if cores[index].ID == "builtin-core-beetle-vb" {
			cores[index].Evidence = map[string]any{
				"note":    "Registry entry only; Device Agent must verify the installed core binary and version.",
				"sources": []string{"https://docs.libretro.com/library/beetle_vb/", "https://github.com/libretro/libretro-super/blob/master/recipes/android/cores-android"},
			}
		}
		if cores[index].ID == "builtin-core-gw" {
			cores[index].Evidence = map[string]any{
				"note":          "The official GW core loads .mgw content and reports no native save or state support. ZIP containers remain a frontend concern and must be verified on the target device.",
				"save_support":  false,
				"state_support": false,
				"sources":       []string{"https://docs.libretro.com/library/gw/", "https://github.com/libretro/gw-libretro", "https://github.com/libretro/libretro-super/blob/master/dist/info/gw_libretro.info"},
			}
		}
		if cores[index].ID == "builtin-core-snes9x" {
			cores[index].ContractVersion = 3
			cores[index].SupportLevel = "package-tested"
			cores[index].Evidence = map[string]any{"scope": "isolated-roundtrip", "verified_at": "2026-08-29", "result": "passed", "version": "1.63", "commit": "6ca2343e5f3b0acbea49ca958251e3a0af58a81d", "format": "raw-libretro-srm", "retroarch_version": "1.22.2", "retroarch_commit": "69a4f0ea1e8aaf442ae4858f2e7f2b31a1776576", "note": "The exact commit passed a Web-to-native-to-Web 2 KiB SRAM handshake. A device with only the same display name is not verified; the Device Agent must attest the installed binary identity before cross-driver sharing can be promoted."}
		}
	}
	return cores
}

func builtinCoreMappings() []catalog.NewCoreMapping {
	defaults := map[string]string{"2600": "stella2014", "3do": "opera", "5200": "a5200", "7800": "prosystem", "amiga": "puae", "amigacd32": "puae", "amstradcpc": "caprice32", "apple2": "applewin", "arcade": "fbneo", "atari8bit": "atari800", "atarist": "hatari", "atomiswave": "flycast", "c64": "vice-x64sc", "cdi": "same-cdi", "colecovision": "bluemsx", "dos": "dosbox-pure", "dreamcast": "flycast", "famicomdisk": "mesen", "gameandwatch": "gw", "gamegear": "genesis-plus-gx", "gb": "mgba", "gba": "mgba", "gbc": "mgba", "intellivision": "freeintv", "jaguar": "virtualjaguar", "lynx": "handy", "mastersystem": "genesis-plus-gx", "megadrive": "genesis-plus-gx", "msx": "bluemsx", "msx2": "bluemsx", "n64": "mupen64plus-next", "n64dd": "mupen64plus-next", "naomi": "flycast", "nds": "melonds-ds", "neogeo": "fbneo", "neogeocd": "neocd", "nes": "mesen", "ngpc": "beetle-neopop", "pc88": "quasi88", "pc98": "np2kai", "pcengine": "beetle-pce-fast", "pcenginecd": "beetle-pce-fast", "pcfx": "beetle-pcfx", "pico8": "retro8", "pokemini": "pokemini", "psp": "ppsspp", "psx": "pcsx-rearmed", "saturn": "beetle-saturn", "scummvm": "scummvm", "segacd": "genesis-plus-gx", "sega32x": "picodrive", "sg1000": "genesis-plus-gx", "snes": "snes9x", "supergrafx": "beetle-supergrafx", "vectrex": "vecx", "virtualboy": "beetle-vb", "wonderswan": "beetle-wswan", "wonderswancolor": "beetle-wswan", "x68000": "px68k", "zxspectrum": "fuse"}
	items := make([]catalog.NewCoreMapping, 0, len(defaults))
	for platformID, coreID := range defaults {
		items = append(items, catalog.NewCoreMapping{ID: "builtin-mapping-global-" + platformID, ScopeType: "global", PlatformID: platformID, CoreID: "builtin-core-" + coreID, Notes: "Catalogued default; device or edition mapping can override.", Builtin: true})
	}
	return items
}
