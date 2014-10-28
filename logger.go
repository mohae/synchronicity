package synchronicity

import (
	"io"
	"io/ioutil"
	"log"
)
// Whether to use verbose output or not.
// Errors and higher use "log", which may or may not be discarded.
var Verbose bool

func init() {
	log.SetOutput(ioutil.Discard)
}

func SetLogger(l io.Writer) {
	log.SetOutput(l)
}

// Use for verbose output. Anything using Log() is for Verbosity.
func Log(v ...interface{}) {
	if Verbose {
		log.Print(v...)
	}
}

// Use for verbose output. Anything using Logf() is for Verbosity.
func Logf(format string, v ...interface{}) {
	if Verbose {
		log.Printf(format, v...)
	}
}
