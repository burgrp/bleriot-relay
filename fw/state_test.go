package relay

import (
	"testing"

	"github.com/burgrp/bleriot-relay/fw/spec"
)

func TestWatchdogLease(t *testing.T) {
	const start = int64(12345)
	var lease watchdogLease
	if lease.remaining(start) != 0 || lease.active(start) {
		t.Fatal("watchdog must start expired")
	}
	if !lease.set(1000, false, start) {
		t.Fatal("positive write did not arm watchdog")
	}
	for _, test := range []struct {
		elapsed int64
		want    int32
	}{
		{0, 60},
		{1, 60},
		{nanosecondsPerSecond, 59},
		{59*nanosecondsPerSecond + 1, 1},
		{60*nanosecondsPerSecond - 1, 1},
		{60 * nanosecondsPerSecond, 0},
		{61 * nanosecondsPerSecond, 0},
	} {
		if got := lease.remaining(start + test.elapsed); got != test.want {
			t.Errorf("remaining after %d ns = %d, want %d", test.elapsed, got, test.want)
		}
	}
	if lease.expire(start + 60*nanosecondsPerSecond - 1) {
		t.Fatal("watchdog expired before deadline")
	}
	if !lease.expire(start+60*nanosecondsPerSecond) || lease.expire(start+61*nanosecondsPerSecond) {
		t.Fatal("watchdog should expire exactly once at deadline")
	}
}

func TestRelayWatchdogLifecycle(t *testing.T) {
	const start = int64(1000)
	state := newControl(spec.Config{WatchdogEnabled: true})
	state.write(spec.RegRelay, 1, false, start)
	if state.relay != 0 {
		t.Fatal("expired watchdog accepted relay write")
	}
	state.write(spec.RegWatchdog, 2, false, start)
	state.write(spec.RegRelay, 1, false, start)
	if value, null := state.read(spec.RegRelay, start); value != 1 || null {
		t.Fatalf("armed relay = (%d, %v), want (1, false)", value, null)
	}
	deadline := start + 2*nanosecondsPerSecond
	if value, null := state.read(spec.RegRelay, deadline); value != 0 || null {
		t.Fatalf("relay at deadline = (%d, %v), want (0, false)", value, null)
	}
	state.write(spec.RegRelay, 1, false, deadline)
	if state.relay != 0 {
		t.Fatal("relay write at deadline restored output")
	}
	state.write(spec.RegWatchdog, 5, false, deadline+1)
	if state.relay != 0 {
		t.Fatal("re-arming watchdog restored old relay state")
	}
	state.write(spec.RegRelay, 1, false, deadline+1)
	if state.relay != 1 {
		t.Fatal("fresh relay write after re-arming was ignored")
	}
}

func TestWatchdogRenewalAtExpiryClearsRelay(t *testing.T) {
	state := newControl(spec.Config{WatchdogEnabled: true})
	state.write(spec.RegWatchdog, 1, false, 0)
	state.write(spec.RegRelay, 1, false, 0)
	state.write(spec.RegWatchdog, 60, false, nanosecondsPerSecond)
	if state.relay != 0 || !state.watchdog.active(nanosecondsPerSecond) {
		t.Fatal("renewal at deadline must clear the old command before re-arming")
	}
}

func TestWatchdogExplicitExpiry(t *testing.T) {
	for _, test := range []struct {
		name  string
		value int32
		null  bool
	}{
		{name: "zero"},
		{name: "negative", value: -1},
		{name: "null", value: 60, null: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newControl(spec.Config{WatchdogEnabled: true})
			state.write(spec.RegWatchdog, 60, false, 0)
			state.write(spec.RegRelay, 1, false, 0)
			state.write(spec.RegWatchdog, test.value, test.null, 1)
			if state.relay != 0 || state.watchdog.active(1) {
				t.Fatal("explicit watchdog expiry did not clear relay and lease")
			}
		})
	}
}

func TestRelayBooleanAndIgnoredWrites(t *testing.T) {
	state := newControl(spec.Config{WatchdogEnabled: true})
	state.write(spec.RegWatchdog, 60, false, 0)
	for _, value := range []int32{0, 1, 2, -1} {
		state.write(spec.RegRelay, value, false, 1)
		want := int32(0)
		if value != 0 {
			want = 1
		}
		if state.relay != want {
			t.Fatalf("boolean normalization of %d = %d, want %d", value, state.relay, want)
		}
	}
	state.write(spec.RegRelay, 0, true, 1)
	state.write(spec.RegDS18B20Base, 0, false, 1)
	state.write(999, 0, false, 1)
	if state.relay != 1 {
		t.Fatal("NULL, read-only, or unknown write changed relay")
	}
	if value, null := state.read(999, 1); value != 0 || !null {
		t.Fatalf("unknown read = (%d, %v), want (0, true)", value, null)
	}
	state.read(spec.RegDS18B20Base, 60*nanosecondsPerSecond)
	if state.relay != 0 {
		t.Fatal("temperature GET did not service watchdog expiry")
	}
}

