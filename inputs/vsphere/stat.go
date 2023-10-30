package vsphere

import (
	"fmt"
	"glouton/logger"
	"sync"
	"time"

	"github.com/vmware/govmomi/vim25/mo"
)

// StatRecorder
type SR struct {
	sync.Mutex

	global        watch
	deviceListing watch
	descCluster   multiWatch
	descHost      multiWatch
	descVM        multiWatch
}

func NewStat() *SR {
	return &SR{
		descCluster: multiWatch{m: make(map[string]*watch)},
		descHost:    multiWatch{m: make(map[string]*watch)},
		descVM:      multiWatch{m: make(map[string]*watch)},
	}
}

func (sr *SR) Display(host string) {
	logger.Printf(
		"vSphere devices stat of %s:\n"+
			"Total: %v\n"+
			"Device listing: %v\n"+
			"Cluster desc: %v\n"+
			"Host desc: %v\n"+
			"VM desc: %v\n",
		host,
		sr.global.total(),
		sr.deviceListing.total(),
		sr.descCluster.display(),
		sr.descHost.display(),
		sr.descVM.display(),
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

type multiWatch struct {
	sync.Mutex

	m map[string]*watch
}

func (mw *multiWatch) Get(obj mo.Reference) *watch {
	mw.Lock()
	defer mw.Unlock()

	if w, ok := mw.m[obj.Reference().Value]; ok {
		return w
	}

	w := new(watch)
	mw.m[obj.Reference().Value] = w

	return w
}

func (mw *multiWatch) display() string {
	mw.Lock()
	defer mw.Unlock()

	var min, max, sum time.Duration
	minZero, maxZero := true, true
	count := 0

	for _, w := range mw.m {
		if w.stop.IsZero() {
			continue
		}

		total := w.total()
		if total < min || minZero {
			min = total
			minZero = false
		}
		if total > max || maxZero {
			max = total
			maxZero = false
		}

		sum += total
		count++
	}

	if sum < 0 {
		logger.Printf("Abnormal multiwatch stats: %v", mw.m)
	}

	avg := time.Duration(float64(sum) / float64(count))

	return fmt.Sprintf("min: %v | max: %v | avg: %v | total: %v", min, max, avg, sum)
}
