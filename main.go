package main

import (
	"agentgo/agent"

	_ "net/http/pprof"
)

func main() {
	agent.Run()
}
