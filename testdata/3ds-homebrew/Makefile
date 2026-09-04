TARGET := varkiv-fixture
BUILD := build
SOURCE := source/main.c
OBJECT := $(BUILD)/main.o
ELF := $(BUILD)/$(TARGET).elf

DEVKITPRO ?= /opt/devkitpro
DEVKITARM ?= $(DEVKITPRO)/devkitARM
CC := $(DEVKITARM)/bin/arm-none-eabi-gcc
CFLAGS := -O2 -Wall -Wextra -ffunction-sections -march=armv6k -mtune=mpcore -mfloat-abi=hard -mtp=soft -D__3DS__ -I$(DEVKITPRO)/libctru/include
LDFLAGS := -specs=3dsx.specs -march=armv6k -mtune=mpcore -mfloat-abi=hard -mtp=soft -Wl,--gc-sections -L$(DEVKITPRO)/libctru/lib

.PHONY: all clean

all: $(TARGET).3dsx

$(BUILD):
	mkdir -p $@

$(OBJECT): $(SOURCE) | $(BUILD)
	$(CC) $(CFLAGS) -c $< -o $@

$(ELF): $(OBJECT)
	$(CC) $(LDFLAGS) $< -lctru -lm -o $@

$(TARGET).3dsx: $(ELF)
	$(DEVKITPRO)/tools/bin/3dsxtool $< $@

clean:
	rm -rf -- $(BUILD) $(TARGET).3dsx
