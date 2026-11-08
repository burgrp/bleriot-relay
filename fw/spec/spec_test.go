package spec

import (
	"fmt"
	"testing"

	"github.com/burgrp/bleriot/lib/shared/firmware"
	"github.com/burgrp/bleriot/lib/shared/inventory"
	"github.com/burgrp/tinygo-drivers/onewire"
)

func sensorID(serial byte) [8]byte {
	id := [8]byte{0x28, serial}
	id[7] = onewire.CRC8(id[:7])
	return id
}

func TestRegisterContract(t *testing.T) {
	for _, test := range []struct {
		count   int
		enabled bool
	}{
		{0, false}, {1, false}, {MaxDS18B20Sensors, false},
		{0, true}, {1, true}, {MaxDS18B20Sensors, true},
	} {
		t.Run(fmt.Sprintf("%d-watchdog-%v", test.count, test.enabled), func(t *testing.T) {
			count := test.count
			config := Config{WatchdogEnabled: test.enabled, DS18B20Sensors: make([]DS18B20Sensor, count)}
			for index := range count {
				config.DS18B20Sensors[index].ID = sensorID(byte(index + 1))
			}
			deviceType := TypeForConfig(config)
			if err := deviceType.Validate(); err != nil {
				t.Fatal(err)
			}
			temperatureStart := 1
			if test.enabled {
				temperatureStart++
			}
			if len(deviceType.Registers) != temperatureStart+count {
				t.Fatalf("register count = %d, want %d", len(deviceType.Registers), temperatureStart+count)
			}
			relay := deviceType.Registers[0]
			if relay.Tag != 1 || relay.Name != "state" || relay.Type != inventory.TypeBool || relay.ReadOnly {
				t.Fatalf("incorrect relay contract: %#v", relay)
			}
			if test.enabled {
				watchdog := deviceType.Registers[1]
				if watchdog.Tag != 2 || watchdog.Name != "watchdog" || watchdog.Type != inventory.TypeInt || watchdog.ReadOnly {
					t.Fatalf("incorrect watchdog contract: %#v", watchdog)
				}
				if watchdog.Metadata["min"] != "0" || watchdog.Metadata["max"] != "60" || watchdog.Metadata["unit"] != "seconds" {
					t.Fatal("incorrect watchdog metadata")
				}
			} else {
				for _, register := range deviceType.Registers {
					if register.Tag == RegWatchdog || register.Name == "watchdog" {
						t.Fatal("disabled watchdog was still exported")
					}
				}
			}
			for index, register := range deviceType.Registers[temperatureStart:] {
				name := "temperature"
				if count > 1 {
					name += fmt.Sprintf(".%d", index+1)
				}
				if register.Tag != uint16(256+index) || register.Name != name || register.Type != inventory.TypeFloat || !register.ReadOnly {
					t.Fatalf("incorrect temperature contract: %#v", register)
				}
				if register.Conversion.Decode == nil || register.Conversion.Encode != nil ||
					register.Metadata["unit"] != "\u00b0C" || register.Metadata["id"] != fmt.Sprintf("%x", config.DS18B20Sensors[index].ID) {
					t.Fatal("incorrect temperature conversion or metadata")
				}
			}
			profile := deviceType.Firmware.TinyGo
			if deviceType.Chip.TinygoTarget != "py32f003x6" ||
				profile.Scheduler != firmware.SchedulerNone || profile.StackSizeBytes != 1024 ||
				profile.GarbageCollector != firmware.GCLeaking || profile.Serial != firmware.SerialRTT {
				t.Fatal("incorrect constrained-MCU build profile")
			}
		})
	}
}

func TestAutomaticTemperatureContract(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		deviceType := TypeForConfig(Config{WatchdogEnabled: enabled, DS18B20Sensors: []DS18B20Sensor{{}}})
		want := 2
		if enabled {
			want++
		}
		if len(deviceType.Registers) != want || deviceType.Registers[want-1].Metadata["id"] != "" {
			t.Fatal("incorrect automatic single-sensor register count or fixed ID")
		}
	}
}

func TestWatchdogIsDisabledByDefault(t *testing.T) {
	deviceType := TypeForConfig(Config{})
	if len(deviceType.Registers) != 1 || deviceType.Registers[0].Tag != RegRelay {
		t.Fatal("default configuration should export only relay state")
	}
}

func TestConfigValidation(t *testing.T) {
	valid := sensorID(1)
	wrongFamily := valid
	wrongFamily[0] = 0x10
	wrongFamily[7] = onewire.CRC8(wrongFamily[:7])
	badCRC := valid
	badCRC[7] ^= 1
	for _, test := range []struct {
		name    string
		sensors []DS18B20Sensor
		valid   bool
	}{
		{"none", nil, true},
		{"automatic single", []DS18B20Sensor{{}}, true},
		{"explicit single", []DS18B20Sensor{{ID: valid}}, true},
		{"explicit multidrop", []DS18B20Sensor{{ID: valid}, {ID: sensorID(2)}}, true},
		{"too many", make([]DS18B20Sensor, MaxDS18B20Sensors+1), false},
		{"missing multidrop ID", []DS18B20Sensor{{ID: valid}, {}}, false},
		{"wrong family", []DS18B20Sensor{{ID: wrongFamily}}, false},
		{"bad CRC", []DS18B20Sensor{{ID: badCRC}}, false},
		{"duplicate", []DS18B20Sensor{{ID: valid}, {ID: valid}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (Config{DS18B20Sensors: test.sensors}).Validate()
			if (err == nil) != test.valid {
				t.Fatalf("validation = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestTemperatureConversion(t *testing.T) {
	conversion := TypeForConfig(Config{DS18B20Sensors: []DS18B20Sensor{{}}}).Registers[1].Conversion
	for _, test := range []struct {
		raw  int32
		want float64
	}{
		{-880, -55},
		{-162, -10.125},
		{0, 0},
		{401, 25.0625},
		{2000, 125},
	} {
		value, err := conversion.Decode(test.raw)
		if err != nil || value != test.want {
			t.Errorf("decode(%d) = (%v, %v), want (%v, nil)", test.raw, value, err, test.want)
		}
	}
	for _, raw := range []int32{-881, 2001, 1<<31 - 1} {
		if value, err := conversion.Decode(raw); err == nil || value != nil {
			t.Errorf("invalid raw temperature %d = (%v, %v), want (nil, error)", raw, value, err)
		}
	}
}

func TestSampleIntervalNormalization(t *testing.T) {
	for _, test := range []struct {
		value, want uint32
	}{
		{0, 1000},
		{1, 800},
		{799, 800},
		{800, 800},
		{1000, 1000},
		{2000, 2000},
	} {
		config := Config{SampleIntervalMilliseconds: test.value, DS18B20Sensors: []DS18B20Sensor{{}}}.Normalized()
		if config.SampleIntervalMilliseconds != test.want {
			t.Fatalf("interval %d normalized to %d, want %d", test.value, config.SampleIntervalMilliseconds, test.want)
		}
	}
}

func TestInvalidSensorTagPanics(t *testing.T) {
	for _, index := range []int{-1, MaxDS18B20Sensors} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid sensor index did not panic")
				}
			}()
			DS18B20RegisterTag(index)
		})
	}
}

func TestInvalidInventoryConfigPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("invalid inventory config did not panic")
		}
	}()
	TypeForConfig(Config{DS18B20Sensors: []DS18B20Sensor{{}, {}}})
}
