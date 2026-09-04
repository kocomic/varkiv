package deviceagent

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func TestServiceTemplatesAreInertAndEscaped(t *testing.T) {
	filename, unit, err := RenderServiceTemplate(ServiceTemplateInput{Kind: "systemd-user", BinaryPath: "/opt/My Games/varkiv", ConfigPath: "/home/player/.config/Varkiv/agent.json", Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if filename != "varkiv-agent.service" || !strings.Contains(unit, `ExecStart="/opt/My Games/varkiv" agent run`) || strings.Contains(unit, "/bin/sh") || strings.Contains(unit, "sudo") {
		t.Fatalf("unsafe or incomplete systemd unit: %s", unit)
	}

	filename, task, err := RenderServiceTemplate(ServiceTemplateInput{Kind: "windows-task", BinaryPath: `C:\Program Files\Varkiv\varkiv.exe`, ConfigPath: `C:\Users\Player & Family\agent.json`, User: `DESKTOP\Player`, Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if filename != "varkiv-agent-task.xml" || !strings.Contains(task, "&amp;") || !strings.Contains(task, `<WorkingDirectory>C:\Program Files\Varkiv</WorkingDirectory>`) || strings.Contains(task, "HighestAvailable") {
		t.Fatalf("unsafe or incomplete Windows task: %s", task)
	}
	var parsed struct{ XMLName xml.Name }
	if err = xml.Unmarshal([]byte(task), &parsed); err != nil {
		t.Fatalf("invalid task XML: %v", err)
	}

	filename, trayTask, err := RenderServiceTemplate(ServiceTemplateInput{Kind: "windows-tray-task", BinaryPath: `C:\Program Files\Varkiv\varkiv.exe`, ConfigPath: `C:\Users\Player & Family\agent.json`, User: `DESKTOP\Player`, Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if filename != "varkiv-agent-tray-task.xml" || !strings.Contains(trayTask, `agent tray --config`) || strings.Contains(trayTask, `agent run --config`) || strings.Contains(trayTask, "HighestAvailable") {
		t.Fatalf("unsafe or incomplete Windows tray task: %s", trayTask)
	}
	if err = xml.Unmarshal([]byte(trayTask), &parsed); err != nil {
		t.Fatalf("invalid tray task XML: %v", err)
	}
}

func TestServiceTemplateRejectsControlCharactersAndFastLoops(t *testing.T) {
	if _, _, err := RenderServiceTemplate(ServiceTemplateInput{Kind: "systemd-user", BinaryPath: "/opt/varkiv\nExecStart=/bin/sh", ConfigPath: "/config", Interval: time.Minute}); err == nil {
		t.Fatal("newline injection was accepted")
	}
	if _, _, err := RenderServiceTemplate(ServiceTemplateInput{Kind: "systemd-user", BinaryPath: "/opt/varkiv", ConfigPath: "/config", Interval: time.Second}); err == nil {
		t.Fatal("unsafe fast service loop was accepted")
	}
	if _, _, err := RenderServiceTemplate(ServiceTemplateInput{Kind: "windows-task", BinaryPath: `varkiv.exe`, ConfigPath: `C:\Data\agent.json`, User: `Player`, Interval: time.Minute}); err == nil {
		t.Fatal("relative Windows path was accepted")
	}
	if _, _, err := RenderServiceTemplate(ServiceTemplateInput{Kind: "windows-task", BinaryPath: `C:\Apps\..\varkiv.exe`, ConfigPath: `C:\Data\agent.json`, User: `Player`, Interval: time.Minute}); err == nil {
		t.Fatal("Windows dot segment was accepted")
	}
}
