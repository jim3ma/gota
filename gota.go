package gota

var (
	Version = "gota_version"
	Commit = "gota_commit"
)

// Gota provides quick usage for gota
type Gota struct {
	ConnManager   *ConnManager
	TunnelManager *TunnelManager
}

// NewGota returns a Gota with mounted ConnManager and TunnelManager
func NewGota(config interface{}, auth *TunnelAuthCredential) *Gota {
	cm := NewConnManager()
	tm := NewTunnelManager(cm.WriteToTunnelChannel(), cm.ReadFromTunnelChannel(), auth)
	tm.SetConfig(config)
	tm.SetCCIDChannel(cm.NewCCIDChannel())
	tm.SetClientID(cm.clientID)
	tm.cleanUpAllConnCh = cm.cleanUpCHChanClientID

	return &Gota{
		ConnManager:   cm,
		TunnelManager: tm,
	}
}

// ListenAndServe listen at local for user connections and connect remote tunnel for forwarding traffic
func (g *Gota) ListenAndServe(addr string) {
	g.TunnelManager.mode = ActiveMode
	g.ConnManager.mode = ActiveMode
	go g.TunnelManager.Start()
	g.ConnManager.ListenAndServe(addr)
}

// Serve listen at local for tunnels
func (g *Gota) Serve(addr string) {
	g.TunnelManager.mode = PassiveMode
	g.ConnManager.mode = PassiveMode
	go g.TunnelManager.Start()
	g.ConnManager.Serve(addr)
}
