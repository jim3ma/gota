package gota

import (
	"errors"
	"testing"
)

// go test with -v to show all logs
func TestRecover(t *testing.T) {
	defer Recover()
	panic(errors.New(`"TestRecover Error"`))
}
