package synchronicity

import (
	"io"
	"io/ioutil"
	"log"
	"os"
)

// Whether to use verbose output or not.
// Errors and higher use "log", which may or may not be discarded.
var verbose bool
var VLogger *log.Logger // Use separate logger for verbose output

func init() {
	log.SetOutput(ioutil.Discard)
	VLogger = log.New(ioutil.Discard, "synchro:", log.Lshortfile)
}

func SetLogger(l io.Writer) {
	log.SetOutput(l)
}

// SetVerbose sets the verbosity, a false also sets output to
// ioutil.Discard, true sets output to stdout.
func SetVerbose(b bool) {
	verbose = b
	if b == false {
		VLogger = log.New(ioutil.Discard, "synchro:", log.Lshortfile)
	} else {
		VLogger = log.New(os.Stdout, "synchro:", log.Lshortfile)
	}
}

// SetVerboseLogger set's the output for the verbose logger.
// It also sets the verbose flag.
func SetVerboseLogger(l io.Writer) {
	if l == ioutil.Discard {
		verbose = false
	} else {
		verbose = true
	}
	VLogger = log.New(l, "synchro:", log.Lshortfile)
}

// Use for verbose output. Anything using Log() is for Verbosity.
func Log(v ...interface{}) {
	if verbose {
		VLogger.Print(v...)
	}
}

// Use for verbose output. Anything using Logf() is for Verbosity.
func Logf(format string, v ...interface{}) {
	if verbose {
		VLogger.Printf(format, v...)
	}
}
