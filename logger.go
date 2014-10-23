// This logger code is based on Seelog's 'Writing libraries with Seelog'
// example: https://github.com/cihub/seelog/wiki/Writing-libraries-with-Seelog
//
// No copyright claims are made and no warranty, express or implied, is made
// on this code by the Authors of the 'rollie' rollie package for Go.
// Please refer to Cloud Instruments Co., Ltd. or the above link for any
// additional information.

package synchronicity

import (
	"errors"
	seelog "github.com/cihub/seelog"
	"io"
)

var logger seelog.LoggerInterface

func init() {
	// Disable logger by default.
	DisableLog()
}

// DisableLog disables all library log output.
func DisableLog() {
	logger = seelog.Disabled
}

// UseLogger uses a specified seelog.LoggerInterface to output library log.
// Use this func if you are using Seelog logging system in your app.
func UseLogger(newLogger seelog.LoggerInterface) {
	logger = newLogger
}

// SetLogWriter uses a specified io.Writer to output library log.
// Use this func if you are not using Seelog logging system in your app.
func SetLogWriter(writer io.Writer) error {
	if writer == nil {
		return errors.New("Nil writer")
	}

	newLogger, err := seelog.LoggerFromWriterWithMinLevel(writer, seelog.TraceLvl)
	if err != nil {
		return err
	}

	UseLogger(newLogger)
	return nil
}

// Call this before app shutdown
func FlushLog() {
	logger.Flush()
}
