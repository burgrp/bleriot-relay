# BleRiot Relay

TinyGo/BleRiot firmware for the PY32F003/PAN2110 relay board, with one
active-low on/off triac output, an optional watchdog lease, and up to 20 externally
powered DS18B20 thermometers. No other sensor types are supported.

## Hardware

The firmware targets `py32f003x6`: 32 KiB flash, 4 KiB RAM, and a 24 MHz
CPU clock. The schematic uses the SOP16 `PY32F003W1xSx` pinout; verify that
the fitted part has at least this memory density. The pinout and density
follow the PY32F003 Datasheet Rev1.7, Figure 3-10 and Table 1-3.

| Function | MCU pin | Schematic net |
| --- | --- | --- |
| Relay/optotriac, active low | PB1 | `~TRIAC` |
| WS2812 status LED | PB0 | `LED` |
| DS18B20 data | PA0 | `SENS` |
| PAN2110 SCK | PA2 | `RF_SCK` |
| PAN2110 bidirectional data | PA1 | `RF_DATA` |
| PAN2110 CSN, active low | PA4 | `~RF_CS` |

Unlike the SOP8 Sense board, this SOP16 board has a separate reset pin;
firmware does not rewrite the PB0/reset option byte.

Connect DS18B20s in parallel on `SENS1`:

| Pin | Connection |
| ---: | --- |
| 1 | GND |
| 2 | DQ |
| 3 | +5 V sensor supply |

R7 is a 4.7 kOhm pull-up from `SENS` to 3.3 V, on the MCU side of the
existing R6 270-ohm series resistor and BAT54S protection. Do not add a
5 V DQ pull-up. The firmware uses open-drain PA0 and a 1 MHz TIM3 counter;
it does not substitute the MCU's weak internal pull-up. Parasite power is
unsupported and reported as a sensor fault.

R7 is included in both the schematic and the updated PCB. Regenerate the
existing fabrication outputs before manufacturing this revision. Older boards
without R7 need the pull-up retrofitted before using DS18B20s.

## Registers

The development inventory explicitly enables the watchdog and exports three
registers:

| Registry name | Wire tag | Type | Access |
| --- | ---: | --- | --- |
| `relay.state` | 1 | bool | Read/write |
| `relay.watchdog` | 2 | int, seconds | Read/write |
| `relay.temperature` | 256 | float, degrees Celsius | Read-only |

`relay.state` reports the commanded output, not electrical load feedback.
Wire boolean values are normalized to 0 or 1. NULL relay writes and unknown
or read-only writes are ignored, matching CPG's boolean-output behavior;
write `false` explicitly to turn the relay off.

Temperature tags are permanently reserved as 256-275, matching Sense's
dedicated DS18B20 range. With multiple configured sensors, Registry names
are `relay.temperature.1`, `relay.temperature.2`, and so on. Temperatures
are transmitted as signed Q12.4 integers and converted to Celsius in the
hub without losing the 1/16-degree resolution.

The number of temperature registers is inventory configuration, not discovery
order or the number of currently connected sensors. An empty sensor list
exports only state plus the watchdog if enabled. With `WatchdogEnabled: false`,
the watchdog register is absent, including when sensors are enabled.
Tag 2 remains reserved and temperature tags never shift.
The development inventory's one-temperature-register configuration
continues to export a NULL temperature if no sensor is connected.
Tags must not be renumbered or reused.

## Watchdog, Offline State, And LED

The relay always starts off. Firmware preloads PB1 high before enabling its
output driver, avoiding an active-low startup pulse.

`WatchdogEnabled` is a boolean firmware configuration property and defaults to
`false`. Set it to `true` to export `watchdog` and enforce its lease. When
disabled or omitted, relay writes need no lease, tag 2 reads return NULL and
writes are ignored, and there is no watchdog-expired blue indication.
Startup-off and RF-loss safety remain active in both modes.

