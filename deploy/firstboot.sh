#!/bin/bash
# First start of a card written by squirrel-maker. Runs from
# squirrel-firstboot.service at every boot until it has succeeded once,
# and again after an update was put on the boot partition.
#
# Steps: install xfsprogs from the packages on the boot partition (no
# network needed), create the XFS data partition, create the drive image
# for the car, install and start squirreld, and make root and boot
# read-only from the next start on.
#
# On a Raspberry Pi this lives in /boot/firmware/squirrel, the FAT
# partition, and the log can be read on any computer. On Armbian (Radxa
# ROCK 4C+) everything is on ext4, in /boot/squirrel; it also sets up the
# user, NetworkManager and the USB port there, copies its log to the drive
# for the car, and restarts once at the end.
set -euo pipefail

B=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ARMBIAN=""
[ -f /etc/armbian-release ] && ARMBIAN=1

# Once read-only, root and boot are made writable just for this run.
was_ro=()
for mp in / /boot/firmware; do
	if findmnt -n -o OPTIONS "$mp" | grep -qE '(^|,)ro(,|$)'; then
		mount -o remount,rw "$mp"
		was_ro+=("$mp")
	fi
done

exec >> "$B/firstboot.log" 2>&1
echo "=== $(date -Is) first start of Squirrel"

# The activity LED blinks fast while this runs and flickers if it fails;
# squirreld takes it over once it runs.
LED=""
for l in /sys/class/leds/ACT /sys/class/leds/led0 /sys/class/leds/blue:status; do
	[ -e "$l/trigger" ] && LED=$l && break
done
led() {
	[ -n "$LED" ] || return 0
	echo timer > "$LED/trigger"
	echo "$1" > "$LED/delay_on"
	echo "$2" > "$LED/delay_off"
}
led 120 120
on_exit() {
	local rc=$?
	if [ "$rc" -ne 0 ]; then
		led 40 40
		echo "=== $(date -Is) failed (exit $rc); will retry on the next start"
	fi
	[ -n "$ARMBIAN" ] && cam_log
	sync
	for mp in "${was_ro[@]}"; do
		mount -o remount,ro "$mp" || true
	done
	# The USB port's device mode and the read-only root need a restart. Only
	# now: the log is on the drive and root is read-only again.
	if [ -n "$ARMBIAN" ] && [ "$rc" -eq 0 ] && [ -e "$B/done" ]; then
		echo "restarting"
		systemctl --no-block reboot || true
	fi
}
trap on_exit EXIT

