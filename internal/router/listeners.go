package router

import (
	"net"
	"strconv"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/listenstatus"
	"github.com/overcast-sh/overcast/internal/services/lambda"
	"github.com/overcast-sh/overcast/internal/smtp"
)

// smtpListenFix is what to change when the SMTP capture server cannot bind; it
// travels with the startup error, the mailer's failure, and /_overcast/health.
const smtpListenFix = "set OVERCAST_SMTP_PORT to a free port, or 0 for an ephemeral one"

// listenSMTPMock binds the SMTP capture server on loopback at port.
//
// The default port falls back to an ephemeral one when it is busy — a second
// Overcast on the same host is the usual reason — because Inbox capture does
// not depend on the number: the mailer that feeds it learns the bound address
// rather than assuming the port, so only a mail client pointed at the default
// by hand would notice. A port set to anything else is pinned and fails, so a
// deliberate choice is never silently replaced. On failure the returned server
// is unbound and must not be served. fellBack reports whether the fallback was
// taken.
func listenSMTPMock(store *smtp.MailStore, port, defaultPort int, logger *zap.Logger) (srv *smtp.Server, addr string, fellBack bool, err error) {
	srv = smtp.NewServer(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), store)
	addr, err = srv.Listen()
	if err == nil || port != defaultPort || port == 0 {
		return srv, addr, false, err
	}
	logger.Warn("smtp mock server: default port busy; selecting an ephemeral port",
		zap.Int("requested_port", port),
		zap.Error(err),
		zap.String("hint", "set OVERCAST_SMTP_PORT to pin a different port"))
	srv = smtp.NewServer("127.0.0.1:0", store)
	addr, err = srv.Listen()
	return srv, addr, err == nil, err
}

// listenerStatusFn merges the listeners the router binds itself with the
// Runtime API listener the Lambda service binds once Docker has answered, for
// /_overcast/health. Nil until anything has reported, so the field is omitted
// rather than rendered as an empty object.
func listenerStatusFn(tracker *listenstatus.Tracker, lambdaSvc *lambda.Service) func() map[string]listenstatus.Status {
	return func() map[string]listenstatus.Status {
		snap := tracker.Snapshot()
		if st, ok := lambdaSvc.RuntimeAPIListenStatus(); ok {
			if snap == nil {
				snap = make(map[string]listenstatus.Status, 1)
			}
			snap[listenstatus.LambdaRuntimeAPI] = st
		}
		return snap
	}
}
