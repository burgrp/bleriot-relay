package relay

import (
	"testing"
	"time"

	"github.com/burgrp/bleriot-relay/fw/spec"
	"github.com/burgrp/tinygo-drivers/onewire"
	"github.com/burgrp/tinygo-drivers/onewire/ds18b20"
)

type fakeTemperatureNetwork struct {
	ids            [spec.MaxDS18B20Sensors]ds18b20.ROM
	samples        [spec.MaxDS18B20Sensors]int16
	configStatuses [spec.MaxDS18B20Sensors]ds18b20.Status
	readStatuses   [spec.MaxDS18B20Sensors]ds18b20.Status
	powerStatus    ds18b20.Status
	startStatus    ds18b20.Status
	count          int
	configCalls    int
	readCalls      int
	powerCalls     int
	startCalls     int
	now            *int64
	startLatency   int64
	readLatency    int64
}

func (network *fakeTemperatureNetwork) index(rom ds18b20.ROM) int {
	for index := range network.count {
		if network.ids[index] == rom {
			return index
		}
	}
	panic("unexpected ROM in fake temperature network")
}

func (network *fakeTemperatureNetwork) Configure12Bit(rom ds18b20.ROM) ds18b20.Status {
	network.configCalls++
	return network.configStatuses[network.index(rom)]
}

func (network *fakeTemperatureNetwork) RequireExternalPowerAll() ds18b20.Status {
	network.powerCalls++
	return network.powerStatus
}

func (network *fakeTemperatureNetwork) StartConversionAll() ds18b20.Status {
	network.startCalls++
	*network.now += network.startLatency
	return network.startStatus
}

func (network *fakeTemperatureNetwork) ReadTemperatureRaw(rom ds18b20.ROM) (int16, ds18b20.Status) {
	network.readCalls++
	*network.now += network.readLatency
	index := network.index(rom)
	return network.samples[index], network.readStatuses[index]
}

func newTemperatures(config spec.Config) temperatureState {
	var state temperatureState
	state.init(config)
	return state
}

func temperatureFixture(count int, now *int64) (temperatureState, *fakeTemperatureNetwork) {
	config := spec.Config{DS18B20Sensors: make([]spec.DS18B20Sensor, count)}
	network := &fakeTemperatureNetwork{count: count, now: now}
	for index := range count {
		id := [8]byte{0x28, byte(index + 1)}
		id[7] = onewire.CRC8(id[:7])
		config.DS18B20Sensors[index].ID = id
		network.ids[index] = ds18b20.ROM(id)
		network.samples[index] = int16(400 + index)
	}
	return newTemperatures(config), network
}

func startTemperatureConversion(t *testing.T, state *temperatureState, network *fakeTemperatureNetwork, clock func() int64) {
	t.Helper()
	for step := 0; step < int(state.count)+3; step++ {
		if _, status := state.service(network, clock); status != ds18b20.StatusOK {
			t.Fatalf("start conversion failed: %s", status)
		}
		if state.phase == temperatureWaitConversion {
			return
		}
	}
	t.Fatal("conversion did not start within the bounded number of passes")
}

func TestNoTemperatureRegistersDisablesBusOperations(t *testing.T) {
	now := int64(time.Second)
	state, network := temperatureFixture(0, &now)
	for range 10 {
		state.service(network, func() int64 { return now })
	}
	if network.configCalls != 0 || network.powerCalls != 0 || network.startCalls != 0 || network.readCalls != 0 {
		t.Fatal("disabled temperatures accessed the bus")
	}
	if value, null := state.read(spec.RegDS18B20Base); value != 0 || !null {
		t.Fatalf("disabled temperature = (%d, %v), want (0, true)", value, null)
	}
}

