package bundler

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"varkiv/internal/catalog"
)

const packageSpaceReserveBytes int64 = 8 * 1024 * 1024

func applyDevicePreflight(plan *Plan, device catalog.DeviceProfile, out string) {
	if !device.Enabled {
		plan.Conflicts = append(plan.Conflicts, "device-profile:"+device.ID)
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("device profile %s is disabled", device.ID))
	}
	if plan.Profile.Target != device.Target {
		plan.Conflicts = append(plan.Conflicts, "device-profile:"+device.ID)
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("device profile %s targets %s, not %s", device.ID, device.Target, plan.Profile.Target))
	}
	if plan.Profile.FileMode == "hardlink" && !device.SupportsHardlink {
		plan.Conflicts = append(plan.Conflicts, "file-mode:hardlink")
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("device profile %s does not support hard links", device.ID))
	}

	seen := map[string]int{}
	for index := range plan.Items {
		item := &plan.Items[index]
		if item.Action == "missing" || item.Action == "conflict" {
			continue
		}
		if err := validateDeviceTargetPath(item.Target, device); err != nil {
			item.Action = "conflict"
			item.Detail = err.Error()
			plan.Conflicts = append(plan.Conflicts, item.Target)
			continue
		}
		key := item.Target
		if !device.CaseSensitive {
			key = strings.ToLower(key)
		}
		if previousIndex, exists := seen[key]; exists && plan.Items[previousIndex].Target != item.Target {
			previous := &plan.Items[previousIndex]
			item.Action = "conflict"
			item.Detail = fmt.Sprintf("target collides with %s on a case-insensitive filesystem", previous.Target)
			if previous.Action != "missing" && previous.Action != "conflict" {
				previous.Action = "conflict"
				previous.Detail = fmt.Sprintf("target collides with %s on a case-insensitive filesystem", item.Target)
			}
			plan.Conflicts = append(plan.Conflicts, previous.Target, item.Target)
			continue
		}
		seen[key] = index
	}
	applySpacePreflight(plan, out)
}

func validateDeviceTargetPath(target string, device catalog.DeviceProfile) error {
	target = filepath.ToSlash(strings.TrimSpace(target))
	if target == "" {
		return fmt.Errorf("target path is empty")
	}
	pathLength := len([]byte(target))
	if device.PathStyle == "windows" {
		pathLength = len(utf16.Encode([]rune(strings.ReplaceAll(target, "/", `\`))))
	}
	if device.MaxPath > 0 && pathLength > device.MaxPath {
		return fmt.Errorf("target path length %d exceeds device maximum %d", pathLength, device.MaxPath)
	}
	for _, segment := range strings.Split(target, "/") {
		if segment == "" {
			return fmt.Errorf("target path contains an empty segment")
		}
		for _, character := range segment {
			if strings.ContainsRune(device.IllegalCharacters, character) {
				return fmt.Errorf("target path segment %q contains device-illegal character %q", segment, character)
			}
			if device.PathStyle == "windows" && character < 32 {
				return fmt.Errorf("target path segment %q contains a Windows control character", segment)
			}
		}
		if device.PathStyle == "windows" {
			if strings.HasSuffix(segment, " ") || strings.HasSuffix(segment, ".") {
				return fmt.Errorf("Windows target path segment %q ends with a space or period", segment)
			}
			if isWindowsReservedName(segment) {
				return fmt.Errorf("Windows target path segment %q is a reserved device name", segment)
			}
		}
	}
	return nil
}

func isWindowsReservedName(segment string) bool {
	base := strings.ToUpper(segment)
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}

func applySpacePreflight(plan *Plan, out string) {
	var estimated int64
	var generatedWithoutSize int64
	for _, item := range plan.Items {
		switch item.Action {
		case "copy":
			if item.Size > 0 {
				estimated += item.Size
			}
		case "generate":
			if item.Size > 0 {
				estimated += item.Size
			} else {
				generatedWithoutSize++
			}
		}
	}
	if estimated > 0 || generatedWithoutSize > 0 {
		estimated += packageSpaceReserveBytes + generatedWithoutSize*64*1024
	}
	plan.EstimatedWriteBytes = estimated
	available, err := filesystemAvailableBytes(out)
	if err != nil {
		plan.Warnings = append(plan.Warnings, "available output space could not be checked: "+err.Error())
		return
	}
	plan.SpaceChecked = true
	if available > uint64(^uint64(0)>>1) {
		plan.AvailableBytes = int64(^uint64(0) >> 1)
	} else {
		plan.AvailableBytes = int64(available)
	}
	if estimated > 0 && plan.AvailableBytes < estimated {
		plan.Conflicts = append(plan.Conflicts, "space:insufficient")
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("output has %d bytes available but the package requires an estimated %d bytes", plan.AvailableBytes, estimated))
	}
}
