//go:build tinygo

package relay

import (
	"device/py32"
	"machine"
	"runtime/interrupt"
)

type py32OneWireHardware struct {
	interruptState interrupt.State
}

func initOneWire() {
	pinSensor.Configure(machine.PinConfig{Mode: machine.PinInput})
	pinSensor.High()
	py32.GPIOA.OTYPER.SetBits(py32.GPIO_OTYPER_OT0)
	py32.GPIOA.MODER.ReplaceBits(1, py32.GPIO_MODER_MODE0_Msk, py32.GPIO_MODER_MODE0_Pos)

	py32.RCC.APBENR1.SetBits(py32.RCC_APBENR1_TIM3EN)
	py32.TIM3.CR1.Set(0)
	py32.TIM3.PSC.Set(machine.CPUFrequency()/1_000_000 - 1)
	py32.TIM3.ARR.Set(0xffff)
	py32.TIM3.CNT.Set(0)
	py32.TIM3.EGR.Set(py32.TIM_EGR_UG)
	py32.TIM3.SR.Set(0)
	py32.TIM3.CR1.Set(py32.TIM_CR1_CEN)
}

func (*py32OneWireHardware) DriveLow() {
	py32.GPIOA.BSRR.Set(py32.GPIO_BSRR_BR0)
}

func (*py32OneWireHardware) Release() {
	py32.GPIOA.BSRR.Set(py32.GPIO_BSRR_BS0)
}

func (*py32OneWireHardware) Read() bool {
	return py32.GPIOA.IDR.HasBits(py32.GPIO_IDR_ID0)
}

func (*py32OneWireHardware) Microseconds() uint16 {
	return uint16(py32.TIM3.CNT.Get())
}

func (hardware *py32OneWireHardware) EnterCritical() {
	hardware.interruptState = interrupt.Disable()
}

func (hardware *py32OneWireHardware) ExitCritical() {
	interrupt.Restore(hardware.interruptState)
}
