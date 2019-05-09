package at

import (
	"bytes"
)

// DefaultProfile is implementation of AT device profile (at.DeviceProfile)
type DefaultProfile struct{}

var _ DeviceProfile = DefaultProfile{}

func (p DefaultProfile) EOLSequence() []byte    { return []byte("\r\n") }
func (p DefaultProfile) PromptSequence() []byte { return []byte(">") }
func (p DefaultProfile) Hooks() DeviceHooks     { return p }

func (p DefaultProfile) InferState(readData []byte, lastCmd []byte, outputCountCmd int) (State, error) {
	switch {
	case len(readData) == 0:
		return ATReady, nil
	case bytes.Equal(readData, append([]byte("OK"), p.EOLSequence()...)):
		return ATReady, nil
	case bytes.HasPrefix(readData, []byte("+CME ERROR:")), bytes.HasPrefix(readData, []byte("+CMS ERROR:")),
		bytes.HasPrefix(readData, []byte("ERROR")), bytes.HasPrefix(readData, []byte("COMMAND NOT SUPPORT")),
		bytes.HasPrefix(readData, append([]byte("TOO MANY PARAMETERS"), p.EOLSequence()...)), bytes.HasPrefix(readData, append([]byte("NO CARRIER"))):
		return ATError, nil
	case bytes.HasSuffix(readData, p.PromptSequence()):
		return ATPrompt, nil
	case len(lastCmd) != 0 &&
		bytes.Equal(readData, lastCmd) &&
		outputCountCmd == 1:

		return ATEcho, nil
	case len(lastCmd) == 0 && outputCountCmd == 0:
		// Output with no command sent / active? Must be notice from the network
		return ATNotice, nil
	default:
		return ATData, nil
	}
}

var _ DeviceHooks = DefaultProfile{}

func (p DefaultProfile) RawOutput(readData []byte) []byte {
	return readData
}
func (p DefaultProfile) Output(readData []byte, lastCmd []byte, outputCountCmd int) []byte {
	return readData
}
func (p DefaultProfile) Input(readData []byte) []byte {
	return readData
}