# Armbian keeps the log on ext4, which a Mac or PC cannot read; a copy goes
# on the drive for the car. The drive is taken from the car for that.
cam_log() {
	local cam=/backingfiles/cam_disk.bin udc
	[ -e "$cam" ] && command -v squirreld > /dev/null || return 0
	# configfs reports a size for every attribute; read it to see whether
	# the car has the drive. Unbinding twice fails, which is fine. (Before
	# an update the gadget may still have its old name.)
	for udc in /sys/kernel/config/usb_gadget/*/UDC; do
		if [ -n "$(cat "$udc" 2> /dev/null)" ]; then
			echo "" > "$udc" || true
		fi
	done
	squirreld put-file "$cam" squirrel-firstboot.txt "$B/firstboot.log" || true
	if [ ! -e "$B/done" ] && systemctl is-active -q squirreld; then
		systemctl restart squirreld || true
	fi
}

# Armbian as shipped: root has password 1234, the console logs in as root,
# and nothing of the user's settings is applied yet.
armbian_setup() {
	# Gone after the first successful run; an update does not need it.
	[ -f "$B/setup.env" ] || return 0
	# shellcheck source=/dev/null
	. "$B/setup.env"
	passwd -l root > /dev/null
	rm -f /etc/systemd/system/getty@.service.d/override.conf \
		/etc/systemd/system/serial-getty@.service.d/override.conf /root/.not_logged_in_yet
	mkdir -p /etc/ssh/sshd_config.d
	{
		echo "PermitRootLogin no"
		[ -n "$TUSB_PASSWORD_HASH" ] || echo "PasswordAuthentication no"
	} > /etc/ssh/sshd_config.d/squirrel.conf

	echo "$TUSB_HOSTNAME" > /etc/hostname
	hostname "$TUSB_HOSTNAME"
	sed -i '/^127\.0\.1\.1[[:space:]]/d' /etc/hosts
	printf '127.0.1.1\t%s\n' "$TUSB_HOSTNAME" >> /etc/hosts

	if ! id "$TUSB_USER" > /dev/null 2>&1; then
		local groups=() g
		for g in sudo adm netdev video plugdev dialout users; do
			getent group "$g" > /dev/null && groups+=("$g")
		done
		useradd -m -s /bin/bash -c Squirrel -G "$(IFS=,; echo "${groups[*]}")" "$TUSB_USER"
	fi
	if [ -n "$TUSB_PASSWORD_HASH" ]; then
		usermod -p "$TUSB_PASSWORD_HASH" "$TUSB_USER"
	else
		passwd -l "$TUSB_USER" > /dev/null
	fi
	echo "$TUSB_USER ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/squirrel
	chmod 0440 /etc/sudoers.d/squirrel
	if [ -n "$TUSB_SSH_KEY" ]; then
		local home
		home=$(getent passwd "$TUSB_USER" | cut -d: -f6)
		install -d -m 0700 -o "$TUSB_USER" -g "$TUSB_USER" "$home/.ssh"
		echo "$TUSB_SSH_KEY" > "$home/.ssh/authorized_keys"
		chown "$TUSB_USER:$TUSB_USER" "$home/.ssh/authorized_keys"
		chmod 0600 "$home/.ssh/authorized_keys"
	fi
	if [ -n "$TUSB_TIMEZONE" ] && [ -e "/usr/share/zoneinfo/$TUSB_TIMEZONE" ]; then
		ln -sf "/usr/share/zoneinfo/$TUSB_TIMEZONE" /etc/localtime
		echo "$TUSB_TIMEZONE" > /etc/timezone
	fi

	local env=/boot/armbianEnv.txt
	if ! grep -q 'ieee80211_regdom=' "$env"; then
		if grep -q '^extraargs=' "$env"; then
			sed -i "s/^extraargs=.*/& cfg80211.ieee80211_regdom=$TUSB_COUNTRY/" "$env"
		else
			echo "extraargs=cfg80211.ieee80211_regdom=$TUSB_COUNTRY" >> "$env"
		fi
	fi
	# The upper USB 3 port in device mode; takes effect after the restart.
	armbian-add-overlay "$B/dwc3-0-device.dts"

	# Without a network cable the start waits two minutes for one; the
	# other wants to write to the read-only root at every start.
	systemctl mask systemd-networkd-wait-online.service systemd-networkd-persistent-storage.service
	# These write to the card at every start or shutdown, which fails on a
	# read-only root; squirreld keeps the clock itself.
	local u
	for u in armbian-ramlog.service armbian-led-state.service fake-hwclock-load.service \
		fake-hwclock-save.service fake-hwclock-save.timer fake-hwclock.service; do
		systemctl disable "$u" 2> /dev/null || true
	done
}

# Wifi on Armbian goes through NetworkManager, as on the Pi. Ethernet stays
# with systemd-networkd, which Armbian set up.
armbian_wifi() {
	mkdir -p /etc/NetworkManager/conf.d
	printf '[keyfile]\nunmanaged-devices=interface-name:e*;interface-name:lan*;interface-name:wan*\n' \
		> /etc/NetworkManager/conf.d/squirrel.conf
	systemctl enable NetworkManager
	systemctl restart NetworkManager
	local i
	for i in $(seq 1 30); do
		nmcli -t general status > /dev/null 2>&1 && break
		sleep 1
	done
	# The networks come from setup.env, which is gone once a first start
	# succeeded.
	if ! declare -p TUSB_WIFI_SSID > /dev/null 2>&1; then
		echo "no setup.env; no wifi networks to add"
		return 0
	fi
	for i in "${!TUSB_WIFI_SSID[@]}"; do
		local ssid=${TUSB_WIFI_SSID[$i]} psk=${TUSB_WIFI_PSK[$i]}
		nmcli connection delete id "$ssid" > /dev/null 2>&1 || true
		if [ -n "$psk" ]; then
			nmcli connection add type wifi con-name "$ssid" ssid "$ssid" connection.autoconnect yes \
				wifi-sec.key-mgmt wpa-psk wifi-sec.psk "$psk"
		else
			nmcli connection add type wifi con-name "$ssid" ssid "$ssid" connection.autoconnect yes
		fi
	done
}

