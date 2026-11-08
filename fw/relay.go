//go:build tinygo

package relay

import (
	"machine"
	"time"

	"github.com/burgrp/bleriot-relay/fw/spec"
	"github.com/burgrp/bleriot/lib/node"
	"github.com/burgrp/bleriot/lib/node/pan211x"
	"github.com/burgrp/tinygo-drivers/onewire"
	"github.com/burgrp/tinygo-drivers/onewire/ds18b20"
	"github.com/burgrp/tinygo-drivers/ws2812"
)

const (
	pinRelay     = machine.PB1
	pinStatusLED = machine.PB0
	pinSensor    = machine.PA0
	pinRadioSCK  = machine.PA2
	pinRadioData = machine.PA1
	pinRadioCS   = machine.PA4
)

var (
	oneWireHardware py32OneWireHardware
	oneWireBus      = onewire.NewMaster(&oneWireHardware)
	temperatureBus  = ds18b20.NewNetwork(&oneWireBus)
)

type Device struct {
	control        controlState
	temperatures   temperatureState
	searcher       onewire.Searcher
	led            ws2812.Device
	ledBytes       [3]byte
	ledRGB         int32
	ledInitialized bool
}

// Run starts the relay firmware with baked provisioning and configuration.
func Run(provisioning node.Provisioning, config spec.Config) {
	device := newDevice(config)
	if err := config.Validate(); err != nil {
		halt(device, "invalid relay configuration: "+err.Error())
		return
	}
	if err := device.writeLED(0); err != nil {
		halt(device, "failed to start status LED: "+err.Error())
		return
	}
	device.temperatures.init(config)
	if device.temperatures.count != 0 {
		initOneWire()
		for !device.discoverTemperatures() {
		}
	}

	if provisioning.OnlineTimeout == 0 {
		provisioning.OnlineTimeout = spec.OfflineTimeoutSeconds * time.Second
	}
	bleNode, err := pan211x.StartNode(provisioning, pinRadioSCK, pinRadioData, pinRadioCS, device)
	if err != nil {
		println("failed to start BleRiot node: " + err.Error())
		device.runOffline()
		return
	}

	for {
		device.control.expireWatchdog(monotonicNanoseconds())
		device.applyRelay()
		online, pulse, _ := bleNode.PollWithStatus()
		if err := device.syncStatus(online, pulse); err != nil {
			halt(device, "failed to update status LED: "+err.Error())
			return
		}
		if device.temperatures.needsDiscovery(monotonicNanoseconds()) {
			device.discoverTemperatures()
		} else {
			index, status := device.temperatures.service(&temperatureBus, monotonicNanoseconds)
			if status != ds18b20.StatusOK {
				if index >= 0 {
					println("DS18B20 sensor", index+1, ":", status.String())
				} else {
					println("DS18B20 bus:", status.String())
				}
			}
		}
	}
}

func newDevice(config spec.Config) *Device {
	// Preload high before enabling the active-low optotriac output.
	pinRelay.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	pinRelay.High()
	pinRelay.Configure(machine.PinConfig{Mode: machine.PinOutput})

	pinStatusLED.Configure(machine.PinConfig{Mode: machine.PinInputPulldown})
	pinStatusLED.Low()
	pinStatusLED.Configure(machine.PinConfig{Mode: machine.PinOutput})
	device := &Device{
		control: newControl(config),
		led:     ws2812.NewWS2812(pinStatusLED),
	}
	device.led.SetBrightness(128)
	return device
}

func (device *Device) Read(tag uint16) (int32, bool) {
	value, null := device.control.read(tag, monotonicNanoseconds())
	device.applyRelay()
	if tag == spec.RegRelay || tag == spec.RegWatchdog {
		return value, null
	}
	return device.temperatures.read(tag)
}

func (device *Device) Write(tag uint16, value int32, null bool) {
	device.control.write(tag, value, null, monotonicNanoseconds())
	device.applyRelay()
}

func (device *Device) applyRelay() {
	pinRelay.Set(device.control.relay == 0)
}

func (device *Device) syncStatus(online, pulse bool) error {
	now := monotonicNanoseconds()
	device.control.service(online, now)
	device.applyRelay()
	return device.writeLED(statusLEDColor(online, pulse, device.control.watchdogExpired(now), device.control.relay != 0))
}

func (device *Device) writeLED(rgb int32) error {
	if device.ledInitialized && rgb == device.ledRGB {
		return nil
	}
	device.ledBytes = [3]byte{byte(rgb >> 16), byte(rgb >> 8), byte(rgb)}
	if _, err := device.led.Write(device.ledBytes[:]); err != nil {
		return err
	}
	device.ledRGB = rgb
	device.ledInitialized = true
	return nil
}

func (device *Device) discoverTemperatures() bool {
	rom, status := device.searcher.Next(&oneWireBus)
	if status == onewire.SearchFound && rom[0] == ds18b20.FamilyCode {
		print("DS18B20 ID: [8]byte{")
		for index, value := range rom {
			if index > 0 {
				print(", ")
			}
			print(value)
		}
		println("}")
	}
	done, count := device.temperatures.observeDiscovery(rom, status, monotonicNanoseconds())
	if done {
		device.searcher.Reset()
		switch {
		case status != onewire.SearchDone:
			println("1-Wire discovery failed with status", uint8(status))
		case count == 0:
			println("No DS18B20 sensors found")
		case device.temperatures.ambiguous:
			println("Multiple DS18B20 sensors found; configure explicit temperature IDs")
		}
	}
	return done
}

func (device *Device) runOffline() {
	device.control.relay = 0
	device.applyRelay()
	for {
		if err := device.syncStatus(false, true); err != nil {
			halt(device, "failed to update offline status: "+err.Error())
			return
		}
		time.Sleep(200 * time.Millisecond)
		if err := device.syncStatus(false, false); err != nil {
			halt(device, "failed to update offline status: "+err.Error())
			return
		}
		time.Sleep(800 * time.Millisecond)
	}
}

func halt(device *Device, message string) {
	device.control.relay = 0
	device.applyRelay()
	pinStatusLED.Low()
	println(message)
	for {
		time.Sleep(time.Second)
	}
}
