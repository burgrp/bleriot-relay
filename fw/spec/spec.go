package spec

import (
	"errors"

	"github.com/burgrp/tinygo-drivers/onewire/ds18b20"
)

type Config struct {
	WatchdogEnabled            bool
	SampleIntervalMilliseconds uint32
	DS18B20Sensors             []DS18B20Sensor
}

// An empty sensor list disables temperatures. One zero ID binds automatically
// only when exactly one DS18B20 is discovered; multidrop requires explicit IDs.
type DS18B20Sensor struct {
	ID [8]byte
}

const (
	RegRelay       uint16 = 1
	RegWatchdog    uint16 = 2
	RegDS18B20Base uint16 = 0x0100

	MaxDS18B20Sensors           = 20
	WatchdogMaxSeconds    int32 = 60
	OfflineTimeoutSeconds       = 5

	DefaultSampleIntervalMilliseconds = 1000
	MinimumSampleIntervalMilliseconds = 800
)

func DS18B20RegisterTag(index int) uint16 {
	if index < 0 || index >= MaxDS18B20Sensors {
		panic("bleriot-relay: DS18B20 sensor index out of range")
	}
	return RegDS18B20Base + uint16(index)
}

func (config Config) Validate() error {
	if len(config.DS18B20Sensors) > MaxDS18B20Sensors {
		return errors.New("at most 20 DS18B20 sensors are supported")
	}
	for index, sensor := range config.DS18B20Sensors {
		if sensor.ID == ([8]byte{}) {
			if len(config.DS18B20Sensors) != 1 {
				return errors.New("every multidrop DS18B20 sensor requires an explicit ID")
			}
			continue
		}
		if status := ds18b20.ROM(sensor.ID).Validate(); status != ds18b20.StatusOK {
			return errors.New(status.String())
		}
		for previous := range index {
			if config.DS18B20Sensors[previous].ID == sensor.ID {
				return errors.New("DS18B20 sensor IDs must be unique")
			}
		}
	}
	return nil
}

func (config Config) Normalized() Config {
	if config.SampleIntervalMilliseconds == 0 {
		config.SampleIntervalMilliseconds = DefaultSampleIntervalMilliseconds
	}
	if len(config.DS18B20Sensors) != 0 &&
		config.SampleIntervalMilliseconds < MinimumSampleIntervalMilliseconds {
		config.SampleIntervalMilliseconds = MinimumSampleIntervalMilliseconds
	}
	return config
}
