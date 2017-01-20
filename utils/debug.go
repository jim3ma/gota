package utils

import (
	"runtime"
	"fmt"
	"path/filepath"
)

// RuntimeInfo return a string containing the file name, function name
// and the line number of a specified entry on the call stack
// use list for parameter, we can call this function pass null parameter
func GoRuntimeInfo(depthList ...int) string {
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

func GoFileName() string {
	_, file, _, _ := runtime.Caller(callerSkip)
	return filepath.Base(file)
}

func GoFileNameFull() string {
	_, file, _, _ := runtime.Caller(callerSkip)
	return file
}

func GoFileLine() int {
	_, _, line, _ := runtime.Caller(callerSkip)
	return line
}

func GoFuncName() string {
	f, _, _, _ := runtime.Caller(callerSkip)
	return runtime.FuncForPC(f).Name()
}