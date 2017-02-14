package gota

import "testing"

func TestConnManager_ListenAndServe(t *testing.T) {

}

func TestNewCCID(t *testing.T) {
	cc := NewCCID(1, 2)
	if cc.GetClientID() != 1 {
		t.Errorf("Error client ID: %d", cc.GetClientID())
	}

	if cc.GetConnID() != 2 {
		t.Errorf("Error conn ID: %d", cc.GetConnID())
	}

	cc = NewCCID(0xFFFFFFFF, 0xFFFFFFFE)
	if cc.GetClientID() != 0xFFFFFFFF {
		t.Errorf("Error client ID: %d", cc.GetClientID())
	}

	if cc.GetConnID() != 0xFFFFFFFE {
		t.Errorf("Error conn ID: %d", cc.GetConnID())
	}
}