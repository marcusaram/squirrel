#!/bin/sh
# Checks the boot files squirrel-maker generates with the cloud-init and
# netplan inside a real Raspberry Pi OS image: the schema, and netplan
# generate, which is the step that fails on the Pi when network-config is
# wrong. Run through `make test-image PIOS_IMG=...`.
set -e
off=$((1064960*512))
loop=$(losetup -f --show -r -o "$off" /pios.img)
mkdir -p /r && mount -o ro "$loop" /r
mount -t proc proc /r/proc
mount -t tmpfs tmpfs /r/run
mount -t tmpfs tmpfs /r/tmp
mount --bind /ci /r/mnt
trap 'umount /r/mnt /r/proc /r/run /r/tmp /r; losetup -d "$loop"' EXIT
chroot /r cloud-init schema -c /mnt/user-data 2>&1 | grep -v WARNING
chroot /r cloud-init schema -t network-config -c /mnt/network-config 2>&1 | grep -v WARNING
chroot /r sh -c 'mkdir -p /tmp/np/etc/netplan && cp /mnt/network-config /tmp/np/etc/netplan/50-cloud-init.yaml &&
	chmod 600 /tmp/np/etc/netplan/50-cloud-init.yaml && netplan generate --root-dir /tmp/np'
echo "netplan generate: ok"
