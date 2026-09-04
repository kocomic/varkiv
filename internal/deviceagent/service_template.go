package deviceagent

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var windowsDriveAbsolute = regexp.MustCompile(`^[A-Za-z]:[\\/].+`)

type ServiceTemplateInput struct {
	Kind       string
	BinaryPath string
	ConfigPath string
	User       string
	Interval   time.Duration
}

func safeTemplateValue(value, name string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("%s is required and must fit on one line", name)
	}
	return value, nil
}

func xmlText(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

func systemdQuote(value string) (string, error) {
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("systemd argument contains an unsafe control character")
	}
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`, nil
}

func windowsPathDirectory(value string) (string, error) {
	if !windowsDriveAbsolute.MatchString(value) && !strings.HasPrefix(value, `\\`) {
		return "", errors.New("Windows service paths must be absolute drive or UNC paths")
	}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '\\' || r == '/' }) {
		if part == "." || part == ".." {
			return "", errors.New("Windows service paths must not contain dot segments")
		}
	}
	index := strings.LastIndexAny(value, `\\/`)
	if index < 2 || index == len(value)-1 {
		return "", errors.New("Windows service path must name a file below a directory")
	}
	return value[:index], nil
}

// RenderServiceTemplate emits an inert, reviewable service definition. It does
// not install, enable, start, or replace a task on the host.
func RenderServiceTemplate(input ServiceTemplateInput) (string, string, error) {
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	binary, err := safeTemplateValue(input.BinaryPath, "binary path")
	if err != nil {
		return "", "", err
	}
	config, err := safeTemplateValue(input.ConfigPath, "config path")
	if err != nil {
		return "", "", err
	}
	if input.Interval < 15*time.Second {
		return "", "", errors.New("service interval must be at least 15s")
	}
	interval := input.Interval.String()
	switch kind {
	case "systemd-user":
		binaryArg, quoteErr := systemdQuote(binary)
		if quoteErr != nil {
			return "", "", quoteErr
		}
		configArg, quoteErr := systemdQuote(config)
		if quoteErr != nil {
			return "", "", quoteErr
		}
		workingArg, quoteErr := systemdQuote(filepath.Dir(binary))
		if quoteErr != nil {
			return "", "", quoteErr
		}
		content := "[Unit]\nDescription=Varkiv save sync agent\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=simple\nExecStart=" + binaryArg + " agent run --config " + configArg + " --interval " + interval + "\nWorkingDirectory=" + workingArg + "\nRestart=on-failure\nRestartSec=15s\nNoNewPrivileges=true\nPrivateTmp=true\n\n[Install]\nWantedBy=default.target\n"
		return "varkiv-agent.service", content, nil
	case "windows-task", "windows-tray-task":
		user, userErr := safeTemplateValue(input.User, "Windows user")
		if userErr != nil {
			return "", "", userErr
		}
		if strings.ContainsAny(binary+config, `"`) {
			return "", "", errors.New("Windows service paths must not contain a double quote")
		}
		workingDirectory, pathErr := windowsPathDirectory(binary)
		if pathErr != nil {
			return "", "", pathErr
		}
		if _, pathErr = windowsPathDirectory(config); pathErr != nil {
			return "", "", pathErr
		}
		command, filename, description := "run", "varkiv-agent-task.xml", "Varkiv automatic save synchronization"
		if kind == "windows-tray-task" {
			command, filename, description = "tray", "varkiv-agent-tray-task.xml", "Varkiv save synchronization tray"
		}
		arguments := `agent ` + command + ` --config "` + config + `" --interval ` + interval
		content := `<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>` + description + `</Description></RegistrationInfo>
  <Triggers><LogonTrigger><Enabled>true</Enabled></LogonTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>` + xmlText(user) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><AllowHardTerminate>true</AllowHardTerminate><StartWhenAvailable>true</StartWhenAvailable><RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable><AllowStartOnDemand>true</AllowStartOnDemand><Enabled>true</Enabled><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><Priority>7</Priority><RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure></Settings>
  <Actions Context="Author"><Exec><Command>` + xmlText(binary) + `</Command><Arguments>` + xmlText(arguments) + `</Arguments><WorkingDirectory>` + xmlText(workingDirectory) + `</WorkingDirectory></Exec></Actions>
</Task>
`
		return filename, content, nil
	default:
		return "", "", errors.New("service template kind must be systemd-user, windows-task, or windows-tray-task")
	}
}
