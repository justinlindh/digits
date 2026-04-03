# Raspberry Pi OS Image Audit — Digits Pi Zero 2 W

**Date:** 2026-04-02  
**Image base:** Raspberry Pi OS Lite (Bookworm, 64-bit)  
**Target hardware:** Pi Zero 2 W (BCM2710A1, ARM Cortex-A53 64-bit, 512MB RAM)  
**Source:** Package list extracted from `images/phone1-fs-backup/rootfs/`  
**Build script:** `tools/build-image.sh`

---

## Summary

| Category | Packages | Disk Size |
|---|---|---|
| Total installed | 724 | ~1,693 MB |
| **REMOVE** (safe to drop) | ~155 | **~1,024 MB** |
| MAYBE (evaluate) | ~40 | ~150 MB |
| KEEP (required) | ~530 | ~519 MB |

Removing the REMOVE packages would cut the rootfs by approximately **1 GB** before compression,
resulting in a meaningfully smaller flashable image.

---

## Non-Package Cruft

These are files that aren't tied to a `.deb` package but consume significant space.

### 🗑️ REMOVE — Firmware blobs for hardware not present

The Pi Zero 2 W uses **Broadcom BCM43430** (Wi-Fi) firmware only. All other wireless
firmware is pure waste:

| Path | Size | Notes |
|---|---|---|
| `/lib/firmware/ath10k/` | 19 MB | Qualcomm Atheros 10K (USB/PCIe Wi-Fi) |
| `/lib/firmware/ath11k/` | 43 MB | Qualcomm Atheros 11K (Wi-Fi 6, PCIe) |
| `/lib/firmware/ath12k/` | 6.4 MB | Qualcomm Atheros 12K (Wi-Fi 7) |
| `/lib/firmware/ath3k-1.fw` | 244 KB | Atheros BT 3K |
| `/lib/firmware/ath6k/` | ~800 KB | Atheros 6K |
| `/lib/firmware/ar*.fw`, `ar*.bin` | ~500 KB | Old Atheros USB blobs |
| `/lib/firmware/mediatek/` | (from pkg) | MediaTek Wi-Fi — see `firmware-mediatek` pkg |
| `/lib/firmware/libertas/` | (from pkg) | Marvell — see `firmware-libertas` pkg |
| `/lib/firmware/mrvl/` | ~1 MB | More Marvell blobs |
| `/lib/firmware/mwl8k/` | ~2 MB | Marvell 88W8k USB |
| `/lib/firmware/qca/` | ~3 MB | Qualcomm Atheros QCA |
| `/lib/firmware/cypress/` | ~5 MB | Cypress IoT (subset of Broadcom, but not BCM43430) |
| `/lib/firmware/rtl_bt/`, `rtl_nic/`, `rtlwifi/`, `rtw88/`, `rtw89/` | ~50 MB | Realtek Wi-Fi/BT |
| `/lib/firmware/mt7601u.bin`, `mt7650.bin`, `mt766*.bin` | ~3 MB | MediaTek USB dongles |

> **Keep:** `/lib/firmware/brcm/brcmfmac43430*raspberrypi,model-zero-2-w*` (Wi-Fi firmware)  
> **Keep:** `/lib/firmware/brcm/BCM43430*.raspberrypi,model-zero-2-w.hcd` (BT firmware, even if BT is disabled, needed by kernel driver init)  
> **REMOVE:** Everything else under `/lib/firmware/` except `brcm/` files for Zero 2 W and `raspberrypi/` (VideoCore GPU firmware)

**Estimated savings from firmware blobs alone: ~130–140 MB**

### 🗑️ REMOVE — Locale data

| Path | Size | Notes |
|---|---|---|
| `/usr/share/locale/` (non-en) | ~140 MB | 198 locale directories; keep `en/`, `en_US/` only |
| `/usr/lib/locale/` (non-C/en) | ~3 MB | Compiled locale archives |

Digits is English-only. Strip to `en`, `en_US`, `C.utf8`.

**Estimated savings: ~140 MB** (depends on dpkg language configuration)

### 🗑️ REMOVE — Documentation and man pages

| Path | Size | Notes |
|---|---|---|
| `/usr/share/doc/` | 77 MB | Package changelogs, copyright notices |
| `/usr/share/man/` | 26 MB | Man pages — unused on embedded device |
| `/usr/share/info/` | ~2 MB | GNU info pages |

These can be fully removed. Apply `dpkg --path-exclude` rules during image build (see Minimal CHROOT_PACKAGES section below).

**Estimated savings: ~105 MB**

### 🗑️ REMOVE — Kernel headers and build artifacts

| Path | Size | Notes |
|---|---|---|
| `/usr/src/linux-headers-6.1.21-v8+/` | ~80 MB | Old kernel headers (6.1 — leftover from prior build) |
| `/usr/src/linux-headers-6.12.47+rpt-*/` | ~80 MB | Current kernel headers (see pkg list) |
| `/usr/src/linux-kbuild-6.12.47+rpt/` | ~3 MB | kbuild scripts |
| `/usr/src/wm8960-soundcard-1.0/` | 164 KB | **WM8960 is deprecated** — replaced by Codec Zero (DA7212) |

