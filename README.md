# Squirrel

[![CI](https://github.com/marcusaram/squirrel/actions/workflows/ci.yml/badge.svg)](https://github.com/marcusaram/squirrel/actions/workflows/ci.yml)

Squirrel keeps every clip your Tesla's dashcam records.

A small computer (a Raspberry Pi or a Radxa ROCK 4C+) sits between the car and its USB port. To the car it is an ordinary USB drive, but Squirrel keeps a copy of everything the car records, including the drives the car deletes again after an hour and what it records while parked. At home the clips go to your NAS, and you watch them in a browser with all cameras in sync, the car's speed, pedals and steering alongside.

Squirrel is a replacement for [teslausb](https://github.com/marcone/teslausb), written as one Go program. It is not made by or affiliated with Tesla.

- [What you need](#what-you-need)
- [Getting started](#getting-started)
- [Using Squirrel](#using-squirrel)
- [Troubleshooting](#troubleshooting)
- [How it works](#how-it-works)
- [Boards and storage](#boards-and-storage)
- [Switching from teslausb](#switching-from-teslausb)
- [Development](#development)
- [Project status](#project-status)
- [License](#license)

## What you need

- **A board that can act as a USB drive:**
  - Raspberry Pi **Zero 2 W**: the usual choice, small, powered by the car through the same cable.
  - Raspberry Pi **4** or **5**: faster, and can keep the clips on a USB SSD.
  - Original Raspberry Pi **Zero W**: works, but slowly (one core, 2.4 GHz wifi only).
  - Radxa **ROCK 4C+**: fast (six cores), and can keep the clips on a USB SSD. Needs its own power next to the cable from the car.

  A Raspberry Pi 3 Model B or B+ cannot act as a USB drive and does not work.
- **A good SD card** of 32 GB or more: A2 or V30, or one sold for dashcams ("High Endurance"). The car writes all the time.
- **A USB cable** from the car to the board's data port (see [Connecting to the car](#3-connect-it-to-the-car)).
- **A Mac** (macOS 13 or later) **or a Windows PC** (Windows 10 or 11, 64-bit) to make the card with Squirrel Maker, with a card reader.
- Optional: **a NAS** with an SMB share (Synology, QNAP, a Windows share, Samba) for the clips to go to at home.

Tesla puts speed, pedals, steering and position into every frame from firmware 2025.44.25 on, on HW3 and later. Older cars still record normally; the player then shows video only.

## Getting started

### 1. Get Squirrel Maker

Squirrel Maker is the app that makes the SD card, for macOS and Windows. Download it from the [releases](https://github.com/marcusaram/squirrel/releases), or build it yourself (see [Development](#development)):

- **macOS:** `Squirrel-Maker-<version>.zip`, with the app inside. It is not signed with an Apple developer certificate: the first time, macOS says it cannot check the developer. Open **System Settings → Privacy & Security** and choose **Open Anyway** below the message, or right-click the app and choose **Open**.
- **Windows:** `Squirrel-Maker-<version>-windows.zip`. Unpack it and start `Squirrel Maker.exe`. It asks for administrator rights, which writing a card needs. It is not signed either: if Windows says it protected your PC, choose **More info** and then **Run anyway**. The app uses Microsoft Edge WebView2, which Windows 10 and 11 normally have.

Squirrel Maker also runs from the command line (`squirrel-maker`, on Windows `squirrel-maker.exe` from a terminal opened as administrator), with the same steps in your browser.

### 2. Make the card

Put the SD card in your computer, open Squirrel Maker and follow the seven steps:

1. **Board**: which board the card is for.
2. **Wifi**: your home network (for the NAS) and perhaps your phone's hotspot, and your country. A Pi Zero W only sees 2.4 GHz networks. Also the password for the Pi's own wifi, which it starts when it finds no known network (see [Wifi in the car](#wifi-in-the-car)).
3. **Log in**: the Pi's name on the network (`squirrel` by default, so `http://squirrel.local/`), a user, and a password and/or your SSH key.
4. **NAS and storage**: the NAS (optional, can be set later), how much of the card the car gets, and on a Pi 4, Pi 5 or ROCK 4C+ whether the clips go on a USB drive instead.
5. **Build image**: downloads the operating system once (checked against its checksum), and puts Squirrel and your settings in it. By default it uses the release tested with Squirrel; newer ones are offered, marked as untested.
6. **SD card**: pick the card. Only external disks and card readers are listed, never the disk your computer runs from; your Mac asks for your password (Windows asked when the app started), and everything written is read back and checked.
7. **Done**.

The app remembers your answers for the next card, except passwords.

### 3. Connect it to the car

Put the card in the board and connect it to one of the car's USB ports that the car records to (in most Teslas the ports in the glovebox or the centre console; check your car's manual).

| Board | Cable from the car goes to |
|---|---|
| Pi Zero 2 W, Zero W | the micro-USB port marked **USB** (the middle one), not PWR. It also powers the Pi. |
| Pi 4, Pi 5 | the **USB-C** port. It also powers the Pi. |
| ROCK 4C+ | the **upper blue USB 3 port**, with a USB-A to USB-A (or A to C) cable. Power goes separately to the USB-C port. |

The first start sets everything up and takes a few minutes, longer on a Zero W. The LED blinks fast meanwhile. The board then restarts by itself if it needs to, and the car sees a new USB drive. The drive already has the `TeslaCam` folder the car looks for, so the car starts recording; turn on Sentry Mode in the car if you want it.

You can do the first start at home on any USB charger: everything the board needs is on the card, so it does not need internet. Connect it to the car afterwards.

### 4. Open the web interface

On the same network as the board, open `http://squirrel.local/` (or the name you chose; or the board's IP address, which your router shows). In the car, join the board's own wifi (see below).

## Using Squirrel

### Clips

The **Clips** page lists Sentry events, saved clips and drives (consecutive minutes form one drive), per day, with a thumbnail.

The player shows all cameras in sync, around a picture of the car you can tap to choose a camera:

- **One camera** shows the chosen camera large; **All cameras** shows them together.
- Next to the video: speed, gear, Autopilot, the turn signals, throttle and brake, the steering wheel as it was turned, and the route. These come from the data Tesla puts in every frame (see [What you need](#what-you-need)); they move in step with the video.
- Each camera plays as one continuous video for the whole drive, so there are no pauses between the car's one-minute files. Where the car did not record (for example while parked), the timeline shows a gap.
- **Space** plays and pauses, the **arrow keys** jump 5 seconds, and the speed can be 0.5× to 4×.
- **Download video** saves the chosen camera as one MP4; **Download all** saves the drive's original files as a zip.
- **Delete** removes a drive or event from the board. It is not taken from the car's drive again.

Sentry events start just before the moment the car noticed something.

### Status and maintenance

The **Status** page shows whether the car is using the drive and how fast it writes, the storage, what was kept, NAS uploads, temperature, memory and wifi.

Under **Maintenance**:

- **Check and repair** the USB drive's file system. The drive leaves the car for about a minute.
- **Change the car's share** of the storage. The drive leaves the car briefly; its clips are kept first, and the car then starts with an empty drive of the new size.
- **Computer mode** (see below).
- **Restart**.

### Settings

- **Wifi**: add and remove networks, with a search for networks in reach.
- **What to keep**: drives, saved clips, Sentry and track mode. Turn off drives to keep only events.
- **NAS**: server, share, folder, user and password, with a connection test, and which kinds of clips to upload.
- **Diagnostics**: off by default (see [Troubleshooting](#troubleshooting)).
- **Password** for the web interface (optional).

### Wifi in the car

When the board sees no known network for a minute and a half, it starts its own wifi, called **Squirrel**, with the password you set in Squirrel Maker (also shown under Settings). Join it with your phone and the web interface opens by itself. When nobody has used it for four minutes, the board looks for known networks again, so at home it joins your wifi and uploads to the NAS.

### NAS uploads

At home the board uploads to the NAS: saved clips, Sentry and track mode by default, drives if you turn them on. An upload goes to a temporary name and is renamed only when it is complete and the size matches, so an interrupted upload is redone later and the NAS never has half a file. Clips land in `SentryClips/…`, `SavedClips/…` and `RecentClips/<date>/…`, the same layout as teslausb.

When the board runs short of space, it removes what is already on the NAS first, then the oldest drives, then track mode, Sentry and saved clips last. It always keeps a reserve free (10 GB by default), because the car stops recording when its drive is full.

### Computer mode

Instead of the car, you can plug the board into a computer: under **Status → Maintenance**, switch on **Computer mode**. The board then shows itself as a read-only USB drive named Squirrel, with a folder per drive and event and every camera as one long video, plus each event's details and thumbnail. Copying over USB is much faster than over wifi.

The car's drive is disconnected meanwhile, nothing is deleted for space, and a restart always returns to car mode. Choose **Back to car mode** before you put the board back in the car.

### What the LED means

| LED (the blue one on the ROCK 4C+) | Meaning |
|---|---|
| a short flash every 2 seconds | ready, waiting for the car |
| a heartbeat (a double tick, 40 to 60 a minute, quicker when busy) | the car is using the drive |
| fast blinking | first start in progress, or uploading to the NAS, or repairing |
| very fast flicker | something needs attention: the first start failed, or the USB drive does not work |

### Updating

Put the card in your computer, open Squirrel Maker and choose **Update it** on the first page. It shows the Squirrel version on each card and puts this version on the one you pick. The board installs it at its next start; settings, wifi networks and kept clips stay.

## Troubleshooting

**The LED flickers very fast after the first start.** The first start failed and will try again at the next start. Put the card in your computer and open `squirrel/firstboot.log` on it (on a ROCK 4C+ the log is also put on the USB drive as `squirrel-firstboot.txt`). If the card was made with an older Squirrel Maker, update or remake it with the latest.

**The car does not see the drive.** Check that the cable goes to the board's data port (see [Connect it to the car](#3-connect-it-to-the-car)) and is a data cable, not a charging-only one. The Status page shows the USB state; "configured" means the car has the drive. After a cold start the car can take a minute to notice a new drive.

**`squirrel.local` does not open.** Your computer or phone may not support `.local` names on that network; use the board's IP address from your router. In the car, join the board's own wifi.

**The end of a drive seems to be missing.** The car deletes what it recorded while parked as soon as it drives off, and does not record at all for a moment when switching between driving and parked. Squirrel takes each recording within seconds of the car finishing it, and if the car deletes one first, recovers it from the drive (see [How it works](#how-it-works)). The log under **Log** says "rescued a deleted recording" when that happened, or why it could not.

**The web interface is slow on a Pi Zero W.** It has one slow core; the Zero 2 W is several times faster. Opening a long drive for the first time takes a moment while the board joins the minutes into one video.

**Diagnostics.** Under Settings, diagnostics keeps the system log on the board and writes a report of the previous run to the USB drive at each start, as `squirrel-log.txt`, readable on any computer. It writes to the card, so it is off by default.

## How it works

```
            USB (mass storage)                    /backingfiles (XFS)
  Car   ───────────────────────►  cam_disk.bin  ──────────────┐
                                     ▲  reads exFAT itself,   │ reflink of the
                                     │  no mount              │ blocks of each clip
                                 squirreld ───────────────────┤
                                     │                        ▼
                     web UI :80 ◄────┤                  archive/
                                     └──── SMB ────►  NAS   SentryClips/…
                                                            SavedClips/…
                                                            RecentClips/<date>/…
```

1. **The drive for the car.** The board's USB port is put in device mode, and `cam_disk.bin`, a file on the board's XFS partition, is offered to the car as a USB drive (through the kernel's USB gadget, set up right after start, before anything else).
2. **Keeping clips.** Every 5 seconds squirreld reads the drive's exFAT file system straight from that file, with its own parser, without mounting it. A recording is taken as soon as it is complete: an MP4 is only valid once the car has written its index at the end. Its clusters are reflinked into a separate file in the archive, and the directory entry is read again afterwards; if it changed meanwhile, the copy is thrown away.
3. **Why that costs nothing.** A reflink shares blocks: no data is copied and nothing extra is written to the card. When the car later deletes a recording and writes there again, XFS gives the car new blocks (copy-on-write) and the kept clip holds on to the old ones.
4. **Recordings the car deletes quickly.** What the car records while parked, it deletes as soon as it drives off, sometimes within a minute. Squirrel remembers where each recording it has not kept yet starts on the drive. If one disappears, it reads it from there: the MP4 says how long it is, all its clusters must still be free, and its video track must be whole. Only then is it kept.
5. **Long videos.** For the player and computer mode, squirreld joins a drive's minute files into one MP4 per camera while it is being read: a new header is built from the files' own frame tables, and the frames are read from the original files, unchanged. Nothing is copied or re-encoded.
6. **A read-only system.** After the first start, the system on the card is mounted read-only, so a power cut in the car cannot damage it. What changes lives on the XFS partition or in memory: settings, wifi networks, the saved clock; logs and temporary files are in RAM; swap is compressed RAM.

### Planning space

The car eventually fills its whole drive. What is left on the data partition, minus the reserve, is for kept clips. With a 256 GB card, 100 GB for the car and a 10 GB reserve, about 140 GB is left for the archive. How much an hour of driving takes depends on the number of cameras; after a few drives the Status page shows it.

### The SD card

Squirrel limits writes to the card: nothing on the system is written, logs stay in RAM, unwritten data is flushed in small steps, `/backingfiles` is mounted with `noatime`, and once a week, while the car does not use its drive, the free space is passed on to the card (TRIM). On a Zero W or Zero 2 W the card runs at about 20 MB/s; Squirrel Maker can optionally raise its clock to 100 MHz (off by default, beyond the SD standard: a card that cannot cope gives errors and can lose recordings).

## Boards and storage

### Pi Zero W

The original Zero W gets 32-bit Raspberry Pi OS with an ARMv6 build of squirreld and its packages from Raspbian. It works the same way, but its single core makes the web interface, joining long videos and NAS uploads slower, and its wifi is 2.4 GHz only.

### Radxa ROCK 4C+

The ROCK 4C+ gets [Armbian](https://www.armbian.com/) (Debian trixie, minimal). There is no stable Armbian release for this board, only community builds; Squirrel Maker uses one that was checked. Armbian keeps everything on ext4, which macOS and Windows cannot write, so Squirrel Maker writes its files itself, without mounting the card: it only touches a file system that is clean and uses ext4 features it knows, and reads everything back afterwards. Updating such a card asks for your password. At the end of the first start the board restarts once; the car then sees the drive. Ethernet stays with Armbian's own network setup; NetworkManager handles wifi and the hotspot. The USB device mode follows the [teslausb wiki](https://github.com/marcone/teslausb/wiki/Rock-Pi-4C-plus-Installation).

### Clips on a USB drive

On a Pi 4, Pi 5 or ROCK 4C+ the clips can live on an external USB drive (an SSD) instead of the card: tick the option in Squirrel Maker. Connect the drive to a USB port other than the one for the car.

- The first start uses the one USB drive that is empty (no partitions, no file system) or was set up for Squirrel before. A drive with anything on it is left alone, as is everything when two drives are connected; the first-start log says which drive it saw. To use a drive that holds data, erase it on purpose over SSH: `sudo squirreld setup-storage -usb-device /dev/sdX`.
- If the drive is missing at a start, squirreld waits for it and the car sees no drive until it is back; the LED flickers.
- Some drives need more power than the car's port gives through the board. A drive with its own power, or a powered hub, avoids that.

## Switching from teslausb

Squirrel can take over a Pi that runs teslausb, keeping its `/backingfiles` and `cam_disk.bin`; the car does not notice.

1. Build the program (`make release`, see [Development](#development)) and copy `squirreld-<version>-arm64.tar.gz` to the Pi (`armv7` for a 32-bit system: `uname -m` says `armv7l`).
2. Look first; this changes nothing and can run alongside teslausb: `./squirreld inspect /backingfiles/cam_disk.bin`.
3. Install: `tar xzf squirreld-*-arm64.tar.gz && cd squirreld-arm64 && sudo ./install.sh`. This turns off teslausb, its nginx and its automounter, installs squirreld, takes over your NAS settings from `/root/teslausb_setup_variables.conf` (CIFS only), and keeps the clips from teslausb's snapshots by reflink, without extra space.

Nothing in `/backingfiles` is deleted. Back to teslausb: `sudo systemctl disable --now squirreld && sudo systemctl enable --now teslausb nginx`.

On a Pi without teslausb, `install.sh` sets up a data partition if there is free space after the system. Usually there is none (the system has grown over the whole card); then make a card with Squirrel Maker instead.

## Development

You need Go, Docker (to fetch the Debian packages the card needs), and for the Mac app Xcode's command line tools.

```bash
make test         # unit tests (macOS or Linux)
make test-linux   # the same in a Linux container on real XFS, with reflinks
make build        # squirreld for arm64, armv7 and armv6
make release      # squirreld archives with the install scripts
make app          # dist/Squirrel Maker.app and its zip (Apple silicon and Intel)
make maker        # dist/squirrel-maker, the command line version
make app-windows  # dist/Squirrel-Maker-<version>-windows.zip: app and command line for Windows
```

`make app` and `make maker` build in two steps, which CI runs on different machines: `make payload` (squirreld, and the packages a new card installs at its first start, fetched with Docker so that the first start needs no internet), then `make app-bundle` or `make maker-bin` around it.

To run squirreld on your computer against a demo image with fake footage from six cameras:

```bash
scripts/mkdemo.sh /tmp/demo
go run ./cmd/squirreld run -config dev.json -web-dir internal/web/static
```

The exFAT and FAT32 parser is tested against images made with the Linux drivers (`make testimages` rebuilds them), including fragmented files and half-written entries; the keep test checks that kept clips survive the whole image being overwritten.

### CI and releases

GitHub Actions tests every push to `main` and every pull request: the Go tests on Linux (also on a real XFS filesystem, for reflinks) and on macOS, formatting, squirreld for every board, Squirrel Maker, and the web pages' scripts.

A release is a version tag:

```bash
git tag v0.16.0
git push origin v0.16.0
```

The release workflow then builds squirreld for every board, Squirrel Maker as a Mac app and as a command line program (with the packages a new card needs), and publishes them with their SHA-256 checksums as a GitHub release. A tag with a suffix, such as `v0.16.0-rc1`, becomes a pre-release.

Dependabot proposes newer Go modules and GitHub Actions once a week, as a pull request that CI tests.

### Commands

```
squirreld run [-config FILE]             the daemon (how the service runs it)
squirreld inspect IMAGE                  show the clips in an image, read-only
squirreld import IMAGE...                keep clips from old images or snapshots
squirreld create-image -size 64G|40% [-fs exfat|fat32] IMAGE
squirreld setup-storage [-root-size 8G]  the XFS data partition in free space on the card
```

The settings are in `/backingfiles/squirreld/config.json`, on the data partition, so they stay writable with a read-only system.

### Translations

The web interface and Squirrel Maker speak English and Dutch, following the browser's language, with a switch at the top. English is the source language in the code; Dutch lives in `i18n.js` next to each app, keyed by the English text. A missing translation shows the English.

## Project status

Used in a car with a Raspberry Pi Zero 2 W: recording, keeping drives and events, the web interface and player, computer mode on a Mac, and updating a card with Squirrel Maker. Uploading to a NAS is tested against a real Samba server, including one that disappears mid-upload, but not yet from a car.

Tested on a Pi Zero W: making the card, the first start and the web interface; not yet in a car.

Squirrel Maker for Windows is tested on GitHub's Windows machines with a virtual disk as the card (listing, writing, inspecting and updating a Raspberry Pi card), not yet with a real card reader. How it changes a card's file systems (FAT32 for the Raspberry Pi, ext4 for the ROCK 4C+) is the same code on macOS and Windows, tested against fsck.fat, e2fsck and the Linux kernel.

Tested in containers and with test images only, not yet on the hardware:

- the ROCK 4C+ (the maker's changes to the real Armbian image, the first start, a full start with a read-only system);
- clips on a USB SSD (with a second loop device as the drive);
- the Pi 4 and Pi 5.

Not built yet: managing music, lightshow and boombox drives in the web interface, keeping the car awake while uploading, and upload targets other than SMB.

## License

Squirrel is under the [MIT license](LICENSE). It uses and carries the work of others under their own licenses, listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

Tesla is a trademark of Tesla, Inc. Squirrel is not made by, endorsed by or affiliated with Tesla.
