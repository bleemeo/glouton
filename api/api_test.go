package api

import (
	"testing"

	"github.com/go-bindata/go-bindata/v3"
)

func TestUseless(t *testing.T) {
	// This test do nothing but it's only present to avoid "go mod tidy" to remove
	// go-bindata from go.mod
	tmp := bindata.Asset{}
	tmp.Name = "ok"
}
