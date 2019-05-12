package at

import (
	"github.com/flipb/at/multibuf"
)

type TwoBufferTokenizer func(firstBuf []byte, secondBuf []byte) (advance int, token []byte, matched bool)
type Tokenizer func(readData []byte) (advance int, token []byte, matched bool)

func PatternSplitTokenizer(pattern []byte) TwoBufferTokenizer {
	return func(buf []byte, inputData []byte) (advance int, token []byte, matched bool) {
		if i := multibuf.PatternIndex(pattern, buf, inputData); i >= 0 {
			advance = i + len(pattern)
			data := multibuf.Slice(0, advance, buf, inputData)

			return advance, data, true
		}
		return 0, inputData, false
	}

}

func tokenizerToTwoBuffers(t Tokenizer) TwoBufferTokenizer {
	return func(readData []byte, moreData []byte) (advance int, token []byte, matched bool) {
		buf := multibuf.Slice(0, -1, readData, moreData)
		return t(buf)
	}
}

func matchAdvanceBuffer(buf *[]byte, inputData []byte, tokenizer TwoBufferTokenizer) (advance int, token []byte, matched bool) {
	advance, token, matched = tokenizer(*buf, inputData)

	// at this stage we have to remove any bytes taken from "buf"
	// as well as decrease the advance count with the same number of bytes
	if advance > 0 && len(*buf) > 0 && len(token) > 0 {
		consumed := len(*buf)
		if len(*buf) > len(token) {
			consumed = len(token)
		}
		nBuf := *buf
		nBuf = nBuf[consumed:]
		*buf = nBuf

		advance -= (consumed)
		if advance < 0 {
			advance = 0
		}
	}

	return advance, token, matched
}
