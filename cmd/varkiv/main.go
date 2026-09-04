package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	osuser "os/user"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"varkiv/internal/agenttray"
	"varkiv/internal/buildinfo"
	"varkiv/internal/bundler"
	"varkiv/internal/catalog"
	"varkiv/internal/deviceagent"
	"varkiv/internal/exporter"
	"varkiv/internal/hashpack"
	"varkiv/internal/importer"
	"varkiv/internal/platforms"
	"varkiv/internal/scanner"
	"varkiv/internal/server"
	"varkiv/internal/statebackup"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "scan":
		err = scan(os.Args[2:])
	case "import-pegasus":
		err = importPegasus(os.Args[2:])
	case "import-esde":
		err = importESDE(os.Args[2:])
	case "import-varkiv":
		err = importVarkiv(os.Args[2:])
	case "export-pegasus":
		err = exportPegasus(os.Args[2:])
	case "export-esde":
		err = exportESDE(os.Args[2:])
	case "build-pack":
		err = buildPack(os.Args[2:])
	case "hash-pack":
		err = hashPackCommand(os.Args[2:])
	case "runtime-hints":
		err = runtimeHintCommand(os.Args[2:])
	case "db-check":
		err = dbCheck(os.Args[2:])
	case "release-audit":
		err = releaseAudit(os.Args[2:])
	case "backup":
		err = backupDatabase(os.Args[2:])
	case "restore-db":
		err = restoreDatabase(os.Args[2:])
	case "backup-state":
		err = backupState(os.Args[2:])
	case "check-state":
		err = checkState(os.Args[2:])
	case "restore-state":
		err = restoreState(os.Args[2:])
	case "platforms":
		err = listPlatforms(os.Args[2:])
	case "agent":
		err = agentCommand(os.Args[2:])
	case "version", "--version", "-version", "-v":
		err = versionCommand(os.Args[2:], os.Stdout)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		log.Fatal("error: ", sanitizeCommandError(err, os.Args[2:]))
	}
}

var privateCLIFlags = map[string]bool{
	"--binary": true, "--code": true, "--config": true, "--db": true,
	"--driver-root": true, "--from": true, "--library": true, "--name": true, "--out": true,
	"--path": true, "--profile": true, "--rom-root": true, "--root": true,
	"--server": true, "--source": true, "--state": true, "--token": true,
	"--user": true, "--web-emulator-assets": true, "--web-emulator-directory": true,
	"--web-netplay-emulator-assets": true, "--web-netplay-emulator-directory": true,
	"--web-netplay-signal-upstream": true, "--web-netplay-ice-servers": true,
}

// sanitizeCommandError prevents command arguments from being copied into logs.
// The command still reports a useful operation-level error, but explicit host
// paths, pairing secrets, server origins, account names, and device metadata are
// represented only by a stable placeholder.
func sanitizeCommandError(source error, args []string) error {
	if source == nil {
		return nil
	}
	replacements := map[string]struct{}{}
	for index := 0; index < len(args); index++ {
		flagName, value, inline := strings.Cut(args[index], "=")
		if !privateCLIFlags[flagName] {
			continue
		}
		if !inline && index+1 < len(args) {
			index++
			value = args[index]
		}
		addPrivateCLIValue(replacements, value)
	}
	addPrivateCLIValue(replacements, os.Getenv("GAME_LIBRARY_TOKEN"))
	if workingDirectory, err := os.Getwd(); err == nil {
		addPrivateCLIValue(replacements, workingDirectory)
	}
	values := make([]string, 0, len(replacements))
	for value := range replacements {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	message := source.Error()
	for _, value := range values {
		if strings.Contains(message, value) {
			return errors.New("operation failed; private command details were <redacted>")
		}
	}
	return errors.New(message)
}

func addPrivateCLIValue(values map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	values[value] = struct{}{}
	if _, pathValue, ok := strings.Cut(value, "="); ok && strings.TrimSpace(pathValue) != "" {
		values[strings.TrimSpace(pathValue)] = struct{}{}
		value = strings.TrimSpace(pathValue)
	}
	if absolute, err := filepath.Abs(value); err == nil {
		values[filepath.Clean(absolute)] = struct{}{}
	}
}

func usage() {
	fmt.Print(`Varkiv - self-hosted personal game library

Usage:
  varkiv serve           --db FILE --library DIR [--state DIR] [--addr 127.0.0.1:8080] [--token SECRET]
  varkiv scan            --db FILE --library DIR --source DIR --platform SLUG
  varkiv import-pegasus  --db FILE --library DIR --source FILE_OR_LIBRARY_RELATIVE --platform SLUG [--content-root LIBRARY_RELATIVE_DIR] [--locale zh-CN]
  varkiv import-esde     --db FILE --library DIR --source FILE_OR_LIBRARY_RELATIVE --platform SLUG [--content-root LIBRARY_RELATIVE_DIR] [--locale zh-CN]
  varkiv import-varkiv --db FILE --library DIR --source FILE_OR_LIBRARY_RELATIVE
  varkiv export-pegasus  --db FILE --out DIR --allow-host-paths [--locale zh-CN]
  varkiv export-esde     --db FILE --out DIR --allow-host-paths [--locale zh-CN]
  varkiv build-pack      --db FILE --library DIR [--state DIR] --out DIR [--profile-id ID | --frontend es-de --target rocknix --mode copy]
  varkiv hash-pack export  --db FILE --out NEW_FILE --source-id ID --name NAME --license LICENSE --release VERSION [--publisher NAME]
  varkiv hash-pack preview --db FILE --from FILE [--json]
  varkiv hash-pack import  --db FILE --from FILE [--accept-conflicts]
  varkiv runtime-hints list  --db FILE [--edition ID] [--status pending] [--json]
  varkiv runtime-hints apply --db FILE --id ID
  varkiv db-check        --db FILE
  varkiv release-audit   --db FILE [--json] [--require-hardware]
  varkiv backup          --db FILE --out FILE
  varkiv restore-db      --from BACKUP --out NEW_DATABASE
  varkiv backup-state    --db FILE --state DIR --out NEW_BACKUP_DIR
  varkiv check-state     --from BACKUP_DIR
  varkiv restore-state   --from BACKUP_DIR --out NEW_RESTORE_ROOT
  varkiv platforms
  varkiv version         [--json]
  varkiv agent pair      --config FILE --server URL --code CODE --name NAME --root DIR [--path save_dir=DIR] [--driver-root DRIVER_ID=DIR] [--allow-http]
  varkiv agent configure --config FILE --driver-root DRIVER_ID=DIR [--driver-root DRIVER_ID=DIR]
  varkiv agent sync      --config FILE
  varkiv agent probe     --config FILE [--json]
  varkiv agent acceptance --config FILE --out NEW_FILE [--observe frontend-launch ...]
  varkiv agent service-template --kind systemd-user|windows-task|windows-tray-task --binary FILE --config FILE --out FILE
  varkiv agent target-package --kind windows-handheld|rocknix|knulli|darkos|arkos|muos|onionos|steamos-bazzite --binary FILE --config FILE --out DIR [--windows-user USER --windows-install-dir DIR]
  varkiv agent target-package verify --path DIR [--json]
  varkiv agent run       --config FILE [--interval 60s]
  varkiv agent tray      --config FILE [--interval 60s]  (Windows)
  varkiv agent status    --config FILE [--json]
`)
}

func hashPackCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("hash-pack requires export, preview, or import")
	}
	switch args[0] {
	case "export":
		return exportHashPack(args[1:])
	case "preview":
		return previewHashPack(args[1:])
	case "import":
		return importHashPack(args[1:])
	default:
		return fmt.Errorf("unknown hash-pack command %q", args[0])
	}
}

