package smtp

import (
	"context"
	"fmt"
	"sync"
)

// LazyMailer is a Mailer that blocks until an underlying Mailer becomes
// available. This lets startup continue while the mock SMTP server binds
// in the background — the first actual email Send will block briefly until
// the server is ready.
type LazyMailer struct {
	once    sync.Once
	readyCh chan struct{}
	mailer  Mailer
}

// NewLazyMailer returns a LazyMailer. Call SetReady once the real Mailer is available.
func NewLazyMailer() *LazyMailer {
	return &LazyMailer{readyCh: make(chan struct{})}
}

// SetReady publishes the real Mailer. Must be called exactly once.
func (l *LazyMailer) SetReady(m Mailer) {
	l.once.Do(func() {
		l.mailer = m
		close(l.readyCh)
	})
}

func (l *LazyMailer) wait() (Mailer, error) {
	<-l.readyCh
	if l.mailer == nil {
		return nil, fmt.Errorf("smtp: lazy mailer was never initialised")
	}
	return l.mailer, nil
}

// Send implements Mailer.
func (l *LazyMailer) Send(ctx context.Context, from string, to []string, subject, body, html string) error {
	m, err := l.wait()
	if err != nil {
		return err
	}
	return m.Send(ctx, from, to, subject, body, html)
}

// SendRaw implements Mailer.
func (l *LazyMailer) SendRaw(ctx context.Context, from string, to []string, msg []byte) error {
	m, err := l.wait()
	if err != nil {
		return err
	}
	return m.SendRaw(ctx, from, to, msg)
}
