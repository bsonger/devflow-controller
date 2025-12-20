package signal

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// WithSignal 返回带信号取消的 context 和 cancel 函数
func WithSignal(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case <-ch:
			cancel() // 接收到中断信号，取消 context
		case <-ctx.Done():
			// 父 context 被取消
		}
	}()

	return ctx, cancel
}
