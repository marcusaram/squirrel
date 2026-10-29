#!/bin/sh
# Builds the small FAT32/exFAT test images in internal/fatfs/testdata.
# Needs root and loop devices; run it through `make testimages`, which uses
# a privileged Debian container.
set -eu

out=${1:-/out}
mkdir -p "$out"

populate() {
	m=$1
	mkdir -p "$m/TeslaCam/RecentClips" "$m/TeslaCam/SavedClips/2026-10-27_08-15-00" \
		"$m/TeslaCam/SentryClips/2026-10-28_18-49-12" "$m/TeslaTrackMode"
	for cam in front back left_repeater right_repeater; do
		head -c 70001 /dev/urandom > "$m/TeslaCam/RecentClips/2026-10-28_18-40-00-$cam.mp4"
		head -c 3333 /dev/urandom > "$m/TeslaCam/SentryClips/2026-10-28_18-49-12/2026-10-28_18-48-00-$cam.mp4"
	done
	printf '{"timestamp":"2026-10-28T18:49:12","city":"Utrecht","reason":"sentry_aware_object_detection","camera":"5"}' \
		> "$m/TeslaCam/SentryClips/2026-10-28_18-49-12/event.json"
	head -c 1500 /dev/urandom > "$m/TeslaCam/SentryClips/2026-10-28_18-49-12/thumb.png"
	# Interleaved appends fragment both files.
	i=0
	while [ $i -lt 25 ]; do
		head -c 6000 /dev/urandom >> "$m/TeslaCam/SavedClips/2026-10-27_08-15-00/2026-10-27_08-14-00-front.mp4"
		head -c 5000 /dev/urandom >> "$m/TeslaCam/SavedClips/2026-10-27_08-15-00/2026-10-27_08-14-00-back.mp4"
		i=$((i + 1))
	done
	# Enough long names to need a multi-cluster directory.
	i=0
	while [ $i -lt 300 ]; do
		printf 'x%s' $i > "$m/TeslaTrackMode/lap-telemetry-with-a-rather-long-file-name-$i.csv"
		i=$((i + 1))
	done
	# A deleted file must not show up.
	head -c 9000 /dev/urandom > "$m/TeslaCam/RecentClips/deleted.mp4"
	rm "$m/TeslaCam/RecentClips/deleted.mp4"
	: > "$m/empty.txt"
	head -c 100 /dev/urandom > "$m/UPPER.TXT"
	sync
}

build() {
	name=$1 fstype=$2 mkfs=$3
	img=/tmp/$name.img
	rm -f "$img"
	truncate -s 48M "$img"
	echo "start=2048, type=$fstype" | sfdisk -q "$img"
	loop=$(losetup -f --show -o 1048576 "$img")
	sh -c "$mkfs $loop" > /dev/null
	mkdir -p /mnt/t
	mount "$loop" /mnt/t
	populate /mnt/t
	(cd /mnt/t && find . -type f | sed 's|^\./||' | sort | while read -r f; do
		printf '%s\t%s\t%s\n' "$f" "$(stat -c %s "$f")" "$(sha256sum "$f" | cut -d' ' -f1)"
	done) > "$out/$name.manifest"
	umount /mnt/t
	losetup -d "$loop"
	gzip -9 -c "$img" > "$out/$name.img.gz"
	echo "built $name"
}

build exfat 7 "mkfs.exfat -c 4K -L CAM"
build fat32 c "mkfs.vfat -F 32 -s 8 -n CAM"
