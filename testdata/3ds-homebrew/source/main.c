// SPDX-License-Identifier: Apache-2.0
#include <3ds.h>
#include <stdio.h>

int main(void) {
    gfxInitDefault();
    consoleInit(GFX_TOP, NULL);
    printf("Varkiv Android acceptance\n");
    printf("Azahar opened the granted 3DSX.\n");
    printf("START exits this public fixture.\n");

    while (aptMainLoop()) {
        gspWaitForVBlank();
        gfxSwapBuffers();
        hidScanInput();
        if (hidKeysDown() & KEY_START) break;
    }

    gfxExit();
    return 0;
}
