package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/saves"
)

const (
	webEmulationSessionLifetime = 4 * time.Hour
	webEmulationMaxROMBytes     = 128 << 20
	webEmulationMaxSaveBytes    = 32 << 20
	webEmulationDeviceID        = "varkiv-web-player"
)

var webEmulationCores = map[string]string{
	"2600":         "stella2014",
	"gb":           "gambatte",
	"gba":          "mgba",
	"gbc":          "gambatte",
	"gamegear":     "genesis_plus_gx",
	"mastersystem": "smsplus",
	"megadrive":    "genesis_plus_gx",
	"n64":          "mupen64plus_next",
	"nes":          "fceumm",
	"ngpc":         "mednafen_ngp",
	"snes":         "snes9x",
}

func webEmulationDriverID(core string) string {
	return "builtin-driver-emulatorjs-" + strings.ReplaceAll(core, "_", "-")
}

var webEmulationPlatformExtensions = map[string]map[string]bool{
	"2600":         {".a26": true, ".bin": true},
	"gb":           {".gb": true},
	"gba":          {".gba": true},
	"gbc":          {".gbc": true},
	"gamegear":     {".bin": true, ".gg": true},
	"mastersystem": {".bin": true, ".sms": true},
	"megadrive":    {".bin": true, ".gen": true, ".md": true, ".smd": true},
	"n64":          {".n64": true, ".v64": true, ".z64": true},
	"nes":          {".nes": true, ".unf": true, ".unif": true},
	"ngpc":         {".ngc": true, ".ngp": true, ".ngpc": true, ".npc": true, ".zip": true},
	"snes":         {".bs": true, ".fig": true, ".sfc": true, ".smc": true},
}

// These are structural lower bounds, not authenticity checks. They keep
// metadata/demo placeholders from being advertised as playable ROMs while
// preserving small legitimate homebrew used by the browser acceptance suite.
var webEmulationPlatformMinimumBytes = map[string]int64{
	"2600":         2048,
	"gb":           32768,
	"gba":          0xc0,
	"gbc":          32768,
	"gamegear":     8192,
	"mastersystem": 8192,
	"megadrive":    0x200,
	"n64":          4096,
	"nes":          64,
	"ngpc":         64,
	"snes":         32768,
}

var nintendoGameBoyLogo = [48]byte{
	0xce, 0xed, 0x66, 0x66, 0xcc, 0x0d, 0x00, 0x0b,
	0x03, 0x73, 0x00, 0x83, 0x00, 0x0c, 0x00, 0x0d,
	0x00, 0x08, 0x11, 0x1f, 0x88, 0x89, 0x00, 0x0e,
	0xdc, 0xcc, 0x6e, 0xe6, 0xdd, 0xdd, 0xd9, 0x99,
	0xbb, 0xbb, 0x67, 0x63, 0x6e, 0x0e, 0xec, 0xcc,
	0xdd, 0xdc, 0x99, 0x9f, 0xbb, 0xb9, 0x33, 0x3e,
}

func webEmulationArtifactSupported(platform, artifactPath string) bool {
	return webEmulationPlatformExtensions[platform][strings.ToLower(filepath.Ext(artifactPath))]
}

func webEmulationArtifactPlausible(platform, artifactPath string, size int64) bool {
	minimum := webEmulationPlatformMinimumBytes[platform]
	return minimum > 0 && size >= minimum && size <= webEmulationMaxROMBytes && webEmulationArtifactSupported(platform, artifactPath)
}

