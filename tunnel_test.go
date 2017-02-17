package gota

import (
	"testing"
)

func TestTunnelManager_SetConfig(t *testing.T) {
	tm := NewTunnelManager(nil, nil)
	ac := TunnelActiveConfig{}
	if err := tm.SetConfig(ac); err != nil {
		t.Errorf("Set config for tunnel manager error: %s", err)
	}

	if tm.Mode() != ActiveMode {
		t.Error("Tunnel manager work mode error")
	}

	acs := make([]TunnelActiveConfig, 10)
	if err := tm.SetConfig(acs); err != nil {
		t.Errorf("Set config for tunnel manager error: %s", err)
	}
	if tm.Mode() != ActiveMode {
		t.Error("Tunnel manager work mode error")
	}

	pc := TunnelPassiveConfig{}
	if err := tm.SetConfig(pc); err != nil {
		t.Errorf("Set config for tunnel manager error: %s", err)
	}
	if tm.Mode() != PassiveMode {
		t.Error("Tunnel manager work mode error")
	}

	pcs := make([]TunnelPassiveConfig, 10)
	if err := tm.SetConfig(pcs); err != nil {
		t.Errorf("Set config for tunnel manager error: %s", err)
	}
	if tm.Mode() != PassiveMode {
		t.Error("Tunnel manager work mode error")
	}

	if err := tm.SetConfig(1); err == nil {
		t.Errorf("Set config for tunnel manager error")
	}
}

func TestTunnelTransport_Start(t *testing.T) {

}
