package at

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Uses stringer from golang.org/x/tools/cmd/stringer
//go:generate stringer -type=State

// State is an enum to classify AT device output
type State int

const (
	// ATReady means command was successful / the device is in a good, ready state
	ATReady State = iota
	// ATError represents an AT command failure (previous command failed) - Device should still be in an OK for next command.
	ATError
	// ATNotice represents a notification / unsolicited output (example USSD responses, "RING" etc). Device should still be able to accept a new command.
	ATNotice

	// ATData represents unclassified data / read more to get success or fail - hopefully
	ATData

	// ATPrompt means waiting for additional input / interactive mode (SMS, needs Escape or Submit)
	ATPrompt
	// ATEcho are lines mirroring the input command when ATE1.
	ATEcho
)

// DeviceProfile implements hardware specific / quirky behavior
type DeviceProfile interface {
	// InferState should handle nil deviceOutputData as OK because reasons
	// Idk if this is still true. // TODO investigate
	InferState(deviceOutputData []byte, lastCmd []byte, outputCountSinceCmd int) (State, error)
	// Hooks should return an implementation of DeviceProfileHooks or nil.
	Hooks() DeviceHooks
	EOLSequence() []byte
	PromptSequence() []byte
}

// DeviceHooks allow DeviceProfiles to fix quirks in the device by modifying input and output directly
type DeviceHooks interface {
	// OutputTokenizer is an additional tokenizer that is sent output from device
	// it returns the advance count (how many bytes to consume from deviceOutputData),
	// a token (essentially an output line) and true if deviceOutputData contains a full token.
	OutputTokenizer(deviceOutputData []byte) (advance int, token []byte, matched bool)
	// Output allows device profile to fix quirks in the device's output
	Output(deviceOutputData []byte, lastCmd []byte, outputCountSinceCmd int) (filteredData []byte)
	// Input allows device profile to fix quirks in the device's input handling
	Input(deviceInputData []byte) (filteredData []byte)
}

// ScanWriter handles input and output to/from an AT modem
type ScanWriter struct {
	// promptSeq is the byte sequence used to identify the prompt state
	// it's the bytes output by modem to signal it wants more data
	// Used when sending SMS
	// default is ">"
	promptSeq []byte
	// eolSeq is the byte sequence sent by AT modem to denote EOL
	// default is "\r\n"
	eolSeq []byte

	state scanWriterState

	device io.ReadWriter

	writer     io.Writer
	writerLock sync.Mutex

	reader  *bufio.Scanner
	profile DeviceProfile

	commandEcho      commandEchoState
	unblockThreshold time.Duration

	debug bool
}

type commandEchoState int

const (
	commandEchoStateUnknown commandEchoState = iota
	commandEchoEnabled
	commandEchoDisabled
)

type scanWriterState struct {
	sync.Mutex

	// current contains the output from the last scan
	current []byte
	// err contains the error
	err error

	// cmd is the last/current (presumed) AT command
	cmd []byte
	// outputCount is the count of output lines/tokens for the current cmd
	outputCount int
	// cmdActive gets set when a command is completed in order to enable usage on notify port
	// which sends unsolicited messages - we dont want to attribute that to the previous command
	cmdActive bool
}

// NewScanWriter returns a new, initialized ScanWriter
// NOTE: Not threadsafe
func NewScanWriter(device io.ReadWriter, deviceProfile DeviceProfile) *ScanWriter {
	return newScanWriter(device, deviceProfile, false)
}

func newScanWriter(device io.ReadWriter, deviceProfile DeviceProfile, debug bool) *ScanWriter {
	if deviceProfile == nil {
		deviceProfile = DefaultProfile{}
	}

	d := &ScanWriter{
		eolSeq:    deviceProfile.EOLSequence(),
		promptSeq: deviceProfile.PromptSequence(),
		device:    device,
		//reader:       bufio.NewScanner(device), // reader is updated in d.newScanner()
		writer:           device,
		profile:          deviceProfile,
		unblockThreshold: time.Second,
		debug:            debug,
	}

	d.newScanner(nil)

	return d
}

// newScanner creates a new scanner with a starting buffer to be read from first.
// Updates the internal scanner in the ScanWriter.
func (d *ScanWriter) newScanner(initialBuffer []byte) {

	// temp hack to get a new scanner when there's been a reading error.
	// currently the internal buffer is lost. we have to fix that
	splitter := makeSplitter(initialBuffer, d.eolSeq, d.promptSeq, d.profile.Hooks(), d)

	d.reader = bufio.NewScanner(d.device)
	d.reader.Split(splitter)
}

