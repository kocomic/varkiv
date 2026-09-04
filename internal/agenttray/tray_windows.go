//go:build windows

package agenttray

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"varkiv/internal/deviceagent"
)

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmTimer         = 0x0113
	wmContextMenu   = 0x007B
	wmApp           = 0x8000
	trayCallback    = wmApp + 1
	syncFinished    = wmApp + 2
	menuSyncNow     = 1001
	menuOpenStatus  = 1002
	menuExit        = 1003
	windowsTimerID  = 1
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203

	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004
	nifMessage    = 0x00000001
	nifIcon       = 0x00000002
	nifTip        = 0x00000004
	notifyVersion = 4

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfDisabled  = 0x00000002
	mfGrayed    = 0x00000001

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	wsOverlapped = 0x00000000
	cwUseDefault = 0x80000000
	swShowNormal = 1

	idiApplication = 32512
	idcArrow       = 32512
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	shell32                 = windows.NewLazySystemDLL("shell32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSetTimer            = user32.NewProc("SetTimer")
	procKillTimer           = user32.NewProc("KillTimer")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procRegisterWindowMsgW  = user32.NewProc("RegisterWindowMessageW")
	procGetUserLocaleName   = kernel32.NewProc("GetUserDefaultLocaleName")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW       = shell32.NewProc("ShellExecuteW")

	activeTray *trayState
)

type point struct{ X, Y int32 }

type message struct {
	Window  windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   point
	Private uint32
}

type windowClassEx struct {
	Size        uint32
	Style       uint32
	WindowProc  uintptr
	ClassExtra  int32
	WindowExtra int32
	Instance    windows.Handle
	Icon        windows.Handle
	Cursor      windows.Handle
	Background  windows.Handle
	MenuName    *uint16
	ClassName   *uint16
	IconSmall   windows.Handle
}

type notifyIconData struct {
	Size           uint32
	Window         windows.Handle
	ID             uint32
	Flags          uint32
	Callback       uint32
	Icon           windows.Handle
	Tip            [128]uint16
	State          uint32
	StateMask      uint32
	Info           [256]uint16
	TimeoutVersion uint32
	InfoTitle      [64]uint16
	InfoFlags      uint32
	GUID           windows.GUID
	BalloonIcon    windows.Handle
}

type trayState struct {
	ctx            context.Context
	cancel         context.CancelFunc
	configPath     string
	interval       time.Duration
	window         windows.Handle
	icon           windows.Handle
	notify         notifyIconData
	text           menuText
	status         string
	taskbarCreated uint32
	syncing        atomic.Bool
}

func Run(parent context.Context, configPath string, interval time.Duration) error {
	if interval < 15*time.Second {
		return errors.New("tray sync interval must be at least 15 seconds")
	}
	if _, err := deviceagent.LoadConfig(configPath); err != nil {
		return deviceagent.SanitizeError(configPath, err)
	}
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(absolute))))
	mutexName, _ := windows.UTF16PtrFromString(fmt.Sprintf("Local\\VarkivAgentTray-%x", digest[:12]))
	mutex, mutexErr := windows.CreateMutex(nil, false, mutexName)
	if mutexErr == windows.ERROR_ALREADY_EXISTS {
		if mutex != 0 {
			_ = windows.CloseHandle(mutex)
		}
		return errors.New("an agent tray is already running for this configuration")
	}
	if mutexErr != nil {
		return fmt.Errorf("create tray instance lock: %w", mutexErr)
	}
	defer windows.CloseHandle(mutex)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx, cancel := context.WithCancel(parent)
	state := &trayState{ctx: ctx, cancel: cancel, configPath: configPath, interval: interval, text: textForLocale(userLocale())}
	activeTray = state
	defer func() { activeTray = nil }()
	if err = state.create(); err != nil {
		cancel()
		return err
	}
	defer state.close()
	go func(window windows.Handle) {
		<-ctx.Done()
		procPostMessageW.Call(uintptr(window), wmClose, 0, 0)
	}(state.window)
	state.startSync()

	var msg message
	for {
		result, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			return fmt.Errorf("read tray message: %w", callErr)
		}
		if result == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (state *trayState) create() error {
	instance, _, callErr := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return fmt.Errorf("get module handle: %w", callErr)
	}
	className, _ := windows.UTF16PtrFromString("VarkivAgentTrayWindow")
	title, _ := windows.UTF16PtrFromString(state.text.Title)
	icon, _, _ := procLoadIconW.Call(0, idiApplication)
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	class := windowClassEx{Size: uint32(unsafe.Sizeof(windowClassEx{})), WindowProc: syscall.NewCallback(windowProcedure), Instance: windows.Handle(instance), Icon: windows.Handle(icon), Cursor: windows.Handle(cursor), ClassName: className, IconSmall: windows.Handle(icon)}
	if result, _, registerErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); result == 0 {
		return fmt.Errorf("register tray window: %w", registerErr)
	}
	window, _, createErr := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), wsOverlapped, cwUseDefault, cwUseDefault, 0, 0, 0, 0, instance, 0)
	if window == 0 {
		return fmt.Errorf("create tray window: %w", createErr)
	}
	state.window, state.icon = windows.Handle(window), windows.Handle(icon)
	taskbarName, _ := windows.UTF16PtrFromString("TaskbarCreated")
	registered, _, _ := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(taskbarName)))
	state.taskbarCreated = uint32(registered)
	state.refreshStatus()
	if err := state.addIcon(); err != nil {
		procDestroyWindow.Call(window)
		return err
	}
	milliseconds := state.interval.Milliseconds()
	if milliseconds > int64(^uint32(0)) {
		milliseconds = int64(^uint32(0))
	}
	if timer, _, timerErr := procSetTimer.Call(window, windowsTimerID, uintptr(milliseconds), 0); timer == 0 {
		return fmt.Errorf("start tray timer: %w", timerErr)
	}
	return nil
}

