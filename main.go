// Package main is the entrypoint for the viam-data-mirror module, which mirrors
// binary data from Viam's data management down to local files on a machine.
package main

import (
	"go.viam.com/rdk/module"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/generic"

	"github.com/viam-soleng/viam-data-mirror/mirror"
)

func main() {
	module.ModularMain(resource.APIModel{API: generic.API, Model: mirror.Model})
}
