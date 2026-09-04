//go:build windows

package deviceagent

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSecureConfigFileUsesAProtectedTwoPrincipalDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := os.WriteFile(path, []byte(`{"access_token":"fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := secureConfigFile(path); err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	dacl, present, err := descriptor.DACL()
	if err != nil || !present || dacl == nil || dacl.AceCount != 2 {
		t.Fatalf("private config DACL present=%t ace_count=%d err=%v", present, func() uint16 {
			if dacl == nil {
				return 0
			}
			return dacl.AceCount
		}(), err)
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("private config DACL is not protected: control=%#x err=%v", control, err)
	}
}
