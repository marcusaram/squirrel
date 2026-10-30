VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"

# arm64: Pi Zero 2 W / 3 / 4 / 5 and Rock Pi 4 with a 64-bit OS.
# armv7: Pi Zero 2 W / 3 / 4 with 32-bit Raspberry Pi OS.
# armv6: the original Pi Zero W.
.PHONY: build
build: dist/squirreld-linux-arm64 dist/squirreld-linux-armv7 dist/squirreld-linux-armv6

dist/squirreld-linux-arm64: FORCE
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o $@ ./cmd/squirreld
dist/squirreld-linux-armv7: FORCE
	GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) -o $@ ./cmd/squirreld
dist/squirreld-linux-armv6: FORCE
	GOOS=linux GOARCH=arm GOARM=6 $(GOBUILD) -o $@ ./cmd/squirreld

# A tarball per architecture with the binary, unit file and installer.
.PHONY: release
release: build
	for a in arm64 armv7 armv6; do \
		d=dist/squirreld-$$a; rm -rf $$d; mkdir -p $$d; \
		install -m 0755 dist/squirreld-linux-$$a $$d/squirreld; install -m 0755 deploy/install.sh $$d/; install -m 0644 deploy/squirreld.service LICENSE THIRD_PARTY_NOTICES.md $$d/; \
		tar -C dist -czf dist/squirreld-$(VERSION)-$$a.tar.gz squirreld-$$a; rm -rf $$d; \
	done

.PHONY: test
test:
	go vet ./...
	go test ./...

# Runs the tests on Linux on a real XFS filesystem, so reflinks are
# exercised. Needs Docker with privileged containers.
.PHONY: test-linux
test-linux:
	mkdir -p dist/test
	for p in fatfs preserve gadget archive driveimage nbd ext4; do GOOS=linux go test -c -o dist/test/$$p.test ./internal/$$p; done
	docker run --rm --privileged -v "$(CURDIR):/src:ro" debian:trixie-slim sh -c '\
		apt-get update -qq >/dev/null && apt-get install -y -qq xfsprogs exfatprogs e2fsprogs dosfstools >/dev/null 2>&1; \
		truncate -s 1G /x.img && mkfs.xfs -q /x.img && mkdir /xfs && mount -o loop /x.img /xfs && \
		cd /src/internal/fatfs && /src/dist/test/fatfs.test && \
		cd /src/internal/gadget && /src/dist/test/gadget.test -test.v -test.run Mounted && \
		cd /src/internal/archive && TBX_TEST_DIR=/xfs /src/dist/test/archive.test -test.v && \
		cd /src/internal/driveimage && /src/dist/test/driveimage.test -test.v && \
		cd /src/internal/preserve && TBX_TEST_DIR=/xfs TBX_EXPECT_REFLINK=1 /src/dist/test/preserve.test -test.v && \
		for i in 0 1 2 3; do [ -e /dev/nbd$$i ] || mknod /dev/nbd$$i b 43 $$((i*32)); done && \
		cd /src/internal/nbd && /src/dist/test/nbd.test -test.v && \
		cd /src/internal/ext4 && /src/dist/test/ext4.test -test.v'

# Validates the generated cloud-init files against a real Raspberry Pi OS
# image (uncompressed .img), e.g. PIOS_IMG=~/Downloads/2026-10-06-raspios-trixie-arm64-lite.img
.PHONY: test-image
test-image:
	rm -rf dist/ci && mkdir -p dist/ci
	OUT=$(CURDIR)/dist/ci go test -run TestWriteFiles ./internal/maker/
	docker run --rm --privileged -v "$(PIOS_IMG):/pios.img:ro" -v "$(CURDIR)/dist/ci:/ci:ro" \
		-v "$(CURDIR)/scripts/validate-cloudinit.sh:/v.sh:ro" debian:trixie-slim sh /v.sh

.PHONY: testimages
testimages:
	docker run --rm --privileged -v "$(CURDIR)/scripts:/scripts:ro" -v "$(CURDIR)/internal/fatfs/testdata:/out" \
		debian:trixie-slim sh -c 'apt-get update -qq >/dev/null && apt-get install -y -qq exfatprogs dosfstools fdisk >/dev/null 2>&1 && sh /scripts/mktestimages.sh /out'

