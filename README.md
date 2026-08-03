# Dacast Watchfolder

Windows tray app that watches a local folder and uploads new files to Dacast via multipart upload. One portable `.exe`, no installer.

### What it does

- Watches a folder you choose and uploads every new file to Dacast as VOD
- On startup, also queues files already in the folder that were not uploaded yet
- Survives short network drops: retries the failed chunk and continues the same multipart upload
- Survives app restart / crash mid-upload: resumes from already uploaded parts (progress is stored locally)
- Leaves files on disk after a successful upload; the same file is not uploaded again unless it changes
- Runs in the system tray; watching starts automatically if API key and folder are already saved

## Usage

1. Run `dacast-watchfolder.exe`
2. Enter your Dacast API key and choose a watch folder
3. Settings are saved when you start; next launches auto-start watching if configured
4. Closing the window hides to the tray (right-click: Open / Start-Stop / Quit)

Local data lives in `%AppData%\DacastWatchfolder\`:

| File | Purpose |
|------|---------|
| `config.json` | API key and watch folder |
| `state.db` | Upload status and part ETags (needed for resume) |
| `app.log` | Process log |

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
