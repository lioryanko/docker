// +build !linux !cgo static_build !journald

package journald

import (
	"io"
	"time"

	"github.com/docker/docker/daemon/logger"
)

func (s *Journald) IsReadable() bool {
	return false
}

func (s *Journald) GetReader(lines int, since time.Time) (io.Reader, error) {
	return nil, logger.ReadLogsNotSupported
}