func readHashPackFile(path string) ([]byte, hashpack.Pack, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, hashpack.Pack{}, "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, hashpack.Pack{}, "", errors.New("hash pack must be an existing exact regular file")
	}
	if info.Size() < 1 || info.Size() > hashpack.MaxPackBytes {
		return nil, hashpack.Pack{}, "", fmt.Errorf("hash pack must contain between 1 and %d bytes", hashpack.MaxPackBytes)
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return nil, hashpack.Pack{}, "", err
	}
	pack, err := hashpack.Decode(data)
	if err != nil {
		return nil, hashpack.Pack{}, "", err
	}
	return data, pack, hashpack.Digest(data), nil
}

func exportHashPack(args []string) error {
	fs := flag.NewFlagSet("hash-pack export", flag.ContinueOnError)
	dbPath := fs.String("db", "./data/library.db", "SQLite database path")
	out := fs.String("out", "", "brand-new .hashpack output path")
	sourceID := fs.String("source-id", "", "portable source identifier")
	name := fs.String("name", "", "shared data set name")
	publisher := fs.String("publisher", "", "optional publisher")
	license := fs.String("license", "", "metadata license identifier")
	release := fs.String("release", "", "immutable release identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*out) == "" {
		return errors.New("--out is required")
	}
	store, err := openExistingDatabase(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	data, manifest, err := store.ExportHashPack(context.Background(), hashpack.Source{ID: *sourceID, Name: *name, Publisher: *publisher, License: *license}, *release)
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(absolute)
		}
	}()
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	complete = true
	fmt.Printf("hash_pack_created=true pack_id=%s records=%d\n", manifest.PackID, manifest.RecordCount)
	return nil
}

func previewHashPack(args []string) error {
	fs := flag.NewFlagSet("hash-pack preview", flag.ContinueOnError)
	dbPath := fs.String("db", "./data/library.db", "SQLite database path")
	from := fs.String("from", "", "existing .hashpack file")
	jsonOutput := fs.Bool("json", false, "write machine-readable preview")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*from) == "" {
		return errors.New("--from is required")
	}
	_, pack, digest, err := readHashPackFile(*from)
	if err != nil {
		return err
	}
	store, err := openExistingDatabase(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	preview, err := store.PreviewHashPack(context.Background(), pack, digest)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(preview)
	}
	fmt.Printf("source=%s release=%s records=%d new=%d existing=%d conflicts=%d existing_release=%t release_conflict=%t\n", preview.Source.ID, preview.Release, preview.RecordCount, preview.NewCount, preview.ExistingCount, preview.ConflictCount, preview.ExistingRelease, preview.ReleaseConflict)
	return nil
}

func importHashPack(args []string) error {
	fs := flag.NewFlagSet("hash-pack import", flag.ContinueOnError)
	dbPath := fs.String("db", "./data/library.db", "SQLite database path")
	from := fs.String("from", "", "existing .hashpack file")
	acceptConflicts := fs.Bool("accept-conflicts", false, "retain conflicting source-attributed metadata without overwriting other sources")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*from) == "" {
		return errors.New("--from is required")
	}
	_, pack, digest, err := readHashPackFile(*from)
	if err != nil {
		return err
	}
	store, err := openExistingDatabase(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	preview, err := store.PreviewHashPack(context.Background(), pack, digest)
	if err != nil {
		return err
	}
	if preview.ReleaseConflict {
		return catalog.ErrHashReleaseConflict
	}
	if preview.ConflictCount > 0 && !*acceptConflicts {
		return fmt.Errorf("hash pack has %d metadata conflicts; run preview and pass --accept-conflicts to retain both sources", preview.ConflictCount)
	}
	result, err := store.ImportHashPack(context.Background(), pack, digest)
	if err != nil {
		return err
	}
	fmt.Printf("hash_pack_imported=true source=%s release=%s records=%d existing_release=%t\n", result.Source.ID, result.Release.Version, result.ImportedRecords, result.ExistingRelease)
	return nil
}

func versionCommand(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "write a stable machine-readable version identity")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("version does not accept positional arguments")
	}
	if *jsonOutput {
		return json.NewEncoder(stdout).Encode(struct {
			Format             string `json:"format"`
			ApplicationVersion string `json:"application_version"`
		}{Format: "varkiv-version-v1", ApplicationVersion: buildinfo.Version})
	}
	_, err := fmt.Fprintln(stdout, "Varkiv", buildinfo.Version)
	return err
}

type repeatedFlag []string

func (values *repeatedFlag) String() string { return strings.Join(*values, ",") }
func (values *repeatedFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func defaultAgentConfig() string {
	root, err := os.UserConfigDir()
	if err != nil || root == "" {
		return "./varkiv-agent.json"
	}
	return filepath.Join(root, "Varkiv", "agent.json")
}

func agentCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("agent requires pair, configure, probe, acceptance, service-template, target-package, sync, run, tray, or status")
	}
	switch args[0] {
	case "pair":
		return agentPair(args[1:])
	case "configure":
		return agentConfigure(args[1:])
	case "sync":
		return agentSync(args[1:])
	case "probe":
		return agentProbe(args[1:])
	case "acceptance":
		return agentAcceptance(args[1:])
	case "service-template":
		return agentServiceTemplate(args[1:])
	case "target-package":
		return agentTargetPackage(args[1:])
	case "run":
		return agentRun(args[1:])
	case "tray":
		return agentTray(args[1:])
	case "status":
		return agentStatus(args[1:])
	default:
		return fmt.Errorf("unknown agent command %q", args[0])
	}
}

