package server

import (
	"net/http"
	"strings"
)

// ConfigTemplatePreset is a read-only starter for an inert package template.
// Copying a preset never grants it extra privileges: the resulting template is
// persisted and validated through the same PackageProfile contract as a
// manually entered template.
type ConfigTemplatePreset struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Summary         string   `json:"summary"`
	ContractVersion int      `json:"contract_version"`
	Scope           string   `json:"scope"`
	OutputPath      string   `json:"output_path"`
	Body            string   `json:"body"`
	Targets         []string `json:"targets"`
	Frontends       []string `json:"frontends"`
	Requires        []string `json:"requires"`
}

func builtinConfigTemplatePresets() []ConfigTemplatePreset {
	allTargets := []string{"android", "windows", "steamos-bazzite", "rocknix", "darkos", "arkos", "knulli", "muos", "onionos", "portable"}
	bothFrontends := []string{"pegasus", "es-de"}
	return []ConfigTemplatePreset{
		{
			ID: "builtin-template-device-directories", Name: "Device directory map",
			Summary:         "Writes the selected device profile's portable ROM, save, and configuration directories once per package.",
			ContractVersion: 1, Scope: "package", OutputPath: "config/varkiv/device-paths.cfg",
			Body:    "target={{profile.target}}\nrom_dir={{device.rom_dir}}\nsave_dir={{device.save_dir}}\nconfig_dir={{device.config_dir}}\n",
			Targets: allTargets, Frontends: bothFrontends, Requires: []string{"device profile"},
		},
		{
			ID: "builtin-template-launch-resolution", Name: "Standalone launch map",
			Summary:         "Writes the reviewed driver, resolved argv, executable hints, ROM path, and save namespace for each exported game edition.",
			ContractVersion: 2, Scope: "edition", OutputPath: "config/varkiv/launch/{{edition.id}}.cfg",
			Body:    "driver={{driver.id}}\nfamily={{driver.family}}\nrom={{rom.path}}\nsave_namespace={{edition.save_namespace}}\narguments_json={{launch.arguments_json}}\nexecutable_hints_json={{launch.executable_hints_json}}\n",
			Targets: allTargets, Frontends: bothFrontends, Requires: []string{"launch binding", "emulator driver"},
		},
		{
			ID: "builtin-template-retroarch-core", Name: "RetroArch core map",
			Summary:         "Writes the reviewed RetroArch core and portable ROM path for each exported edition.",
			ContractVersion: 1, Scope: "edition", OutputPath: "config/retroarch/{{platform.id}}/{{edition.id}}.opt",
			Body:    "core={{core.library}}\nrom={{rom.path}}\nsave_namespace={{edition.save_namespace}}\n",
			Targets: allTargets, Frontends: bothFrontends, Requires: []string{"RetroArch launch binding", "core mapping"},
		},
		{
			ID: "builtin-template-android-intent", Name: "Android intent map",
			Summary:         "Writes the reviewed Android package and activity without embedding a server path or executable shell command.",
			ContractVersion: 1, Scope: "edition", OutputPath: "config/android/{{edition.id}}.properties",
			Body:    "package={{launch.android_package}}\nactivity={{launch.android_activity}}\nrom={{rom.path}}\n",
			Targets: []string{"android"}, Frontends: bothFrontends, Requires: []string{"Android launch binding", "package and activity"},
		},
	}
}

func (s *Server) listConfigTemplatePresets(w http.ResponseWriter, r *http.Request) {
	target := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("target")))
	frontend := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("frontend")))
	items := make([]ConfigTemplatePreset, 0)
	for _, item := range builtinConfigTemplatePresets() {
		if target != "" && !containsString(item.Targets, target) {
			continue
		}
		if frontend != "" && !containsString(item.Frontends, frontend) {
			continue
		}
		items = append(items, item)
	}
	writeCollection(w, r, items)
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
