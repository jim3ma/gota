package gota

type Gota struct {
	ConnManager   *ConnManager
	TunnelManager *TunnelManager
}

func NewGota(config interface{}) *Gota {
	cm := NewConnManager()
	tm := NewTunnelManager(cm.WriteToTunnelChannel(), cm.ReadFromTunnelChannel())
	tm.SetConfig(config)
	tm.SetCCIDChannel(cm.NewCCIDChannel())
	tm.clientID = cm.clientID

	return &Gota{
		ConnManager:   cm,
		TunnelManager: tm,
	}
}

func (g *Gota) ListenAndServe(addr string) {
	go g.TunnelManager.Start()
	g.ConnManager.ListenAndServe(addr)
}

func (g *Gota) Serve(addr string) {
	go g.TunnelManager.Start()
	g.ConnManager.Serve(addr)
}