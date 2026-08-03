# Dacast Watchfolder

Windows tray app that watches a local folder and uploads files to Dacast via multipart upload. Single portable `.exe`, no installer.

## Build

Requires Go 1.22+ (no CGO). The Windows Common Controls v6 manifest is embedded via `cmd/watchfolder/rsrc.syso` (regenerate with `rsrc` if you change `app.manifest`):

```bat
build.bat
```

Or:

```bat
set CGO_ENABLED=0
go build -ldflags="-H windowsgui -s -w" -o dacast-watchfolder.exe ./cmd/watchfolder
```

## Usage

1. Run `dacast-watchfolder.exe`
2. Enter Dacast API key and choose a watch folder
3. **Save settings**, then **Start watching**
4. Closing the window hides to the system tray (right-click tray icon: Open / Start-Stop / Quit)

Data under `%AppData%\DacastWatchfolder\`:

| File | Purpose |
|------|---------|
| `config.json` | API key + watch folder |
| `state.db` | Upload status + part ETags (resume after crash / network blips) |
| `app.log` | Process log |

On start, files already in the folder are queued. Successful uploads leave files in place and skip them later when path + size + mtime match.
