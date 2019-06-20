package main

import (
	"log"
	"time"

	"agentgo/inputs/cpu"
	"agentgo/inputs/disk"
	"agentgo/inputs/mem"
	"agentgo/store"

	"github.com/influxdata/telegraf"
)

func main() {
	log.Println("Starting agent")
	store := store.New()

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

	go func() {
		for {
			time.Sleep(60 * time.Second)
			metrics, _ := store.Metrics(nil)
			log.Printf("Count of metrics: %v", len(metrics))

			metrics, _ = store.Metrics(map[string]string{"__name__": "cpu_used"})
			for _, m := range metrics {
				log.Printf("Details for metrics %v", m)
				points, _ := m.Points(time.Now().Add(-75*time.Second), time.Now())
				log.Printf("points count: %v", len(points))
			}
		}
	}()

	for {
		time.Sleep(10 * time.Second)
		acc := store.Accumulator()
		for _, i := range inputs {
			err := i.Gather(acc)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}
