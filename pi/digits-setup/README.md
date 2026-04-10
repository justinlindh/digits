# digits-setup

Captive portal for first-boot WiFi configuration. Runs on the Pi when the phone has no saved WiFi credentials.

## How it works

On first boot (or after a factory reset), `digits-ap-check` detects that no WiFi config exists on the data partition. It starts the phone in AP mode:

1. **hostapd** broadcasts a WiFi network (SSID: `Digits-XXXX`)
2. **dnsmasq** serves DHCP and redirects all DNS to `192.168.4.1`
3. **digits-setup** serves a captive portal on port 80

The user connects to the Digits WiFi network from their phone or laptop, gets redirected to the portal, selects their home WiFi network, and enters the password. `digits-setup` writes a NetworkManager connection file to `/data/wifi/`, then reboots. On the next boot, `digits-ap-check` finds the saved config and connects normally.

## Boot sequence

```
digits-first-boot.service  (generates hostname, SSH keys, device UUID)
        |
digits-ap-check.service    (no WiFi config? start AP mode)
        |
digits-ap.service          (hostapd)
        |
digits-dnsmasq-ap.service  (DHCP + captive portal DNS)
        |
digits-setup.service       (this binary, serves the portal on :80)
```

## Build

```bash
make build       # cross-compile to linux/arm64
make build-local # native build
make test
```

Pure Go, no CGO dependencies.

## Packages

| Package | Purpose |
|---------|---------|
| `portal` | HTTP handler, embedded static UI |
| `wifi` | WiFi network scanning (`iw`) and NetworkManager config writing |
