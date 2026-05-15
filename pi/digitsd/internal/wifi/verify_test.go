package wifi

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// mockCmdRunner records calls and returns preconfigured responses.
type mockCmdRunner struct {
	// calls records every (name, args) pair in order.
	calls []mockCall

	// responses maps "name arg1 arg2 ..." to (output, error) for exact matches.
	responses map[string]mockResponse

	// nmcliStatusResponses is a queue of responses for "nmcli general status".
	// Each poll pops one; when empty, falls through to responses map.
	nmcliStatusResponses []mockResponse
	nmcliStatusIdx       int
}

type mockCall struct {
	name string
	args []string
}

type mockResponse struct {
	output string
	err    error
}

func newMockCmdRunner() *mockCmdRunner {
	return &mockCmdRunner{
		responses: make(map[string]mockResponse),
	}
}

func (m *mockCmdRunner) run(name string, args ...string) (string, error) {
	m.calls = append(m.calls, mockCall{name: name, args: args})

	key := name + " " + strings.Join(args, " ")

	// Special handling for nmcli general status: use queue if available.
	if key == "nmcli general status" && m.nmcliStatusIdx < len(m.nmcliStatusResponses) {
		r := m.nmcliStatusResponses[m.nmcliStatusIdx]
		m.nmcliStatusIdx++
		return r.output, r.err
	}

	if r, ok := m.responses[key]; ok {
		return r.output, r.err
	}
	return "", nil
}

