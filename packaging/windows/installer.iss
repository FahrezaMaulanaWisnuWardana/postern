; Build via scripts/release.py. AppId must remain stable across upgrades.
[Setup]
AppId=dev.{#AppSlug}.desktop
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppName} contributors
DefaultDirName={localappdata}\Programs\{#AppName}
DefaultGroupName={#AppName}
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir={#OutputDir}
OutputBaseFilename={#OutputName}
SetupIconFile={#IconFile}
UninstallDisplayIcon={app}\{#AppSlug}.exe
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
LicenseFile={#PayloadDir}\LICENSE
CloseApplications=yes
RestartApplications=no

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; Flags: unchecked

[Components]
Name: "app"; Description: "Desktop application"; Types: full compact custom; Flags: fixed
Name: "cli"; Description: "Command-line companion (not added to PATH)"; Types: full

[Types]
Name: "full"; Description: "Desktop and CLI"
Name: "compact"; Description: "Desktop only"
Name: "custom"; Description: "Custom"; Flags: iscustom

[Files]
Source: "{#PayloadDir}\{#AppSlug}.exe"; DestDir: "{app}"; Components: app; Flags: ignoreversion
Source: "{#PayloadDir}\{#AppSlug}-cli.exe"; DestDir: "{app}"; Components: cli; Flags: ignoreversion
Source: "{#PayloadDir}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#AppName}"; Filename: "{app}\{#AppSlug}.exe"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppSlug}.exe"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppSlug}.exe"; Description: "Launch {#AppName}"; Flags: nowait postinstall skipifsilent

; Deliberately no [UninstallDelete]: workspace data must survive uninstall.