func TestConversionWaitAndAddressedReads(t *testing.T) {
	now := int64(time.Second)
	state, network := temperatureFixture(2, &now)
	network.startLatency = int64(2 * time.Millisecond)
	network.samples[0] = -162
	clock := func() int64 { return now }
	if _, null := state.read(spec.RegDS18B20Base); !null {
		t.Fatal("temperature must start NULL")
	}
	startTemperatureConversion(t, &state, network, clock)
	started := state.conversionStarted
	if network.configCalls != 2 || network.startCalls != 1 {
		t.Fatal("sensors were not configured before the single broadcast conversion")
	}
	if state.nextAction != now+int64(800*time.Millisecond) {
		t.Fatal("conversion wait must start after the broadcast completes")
	}
	now = state.nextAction - 1
	state.service(network, clock)
	if network.readCalls != 0 {
		t.Fatal("read a sensor before the full 800 ms conversion wait")
	}
	now++
	state.service(network, clock)
	for index := range 2 {
		before := network.readCalls
		state.service(network, clock)
		if network.readCalls != before+1 {
			t.Fatal("a pass must read exactly one addressed sensor")
		}
		if got, null := state.read(spec.DS18B20RegisterTag(index)); got != int32(network.samples[index]) || null {
			t.Fatalf("sensor %d = (%d, %v), want (%d, false)", index, got, null, network.samples[index])
		}
	}
	if _, null := state.read(spec.DS18B20RegisterTag(2)); !null {
		t.Fatal("unconfigured sensor tag did not return NULL")
	}
	state.service(network, clock)
	if state.nextAction != started+state.interval {
		t.Fatal("cycle was not scheduled from conversion start")
	}
	if state.phase != temperatureConfigure {
		t.Fatal("finished cycle did not return to configuration")
	}
}

func TestSensorFailureIsIsolatedAndRecovers(t *testing.T) {
	now := int64(time.Second)
	state, network := temperatureFixture(2, &now)
	clock := func() int64 { return now }
	startTemperatureConversion(t, &state, network, clock)
	now = state.nextAction
	state.service(network, clock)
	state.service(network, clock)
	network.readStatuses[1] = ds18b20.StatusScratchpadCRC
	if index, status := state.service(network, clock); index != 1 || status != ds18b20.StatusScratchpadCRC {
		t.Fatalf("sensor failure = (%d, %s), want (1, scratchpad CRC)", index, status)
	}
	if _, null := state.read(spec.RegDS18B20Base); null {
		t.Fatal("one failed sensor invalidated its successful neighbor")
	}
	if value, null := state.read(spec.RegDS18B20Base + 1); value != 0 || !null {
		t.Fatal("failed sensor retained a non-NULL sample")
	}
	state.service(network, clock)
	now = state.nextAction
	network.readStatuses[1] = ds18b20.StatusOK
	before := network.configCalls
	startTemperatureConversion(t, &state, network, clock)
	if network.configCalls != before+1 {
		t.Fatal("recovery should reconfigure only the failed sensor")
	}
	now = state.nextAction
	state.service(network, clock)
	state.service(network, clock)
	state.service(network, clock)
	if _, null := state.read(spec.RegDS18B20Base + 1); null {
		t.Fatal("recovered sensor remained NULL")
	}
}

func TestBusFailuresClearAllSamples(t *testing.T) {
	for _, status := range []ds18b20.Status{
		ds18b20.StatusNoPresence,
		ds18b20.StatusBusStuckLow,
	} {
		t.Run(status.String(), func(t *testing.T) {
			now := int64(time.Second)
			state, network := temperatureFixture(2, &now)
			clock := func() int64 { return now }
			startTemperatureConversion(t, &state, network, clock)
			now = state.nextAction
			state.service(network, clock)
			state.service(network, clock)
			network.readStatuses[1] = status
			if _, got := state.service(network, clock); got != status {
				t.Fatalf("bus error = %s, want %s", got, status)
			}
			if state.ready != 0 || state.configured != 0 || state.phase != temperatureConfigure {
				t.Fatal("unavailable bus did not clear all validity and configuration bits")
			}
		})
	}
}

