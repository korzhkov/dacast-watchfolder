# Dacast Watchfolder

Windows tray app that watches a local folder and uploads files to Dacast via multipart upload. Single portable `.exe`, no installer.

## Usage

1. Run `dacast-watchfolder.exe`
2. Enter Dacast API key and choose a watch folder
3. Settings are saved automatically when you start; watching auto-starts if configured
4. Closing the window hides to the system tray (right-click tray icon: Open / Start-Stop / Quit)

Data under `%AppData%\DacastWatchfolder\`:

| File | Purpose |
|------|---------|
| `config.json` | API key + watch folder |
| `state.db` | Upload status + part ETags (resume after crash / network blips) |
| `app.log` | Process log |

On start, files already in the folder are queued. Successful uploads leave files in place and skip them later when path + size + mtime match.

## Download

Get the latest `dacast-watchfolder.exe` from [Releases](https://github.com/korzhkov/dacast-watchfolder/releases).

Do not use the source ZIP from Code — binaries are published only via Releases.

## Build

Requires Go 1.22+ (no CGO). Manifest/icon are embedded via `cmd/watchfolder/rsrc.syso`.

**Windows:**

```bat
build.bat
```

**Linux / macOS** (cross-compiles Windows `.exe` by default):

```bash
chmod +x build.sh
./build.sh
```

Or manually:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui -s -w" -o dacast-watchfolder.exe ./cmd/watchfolder
```

## Release (maintainers)

CI builds on every push to `main`. To publish a new Release with the exe attached:

```bash
git tag v0.2.0
git push origin v0.2.0
```

The `Release` GitHub Action builds `dacast-watchfolder.exe` and uploads it to that tag’s release page.
