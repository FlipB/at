package at

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
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
	// ATData represents unclassified data / read more to get success or fail - hopefully
	ATData
	// ATPrompt means waiting for additional input / interactive mode (SMS, needs Escape or Submit)
	ATPrompt
	// ATEcho are lines mirroring the input command when ATE1.
	ATEcho
	// ATNotice represents a notification / unsolicited output (example USSD responses, "RING" etc)
	ATNotice
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

	current []byte
	err     error

	// cmd is the last/current (presumed) AT command
	cmd []byte
	// outputCount is the count of output lines/tokens for the current cmd
	outputCount int
	// cmdActive gets set when a command is completed in order to enable usage on notify port
	// which sends unsolicited messages - we dont want to attribute that to the previous command
	cmdActive bool

	device io.ReadWriter

	writer io.Writer

	reader  *bufio.Scanner
	profile DeviceProfile
}

// NewScanWriter returns a new, initialized ScanWriter
// NOTE: Not threadsafe
func NewScanWriter(device io.ReadWriter, deviceProfile DeviceProfile) *ScanWriter {

	if deviceProfile == nil {
		deviceProfile = DefaultProfile{}
	}

	d := &ScanWriter{
		eolSeq:    deviceProfile.EOLSequence(),
		promptSeq: deviceProfile.PromptSequence(),
		device:    device,
		//reader:       bufio.NewScanner(device), // reader is updated in d.newScanner()
		writer:  device,
		profile: deviceProfile,
	}

	d.newScanner(nil)

	return d
}

// newScanner creates a new scanner with a starting buffer to be read from first.
// Updates the internal scanner in the ScanWriter.
func (d *ScanWriter) newScanner(initialBuffer []byte) {

	// temp hack to get a new scanner when there's been a reading error.
	// currently the internal buffer is lost. we have to fix that
	splitter := makeSplitter(initialBuffer, d.eolSeq, d.promptSeq, d.profile.Hooks())

	d.reader = bufio.NewScanner(d.device)
	d.reader.Split(splitter)
}

func makeSplitter(buf []byte, eolSeq, promptSeq []byte, hooks DeviceHooks) bufio.SplitFunc {

	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
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

// Bytes returns the data read from device during the previous call to Next, unless an error ocurred.
// Requires a call to next before calling Bytes.
// Even on errors data can be set.
func (d ScanWriter) Bytes() []byte {
	// data/token/line received
	// TODO figure out if we need to change this behaviour.
	// which is least astonishing, the fact that bytes returns bytes on an error,
	// or that bytes doesnt always return the received bytes?
	//if d.err == nil && d.State() == ATError {
	//	return nil
	//}
	return d.current
}

// String returns the data read from device during the previous call to Next as a string, unless an error occurred.
func (d ScanWriter) String() string {
	// data/token/line received
	return string(d.Bytes())
}

// Err returns the error that occured during the call to Next or nil if no error occured
func (d ScanWriter) Err() error {
	if d.err == nil && d.State() == ATError {
		return errors.New(string(d.current))
	}
	return d.err
}

// State returns the classification of the read output after a call to Next.
func (d ScanWriter) State() State {
	// at prompt or not
	state, _ := d.profile.InferState(d.current, d.cmd, d.outputCount)
	return state
}

// Write to the device. It's up to the caller to confirm the device is in a good state before calling Write.
// You should only send one command at a time. You have to end with EOL
func (d *ScanWriter) Write(data []byte) error {

	/*
		if d.State() == ATData {
			// This check is terrible because if we are stuck in the reader we cannot send AT_UNBLOCK
			// Weäre better off trusting the user to do the right thing. Will document this instead.
			return fmt.Errorf("waiting for last command to finish")
		}
	*/

	// FIXME if we send AT\r\n\r\n then the command is not detected properly
	// We should also try to support multiple commands at the same time, eg. the input:
	// "AT\r\n\r\nAT\r\n"
	// But then we have to change cmd to be a slice/log of past commands
	// and then we probably have to go though and remove commands as they finish,
	// and what happens if we miss one? Might be worth a shot, but state tracking has to be spot on, or we'll have leaks.

	// We could also do a partial write up to the first \r\n sequence if we change signature
	// to return the number of bytes written. Also added benefit of matching io.Writer interface.

	// I still like the idea of keeping a log / ring buffer of written commands that we match against in order
	// The alternative is to perform the split on \r\n in at.Send and handle multilple commands in that function nicely somehow

	// I've decided this is better handled outsite of this type. This file is complicated enough,
	// and having ring buffers on the input commands might make sense, but so would having a ring buffer of outputs
	// in order to easier support multi threaded access to the device (especially with support for both predefined and custom commands)
	// For instance if you have a device which is both command port and notify port, you would HAVE to continuously read,
	// even if the user is not interested in reading the command outputs.

	hooks := d.profile.Hooks()
	if hooks != nil {
		data = hooks.Input(data)
	}
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
	start := bytes.LastIndex(data[0:len(data)-len(d.eolSeq)], d.eolSeq)
	if start == -1 {
		start = 0
	}
	d.cmd = data[start:]
	d.outputCount = 0
	d.cmdActive = true
}

// Scan reads device output, updates scanner internal state and returns true if read successful.
func (d *ScanWriter) Scan(ctx context.Context) bool {
	if ctx.Err() != nil {
		d.err = ctx.Err()
		return false
	}

	// handle cases where read is blocked waiting for input
	// when context cancels we sent a fake command to trigger "COMMAND NOT SUPPORT"
	stop := make(chan struct{})
	defer close(stop) // Cannot defer close - will race with context!
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
				return
			default:
			}
			err := d.cancelRead()
			if err != nil {
				log.Printf("WARNING: cancelRead returned error in ctx cancelled: %v\n", err)
			}
			return
		}
	}()

	// get next token from input
	d.reader.Scan()
	data, err := d.reader.Bytes(), d.reader.Err()
	if err != nil {
		d.err = fmt.Errorf("error reading from device: %v", err)
		// on io errors we have to recreate the scanner to be able to keep reading
		// we pass in the data returned on error to the new instance to
		// ensure we dont skip any data in the stream
		d.newScanner(data)
		return false
	}

	// if context is cancelled AT_UNBLOCK has been sent and needs to be cleared from the data stream
	if ctx.Err() != nil {
		err = d.clearReader(data)
		if err != nil {
			d.err = err
			return false
		}
		d.err = ctx.Err()
		return false
	}

	d.outputCount++

	// reset cmd and output count if the last sent command is old / already completed
	// this must be unsolicited output
	if !d.cmdActive {
		d.cmd = nil
		d.outputCount = 0
	}

	hooks := d.profile.Hooks()
	if hooks != nil {
		data = hooks.Output(data, d.cmd, d.outputCount)
	}
	d.current = data

	state, err := d.inferState(d.current, d.cmd, d.outputCount)
	if err != nil {
		d.err = fmt.Errorf("unable to classify device output: %v", err)
		return false
	}
	d.err = nil
	switch state {
	case ATError:
		d.cmdActive = false
	case ATReady:
		d.cmdActive = false
	}
	return true
}

