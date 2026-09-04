TARGET = varkiv_psp_fixture
OBJS = main.o

CFLAGS = -O2 -G0 -Wall -Wextra
CXXFLAGS = $(CFLAGS) -fno-exceptions -fno-rtti
ASFLAGS = $(CFLAGS)
LIBS = -lpspdebug -lpspdisplay -lpspge

EXTRA_TARGETS = EBOOT.PBP
PSP_EBOOT_TITLE = Varkiv PSP Fixture

PSPSDK = $(shell psp-config --pspsdk-path)
include $(PSPSDK)/lib/build.mak
