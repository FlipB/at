package at

import (
	"context"
	"fmt"
	"io"
)

// ErrUnexpectedPrompt is returned when an AT device is found to be at an unexpected interactive prompt
var ErrUnexpectedPrompt = fmt.Errorf("device unexpectedly at interactive prompt")

// Opt is a type representing options to configure AT
type Opt func(*AT)

// WithProfile configures AT to use the supplied device profile
func WithProfile(profile DeviceProfile) Opt {
	return func(at *AT) {
		at.profile = profile
	}
}

// AT represents an AT device, such as a 3g usb modem.
// AT is not threadsafe.
// TODO upgrade this to support notifications and commands on the same port, also maybe mutliple ports?
// Add support for multiple commands and concurrency.
type AT struct {
	device  *ScanWriter
	profile DeviceProfile
}

// NewAT constructs a new AT.
// AT will require exclusive access to the supplied ReadWriter
func NewAT(device io.ReadWriter, opts ...Opt) *AT {
	a := &AT{
		profile: DefaultProfile{},
	}
	for _, o := range opts {
		o(a)
	}
	a.device = NewScanWriter(device, a.profile)
	return a
}

// Send cmd and EOL to device, returns data and any error
// cmd string should only contain one AT command. Linebreak is automatically added.
func (a *AT) Send(ctx context.Context, cmd string) (string, error) {
	// TODO take EOL sequence from device profile?
	// Some devices might output CRLF while expecting newline?
	err := a.device.Write(append([]byte(cmd), []byte("\r\n")...))
	if err != nil {
		return "", err
	}

	response := []byte{}

	ctx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()
	for a.device.Scan(ctx) {
		if err != nil {
			return "", err
		}
		state := a.device.State()
		switch state {
		case ATEcho:
			continue
		case ATData:
			// waiting for data output
			response = append(response, a.device.Bytes()...)
			continue
		case ATReady:
			return string(response), nil
		case ATError:
			return string(response), a.device.Err()
		case ATPrompt:
			return string(append(response, a.device.Bytes()...)), ErrUnexpectedPrompt
		case ATNotice:
			// It is probably data
			fmt.Printf("DEBUG: Unexpected ATNotice: %s\n", a.device.String())
			// waiting for data output
			response = append(response, a.device.Bytes()...)
			continue
		default:
			panic("inexhaustive state switch")
		}
	}
	return string(append(response, a.device.Bytes()...)), a.device.Err()
}

// SendPrompt sends cmd and EOL expecting an interative prompt, at which point promptCmd and Sub char is sent.
// Linebreaks are automatically added at the end of the cmd string.
func (a *AT) SendPrompt(ctx context.Context, cmd, promptCmd string) (string, error) {
	// TODO take EOL sequence from device profile?
	// Some devices might output CRLF while expecting newline?
	err := a.device.Write(append([]byte(cmd), []byte("\r\n")...))
	if err != nil {
		return "", err
	}

	// TODO make into a proper buffer?
	response := []byte{}

	promptWritten := false
	ctx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()
	for a.device.Scan(ctx) {
		state := a.device.State()
		switch state {
		case ATData:
			response = append(response, a.device.Bytes()...)
			continue
		case ATEcho:
			continue
		case ATPrompt:
			if promptWritten {
				return string(append(response, a.device.Bytes()...)), ErrUnexpectedPrompt
			}
			// TODO move escape sequence to deviceProfile now that we have that?
			err := a.device.Write([]byte(promptCmd + string([]byte{0x1a})))
			if err != nil {
				return string(append(response, a.device.Bytes()...)), fmt.Errorf("error writing at prompt: %v", err)
			}
			promptWritten = true
			continue
		case ATReady:
			// waiting for data output
			return string(append(response, a.device.Bytes()...)), nil
		case ATError:
			return string(response), a.device.Err() // fmt.Errorf("error sending interactive command: %s", a.cmdPort.String())
		case ATNotice:
			// It is probably data
			fmt.Printf("DEBUG: Unexpected ATNotice: %s\n", a.device.String())
			// waiting for data output
			response = append(response, a.device.Bytes()...)
			continue
		default:
			panic("inexhaustive state switch")
		}
	}
	return string(response), a.device.Err()
}