func validateSingleROMZIP(platform string, file *os.File) error {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > webEmulationMaxROMBytes {
		return errors.New("ROM archive is unavailable")
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil || len(reader.File) != 1 {
		return errors.New("ROM archive must contain exactly one file")
	}
	entry := reader.File[0]
	extension := strings.ToLower(filepath.Ext(entry.Name))
	minimum := webEmulationPlatformMinimumBytes[platform]
	if entry.Name == "" || strings.ContainsAny(entry.Name, "/\\") || entry.FileInfo().IsDir() || !entry.FileInfo().Mode().IsRegular() {
		return errors.New("ROM archive entry is unsafe")
	}
	if entry.Flags&0x1 != 0 || (entry.Method != zip.Store && entry.Method != zip.Deflate) {
		return errors.New("ROM archive compression is unsupported")
	}
	if extension == ".zip" || !webEmulationPlatformExtensions[platform][extension] {
		return errors.New("ROM archive entry is not supported for the selected platform")
	}
	if minimum <= 0 || entry.UncompressedSize64 < uint64(minimum) || entry.UncompressedSize64 > uint64(webEmulationMaxROMBytes) {
		return errors.New("ROM archive entry size is invalid")
	}
	return nil
}

func validateWebROMHeader(platform, artifactPath string, file *os.File) error {
	if file == nil {
		return errors.New("ROM file is unavailable")
	}
	defer func() { _, _ = file.Seek(0, io.SeekStart) }()
	if strings.EqualFold(filepath.Ext(artifactPath), ".zip") {
		return validateSingleROMZIP(platform, file)
	}
	header := make([]byte, 0x150)
	read, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return errors.New("ROM header could not be read")
	}
	header = header[:read]
	has := func(offset, length int) bool { return offset >= 0 && length >= 0 && len(header) >= offset+length }
	extension := strings.ToLower(filepath.Ext(artifactPath))
	switch platform {
	case "nes":
		if extension == ".nes" {
			if !has(0, 4) || !bytes.Equal(header[:4], []byte{'N', 'E', 'S', 0x1a}) {
				return errors.New("NES cartridge header is invalid")
			}
		} else if !has(0, 4) || !bytes.Equal(header[:4], []byte("UNIF")) {
			return errors.New("UNIF cartridge header is invalid")
		}
	case "gb", "gbc":
		if !has(0x104, len(nintendoGameBoyLogo)) || !bytes.Equal(header[0x104:0x104+len(nintendoGameBoyLogo)], nintendoGameBoyLogo[:]) || !has(0x134, 0x1a) {
			return errors.New("Game Boy cartridge header is invalid")
		}
		checksum := byte(0)
		for _, value := range header[0x134:0x14d] {
			checksum = checksum - value - 1
		}
		if checksum != header[0x14d] {
			return errors.New("Game Boy cartridge header checksum is invalid")
		}
	case "gba":
		if !has(0xa0, 0x1e) || header[0xb2] != 0x96 {
			return errors.New("Game Boy Advance cartridge header is invalid")
		}
		checksum := byte(0)
		for _, value := range header[0xa0:0xbd] {
			checksum -= value
		}
		checksum -= 0x19
		if checksum != header[0xbd] {
			return errors.New("Game Boy Advance cartridge header checksum is invalid")
		}
	case "n64":
		if !has(0, 4) {
			return errors.New("Nintendo 64 cartridge header is invalid")
		}
		magic := [4]byte{header[0], header[1], header[2], header[3]}
		if magic != [4]byte{0x80, 0x37, 0x12, 0x40} && magic != [4]byte{0x37, 0x80, 0x40, 0x12} && magic != [4]byte{0x40, 0x12, 0x37, 0x80} {
			return errors.New("Nintendo 64 cartridge byte order is invalid")
		}
	}
	return nil
}

type webEmulationSessionInput struct {
	EditionID string `json:"edition_id"`
	Locale    string `json:"locale,omitempty"`
}

type webEmulationStatus struct {
	Available      bool     `json:"available"`
	Reason         string   `json:"reason,omitempty"`
	PlatformID     string   `json:"platform_id"`
	Core           string   `json:"core,omitempty"`
	DriverID       string   `json:"driver_id,omitempty"`
	EditionID      string   `json:"edition_id"`
	ArtifactID     string   `json:"artifact_id,omitempty"`
	ROMSize        int64    `json:"rom_size,omitempty"`
	SaveSupport    string   `json:"save_support"`
	InputSupport   []string `json:"input_support"`
	GamepadMapping string   `json:"gamepad_mapping"`
}

type webEmulationSession struct {
	Status      webEmulationStatus `json:"status"`
	PlayerURL   string             `json:"player_url"`
	ExpiresAt   time.Time          `json:"expires_at"`
	AssetSource string             `json:"asset_source"`
}

type webEmulationToken struct {
	ArtifactID       string `json:"artifact_id"`
	EditionID        string `json:"edition_id"`
	SHA256           string `json:"sha256"`
	Core             string `json:"core,omitempty"`
	DriverID         string `json:"driver_id,omitempty"`
	StreamID         string `json:"stream_id,omitempty"`
	BaseID           string `json:"base_revision_id,omitempty"`
	Locale           string `json:"locale"`
	ExpiresAt        int64  `json:"expires_at"`
	Nonce            string `json:"nonce"`
	NetplaySessionID string `json:"netplay_session_id,omitempty"`
	NetplayRole      string `json:"netplay_role,omitempty"`
	NetplayRoom      string `json:"netplay_room,omitempty"`
	NetplayPassword  string `json:"netplay_password,omitempty"`
	NetplayPlayer    string `json:"netplay_player,omitempty"`
}

