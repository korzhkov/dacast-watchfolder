@echo off
set CGO_ENABLED=0
where rsrc >nul 2>&1
if %ERRORLEVEL%==0 (
  if exist .\assets\dacast.ico copy /Y .\assets\dacast.ico .\cmd\watchfolder\app.ico >nul
  rsrc -manifest .\cmd\watchfolder\app.manifest -ico .\cmd\watchfolder\app.ico -o .\cmd\watchfolder\rsrc.syso
)
go build -ldflags="-H windowsgui -s -w" -o dacast-watchfolder.exe ./cmd/watchfolder
