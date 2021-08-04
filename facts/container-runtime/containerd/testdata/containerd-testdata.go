package main

import (
	"context"
	"flag"
	"fmt"
	gloutonContainerd "glouton/facts/container-runtime/containerd"
	"log"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
)

var createRedis = flag.Bool("create-redis", false, "Create a Redis container in example namespace")

func main() {
	flag.Parse()

	if *createRedis {
		if err := redisExample(); err != nil {
			log.Fatal(err)
		}

		return
	}

	json, err := gloutonContainerd.DumpToJSON(context.Background(), "/run/containerd/containerd.sock")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(json))
}

func redisExample() error {
	// create a new client connected to the default socket path for containerd
	client, err := containerd.New("/run/containerd/containerd.sock")
	if err != nil {
		return err
	}
	defer client.Close()

	// create a new context with an "example" namespace
	ctx := namespaces.WithNamespace(context.Background(), "example")

	// pull the redis image from DockerHub
	image, err := client.Pull(ctx, "docker.io/library/redis:alpine", containerd.WithPullUnpack)
	if err != nil {
		return err
	}

	// create a container
	container, err := client.NewContainer(
		ctx,
		"redis-server",
		containerd.WithImage(image),
		containerd.WithNewSnapshot("redis-server-snapshot", image),
		containerd.WithNewSpec(oci.WithImageConfig(image)),
	)
	if err != nil {
		return err
	}

	// create a task from the container
	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStdio))
	if err != nil {
		return err
	}
	defer task.Delete(ctx)

	// make sure we wait before calling start
	_, err = task.Wait(ctx)
	if err != nil {
		log.Println(err)
	}

	// call start on the task to execute the redis server
	if err := task.Start(ctx); err != nil {
		return err
	}

	return nil
}
