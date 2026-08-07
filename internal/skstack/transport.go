// Package skstack drives a Rohm BP35A1 Wi-SUN dongle over its SKSTACK
// command protocol, taking it from power-on through PANA-established
// B-route connection to a smart power meter.
package skstack

import (
	"fmt"
	"io"

	"go.bug.st/serial"
)

// Transport is the serial-port surface the client needs. go.bug.st/serial's
// Port type satisfies this directly, and tests supply an in-memory fake.
type Transport = io.ReadWriteCloser

// NewSerialTransport opens device in raw 115200-8N1 mode, matching the
// termios configuration SKSock.pm set up by hand.
func NewSerialTransport(device string) (Transport, error) {
	mode := &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	port, err := serial.Open(device, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial device %q: %w", device, err)
	}
	return port, nil
}
