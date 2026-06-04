package cmd

import (
	"fmt"
	"os"
	"sync"
)

var (
	verbose   bool
	logMutex  sync.Mutex
)

func debugf(format string, args ...interface{}) {
	if !verbose {
		return
	}
	logMutex.Lock()
	defer logMutex.Unlock()
	fmt.Fprintf(os.Stderr, "[debug] "+format+"\n", args...)
}