An enabled watchdog starts expired. Positive writes arm a lease, clamped to
60 seconds. Reads return the remaining whole seconds rounded up.
Zero, negative, and NULL watchdog
writes expire the lease immediately. While expired, relay SETs are acknowledged
but ignored. Expiry clears the retained relay command and drives PB1 high.
Re-arming alone never restores the old command; send a fresh relay SET.

The node starts RF-offline. The default five-second timeout without a valid
BleRiot packet forces the same relay-off state and clears the retained command,
matching REG6. The development inventory explicitly selects five seconds;
external inventories can override it with `Instance.OnlineTimeout`.
Reconnection does not restore the command. If enabled, a still-active watchdog lease
is retained across RF loss; renew it if necessary and send a fresh relay SET.
A radio initialization failure also leaves the output off and runs the
offline heartbeat.

| Status | LED |
| --- | --- |
| RF offline, regardless of watchdog | Orange-red `0xff3000`, 200 ms on / 800 ms off |
| Online, enabled watchdog expired | Pure blue `0x0000ff`, 200 ms on / 800 ms off |
| Online, watchdog disabled or armed, relay off | Steady amber `0xa05000` |
| Online, watchdog disabled or armed, relay on | Steady green `0x00ff10` |

The heartbeat comes from BleRiot's status phase. Colors match CPG, with
REG6's global brightness limit of 128/255. RF offline has priority over
watchdog expiry. Temperature faults do not change the output or its LED status.

## Firmware Configuration

Edit `relayConfig` in [`fw/cmd/dev/main.go`](fw/cmd/dev/main.go).
The same config is passed to `spec.TypeForConfig` and baked into the firmware,
keeping the hub register table and node layout consistent:

```go
var relayConfig = spec.Config{
    WatchdogEnabled: true,
    SampleIntervalMilliseconds: 1000,
    DS18B20Sensors: []spec.DS18B20Sensor{{}},
}
```

To disable the watchdog, set `WatchdogEnabled: false` or omit the property.
Rebuild/reflash the node and restart the hub after changing it so register
export and firmware enforcement stay consistent.

One zero ID automatically binds only when exactly one DS18B20 is discovered.
If more than one is found, automatic binding is deliberately refused and the
temperature remains NULL. Firmware prints copy-pasteable `[8]byte{...}` ROM IDs
over RTT at boot. For multidrop, put one `spec.DS18B20Sensor{ID: ...}` entry
per register into the slice, using those IDs in the desired register order.
Every multidrop ID must be explicit, unique, family 0x28, and CRC-valid.

To disable temperatures, use `DS18B20Sensors: nil`. Up to 20 entries are
supported. Rebuild/reflash the node and restart the hub after changing the list.
An external inventory can use `RegistryNames` to give tags meaningful
deployment-specific names without changing their wire identities.

Sampling defaults to 1000 ms; intervals below 800 ms are raised to 800 ms.
Firmware configures addressed sensors for volatile 12-bit resolution,
broadcasts one conversion, waits the full 800 ms while servicing RF and
watchdog/LED status, then reads at most one sensor per main-loop pass.
The interval is measured from conversion start, and a large sensor bank
stretches the cycle rather than producing a blocking catch-up batch.

ROM and scratchpad CRCs, sensor power, configuration, and temperature range
are checked by the shared 1-Wire driver. Failed sensors become NULL independently
and are retried. A missing or stuck-low bus invalidates all temperature samples;
faults are reported over RTT. A previously missing automatic single sensor
can bind when connected later. An ambiguous automatic bus requires explicit
IDs or a reboot after correcting the wiring.

## Build And Run

The importable firmware module and specification are in [`fw/`](fw/).
External inventories import:

```go
import "github.com/burgrp/bleriot-relay/fw/spec"
```

Dependencies are pinned in [`fw/go.mod`](fw/go.mod). Use Go 1.25.2 or newer,
TinyGo with `py32f003x6` support, GNU Arm Embedded binutils, and pyOCD with
the Puya PY32F003 pack. The build profile uses scheduler-none, a 1 KiB system
stack, leaking GC, and RTT serial.

