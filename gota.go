package gota

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

	return &Gota{
		ConnManager:   cm,
		TunnelManager: tm,
	}
}

// ListenAndServe listen at local for user connections and connect remote tunnel for forwarding traffic
func (g *Gota) ListenAndServe(addr string) {
	go g.TunnelManager.Start()
	g.ConnManager.ListenAndServe(addr)
}

// Serve listen at local for tunnels
func (g *Gota) Serve(addr string) {
	go g.TunnelManager.Start()
	g.ConnManager.Serve(addr)
}