var webPlayerTemplate = template.Must(template.New("web-player").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
  <title>{{.Title}}</title>
  <style nonce="{{.Nonce}}">
    *{box-sizing:border-box}html,body,#game{width:100%;height:100%;margin:0;background:#07080b;color:#f4f3f8}body{overflow:hidden;font-family:Inter,"PingFang SC",system-ui,sans-serif}.boot{position:fixed;inset:0;display:grid;place-items:center;z-index:0;background:radial-gradient(circle at 70% 15%,#7465db22,transparent 34%),#07080b}.boot span,.sync{padding:10px 14px;border:1px solid #343944;background:#111318;box-shadow:4px 4px 0 #050609;font-size:14px}.sync{position:fixed;right:14px;top:14px;z-index:3;padding:7px 10px;opacity:.9}.sync[data-error="true"]{border-color:#d95858;color:#ffd0d0}.ejs_parent{position:relative;z-index:1}
  </style>
</head>
<body data-runtime-state="loading">
  <div class="boot"><span>{{.Loading}}</span></div>
  <div class="sync" role="status" hidden></div>
  <div id="game"></div>
  <script nonce="{{.Nonce}}">
  (()=>{
    const varkivPlayerConfig={{.Config}};
    const status=document.querySelector('.sync');
    let baseRevision=varkivPlayerConfig.baseRevision||'';
    let started=false;
    let watchdog;
    let uploadQueue=Promise.resolve();
    let restoreQueue=null;
    let lastRestoreAt=0;
    let lastInputState='';
	let netplayWatchdog;
    const publishRuntimeState=value=>{if(window.parent!==window)window.parent.postMessage({type:'varkiv:web-player-state',state:value},location.origin)};
	const publishNetplayState=(value,players=0)=>{document.body.dataset.netplayState=value;if(window.parent!==window)window.parent.postMessage({type:'varkiv:web-netplay-state',state:value,players},location.origin)};
    const publishInputState=()=>{
      let pads=[];
      const supported=typeof navigator.getGamepads==='function';
      if(supported){try{pads=Array.from(navigator.getGamepads()||[]).filter(Boolean)}catch(_){pads=[]}}
      const value={supported,count:pads.length,standard:pads.filter(pad=>pad.mapping==='standard').length,touch:matchMedia('(pointer: coarse)').matches};
      const signature=JSON.stringify(value);
      if(signature===lastInputState)return;
      lastInputState=signature;
      if(window.parent!==window)window.parent.postMessage({type:'varkiv:web-player-input',...value},location.origin);
    };
    const setRuntimeState=value=>{document.body.dataset.runtimeState=value;publishRuntimeState(value)};
    const showStatus=(message,error=false)=>{status.textContent=message;status.dataset.error=String(error);status.hidden=!message};
    const saveBytes=value=>value&&value.save!==undefined?value.save:value;
    const uploadSave=value=>{
	  if(varkivPlayerConfig.netplay)return;
      const bytes=saveBytes(value);
      if(!bytes||!bytes.byteLength)return;
      uploadQueue=uploadQueue.then(async()=>{
        const response=await fetch(varkivPlayerConfig.saveUrl,{method:'POST',headers:{'Content-Type':'application/octet-stream','X-Varkiv-Base-Revision':baseRevision},body:bytes});
        if(!response.ok)throw new Error('save upload failed');
        const result=await response.json();
        baseRevision=result.revision.id;
        showStatus(varkivPlayerConfig.labels.saved);
        setTimeout(()=>started&&showStatus(''),1800);
      }).catch(()=>showStatus(varkivPlayerConfig.labels.saveError,true));
    };
    const restoreSave=()=>{
	  if(varkivPlayerConfig.netplay)return Promise.resolve();
      if(restoreQueue)return restoreQueue;
      if(Date.now()-lastRestoreAt<1000)return Promise.resolve();
      restoreQueue=(async()=>{
        const response=await fetch(varkivPlayerConfig.saveUrl,{cache:'no-store'});
        if(response.status===204){lastRestoreAt=Date.now();return}
        if(!response.ok)throw new Error('save restore failed');
        const bytes=new Uint8Array(await response.arrayBuffer());
        const manager=window.EJS_emulator&&window.EJS_emulator.gameManager;
        if(!manager||!bytes.byteLength)return;
        manager.writeFile(manager.getSaveFilePath(),bytes);
        manager.loadSaveFiles();
        lastRestoreAt=Date.now();
        baseRevision=response.headers.get('X-Varkiv-Revision')||baseRevision;
        showStatus(varkivPlayerConfig.labels.restored);
        setTimeout(()=>started&&showStatus(''),2200);
      })().finally(()=>{restoreQueue=null});
      return restoreQueue;
    };
    window.EJS_ready=()=>{
      const emulator=window.EJS_emulator;
      setRuntimeState('ready');
      emulator.on('start-clicked',()=>{setRuntimeState('starting');watchdog=setTimeout(()=>{if(!started){setRuntimeState('timeout');showStatus(varkivPlayerConfig.labels.timeout,true)}},75000)});
      emulator.on('saveSaveFiles',uploadSave);
    };
	const startNetplay=async()=>{
	  if(!varkivPlayerConfig.netplay)return;
	  publishNetplayState(varkivPlayerConfig.netplay.role==='host'?'opening':'finding');
	  const deadline=Date.now()+30000;
	  while((!window.EJS_emulator||!window.EJS_emulator.netplay)&&Date.now()<deadline)await new Promise(resolve=>setTimeout(resolve,100));
	  const netplay=window.EJS_emulator&&window.EJS_emulator.netplay;
	  if(!netplay)throw new Error('netplay runtime unavailable');
	  if(!netplay.isMenuCreated())netplay.createNetplayMenu();
	  netplay.name=varkivPlayerConfig.netplay.player;
	  netplay.openMenu();
	  if(netplay._menuElement)netplay._menuElement.style.display='none';
	  if(varkivPlayerConfig.netplay.role==='host'){
	    netplay.openRoom(varkivPlayerConfig.netplay.room,2,varkivPlayerConfig.netplay.password);
	  }else{
	    let room;
	    while(!room&&Date.now()<deadline){
	      const rooms=await netplay.getOpenRooms();
	      for(const [id,value] of Object.entries(rooms||{}))if(value.room_name===varkivPlayerConfig.netplay.room)room={id,...value};
	      if(!room)await new Promise(resolve=>setTimeout(resolve,250));
	    }
	    if(!room)throw new Error('netplay room unavailable');
	    netplay.joinRoom(room.id,room.room_name,room.max,varkivPlayerConfig.netplay.password);
	  }
	  clearInterval(netplayWatchdog);
	  netplayWatchdog=setInterval(()=>{
	    const players=Object.keys(netplay.players||{}).length;
	    if(netplay.emu.isNetplay&&players>1&&netplay.webRtcReady)publishNetplayState('connected',players);
	    else if(netplay.emu.isNetplay)publishNetplayState(players>1?'negotiating':'waiting',players);
	  },250);
	};
	window.EJS_onGameStart=()=>{started=true;setRuntimeState('started');clearTimeout(watchdog);showStatus('');restoreSave().catch(()=>showStatus(varkivPlayerConfig.labels.saveError,true));startNetplay().catch(()=>publishNetplayState('error'));window.dispatchEvent(new CustomEvent('varkiv:player-started'))};
    window.EJS_onSaveSave=uploadSave;
    window.EJS_onLoadSave=()=>restoreSave().catch(()=>showStatus(varkivPlayerConfig.labels.saveError,true));
    addEventListener('gamepadconnected',publishInputState);
    addEventListener('gamepaddisconnected',publishInputState);
    document.addEventListener('visibilitychange',()=>{if(!document.hidden)publishInputState()});
    setInterval(()=>{if(!document.hidden)publishInputState()},1000);
	for(const [key,value] of Object.entries(varkivPlayerConfig.options)) window[key]=key==='EJS_netplayServer'&&value==='same-origin'?location.origin:value;
    publishRuntimeState('loading');
    publishInputState();
    const loader=document.createElement('script');
    loader.src=varkivPlayerConfig.loader;
    loader.crossOrigin='anonymous';
    loader.onerror=()=>{setRuntimeState('error');document.querySelector('.boot span').textContent=varkivPlayerConfig.labels.loadError};
    document.head.appendChild(loader);
  })();
  </script>
</body>
</html>`))

func validateWebEmulatorAssets(value string) error {
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("web emulator assets must be an absolute HTTP(S) directory URL or a same-origin absolute path")
	}
	return nil
}

func (s *Server) webEmulatorAsset(w http.ResponseWriter, r *http.Request) {
	relative := r.PathValue("path")
	if relative == "" || relative == "." || !fs.ValidPath(relative) || strings.Contains(relative, "\\") {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(s.webEmulatorDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	asset, found := webEmulatorManifestAsset(s.webEmulatorManifest, relative)
	if !found {
		http.NotFound(w, r)
		return
	}
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

func webAssetDirectory(value string) string {
	return strings.TrimRight(value, "/") + "/"
}

func webAssetSource(value string) string {
	if strings.HasPrefix(value, "/") {
		return "self-hosted"
	}
	return "external"
}

func webEmulationLocale(value string) string {
	switch value {
	case "zh-CN", "zh-TW", "ja", "en":
		return value
	default:
		return "en"
	}
}

func (s *Server) ensureWebEmulationDevice(ctx context.Context) error {
	device, err := s.store.GetDevice(ctx, webEmulationDeviceID)
	if err == nil {
		if device.OSFamily != "web" || device.Status != "active" {
			return errors.New("reserved browser player device identity is unavailable")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	device, err = s.store.CreateDevice(ctx, catalog.NewDevice{
		ID: webEmulationDeviceID, Name: "Varkiv 网页模拟器", OSFamily: "web", Distribution: "browser", Architecture: "wasm",
		AgentVersion: "emulatorjs-4.2.3", Capabilities: map[string]bool{"automatic_saves": true, "core_isolated_saves": true},
	})
	if err != nil {
		if current, getErr := s.store.GetDevice(ctx, webEmulationDeviceID); getErr == nil && current.OSFamily == "web" && current.Status == "active" {
			return nil
		}
		return err
	}
	return nil
}

func (s *Server) webEmulationSaveStream(ctx context.Context, editionID, driverID string) (catalog.SaveStream, error) {
	stream, err := s.store.ResolveSaveStream(ctx, editionID, driverID, "game", "")
	if err == nil {
		return stream, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return catalog.SaveStream{}, err
	}
	compatibilityGroupID := ""
	if driverID == "builtin-driver-emulatorjs-snes9x" {
		compatibilityGroupID = snesRawSRMCompatibilityGroupID
	}
	stream, err = s.store.CreateSaveStream(ctx, catalog.NewSaveStream{
		OwnerType: "edition", OwnerKey: editionID, DriverID: driverID, Portability: "core-dependent",
		CompatibilityGroupID: compatibilityGroupID, EditionIDs: []string{editionID}, Compatibility: "native",
	})
	if err == nil {
		return stream, nil
	}
	// A concurrent session may have created the unique stream first.
	if resolved, resolveErr := s.store.ResolveSaveStream(ctx, editionID, driverID, "game", ""); resolveErr == nil {
		return resolved, nil
	}
	return catalog.SaveStream{}, err
}

func (s *Server) webEmulationEdition(r *http.Request, editionID string) (catalog.Game, catalog.Edition, webEmulationStatus, error) {
	edition, err := s.store.GetEdition(r.Context(), editionID, webEmulationLocale(r.URL.Query().Get("locale")))
	if err != nil {
		return catalog.Game{}, catalog.Edition{}, webEmulationStatus{}, err
	}
	game, err := s.store.GetGame(r.Context(), edition.GameID, webEmulationLocale(r.URL.Query().Get("locale")))
	if err != nil {
		return catalog.Game{}, catalog.Edition{}, webEmulationStatus{}, err
	}
	core := webEmulationCores[game.Platform]
	status := webEmulationStatus{PlatformID: game.Platform, Core: core, DriverID: webEmulationDriverID(core), EditionID: edition.ID, SaveSupport: "automatic-when-core-emits", InputSupport: []string{"keyboard", "gamepad", "touch"}, GamepadMapping: "user-configurable"}
	if s.webEmulatorAssets == "" {
		status.Reason = "not_configured"
		return game, edition, status, nil
	}
	if core == "" {
		status.Reason = "platform_not_supported"
		return game, edition, status, nil
	}
	artifact := catalog.SelectLaunchArtifact(edition.Artifacts)
	if artifact == nil || artifact.SHA256 == "" {
		status.Reason = "rom_unavailable"
		return game, edition, status, nil
	}
	status.ArtifactID, status.ROMSize = artifact.ID, artifact.Size
	if !webEmulationArtifactSupported(game.Platform, artifact.Path) {
		status.Reason = "artifact_not_supported"
		return game, edition, status, nil
	}
	if !webEmulationArtifactPlausible(game.Platform, artifact.Path, artifact.Size) {
		status.Reason = "rom_format_invalid"
		return game, edition, status, nil
	}
	status.Available = true
	return game, edition, status, nil
}

func (s *Server) webEmulationEditionStatus(w http.ResponseWriter, r *http.Request) {
	_, _, status, err := s.webEmulationEdition(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) createWebEmulationSession(w http.ResponseWriter, r *http.Request) {
	var input webEmulationSessionInput
	if !decode(w, r, &input) {
		return
	}
	input.EditionID = strings.TrimSpace(input.EditionID)
	input.Locale = webEmulationLocale(input.Locale)
	if input.EditionID == "" {
		writeAPIError(w, http.StatusBadRequest, "edition_id_required", "edition_id is required")
		return
	}
	request := r.Clone(r.Context())
	query := request.URL.Query()
	query.Set("locale", input.Locale)
	request.URL.RawQuery = query.Encode()
	game, edition, status, err := s.webEmulationEdition(request, input.EditionID)
	if err != nil {
		writeError(w, err)
		return
	}
	if !status.Available {
		code := "web_emulation_unavailable"
		if status.Reason == "rom_format_invalid" {
			code = "web_emulation_rom_invalid"
		}
		writeAPIError(w, http.StatusConflict, code, status.Reason)
		return
	}
	artifact := catalog.SelectLaunchArtifact(edition.Artifacts)
	file, err := s.openVerifiedWebROM(*artifact)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "web_emulation_rom_changed", "ROM content is unavailable or no longer matches its catalog identity")
		return
	}
	if err = validateWebROMHeader(game.Platform, artifact.Path, file); err != nil {
		_ = file.Close()
		writeAPIError(w, http.StatusConflict, "web_emulation_rom_invalid", "ROM header is not valid for the selected platform")
		return
	}
	_ = file.Close()
	stream, err := s.webEmulationSaveStream(r.Context(), edition.ID, status.DriverID)
	if err != nil {
		writeError(w, err)
		return
	}
	baseID := ""
	if current, currentErr := s.store.CurrentStreamRevision(r.Context(), stream.ID); currentErr == nil {
		baseID = current.ID
	} else if !errors.Is(currentErr, sql.ErrNoRows) {
		writeError(w, currentErr)
		return
	}
	expiresAt := time.Now().UTC().Add(webEmulationSessionLifetime)
	token, err := s.signWebEmulationToken(webEmulationToken{
		ArtifactID: artifact.ID, EditionID: edition.ID, SHA256: artifact.SHA256, Locale: input.Locale,
		Core: status.Core, DriverID: status.DriverID, StreamID: stream.ID, BaseID: baseID, ExpiresAt: expiresAt.Unix(),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, webEmulationSession{Status: status, PlayerURL: "/play/" + token, ExpiresAt: expiresAt, AssetSource: webAssetSource(s.webEmulatorAssets)})
}

func (s *Server) signWebEmulationToken(payload webEmulationToken) (string, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	payload.Nonce = base64.RawURLEncoding.EncodeToString(nonce[:])
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, s.importKey[:])
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Server) parseWebEmulationToken(value string) (webEmulationToken, error) {
	encoded, signature, ok := strings.Cut(value, ".")
	if !ok || encoded == "" || signature == "" {
		return webEmulationToken{}, errors.New("invalid web emulation token")
	}
	want, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return webEmulationToken{}, errors.New("invalid web emulation token")
	}
	mac := hmac.New(sha256.New, s.importKey[:])
	_, _ = mac.Write([]byte(encoded))
	if !hmac.Equal(want, mac.Sum(nil)) {
		return webEmulationToken{}, errors.New("invalid web emulation token")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return webEmulationToken{}, errors.New("invalid web emulation token")
	}
	var payload webEmulationToken
	if json.Unmarshal(data, &payload) != nil || payload.ArtifactID == "" || payload.EditionID == "" || payload.SHA256 == "" || payload.Nonce == "" || time.Now().Unix() > payload.ExpiresAt {
		return webEmulationToken{}, errors.New("invalid or expired web emulation token")
	}
	return payload, nil
}

func (s *Server) openVerifiedWebROM(artifact catalog.Artifact) (*os.File, error) {
	if artifact.Missing || artifact.SHA256 == "" || artifact.Size <= 0 || artifact.Size > webEmulationMaxROMBytes {
		return nil, errors.New("ROM is not eligible for browser streaming")
	}
	path, err := s.storage.ResolveArtifact(artifact)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	closeWith := func(err error) (*os.File, error) {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Size {
		return closeWith(errors.New("ROM file identity changed"))
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.SHA256) {
		return closeWith(errors.New("ROM file identity changed"))
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return closeWith(err)
	}
	return file, nil
}

func (s *Server) webEmulationContent(w http.ResponseWriter, r *http.Request) {
	payload, err := s.parseWebEmulationToken(r.PathValue("token"))
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "web_emulation_token_invalid", "web emulation session is invalid or expired")
		return
	}
	artifact, err := s.store.GetArtifact(r.Context(), payload.ArtifactID)
	if err != nil || artifact.EditionID != payload.EditionID || !strings.EqualFold(artifact.SHA256, payload.SHA256) {
		writeAPIError(w, http.StatusConflict, "web_emulation_rom_changed", "ROM identity changed after the session was created")
		return
	}
	file, err := s.openVerifiedWebROM(artifact)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "web_emulation_rom_changed", "ROM content is unavailable or no longer matches its catalog identity")
		return
	}
	defer file.Close()
	name := filepath.Base(filepath.FromSlash(artifact.OriginalName))
	if name == "." || name == "" {
		name = filepath.Base(filepath.FromSlash(artifact.Path))
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%s", strconv.Quote(name)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", `"sha256-`+artifact.SHA256+`"`)
	http.ServeContent(w, r, name, time.Time{}, file)
}

func (s *Server) validateWebEmulationSaveToken(ctx context.Context, payload webEmulationToken) (catalog.SaveStream, error) {
	if payload.Core == "" || payload.DriverID == "" || payload.StreamID == "" || payload.DriverID != webEmulationDriverID(payload.Core) {
		return catalog.SaveStream{}, errors.New("web save capability is incomplete")
	}
	edition, err := s.store.GetEdition(ctx, payload.EditionID, payload.Locale)
	if err != nil {
		return catalog.SaveStream{}, err
	}
	game, err := s.store.GetGame(ctx, edition.GameID, payload.Locale)
	if err != nil || webEmulationCores[game.Platform] != payload.Core {
		return catalog.SaveStream{}, errors.New("web save core binding changed")
	}
	stream, err := s.store.GetSaveStream(ctx, payload.StreamID)
	if err != nil || stream.OwnerType != "edition" || stream.OwnerKey != payload.EditionID || stream.DriverID != payload.DriverID {
		return catalog.SaveStream{}, errors.New("web save stream binding changed")
	}
	linked := false
	for _, relation := range stream.Editions {
		linked = linked || relation.EditionID == payload.EditionID
	}
	if !linked {
		return catalog.SaveStream{}, errors.New("web save stream is not linked to this edition")
	}
	return stream, nil
}

func (s *Server) webEmulationSave(w http.ResponseWriter, r *http.Request) {
	payload, err := s.parseWebEmulationToken(r.PathValue("token"))
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "web_emulation_token_invalid", "web emulation session is invalid or expired")
		return
	}
	stream, err := s.validateWebEmulationSaveToken(r.Context(), payload)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "web_emulation_save_binding_changed", "browser save binding changed after the session was created")
		return
	}
	if r.Method == http.MethodGet {
		revision, currentErr := s.store.CurrentStreamRevision(r.Context(), stream.ID)
		if errors.Is(currentErr, sql.ErrNoRows) {
			w.Header().Set("Cache-Control", "private, no-store")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if currentErr != nil || len(revision.Files) != 1 {
			if currentErr == nil {
				currentErr = errors.New("browser save revision must contain exactly one file")
			}
			writeError(w, currentErr)
			return
		}
		file, metadata, openErr := s.saves.OpenRevisionFile(r.Context(), revision.ID, revision.Files[0].ID)
		if openErr != nil {
			writeError(w, openErr)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(metadata.Size, 10))
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("ETag", `"`+metadata.Checksum+`"`)
		w.Header().Set("X-Varkiv-Revision", revision.ID)
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, file)
		return
	}
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "web_emulation_save_type_invalid", "browser save upload must use application/octet-stream")
		return
	}
	if err = s.ensureWebEmulationDevice(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, webEmulationMaxSaveBytes)
	baseID := strings.TrimSpace(r.Header.Get("X-Varkiv-Base-Revision"))
	if baseID == "" {
		baseID = payload.BaseID
	}
	result, err := s.saves.PushSet(r.Context(), saves.PushSetInput{
		EditionID: payload.EditionID, DeviceID: webEmulationDeviceID, DriverID: payload.DriverID,
		ScopeType: "game", BaseRevisionID: baseID,
		Files: []saves.IncomingFile{{LogicalPath: "battery.sav", Reader: r.Body, Mode: 0o600}},
	})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "save-revisions", result.Revision.ID))
	writeJSON(w, http.StatusCreated, result)
}

func webPlayerLabels(locale string) map[string]string {
	switch locale {
	case "zh-CN":
		return map[string]string{"loading": "正在准备网页模拟器…", "loadError": "模拟器资源加载失败，请检查资源目录。", "timeout": "模拟核心启动超时，请退出后重试。", "restored": "已恢复云端存档", "saved": "存档已同步", "saveError": "存档同步失败，游戏仍可继续"}
	case "zh-TW":
		return map[string]string{"loading": "正在準備網頁模擬器…", "loadError": "模擬器資源載入失敗，請檢查資源目錄。", "timeout": "模擬核心啟動逾時，請退出後重試。", "restored": "已還原雲端存檔", "saved": "存檔已同步", "saveError": "存檔同步失敗，遊戲仍可繼續"}
	case "ja":
		return map[string]string{"loading": "ブラウザーエミュレーターを準備中…", "loadError": "エミュレーター資産を読み込めません。資産ディレクトリを確認してください。", "timeout": "コアの起動がタイムアウトしました。終了して再試行してください。", "restored": "セーブデータを復元しました", "saved": "セーブデータを同期しました", "saveError": "セーブデータを同期できません。ゲームは続行できます"}
	default:
		return map[string]string{"loading": "Preparing browser player…", "loadError": "Emulator assets could not be loaded. Check the configured asset directory.", "timeout": "The emulator core timed out. Exit and try again.", "restored": "Save restored", "saved": "Save synced", "saveError": "Save sync failed; play can continue"}
	}
}

func (s *Server) webEmulationPlayer(w http.ResponseWriter, r *http.Request) {
	payload, err := s.parseWebEmulationToken(r.PathValue("token"))
	if err != nil {
		http.Error(w, "This browser-play session is invalid or expired.", http.StatusUnauthorized)
		return
	}
	edition, err := s.store.GetEdition(r.Context(), payload.EditionID, payload.Locale)
	if err != nil {
		http.Error(w, "This game edition is no longer available.", http.StatusNotFound)
		return
	}
	artifact, err := s.store.GetArtifact(r.Context(), payload.ArtifactID)
	if err != nil || artifact.EditionID != edition.ID || !strings.EqualFold(artifact.SHA256, payload.SHA256) {
		http.Error(w, "The ROM identity changed after this session was created.", http.StatusConflict)
		return
	}
	game, err := s.store.GetGame(r.Context(), edition.GameID, payload.Locale)
	if err != nil || webEmulationCores[game.Platform] == "" || payload.Core != webEmulationCores[game.Platform] {
		http.Error(w, "This platform is not available in the browser player.", http.StatusConflict)
		return
	}
	if _, err = s.validateWebEmulationSaveToken(r.Context(), payload); err != nil {
		http.Error(w, "This browser save binding is no longer available.", http.StatusConflict)
		return
	}
	var nonce [18]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		http.Error(w, "Unable to create player.", http.StatusInternalServerError)
		return
	}
	nonceText := base64.RawURLEncoding.EncodeToString(nonce[:])
	assets := webAssetDirectory(s.webEmulatorAssets)
	loader := assets + "loader.js"
	romName := url.PathEscape(filepath.Base(filepath.FromSlash(artifact.OriginalName)))
	if romName == "." || romName == "" {
		romName = url.PathEscape(filepath.Base(filepath.FromSlash(artifact.Path)))
	}
	gameID, _ := strconv.ParseUint(artifact.SHA256[:12], 16, 64)
	options := map[string]any{
		"EJS_player": "#game", "EJS_core": payload.Core,
		"EJS_gameUrl":    "/api/v1/web-emulation/content/" + r.PathValue("token") + "/" + romName,
		"EJS_pathtodata": assets, "EJS_gameName": edition.DisplayTitle, "EJS_gameID": gameID,
		"EJS_language": payload.Locale, "EJS_disableAutoLang": true, "EJS_startOnLoaded": false,
		"EJS_noAutoFocus": false, "EJS_threads": false,
	}
	labels := webPlayerLabels(payload.Locale)
	configJSON, err := json.Marshal(map[string]any{
		"loader": loader, "saveUrl": "/api/v1/web-emulation/saves/" + r.PathValue("token"),
		"baseRevision": payload.BaseID, "labels": labels, "options": options,
	})
	if err != nil {
		http.Error(w, "Unable to create player.", http.StatusInternalServerError)
		return
	}
	assetOrigin := "'self'"
	if parsed, parseErr := url.Parse(assets); parseErr == nil && parsed.IsAbs() {
		assetOrigin = parsed.Scheme + "://" + parsed.Host
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' "+assetOrigin+" blob: 'nonce-"+nonceText+"' 'wasm-unsafe-eval' 'unsafe-eval'; style-src 'self' "+assetOrigin+" 'nonce-"+nonceText+"' 'unsafe-inline'; img-src 'self' "+assetOrigin+" data: blob:; media-src 'self' blob:; connect-src 'self' "+assetOrigin+" data: blob:; worker-src 'self' blob:; font-src 'self' "+assetOrigin+" data:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
	w.WriteHeader(http.StatusOK)
	_ = webPlayerTemplate.Execute(w, map[string]any{
		"Lang": payload.Locale, "Title": edition.DisplayTitle, "Loading": labels["loading"],
		"Nonce": nonceText, "Config": template.JS(configJSON),
	})
}
