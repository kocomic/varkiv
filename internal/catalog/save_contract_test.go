package catalog

import (
	"strings"
	"testing"

	"varkiv/internal/portablepath"
)

func TestCatalogSaveLogicalPathUsesPortablePersistenceContract(t *testing.T) {
	for input, expected := range map[string]string{
		"cards/Mcd001.ps2": "cards/Mcd001.ps2",
		"emoji/🎮.sav":      "emoji/🎮.sav",
	} {
		actual, err := cleanSaveLogicalPath(input)
		if err != nil || actual != expected {
			t.Fatalf("safe logical path %q = %q, %v", input, actual, err)
		}
	}
	for _, input := range []string{"../private.sav", `C:\private\save.sav`, "CON", "bad?.sav", "a/../private.sav"} {
		if _, err := cleanSaveLogicalPath(input); err == nil {
			t.Fatalf("unsafe persistence path accepted: %q", input)
		} else if strings.Contains(err.Error(), input) {
			t.Fatalf("persistence error disclosed rejected path %q: %v", input, err)
		}
	}
}

func TestCatalogSaveRevisionAggregateBudgetCannotOverflow(t *testing.T) {
	if total, err := addSaveRevisionSize(portablepath.MaxSaveRevisionBytes-3, 3); err != nil || total != portablepath.MaxSaveRevisionBytes {
		t.Fatalf("exact aggregate boundary = %d, %v", total, err)
	}
	for _, values := range [][2]int64{{portablepath.MaxSaveRevisionBytes - 3, 4}, {portablepath.MaxSaveRevisionBytes + 1, 0}, {-1, 0}, {0, -1}} {
		if _, err := addSaveRevisionSize(values[0], values[1]); err == nil {
			t.Fatalf("unsafe aggregate accepted: total=%d size=%d", values[0], values[1])
		}
	}
}