# squirrel-maker (macOS) carries everything a new card needs: the arm64
# daemon, the install scripts, and the packages each OS lacks. apt in a
# Debian trixie container fetches and verifies them.
#  - files/: the squirrel folder of a Raspberry Pi OS card, with
#    the packages for mkfs.xfs.
#  - files-armv6/: the same for 32-bit Raspberry Pi OS on the original
#    Pi Zero W: an ARMv6 daemon and Raspbian's packages. The Raspbian key
#    in deploy/rpi-armv6 comes from the pinned 32-bit image.
#  - armbian/: what differs for Armbian (Radxa ROCK 4C+): NetworkManager,
#    xfsprogs, exfatprogs and their dependencies, worked out against the
#    package list of the pinned Armbian image (deploy/armbian/dpkg-status),
#    the USB overlay and the unit.
PAYLOAD := internal/makerui/payload
XFS_DEBS := xfsprogs libinih1 liburcu8t64 python3-dbus
# download fetches packages into the current directory in a container,
# trying three times, and fails unless every one of them is there: a card
# missing one falls back to apt over wifi at its first start, which takes
# a Pi Zero W a quarter of an hour.
download = ok=; for i in 1 2 3; do rm -f ./*.deb; apt-get update -qq >/dev/null && \
	apt-get download -qq $(1) && ok=1 && break; sleep 5; done; \
	[ -n "$$ok" ] || { echo "downloading $(1) failed" >&2; exit 1; }; \
	for p in $(foreach p,$(1),$(firstword $(subst :, ,$(p)))); do \
	ls "$${p}"_*.deb >/dev/null 2>&1 || { echo "$${p} missing" >&2; exit 1; }; done; ls
ARMBIAN_PKGS := network-manager dnsmasq-base xfsprogs exfatprogs rfkill

# payload needs Docker; CI builds it on Linux and the Mac app around it on
# a Mac.
.PHONY: payload
payload: dist/squirreld-linux-arm64 dist/squirreld-linux-armv6
	rm -rf $(PAYLOAD)/files $(PAYLOAD)/files-armv6 $(PAYLOAD)/armbian
	mkdir -p $(PAYLOAD)/files/debs $(PAYLOAD)/files-armv6/debs $(PAYLOAD)/armbian/debs
	cp dist/squirreld-linux-arm64 $(PAYLOAD)/files/squirreld
	cp deploy/install.sh deploy/firstboot.sh deploy/squirreld.service deploy/squirrel-firstboot.service $(PAYLOAD)/files/
	echo $(VERSION) > $(PAYLOAD)/files/version
	docker run --rm --platform linux/arm64 -v "$(CURDIR)/$(PAYLOAD)/files/debs:/out" debian:trixie-slim sh -c '\
		cd /out && $(call download,$(XFS_DEBS))'
	cp dist/squirreld-linux-armv6 $(PAYLOAD)/files-armv6/squirreld
	cp deploy/install.sh deploy/firstboot.sh deploy/squirreld.service deploy/squirrel-firstboot.service $(PAYLOAD)/files-armv6/
	echo $(VERSION) > $(PAYLOAD)/files-armv6/version
	docker run --rm --platform linux/arm64 -v "$(CURDIR)/deploy/rpi-armv6/sources.d:/etc/apt/sources.list.d:ro" \
		-v "$(CURDIR)/deploy/rpi-armv6/raspbian-archive-keyring.gpg:/raspbian-archive-keyring.gpg:ro" \
		-v "$(CURDIR)/$(PAYLOAD)/files-armv6/debs:/out" debian:trixie-slim sh -c '\
		dpkg --add-architecture armhf && cd /out && $(call download,$(addsuffix :armhf,$(XFS_DEBS)))'
	cp deploy/armbian/dwc3-0-device.dts deploy/armbian/squirrel-firstboot.service $(PAYLOAD)/armbian/
	docker run --rm --platform linux/arm64 -v "$(CURDIR)/deploy/armbian/dpkg-status:/status:ro" \
		-v "$(CURDIR)/$(PAYLOAD)/armbian/debs:/out" debian:trixie-slim sh -c '\
		set -e; for i in 1 2 3; do apt-get update -qq >/dev/null && break; sleep 5; done; \
		cp /status /var/lib/dpkg/status; mkdir -p /tmp/a/partial; \
		apt-get install -qq -y --download-only --no-install-recommends -o Dir::Cache::archives=/tmp/a $(ARMBIAN_PKGS) >/dev/null; \
		cp /tmp/a/*.deb /out/ && ls /out'

# Squirrel Maker, the Mac app: its own window, Dock icon and menu, built
# with Wails (cgo, so Xcode's command line tools). One binary for Apple
# silicon and Intel.
APP := dist/Squirrel Maker.app
APPCGO := CGO_ENABLED=1 CGO_CFLAGS=-mmacosx-version-min=13.0 CGO_LDFLAGS=-mmacosx-version-min=13.0

# Signing: by default ad hoc, which runs on this Mac but makes others warn.
# For a release, MAC_SIGN is a Developer ID Application certificate in the
# keychain and NOTARY the credentials for Apple's notary service (see
# scripts/notarize.sh), e.g.
#   make app MAC_SIGN="Developer ID Application: Name (TEAMID)" NOTARY="--keychain-profile squirrel"
MAC_SIGN ?= -
NOTARY ?=
ifeq ($(MAC_SIGN),-)
MAC_SIGNFLAGS :=
else
MAC_SIGNFLAGS := --options runtime --timestamp
endif
MAC_CODESIGN = codesign --force $(MAC_SIGNFLAGS) --sign "$(MAC_SIGN)"

.PHONY: app app-bundle
app: payload
	$(MAKE) app-bundle

# app-bundle builds the app around the payload that is already there.
app-bundle:
	GOOS=darwin GOARCH=arm64 $(APPCGO) CC="clang -arch arm64" go build -trimpath -ldflags "$(LDFLAGS)" -o dist/squirrel-app-darwin-arm64 ./cmd/squirrel-app
	GOOS=darwin GOARCH=amd64 $(APPCGO) CC="clang -arch x86_64" go build -trimpath -ldflags "$(LDFLAGS)" -o dist/squirrel-app-darwin-amd64 ./cmd/squirrel-app
	rm -rf "$(APP)" && mkdir -p "$(APP)/Contents/MacOS" "$(APP)/Contents/Resources"
	lipo -create -output "$(APP)/Contents/MacOS/squirrel-app" dist/squirrel-app-darwin-arm64 dist/squirrel-app-darwin-amd64
	sed 's/@VERSION@/$(VERSION)/' deploy/app/Info.plist > "$(APP)/Contents/Info.plist"
	cp deploy/app/Squirrel.icns "$(APP)/Contents/Resources/"
	cp LICENSE THIRD_PARTY_NOTICES.md "$(APP)/Contents/Resources/"
	$(MAC_CODESIGN) "$(APP)"
	cd dist && rm -f "Squirrel-Maker-$(VERSION).zip" && ditto -c -k --keepParent "Squirrel Maker.app" "Squirrel-Maker-$(VERSION).zip"
ifneq ($(NOTARY),)
	scripts/notarize.sh "dist/Squirrel-Maker-$(VERSION).zip" $(NOTARY)
	xcrun stapler staple "$(APP)"
	cd dist && rm -f "Squirrel-Maker-$(VERSION).zip" && ditto -c -k --keepParent "Squirrel Maker.app" "Squirrel-Maker-$(VERSION).zip"
endif

# The same maker on the command line, without cgo or Wails: it serves the
# page on localhost and opens it in the browser. One binary for Apple
# silicon and Intel.
.PHONY: maker maker-bin
maker: payload
	$(MAKE) maker-bin

maker-bin:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/squirrel-maker-darwin-arm64 ./cmd/squirrel-maker
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/squirrel-maker-darwin-amd64 ./cmd/squirrel-maker
	lipo -create -output dist/squirrel-maker dist/squirrel-maker-darwin-arm64 dist/squirrel-maker-darwin-amd64
	$(MAC_CODESIGN) --identifier io.github.marcusaram.squirrel.maker-cli dist/squirrel-maker
	cd dist && rm -f "squirrel-maker-$(VERSION)-macos.zip" && zip -q "squirrel-maker-$(VERSION)-macos.zip" squirrel-maker
ifneq ($(NOTARY),)
	scripts/notarize.sh "dist/squirrel-maker-$(VERSION)-macos.zip" $(NOTARY)
endif

# Squirrel Maker for Windows (64-bit): the app (Wails, without cgo, so it
# builds anywhere) and the command line version, in one zip. Each gets a
# Windows resource (go-winres, pinned in go.mod) with the squirrel icon,
# a manifest and the version; the app's manifest asks for administrator
# rights when it starts, which writing a card needs.
WIN := dist/windows/Squirrel Maker
# The file version must be numbers: 0.16.0 from v0.16.0-rc1, 0.0.0 from dev.
WINVER = $(shell echo "$(VERSION)" | sed -E -e 's/^v?([0-9]+(\.[0-9]+){0,3}).*/\1/' -e t -e 's/.*/0.0.0/')
WINRES = go tool go-winres simply --arch amd64 --icon cmd/squirrel-app/icon.png --product-name "Squirrel Maker" \
	--product-version "$(VERSION)" --file-version "$(WINVER)" --copyright "Marcus Aram, MIT license"

.PHONY: app-windows app-windows-bundle
app-windows: payload
	$(MAKE) app-windows-bundle

app-windows-bundle:
	rm -rf "$(WIN)" && mkdir -p "$(WIN)"
	$(WINRES) --manifest gui --admin --file-description "Squirrel Maker" \
		--original-filename "Squirrel Maker.exe" --out cmd/squirrel-app/rsrc
	$(WINRES) --manifest cli --file-description "Squirrel Maker (command line)" \
		--original-filename squirrel-maker.exe --out cmd/squirrel-maker/rsrc
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o "$(WIN)/Squirrel Maker.exe" ./cmd/squirrel-app
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(WIN)/squirrel-maker.exe" ./cmd/squirrel-maker
	cp LICENSE THIRD_PARTY_NOTICES.md "$(WIN)/"
	rm -f "dist/Squirrel-Maker-$(VERSION)-windows.zip"
	cd dist/windows && zip -qr "../Squirrel-Maker-$(VERSION)-windows.zip" "Squirrel Maker"

.PHONY: clean
clean:
	rm -rf dist $(PAYLOAD)/files $(PAYLOAD)/files-armv6 $(PAYLOAD)/armbian

FORCE:
