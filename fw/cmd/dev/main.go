//go:build !tinygo

package main

import (
	"time"

	"github.com/burgrp/bleriot-relay/fw/spec"
	"github.com/burgrp/bleriot/lib/shared/config"
	"github.com/burgrp/bleriot/lib/shared/inventory"
	"github.com/burgrp/bleriot/lib/site/cli"
)

var relayConfig = spec.Config{
	WatchdogEnabled:            true,
	SampleIntervalMilliseconds: 1000,
	DS18B20Sensors:             []spec.DS18B20Sensor{{}},
}

func main() {
	// This intentionally public bench identity is not deployment provisioning.
	cli.Start(inventory.Inventory{{
		Name:    "relay",
		Address: [4]byte{0x52, 0x45, 0x4C, 0x59},
		Key:     [16]byte{},
		Channel: inventory.Channel{
			Name:         "relay",
			Number:       37,
			SpreadFactor: config.SpreadFactorS8,
		},
		OnlineTimeout: spec.OfflineTimeoutSeconds * time.Second,
		Type:          spec.TypeForConfig(relayConfig),
		Config:        relayConfig,
	}})
}
