package vsphere

import (
	"glouton/logger"
	"time"
)

// SR stands for StatRecorder
type SR struct {
	global        watch
	deviceListing watch
	descCluster   watch
	descDatastore watch
	descHost      watch
	descVM        watch
}

func NewStat() *SR {
	return new(SR)
}

func (sr *SR) Display(host string) {
	logger.Printf(
		"Timing stats of vSphere %s:\n"+
			"Total: %v\n"+
			"Device listing: %v\n"+
			"Cluster desc: %v\n"+
			"Datastore desc: %v\n"+
			"Host desc: %v\n"+
			"VM desc: %v\n",
		host,
		sr.global.total(),
		sr.deviceListing.total(),
		sr.descCluster.total(),
		sr.descDatastore.total(),
		sr.descHost.total(),
		sr.descVM.total(),
	)
}

type watch struct {
	start, stop time.Time
}

func (w *watch) Start() {
	w.start = time.Now()
}

func (w *watch) Stop() {
	w.stop = time.Now()
}

func (w *watch) total() time.Duration {
	return w.stop.Sub(w.start)
}
