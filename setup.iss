[Setup]
; Basic App Info
AppName=Android ADB Bridge
AppVersion=5.0.9
AppPublisher=nuken
DefaultDirName={autopf}\AndroidBridge
DisableProgramGroupPage=yes
; The icon for the installer itself
SetupIconFile=icon.ico
; Where the final setup.exe will be saved
OutputDir=Output
OutputBaseFilename=AndroidBridge_Setup_v5.0.9
Compression=lzma
SolidCompression=yes
; Require admin rights to add firewall rules
PrivilegesRequired=admin

[Files]
; Grab the compiled Go app
Source: "AndroidBridge.exe"; DestDir: "{app}"; Flags: ignoreversion

; Grab the required ADB binaries so the bridge works natively on any PC
Source: "adb.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "AdbWinApi.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "AdbWinUsbApi.dll"; DestDir: "{app}"; Flags: ignoreversion
; Grab FFmpeg for local USB capture card support
Source: "ffmpeg.exe"; DestDir: "{app}"; Flags: ignoreversion
; Grab the icon
Source: "icon.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
; Desktop and Start Menu shortcuts now pass the -ui parameter to open the browser
Name: "{autodesktop}\Android ADB Bridge"; Filename: "{app}\AndroidBridge.exe"; Parameters: "-ui"; IconFilename: "{app}\icon.ico"
Name: "{autoprograms}\Android ADB Bridge"; Filename: "{app}\AndroidBridge.exe"; Parameters: "-ui"; IconFilename: "{app}\icon.ico"

; Auto-start the app silently in the background when Windows boots (NO -ui parameter here!)
Name: "{commonstartup}\Android ADB Bridge"; Filename: "{app}\AndroidBridge.exe"; IconFilename: "{app}\icon.ico"

[Run]
; 1. Add Windows Defender Firewall exception silently
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall add rule name=""Android ADB Bridge"" dir=in action=allow program=""{app}\AndroidBridge.exe"" enable=yes"; Flags: runhidden

; 2. Launch the background service immediately after installation
Filename: "{app}\AndroidBridge.exe"; Description: "Start Background Service"; Flags: nowait postinstall skipifsilent

; 3. Open the Dashboard in the user's browser
Filename: "{app}\AndroidBridge.exe"; Parameters: "-ui"; Description: "Open Dashboard"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Clean up the firewall rule if the user uninstalls the app
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""Android ADB Bridge"" program=""{app}\AndroidBridge.exe"""; Flags: runhidden; RunOnceId: "RemoveFirewallRule"

[Code]
// This runs right before the installation starts
function InitializeSetup(): Boolean;
var
  ResultCode: Integer;
begin
  // Forcefully kill the bridge, ADB, and FFmpeg (and any child processes) silently
  Exec('taskkill.exe', '/F /IM AndroidBridge.exe /T', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('taskkill.exe', '/F /IM adb.exe /T', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('taskkill.exe', '/F /IM ffmpeg.exe /T', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Result := True;
end;

// This runs right before the uninstallation starts
function InitializeUninstall(): Boolean;
var
  ResultCode: Integer;
begin
  // Ensure everything is stopped so the folders can be successfully deleted
  Exec('taskkill.exe', '/F /IM AndroidBridge.exe /T', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('taskkill.exe', '/F /IM adb.exe /T', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('taskkill.exe', '/F /IM ffmpeg.exe /T', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Result := True;
end;