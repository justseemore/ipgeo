//go:build unix

package ipgeo

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// WatchSIGHUP 收到 SIGHUP 时强制重载一次。
func (p *Provider) WatchSIGHUP(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			if err := p.Reload(ctx); err != nil {
				p.log.Error("ipgeo: SIGHUP reload failed", "err", err)
			}
		}
	}
}
