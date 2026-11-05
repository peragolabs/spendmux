package proxy

import "bufio"

// sseWriterLocal is the fiber Locals key a handler sets to change the wire
// format of a streamed chat reply (Anthropic events, Responses events).
const sseWriterLocal = "sse_writer"

// streamRecorderLocal holds the *streamRecorder of a stream being cached.
const streamRecorderLocal = "stream_recorder"

// sseWriter receives the OpenAI chat-chunk SSE lines the stream writers
// produce ("data: {...}", "data: [DONE]", "") and writes the client's format.
type sseWriter interface {
	Line(w *bufio.Writer, line string)
}
