# Throne Tachyon Edition

Fork of [throneproj/Throne](https://github.com/throneproj/Throne) with an optional
[tachyon-core](https://github.com/Dushnilin/tachyon-core) backend. Built-in sing-box
remains the default and keeps the original Throne feature set.

## Switching cores

Open **Preferences > Basic Settings > Core > Proxy core**, choose **sing-box
(built-in)** or **tachyon-core (experimental)**, save and restart Throne. Profiles
and routing settings are retained. Switching back to sing-box requires no import
or conversion. A missing or failing Tachyon binary reports an error; Throne never
silently falls back to a different core.

Tachyon support is pinned to **v0.1.1**, using its sing-box JSON compatibility.
Desktop build archives bundle a verified binary for Windows x64, Linux x64/ARM64
and macOS x64/ARM64. Windows ARM64, legacy OS builds and Android do not bundle
Tachyon; built-in sing-box remains available. For a source build, download it with
`python script/download_tachyon.py --os windows --arch amd64 --dest <app-directory>`
(replace OS/architecture as appropriate), or select an absolute executable path
in the same settings dialog. An empty path uses `tachyon-core[.exe]` beside the GUI.

The current external backend supports local SOCKS/HTTP inbounds, system proxy,
Tachyon-compatible outbounds/routing/DNS, configuration checks, URL latency and
speed tests, live connection inspection and closing connections. URL tests use Tachyon,
including tests of profiles that are not running. No test runs a hidden sing-box
data plane when Tachyon is selected.

**Experimental backend limitations:** TUN/transparent interception, Xray sidecars
and Xray full configs, Extra Core, Throne auto-selectors, OpenVPN/OpenConnect,
kill switch, dashboards, IP tests and per-profile traffic history are not
available. Select built-in sing-box for these features. The Tachyon Clash API
exposes live connections and process totals, but no lossless per-outbound history
or recently closed connection ring; profile counters are deliberately not filled
with estimates. Native-compatible VLESS links (including REALITY) imported while
Tachyon is selected use native profiles, bypassing the Xray preference. Existing
Xray profiles and links requiring Xray-specific features still require built-in
sing-box; profiles are not rewritten on switching. Config validation and runtime errors come from Tachyon itself;
the GUI's bundled JSON schema still describes Throne's sing-box dialect.

The RPC bridge keeps Throne's private config files, environment filtering and
privilege drop for external processes. Tachyon's authenticated control listener
is bound to a random loopback port; the user's dashboard listeners are replaced
only in its temporary runtime config. Source profiles are not rewritten.
Throne's default DNS resolver objects are converted to equivalent resolver tags
for v0.1.1. Resolver objects with non-default strategy/cache/TTL/subnet options
are rejected explicitly because that release reads only tags; use built-in
sing-box for those options. Text DNS records are encoded as equivalent wire
records, preserving their names, types, values and TTL. Ordinary DNS server and
routing settings are retained. Speed tests reuse Throne's downloader/Speedtest
measurement code through an authenticated loopback SOCKS listener in Tachyon,
with each test routed to its exact outbound. No sing-box data plane is created.
For a custom download URL such as Cloudflare `__down`, select **Simple download**
in the general test settings; the ordinary download/upload modes use Speedtest.
HTTP errors and broken response bodies are reported as failures. A successful
payload sample may stop at the configured test duration before a large file
finishes. Cloudflare `__down` requests include the site Referer required for
large downloads.

The updater checks this fork's releases. Upstream installers and package
repositories below install original Throne, not Tachyon Edition.

The `Tachyon backend checks` workflow runs adapter tests and real CLI/RPC proxy
smoke tests on Windows and Linux. `Throne build matrix` includes the verified
Tachyon binary in supported desktop archives and preserves the built-in core.
The original AUR and signed package repository publication jobs only run in the
upstream repository.

## Original Throne

Qt based Desktop cross-platform GUI proxy utility, empowered by [Sing-box](https://github.com/SagerNet/sing-box)

Supports Windows 11/10/8/7 / Linux / MacOS out of the box.

<img width="1002" height="789" alt="image" src="https://github.com/user-attachments/assets/af4a8e32-7e55-430c-9402-ec2d665cf71a" />

### Note on MacOS releases
Apple platforms have a very strict security policy and since Throne does not have a signed certificate, you will have to remove the quarantine using `xattr -d com.apple.quarantine /path/to/throne.app`. Move `Throne.app` to `/Applications` before the first launch — the built-in privilege escalation opens `Terminal` to make the core setuid-root, and that step can fail while the app is still inside `~/Downloads`.

### GitHub Releases (Portable ZIP)

[![GitHub All Releases](https://img.shields.io/github/downloads/throneproj/Throne/total?label=downloads-total&logo=github&style=flat-square)](https://github.com/throneproj/Throne/releases)

# Linux CLI installer
```bash
curl -fsSL https://raw.githubusercontent.com/throneproj/Throne/dev/script/install_linux.py | sudo python3
```

### RPM repository
[Throne RPM repository](https://parhelia512.github.io/) for Fedora/RHEL and openSUSE/SLE.

## Supported protocols

- SOCKS
- HTTP(S)
- Shadowsocks
- Trojan
- VMess
- VLESS
- TUIC
- Hysteria
- Hysteria2
- AnyTLS
- Mieru
- Snell
- NaïveProxy
- Juicity
- TrustTunnel
- ShadowTLS
- Wireguard
- AmneziaWG
- MASQUE
- SSH
- Xray VLESS
- OpenVPN/OpenConnect
- Custom Outbound (Both Sing-box and Xray)
- Custom Config (Both Sing-box and Xray)
- Chaining outbounds
- Extra Core

## Subscription Formats

Various formats are supported, including share links, various JSON representation of Sing-box configs, and v2rayN link format as well as limited support for Shadowsocks and Clash formats.

Deeplinks are also supported, read the [documentation](https://throneproj.github.io/advanced/deeplinks/) for more information.

## Credits

- [SagerNet/sing-box](https://github.com/SagerNet/sing-box)
- [XTLS/Xray-core](https://github.com/xtls/xray-core)
- [Qv2ray](https://github.com/Qv2ray/Qv2ray)
- [Qt](https://www.qt.io/)
- [simple-protobuf](https://github.com/tonda-kriz/simple-protobuf)
- [fkYAML](https://github.com/fktn-k/fkYAML)
- [quirc](https://github.com/dlbeer/quirc)
- [srombauts/sqlitecpp](https://github.com/srombauts/sqlitecpp)

## FAQ
**How does this project differ from the original Nekoray?** <br/>
Nekoray's developer partially abandoned the project on December of 2023, some minor updates were done recently but the project is now officially archived. This project was meant to continue the way of the original project, with lots of improvements, tons of new features and also, removal of obsolete features and simplifications.

**Why does my Anti-Virus detect Throne and/or its Core as malware?** <br/>
Throne's built-in update functionallity downloads the new release, removes the old files and replaces them with the new ones, which is quite simliar to what malwares do, remove your files and replace them with an encrypted version of your files.

**Is setting the `SUID` bit really needed on Linux?** <br/>
To create and manage a system TUN interface, root access is required, without it, you will have to grant the Core some `Cap_xxx_admin` and still, need to enter your password 3 to 4 times per TUN activation. You can also opt to disable the automatic privilege escalation in `Basic Settings`->`Security`, but note that features that require root access will stop working unless you manually grant the needed permissions.

**Why does my internet stop working after I force quit Throne?** <br/>
If Throne is force-quit while `System proxy` is enabled, the process ends immediately and Throne cannot reset the proxy. <br/>
Solution:
- Always close Throne normally.
- If you force quit by accident, open Throne again, enable `System proxy`, then disable it- this will reset the settings.

**Where are the downloadable route profiles/rulesets coming from?**<br/>
They are located at the [routeprofiles](https://github.com/throneproj/routeprofiles) repository.

**How does "Throne-\<version\>-debian-system-qt-x64.deb" differ from "Throne-\<version\>-debian-x64.deb" and why is the latter 3 times heavier then the former?**<br/>
The first one does not pack the Qt libraries and relies on those installed on the host. The second one packs everything needed with itself, thus being heavier. The reason the first one exists is that on legacy systems provided Qt libraries use unsupported system features. If a graphical interface fails to load for your system, you may try to download the system-qt version and install fitting Qt libraries from your package manager or compile them from source.
