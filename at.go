package at

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// ErrUnexpectedPrompt is returned when an AT device is found to be at an unexpected interactive prompt
var ErrUnexpectedPrompt = fmt.Errorf("device unexpectedly at interactive prompt")

// atOpt is a type representing options to configure AT
type atOpt func(*AT)

// WithProfile configures AT to use the supplied device profile
func WithProfile(profile DeviceProfile) atOpt {
	return func(at *AT) {
		at.profile = profile
	}
}

// WithDebug configures AT to output debug logs
func WithDebug() atOpt {
	return func(at *AT) {
		at.rawDevice = eavesdrop{at.rawDevice}
		at.debug = true
	}
}

// AT represents an AT device, such as a 3g usb modem.
type AT struct {
	rawDevice io.ReadWriter
	device    *ScanWriter
	profile   DeviceProfile
	writeLock sync.Mutex

	debug bool
}

// NewAT constructs a new AT.
// AT will require exclusive access to the supplied ReadWriter
func NewAT(device io.ReadWriter, opts ...atOpt) *AT {
	a := &AT{
		rawDevice: device,
		profile:   DefaultProfile{},
		writeLock: sync.Mutex{},
	}
	for _, opt := range opts {
		opt(a)
	}

	a.device = newScanWriter(a.rawDevice, a.profile, a.debug)

	return a
}

// Send cmd and EOL to device, returns data and any error
// cmd string should only contain one AT command. Linebreak is automatically added.
// NOTE: on command success the final line containing "OK" is stripped from output returned.
// TODO: revisit this behaviour.
func (a *AT) Send(ctx context.Context, cmd string) (string, error) {
	a.lock()
	defer a.unlock()

	err := a.device.Write(append([]byte(cmd), []byte(a.profile.EOLSequence())...))
	if err != nil {
		return "", err
	}

	response := []byte{}

	ctx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()
	for a.device.Scan(ctx) {
		state := a.device.State()
		switch state {
		case ATEcho:
			continue
		case ATData:
			// waiting for data output
			response = append(response, a.device.Bytes()...)
			continue
		case ATReady:
			data := a.device.String()
			if data == "OK"+string(a.profile.EOLSequence()) {
				return string(response), nil
			}

			return string(append(response, a.device.Bytes()...)), nil
		case ATError:
			return string(response), a.device.Err()
		case ATPrompt:
			return string(append(response, a.device.Bytes()...)), ErrUnexpectedPrompt
		case ATNotice:
			// It is probably data
			a.debugf("Unexpected ATNotice: %s\n", a.device.Bytes())
			// waiting for data output
			response = append(response, a.device.Bytes()...)
			continue
		default:
			panic("inexhaustive state switch")
		}
	}
	// if scan fails, we shouldnt read a.device.Bytes() - should be stale.
	return string(response), a.device.Err()
}

// SendPrompt sends cmd and EOL expecting an interative prompt, at which point promptCmd and Sub char is sent.
// Linebreaks are automatically added at the end of the cmd string.
func (a *AT) SendPrompt(ctx context.Context, cmd, promptCmd string) (string, error) {
	a.lock()
	defer a.unlock()

	err := a.device.Write(append([]byte(cmd), []byte(a.profile.EOLSequence())...))
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
			a.debugf("Unexpected ATNotice: %s\n", a.device.Bytes())
			// waiting for data output
			response = append(response, a.device.Bytes()...)
			continue
		default:
			panic("inexhaustive state switch")
		}
	}
	return string(response), a.device.Err()
}

func (a *AT) lock() {
	a.writeLock.Lock()
}

func (a *AT) unlock() {
	a.writeLock.Unlock()
}

func (a *AT) debugf(format string, v ...interface{}) {
	if !a.debug {
		return
	}
	fmt.Printf("AT DEBUG: "+format, v...)
}