func TestOfflineClearsRelayWithoutRestoringIt(t *testing.T) {
	state := newControl(spec.Config{WatchdogEnabled: true})
	state.write(spec.RegWatchdog, 60, false, 0)
	state.write(spec.RegRelay, 1, false, 0)
	state.service(false, spec.OfflineTimeoutSeconds*nanosecondsPerSecond)
	if state.relay != 0 || !state.watchdog.active(5*nanosecondsPerSecond) {
		t.Fatal("RF loss must clear the relay without changing an active lease")
	}
	state.service(true, 6*nanosecondsPerSecond)
	if state.relay != 0 {
		t.Fatal("reconnection restored old relay state")
	}
	state.write(spec.RegRelay, 1, false, 6*nanosecondsPerSecond)
	if state.relay != 1 {
		t.Fatal("fresh command with active lease did not restore relay")
	}
	state.service(true, 60*nanosecondsPerSecond)
	if state.relay != 0 {
		t.Fatal("watchdog expiry in service did not clear relay")
	}
}

func TestStatusLEDColor(t *testing.T) {
	for _, test := range []struct {
		name                       string
		online, pulse, expired, on bool
		want                       int32
	}{
		{"offline on", false, true, false, true, 0xFF3000},
		{"offline off", false, false, false, true, 0},
		{"offline priority", false, true, true, true, 0xFF3000},
		{"expired on", true, true, true, true, 0x0000FF},
		{"expired off", true, false, true, true, 0},
		{"relay off", true, true, false, false, 0xA05000},
		{"relay off without pulse", true, false, false, false, 0xA05000},
		{"relay on", true, true, false, true, 0x00FF10},
		{"relay on without pulse", true, false, false, true, 0x00FF10},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := statusLEDColor(test.online, test.pulse, test.expired, test.on); got != test.want {
				t.Fatalf("color = %#06x, want %#06x", got, test.want)
			}
		})
	}
}

func TestDisabledWatchdogAllowsRelayWithoutLease(t *testing.T) {
	state := newControl(spec.Config{})
	if state.watchdogEnabled || state.relay != 0 {
		t.Fatal("default configuration must disable watchdog and start with relay off")
	}
	state.write(spec.RegRelay, 1, false, 0)
	if value, null := state.read(spec.RegRelay, 0); value != 1 || null {
		t.Fatal("disabled watchdog still required a lease for the relay")
	}
	if value, null := state.read(spec.RegWatchdog, 0); value != 0 || !null {
		t.Fatal("disabled watchdog tag must return NULL")
	}
	for _, test := range []struct {
		value int32
		null  bool
	}{{60, false}, {0, false}, {-1, false}, {60, true}} {
		state.write(spec.RegWatchdog, test.value, test.null, 0)
		if state.relay != 1 || state.watchdog.deadline != 0 {
			t.Fatal("disabled watchdog accepted a lease or changed the relay")
		}
	}
	state.service(true, 120*nanosecondsPerSecond)
	if state.relay != 1 || state.watchdogExpired(120*nanosecondsPerSecond) {
		t.Fatal("disabled watchdog expired or stopped the relay")
	}
	if color := statusLEDColor(true, false, state.watchdogExpired(120*nanosecondsPerSecond), state.relay != 0); color != 0x00FF10 {
		t.Fatal("disabled watchdog did not use the steady relay LED indication")
	}
	state.write(spec.RegRelay, 0, false, 120*nanosecondsPerSecond)
	if color := statusLEDColor(true, true, state.watchdogExpired(120*nanosecondsPerSecond), state.relay != 0); color != 0xA05000 {
		t.Fatal("disabled watchdog showed expiry instead of idle amber")
	}
}

func TestDisabledWatchdogRetainsOfflineSafety(t *testing.T) {
	state := newControl(spec.Config{WatchdogEnabled: false})
	state.write(spec.RegRelay, 1, false, 0)
	state.service(false, spec.OfflineTimeoutSeconds*nanosecondsPerSecond)
	if state.relay != 0 {
		t.Fatal("disabling watchdog also disabled RF-loss safety")
	}
	if color := statusLEDColor(false, true, state.watchdogExpired(5*nanosecondsPerSecond), state.relay != 0); color != 0xFF3000 {
		t.Fatal("disabled watchdog did not retain the offline heartbeat")
	}
	state.service(true, 6*nanosecondsPerSecond)
	if state.relay != 0 {
		t.Fatal("reconnection restored an old relay command")
	}
	state.write(spec.RegRelay, 1, false, 6*nanosecondsPerSecond)
	if state.relay != 1 {
		t.Fatal("disabled watchdog did not accept a fresh command after reconnection")
	}
}

func TestControlDoesNotAllocate(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		state := newControl(spec.Config{WatchdogEnabled: enabled})
		state.write(spec.RegWatchdog, 60, false, 0)
		if allocations := testing.AllocsPerRun(1000, func() {
			state.write(spec.RegRelay, 1, false, 1)
			state.read(spec.RegRelay, 1)
			state.service(true, 1)
		}); allocations != 0 {
			t.Fatalf("steady-state control allocated %v times (watchdog enabled: %v)", allocations, enabled)
		}
	}
}