func (m *mockCmdRunner) calledWith(name string, args ...string) bool {
	for _, c := range m.calls {
		if c.name != name || len(c.args) != len(args) {
			continue
		}
		match := true
		for i := range args {
			if c.args[i] != args[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

var testVerifyConfig = verifyConfig{
	pollInterval:     1 * time.Millisecond,
	maxAttempts:      2,
	postFlushDelay:   1 * time.Millisecond,
	postHostapdDelay: 1 * time.Millisecond,
	postReapplyDelay: 1 * time.Millisecond,
}

func TestVerifySuccess(t *testing.T) {
	cmd := newMockCmdRunner()
	cmd.nmcliStatusResponses = []mockResponse{
		{output: "STATE      CONNECTIVITY  WIFI-HW  WIFI     WWAN-HW  WWAN\nconnected  full          enabled  enabled  enabled  enabled\n"},
	}

	result := verifyWithConfig("TestNet", "/data/wifi/test.nmconnection", false, cmd, testVerifyConfig)

	if !result.Connected {
		t.Fatalf("expected Connected=true, got error: %s", result.Error)
	}
	if result.Error != "" {
		t.Errorf("expected empty error, got: %s", result.Error)
	}

	// Verify AP teardown and restore sequence.
	if !cmd.calledWith("systemctl", "stop", "digits-ap") {
		t.Error("should stop digits-ap")
	}
	if !cmd.calledWith("systemctl", "stop", "digits-dnsmasq-ap") {
		t.Error("should stop digits-dnsmasq-ap")
	}
	if !cmd.calledWith("systemctl", "start", "NetworkManager") {
		t.Error("should start NetworkManager")
	}
	if !cmd.calledWith("systemctl", "stop", "NetworkManager") {
		t.Error("should stop NetworkManager after verify")
	}
	if !cmd.calledWith("systemctl", "start", "digits-ap") {
		t.Error("should restart digits-ap")
	}
	if !cmd.calledWith("systemctl", "start", "digits-dnsmasq-ap") {
		t.Error("should restart digits-dnsmasq-ap")
	}
}

func TestVerifyAuthFailure(t *testing.T) {
	cmd := newMockCmdRunner()
	cmd.nmcliStatusResponses = []mockResponse{
		{output: "STATE         CONNECTIVITY  WIFI-HW  WIFI     WWAN-HW  WWAN\ndisconnected  none          enabled  enabled  enabled  enabled\n"},
		{output: "STATE         CONNECTIVITY  WIFI-HW  WIFI     WWAN-HW  WWAN\ndisconnected  none          enabled  enabled  enabled  enabled\n"},
	}
	// wifi list shows the SSID (so it is in range, just bad password).
	cmd.responses["nmcli device wifi list"] = mockResponse{
		output: "IN-USE  BSSID              SSID        MODE   CHAN  RATE       SIGNAL  BARS  SECURITY\n        AA:BB:CC:DD:EE:FF  BadPassNet  Infra  6     54 Mbit/s  80      ***   WPA2\n",
	}

	result := verifyWithConfig("BadPassNet", "/data/wifi/test.nmconnection", false, cmd, testVerifyConfig)

	if result.Connected {
		t.Fatal("expected Connected=false")
	}
	want := "Could not connect to BadPassNet. Check the password and try again."
	if result.Error != want {
		t.Errorf("error = %q, want %q", result.Error, want)
	}
}

func TestVerifySSIDNotFound(t *testing.T) {
	cmd := newMockCmdRunner()
	cmd.nmcliStatusResponses = []mockResponse{
		{output: "STATE         CONNECTIVITY  WIFI-HW  WIFI     WWAN-HW  WWAN\ndisconnected  none          enabled  enabled  enabled  enabled\n"},
		{output: "STATE         CONNECTIVITY  WIFI-HW  WIFI     WWAN-HW  WWAN\ndisconnected  none          enabled  enabled  enabled  enabled\n"},
	}
	// wifi list does NOT include the target SSID.
	cmd.responses["nmcli device wifi list"] = mockResponse{
		output: "IN-USE  BSSID              SSID          MODE   CHAN  RATE       SIGNAL  BARS  SECURITY\n        AA:BB:CC:DD:EE:FF  OtherNetwork  Infra  6     54 Mbit/s  80      ***   WPA2\n",
	}

	result := verifyWithConfig("GhostNet", "/data/wifi/test.nmconnection", false, cmd, testVerifyConfig)

	if result.Connected {
		t.Fatal("expected Connected=false")
	}
	want := "Could not find GhostNet. It may be out of range."
	if result.Error != want {
		t.Errorf("error = %q, want %q", result.Error, want)
	}
}

func TestVerifyNmcliError(t *testing.T) {
	cmd := newMockCmdRunner()
	cmd.nmcliStatusResponses = []mockResponse{
		{output: "", err: fmt.Errorf("nmcli not found")},
		{output: "", err: fmt.Errorf("nmcli not found")},
	}
	// wifi list also errors.
	cmd.responses["nmcli device wifi list"] = mockResponse{
		output: "",
		err:    fmt.Errorf("nmcli not found"),
	}

	result := verifyWithConfig("TestNet", "/data/wifi/test.nmconnection", false, cmd, testVerifyConfig)

	if result.Connected {
		t.Fatal("expected Connected=false when nmcli errors")
	}
	// When wifi list also errors, we can't determine if SSID is visible,
	// so the default "check the password" message is returned.
	want := "Could not connect to TestNet. Check the password and try again."
	if result.Error != want {
		t.Errorf("error = %q, want %q", result.Error, want)
	}
}

func TestVerifyAlwaysRestoresAP(t *testing.T) {
	// Even on success, AP services must be restored.
	cmd := newMockCmdRunner()
	cmd.nmcliStatusResponses = []mockResponse{
		{output: "connected  full\n"},
	}

	_ = verifyWithConfig("Net", "/data/wifi/test.nmconnection", false, cmd, testVerifyConfig)

	// Count restore calls (after NM stop).
	var foundNMStop bool
	var hostapdRestarted, dnsmasqRestarted bool
	for _, c := range cmd.calls {
		key := c.name + " " + strings.Join(c.args, " ")
		if key == "systemctl stop NetworkManager" {
			foundNMStop = true
		}
		if foundNMStop && key == "systemctl start digits-ap" {
			hostapdRestarted = true
		}
		if foundNMStop && key == "systemctl start digits-dnsmasq-ap" {
			dnsmasqRestarted = true
		}
	}
	if !foundNMStop {
		t.Error("NetworkManager should be stopped after verification")
	}
	if !hostapdRestarted {
		t.Error("hostapd should be restarted after verification")
	}
	if !dnsmasqRestarted {
		t.Error("dnsmasq should be restarted after verification")
	}
}
