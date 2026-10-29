# payload

`make payload` fills this directory with what goes on a card (generated,
not in git):

- `files/`: the arm64 squirreld, the install scripts and the xfsprogs
  packages; on a Raspberry Pi card this is the `squirrel` folder on the
  boot partition.
- `armbian/`: for the Radxa ROCK 4C+ (Armbian): NetworkManager, xfsprogs,
  exfatprogs and their dependencies, the USB overlay and the first-start
  unit.
- `mac/`: `debugfs` and `e2fsck` from e2fsprogs (GPL-2), built for Apple
  silicon and Intel; the app writes Armbian's ext4 root with them.
