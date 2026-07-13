// Copyright 2015-2026 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// ...

package vault

import (
	"strings"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/vault"
)

// New initialise vault.Input.
func New(url string, token string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["vault"]
	if ok {
		vaultInput, ok := input().(*vault.Vault)
		if ok {
			vaultInput.URL = url
			vaultInput.Token = token

			i = &internal.Input{
				Input: vaultInput,
				Accumulator: internal.Accumulator{
					RenameGlobal: renameGlobal,
				},
				Name: "vault",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	gatherContext.Measurement = strings.ReplaceAll(gatherContext.Measurement, ".", "_")

	return gatherContext, false
}