func TestConfigurationFailures(t *testing.T) {
	t.Run("isolated sensor fault", func(t *testing.T) {
		now := int64(time.Second)
		state, network := temperatureFixture(2, &now)
		network.configStatuses[0] = ds18b20.StatusScratchpadCRC
		clock := func() int64 { return now }
		if index, status := state.service(network, clock); index != 0 || status != ds18b20.StatusScratchpadCRC {
			t.Fatalf("configuration error = (%d, %s)", index, status)
		}
		startTemperatureConversion(t, &state, network, clock)
		now = state.nextAction
		state.service(network, clock)
		state.service(network, clock)
		state.service(network, clock)
		if _, null := state.read(spec.RegDS18B20Base); !null {
			t.Fatal("failed configuration published a valid sample")
		}
		if _, null := state.read(spec.RegDS18B20Base + 1); null {
			t.Fatal("failed configuration invalidated another sensor")
		}
	})
	t.Run("unavailable bus", func(t *testing.T) {
		now := int64(time.Second)
		state, network := temperatureFixture(2, &now)
		state.ready = 3
		network.configStatuses[0] = ds18b20.StatusBusStuckLow
		if _, status := state.service(network, func() int64 { return now }); status != ds18b20.StatusBusStuckLow {
			t.Fatal("configuration bus fault was not reported")
		}
		if state.ready != 0 || state.configured != 0 || state.nextAction != now+state.interval {
			t.Fatal("configuration bus fault did not invalidate samples and schedule a retry")
		}
	})
	t.Run("no configured sensors", func(t *testing.T) {
		now := int64(time.Second)
		state, network := temperatureFixture(1, &now)
		network.configStatuses[0] = ds18b20.StatusConfiguration
		clock := func() int64 { return now }
		if _, status := state.service(network, clock); status != ds18b20.StatusConfiguration {
			t.Fatal("sensor configuration failure was not reported")
		}
		state.service(network, clock)
		state.service(network, clock)
		if network.startCalls != 0 || state.nextAction != now+state.interval {
			t.Fatal("empty configured bank attempted a conversion or failed to back off")
		}
	})
}

func TestPowerAndConversionFailuresAreReported(t *testing.T) {
	for _, test := range []struct {
		name         string
		power, start ds18b20.Status
		want         ds18b20.Status
	}{
		{"parasite power", ds18b20.StatusParasitePower, ds18b20.StatusOK, ds18b20.StatusParasitePower},
		{"conversion failure", ds18b20.StatusOK, ds18b20.StatusNoPresence, ds18b20.StatusNoPresence},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := int64(time.Second)
			state, network := temperatureFixture(1, &now)
			network.powerStatus, network.startStatus = test.power, test.start
			state.ready = 1
			clock := func() int64 { return now }
			state.service(network, clock)
			state.service(network, clock)
			if index, status := state.service(network, clock); index != -1 || status != test.want {
				t.Fatalf("broadcast failure = (%d, %s), want (-1, %s)", index, status, test.want)
			}
			if state.ready != 0 || state.configured != 0 || state.nextAction != now+state.interval {
				t.Fatal("failed conversion retained valid data or did not schedule a retry")
			}
		})
	}
}

func TestTwentySensorsYieldBetweenReads(t *testing.T) {
	now := int64(time.Second)
	state, network := temperatureFixture(spec.MaxDS18B20Sensors, &now)
	network.readLatency = int64(11700 * time.Microsecond)
	clock := func() int64 { return now }
	startTemperatureConversion(t, &state, network, clock)
	started := state.conversionStarted
	now = state.nextAction
	state.service(network, clock)
	for index := range spec.MaxDS18B20Sensors {
		before := network.readCalls
		state.service(network, clock)
		if network.readCalls != before+1 {
			t.Fatalf("sensor pass %d did not yield after one addressed read", index)
		}
	}
	state.service(network, clock)
	if state.ready != (1<<spec.MaxDS18B20Sensors)-1 {
		t.Fatal("full sensor bank did not publish every temperature")
	}
	if now-started != int64(1034*time.Millisecond) || state.nextAction != now {
		t.Fatal("full bank should extend the cycle instead of catching up in a blocking batch")
	}
}

