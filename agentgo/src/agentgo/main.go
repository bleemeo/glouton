package main

import (
	"log"
	"time"

	"agentgo/inputs/cpu"
	"agentgo/inputs/disk"
	"agentgo/inputs/mem"
	"agentgo/store"
	"agentgo/types"

	"github.com/influxdata/telegraf"
)

type storeInterface interface {
	Metrics(filters map[string]string) ([]types.Metric, error)
}

func stats(db storeInterface) {
	for {
		time.Sleep(60 * time.Second)
		metrics, _ := db.Metrics(nil)
		log.Printf("Count of metrics: %v", len(metrics))

		metrics, _ = db.Metrics(map[string]string{"__name__": "cpu_used"})
		for _, m := range metrics {
			log.Printf("Details for metrics %v", m)
			points, _ := m.Points(time.Now().Add(86400*time.Second), time.Now())
			log.Printf("points count: %v", len(points))
		}
	}
}

func main() {
	log.Println("Starting agent")
	db := store.New()

	inputs := make([]telegraf.Input, 0)

	i1, err := cpu.New()
	if err != nil {
		log.Fatalf("Unable to initiazlied cpu input")
	}
	inputs = append(inputs, i1)

	i2, err := mem.New()
	if err != nil {
		log.Fatalf("Unable to initiazlied mem input")
	}
	inputs = append(inputs, i2)

	i3, err := disk.New("/", nil)
	if err != nil {
		log.Fatalf("Unable to initiazlied disk input")
	}
	inputs = append(inputs, i3)

	go stats(db)

	for {
		time.Sleep(10 * time.Second)
		acc := db.Accumulator()
		for _, i := range inputs {
			err := i.Gather(acc)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}