Both watchdog modes with 0, 1, and 20 sensors were built with Go 1.26.3 and
TinyGo 0.42.0-dev-f8e1f465 / LLVM 18.1.8:

| Watchdog | Temperature registers | Flash, including initialized data | Reserved RAM |
| --- | ---: | ---: | ---: |
| Disabled | 0 | 15,644 bytes | 2,224 bytes |
| Disabled | 1, automatic | 16,508 bytes | 2,224 bytes |
| Disabled | 20, explicit IDs | 16,948 bytes | 2,224 bytes |
| Enabled | 0 | 15,652 bytes | 2,224 bytes |
| Enabled | 1, automatic | 16,512 bytes | 2,224 bytes |
| Enabled | 20, explicit IDs | 16,948 bytes | 2,224 bytes |

Reserved RAM includes the 1,024-byte system stack and static data, leaving
1,872 bytes for the heap. Host tests verify allocation-free steady-state
control and temperature sampling, and cover optional-watchdog behavior,
fail-safe, LED priority, discovery,
sensor isolation/recovery, timing, and the register/conversion contract.
The updated schematic/PCB pass ERC, DRC, and schematic parity checks.
Hardware has not been flashed or exercised as part of this implementation.

```sh
go -C fw test ./...
make -C fw build
go -C fw run ./cmd/dev node gen --name relay
make -C fw install-pack
make -C fw flash
```

The default ELF and size report are generated under
[`fw/.bleriot/firmware/relay/`](fw/.bleriot/firmware/relay/), not directly in
the firmware module directory.

The flash target programs the board and opens RTT. Confirm the intended
board and probe before using it. The SOP16 SWD pins are PA13 (DIO) and
PA14/PF1 (CLK); `X1` has no target-voltage or reset connection.

**The checked-in development inventory uses an intentionally public bench
address and all-zero XTEA key on RF channel 37 / S8. Do not deploy this identity.**
Use `make -C fw new` to generate private provisioning for your own inventory,
kept outside tracked source. Never commit production keys. External inventories
can import this specification and build their own baked identity.

With a Registry server and the matching MCP2210/PAN2110 hub dongle:

```sh
go -C fw run ./cmd/dev hub --registry http://localhost:8080 --diagnostics rf
```

When enabled, renew `relay.watchdog` before commanding `relay.state`. Temperature registers
are polled by the hub; measurements do not require an armed output watchdog.

## Bench Validation

Start with an isolated low-voltage supply and no mains connected:

1. Confirm PB1 stays high at power-up and the LED shows the offline heartbeat.
2. Start RF traffic, arm a short watchdog, and send fresh on/off commands.
   Confirm PB1 is low only for the on command and the LED is green/amber.
3. Stop watchdog renewals while maintaining RF traffic. Confirm PB1 goes
   high, state reads false, and the LED blinks blue. Re-arm without a relay
   SET and verify the output remains off.
4. Command on, stop RF traffic for five seconds, and confirm output off
   with the orange-red heartbeat. Restore RF without a relay SET and verify
   it remains off.
5. Verify R7 is fitted, test zero/one/multiple DS18B20s, and use explicit ROM
   IDs for multidrop. Check negative temperatures, unplug one sensor, and
   confirm its NULL does not invalidate neighboring sensors.
6. Repeat watchdog/RF-loss tests during conversion and addressed reads.
   Scope the 1-Wire waveform and test the actual cable/sensor population.
7. Rebuild with `WatchdogEnabled: false` and restart the hub. Verify watchdog
   is absent, relay SETs work without a lease, online status is steady amber/green,
   and RF loss still switches the output off with the orange-red heartbeat.

The triac output is on/off only, not a dimmer. Mains testing, load suitability,
protection, isolation, enclosure, and thermal checks require qualified personnel.
Firmware builds and host tests do not replace electrical or hardware-in-loop
validation.
