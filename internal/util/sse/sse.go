// Package sse reads and writes Server-Sent Events. The reader yields upstream
// frames for translation; the writer formats frames back to a client. Only the
// `event:` and `data:` fields are handled (all the LLM backends we speak use).
package sse

import (
	"bufio"
	"io"
	"iter"
	"strings"
)

// Event is one SSE frame. Data is the concatenation of its data: lines.
type Event struct {
	Event string
	Data  string
}

// Frames returns an iterator over the SSE frames in r. It uses a bufio.Reader
// (not Scanner) so there is NO line-length cap — a single huge data: line (e.g.
// an inline base64 image) is never silently truncated. Iteration stops at EOF.
// The caller is responsible for closing r.
func Frames(r io.Reader) iter.Seq[Event] {
	return func(yield func(Event) bool) {
		br := bufio.NewReader(r)
		var ev Event
		var data []string
		flush := func() bool {
			if len(data) == 0 && ev.Event == "" {
				return true
			}
			ev.Data = strings.Join(data, "\n")
			ok := yield(ev)
			ev = Event{}
			data = data[:0]
			return ok
		}
		for {
			raw, err := br.ReadString('\n')
			line := strings.TrimRight(raw, "\r\n")
			switch {
			case line == "" && raw != "": // blank line terminates a frame
				if !flush() {
					return
				}
			case line == "": // no data (EOF with empty tail)
			case strings.HasPrefix(line, ":"): // comment / heartbeat
			default:
				field, value, _ := strings.Cut(line, ":")
				value = strings.TrimPrefix(value, " ")
				switch field {
				case "event":
					ev.Event = value
				case "data":
					data = append(data, value)
				}
			}
			if err != nil {
				flush() // emit a trailing frame that had no closing blank line
				return
			}
		}
	}
}

// Lines returns an iterator over the raw newline-delimited lines of r (for
// NDJSON streams like Grok's, which are not SSE-framed). Uses bufio.Reader so
// there is no line-length cap. Stops at EOF.
func Lines(r io.Reader) iter.Seq[string] {
	return func(yield func(string) bool) {
		br := bufio.NewReader(r)
		for {
			raw, err := br.ReadString('\n')
			if line := strings.TrimRight(raw, "\r\n"); line != "" {
				if !yield(line) {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
}

// DataFrame formats a `data: <payload>` SSE frame (payload is written verbatim,
// so it must not contain a newline-delimited blank line).
func DataFrame(payload string) []byte { return []byte("data: " + payload + "\n\n") }

// Done is the terminal SSE frame OpenAI clients expect.
var Done = []byte("data: [DONE]\n\n")