The `wm8960-soundcard` source tree in `/usr/src/` is dead weight — `setup_wm8960.sh` itself says "DEPRECATED."

**Estimated savings: ~163 MB**

### 🗑️ REMOVE — Include files (C headers)

| Path | Size | Notes |
|---|---|---|
| `/usr/include/` | 28 MB | C/C++ headers — not needed on runtime-only image |

Remove all of `/usr/include/` in the image (it's pulled in by `libc6-dev`, `libstdc++-12-dev`, etc., which should themselves be removed).

---

## Package Audit

### Legend
- 🟢 **KEEP** — Directly required by digitsd, digits-setup, OS boot, or audio
- 🟡 **MAYBE** — Possibly needed but worth investigating
- 🔴 **REMOVE** — Safe to remove with disk savings estimate

---

### 🔴 REMOVE — Development Tools (~430 MB)

Not needed on a production embedded image. These were likely installed transitively during
image prep or left from a dev session.

| Package | Size (KB) | Reason |
|---|---|---|
| `gcc-12` | 58,809 | C compiler — not needed at runtime |
| `cpp-12` | 28,781 | C preprocessor — not needed at runtime |
| `g++-12` | 31,115 | C++ compiler — not needed at runtime |
| `gcc`, `gcc-12-base`, `cpp`, `g++` | ~300 | Metapackages |
| `binutils-aarch64-linux-gnu` | 15,797 | Assembler/linker for cross-compile |
| `binutils-common`, `binutils` | 15,130 | Binutils support files |
| `libgcc-12-dev` | 10,852 | GCC development libs |
| `libstdc++-12-dev` | 19,820 | C++ STL dev headers |
| `libc6-dev` | 8,999 | glibc headers |
| `libc6-dbg` | 10,610 | glibc debug symbols |
| `linux-libc-dev` | 8,172 | Kernel UAPI headers for compilation |
| `autoconf` | 2,025 | Build system generator |
| `automake` | 1,837 | Makefile generator |
| `autotools-dev` | 134 | Autotools helpers |
| `m4` | 722 | Macro processor |
| `make` | 1,596 | Build tool |
| `build-essential` | 20 | Metapackage |
| `git` | 45,520 | Version control — not needed on device |
| `git-man` | 2,107 | Git documentation |
| `gdb` | 11,748 | Debugger — use SSH + gdbserver for remote debug if ever needed |
| `strace` | 2,455 | syscall tracer — dev only |
| `libasan8` | 8,072 | AddressSanitizer runtime |
| `libtsan2` | 7,914 | ThreadSanitizer runtime |
| `libubsan1` | 2,706 | UB Sanitizer runtime |
| `liblsan0` | 2,951 | LeakSanitizer runtime |
| `libitm1` | 146 | GCC transactional memory |
| `libhwasan0` | 3,123 | HW AddressSanitizer (ARM64) |
| `libgprofng0` | 3,260 | GNU profiler runtime |
| `man-db` | 3,319 | Man page database |
| `manpages` | 1,548 | General man pages |
| `manpages-dev` | 3,732 | Developer man pages |
| `groff-base` | 3,925 | Text formatter (man page dependency) |
| **Subtotal** | **~430,000** | **~420 MB** |

---

### 🔴 REMOVE — Kernel packages for Pi 5 / wrong architecture (~85 MB)

Pi Zero 2 W uses the `v8` (64-bit ARMv8) kernel. The `2712` variant is for **Pi 5 only** (BCM2712).

| Package | Size (KB) | Reason |
|---|---|---|
| `linux-image-6.12.47+rpt-rpi-2712` | 35,955 | Pi 5 kernel — wrong board |
| `linux-image-rpi-2712` | 13 | Metapackage pointing to Pi 5 kernel |
| `linux-headers-6.12.47+rpt-rpi-2712` | 3,472 | Pi 5 headers |
| `linux-headers-rpi-2712` | 10 | Metapackage |
| `linux-headers-6.12.47+rpt-common-rpi` | 47,542 | Common kernel headers — not needed at runtime |
| `linux-headers-6.12.47+rpt-rpi-v8` | 3,474 | ARM v8 kernel headers — runtime doesn't need them |
| `linux-headers-rpi-v8` | 10 | Metapackage |
| `linux-kbuild-6.12.47+rpt` | 2,939 | Build scripts for kernel modules |
| `raspberrypi-kernel-headers` | 63,242 | Metapackage + headers |
| **Subtotal** | **~156,657** | **~153 MB** |

> **Keep:** `linux-image-6.12.47+rpt-rpi-v8` and `linux-image-rpi-v8` — these are the actual running kernel.

---

### 🔴 REMOVE — Wireless firmware for non-present hardware (~130 MB)

Pi Zero 2 W only has Broadcom BCM43430. Everything else is dead weight.

| Package | Size (KB) | Reason |
|---|---|---|
| `firmware-atheros` | 74,147 | Qualcomm Atheros Wi-Fi — no Atheros hardware on Pi Zero 2 W |
| `firmware-mediatek` | 32,895 | MediaTek/Ralink Wi-Fi chips — not present |
| `firmware-libertas` | 11,379 | Marvell wireless — not present |
| `firmware-realtek` | 11,282 | Realtek Wi-Fi/BT — not present |
| **Subtotal** | **~129,703** | **~127 MB** |

> **Keep:** `firmware-brcm80211` — Broadcom BCM43430 Wi-Fi firmware (Wi-Fi required!)

---

### 🔴 REMOVE — Bluetooth stack (~6 MB)

BT is explicitly disabled via `dtoverlay=disable-bt` in `config.txt`. The BT stack has no function.

| Package | Size (KB) | Reason |
|---|---|---|
| `bluez` | 4,881 | Bluetooth daemon — disabled in firmware |
| `bluez-firmware` | 464 | Bluetooth firmware files |
| `pi-bluetooth` | 28 | RPi Bluetooth service scripts |
| `libbluetooth3` | 414 | Bluetooth socket library |
| **Subtotal** | **~5,787** | **~5.7 MB** |

> **Note:** `BCM43430*.hcd` firmware files in `/lib/firmware/brcm/` can also be removed since BT is disabled.

---

### 🔴 REMOVE — Cellular modem stack (~12 MB)

No cellular modem. No USB modem dongle. ModemManager actively conflicts with our NetworkManager-only setup.

| Package | Size (KB) | Reason |
|---|---|---|
| `modemmanager` | 6,396 | Cellular modem manager — no modem hardware |
| `libmbim-glib4` | 705 | MBIM (4G/LTE protocol) library |
| `libmbim-proxy` | 80 | MBIM proxy daemon |
| `libmbim-utils` | 238 | MBIM tools |
| `libqmi-glib5` | 3,881 | QMI (Qualcomm modem) protocol library |
| `libqmi-proxy` | 82 | QMI proxy daemon |
| `libqmi-utils` | 838 | QMI tools |
| `libqrtr-glib0` | 60 | Qualcomm IPC Router library |
| **Subtotal** | **~12,280** | **~12 MB** |

---

### 🔴 REMOVE — Mesa GPU / Display stack (~220 MB)

Headless embedded device. No display, no GPU compute, no X11, no Wayland. All of this is pure waste.

| Package | Size (KB) | Reason |
|---|---|---|
| `libllvm15` | 109,066 | LLVM runtime — only needed by Mesa |
| `mesa-vulkan-drivers` | 38,418 | Vulkan GPU drivers |
| `mesa-libgallium` | 31,804 | Mesa Gallium drivers |
| `mesa-va-drivers` | 47 | VA-API Mesa backend |
| `mesa-vdpau-drivers` | 62 | VDPAU Mesa backend |
| `libgl1-mesa-dri` | 304 | Mesa DRI driver |
| `libglapi-mesa` | 370 | Mesa GL dispatch table |
| `libglx-mesa0` | 596 | Mesa GLX loader |
| `libvulkan1` | 545 | Vulkan ICD loader |
| `libglvnd0` | 1,564 | OpenGL vendor-neutral dispatch |
| `libgl1` | 1,084 | OpenGL stub |
| `libglx0` | 218 | GLX stub |
| `libdrm2` | 169 | DRM kernel interface |
| `libdrm-amdgpu1` | 101 | AMD GPU DRM (irrelevant) |
| `libdrm-common` | 45 | DRM common files |
| `libdrm-radeon1` | 99 | AMD Radeon DRM (irrelevant) |
| `libva2` | 225 | Video Acceleration API |
| `libva-drm2` | 95 | VA-API DRM backend |
| `libva-x11-2` | 95 | VA-API X11 backend |
| `libvdpau1` | 157 | VDPAU API |
| `libvdpau-va-gl1` | 270 | VDPAU to VA-API bridge |
| **Subtotal** | **~185,339** | **~181 MB** |

---

### 🔴 REMOVE — X11 client libraries (~15 MB)

Zero X11 or Wayland usage. These exist only as transitive deps of the GPU/camera stacks.

| Package | Size (KB) | Reason |
|---|---|---|
| `libx11-6` | 1,573 | X11 client library |
| `libx11-data` | 1,577 | X11 locale data |
| `libx11-xcb1` | 304 | X11/XCB bridge |
| `libxcb1` + all `libxcb-*` | ~2,200 | XCB protocol libraries (11 packages) |
| `libxau6`, `libxdmcp6` | 95 | X11 auth/display libraries |
| `libxext6`, `libxfixes3`, `libxrender1` | ~355 | X11 extensions |
| `libxshmfence1`, `libxxf86vm1`, `libxpm4`, `libxmuu1`, `libxpm4` | ~250 | More X11 extras |
| `xkb-data` | 6,925 | X Keyboard Extension data |
| `libgdk-pixbuf-2.0-0`, `-bin`, `-common` | 3,788 | GTK image loader |
| `libcairo2`, `libcairo-gobject2` | 1,505 | Cairo 2D graphics |
| `libpango-1.0-0`, `libpangocairo-1.0-0`, `libpangoft2-1.0-0` | 837 | Pango text rendering |
| `libharfbuzz0b` | 2,597 | Text shaping |
| `libfreetype6` | 855 | Font rendering |
| `libfontconfig1` | 603 | Font configuration |
| `libfribidi0` | 172 | Bidirectional text |
| `fonts-dejavu-core` | 2,960 | DejaVu fonts |
| `librsvg2-2`, `librsvg2-common` | 9,897 | SVG renderer |
| `libdatrie1`, `libthai0`, `libthai-data` | 838 | Thai locale/input libs |
| `libgraphite2-3` | 167 | Graphite font engine |
| `libpixman-1-0` | 667 | Pixel manipulation |
| `shared-mime-info` | 5,030 | MIME type database |
| **Subtotal** | **~43,994** | **~43 MB** |

---

### 🔴 REMOVE — Video/camera stack (~50 MB)

No camera attached. No display pipeline. rpicam/libcamera/V4L2 are useless.

| Package | Size (KB) | Reason |
|---|---|---|
| `libcamera0.5` | 2,127 | libcamera HAL |
| `libcamera-ipa` | 4,715 | Camera IPA modules |
| `libpisp1` | 853 | Pi ISP library |
| `libpisp-common` | 21 | Pi ISP common files |
| `rpicam-apps-core` | 1,014 | rpicam CLI apps |
| `rpicam-apps-lite` | 10 | rpicam metapackage |
| `librpicam-app1` | 727 | rpicam app library |
| `v4l-utils` | 2,742 | Video4Linux tools |
| `libv4l-0`, `libv4lconvert0`, `libv4l2rds0` | 1,109 | V4L2 libraries |
| **Subtotal** | **~13,318** | **~13 MB** |

---

### 🔴 REMOVE — Video codecs and multimedia (~50 MB)

No media playback. `mkvtoolnix` is especially egregious at 25 MB.

| Package | Size (KB) | Reason |
|---|---|---|
| `mkvtoolnix` | 25,422 | Matroska file tools — completely unrelated |
| `libavcodec59` | 12,726 | FFmpeg audio/video codecs |
| `libavutil57` | 961 | FFmpeg utility library |
| `libswresample4` | 233 | FFmpeg resampling |
| `libvpx7` | 2,085 | VP8/VP9 codec |
| `libaom3` | 3,583 | AV1 codec |
| `libdav1d6` | 796 | AV1 decoder |
| `librav1e0` | 1,884 | AV1 encoder |
| `libsvtav1enc1` | 3,207 | Scalable AV1 encoder |
| `libx264-164` | 1,368 | H.264 encoder |
| `libx265-199` | 2,949 | H.265 encoder |
| `libheif1` | 679 | HEIF image codec |
| `libjxl0.7` | 1,764 | JPEG XL codec |
| `libopenjp2-7` | 467 | OpenJPEG 2 codec |
| `libmatroska7` | 726 | Matroska library |
| `libebml5` | 211 | EBML library (Matroska format) |
| `libzvbi0`, `libzvbi-common` | 910 | Teletext decoder |
| **Subtotal** | **~59,971** | **~58.6 MB** |

> **Note:** digitsd uses `libopus0` and `libopusfile0` for WebRTC audio — those are correctly in CHROOT_PACKAGES and should be kept. The `libsndfile` family (for WAV tone playback) should also be kept.

---

### 🔴 REMOVE — NFS/RPC network filesystem stack (~3.5 MB)

No NFS mounts. No RPC services.

| Package | Size (KB) | Reason |
|---|---|---|
| `nfs-common` | 1,465 | NFS client utilities |
| `rpcbind` | 194 | RPC port mapper |
| `rpcsvc-proto` | 282 | RPC protocol definitions |
| `libnfsidmap1` | 364 | NFS identity mapping |
| `libtirpc3`, `libtirpc-common`, `libtirpc-dev` | 1,037 | TI-RPC library |
| `libtalloc2` | 102 | Talloc memory pool (NFS dep) |
| **Subtotal** | **~3,444** | **~3.4 MB** |

---

### 🔴 REMOVE — Storage management extras (~10 MB)

No removable drives to manage. No NTFS. No MTP (Android file transfer).

| Package | Size (KB) | Reason |
|---|---|---|
| `udisks2` | 2,637 | Disk management daemon |
| `libudisks2-0` | 847 | UDisks library |
| `ntfs-3g` | 2,106 | NTFS driver |
| `libntfs-3g89` | 414 | NTFS library |
| `libmtp9`, `libmtp-common`, `libmtp-runtime` | 1,011 | MTP (Android) protocol |
| `usb-modeswitch` | 146 | USB modem mode switcher |
| `usb-modeswitch-data` | 102 | USB modem ID database |
| `libparted2`, `libparted-fs-resize0` | 682 | libparted (partitioning) |
| `parted` | 218 | Parted CLI |
| `dosfstools` | 307 | FAT filesystem tools |
| `exfatprogs` | 424 | exFAT tools |
| `ntfs-3g` | — | (listed above) |
| `fuse3`, `libfuse3-3` | 485 | FUSE userspace filesystem |
| **Subtotal** | **~9,379** | **~9.2 MB** |

---

### 🔴 REMOVE — Locale and i18n data (~45 MB)

| Package | Size (KB) | Reason |
|---|---|---|
| `iso-codes` | 20,086 | Country/language/currency code translations |
| `locales` | 15,846 | GNU locale data (198 locales) |
| `libc-l10n` | 4,349 | glibc locale support files |
| `gnupg-l10n` | 4,874 | GnuPG translations |
| `libglib2.0-data` | 9,406 | GLib locale/schema data |
| **Subtotal** | **~54,561** | **~53 MB** |

> Only `en_US.UTF-8` is needed. Use `localedef` to install just that locale.

---

### 🔴 REMOVE — Polkit / AppArmor (~4 MB)

PolicyKit handles privilege escalation for desktop environments. AppArmor is a MAC system.
Neither is relevant for a single-purpose headless device where root services are tightly controlled.

| Package | Size (KB) | Reason |
|---|---|---|
| `polkitd` | 674 | PolicyKit daemon |
| `polkitd-pkla` | 192 | PKla rules backend |
| `policykit-1` | 32 | Metapackage |
| `pkexec` | 96 | PolicyKit privilege escalation |
| `libpolkit-agent-1-0` | 90 | PolicyKit agent library |
| `libpolkit-gobject-1-0` | 160 | PolicyKit GObject library |
| `apparmor` | 2,746 | AppArmor MAC framework |
| `libapparmor1` | 161 | AppArmor library |
| `dkms` | 186 | Dynamic kernel module support |
| **Subtotal** | **~4,337** | **~4.2 MB** |

---

### 🔴 REMOVE — Pi-specific tools not needed on production device (~110 MB)

| Package | Size (KB) | Reason |
|---|---|---|
| `rpi-eeprom` | 43,340 | Pi 4/5 EEPROM updater — Zero 2 W does not have user-updatable EEPROM |
| `raspi-firmware` | 22,140 | Extra firmware + bootloaders for Pi 4/5 |
| `rpi-update` | 31 | Kernel/firmware updater script |
| `rpi-keyboard-config` | 188 | RPi keyboard variant config |
| `rpi-keyboard-fw-update` | 1,001 | Keyboard firmware updater |
| `raspi-config` | 163 | Interactive config tool — not needed on locked-down device |
| `raspi-gpio` | 82 | GPIO tool |
| `raspinfo` | 21 | Pi system info reporter |
| `raspi-utils`, `raspi-utils-core`, `raspi-utils-dt`, `raspi-utils-eeprom`, `raspi-utils-otp` | 719 | Utility metapackages |
| `read-edid` | 58 | Monitor EDID reader — no display |
| `userconf-pi` | 35 | First-boot user creation — handled by our own `digits-first-boot.sh` |
| `flashrom` | 893 | Flash chip programmer |
| **Subtotal** | **~68,671** | **~67 MB** |

---

### 🔴 REMOVE — libz3-4 and Boost (~35 MB)

| Package | Size (KB) | Reason |
|---|---|---|
| `libz3-4` | 21,607 | Z3 theorem prover — pulled in by LLVM/Mesa |
| `libboost-filesystem1.74.0` | 2,152 | Boost filesystem — dep of camera/Mesa |
| `libboost-log1.74.0` | 3,500 | Boost logging |
| `libboost-program-options1.74.0` | 2,472 | Boost program options |
| `libboost-regex1.74.0` | 2,985 | Boost regex |
| `libboost-thread1.74.0` | 2,216 | Boost threads |
| **Subtotal** | **~34,932** | **~34 MB** |

---

### 🟡 MAYBE — Items requiring review

| Package | Size (KB) | Notes |
|---|---|---|
| `pigpio`, `pigpiod`, `libpigpio1`, `libpigpio-dev`, `libpigpiod-if*` | 1,839 | GPIO library — digitsd doesn't import it; used by deprecated Pi scripts. **Remove** unless future features need it. |
| `python3.11-minimal`, `python3-*` | ~24,000 | Python stack — not used by digitsd or digits-setup. Used by legacy `setup_wm8960.sh` and test scripts only. **Remove** for production; **keep** for dev builds. |
| `perl-base`, `perl-modules-5.36`, `libperl5.36` | ~57,000 | Perl — `dpkg` scripts depend on `perl-base`. `perl-modules` and `libperl5.36` are safe to remove if `dpkg` is retained without full Perl. Check carefully. |
| `avahi-daemon`, `libavahi-*` | ~1,200 | mDNS/Bonjour — digitsd.service has `After=network-online.target` but doesn't list Avahi. Task spec says "digitsd needs avahi" but there's no Avahi import in `go.mod`. Verify if `.local` discovery is actually used. If not, **remove**. |
| `iproute2` | 4,158 | `ip` command — used in `digits-ap-setup` for `ip link`, `ip addr`. **Keep.** |
| `iw` | 353 | Wi-Fi scan tool — used by digits-setup `SystemScanner`. **Keep.** |
| `openssh-server`, `openssh-client` | ~6,700 | SSH — disabled by default. Needed for dev/debug access. **Keep** with systemd mask; consider dropping from prod image, installing only in `--dev` builds. |
| `sudo` | 6,358 | `flash-pico.sh` calls `sudo systemctl` — but runs as root on device anyway. Check if truly needed. |
| `fake-hwclock` | 32 | Saves/restores clock on reboot — useful on Pi Zero 2 W which has no RTC. **Keep** or replace with `systemd-timesyncd` only (already installed). |
| `cron` | ~1,200 | Standard cron daemon — no digits cron jobs visible. **Remove** unless needed. |
| `triggerhappy` | 190 | Input event daemon — headless device with no keyboard/buttons. **Remove.** |
| `htop` | ~800 | Interactive process viewer — dev tool. **Remove** from prod. |
| `ncdu` | ~400 | ncurses disk usage — dev tool. **Remove** from prod. |
| `wget` | 3,529 | HTTP downloader — `rpi-update` dep. Remove with rpi-update. |
| `curl` | ~1,500 | HTTP client — not used by digitsd/digits-setup. Possibly needed for NM connectivity checks. Verify. |
| `pastebinit` | ~150 | Pastebin uploader — remove. |
| `dirmngr`, `gnupg`, `gpg*` | ~8,000 | GnuPG — needed for `apt` key verification during build. Not needed at runtime. |
| `ppp` | ~1,500 | Point-to-Point Protocol — no dialup/cellular. Remove. |
| `iperf3`, `libiperf0` | 393 | Network benchmark tool — dev only, remove from prod. |
| `net-tools` | 1,368 | `ifconfig`, `netstat` — superseded by `iproute2`. Remove. |
| `openssl` | ~1,800 | TLS CLI — probably needed by some runtime scripts (NM, ssh-keygen). **Keep.** |
| `lsb-release` | ~100 | OS identification — needed by some scripts. Low impact. Keep. |
| `rfkill` | 113 | RF kill switch utility — used explicitly in `digits-ap-check` (`rfkill unblock wifi`). **Keep.** |
| `wireless-tools` | 548 | `iwconfig` etc — superseded by `iw`. Verify if anything uses `iwconfig`. |

---

### 🟢 KEEP — Required packages

These are required for correct operation. Do not remove.

**Core OS:**
`systemd`, `systemd-sysv`, `systemd-timesyncd`, `udev`, `dbus`, `dbus-bin`, `dbus-daemon`, `init`, `init-system-helpers`, `util-linux`, `coreutils`, `bash`, `dash`, `sed`, `grep`, `findutils`, `gzip`, `tar`, `xz-utils`, `bzip2`, `gawk` / `mawk`, `procps`, `sysvinit-utils`, `kmod`, `login`, `passwd`, `shadow`

**Networking (NetworkManager stack):**
`network-manager`, `wpasupplicant`, `iproute2`, `iw`, `rfkill`, `wireless-regdb`, `libnm0`, `libndp0`, `libteamdctl0`, `libmnl0`, `libnl-*`

**Wi-Fi AP mode:**
`hostapd`, `dnsmasq-base` (note: use `dnsmasq-base`, not full `dnsmasq`)

**Audio:**
`alsa-utils`, `alsa-topology-conf`, `alsa-ucm-conf`, `libasound2`, `libasound2-data`, `libsndfile1` (for tone WAV playback), `libopus0`, `libopusfile0`, `libogg0`, `libflac12`, `libvorbis0a`, `libvorbisenc2`, `libvorbisfile3`, `libsamplerate0`, `libsoxr0`

**Avahi (if confirmed needed):**
`avahi-daemon`, `libavahi-common3`, `libavahi-common-data`, `libavahi-core7`, `libnss-mdns`

**OpenOCD (Pico flashing):**
`openocd`, `libjaylink0`, `libjim0.81`, `libftdi1-2`, `libhidapi-hidraw0`, `libusb-1.0-0`

**I2C/GPIO (audio codec, hardware access):**
`i2c-tools`, `libi2c0`, `gpiod`, `libgpiod2`

**SSH (dev/debug access):**
`openssh-server`, `openssh-client`, `openssh-sftp-server`

**TLS/crypto (used by NM, SSH, HTTPS):**
`openssl`, `libssl3`, `ca-certificates`, `libgnutls30`, `libnettle8`, `libhogweed6`

**PAM/auth:**
`libpam0g`, `libpam-modules`, `libpam-runtime`, `libpam-systemd`, `sudo`

**System tools:**
`apt`, `dpkg`, `adduser`, `logrotate`, `rsync`, `less`, `nano`, `base-files`, `base-passwd`, `hostname`, `tzdata`, `readline-common`

**Pi-specific (Zero 2 W):**
`linux-image-6.12.47+rpt-rpi-v8`, `linux-image-rpi-v8`, `raspi-firmware` (partial — only RPi3/Zero boot blobs), `raspberrypi-archive-keyring`, `raspberrypi-sys-mods`, `raspberrypi-net-mods`, `firmware-brcm80211`, `pi-bluetooth` (keep for BT init even if disabled — kernel needs HCD loaded)

**Serial/UART (Pico comms):**
`stty` (part of `coreutils`)

---

## Minimal CHROOT_PACKAGES for build-image.sh

Current:
```bash
CHROOT_PACKAGES="hostapd dnsmasq alsa-utils libopus0 libopusfile0 openocd"
```

The Pi OS Lite base already includes: `systemd`, `network-manager`, `wpasupplicant`, `avahi-daemon`, `iproute2`, `rfkill`, `iw`, `ca-certificates`, `openssl`, `openssh-server`.

**Recommended minimal CHROOT_PACKAGES:**
```bash
CHROOT_PACKAGES="\
  hostapd \
  dnsmasq-base \
  alsa-utils \
  libopus0 \
  libopusfile0 \
  libsndfile1 \
  openocd \
  i2c-tools \
"
```

Changes from current:
- `dnsmasq` → `dnsmasq-base` (avoids pulling in full dnsmasq service, we manage it ourselves)
- Added `libsndfile1` (WAV tone file playback by digitsd)
- Added `i2c-tools` (needed for audio codec DA7212 configuration)
- Removed nothing from current list — everything else was already lean

### Packages to explicitly PURGE in chroot (add to build-image.sh)

Add this step after base install to strip unneeded packages from the base OS image:

```bash
# Purge packages not needed on production Digits device
PURGE_PACKAGES="\
  gcc-12 g++-12 cpp-12 binutils binutils-aarch64-linux-gnu binutils-common \
  libgcc-12-dev libstdc++-12-dev libc6-dev libc6-dbg linux-libc-dev \
  autoconf automake autotools-dev m4 make build-essential \
  git git-man gdb strace \
  man-db manpages manpages-dev groff-base \
  linux-headers-6.12.47+rpt-common-rpi linux-headers-6.12.47+rpt-rpi-2712 \
  linux-headers-6.12.47+rpt-rpi-v8 linux-headers-rpi-2712 linux-headers-rpi-v8 \
  linux-kbuild-6.12.47+rpt raspberrypi-kernel-headers \
  linux-image-6.12.47+rpt-rpi-2712 linux-image-rpi-2712 \
  firmware-atheros firmware-mediatek firmware-libertas firmware-realtek \
  bluez bluez-firmware \
  modemmanager libmbim-glib4 libmbim-proxy libmbim-utils \
  libqmi-glib5 libqmi-proxy libqmi-utils libqrtr-glib0 \
  libllvm15 mesa-vulkan-drivers mesa-libgallium mesa-va-drivers mesa-vdpau-drivers \
  libgl1-mesa-dri libglapi-mesa libglx-mesa0 libvulkan1 libglvnd0 \
  libdrm-amdgpu1 libdrm-radeon1 libva2 libva-drm2 libva-x11-2 libvdpau1 libvdpau-va-gl1 \
  libz3-4 libboost-filesystem1.74.0 libboost-log1.74.0 libboost-program-options1.74.0 \
  libboost-regex1.74.0 libboost-thread1.74.0 \
  libx11-6 libx11-data xkb-data shared-mime-info \
  libcairo2 libcairo-gobject2 libpango-1.0-0 libpangocairo-1.0-0 libpangoft2-1.0-0 \
  libharfbuzz0b libfreetype6 libfontconfig1 libfribidi0 fonts-dejavu-core \
  librsvg2-2 librsvg2-common libgdk-pixbuf-2.0-0 libgdk-pixbuf2.0-bin libgdk-pixbuf2.0-common \
  libcamera0.5 libcamera-ipa libpisp1 libpisp-common rpicam-apps-core rpicam-apps-lite librpicam-app1 \
  v4l-utils libv4l-0 libv4lconvert0 libv4l2rds0 \
  libavcodec59 libavutil57 libswresample4 mkvtoolnix libmatroska7 libebml5 \
  libvpx7 libaom3 libdav1d6 librav1e0 libsvtav1enc1 libx264-164 libx265-199 \
  libheif1 libjxl0.7 libopenjp2-7 libzvbi0 libzvbi-common \
  nfs-common rpcbind rpcsvc-proto libnfsidmap1 libtirpc-dev libtalloc2 \
  udisks2 libudisks2-0 ntfs-3g libntfs-3g89 libmtp9 libmtp-common libmtp-runtime \
  usb-modeswitch usb-modeswitch-data libparted2 libparted-fs-resize0 parted \
  dosfstools exfatprogs fuse3 libfuse3-3 \
  iso-codes locales gnupg-l10n libglib2.0-data \
  polkitd polkitd-pkla policykit-1 pkexec libpolkit-agent-1-0 libpolkit-gobject-1-0 \
  apparmor libapparmor1 dkms \
  rpi-eeprom raspi-firmware rpi-update rpi-keyboard-config rpi-keyboard-fw-update \
  raspi-config raspi-gpio raspinfo userconf-pi read-edid flashrom \
  pigpio pigpiod libpigpio1 libpigpio-dev libpigpiod-if1 libpigpiod-if2-1 libpigpiod-if-dev \
  triggerhappy htop ncdu iperf3 libiperf0 net-tools wget pastebinit \
  cron cron-daemon-common ppp libasan8 libtsan2 libubsan1 liblsan0 libitm1 libhwasan0 libgprofng0 \
"

chroot "$ROOTFS_MNT" /bin/bash -c "
  DEBIAN_FRONTEND=noninteractive apt-get purge -y --auto-remove ${PURGE_PACKAGES} 2>/dev/null || true
  apt-get autoremove -y 2>/dev/null || true
  apt-get clean
  rm -rf /var/lib/apt/lists/*
"
```

### Additional cleanup in chroot

```bash
# Strip locale data (keep only en_US)
chroot "$ROOTFS_MNT" /bin/bash -c "
  find /usr/share/locale -mindepth 1 -maxdepth 1 -type d \
    ! -name 'en' ! -name 'en_US' | xargs rm -rf
  find /usr/lib/locale -mindepth 1 -maxdepth 1 \
    ! -name 'C.utf8' ! -name 'en_US.UTF-8' | xargs rm -rf 2>/dev/null || true
"

# Remove doc/man pages (already purged packages help, but clean remaining)
chroot "$ROOTFS_MNT" /bin/bash -c "
  rm -rf /usr/share/doc/
  rm -rf /usr/share/man/
  rm -rf /usr/share/info/
  rm -rf /usr/share/groff/
"

# Remove dev include files (no compilation on device)
rm -rf "${ROOTFS_MNT}/usr/include/"

# Remove old/deprecated kernel source
rm -rf "${ROOTFS_MNT}/usr/src/linux-headers-6.1.21-v8+"
rm -rf "${ROOTFS_MNT}/usr/src/wm8960-soundcard-1.0"  # deprecated HAT

# Strip unused firmware blobs (keep only brcm/43430 for Zero 2 W)
find "${ROOTFS_MNT}/lib/firmware/" \
  -mindepth 1 -maxdepth 1 \
  ! -name 'brcm' \
  ! -name 'raspberrypi' \
  ! -name 'regulatory.db' \
  ! -name 'regulatory.db-debian' \
  -exec rm -rf {} +

# Strip BT firmware from brcm (BT is disabled)
find "${ROOTFS_MNT}/lib/firmware/brcm" \
  -name "BCM*.hcd" \
  -delete
```

---

## Estimated Total Savings

| Category | Savings |
|---|---|
| Development tools (gcc, git, gdb, etc.) | ~420 MB |
| Kernel headers + Pi5 kernel | ~153 MB |
| Non-brcm wireless firmware (packages) | ~127 MB |
| Non-brcm firmware blobs (files) | ~130 MB |
| Mesa / GPU / LLVM / X11 | ~220 MB |
| Video codecs + multimedia | ~60 MB |
| Locale data | ~145 MB |
| Documentation + man pages | ~105 MB |
| `/usr/include/` headers | ~28 MB |
| Camera/V4L2 stack | ~13 MB |
| Bluetooth stack | ~6 MB |
| ModemManager/MBIM/QMI | ~12 MB |
| NFS/RPC | ~3.5 MB |
| Storage management (udisks, NTFS, MTP) | ~9 MB |
| Boost/Z3 (LLVM deps) | ~34 MB |
| Pi tools (rpi-eeprom, raspi-config, etc.) | ~67 MB |
| Old kernel source in /usr/src | ~160 MB |
| Python (optional, dev only) | ~24 MB |
| **Estimated Total** | **~1,716 MB** |

> ⚠️ These figures reflect installed size (uncompressed). Many packages share deps, so
> `apt-get autoremove` will cascade. The actual reduction will be somewhat less due to
> shared dependencies among the REMOVE packages themselves. Realistic estimate:
> **~800–900 MB reduction in rootfs partition size**, yielding a final `digits-pi-*.img.gz`
> that should compress to roughly **400–600 MB** (down from ~900+ MB).

---

## Notes and Caveats

1. **`dkms` and `wm8960-soundcard`**: The wm8960 source is in `/usr/src/` but `setup_wm8960.sh` is explicitly marked deprecated. Remove both. The Codec Zero (DA7212) uses a mainline dtoverlay (`dtoverlay=rpi-codeczero`), no dkms needed.

2. **`openocd` deps**: `openocd` is in CHROOT_PACKAGES and pulls in `libjaylink0`, `libjim0.81`, `libftdi1-2`, `libhidapi-hidraw0`, `libusb-1.0-0`. These should all stay. The Pi Zero 2 W acts as a Pico programmer — openocd must remain.

3. **`avahi-daemon`**: The task spec says digitsd needs avahi, but `go.mod` has no avahi import and there are no mDNS calls visible in the source. This may refer to mDNS for `.local` hostname resolution (via `libnss-mdns`). Verify with the running device before removing.

4. **`linux-image-6.12.47+rpt-rpi-v8`**: Keep. This is the actual kernel for Pi Zero 2 W. The `rpi-2712` variant is for Pi 5 only.

5. **`firmware-brcm80211`**: Must stay — it provides the BCM43430 Wi-Fi firmware. Removing it breaks Wi-Fi entirely.

6. **Pi-bluetooth firmware in brcm/**: The `BCM43430*.hcd` files can be removed since `dtoverlay=disable-bt` disables the BT radio at the firmware level. However, if the BT UART init fails silently, it won't break Wi-Fi, so this is a safe removal.

7. **`perl-base`**: Required by `dpkg`. Do not remove `perl-base` even though full `perl-modules-5.36` and `libperl5.36` can go.

8. **`systemd-rfkill`**: Already masked in `build-image.sh`. This prevents rfkill state file from re-blocking Wi-Fi on ro root reboot.

9. **Python3**: Only used by maintenance scripts (`mic_test.sh`, `test_audio.py`, etc.) in `pi/`. These are dev-time tools, not runtime deps. Safe to remove from production image.
