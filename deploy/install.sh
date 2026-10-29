#!/bin/bash
# Installs squirreld on a Raspberry Pi or another Linux board with a USB
# device controller. Run as root from the directory with the binary:
#
#   sudo ./install.sh [path/to/squirreld]
#
# On a Pi that runs teslausb, the existing /backingfiles and cam_disk.bin
# are reused and teslausb's own services are switched off. Nothing in
# /backingfiles is deleted.
set -euo pipefail

BIN=${1:-"$(dirname "$0")/squirreld"}
UNIT="$(dirname "$0")/squirreld.service"
CONFIG=/backingfiles/squirreld/config.json

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run this as root (sudo)"
[ -x "$BIN" ] || die "binary not found: $BIN"
[ -f "$UNIT" ] || die "unit file not found: $UNIT"
"$BIN" version > /dev/null || die "$BIN does not run on this machine (wrong architecture?)"

if ! mountpoint -q /backingfiles; then
	say "/backingfiles is not set up yet; looking for room for an XFS data partition"
	command -v mkfs.xfs > /dev/null || apt-get install -y xfsprogs || die "could not install xfsprogs"
	"$BIN" setup-storage || die "no data partition. A card made with Squirrel Maker sets this up by itself."
fi
fstype=$(findmnt -n -o FSTYPE /backingfiles)
case "$fstype" in
	xfs | btrfs) say "/backingfiles is $fstype: clips are reflinked, no extra space or writes" ;;
	*) say "warning: /backingfiles is $fstype; clips will be copied. XFS is strongly recommended." ;;
esac

# teslausb keeps the root filesystem read-only.
if findmnt -n -o OPTIONS / | grep -qE '(^|,)ro(,|$)'; then
	say "remounting / read-write for the install"
	mount -o remount,rw /
	trap 'sync; mount -o remount,ro / || true' EXIT
fi

if [ -e /lib/systemd/system/teslausb.service ] || [ -e /etc/systemd/system/teslausb.service ]; then
	say "switching off teslausb (its files in /backingfiles stay where they are)"
	systemctl disable --now teslausb.service || true
	pkill -f /root/bin/archiveloop || true
fi
# teslausb mounts the drive images on the Pi while archiving. squirreld
# refuses to hand a mounted image to the car, so unmount them here.
for img in /backingfiles/*_disk.bin; do
	[ -e "$img" ] || continue
	for dev in $(losetup -n -O NAME -j "$img"); do
		for m in $(findmnt -rn -o TARGET -S "$dev") $(findmnt -rn -o TARGET -S "${dev}p1"); do
			say "unmounting $m ($img)"
			umount "$m"
		done
		losetup -d "$dev" || true
	done
done
# teslausb links its site as nginx's default site.
if [[ "$(readlink -f /etc/nginx/sites-enabled/default 2> /dev/null)" == *teslausb* ]]; then
	say "switching off teslausb's nginx web UI (squirreld serves port 80)"
	systemctl disable --now nginx.service || true
fi
# teslausb mounts its snapshots through autofs.
if [ -e /etc/auto.master.d/teslausb.autofs ]; then
	say "switching off teslausb's snapshot automounter"
	systemctl disable --now autofs.service || true
fi

# squirreld was called teslausbd before; take over from it. Its settings
# and state move to /backingfiles/squirreld at squirreld's first start.
if [ -e /etc/systemd/system/teslausbd.service ]; then
	say "replacing teslausbd, the earlier name of squirreld"
	systemctl disable --now teslausbd.service || true
	rm -f /etc/systemd/system/teslausbd.service /usr/local/bin/teslausbd /etc/modules-load.d/teslausbd.conf \
		/etc/NetworkManager/dnsmasq-shared.d/teslausb-captive.conf /etc/rpi/swap.conf.d/teslausb.conf
fi

if [ "$(readlink -f "$BIN")" != /usr/local/bin/squirreld ]; then
	install -m 0755 "$BIN" /usr/local/bin/squirreld
fi
install -m 0644 "$UNIT" /etc/systemd/system/squirreld.service
# Copied from the FAT boot partition by cards made before this, it came
# out executable, which systemd warns about at every start.
[ -f /etc/systemd/system/squirrel-firstboot.service ] && chmod 0644 /etc/systemd/system/squirrel-firstboot.service
mkdir -p /etc/modules-load.d
echo libcomposite > /etc/modules-load.d/squirreld.conf

# Raspberry Pi: the USB port must run in device mode.
model=$({ tr -d '\0' < /proc/device-tree/model; } 2> /dev/null || true)
if [[ "$model" == Raspberry* ]]; then
	cfg=/boot/firmware/config.txt
	[ -f "$cfg" ] || cfg=/boot/config.txt
	if [ -f "$cfg" ] && ! grep -q '^dtoverlay=dwc2' "$cfg"; then
		cp "$cfg" "$cfg.before-squirreld"
		echo "dtoverlay=dwc2" >> "$cfg"
		say "added dtoverlay=dwc2 to $cfg (backup: $cfg.before-squirreld); reboot needed"
	fi
	grep -qx dwc2 /etc/modules-load.d/squirreld.conf || echo dwc2 >> /etc/modules-load.d/squirreld.conf
fi
if [ -z "$(ls /sys/class/udc 2>/dev/null)" ]; then
	say "warning: no USB device controller in /sys/class/udc yet. On a Pi this appears after a reboot;"
	say "         on other boards the OTG port must be set to peripheral or otg mode in the device tree."
fi

if [ ! -e /backingfiles/cam_disk.bin ]; then
	say "creating the drive for the car on 40% of /backingfiles"
	/usr/local/bin/squirreld create-image -size 40% /backingfiles/cam_disk.bin
fi

# Clips that only exist in old teslausb snapshots. Importing reflinks them
# into the archive; the snapshots themselves are left alone.
shopt -s nullglob
snaps=(/backingfiles/snapshots/snap-*/snap.bin)
if [ ${#snaps[@]} -gt 0 ]; then
	say "preserving clips from ${#snaps[@]} teslausb snapshot(s)"
	systemctl stop squirreld.service 2> /dev/null || true
	/usr/local/bin/squirreld import -config "$CONFIG" "${snaps[@]}" || say "import had problems; see above"
	say "imported snapshots are deleted automatically, oldest first, when space runs low."
	say "to free the space right away once you have checked the clips in the web UI:"
	say "  sudo rm -rf /backingfiles/snapshots"
fi

systemctl daemon-reload
systemctl enable squirreld.service
systemctl restart squirreld.service
sleep 2
systemctl --no-pager --lines=5 status squirreld.service || true

ip=$(hostname -I 2> /dev/null | awk '{print $1}')
say "done. Web UI: http://${ip:-$(hostname)}/"
