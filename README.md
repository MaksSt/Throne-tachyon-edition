# Throne Tachyon Edition

A community fork of **[Throne](https://github.com/throneproj/Throne)** with
native **[Tachyon Core](https://github.com/Dushnilin/tachyon-core)** integration.
Ordinary profiles and subscriptions can use a managed Tachyon process, while
the original sing-box backend remains available.

## Upstream projects and attribution

- **Original application:** [throneproj/Throne](https://github.com/throneproj/Throne),
  developed by the Throne maintainers and contributors. This fork preserves its
  source history, original copyright notices and [GPL-3.0 license](LICENSE).
- **Tachyon Core:** [Dushnilin/tachyon-core](https://github.com/Dushnilin/tachyon-core),
  maintained by [Dushnilin](https://github.com/Dushnilin). Its current binaries
  have a separate [binary license](https://github.com/Dushnilin/tachyon-core/blob/main/LICENSE).
  Throne downloads them directly from the official repository for local use;
  Tachyon binaries are not committed or bundled in this repository.
- **Upstream documentation:** [Throne documentation](https://throneproj.github.io/).
  Upstream installers and package repositories install the original Throne.

## What this edition adds

- A selectable native Tachyon backend for existing profile editors, share links
  and subscriptions, including compatible stored Xray VLESS profiles.
- Automatic installation of the latest stable Tachyon GitHub release, a manual
  update button, daily checks and SHA-256 verification. No version or executable
  path needs to be entered.
- Native latency, speed and IP tests, DNS/routing adaptation, connection and
  traffic reporting, selector controls and managed process cleanup.
- Windows TUN egress protection: the core's own sockets stay on the physical
  interface instead of looping back into its Wintun tunnel.

## Quick start

1. Build this edition using the [build workflow](.github/workflows/build.yml)
   and the existing scripts under [script](script/). Keep `Throne.exe`,
   `ThroneCore.exe` and the required runtime libraries together.
2. In **Basic Settings > Common > Proxy core**, select **tachyon-core** and save.
3. On first use, Throne installs the latest stable release. **Install / Update
   from GitHub** checks immediately; automatic daily checks are enabled by default.
4. Reconnect to apply a newly installed core. For Windows TUN, run Throne with
   administrator rights.

**Status:** experimental. Windows x64 was exercised with Tachyon 0.2.3 using
IPv4 HTTPS, external-IP checks, an 8 MiB download and UDP DNS through TUN.
General/game UDP, IPv6 TUN and other platforms have not been fully validated.
Details and remaining limitations are listed below. The earlier `dev` branch is
retained for history; `main` contains this managed integration.

## Tachyon native backend

Basic Settings > Common > Proxy core selects `sing-box` (the default) or
[`tachyon-core`](https://github.com/Dushnilin/tachyon-core). Throne downloads the
latest stable release automatically on first use. **Install / Update from GitHub**
checks for the latest stable release immediately; the automatic check is enabled
by default and runs daily. No version number or executable path is entered.

Release metadata comes from GitHub's `/releases/latest` API. Throne selects the
asset for its OS/architecture, requires strict TLS, and checks the published
SHA-256 digest and size before installation. The controller verifies the archive
again and runs the staged executable's `version` command. The managed files live
under `cores/tachyon` in Throne's data directory. Updates publish an atomic
`current.json` manifest and retain `previous.json` plus the previous executable.
Running connections keep their existing process; reconnect to use an update.
A failed download, checksum, extraction or executable check keeps the installed
release. An unchanged release is not downloaded again. Unsupported platforms
and unavailable GitHub access report an error; they do not select another core.

Tachyon is downloaded directly for local use under its own
[binary license](https://github.com/Dushnilin/tachyon-core/blob/main/LICENSE).
Its binary is not redistributed in Throne's archive. `ThroneCore.exe` remains
Throne's IPC controller; users do not need to locate or replace either executable.

With Tachyon selected, its native process owns inbound listeners, protocol
connections, DNS, routing rules, chains, endpoints, cache and Clash API.
No sing-box instance is created and no traffic crosses a sing-box bridge.
Throne adapts and checks the configuration, waits for authenticated API/probe
listeners, and stops/reaps the process on disconnect. On Windows a job object
also terminates it if the controller exits abruptly. Native startup errors are
reported directly. There is no automatic fallback.

Existing editors, share links and subscriptions remain usable. Stored Xray
VLESS profiles are converted at build time, including TCP/REALITY/Vision, TLS,
WebSocket, HTTPUpgrade, gRPC and XHTTP; saved profiles are not rewritten.
Compatible custom Xray outbounds are adapted too. Unsupported wire options
(Xray Mux, XDRIVE, ML-DSA REALITY verification) fail explicitly. Native custom
outbounds and full configurations use Tachyon's validator. DNS predefined
records are converted from text to the wire format its runtime expects.

Latency, download/upload speed and IP tests use Tachyon's own probe listeners.
Live connections, rule-set refresh and selector actions use its Clash API.
Global cumulative traffic counters are converted to deltas and labelled
**Tachyon (total)**. The API does not supply exact per-outbound totals, so global
traffic is not charged to profiles. Recently closed connections retain their
last observed snapshot. Throne's adaptive selector uses Tachyon's native
URL-test policy; sing-box-specific sampling/cooldown telemetry is unavailable.

Verified with Tachyon 0.2.3 on Windows x64:

- Windows TUN uses Wintun and requires administrator rights. Before `auto_route`
  captures traffic, Throne binds root outbound and DNS dialers to the physical
  interface selected by its network monitor. This keeps the proxy's own sockets
  outside its tunnel. Explicit per-outbound interfaces, bind addresses and
  detours are preserved. `route.default_interface` overrides detection;
  `route.auto_detect_interface=false` keeps manual egress configuration.
- A missing physical interface or a native TUN startup failure is reported as
  an error. Tachyon's `continuing in passive mode` warning is treated as failed
  startup, even if its private SOCKS listeners are ready.
- IPv4 HTTPS and external-IP checks through the Windows tunnel have passed on
  a REALITY profile without a manually configured interface. UDP DNS replies and
  an 8 MiB download through the tunnel were also verified. A separate STUN probe
  timed out on the physical, SOCKS and TUN paths, so general/game UDP is not yet
  confirmed. IPv6 TUN and Linux TUN have not been live-tested. Earlier Tachyon
  0.1.6 builds lacked Windows TUN.
- Protocol/endpoint availability depends on the installed Tachyon release.
  Interactive VPN challenges and full Xray configurations require the default
  backend. Not every protocol has been tested against a remote server.
- Extra Core helpers retain their separate lifecycle. Other native
  protocols/transports/endpoints are passed to Tachyon's validator/runtime.

Subscription updates preserve the local backend and automatic-update settings. Select
`sing-box` and reconnect to return to the default backend. Tests in
`core/internal/coreupdate`, `core/internal/tachyon`, `core/internal/rpc` and `core/internal/probe` cover
conversion, native DNS, lifecycle, API statistics, backend switching and the
absence of a sing-box instance in native mode. Set `TACHYON_BINARY` for
release-binary tests; use the tags/linker flags from `script/build_go.sh`.

## Build and verification

This fork follows Throne's Qt/CMake and Go build process. See
[the build workflow](.github/workflows/build.yml) and
[the Go build script](script/build_go.sh) for toolchains and platform tags.
The [Tachyon checks workflow](.github/workflows/tachyon-check.yml) verifies the
Windows backend against the latest official release without publishing binaries.

After installing the upstream protobuf tools, generate the Go RPC bindings:

```sh
protoc -I core/gen --go_out=core/gen --go-grpc_out=core/gen core/gen/libcore.proto
```

Example Windows PowerShell checks (with Go, CMake and an official Tachyon
executable available):

```powershell
$tags = 'with_clash_api,with_quic,with_wireguard,with_utls,with_dhcp,with_tailscale,with_openvpn,with_openconnect,badlinkname,tfogo_checklinkname0,with_purego,with_naive_outbound'
$env:TACHYON_BINARY = 'C:\tools\tachyon-core.exe'
go -C core test -ldflags=-checklinkname=0 -tags $tags ./internal/...
go -C core vet -tags $tags ./internal/...
cmake -P cmake/EngineBoundary.cmake
```

Without `TACHYON_BINARY`, tests requiring the actual Tachyon executable are
skipped. Unit tests and a successful config check do not prove that TUN works on
an end-user machine; Windows administrator privileges and real traffic still
need verification. Local profiles, API keys, configuration databases and build
artifacts must stay outside commits.

## Additional upstream credits

- [SagerNet/sing-box](https://github.com/SagerNet/sing-box)
- [XTLS/Xray-core](https://github.com/xtls/xray-core)
- [Qv2ray](https://github.com/Qv2ray/Qv2ray)
- [Qt](https://www.qt.io/)
- [simple-protobuf](https://github.com/tonda-kriz/simple-protobuf)
- [fkYAML](https://github.com/fktn-k/fkYAML)
- [quirc](https://github.com/dlbeer/quirc)
- [srombauts/sqlitecpp](https://github.com/srombauts/sqlitecpp)


You can support the original Throne project through its
[upstream support page](https://github.com/throneproj/Throne/blob/dev/.github/donate/Donate.md)
or [GitHub Sponsors](https://github.com/sponsors/Mahdi-zarei).
