//go:build !tinygo

package spec

import (
	"fmt"
	"strconv"

	"github.com/burgrp/bleriot/lib/shared/conversion"
	"github.com/burgrp/bleriot/lib/shared/firmware"
	"github.com/burgrp/bleriot/lib/shared/inventory"
	"github.com/burgrp/bleriot/lib/shared/puya"
	"github.com/burgrp/tinygo-drivers/onewire/ds18b20"
)

var Chip = puya.PY32F003x6

func TypeForConfig(config Config) inventory.DeviceType {
	if err := config.Validate(); err != nil {
		panic("bleriot-relay: " + err.Error())
	}
	deviceType := inventory.DeviceType{
		Name: "bleriot-relay",
		Chip: Chip,
		Firmware: firmware.Manifest{
			Package: "github.com/burgrp/bleriot-relay/fw",
			TinyGo: firmware.TinyGoProfile{
				Scheduler:        firmware.SchedulerNone,
				StackSizeBytes:   1024,
				GarbageCollector: firmware.GCLeaking,
				Serial:           firmware.SerialRTT,
				SizeReport:       firmware.SizeReportHTML,
				PrintAllocs:      true,
			},
			PyOCD: firmware.PyOCDProfile{
				FrequencyHz:              100_000,
				LoadMode:                 firmware.ConnectUnderReset,
				RTTMode:                  firmware.ConnectAttach,
				GDBMode:                  firmware.ConnectAttach,
				Reclaim:                  true,
				ReclaimDelayMilliseconds: 1000,
			},
		},
		Registers: []inventory.Register{
			{Tag: RegRelay, Name: "state", Type: inventory.TypeBool},
		},
	}
	if config.WatchdogEnabled {
		deviceType.Registers = append(deviceType.Registers, inventory.Register{
			Tag:  RegWatchdog,
			Name: "watchdog",
			Type: inventory.TypeInt,
			Metadata: map[string]string{
				"min":  "0",
				"max":  "60",
				"unit": "seconds",
			},
		})
	}

	count := len(config.DS18B20Sensors)
	for index, sensor := range config.DS18B20Sensors {
		name := "temperature"
		if count > 1 {
			name += "." + strconv.Itoa(index+1)
		}
		metadata := map[string]string{"mode": "ds18b20", "unit": "\u00b0C"}
		if sensor.ID != ([8]byte{}) {
			metadata["id"] = fmt.Sprintf("%x", sensor.ID)
		}
		deviceType.Registers = append(deviceType.Registers, inventory.Register{
			Tag:        DS18B20RegisterTag(index),
			Name:       name,
			Type:       inventory.TypeFloat,
			ReadOnly:   true,
			Conversion: temperatureConversion(),
			Metadata:   metadata,
		})
	}
	return deviceType
}

func temperatureConversion() inventory.Conversion {
	result := conversion.Scale(1.0 / ds18b20.RawUnitsPerDegree)
	result.Encode = nil
	decode := result.Decode
	result.Decode = func(raw int32) (any, error) {
		if raw < int32(ds18b20.MinimumTemperatureRaw) || raw > int32(ds18b20.MaximumTemperatureRaw) {
			return nil, fmt.Errorf("bleriot-relay: DS18B20 raw temperature %d is outside the device range", raw)
		}
		return decode(raw)
	}
	return result
}
