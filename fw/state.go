package relay

import "github.com/burgrp/bleriot-relay/fw/spec"

const nanosecondsPerSecond = int64(1_000_000_000)

type controlState struct {
	relay           int32
	watchdogEnabled bool
	watchdog        watchdogLease
}

func newControl(config spec.Config) controlState {
	return controlState{watchdogEnabled: config.WatchdogEnabled}
}

func (state *controlState) read(tag uint16, now int64) (int32, bool) {
	state.expireWatchdog(now)
	switch tag {
	case spec.RegRelay:
		return state.relay, false
	case spec.RegWatchdog:
		if !state.watchdogEnabled {
			return 0, true
		}
		return state.watchdog.remaining(now), false
	default:
		return 0, true
	}
}

func (state *controlState) write(tag uint16, value int32, null bool, now int64) {
	state.expireWatchdog(now)
	if tag == spec.RegWatchdog {
		if !state.watchdogEnabled {
			return
		}
		if !state.watchdog.set(value, null, now) {
			state.relay = 0
		}
		return
	}
	if tag != spec.RegRelay || null || state.watchdogExpired(now) {
		return
	}
	state.relay = 0
	if value != 0 {
		state.relay = 1
	}
}

func (state *controlState) service(online bool, now int64) {
	state.expireWatchdog(now)
	if !online {
		state.relay = 0
	}
}

func (state *controlState) expireWatchdog(now int64) {
	if state.watchdogEnabled && state.watchdog.expire(now) {
		state.relay = 0
	}
}

func (state *controlState) watchdogExpired(now int64) bool {
	return state.watchdogEnabled && !state.watchdog.active(now)
}

type watchdogLease struct {
	deadline int64
}

func (lease *watchdogLease) set(value int32, null bool, now int64) bool {
	if null || value <= 0 {
		lease.deadline = 0
		return false
	}
	if value > spec.WatchdogMaxSeconds {
		value = spec.WatchdogMaxSeconds
	}
	lease.deadline = now + int64(value)*nanosecondsPerSecond
	return true
}

func (lease *watchdogLease) active(now int64) bool {
	return lease.deadline != 0 && now < lease.deadline
}

func (lease *watchdogLease) remaining(now int64) int32 {
	if !lease.active(now) {
		return 0
	}
	return int32((lease.deadline - now + nanosecondsPerSecond - 1) / nanosecondsPerSecond)
}

func (lease *watchdogLease) expire(now int64) bool {
	if lease.deadline == 0 || now < lease.deadline {
		return false
	}
	lease.deadline = 0
	return true
}

func statusLEDColor(online, pulse, watchdogExpired, relayOn bool) int32 {
	if !online {
		if pulse {
			return 0xFF3000
		}
		return 0
	}
	if watchdogExpired {
		if pulse {
			return 0x0000FF
		}
		return 0
	}
	if relayOn {
		return 0x00FF10
	}
	return 0xA05000
}
