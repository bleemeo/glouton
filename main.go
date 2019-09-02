package main

import (
	"agentgo/agent"
	"flag"
	"fmt"
	"os"

	_ "net/http/pprof"
)

//nolint: gochecknoglobals
var (
	runAsRoot = flag.Bool("yes-run-as-root", false, "Allows Bleemeo agent to run as root")
)

func main() {
	flag.Parse()
	if os.Getuid() == 0 && !*runAsRoot {
		fmt.Println("Error: trying to run Bleemeo agent as root without \"--yes-run-as-root\" option.")
		fmt.Println("If Bleemeo agent is installed using standard method, start it with:")
		fmt.Println("    service bleemeo-agent start")
		fmt.Println("")
		os.Exit(1)
	}
	agent.Run()
}
