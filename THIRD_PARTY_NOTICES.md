# Third-party notices

Squirrel itself is under the MIT license (see [LICENSE](LICENSE)). It is built
with, and the Squirrel Maker app carries, the work of others under their own
licenses, listed here. Each Go module's full license text is in its source,
which `go mod download` fetches (or see the links).

## Go modules in squirreld (the program on the Pi)

| Module | License |
|---|---|
| [github.com/cloudsoda/go-smb2](https://github.com/cloudsoda/go-smb2) | BSD-2-Clause, © 2016 Hiroshi Ioka |
| [github.com/cloudsoda/sddl](https://github.com/cloudsoda/sddl) | **LGPL-3.0** (see below) |
| [github.com/geoffgarside/ber](https://github.com/geoffgarside/ber) | BSD-3-Clause, © 2009 The Go Authors |
| [github.com/hashicorp/go-uuid](https://github.com/hashicorp/go-uuid) | MPL-2.0, © HashiCorp, Inc. |
| [github.com/jcmturner/aescts](https://github.com/jcmturner/aescts), [dnsutils](https://github.com/jcmturner/dnsutils), [goidentity](https://github.com/jcmturner/goidentity), [gokrb5](https://github.com/jcmturner/gokrb5), [rpc](https://github.com/jcmturner/rpc) | Apache-2.0 |
| [github.com/jcmturner/gofork](https://github.com/jcmturner/gofork) | BSD-3-Clause, © 2009 The Go Authors |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto), [x/net](https://pkg.go.dev/golang.org/x/net), [x/sys](https://pkg.go.dev/golang.org/x/sys) | BSD-3-Clause, © 2009 The Go Authors |

**LGPL-3.0 (cloudsoda/sddl).** Go links libraries into the program, so squirreld
contains this library. The LGPL lets you replace it with your own version:
the complete source of squirreld is in this repository, and `go build` with a
`replace github.com/cloudsoda/sddl => ../your-sddl` line in `go.mod` builds
squirreld with your version. The library's source and license text:
<https://github.com/cloudsoda/sddl>.

**MPL-2.0 (hashicorp/go-uuid)** is used unchanged; its source is at the link above.

## Go modules in Squirrel Maker (the Mac app)

| Module | License |
|---|---|
| [github.com/wailsapp/wails/v3](https://github.com/wailsapp/wails) | MIT, © 2018-present Lea Anthony |
| [github.com/adrg/xdg](https://github.com/adrg/xdg) | MIT, © 2014 Adrian-George Bostan |
| [github.com/mattn/go-isatty](https://github.com/mattn/go-isatty) | MIT, © Yasuhiro Matsumoto |
| [github.com/ulikunitz/xz](https://github.com/ulikunitz/xz) | BSD-3-Clause, © 2014-2022 Ulrich Kunitz |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | BSD-3-Clause, © 2009 The Go Authors |

## Programs and packages the Mac app carries

These are separate programs, put on the SD card or run as they are; they are
not part of Squirrel and keep their own licenses.

- **e2fsprogs 1.47.4** (`debugfs`, `e2fsck`, to write to the ext4 card of a
  Radxa ROCK 4C+): GPL-2.0. Built unchanged from the release at
  <https://mirrors.edge.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs/v1.47.4/>
  (the `Makefile` checks its SHA-256). That tarball is the complete source.
- **Debian packages** for a new card, put on it unchanged and installed at its
  first start: `xfsprogs`, `libinih1`, `liburcu8t64` and `python3-dbus` for
  Raspberry Pi OS (from Debian trixie for 64-bit, from Raspbian trixie for the
  Pi Zero W), and NetworkManager, `dnsmasq-base`, `exfatprogs`, `rfkill` and
  their dependencies for Armbian. Each keeps the license in its
  `/usr/share/doc/<package>/copyright`; the source of every version is at
  <https://snapshot.debian.org/> (Debian) and <https://archive.raspbian.org/>
  (Raspbian), or with `apt-get source <package>=<version>`. The exact versions
  are in the file names under `internal/makerui/payload/*/debs` after `make
  payload`.

## Fonts

[Barlow](https://github.com/jpt/barlow), © 2017 The Barlow Project Authors,
under the SIL Open Font License 1.1 (the full text is next to the fonts, in
`internal/web/static/fonts/OFL.txt` and `internal/makerui/static/fonts/OFL.txt`).

## Trademarks

Tesla is a trademark of Tesla, Inc. Squirrel is not made by, endorsed by or
affiliated with Tesla; the name is only used to say what Squirrel is for.
Raspberry Pi is a trademark of Raspberry Pi Ltd; Radxa and ROCK are trademarks
of Radxa.