[ -n "$ARMBIAN" ] && armbian_setup

pkgs=(xfsprogs) cmds=(mkfs.xfs)
if [ -n "$ARMBIAN" ]; then
	pkgs+=(network-manager dnsmasq-base exfatprogs rfkill)
	cmds+=(nmcli dnsmasq mkfs.exfat rfkill)
fi
# missing: a tool is not there, or a package is unpacked but not set up
# (dpkg -i without its dependencies leaves the program, which then cannot
# find its libraries).
missing() {
	local c p
	for c in "${cmds[@]}"; do
		command -v "$c" > /dev/null || return 0
	done
	for p in "${pkgs[@]}"; do
		[ "$(dpkg-query -W -f '${db:Status-Status}' "$p" 2> /dev/null)" = installed ] || return 0
	done
	return 1
}
if missing; then
	# Offline first; the network is only a fallback. After a failed dpkg -i
	# apt calls the half installed package the newest and refuses to add
	# its dependencies, unless it is first told to repair (-f install, with
	# no package names).
	if ! dpkg -i "$B"/debs/*.deb; then
		echo "installing the bundled packages failed; trying apt"
		# This runs early: the wifi is set up but may not be connected yet.
		command -v nm-online > /dev/null && nm-online -q -t 120 || echo "no network yet; trying anyway"
		apt-get update -q && apt-get -f install -y -q && apt-get install -y -q "${pkgs[@]}"
	fi
fi
if missing; then
	echo "could not install ${pkgs[*]}; will retry on the next start"
	exit 1
fi
if [ -n "$ARMBIAN" ] && [ ! -e "$B/wifi-done" ]; then
	armbian_wifi
	touch "$B/wifi-done"
fi

install -m 0755 "$B/squirreld" /usr/local/bin/squirreld
# The data goes on the card, or, when the maker was told so, on an external
# USB drive (one that is empty or set up earlier; see setup-storage).
if [ "$(cat "$B/storage" 2> /dev/null)" = usb ]; then
	squirreld setup-storage -yes -usb
else
	squirreld setup-storage -yes -root-size 8G
fi

# squirreld was teslausbd before; its state keeps its place under the new
# name.
if [ -d /backingfiles/teslausbd ] && [ ! -e /backingfiles/squirreld ]; then
	mv /backingfiles/teslausbd /backingfiles/squirreld
fi
mkdir -p /backingfiles/squirreld
if [ -e "$B/config.json" ] && [ ! -e /backingfiles/squirreld/config.json ]; then
	install -m 0600 "$B/config.json" /backingfiles/squirreld/config.json
fi
# It holds the NAS password, and the boot partition is readable anywhere.
rm -f "$B/config.json"

if [ ! -e /backingfiles/cam_disk.bin ]; then
	squirreld create-image -size "$(cat "$B/cam-size" 2> /dev/null || echo 40%)" /backingfiles/cam_disk.bin
fi

bash "$B/install.sh" /usr/local/bin/squirreld

# Read-only root and boot: a power cut in the car cannot damage them.
# What changes all the time lives on /backingfiles or in RAM.
echo "making root and boot read-only from the next start"
# DNS: NetworkManager keeps a copy in /run.
if [ ! -L /etc/resolv.conf ]; then
	ln -sf /run/NetworkManager/resolv.conf /etc/resolv.conf
fi
# cloud-init has done its work; on a read-only root it would only fail.
[ -d /etc/cloud ] && touch /etc/cloud/cloud-init.disabled
# Swap in RAM only; no swap file on the card. Armbian already uses zram.
if [ -z "$ARMBIAN" ]; then
	mkdir -p /etc/rpi/swap.conf.d
	printf '[Main]\nMechanism=zram\n' > /etc/rpi/swap.conf.d/squirrel.conf
	rm -f /var/swap
fi
# The hotspot's DNS answers every name with the Pi (captive portal).
mkdir -p /etc/NetworkManager/dnsmasq-shared.d
echo "address=/#/10.42.0.1" > /etc/NetworkManager/dnsmasq-shared.d/squirrel-captive.conf
# Wifi networks live on /backingfiles. NetworkManager runs in its own
# mount namespace, so making root writable for a moment does not reach it.
# The card keeps a copy, used if /backingfiles cannot be mounted.
NMC=/etc/NetworkManager/system-connections
NMS=/backingfiles/system/nm-connections
if ! mountpoint -q "$NMC"; then
	# Networks set up through cloud-init live in netplan, which writes to
	# /etc/netplan; make them plain NetworkManager connections. (Armbian's
	# netplan file is for ethernet through systemd-networkd and stays.)
	shopt -s nullglob
	plans=(/etc/netplan/*.yaml)
	if [ -z "$ARMBIAN" ] && [ ${#plans[@]} -gt 0 ]; then
		for f in /run/NetworkManager/system-connections/netplan-*.nmconnection; do
			n=$(basename "$f")
			sed -E 's/^id=netplan-[^-]+-/id=/' "$f" > "$NMC/${n#netplan-}"
			chmod 600 "$NMC/${n#netplan-}"
		done
		mkdir -p /etc/netplan.before-readonly
		mv "${plans[@]}" /etc/netplan.before-readonly/
	fi
	shopt -u nullglob
	mkdir -p "$NMS"
	chmod 700 "$NMS"
	cp -an "$NMC"/. "$NMS"/
	mount --bind "$NMS" "$NMC"
	nmcli connection reload || true
fi
# fstab: ro for / and /boot/firmware, RAM for what is written all the time.
[ -e /etc/fstab.before-readonly ] || cp /etc/fstab /etc/fstab.before-readonly
awk 'BEGIN { OFS = "\t" }
	!/^#/ && ($2 == "/" || $2 == "/boot/firmware") && $4 !~ /(^|,)ro(,|$)/ { $4 = $4 ",ro" }
	{ print }' /etc/fstab > /etc/fstab.new
for t in "/tmp size=64M,mode=1777" "/var/tmp size=16M,mode=1777" "/var/log size=32M,mode=0755" \
	"/var/lib/NetworkManager size=4M,mode=0700"; do
	set -- $t
	grep -qE "^tmpfs[[:space:]]+$1[[:space:]]" /etc/fstab.new ||
		printf 'tmpfs\t%s\ttmpfs\tnosuid,nodev,%s\t0\t0\n' "$1" "$2" >> /etc/fstab.new
done
grep -qE "^$NMS[[:space:]]" /etc/fstab.new ||
	printf '%s\t%s\tnone\tbind,nofail,x-systemd.requires-mounts-for=/backingfiles,x-systemd.before=NetworkManager.service\t0\t0\n' \
		"$NMS" "$NMC" >> /etc/fstab.new
# /backingfiles' log in fewer, larger writes (cards set up before this
# have noatime,nofail only); takes effect at the next start.
awk 'BEGIN { OFS = "\t" }
	!/^#/ && $2 == "/backingfiles" && $3 == "xfs" && $4 !~ /logbsize=/ { $4 = $4 ",logbsize=256k" }
	{ print }' /etc/fstab.new > /etc/fstab.new2 && mv /etc/fstab.new2 /etc/fstab.new
findmnt --verify --tab-file /etc/fstab.new
mv /etc/fstab.new /etc/fstab

# Write-back in steady, bounded steps. By default the kernel may hold 20%
# of memory in unwritten data (some 700 MB on a 4 GB board) and then flush
# it all at once, which stalls everything else on the card. On a 512 MB Pi
# this is about the default.
cat > /etc/sysctl.d/90-squirrel.conf << 'EOF2'
# Written by Squirrel's first start: write to the card in bounded steps.
vm.dirty_background_bytes = 16777216
vm.dirty_bytes = 67108864
vm.dirty_expire_centisecs = 1000
EOF2

touch "$B/done"
echo "=== $(date -Is) done. Web UI: http://$(hostname).local/"
if [ -n "$ARMBIAN" ]; then
	# The passwords are in NetworkManager and /etc/shadow now. on_exit
	# restarts.
	rm -f "$B/setup.env"
fi
