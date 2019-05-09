package multibuf

func PatternIndex(pattern []byte, buf []byte, bufs ...[]byte) int {
	if len(pattern) == 0 {
		// does the empty pattern match everything or nothing?
		return -1
	}
	b := buf
	currentIndex := -1 // "global" index
	var pIndex int
	for buffersIndex := 0; buffersIndex <= len(bufs); buffersIndex++ {

		for bIndex := 0; bIndex < len(b); bIndex++ {
			currentIndex++
			if pattern[pIndex] != b[bIndex] {
				pIndex = 0
			} else {
				pIndex++
			}
			if pIndex == len(pattern) {
				// full match!
				return currentIndex - len(pattern) + 1
			}
		}

		if buffersIndex == len(bufs) {
			// we have gone through all the buffers
			break
		}
		b = bufs[buffersIndex]
	}
	return -1
}

// Index converts the supplied index and returns the index of the buffer and the index in that buffer
// it allows you to index multiple buffers as one contigious buffer
// The first returned int is the index of the buffer that the second returned int is an index of.
// The input "buf" is index 0. The first buffer in "bufs" is index 1.
// Doesnt panic on index out of range, returns -1
func Index(index int, buf []byte, bufs ...[]byte) (int, int) {
	if index < 0 {
		return -1, index
	}
	if index > (len(buf)-1) && len(bufs) == 0 {
		//panic("index out of range")
		return -1, -1
	}
	if index < len(buf) {
		return 0, index
	}
	// index is greater than the first buffer.
	index -= len(buf)
	for i := 0; i < len(bufs); i++ {
		if index >= len(bufs[i]) {
			// index greater than buffer
			index -= len(bufs[i])
			continue
		}
		return i + 1, index
	}

	//panic("index out of range")
	return -1, -1
}

func Len(buf []byte, bufs ...[]byte) int {
	n := 0
	for _, b := range bufs {
		n += len(b)
	}
	return n + len(buf)
}

// Slice acts like a slice operator (eg. slice[0:4]) but on multiple slices
// as if they were one contigious slice.
// upToNotInclIndex can be -1 for reading to the end of the buffers.
// NOTE that the slice returned might be a copy of the data in the "sliced" input buffers
func Slice(fromIndex, upToNotInclIndex int, buf []byte, bufs ...[]byte) []byte {
	bufsLen := Len(buf, bufs...)

	if fromIndex < 0 || bufsLen < upToNotInclIndex || bufsLen <= fromIndex {
		panic("index out of bounds")
	}
	if fromIndex >= 0 && !(upToNotInclIndex > fromIndex || upToNotInclIndex == -1) {
		return nil
	}

	var outBuffer []byte
	index := 0
	b := buf
	for buffersIndex := 0; buffersIndex <= len(bufs); buffersIndex++ {
		if index+len(b) <= fromIndex {
			// end of current buffer is before start as specified by fromIndex
			index += len(b)
			b = bufs[buffersIndex]
			continue
		}

		// Optimization: skip allocation if we only want a slice of a single buffer (the current 'b')
		if fromIndex >= index && upToNotInclIndex != -1 && (upToNotInclIndex-index-1) < len(b) {
			// first and only buffer we need to slice.
			return b[(fromIndex - index):(upToNotInclIndex - index)]
		}

		start := 0
		if fromIndex > index {
			// fromIndex is higher than index 0 of this buffer (start is in the middle of the slice)
			start = fromIndex - index
		}
		end := len(b)
		if end > (upToNotInclIndex-index) && upToNotInclIndex != -1 {
			// "upTo" is less than the end of the current buffer.
			end = upToNotInclIndex - index
		}

		if upToNotInclIndex > 0 && cap(outBuffer) == 0 {
			outBuffer = make([]byte, 0, upToNotInclIndex-fromIndex)
		}
		outBuffer = append(outBuffer, b[start:end]...)
		index += end

		if upToNotInclIndex == index {
			break
		}
		if buffersIndex == len(bufs) {
			// we have gone through all the buffers
			break
		}
		b = bufs[buffersIndex]
	}
	return outBuffer
}
