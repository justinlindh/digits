package subsystem

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"time"
)

type WiFiAPConfig struct {
	SSID          string
	UseSystemd    bool
	HostapdPath   string
	DnsmasqPath   string
	InterfaceWait time.Duration
}

type WiFiAPModule struct {
	cfg    WiFiAPConfig
	status ModuleStatus
}

func NewWiFiAPModule(cfg WiFiAPConfig) *WiFiAPModule {
	if cfg.SSID == "" {
		cfg.SSID = "Digits-Recovery"
	}
	if cfg.HostapdPath == "" {
		cfg.HostapdPath = "/bin/hostapd"
	}
	if cfg.DnsmasqPath == "" {
		cfg.DnsmasqPath = "/bin/dnsmasq"
	}
	if cfg.InterfaceWait == 0 {
		cfg.InterfaceWait = 15 * time.Second
	}
	return &WiFiAPModule{cfg: cfg, status: ModuleStatus{State: StatePending}}
}

func (w *WiFiAPModule) Name() string { return "wifi-ap" }

func (w *WiFiAPModule) Init(ctx context.Context) error {
	w.status.State = StateInitializing

	slog.Info("subsystem wifi-ap: waiting for wlan0")
	if err := waitForInterface("wlan0", w.cfg.InterfaceWait); err != nil {
		w.status = ModuleStatus{State: StateFailed, Message: err.Error()}
		return err
	}
	unblockWifi()

	if w.cfg.UseSystemd {
		return w.initSystemd()
	}
	return w.initDirect()
}

func (w *WiFiAPModule) initDirect() error {
	env := os.Environ()
	run := func(args ...string) error {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = append(env[:len(env):len(env)], "LD_LIBRARY_PATH=/lib")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	if err := run("ip", "addr", "add", "192.168.4.1/24", "dev", "wlan0"); err != nil {
		return fmt.Errorf("ip addr: %w", err)
	}
	if err := run("ip", "link", "set", "wlan0", "up"); err != nil {
		return fmt.Errorf("ip link up: %w", err)
	}

	hostapdConf := "/tmp/recovery-hostapd.conf"
	if err := os.WriteFile(hostapdConf, []byte(fmt.Sprintf("interface=wlan0\ndriver=nl80211\nssid=%s\nchannel=6\nhw_mode=g\nwmm_enabled=0\nauth_algs=1\nwpa=0\n", w.cfg.SSID)), 0644); err != nil {
		return fmt.Errorf("write hostapd.conf: %w", err)
	}

	dnsmasqConf := "/tmp/recovery-dnsmasq.conf"
	if err := os.WriteFile(dnsmasqConf, []byte("interface=wlan0\nbind-interfaces\nuser=root\ndhcp-range=192.168.4.10,192.168.4.50,24h\npid-file=/tmp/dnsmasq.pid\naddress=/#/192.168.4.1\nlog-facility=/tmp/dnsmasq.log\ndhcp-leasefile=/tmp/dnsmasq-recovery.leases\n"), 0644); err != nil {
		return fmt.Errorf("write dnsmasq.conf: %w", err)
	}

	if err := run(w.cfg.HostapdPath, "-B", hostapdConf); err != nil {
		return fmt.Errorf("hostapd: %w", err)
	}
	time.Sleep(500 * time.Millisecond)
	cmd := exec.Command(w.cfg.DnsmasqPath, "-C", dnsmasqConf)
	cmd.Env = append(env[:len(env):len(env)], "LD_LIBRARY_PATH=/lib")
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("subsystem wifi-ap: dnsmasq failed", "error", err, "output", string(out))
		return fmt.Errorf("dnsmasq: %w", err)
	}

	w.status.State = StateReady
	slog.Info("subsystem wifi-ap: AP started", "ssid", w.cfg.SSID)
	return nil
}

func (w *WiFiAPModule) initSystemd() error {
	for _, svc := range []string{"hostapd", "dnsmasq"} {
		if err := exec.Command("systemctl", "start", svc).Run(); err != nil {
			return fmt.Errorf("start %s: %w", svc, err)
		}
	}
	w.status.State = StateReady
	slog.Info("subsystem wifi-ap: AP started via systemd", "ssid", w.cfg.SSID)
	return nil
}

func (w *WiFiAPModule) Teardown() error {
	if w.cfg.UseSystemd {
		for _, svc := range []string{"hostapd", "dnsmasq"} {
			_ = exec.Command("systemctl", "stop", svc).Run()
		}
	} else {
		_ = exec.Command("killall", "hostapd").Run()
		_ = exec.Command("killall", "dnsmasq").Run()
	}
	return nil
}

func (w *WiFiAPModule) Status() ModuleStatus               { return w.status }
func (w *WiFiAPModule) Shutdown(ctx context.Context) error { return w.Teardown() }

func waitForInterface(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := net.InterfaceByName(name); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("interface %s not found after %s", name, timeout)
}

func unblockWifi() {
	_ = exec.Command("rfkill", "unblock", "wifi").Run()
}
