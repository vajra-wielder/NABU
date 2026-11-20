package server

import (
	"html/template"
	"net/http"

	"github.com/vajra-wielder/nabu/internal/qr"
)

type shareAddr struct {
	URL string
	QR  template.HTML
}

// serveShare shows how to read NABU on phones and other computers: a
// switch, and each address with a QR code to point a camera at.
func (s *Server) serveShare(w http.ResponseWriter, r *http.Request) {
	var addrs []shareAddr
	on, fixed := false, false
	if s.WiFi != nil {
		on, fixed = s.WiFi.On(), s.WiFi.Fixed()
		for _, a := range s.WiFi.Addrs() {
			sa := shareAddr{URL: a}
			if c, err := qr.Encode(a); err == nil {
				sa.QR = template.HTML(c.SVG(220)) // made here from an address: safe
			}
			addrs = append(addrs, sa)
		}
	}
	s.render(w, r, "share.html", map[string]interface{}{
		"PageTitle": "Read on phones", "On": on, "Fixed": fixed, "Available": s.WiFi != nil,
		"Shared": addrs, "Offline": s.Offline,
	})
}