func makeSplitter(buf []byte, eolSeq, promptSeq []byte, hooks DeviceHooks, debugPrinter debugfPrinter) bufio.SplitFunc {

	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}

		debugPrinter.debugf("Splitter data '%s'\n", strings.ReplaceAll(strings.ReplaceAll(string(data), "\r", "CR"), "\n", "LF"))

		if hooks != nil {
			if advance, token, match := matchAdvanceBuffer(&buf, data, tokenizerToTwoBuffers(hooks.OutputTokenizer)); match {
				return advance, token, nil
			}
		}
		if advance, token, match := matchAdvanceBuffer(&buf, data, PatternSplitTokenizer(eolSeq)); match {
			return advance, token, nil
		}
		if advance, token, match := matchAdvanceBuffer(&buf, data, PatternSplitTokenizer(promptSeq)); match {
			return advance, token, nil
		}
		if atEOF {
			return len(data), data, nil
		}
		// wait for more data
		return 0, nil, nil
	}
}

// Bytes returns the data read from device during the previous call to Scan, unless an error ocurred.
// Requires a call to Scan before calling Bytes.
// Returned byte slice is strictly read-only and only valid until the next call to Scan.
func (d *ScanWriter) Bytes() []byte {
	// data/token/line received
	d.state.Lock()
	defer d.state.Unlock()
	return d.state.current
}

// String returns the data read from device during the previous call to Scan as a string, unless an error occurred.
// Output is only valid until the next call to Scan.
func (d *ScanWriter) String() string {
	// data/token/line received
	d.state.Lock()
	defer d.state.Unlock()
	return string(d.state.current)
}

// Err returns the error that occured during the call to Scan or nil if no error occured.
// Output is only valid until the next call to Scan.
func (d *ScanWriter) Err() error {
	d.state.Lock()
	defer d.state.Unlock()
	state, _ := d.inferState(d.state.current, d.state.cmd, d.state.outputCount)
	if d.state.err == nil && state == ATError {
		return errors.New(string(d.state.current))
	}
	return d.state.err
}

// State returns the classification of the read output after a call to Next.
// Output is only valid until the next call to Scan.
func (d *ScanWriter) State() State {
	// at prompt or not
	d.state.Lock()
	defer d.state.Unlock()
	state, _ := d.inferState(d.state.current, d.state.cmd, d.state.outputCount)
	return state
}

func (d *ScanWriter) readyForInput() bool {
	switch d.State() {
	case ATReady:
		return true
	case ATError:
		return true
	case ATNotice:
		return true
	case ATPrompt:
		// not really ready for command, just ready for input?
		return true
	case ATData:
		return false
	case ATEcho:
		return false
	default:
		panic("inexhaustive switch")
	}
}

// Write to the device. It's up to the caller to confirm the device is in a good state before calling Write.
// You should only send one command at a time. You have to end with EOL
func (d *ScanWriter) Write(data []byte) error {

	if !d.readyForInput() {
		// This check might be bad if we're no good at guessing the state.
		// Make this toggleable?
		return fmt.Errorf("waiting for last command to finish")
	}

	// TODO: revisit the check below.
	// We could also do a partial write up to the first \r\n sequence if we change signature
	// to return the number of bytes written. Also added benefit of matching io.Writer interface.
	if bytes.Count(data, d.eolSeq) > 1 {
		return fmt.Errorf("only one command at a time")
	}

	hooks := d.profile.Hooks()
	if hooks != nil {
		data = hooks.Input(data)
	}
	d.writerLock.Lock()
	defer d.writerLock.Unlock()

	_, err := d.writer.Write(data)
	if err != nil {
		return err
	}

	if bytes.HasSuffix(data, d.eolSeq) {
		switch d.State() {
		case ATReady:
			d.setCmd(data)
		case ATError:
			d.setCmd(data)
		}
	}

	return nil
}

func (d *ScanWriter) setCmd(data []byte) {
	d.state.Lock()
	defer d.state.Unlock()
	start := bytes.LastIndex(data[0:len(data)-len(d.eolSeq)], d.eolSeq)
	if start == -1 {
		start = 0
	}
	d.state.cmd = data[start:]
	d.state.outputCount = 0
	d.state.cmdActive = true
}

