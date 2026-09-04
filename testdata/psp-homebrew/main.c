// SPDX-License-Identifier: Apache-2.0

#include <pspdebug.h>
#include <pspdisplay.h>
#include <pspkernel.h>

PSP_MODULE_INFO("VarkivFixture", PSP_MODULE_USER, 1, 0);
PSP_MAIN_THREAD_ATTR(THREAD_ATTR_USER);

int main(void) {
    pspDebugScreenInit();
    pspDebugScreenSetBackColor(0x00000000);
    pspDebugScreenSetTextColor(0x00FFFFFF);
    pspDebugScreenClear();
    pspDebugScreenPrintf("VARKIV PSP\n");
    pspDebugScreenPrintf("PPSSPP INTENT OK\n");

    for (;;) {
        sceDisplayWaitVblankStart();
    }
    return 0;
}
