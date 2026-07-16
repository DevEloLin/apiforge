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

// Frames returns an iterator over the SSE frames in r. Iteration stops at EOF
// or the first read error (a proxy has nothing useful to do with a torn stream).
// The caller is responsible for closing r.
func Frames(r io.Reader) iter.Seq[Event] {
	return func(yield func(Event) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // allow large data lines
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
		for sc.Scan() {
			line := sc.Text()
			if line == "" { // blank line terminates a frame
				if !flush() {
					return
				}
				continue
			}
			if strings.HasPrefix(line, ":") { // comment / heartbeat
				continue
			}
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				ev.Event = value
			case "data":
				data = append(data, value)
			}
		}
		flush()
	}
}

// Lines returns an iterator over the raw newline-delimited lines of r (for
// NDJSON streams like Grok's, which are not SSE-framed). Stops at EOF/error.
func Lines(r io.Reader) iter.Seq[string] {
	return func(yield func(string) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			if !yield(sc.Text()) {
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
