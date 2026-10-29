#!/usr/bin/env bash
# Builds a demo cam_disk.bin with synthetic dashcam footage, for working on
# the web UI without a car. Needs ffmpeg and Docker.
#   scripts/mkdemo.sh OUTDIR
set -eu

out=${1:?usage: mkdemo.sh OUTDIR}
mkdir -p "$out/files"
cd "$out/files"

cams="front back left_repeater right_repeater left_pillar right_pillar"

clip() { # clip DIR TIMESTAMP CAM HUE
	mkdir -p "$1"
	f="$1/$2-$3.mp4"
	[ -e "$f" ] && return
	ffmpeg -loglevel error -f lavfi -i "testsrc=size=480x360:rate=12" -t 60 \
		-vf "hue=h=$4" \
		-c:v libx264 -preset veryfast -crf 34 -pix_fmt yuv420p "$f"
}

segments() { # segments DIR DATE START_HHMM COUNT
	i=0
	h=${3%??}
	m=${3#??}
	while [ $i -lt "$4" ]; do
		mm=$(printf %02d $(((10#$m + i) % 60)))
		hh=$(printf %02d $((10#$h + (10#$m + i) / 60)))
		hue=0
		for c in $cams; do
			clip "$1" "$2_$hh-$mm-00" "$c" $hue
			hue=$((hue + 60))
		done
		i=$((i + 1))
	done
}

segments TeslaCam/SentryClips/2026-10-27_22-14-38 2026-10-27 2205 4
printf '{"timestamp":"2026-10-27T22:08:38","city":"Utrecht","est_lat":"52.0907","est_lon":"5.1214","reason":"sentry_aware_object_detection","camera":"0"}' \
	> TeslaCam/SentryClips/2026-10-27_22-14-38/event.json
ffmpeg -loglevel error -y -f lavfi -i "testsrc2=size=240x180" -frames:v 1 TeslaCam/SentryClips/2026-10-27_22-14-38/thumb.png

segments TeslaCam/SavedClips/2026-10-28_08-31-02 2026-10-28 0829 2
printf '{"timestamp":"2026-10-28T08:30:40","city":"Amersfoort","reason":"user_interaction_dashcam_icon_tapped","camera":"0"}' \
	> TeslaCam/SavedClips/2026-10-28_08-31-02/event.json
ffmpeg -loglevel error -y -f lavfi -i "testsrc2=size=240x180" -vf hue=h=200 -frames:v 1 TeslaCam/SavedClips/2026-10-28_08-31-02/thumb.png

segments TeslaCam/RecentClips 2026-10-28 1802 5

docker run --rm --privileged -v "$out:/work" debian:trixie-slim sh -c '
	set -e
	apt-get update -qq >/dev/null && apt-get install -y -qq exfatprogs fdisk >/dev/null 2>&1
	img=/work/cam_disk.bin
	rm -f $img
	truncate -s 2G $img
	echo "start=2048, type=7" | sfdisk -q $img
	loop=$(losetup -f --show -o 1048576 $img)
	mkfs.exfat -L CAM $loop >/dev/null
	mkdir -p /mnt/cam && mount $loop /mnt/cam
	cp -r /work/files/. /mnt/cam/
	umount /mnt/cam && losetup -d $loop
	echo built $img'
