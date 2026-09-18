package vpn

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteVPNCWrapper(t *testing.T) {
	dir := t.TempDir()
	path, err := writeVPNCWrapper(dir, "corp vpn", "/etc/vpnc/vpnc-script")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "vpnc-corp_vpn.sh" {
		t.Errorf("wrapper path = %s, want vpnc-corp_vpn.sh in the state dir", path)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 == 0 {
		t.Error("wrapper is not executable")
	}
	body, _ := os.ReadFile(path)
	for _, want := range []string{"VPN='corp vpn'", "SYSTEM_SCRIPT='/etc/vpnc/vpnc-script'", "STATE_FILE='" + filepath.Join(dir, "vpn-corp_vpn.pushed.json") + "'"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("wrapper lacks %s", want)
		}
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		if out, err := exec.Command(sh, "-n", path).CombinedOutput(); err != nil {
			t.Errorf("wrapper does not parse: %v\n%s", err, out)
		}
	}
}

// The wrapper records what the gateway pushed; run it with a fake system
// script and fake route tools so it works in a sandbox.
func TestVPNCWrapperRecordsPushedConfig(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake ip/route/ifconfig that only log their calls.
	calls := filepath.Join(dir, "calls.log")
	for _, tool := range []string{"ip", "route", "ifconfig"} {
		script := "#!/bin/sh\necho \"" + tool + " $*\" >> '" + calls + "'\n"
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	system := filepath.Join(dir, "system-vpnc-script")
	if err := os.WriteFile(system, []byte("#!/bin/sh\necho \"system $reason\" >> '"+calls+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper, err := writeVPNCWrapper(dir, "corp", system)
	if err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"reason=connect", "TUNDEV=tun7", "VPNGATEWAY=203.0.113.1",
		"INTERNAL_IP4_ADDRESS=10.4.1.5", "INTERNAL_IP4_DNS=10.4.60.100 10.4.60.50",
		"CISCO_SPLIT_INC=2",
		"CISCO_SPLIT_INC_0_ADDR=10.4.0.0", "CISCO_SPLIT_INC_0_MASKLEN=22",
		"CISCO_SPLIT_INC_1_ADDR=10.0.0.0", "CISCO_SPLIT_INC_1_MASKLEN=8",
	)
	cmd := exec.Command(sh, wrapper)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed: %v\n%s", err, out)
	}

	got := readPushed(dir, "corp", "tun7")
	if got.IP != "10.4.1.5" || got.TunDev != "tun7" || got.Gateway != "203.0.113.1" {
		t.Errorf("pushed = %+v", got)
	}
	if strings.Join(got.Routes, " ") != "10.4.0.0/22 10.0.0.0/8" {
		t.Errorf("routes = %v", got.Routes)
	}
	if strings.Join(got.DNS, " ") != "10.4.60.100 10.4.60.50" {
		t.Errorf("dns = %v", got.DNS)
	}
	if empty := readPushed(dir, "corp", "tun8"); empty.TunDev != "" {
		t.Errorf("a record for another device must be ignored, got %+v", empty)
	}

	log, _ := os.ReadFile(calls)
	for _, want := range []string{"system connect", "10.4.0.0/22", "10.0.0.0/8", "tun7"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("expected %q in the tool calls:\n%s", want, log)
		}
	}

	// Disconnect removes the record.
	cmd = exec.Command(sh, wrapper)
	cmd.Env = append(env, "reason=disconnect")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper (disconnect) failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, pushedFileName("corp"))); !os.IsNotExist(err) {
		t.Error("record still exists after disconnect")
	}
}

func TestReadPushedIgnoresGarbage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, pushedFileName("x")), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readPushed(dir, "x", "tun0"); got.TunDev != "" {
		t.Errorf("got %+v, want zero", got)
	}
	rec, _ := json.Marshal(Pushed{VPN: "x", TunDev: "tun0", Routes: []string{"10.0.0.0/8"}})
	if err := os.WriteFile(filepath.Join(dir, pushedFileName("x")), rec, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readPushed(dir, "x", "tun0"); len(got.Routes) != 1 {
		t.Errorf("got %+v, want the record", got)
	}
	if got := readPushed("", "x", "tun0"); got.TunDev != "" {
		t.Error("no state dir must read nothing")
	}
}