func agentTray(args []string) error {
	fs := flag.NewFlagSet("agent tray", flag.ContinueOnError)
	configPath := fs.String("config", defaultAgentConfig(), "private agent configuration file")
	interval := fs.Duration("interval", time.Minute, "sync interval (minimum 15s)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *interval < 15*time.Second {
		return errors.New("--interval must be at least 15s")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return agenttray.Run(ctx, *configPath, *interval)
}

func parseDriverRoots(rawValues []string) (map[string]string, error) {
	driverRoots := map[string]string{}
	for _, raw := range rawValues {
		driverID, value, ok := strings.Cut(raw, "=")
		driverID, value = strings.TrimSpace(driverID), strings.TrimSpace(value)
		validID := driverID != ""
		for _, char := range driverID {
			if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '-' && char != '_' && char != '.' {
				validID = false
				break
			}
		}
		if !ok || !validID || value == "" {
			return nil, fmt.Errorf("invalid --driver-root %q; expected DRIVER_ID=/explicit/directory", raw)
		}
		absolute, absoluteErr := filepath.Abs(value)
		if absoluteErr != nil {
			return nil, absoluteErr
		}
		info, statErr := os.Lstat(absolute)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("driver root for %s must be an existing real directory", driverID)
		}
		driverRoots[driverID] = absolute
	}
	return driverRoots, nil
}

func agentConfigure(args []string) error {
	fs := flag.NewFlagSet("agent configure", flag.ContinueOnError)
	configPath := fs.String("config", defaultAgentConfig(), "private agent configuration file")
	var rawDriverRoots repeatedFlag
	fs.Var(&rawDriverRoots, "driver-root", "explicit emulator user directory such as builtin-driver-pcsx2=/storage/pcsx2 (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(rawDriverRoots) == 0 {
		return errors.New("at least one --driver-root is required; configure never removes existing roots implicitly")
	}
	roots, err := parseDriverRoots(rawDriverRoots)
	if err != nil {
		return err
	}
	config, err := deviceagent.LoadConfig(*configPath)
	if err != nil {
		return deviceagent.SanitizeError(*configPath, err)
	}
	if config.DriverRoots == nil {
		config.DriverRoots = map[string]string{}
	}
	for id, path := range roots {
		config.DriverRoots[id] = path
	}
	if err = deviceagent.UpdateConfig(*configPath, config); err != nil {
		return deviceagent.SanitizeError(*configPath, err)
	}
	fmt.Printf("configured_driver_roots=%d\n", len(roots))
	return nil
}

func agentTargetPackage(args []string) error {
	if len(args) > 0 && args[0] == "verify" {
		return agentTargetPackageVerify(args[1:])
	}
	fs := flag.NewFlagSet("agent target-package", flag.ContinueOnError)
	kind := fs.String("kind", "", "target handheld platform")
	binaryPath := fs.String("binary", "", "varkiv binary built for the exact target architecture")
	configPath := fs.String("config", defaultAgentConfig(), "private agent configuration file")
	out := fs.String("out", "", "new private package directory; existing paths are never merged or replaced")
	windowsUser := fs.String("windows-user", "", "explicit Windows account for the least-privilege logon task")
	windowsInstallDir := fs.String("windows-install-dir", "", "explicit absolute Windows directory where package files will be copied")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kind == "" || *binaryPath == "" || *configPath == "" || *out == "" {
		return errors.New("--kind, --binary, --config, and --out are required")
	}
	result, err := deviceagent.BuildTargetPackage(deviceagent.TargetPackageInput{
		Kind:              *kind,
		BinaryPath:        *binaryPath,
		ConfigPath:        *configPath,
		OutputPath:        *out,
		WindowsUser:       *windowsUser,
		WindowsInstallDir: *windowsInstallDir,
	})
	if err != nil {
		return deviceagent.SanitizeError(*configPath, err)
	}
	fmt.Printf("target_package=created kind=%s files=%d sensitive=%t\n", result.Kind, len(result.Files), result.Sensitive)
	return nil
}

func agentTargetPackageVerify(args []string) error {
	fs := flag.NewFlagSet("agent target-package verify", flag.ContinueOnError)
	packagePath := fs.String("path", "", "private target package directory to inspect without modification")
	jsonOutput := fs.Bool("json", false, "print a machine-readable privacy-minimized result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *packagePath == "" {
		return errors.New("--path is required")
	}
	result, err := deviceagent.VerifyTargetPackage(*packagePath)
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoded, encodeErr := json.Marshal(result)
		if encodeErr != nil {
			return encodeErr
		}
		fmt.Println(string(encoded))
	} else {
		status := "invalid"
		if result.Verified {
			status = "verified"
		}
		fmt.Printf("target_package=%s kind=%s files=%d missing=%d changed=%d mode_mismatch=%d unsafe=%d extra=%d mode_checks=%t\n", status, result.Kind, result.Files, result.Missing, result.Changed, result.ModeMismatch, result.Unsafe, result.Extra, result.ModeChecksRun)
	}
	if !result.Verified {
		return errors.New("target package verification failed")
	}
	return nil
}