func (d *ScanWriter) cancelRead() error {
	s := d.State()
	if s == ATPrompt {
		err := d.Write([]byte{0x1b}) // send escape (cancels prompt)
		if err != nil {
			return err
		}
	}
	// send a fake AT command to trigger some output ("COMMAND NOT SUPPORT")
	return d.Write(append([]byte("AT_UNBLOCK"), d.eolSeq...))
}

func (d *ScanWriter) inferState(data []byte, cmd []byte, outputCount int) (State, error) {
	return d.profile.InferState([]byte(string(data)), []byte(string(cmd)), outputCount)
}

func (d *ScanWriter) clearReader(readData []byte) error {
	gotAtUnblock := false
	count := 1
	for {

		state, err := d.inferState(readData, d.cmd, count)
		if err != nil {
			return fmt.Errorf("error classifying device output: %v", err)
		}
		switch {
		// FIXME handle echo off
		// With ATE0 we get an empty line on AT_UNBLOCK, Ignoring that for now!
		case state == ATEcho && bytes.Equal(d.cmd, append([]byte("AT_UNBLOCK"), d.eolSeq...)) && !gotAtUnblock:
			gotAtUnblock = true
		// "COMMAND NOT SUPPORT" is expected after AT_UNBLOCK was sent (and echoed).
		case gotAtUnblock && bytes.Equal(readData, []byte("COMMAND NOT SUPPORT\r\n")):
			return nil
		// if we get multiple lines of output that we can't classify something is horribly wrong.
		default:
			log.Printf("DEBUG: got extra data lines: %v\n", string(readData))
		}

		d.reader.Scan()
		readData, err = d.reader.Bytes(), d.reader.Err()
		if err != nil {
			return fmt.Errorf("error reading after context cancel: %v", readData)
		}
		count++
	}
	return nil
}

// Clear will reset state of <d> and send a command want wait for matching expected output to ensure input and output buffers are in lock step.Clear
// All data currently in the buffers will be ignored
// TODO add debug flag to make this print the lines that are ignored.
func (d ScanWriter) Clear() error {
	// make <d> go into ready state by any means necessary.
	// should clear / flush input /output buffers to ensure we have clean buffers

	ctx, cancelFunc := context.WithCancel(context.Background())
	go func() {
		select {
		case <-time.After(time.Second * 3):
			// timed out, send AT_UNBLOCK!
			err := d.cancelRead()
			if err != nil {
				log.Printf("error sending AT_UNBLOCK: %v\n", err)
			}
		case <-ctx.Done():
			// clearRead finished.
			break
		}
	}()
	err := d.clearReader(nil)
	cancelFunc() // cancel the AT_UNBLOCK sender

	// Maybe try sending AT and getting expected response?
	// If we time out, maybe we're at a prompt?
	// Then try sending escape?

	if err != nil {
		return fmt.Errorf("error clearing reader: %v", err)
	}

	d.current = nil
	d.outputCount = 0
	d.cmd = nil
	d.cmdActive = false
	d.err = nil

	return nil
}
