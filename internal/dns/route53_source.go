package dns

// SetRoute53 wires the source consulted for Route 53 hosted-zone data, and
// may be called at any time — including after Serve has started, and before
// it, since the resolver binds before the Route 53 service is constructed
// (see internal/router.startContainerDNS and its caller).
//
// With no source set — the default — Route 53 zones answer nothing, which is
// the pre-existing behaviour this package's package doc describes: a hosted
// zone provisions and stores records but no DNS is served from them.
func (s *Server) SetRoute53(src Route53Source) {
	if src == nil {
		s.r53.Store(nil)
		return
	}
	s.r53.Store(&src)
}

// loadRoute53 returns the wired Route53Source, or nil.
func (s *Server) loadRoute53() Route53Source {
	if p := s.r53.Load(); p != nil {
		return *p
	}
	return nil
}
