package relay

import (
	"time"

	"github.com/burgrp/bleriot-relay/fw/spec"
	"github.com/burgrp/tinygo-drivers/onewire"
	"github.com/burgrp/tinygo-drivers/onewire/ds18b20"
)

type temperatureNetwork interface {
	Configure12Bit(ds18b20.ROM) ds18b20.Status
	RequireExternalPowerAll() ds18b20.Status
	StartConversionAll() ds18b20.Status
	ReadTemperatureRaw(ds18b20.ROM) (int16, ds18b20.Status)
}

type temperaturePhase uint8

const (
	temperatureConfigure temperaturePhase = iota
	temperatureStartConversion
	temperatureWaitConversion
	temperatureReadSensors
)

type temperatureState struct {
	samples           [spec.MaxDS18B20Sensors]int32
	ids               [spec.MaxDS18B20Sensors]ds18b20.ROM
	ready             uint32
	configured        uint32
	nextAction        int64
	conversionStarted int64
	interval          int64
	discoveredFirst   ds18b20.ROM
	discoveredCount   uint8
	count             uint8
	index             uint8
	phase             temperaturePhase
	ambiguous         bool
}

func (state *temperatureState) init(config spec.Config) {
	config = config.Normalized()
	state.count = uint8(len(config.DS18B20Sensors))
	state.interval = int64(config.SampleIntervalMilliseconds) * int64(time.Millisecond)
	for index, sensor := range config.DS18B20Sensors {
		state.ids[index] = ds18b20.ROM(sensor.ID)
	}
}

func (state *temperatureState) read(tag uint16) (int32, bool) {
	if tag < spec.RegDS18B20Base {
		return 0, true
	}
	index := tag - spec.RegDS18B20Base
	if index >= uint16(state.count) || state.ready&(uint32(1)<<index) == 0 {
		return 0, true
	}
	return state.samples[index], false
}

func (state *temperatureState) unbound() bool {
	return state.count == 1 && state.ids[0] == (ds18b20.ROM{})
}

func (state *temperatureState) needsDiscovery(now int64) bool {
	return state.unbound() && !state.ambiguous && now >= state.nextAction
}

func (state *temperatureState) observeDiscovery(rom onewire.ROM, status onewire.SearchStatus, now int64) (done bool, count uint8) {
	if status == onewire.SearchFound {
		if rom[0] == ds18b20.FamilyCode {
			if state.discoveredCount == 0 {
				state.discoveredFirst = ds18b20.ROM(rom)
			}
			if state.discoveredCount < 255 {
				state.discoveredCount++
			}
		}
		return false, state.discoveredCount
	}

	count = state.discoveredCount
	if status == onewire.SearchDone && state.unbound() {
		if count == 1 {
			state.ids[0] = state.discoveredFirst
		} else if count > 1 {
			state.ambiguous = true
		}
	}
	state.discoveredCount = 0
	state.discoveredFirst = ds18b20.ROM{}
	state.nextAction = now
	if status != onewire.SearchDone || state.unbound() {
		state.nextAction += state.interval
	}
	return true, count
}

// Each call performs at most one addressed sensor operation, allowing the
// caller to service RF, watchdog, and LED status between sensors.
func (state *temperatureState) service(network temperatureNetwork, clock func() int64) (sensor int, status ds18b20.Status) {
	now := clock()
	if state.count == 0 || state.unbound() || now < state.nextAction {
		return -1, ds18b20.StatusOK
	}

	switch state.phase {
	case temperatureConfigure:
		if state.index >= state.count {
			state.index = 0
			state.phase = temperatureStartConversion
			return -1, ds18b20.StatusOK
		}
		index := state.index
		state.index++
		mask := uint32(1) << index
		if state.configured&mask != 0 {
			return -1, ds18b20.StatusOK
		}
		status = network.Configure12Bit(state.ids[index])
		if status == ds18b20.StatusOK {
			state.configured |= mask
		} else {
			state.ready &^= mask
			if busUnavailable(status) {
				state.failBus(clock())
			}
		}
		return int(index), status

	case temperatureStartConversion:
		if state.configured == 0 {
			state.finishCycle(clock())
			return -1, ds18b20.StatusOK
		}
		if status = network.RequireExternalPowerAll(); status != ds18b20.StatusOK {
			state.failBus(clock())
			return -1, status
		}
		if status = network.StartConversionAll(); status != ds18b20.StatusOK {
			state.failBus(clock())
			return -1, status
		}
		state.conversionStarted = clock()
		state.nextAction = state.conversionStarted + int64(ds18b20.ConversionWait12Bit)
		state.phase = temperatureWaitConversion
		return -1, ds18b20.StatusOK

	case temperatureWaitConversion:
		state.index = 0
		state.phase = temperatureReadSensors
		return -1, ds18b20.StatusOK

	case temperatureReadSensors:
		if state.index >= state.count {
			state.finishCycle(clock())
			return -1, ds18b20.StatusOK
		}
		index := state.index
		state.index++
		mask := uint32(1) << index
		if state.configured&mask == 0 {
			state.ready &^= mask
			return -1, ds18b20.StatusOK
		}
		sample, status := network.ReadTemperatureRaw(state.ids[index])
		if status == ds18b20.StatusOK {
			state.samples[index] = int32(sample)
			state.ready |= mask
		} else if busUnavailable(status) {
			state.failBus(clock())
		} else {
			state.ready &^= mask
			state.configured &^= mask
		}
		return int(index), status
	}
	panic("bleriot-relay: invalid temperature phase")
}

func (state *temperatureState) finishCycle(now int64) {
	state.nextAction = now + state.interval
	if state.conversionStarted != 0 {
		state.nextAction = state.conversionStarted + state.interval
		if state.nextAction < now {
			state.nextAction = now
		}
	}
	state.conversionStarted = 0
	state.index = 0
	state.phase = temperatureConfigure
}

func (state *temperatureState) failBus(now int64) {
	state.ready = 0
	state.configured = 0
	state.finishCycle(now)
}

func busUnavailable(status ds18b20.Status) bool {
	return status == ds18b20.StatusNoPresence || status == ds18b20.StatusBusStuckLow
}
