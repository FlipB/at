# at

Package for dealing with AT devices / data streams.

PR's welcome.

## Go generate dependencies

`stringer` from https://github.com/golang/tools/blob/master/cmd/stringer/stringer.go is used.

These dependencies are added to `tools.go` and included in `go.mod` - to install correct versions you can use `go mod vendor`, and then use `go run ./vendor/golang.org/x/tools/cmd/stringer`.

Invoking `go generate` requires `stringer` on the path, however.

# AT

`AT` is a struct for wrapping AT devices to provide a nice, simple API to send AT commands.

## Example

```go

	ctx := context.Background()
	var rawDevice io.ReadWriter = openATDevice()

	// create AT - note that specifying the DeviceProfile is optional
	atDevice := at.NewAT(rawDevice, at.WithProfile(at.DefaultProfile{}))

	// we want to set a timeout for the AT command we're about to send.
	commandCtx, cancelFn := context.WithTimeout(ctx, time.Second*10)
	defer cancelFn()

	// Send the command "AT". Implementation of Send adds EOL automatically.
	response, err := atDevice.Send(commandCtx, "AT")
	if err != nil {
		// there was an error sending the command.
		// Could be io error, context timeout, or some AT error
		switch err {
		case at.ErrUnexpectedPrompt:
			// handle
		case context.DeadlineExceeded:
			// handle
		default:
			panic(err)
		}
	}

	// Print the response
	println(response)
	// NOTE: because we sent the "AT" command, response here *should* be an empty string.
	// This is because command echoes, "OK" and error strings are stripped from the response.
	// Error strings are instead put in the err return value.
	// OK is inferred from the lack of errors.
	// And Echoes are removed so as to not get mixed up with "real" command outputs

	// so, again, response is empty for AT commands that don't output anything (other than potential command echo and OK)

```

# at.ScanWriter

`ScanWriter` wraps an AT device to infer the device state.
`ScanWriter` can tell you if the received "token" is an AT command echo, or and AT error, or an interactive prompt.

## Example

```go
	ctx := context.Background()
	var rawDevice io.ReadWriter = openATDevice()

	// create a scanwriter over an io.ReadWriter
	// after this point you should no longer use the rawDevice anywhere else
	scanwriter := at.NewScanWriter(rawDevice, at.DefaultProfile{})

	// initial state should be "ready"
	state := scanwriter.State()
	if state != at.ATReady {
		panic("unexpected state")
	}

	// write a command to the AT device
	err := scanwriter.Write([]byte("AT\r\n"))
	if err != nil {
		panic("error writing to scanwriter")
	}

	// scan in a loop
	for scanwriter.Scan(ctx) {
		state := scanwriter.State()
		switch state {
		// We expect the first output after sending out command to be an Echo
		case at.ATEcho:
			continue
		case at.ATData:
			// we got data - this is unexpected for the AT command
			// AT normally only returns an echo (ATEcho) and an OK (ATReady)

			// get the data
			data := scanwriter.Bytes()
			println(data)
			continue
		case at.ATError:
			// ATError means the command failed and returned an error

			// we can get the error
			err = scanwriter.Err()
			// as well as any data returned before the error occurred
			data := scanwriter.Bytes()

			fmt.Printf("error in AT command: %v\nData: %s\n", err, data)
			break
		case at.ATNotice:
			// notice is very unexpected here - normally only sent on the Notification port on my device.
			// An ATNotice is a notification pushed from the network, ie. ^BOOT, RING, etc.
			// ATNotice and ATData classification might get be mixed up.
			
			// we can inspect the data received
			data := scanwriter.Byte()
			println(data)
			continue
		case at.ATReady:
			// we got an "OK" - command finished successfully
			println("Command completed successfully!")
			break
		}
	}
	err = scanwriter.Err()
	if err != nil {
		panic("error scanning")
	}
```