func agentPair(args []string) error {
	fs := flag.NewFlagSet("agent pair", flag.ContinueOnError)
	configPath := fs.String("config", defaultAgentConfig(), "private agent configuration file")
	serverURL := fs.String("server", "", "Varkiv server origin")
	code := fs.String("code", "", "short-lived pairing code")
	name := fs.String("name", "", "device display name")
	root := fs.String("root", "", "explicit local root containing save/config directories")
	osFamily := fs.String("os", "", "OS family override")
	distribution := fs.String("distribution", "", "handheld distribution")
	architecture := fs.String("arch", "", "architecture override")
	allowHTTP := fs.Bool("allow-http", false, "allow unencrypted HTTP to a non-loopback server")
	var rawPaths repeatedFlag
	var rawDriverRoots repeatedFlag
	var rawROMRoots repeatedFlag
	fs.Var(&rawPaths, "path", "path override such as save_dir=/storage/saves (repeatable)")
	fs.Var(&rawDriverRoots, "driver-root", "explicit emulator user directory such as builtin-driver-pcsx2=/storage/pcsx2 (repeatable)")
	fs.Var(&rawROMRoots, "rom-root", "explicit ROM inventory root such as gba=/storage/roms/gba (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *serverURL == "" || *code == "" || *name == "" || *root == "" {
		return errors.New("--server, --code, --name, and --root are required")
	}
	if _, err := os.Lstat(*configPath); err == nil {
		return errors.New("agent config already exists; move it explicitly before pairing another identity")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, err := os.Stat(*root); err != nil || !info.IsDir() {
		return errors.New("--root must reference an existing directory")
	}
	paths := map[string]string{}
	for _, raw := range rawPaths {
		key, value, ok := strings.Cut(raw, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || (key != "save_dir" && key != "config_dir" && key != "rom_dir" && key != "core_dir" && key != "emulator_dir") || value == "" {
			return fmt.Errorf("invalid --path %q; allowed keys are save_dir, config_dir, rom_dir, core_dir, and emulator_dir", raw)
		}
		paths[key] = value
	}
	driverRoots, err := parseDriverRoots(rawDriverRoots)
	if err != nil {
		return err
	}
	romRoots := map[string]string{}
	for _, raw := range rawROMRoots {
		platformID, value, ok := strings.Cut(raw, "=")
		platformID, value = canonicalPlatform(strings.TrimSpace(platformID)), strings.TrimSpace(value)
		if !ok || platformID == "" || value == "" {
			return fmt.Errorf("invalid --rom-root %q; expected platform=/explicit/directory", raw)
		}
		info, statErr := os.Stat(value)
		if statErr != nil || !info.IsDir() {
			return fmt.Errorf("ROM root for %s must be an existing directory", platformID)
		}
		romRoots[platformID] = value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config, err := deviceagent.Pair(ctx, deviceagent.PairInput{ServerURL: *serverURL, Code: *code, Name: *name, OSFamily: *osFamily, Distribution: *distribution, Architecture: *architecture, AgentVersion: buildinfo.Version, RootDir: *root, PathOverrides: paths, DriverRoots: driverRoots, ROMRoots: romRoots, AllowHTTP: *allowHTTP})
	if err != nil {
		return err
	}
	if err = deviceagent.SaveConfig(*configPath, config); err != nil {
		return err
	}
	fmt.Println("paired=true config_saved=true")
	return nil
}

func agentSync(args []string) error {
	fs := flag.NewFlagSet("agent sync", flag.ContinueOnError)
	configPath := fs.String("config", defaultAgentConfig(), "private agent configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := deviceagent.SyncOnce(ctx, *configPath)
	fmt.Printf("sync_status=%s session_recorded=%t uploaded=%d downloaded=%d conflicts=%d\n", result.Status, result.SessionID != "", result.Uploaded, result.Downloaded, result.Conflicts)
	return deviceagent.SanitizeError(*configPath, err)
}

func agentProbe(args []string) error {
	fs := flag.NewFlagSet("agent probe", flag.ContinueOnError)
	configPath := fs.String("config", defaultAgentConfig(), "private agent configuration file")
	jsonOutput := fs.Bool("json", false, "print a path-free JSON report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := deviceagent.ProbeRuntime(ctx, *configPath)
	if err != nil {
		return deviceagent.SanitizeError(*configPath, err)
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	fmt.Printf("target=%s emulator_dir_configured=%t core_dir_configured=%t installed_drivers=%d installed_cores=%d\n", result.Target, result.EmulatorDirConfigured, result.CoreDirConfigured, result.InstalledDrivers, result.InstalledCores)
	for _, item := range result.Drivers {
		fmt.Printf("driver id=%s status=%s candidate=%s\n", item.ID, item.Status, item.Match)
	}
	for _, item := range result.Cores {
		fmt.Printf("core id=%s status=%s candidate=%s\n", item.ID, item.Status, item.Match)
	}
	return nil
}

func agentAcceptance(args []string) error {
	fs := flag.NewFlagSet("agent acceptance", flag.ContinueOnError)
	configPath := fs.String("config", defaultAgentConfig(), "private agent configuration file")
	out := fs.String("out", "", "new privacy-minimized JSON report; existing files are never replaced")
	var observations repeatedFlag
	fs.Var(&observations, "observe", "confirmed real-device observation (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("--out is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report, err := deviceagent.BuildHardwareAcceptanceReport(ctx, *configPath, buildinfo.Version, observations)
	if err != nil {
		return deviceagent.SanitizeError(*configPath, err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	handle, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = handle.Close()
		if !complete {
			_ = os.Remove(*out)
		}
	}()
	if _, err = handle.Write(data); err == nil {
		err = handle.Sync()
	}
	if closeErr := handle.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	complete = true
	fmt.Printf("hardware_acceptance_report=created preflight=%t observations=%d review_required=true\n", report.SoftwarePreflight, len(report.ObservedOnHardware))
	return nil
}

func agentServiceTemplate(args []string) error {
	fs := flag.NewFlagSet("agent service-template", flag.ContinueOnError)
	kind := fs.String("kind", "", "systemd-user, windows-task, or windows-tray-task")
	binaryPath := fs.String("binary", "", "absolute path to the varkiv binary on the target device")
	configPath := fs.String("config", "", "absolute path to the private Agent configuration on the target device")
	out := fs.String("out", "", "new service definition file; existing files are never replaced")
	interval := fs.Duration("interval", time.Minute, "sync interval (minimum 15s)")
	current, _ := osuser.Current()
	defaultUser := ""
	if current != nil {
		defaultUser = current.Username
	}
	windowsUser := fs.String("user", defaultUser, "Windows account for the least-privilege interactive task")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kind == "" || *binaryPath == "" || *configPath == "" || *out == "" {
		return errors.New("--kind, --binary, --config, and --out are required")
	}
	filename, content, err := deviceagent.RenderServiceTemplate(deviceagent.ServiceTemplateInput{Kind: *kind, BinaryPath: *binaryPath, ConfigPath: *configPath, User: *windowsUser, Interval: *interval})
	if err != nil {
		return err
	}
	handle, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(*out)
		}
	}()
	if _, err = handle.WriteString(content); err == nil {
		err = handle.Sync()
	}
	if closeErr := handle.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	complete = true
	fmt.Printf("service_template=created kind=%s filename=%s\n", *kind, filename)
	return nil
}

func agentRun(args []string) error {
	fs := flag.NewFlagSet("agent run", flag.ContinueOnError)
	configPath := fs.String("config", defaultAgentConfig(), "private agent configuration file")
	interval := fs.Duration("interval", time.Minute, "sync interval (minimum 15s)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *interval < 15*time.Second {
		return errors.New("--interval must be at least 15s")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	run := func() {
		syncCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		result, err := deviceagent.SyncOnce(syncCtx, *configPath)
		if err != nil {
			log.Printf("sync status=%s session_recorded=%t uploaded=%d downloaded=%d conflicts=%d error=%v", result.Status, result.SessionID != "", result.Uploaded, result.Downloaded, result.Conflicts, deviceagent.SanitizeError(*configPath, err))
			return
		}
		log.Printf("sync status=%s session_recorded=%t uploaded=%d downloaded=%d conflicts=%d", result.Status, result.SessionID != "", result.Uploaded, result.Downloaded, result.Conflicts)
	}
	run()
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			run()
		}
	}
}

func agentStatus(args []string) error {
	fs := flag.NewFlagSet("agent status", flag.ContinueOnError)
	configPath := fs.String("config", defaultAgentConfig(), "private agent configuration file")
	asJSON := fs.Bool("json", false, "emit privacy-minimized JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	config, err := deviceagent.LoadConfig(*configPath)
	if err != nil {
		return deviceagent.SanitizeError(*configPath, err)
	}
	pending := "no"
	if config.Pending != nil {
		pending = "yes"
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(agentStatusView(config, pending))
	}
	fmt.Print(formatAgentStatus(config, pending))
	return nil
}

type agentStatusOutput struct {
	ServerConfigured  bool                         `json:"server_configured"`
	DevicePaired      bool                         `json:"device_paired"`
	ProfileConfigured bool                         `json:"profile_configured"`
	RootConfigured    bool                         `json:"root_configured"`
	Streams           int                          `json:"streams"`
	Pending           bool                         `json:"pending"`
	LastSync          *deviceagent.AgentSyncStatus `json:"last_sync,omitempty"`
}

func agentStatusView(config deviceagent.Config, pending string) agentStatusOutput {
	return agentStatusOutput{
		ServerConfigured:  config.ServerURL != "",
		DevicePaired:      config.DeviceID != "",
		ProfileConfigured: config.DeviceProfileID != "",
		RootConfigured:    config.RootDir != "",
		Streams:           len(config.Streams),
		Pending:           pending == "yes",
		LastSync:          config.LastSync,
	}
}

func formatAgentStatus(config deviceagent.Config, pending string) string {
	view := agentStatusView(config, pending)
	lastState := "never"
	sessionRecorded, uploaded, downloaded, conflicts, errorCode := false, 0, 0, 0, "none"
	if view.LastSync != nil {
		lastState = view.LastSync.State
		sessionRecorded = view.LastSync.SessionRecorded
		uploaded = view.LastSync.Uploaded
		downloaded = view.LastSync.Downloaded
		conflicts = view.LastSync.Conflicts
		if view.LastSync.ErrorCode != "" {
			errorCode = view.LastSync.ErrorCode
		}
	}
	return fmt.Sprintf("server_configured=%t device_paired=%t profile_configured=%t root_configured=%t streams=%d pending=%s last_sync=%s session_recorded=%t uploaded=%d downloaded=%d conflicts=%d error_code=%s\n",
		view.ServerConfigured, view.DevicePaired, view.ProfileConfigured, view.RootConfigured, view.Streams, pending, lastState, sessionRecorded, uploaded, downloaded, conflicts, errorCode)
}

type baseFlags struct{ db, library string }

func addBase(fs *flag.FlagSet) *baseFlags {
	b := &baseFlags{}
	fs.StringVar(&b.db, "db", "./data/library.db", "SQLite database path")
	fs.StringVar(&b.library, "library", "./library", "library root")
	return b
}
func open(b *baseFlags) (*catalog.Store, error) {
	if err := os.MkdirAll(filepath.Dir(b.db), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(b.library, 0o755); err != nil {
		return nil, err
	}
	store, err := catalog.Open(b.db)
	if err != nil {
		return nil, err
	}
	if err = server.EnsureDefaults(context.Background(), store); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	b := addBase(fs)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address")
	state := fs.String("state", "", "mutable state root for saves and generated packages (default: database directory)")
	token := fs.String("token", os.Getenv("GAME_LIBRARY_TOKEN"), "owner API token (or GAME_LIBRARY_TOKEN)")
	webEmulatorAssets := fs.String("web-emulator-assets", os.Getenv("VARKIV_WEB_EMULATOR_ASSETS"), "optional EmulatorJS data directory URL or same-origin path")
	webEmulatorDirectory := fs.String("web-emulator-directory", os.Getenv("VARKIV_WEB_EMULATOR_DIRECTORY"), "optional local EmulatorJS data directory served from /emulatorjs/")
	webNetplayAssets := fs.String("web-netplay-emulator-assets", os.Getenv("VARKIV_WEB_NETPLAY_EMULATOR_ASSETS"), "optional experimental EmulatorJS data directory URL")
	webNetplayDirectory := fs.String("web-netplay-emulator-directory", os.Getenv("VARKIV_WEB_NETPLAY_EMULATOR_DIRECTORY"), "optional verified experimental EmulatorJS data directory")
	webNetplaySignal := fs.String("web-netplay-signal-upstream", os.Getenv("VARKIV_WEB_NETPLAY_SIGNAL_UPSTREAM"), "optional internal EmulatorJS netplay signal origin")
	webNetplayICE := fs.String("web-netplay-ice-servers", os.Getenv("VARKIV_WEB_NETPLAY_ICE_SERVERS"), "optional JSON array of browser ICE servers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !loopbackAddress(*addr) && *token == "" {
		return fmt.Errorf("--token or GAME_LIBRARY_TOKEN is required for non-loopback address %q", *addr)
	}
	if strings.TrimSpace(*state) == "" {
		*state = filepath.Dir(b.db)
	}
	store, err := open(b)
	if err != nil {
		return err
	}
	defer store.Close()
	app, err := server.New(store, b.library, server.WithToken(*token), server.WithStateRoot(*state), server.WithWebEmulatorAssets(*webEmulatorAssets), server.WithWebEmulatorDirectory(*webEmulatorDirectory), server.WithWebNetplay(*webNetplayAssets, *webNetplayDirectory, *webNetplaySignal, *webNetplayICE))
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: *addr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second}
	openAddr := *addr
	if openAddr == "" {
		openAddr = ":8080"
	}
	if openAddr[0] == ':' {
		openAddr = "localhost" + openAddr
	}
	log.Printf("Varkiv %s\nlibrary: configured\nstate: configured\ndatabase: configured\nopen: http://%s", buildinfo.Version, openAddr)
	if *token != "" {
		log.Print("authentication: bearer token required")
	}
	return srv.ListenAndServe()
}

func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func openExistingDatabase(path string) (*catalog.Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--db is required")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("database is not accessible: %w", err)
	}
	return catalog.Open(path)
}

func openExistingDatabaseReadOnly(path string) (*catalog.Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--db is required")
	}
	store, err := catalog.OpenReadOnly(path)
	if err != nil {
		return nil, fmt.Errorf("database is not accessible for read-only access: %w", err)
	}
	return store, nil
}

func dbCheck(args []string) error {
	fs := flag.NewFlagSet("db-check", flag.ContinueOnError)
	dbPath := fs.String("db", "./data/library.db", "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := catalog.OpenReadOnly(*dbPath)
	if err != nil {
		return fmt.Errorf("database is not accessible for a read-only check: %w", err)
	}
	defer store.Close()
	version, err := store.SchemaVersion(context.Background())
	if err != nil {
		return err
	}
	if version != catalog.CurrentSchemaVersion {
		return fmt.Errorf("database schema %d is not the current supported schema %d", version, catalog.CurrentSchemaVersion)
	}
	result, err := store.IntegrityCheck(context.Background())
	if err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("database integrity check returned %q", result)
	}
	violations, err := store.ForeignKeyViolationCount(context.Background())
	if err != nil {
		return err
	}
	if violations != 0 {
		return fmt.Errorf("database foreign-key check returned %d violations", violations)
	}
	if err = store.ValidateRuntimeCatalog(context.Background()); err != nil {
		return err
	}
	fmt.Printf("schema_version=%d supported=%d integrity=%s foreign_keys=ok mode=read-only\n", version, catalog.CurrentSchemaVersion, result)
	return nil
}

type externalReleaseGate struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type releaseAuditReport struct {
	Format             string                          `json:"format"`
	ApplicationVersion string                          `json:"application_version"`
	SchemaVersion      int                             `json:"schema_version"`
	SoftwareReady      bool                            `json:"software_ready"`
	HardwareReady      bool                            `json:"hardware_ready"`
	Software           server.SoftwareReadinessReport  `json:"software"`
	Hardware           catalog.HardwareReadinessReport `json:"hardware"`
	PublicReleaseReady bool                            `json:"public_release_ready"`
	ExternalGates      []externalReleaseGate           `json:"external_gates"`
}

func currentExternalReleaseGates() []externalReleaseGate {
	return []externalReleaseGate{
		{ID: "formal-product-name", Status: "external-review-required"},
		{ID: "project-license", Status: "ready"},
		{ID: "contribution-rights", Status: "ready"},
		{ID: "protected-release-authorization", Status: "external-review-required"},
	}
}

func releaseAudit(args []string) error {
	return releaseAuditTo(args, os.Stdout)
}

func releaseAuditTo(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("release-audit", flag.ContinueOnError)
	dbPath := fs.String("db", "./data/library.db", "SQLite database path")
	jsonOutput := fs.Bool("json", false, "write stable JSON")
	requireHardware := fs.Bool("require-hardware", false, "return a failure while real-device evidence gates are pending")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := openExistingDatabaseReadOnly(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx := context.Background()
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version != catalog.CurrentSchemaVersion {
		return fmt.Errorf("database schema %d is not the current supported schema %d", version, catalog.CurrentSchemaVersion)
	}
	integrity, err := store.IntegrityCheck(ctx)
	if err != nil || integrity != "ok" {
		if err != nil {
			return err
		}
		return fmt.Errorf("database integrity check returned %q", integrity)
	}
	violations, err := store.ForeignKeyViolationCount(ctx)
	if err != nil {
		return err
	}
	if violations != 0 {
		return fmt.Errorf("database foreign-key check returned %d violations", violations)
	}
	if err = store.ValidateRuntimeCatalog(ctx); err != nil {
		return err
	}
	hardware, err := store.HardwareReadiness(ctx)
	if err != nil {
		return err
	}
	software, err := server.SoftwareReadiness(ctx, store)
	if err != nil {
		return err
	}
	report := releaseAuditReport{
		Format: "varkiv-release-audit-v3", ApplicationVersion: buildinfo.Version, SchemaVersion: version, SoftwareReady: software.Ready, HardwareReady: hardware.Ready, Software: software, Hardware: hardware,
		PublicReleaseReady: false,
		ExternalGates:      currentExternalReleaseGates(),
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err = encoder.Encode(report); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "application_version=%s software_ready=%t hardware_ready=%t public_release_ready=%t schema_version=%d mode=read-only\n", report.ApplicationVersion, report.SoftwareReady, report.HardwareReady, report.PublicReleaseReady, report.SchemaVersion)
		for _, gate := range report.Software.Gates {
			fmt.Fprintf(stdout, "software_gate=%s status=%s missing=%s disabled=%s drifted=%s\n", gate.ID, gate.Status, strings.Join(gate.Missing, ","), strings.Join(gate.Disabled, ","), strings.Join(gate.Drifted, ","))
		}
		for _, gate := range report.Hardware.Gates {
			fmt.Fprintf(stdout, "hardware_gate=%s status=%s required=%s missing=%s targets=%s\n", gate.ID, gate.Status, gate.RequiredLevel, strings.Join(gate.Missing, ","), strings.Join(gate.SatisfiedTargets, ","))
		}
		for _, gate := range report.ExternalGates {
			fmt.Fprintf(stdout, "external_gate=%s status=%s\n", gate.ID, gate.Status)
		}
	}
	if !software.Ready {
		return errors.New("software release evidence gates are pending")
	}
	if *requireHardware && !hardware.Ready {
		return errors.New("real-device release evidence gates are pending")
	}
	return nil
}

func backupDatabase(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	dbPath := fs.String("db", "./data/library.db", "SQLite database path")
	out := fs.String("out", "", "new backup file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*out) == "" {
		return errors.New("--out is required")
	}
	store, err := openExistingDatabase(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.Backup(context.Background(), *out); err != nil {
		return err
	}
	fmt.Println("backup_created=true")
	return nil
}

func restoreDatabase(args []string) error {
	fs := flag.NewFlagSet("restore-db", flag.ContinueOnError)
	from := fs.String("from", "", "existing SQLite backup file")
	out := fs.String("out", "", "new restored database file; existing paths are never replaced")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*from) == "" || strings.TrimSpace(*out) == "" {
		return errors.New("--from and --out are required")
	}
	version, err := catalog.RestoreDatabaseBackup(context.Background(), *from, *out)
	if err != nil {
		return err
	}
	fmt.Printf("restore_created=true schema_version=%d integrity=ok\n", version)
	return nil
}

func backupState(args []string) error {
	fs := flag.NewFlagSet("backup-state", flag.ContinueOnError)
	dbPath := fs.String("db", "./data/library.db", "SQLite database path")
	state := fs.String("state", "", "service-managed state root")
	out := fs.String("out", "", "brand-new backup directory outside the state root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*state) == "" || strings.TrimSpace(*out) == "" {
		return errors.New("--state and --out are required")
	}
	report, err := statebackup.Create(context.Background(), *dbPath, *state, *out)
	if err != nil {
		return err
	}
	fmt.Printf("state_backup_created=true format_version=%d schema_version=%d files=%d bytes=%d managed_roms=%d managed_media=%d save_blobs=%d recovery_snapshots=%d\n",
		statebackup.FormatVersion, report.SchemaVersion, report.Files, report.Bytes, report.ManagedArtifacts, report.ManagedMedia, report.SaveBlobs, report.RecoverySnapshots)
	return nil
}

func checkState(args []string) error {
	fs := flag.NewFlagSet("check-state", flag.ContinueOnError)
	from := fs.String("from", "", "existing complete-state backup directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*from) == "" {
		return errors.New("--from is required")
	}
	report, err := statebackup.Check(context.Background(), *from)
	if err != nil {
		return err
	}
	fmt.Printf("state_backup_valid=true format_version=%d schema_version=%d files=%d bytes=%d managed_roms=%d managed_media=%d save_blobs=%d recovery_snapshots=%d\n",
		statebackup.FormatVersion, report.SchemaVersion, report.Files, report.Bytes, report.ManagedArtifacts, report.ManagedMedia, report.SaveBlobs, report.RecoverySnapshots)
	return nil
}

func restoreState(args []string) error {
	fs := flag.NewFlagSet("restore-state", flag.ContinueOnError)
	from := fs.String("from", "", "existing complete-state backup directory")
	out := fs.String("out", "", "brand-new restore root; existing paths are never replaced")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*from) == "" || strings.TrimSpace(*out) == "" {
		return errors.New("--from and --out are required")
	}
	report, err := statebackup.Restore(context.Background(), *from, *out)
	if err != nil {
		return err
	}
	fmt.Printf("state_restore_created=true schema_version=%d files=%d bytes=%d managed_roms=%d managed_media=%d save_blobs=%d recovery_snapshots=%d\n",
		report.SchemaVersion, report.Files, report.Bytes, report.ManagedArtifacts, report.ManagedMedia, report.SaveBlobs, report.RecoverySnapshots)
	return nil
}

func listPlatforms(args []string) error {
	fs := flag.NewFlagSet("platforms", flag.ContinueOnError)
	dbPath := fs.String("db", "", "optional existing SQLite database; includes enabled custom platforms")
	if err := fs.Parse(args); err != nil {
		return err
	}
	items := platforms.All()
	if strings.TrimSpace(*dbPath) != "" {
		store, err := catalog.OpenReadOnly(*dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		registry, err := store.PlatformRegistry(context.Background())
		if err != nil {
			return err
		}
		items = registry.All()
	}
	fmt.Println("ID\tNAME\tTYPE\tRUNTIME\tES-DE")
	for _, item := range items {
		kind := "custom"
		if item.Builtin {
			kind = "builtin"
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", item.ID, item.Name, kind, item.Runtime, strings.Join(item.ESDESystems, ","))
	}
	return nil
}

func scan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	b := addBase(fs)
	source := fs.String("source", "", "directory inside library root")
	platform := fs.String("platform", "", "platform slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" || *platform == "" {
		return fmt.Errorf("--source and --platform are required")
	}
	store, err := open(b)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx := context.Background()
	registry, err := store.PlatformRegistry(ctx)
	if err != nil {
		return err
	}
	r, err := scanner.ScanWithRegistry(ctx, store, b.library, *source, *platform, registry)
	if err == nil {
		fmt.Printf("found=%d imported=%d skipped=%d\n", r.Found, r.Imported, r.Skipped)
	}
	return err
}

func importPegasus(args []string) error {
	fs := flag.NewFlagSet("import-pegasus", flag.ContinueOnError)
	b := addBase(fs)
	source := fs.String("source", "", "metadata.pegasus.txt")
	contentRoot := fs.String("content-root", "", "optional ROM directory inside the library root when metadata is stored separately")
	platform := fs.String("platform", "", "platform slug")
	locale := fs.String("locale", "", "source title locale")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" || *platform == "" {
		return fmt.Errorf("--source and --platform are required")
	}
	store, err := open(b)
	if err != nil {
		return err
	}
	defer store.Close()
	metadataPath := resolveMetadataSource(b.library, *source)
	ctx := context.Background()
	platformID, err := canonicalPlatformWithStore(ctx, store, *platform)
	if err != nil {
		return err
	}
	r, err := importer.ImportPegasusWithContentRoot(ctx, store, b.library, metadataPath, *contentRoot, platformID, *locale)
	if err == nil {
		fmt.Printf("parsed=%d imported=%d skipped=%d\n", r.Parsed, r.Imported, r.Skipped)
	}
	return err
}
func importESDE(args []string) error {
	fs := flag.NewFlagSet("import-esde", flag.ContinueOnError)
	b := addBase(fs)
	source := fs.String("source", "", "gamelist.xml")
	contentRoot := fs.String("content-root", "", "optional ROM directory inside the library root when metadata is stored separately")
	platform := fs.String("platform", "", "platform slug")
	locale := fs.String("locale", "", "source title locale")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" || *platform == "" {
		return fmt.Errorf("--source and --platform are required")
	}
	store, err := open(b)
	if err != nil {
		return err
	}
	defer store.Close()
	metadataPath := resolveMetadataSource(b.library, *source)
	ctx := context.Background()
	registry, err := store.PlatformRegistry(ctx)
	if err != nil {
		return err
	}
	platformID := strings.TrimSpace(*platform)
	if preset, ok := registry.Resolve(platformID); ok {
		platformID = preset.ID
	}
	games, err := importer.PreviewESDEWithContentRootAndRuntimeRegistry(b.library, metadataPath, *contentRoot, "", platformID, *locale, registry)
	if err != nil {
		return err
	}
	r, err := importer.Commit(ctx, store, games)
	if err == nil {
		fmt.Printf("parsed=%d imported=%d skipped=%d\n", r.Parsed, r.Imported, r.Skipped)
	}
	return err
}

func importVarkiv(args []string) error {
	fs := flag.NewFlagSet("import-varkiv", flag.ContinueOnError)
	b := addBase(fs)
	source := fs.String("source", "", "library-manifest.json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*source) == "" {
		return errors.New("--source is required")
	}
	store, err := open(b)
	if err != nil {
		return err
	}
	defer store.Close()
	metadataPath := resolveMetadataSource(b.library, *source)
	r, err := importer.ImportLibraryManifest(context.Background(), store, b.library, metadataPath)
	if err == nil {
		fmt.Printf("parsed=%d imported=%d skipped=%d\n", r.Parsed, r.Imported, r.Skipped)
	}
	return err
}

// resolveMetadataSource preserves the original explicit/CWD-relative CLI
// behavior when that path exists, then falls back to the path users naturally
// express relative to --library. The importer still performs its exact-file,
// size, symlink, and library-boundary checks.
func resolveMetadataSource(libraryRoot, source string) string {
	source = filepath.Clean(filepath.FromSlash(strings.TrimSpace(source)))
	if filepath.IsAbs(source) {
		return source
	}
	if _, err := os.Lstat(source); err == nil {
		return source
	}
	return filepath.Join(libraryRoot, source)
}

func canonicalPlatform(value string) string {
	value = strings.TrimSpace(value)
	if preset, ok := platforms.Resolve(value); ok {
		return preset.ID
	}
	return value
}

func canonicalPlatformWithStore(ctx context.Context, store *catalog.Store, value string) (string, error) {
	value = strings.TrimSpace(value)
	registry, err := store.PlatformRegistry(ctx)
	if err != nil {
		return "", err
	}
	if preset, ok := registry.Resolve(value); ok {
		return preset.ID, nil
	}
	return value, nil
}
func exportPegasus(args []string) error {
	fs := flag.NewFlagSet("export-pegasus", flag.ContinueOnError)
	b := addBase(fs)
	out := fs.String("out", "./export/pegasus", "output root")
	locale := fs.String("locale", "zh-CN", "display locale")
	allowHostPaths := fs.Bool("allow-host-paths", false, "allow metadata-only output to contain local ROM paths; prefer build-pack for portable exports")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*allowHostPaths {
		return errors.New("metadata-only export can expose local ROM paths; use build-pack for a portable relative-path package, or pass --allow-host-paths after reviewing the destination")
	}
	store, err := open(b)
	if err != nil {
		return err
	}
	defer store.Close()
	n, err := exporter.ExportPegasus(context.Background(), store, b.library, *out, *locale)
	if err == nil {
		fmt.Printf("exported_editions=%d output_created=true\n", n)
	}
	return err
}
func exportESDE(args []string) error {
	fs := flag.NewFlagSet("export-esde", flag.ContinueOnError)
	b := addBase(fs)
	out := fs.String("out", "./export/esde", "output root")
	locale := fs.String("locale", "zh-CN", "display locale")
	allowHostPaths := fs.Bool("allow-host-paths", false, "allow metadata-only output to contain local ROM paths; prefer build-pack for portable exports")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*allowHostPaths {
		return errors.New("metadata-only export can expose local ROM paths; use build-pack for a portable relative-path package, or pass --allow-host-paths after reviewing the destination")
	}
	store, err := open(b)
	if err != nil {
		return err
	}
	defer store.Close()
	n, err := exporter.ExportESDE(context.Background(), store, b.library, *out, *locale)
	if err == nil {
		fmt.Printf("exported_artifacts=%d output_created=true\n", n)
	}
	return err
}

func buildPack(args []string) error {
	fs := flag.NewFlagSet("build-pack", flag.ContinueOnError)
	b := addBase(fs)
	out := fs.String("out", "./export/portable", "package output root")
	name := fs.String("name", "portable-library", "profile name")
	frontend := fs.String("frontend", "es-de", "frontend: es-de or pegasus")
	target := fs.String("target", "portable", "target device family")
	locale := fs.String("locale", "zh-CN", "display locale")
	mode := fs.String("mode", "copy", "file mode: copy, hardlink, or reference")
	state := fs.String("state", "", "managed state root containing roms/ and media/ (default: database directory)")
	profileID := fs.String("profile-id", "", "saved PackageProfile ID; uses its device, frontend, locale, file mode, runtime references, and reviewed templates")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := open(b)
	if err != nil {
		return err
	}
	defer store.Close()
	profile := bundler.Profile{Name: *name, Frontend: *frontend, Target: *target, Locale: *locale, FileMode: *mode}
	if strings.TrimSpace(*profileID) != "" {
		conflicting := []string{}
		fs.Visit(func(item *flag.Flag) {
			switch item.Name {
			case "name", "frontend", "target", "locale", "mode":
				conflicting = append(conflicting, "--"+item.Name)
			}
		})
		if len(conflicting) > 0 {
			sort.Strings(conflicting)
			return fmt.Errorf("--profile-id uses the complete saved profile; remove conflicting flags: %s", strings.Join(conflicting, ", "))
		}
		saved, getErr := store.GetPackageProfile(context.Background(), strings.TrimSpace(*profileID))
		if getErr != nil {
			return fmt.Errorf("saved package profile is unavailable: %w", getErr)
		}
		if !saved.Enabled {
			return errors.New("saved package profile is disabled")
		}
		profile = bundler.ProfileFromCatalog(saved)
	}
	if strings.TrimSpace(*state) == "" {
		*state = filepath.Dir(b.db)
	}
	result, err := bundler.BuildWithStorage(context.Background(), store, b.library, filepath.Join(*state, "roms"), filepath.Join(*state, "media"), *out, profile)
	if err == nil {
		fmt.Print(formatBuildPackResult(result))
	}
	return err
}

func formatBuildPackResult(result bundler.Result) string {
	return fmt.Sprintf("exported=%d copied=%d linked=%d unchanged=%d missing=%d warnings=%d output_created=true recovery_snapshot=%t\n",
		result.Exported, result.Copied, result.Linked, result.Unchanged, result.Missing, len(result.Warnings), result.RecoverySnapshot != "")
}

type runtimeHintView struct {
	ID                string   `json:"id"`
	EditionID         string   `json:"edition_id"`
	SourceKind        string   `json:"source_kind"`
	SourceFormat      string   `json:"source_format"`
	DeviceProfileID   string   `json:"device_profile_id,omitempty"`
	FrontendAdapterID string   `json:"frontend_adapter_id,omitempty"`
	DriverID          string   `json:"driver_id,omitempty"`
	CoreID            string   `json:"core_id,omitempty"`
	Arguments         []string `json:"arguments"`
	Trust             string   `json:"trust"`
	Status            string   `json:"status"`
}

func publicRuntimeHint(item catalog.RuntimeImportHint) runtimeHintView {
	return runtimeHintView{
		ID: item.ID, EditionID: item.EditionID, SourceKind: item.SourceKind, SourceFormat: item.SourceFormat,
		DeviceProfileID: item.DeviceProfileID, FrontendAdapterID: item.FrontendAdapterID, DriverID: item.DriverID,
		CoreID: item.CoreID, Arguments: append([]string{}, item.Arguments...), Trust: item.Trust, Status: item.Status,
	}
}

func runtimeHintCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("runtime-hints requires list or apply")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("runtime-hints list", flag.ContinueOnError)
		dbPath := fs.String("db", "", "existing SQLite database")
		editionID := fs.String("edition", "", "optional Edition ID")
		status := fs.String("status", "", "optional pending, applied, or dismissed status")
		asJSON := fs.Bool("json", false, "emit a privacy-minimized JSON array")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		store, err := openExistingDatabaseReadOnly(*dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		items, err := store.ListRuntimeImportHints(context.Background(), *editionID, *status)
		if err != nil {
			return err
		}
		views := make([]runtimeHintView, len(items))
		for index, item := range items {
			views[index] = publicRuntimeHint(item)
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(views)
		}
		fmt.Println("ID\tEDITION\tSOURCE\tTRUST\tSTATUS\tDEVICE\tDRIVER\tCORE")
		for _, item := range views {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", item.ID, item.EditionID, item.SourceFormat, item.Trust, item.Status, item.DeviceProfileID, item.DriverID, item.CoreID)
		}
		return nil
	case "apply":
		fs := flag.NewFlagSet("runtime-hints apply", flag.ContinueOnError)
		dbPath := fs.String("db", "", "existing SQLite database")
		id := fs.String("id", "", "pending structured runtime hint ID")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*id) == "" {
			return errors.New("--id is required")
		}
		store, err := openExistingDatabase(*dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		hint, err := store.GetRuntimeImportHint(context.Background(), *id)
		if err != nil {
			return err
		}
		if hint.Status != "pending" {
			return errors.New("runtime hint is not pending")
		}
		if hint.Trust != "structured" || hint.SourceKind != "structured-sidecar" {
			return errors.New("CLI apply accepts only structured sidecar hints; review untrusted frontend commands in the Web interface")
		}
		binding, err := store.ApplyRuntimeImportHint(context.Background(), hint.ID, catalog.NewLaunchBinding{})
		if err != nil {
			return err
		}
		fmt.Printf("launch_binding_created=true hint_id=%s binding_id=%s edition_id=%s device_profile_id=%s driver_id=%s core_id=%s\n", hint.ID, binding.ID, binding.EditionID, binding.DeviceProfileID, binding.DriverID, binding.CoreID)
		return nil
	default:
		return errors.New("runtime-hints requires list or apply")
	}
}
