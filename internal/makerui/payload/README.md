# payload

`make payload` fills this directory with what goes on a card (generated,
not in git):

- `files/`: the arm64 squirreld, the install scripts and the xfsprogs
  packages; on a Raspberry Pi card this is the `squirrel` folder on the
  boot partition.
- `files-armv6/`: the same for 32-bit Raspberry Pi OS on the original Pi
  Zero W: an ARMv6 squirreld and Raspbian's packages.
- `armbian/`: for the Radxa ROCK 4C+ (Armbian): NetworkManager, xfsprogs,
  exfatprogs and their dependencies, the USB overlay and the first-start
  unit.

