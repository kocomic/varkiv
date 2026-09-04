//go:build !windows

package agenttray

import (
	"context"
	"errors"
	"time"
)

func Run(context.Context, string, time.Duration) error {
	return errors.New("agent tray is available only on Windows")
}
