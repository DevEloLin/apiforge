package codex

import (
	"encoding/base64"
	"io"
	"mime/multipart"
	"strconv"
	"strings"

	"apiforge/internal/types"
)

// mpWriter streams a multipart/form-data image-edit body to a pipe so a large
// upload never fully buffers in memory. contentType carries the boundary.
type mpWriter struct {
	pw          *io.PipeWriter
	req         types.ImageRequest
	contentType string
	boundary    string
}

func multipartWriter(pw *io.PipeWriter, req types.ImageRequest) *mpWriter {
	// Materialise a boundary up-front so we can set the header before writing.
	tmp := multipart.NewWriter(io.Discard)
	b := tmp.Boundary()
	return &mpWriter{
		pw:          pw,
		req:         req,
		boundary:    b,
		contentType: "multipart/form-data; boundary=" + b,
	}
}

func (m *mpWriter) write() {
	w := multipart.NewWriter(m.pw)
	_ = w.SetBoundary(m.boundary)

	writeField := func(k, v string) error {
		if v == "" {
			return nil
		}
		return w.WriteField(k, v)
	}
	writeFile := func(field string, img types.ImageInput) error {
		name := img.Filename
		if name == "" {
			name = field + ".png"
		}
		fw, err := w.CreateFormFile(field, name)
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, base64.NewDecoder(base64.StdEncoding, strings.NewReader(img.B64)))
		return err
	}

	var err error
	for _, fn := range []func() error{
		func() error { return writeField("model", m.req.Model) },
		func() error { return writeField("prompt", m.req.Prompt) },
		func() error { return writeField("size", m.req.Size) },
		func() error { return writeField("quality", m.req.Quality) },
		func() error {
			if m.req.N > 0 {
				return writeField("n", strconv.Itoa(m.req.N))
			}
			return nil
		},
	} {
		if err = fn(); err != nil {
			m.pw.CloseWithError(err)
			return
		}
	}
	for _, img := range m.req.Images {
		if err = writeFile("image[]", img); err != nil {
			m.pw.CloseWithError(err)
			return
		}
	}
	if m.req.Mask != nil {
		if err = writeFile("mask", *m.req.Mask); err != nil {
			m.pw.CloseWithError(err)
			return
		}
	}
	if err = w.Close(); err != nil {
		m.pw.CloseWithError(err)
		return
	}
	m.pw.Close()
}
