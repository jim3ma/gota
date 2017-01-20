package main

import(
	//"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/jim3ma/gota/utils"
	"runtime/debug"
)

func main(){
	//fmt.Print("Hello World~")
	log.Info("gota")
	log.Infof("Runtime information: %s", utils.GoRuntimeInfo())
	log.Infof("Runtime information, file name: %s", utils.GoFileName())
	log.Infof("Runtime information, file line: %d", utils.GoFileLine())
	log.Infof("Runtime information, function name: %s", utils.GoFuncName())
	defer func(){
		if r := recover(); r != nil {
			log.Errorf("Close connection error: %s", r)
			log.Errorf("Call stack: %s", debug.Stack())
		}
	}()
	panic("test")
}
