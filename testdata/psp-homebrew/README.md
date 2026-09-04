# PSP acceptance fixture source

This Apache-2.0 source builds a tiny PSP homebrew `EBOOT.PBP` that renders two
fixed text lines and then waits for vertical blank forever. It contains no game
assets, firmware, BIOS, keys, or third-party code.

The binary is deliberately not checked into the repository. The Android
PPSSPP acceptance builds it inside a digest-pinned PSPDEV container, verifies
the exact output size and SHA-256, and keeps it only in the one-use acceptance
root.
