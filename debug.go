package gota

import (
	"runtime"
	"fmt"
	"path/filepath"
	log "github.com/Sirupsen/logrus"
	"runtime/debug"
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

const callerSkip  = 1

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
		log.Errorf("Runtime error caught: %v, runtime info: %s", r, RuntimeInfo(2))
		log.Errorf("Call stack: %s", debug.Stack())
	}
}