func TestWatchdogIsServicedDuringTemperatureConversion(t *testing.T) {
	control := newControl(spec.Config{WatchdogEnabled: true})
	control.write(spec.RegWatchdog, 1, false, 0)
	control.write(spec.RegRelay, 1, false, 0)
	now := int64(600 * time.Millisecond)
	state, network := temperatureFixture(1, &now)
	clock := func() int64 { return now }
	startTemperatureConversion(t, &state, network, clock)
	now = nanosecondsPerSecond
	control.service(true, now)
	state.service(network, clock)
	if control.relay != 0 || state.phase != temperatureWaitConversion || network.readCalls != 0 {
		t.Fatal("waiting for a conversion prevented watchdog expiry")
	}
}

func TestAutomaticDiscovery(t *testing.T) {
	now := int64(time.Second)
	_, network := temperatureFixture(2, &now)
	first, second := onewire.ROM(network.ids[0]), onewire.ROM(network.ids[1])
	auto := spec.Config{DS18B20Sensors: []spec.DS18B20Sensor{{}}}
	t.Run("single and unrelated family", func(t *testing.T) {
		state := newTemperatures(auto)
		state.observeDiscovery(onewire.ROM{0x10}, onewire.SearchFound, now)
		state.observeDiscovery(first, onewire.SearchFound, now)
		done, count := state.observeDiscovery(onewire.ROM{}, onewire.SearchDone, now)
		if !done || count != 1 || state.ids[0] != ds18b20.ROM(first) || state.needsDiscovery(now) {
			t.Fatal("single DS18B20 did not bind independently of unrelated families")
		}
	})
	t.Run("ambiguous", func(t *testing.T) {
		state := newTemperatures(auto)
		state.observeDiscovery(first, onewire.SearchFound, now)
		state.observeDiscovery(second, onewire.SearchFound, now)
		state.observeDiscovery(onewire.ROM{}, onewire.SearchDone, now)
		if !state.ambiguous || !state.unbound() || state.needsDiscovery(now+state.interval) {
			t.Fatal("ambiguous discovery chose an unstable automatic mapping")
		}
	})
	t.Run("missing and retry", func(t *testing.T) {
		state := newTemperatures(auto)
		state.observeDiscovery(onewire.ROM{}, onewire.SearchDone, now)
		if state.needsDiscovery(now+state.interval-1) || !state.needsDiscovery(now+state.interval) {
			t.Fatal("missing sensor discovery did not honor the retry interval")
		}
		state.observeDiscovery(first, onewire.SearchFound, now+state.interval)
		state.observeDiscovery(onewire.ROM{}, onewire.SearchDone, now+state.interval)
		if state.unbound() {
			t.Fatal("sensor connected after boot did not bind")
		}
	})
	t.Run("incomplete search", func(t *testing.T) {
		state := newTemperatures(auto)
		state.observeDiscovery(first, onewire.SearchFound, now)
		state.observeDiscovery(onewire.ROM{}, onewire.SearchCRC, now)
		if !state.unbound() || state.discoveredCount != 0 {
			t.Fatal("incomplete search bound an ID or retained discovery state")
		}
	})
	t.Run("explicit mapping stays in inventory order", func(t *testing.T) {
		state, _ := temperatureFixture(2, &now)
		state.observeDiscovery(second, onewire.SearchFound, now)
		state.observeDiscovery(first, onewire.SearchFound, now)
		state.observeDiscovery(onewire.ROM{}, onewire.SearchDone, now)
		if state.ids[0] != ds18b20.ROM(first) || state.ids[1] != ds18b20.ROM(second) {
			t.Fatal("discovery order renumbered explicitly mapped sensors")
		}
	})
}

func TestTemperatureServiceDoesNotAllocate(t *testing.T) {
	now := int64(time.Second)
	state, network := temperatureFixture(2, &now)
	clock := func() int64 { return now }
	if allocations := testing.AllocsPerRun(1000, func() {
		state.service(network, clock)
		now += int64(time.Second)
	}); allocations != 0 {
		t.Fatalf("steady-state temperature service allocated %v times", allocations)
	}
}
