# Packaging

## Scoop (Windows)

Users can install directly from this repo — no bucket needed:

```powershell
scoop install https://raw.githubusercontent.com/JorMath/mc-tui-server/main/packaging/scoop/mc-tui-server.json
```

The manifest has `checkver`/`autoupdate` wired to GitHub releases and the
`checksums.txt` asset, so `scoop update mc-tui-server` picks up new tags.
After each release, refresh the pinned `version`/`url`/`hash` here (or run
`scoop-autoupdate` tooling) so fresh installs get the latest version too.

## winget (Windows)

winget requires the manifests to live in the community repo. To publish a
version:

1. Update `PackageVersion`, `InstallerUrl` and `InstallerSha256` in the
   three files under `packaging/winget/` (the sha256 is in the release's
   `checksums.txt`, uppercase it).
2. Fork <https://github.com/microsoft/winget-pkgs> and copy the three files
   to `manifests/j/JorMath/mc-tui-server/<version>/`.
3. Open a PR; validation bots run automatically. Once merged:
   `winget install mc-tui-server`.

`wingetcreate update JorMath.mc-tui-server -u <installer-url> -v <version> --submit`
automates steps 1–3 once the package exists.