func (state *trayState) addIcon() error {
	state.notify = notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: state.window, ID: 1, Flags: nifMessage | nifIcon | nifTip, Callback: trayCallback, Icon: state.icon}
	copyUTF16(state.notify.Tip[:], state.tooltip())
	if result, _, callErr := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&state.notify))); result == 0 {
		return fmt.Errorf("add notification icon: %w", callErr)
	}
	state.notify.TimeoutVersion = notifyVersion
	procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&state.notify)))
	return nil
}

func (state *trayState) close() {
	state.cancel()
	if state.notify.Window != 0 {
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&state.notify)))
		state.notify.Window = 0
	}
	if state.window != 0 {
		procKillTimer.Call(uintptr(state.window), windowsTimerID)
		procDestroyWindow.Call(uintptr(state.window))
		state.window = 0
	}
}

func (state *trayState) refreshStatus() {
	if state.syncing.Load() {
		state.status = state.text.Running
	} else if config, err := deviceagent.LoadConfig(state.configPath); err == nil {
		state.status = statusText(state.text, config.LastSync)
	} else {
		state.status = state.text.Failed
	}
	copyUTF16(state.notify.Tip[:], state.tooltip())
	if state.notify.Window != 0 {
		procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&state.notify)))
	}
}

func (state *trayState) tooltip() string {
	return state.text.Title + " · " + state.status
}

func (state *trayState) startSync() {
	if !state.syncing.CompareAndSwap(false, true) {
		return
	}
	state.refreshStatus()
	go func() {
		syncContext, cancel := context.WithTimeout(state.ctx, 10*time.Minute)
		_, _ = deviceagent.SyncOnce(syncContext, state.configPath)
		cancel()
		state.syncing.Store(false)
		procPostMessageW.Call(uintptr(state.window), syncFinished, 0, 0)
	}()
}

func (state *trayState) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	appendMenu(menu, mfString, menuSyncNow, state.text.SyncNow)
	appendMenu(menu, mfString|mfDisabled|mfGrayed, 0, state.status)
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, menuOpenStatus, state.text.OpenStatus)
	appendMenu(menu, mfString, menuExit, state.text.Exit)
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForegroundWindow.Call(uintptr(state.window))
	command, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd, uintptr(cursor.X), uintptr(cursor.Y), 0, uintptr(state.window), 0)
	state.handleCommand(uint16(command))
}

func (state *trayState) handleCommand(command uint16) {
	switch command {
	case menuSyncNow:
		state.startSync()
	case menuOpenStatus:
		state.openStatus()
	case menuExit:
		state.cancel()
		procDestroyWindow.Call(uintptr(state.window))
	}
}

func (state *trayState) openStatus() {
	config, err := deviceagent.LoadConfig(state.configPath)
	if err != nil {
		return
	}
	operation, _ := windows.UTF16PtrFromString("open")
	url, _ := windows.UTF16PtrFromString(strings.TrimRight(config.ServerURL, "/") + "/#sync")
	procShellExecuteW.Call(uintptr(state.window), uintptr(unsafe.Pointer(operation)), uintptr(unsafe.Pointer(url)), 0, 0, swShowNormal)
}

func windowProcedure(window uintptr, event uint32, wParam, lParam uintptr) uintptr {
	state := activeTray
	if state == nil {
		result, _, _ := procDefWindowProcW.Call(window, uintptr(event), wParam, lParam)
		return result
	}
	if event == state.taskbarCreated && event != 0 {
		_ = state.addIcon()
		return 0
	}
	switch event {
	case trayCallback:
		mouseEvent := uint32(lParam & 0xffff)
		if mouseEvent == wmLButtonUp || mouseEvent == wmRButtonUp || mouseEvent == wmContextMenu {
			state.showMenu()
		} else if mouseEvent == wmLButtonDblClk {
			state.openStatus()
		}
		return 0
	case syncFinished:
		state.refreshStatus()
		return 0
	case wmTimer:
		state.startSync()
		return 0
	case wmCommand:
		state.handleCommand(uint16(wParam & 0xffff))
		return 0
	case wmClose:
		procDestroyWindow.Call(window)
		return 0
	case wmDestroy:
		state.cancel()
		procKillTimer.Call(window, windowsTimerID)
		if state.notify.Window != 0 {
			procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&state.notify)))
			state.notify.Window = 0
		}
		state.window = 0
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(window, uintptr(event), wParam, lParam)
	return result
}

func appendMenu(menu uintptr, flags uintptr, id uint16, label string) {
	var pointer uintptr
	if label != "" {
		value, _ := windows.UTF16PtrFromString(label)
		pointer = uintptr(unsafe.Pointer(value))
	}
	procAppendMenuW.Call(menu, flags, uintptr(id), pointer)
}

func copyUTF16(destination []uint16, value string) {
	clear(destination)
	encoded, _ := windows.UTF16FromString(value)
	if len(encoded) > len(destination) {
		encoded = encoded[:len(destination)]
		encoded[len(encoded)-1] = 0
	}
	copy(destination, encoded)
}

func userLocale() string {
	buffer := make([]uint16, 85)
	result, _, _ := procGetUserLocaleName.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if result == 0 {
		return "en"
	}
	return windows.UTF16ToString(buffer)
}
