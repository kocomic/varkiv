package portablepath

import "testing"

func TestCleanSaveLogicalIsHostIndependent(t *testing.T) {
	for input, expected := range map[string]string{
		"primary.srm":      "primary.srm",
		"cards/Mcd001.ps2": "cards/Mcd001.ps2",
		"セーブ/slot 1.dat":   "セーブ/slot 1.dat",
		"emoji/🎮.sav":      "emoji/🎮.sav",
	} {
		if actual, err := CleanSaveLogical(input); err != nil || actual != expected {
			t.Fatalf("safe logical path %q = %q, %v", input, actual, err)
		}
	}
	for _, input := range []string{
		"", "/absolute", `C:\private\save.srm`, "C:/private/save.srm", "../escape", "a/../escape",
		"double//separator", "trailing/", " leading", "trailing ", "slot.", "CON", "nul.dat", "COM1.bin",
		"bad?.sav", "bad\x00name", "line\nbreak.sav",
	} {
		if _, err := CleanSaveLogical(input); err == nil {
			t.Fatalf("unsafe logical path accepted: %q", input)
		}
	}
}