// Scan reads device output, updates scanner internal state and returns true if read successful.
func (d *ScanWriter) Scan(ctx context.Context) bool {
	if ctx.Err() != nil {
		d.setError(ctx.Err())
		return false
	}

	// handle cases where read is blocked waiting for input
	// when context cancels we sent a fake command to trigger "COMMAND NOT SUPPORT"
	stop := make(chan struct{}) // stop channel is used to stop the go routine that sends AT_UNBLOCK when this function returns
	defer close(stop)           // Cannot defer close - will race with context!
	go func() {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			// Double check that we're still running, we don't want to send AT_UNBLOCK unless we *really* have to.
			// If we remove this there is a guaranteed race between the defer and the context cancel
			// TODO make this better.
			select {
			case <-stop:
				return // stop is closed - this means the read must have been unblocked.
			default:
			}
			d.writerLock.Lock()
			defer d.writerLock.Unlock()

			err := d.cancelRead()
			if err != nil {
				d.debugf("WARNING: cancelRead returned error in ctx cancelled: %v\n", err)
			}
			return
		}
	}()

	// get next token from input
	_ = d.reader.Scan()
	data, err := d.reader.Bytes(), d.reader.Err()
	if err != nil {
		d.setError(fmt.Errorf("error reading from device: %v", err))
		// on io errors we have to recreate the scanner to be able to keep reading
		// we pass in the data returned on error to the new instance to
		// ensure we dont skip any data in the stream
		d.newScanner(data)
		return false
	}

	// if context is cancelled AT_UNBLOCK has been sent and needs to be cleared from the data stream
	if ctx.Err() != nil {
		d.debugf("Read unblocked with Data '%s', err %v\n", data, err)

		err = d.clearReader(data)
		if err != nil {
			d.setError(err)
		} else {
			d.setError(ctx.Err())
		}
		return false
	}

	d.state.Lock()
	defer d.state.Unlock()

	d.state.outputCount++

	// reset cmd and output count if the last sent command is old / already completed
	// this must be unsolicited output
	if !d.state.cmdActive {
		// reset cmd and output count so that the state can be correctly inferred
		d.state.cmd = nil
		d.state.outputCount = 0
	}

	hooks := d.profile.Hooks()
	if hooks != nil {
		data = hooks.Output(data, d.state.cmd, d.state.outputCount)
	}
	d.state.current = data

	// FIXME: some of this logic is duplicated in recoverState() (for handling AT_UNLOCK cleanup)
	state, err := d.inferState(d.state.current, d.state.cmd, d.state.outputCount)
	if err != nil {
		// don't use setError because we already have the lock
		d.state.err = fmt.Errorf("unable to classify device output: %v", err)
		return false
	}
	d.state.err = nil
	switch state {
	case ATError:
		d.state.cmdActive = false
	case ATReady:
		d.state.cmdActive = false
		if bytes.Equal(d.state.cmd, []byte("ATE0"+string(d.eolSeq))) {
			d.commandEcho = commandEchoDisabled
		}
		if bytes.Equal(d.state.cmd, []byte("ATE1"+string(d.eolSeq))) {
			d.commandEcho = commandEchoEnabled
		}
	}
	return true
}

func (d *ScanWriter) inferState(data []byte, cmd []byte, outputCount int) (State, error) {
	return d.profile.InferState([]byte(string(data)), []byte(string(cmd)), outputCount)
}

func (d *ScanWriter) setError(err error) {
	d.state.Lock()
	defer d.state.Unlock()
	d.state.err = err
}

// Clear will reset state of <d> and send a command want wait for matching expected output to ensure input and output buffers are in lock step.Clear
// All data currently in the buffers will be ignored
// TODO add debug flag to make this print the lines that are ignored.
func (d *ScanWriter) Clear() error {
	// make <d> go into ready state by any means necessary.
	// should clear / flush input /output buffers to ensure we have clean buffers

	d.writerLock.Lock()
	err := d.cancelRead()
	d.writerLock.Unlock()
	if err != nil {
		return fmt.Errorf("unable to cancel read: %v", err)
	}
	err = d.clearReader(nil)
	if err != nil {
		return fmt.Errorf("unable to clear reader: %v", err)
	}

	d.state.Lock()
	defer d.state.Unlock()

	d.state.outputCount = 0
	d.state.cmdActive = false
	d.state.err = nil

	return nil
}

type debugfPrinter interface {
	debugf(format string, a ...interface{})
}

func (d *ScanWriter) debugf(format string, a ...interface{}) {
	if !d.debug {
		return
	}

	fmt.Printf("ScanWriter DEBUG: "+format, a...)
}
