package gota

import (
	"fmt"
	"path/filepath"
	"runtime"
	"runtime/debug"

	log "github.com/Sirupsen/logrus"
	"os"
	"os/signal"
	"syscall"
)

// RuntimeInfo return a string containing the file name, function name
// and the line number of a specified entry on the call stack
// use list for parameter, we can call this function pass null parameter
func RuntimeInfo(depthList ...int) string {
	var depth int
	if depthList == nil {
		depth = 1
	} else {
		depth = depthList[0]
	}
	function, file, line, _ := runtime.Caller(depth)
	return fmt.Sprintf("(File: %s, Line: %d, Function: %s)", filepath.Base(file), line, runtime.FuncForPC(function).Name())
}

const callerSkip = 1

var verbose = false

// GoFileName return the file name
func GoFileName() string {
	_, file, _, _ := runtime.Caller(callerSkip)
	return filepath.Base(file)
}

// GofileNameFull return the full path of the current file
func GoFileNameFull() string {
	_, file, _, _ := runtime.Caller(callerSkip)
	return file
}

// GoFileLine return the current executing line number
func GoFileLine() int {
	_, _, line, _ := runtime.Caller(callerSkip)
	return line
}

// GoFuncName return the current executing function name
func GoFuncName() string {
	f, _, _, _ := runtime.Caller(callerSkip)
	return runtime.FuncForPC(f).Name()
}

func Recover() {
	if r := recover(); r != nil {
		log.Warnf("Runtime error caught: \"%v\",\nRuntime info: %s, \nCall stack: %s",
			r, RuntimeInfo(2), debug.Stack())
	}
}

func SetVerbose(v bool) {
	verbose = v
}

func IsVerbose() bool {
	return verbose
}

func Verbosef(format string, args ...interface{}) {
	if verbose {
		log.Debugf(format, args...)
	}
}

func setupSIGUSR1Trap() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGUSR1)
	go func() {
		for range c {
			DumpStacks()
		}
	}()
}

func DumpStacks() {
	buf := make([]byte, 64*1024)
	buf = buf[:runtime.Stack(buf, true)]
	fmt.Printf("=== BEGIN goroutine stack dump ===\n%s\n=== END goroutine stack dump ===", buf)
}
