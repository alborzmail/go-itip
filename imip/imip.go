// Package imip carries iTIP scheduling messages in mail (RFC 6047).
package imip

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/alborzmail/go-itip"
	"github.com/emersion/go-ical"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
)

// Write writes a mail of header h carrying cal, after text as its human-readable alternative unless text is empty (RFC 6047 2.4).
func Write(w io.Writer, h mail.Header, cal *ical.Calendar, text string) error {
	method := itip.Method(cal)
	if method == "" {
		return errors.New("imip: calendar has no METHOD")
	}
	var body bytes.Buffer
	if err := ical.NewEncoder(&body).Encode(cal); err != nil {
		return err
	}
	if text == "" {
		h = h.Copy()
		setCalendar(&h.Header, method)
		cw, err := mail.CreateSingleInlineWriter(w, h)
		if err != nil {
			return err
		}
		return writeClose(cw, body.Bytes())
	}

	iw, err := mail.CreateInlineWriter(w, h)
	if err != nil {
		return err
	}
	var th mail.InlineHeader
	th.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	tw, err := iw.CreatePart(th)
	if err != nil {
		return err
	}
	if err := writeClose(tw, []byte(text)); err != nil {
		return err
	}
	var ch mail.InlineHeader
	setCalendar(&ch.Header, method)
	cw, err := iw.CreatePart(ch)
	if err != nil {
		return err
	}
	if err := writeClose(cw, body.Bytes()); err != nil {
		return err
	}
	return iw.Close()
}

func setCalendar(h *message.Header, method string) {
	h.SetContentType("text/calendar", map[string]string{"method": method, "charset": "utf-8"})
	// Quoted-printable keeps a UTF-8 object whole over a transport that is not 8-bit clean (RFC 6047 2.5).
	h.Set("Content-Transfer-Encoding", "quoted-printable")
}

func writeClose(wc io.WriteCloser, b []byte) error {
	if _, err := wc.Write(b); err != nil {
		wc.Close()
		return err
	}
	return wc.Close()
}

// Read returns the scheduling messages e carries: every one of a multipart/mixed, the first of a multipart/alternative (RFC 6047 2.4).
// A text/calendar part without a method parameter is not one (RFC 6047 2.4, note 2).
func Read(e *message.Entity) ([]*ical.Calendar, error) {
	t, params, _ := e.Header.ContentType()
	if mr := e.MultipartReader(); mr != nil {
		var cals []*ical.Calendar
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return cals, nil
			}
			if err != nil {
				return nil, err
			}
			if t == "multipart/alternative" && len(cals) > 0 {
				continue
			}
			found, err := Read(part)
			if err != nil {
				return nil, err
			}
			cals = append(cals, found...)
		}
	}
	if t != "text/calendar" || params["method"] == "" {
		return nil, nil
	}
	raw, err := io.ReadAll(e.Body)
	if err != nil {
		return nil, err
	}
	cal, err := itip.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(params["method"], itip.Method(cal)) {
		return nil, fmt.Errorf("imip: method parameter %q is not the calendar's METHOD %q", params["method"], itip.Method(cal))
	}
	return []*ical.Calendar{cal}, nil
}
