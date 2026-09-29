package imip

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
)

func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

const event = `BEGIN:VCALENDAR
PRODID:-//Example/ExampleCalendarClient//EN
METHOD:REQUEST
VERSION:2.0
BEGIN:VEVENT
ORGANIZER:mailto:foo1@example.com
ATTENDEE;ROLE=CHAIR;PARTSTAT=ACCEPTED:mailto:foo1@example.com
ATTENDEE;RSVP=YES;CUTYPE=INDIVIDUAL:mailto:foo2@example.com
DTSTAMP:19970611T190000Z
DTSTART:19970701T170000Z
DTEND:19970701T173000Z
SUMMARY:Phone Conference
UID:calsvr.example.com-8739701987387771
SEQUENCE:0
STATUS:CONFIRMED
END:VEVENT
END:VCALENDAR
`

// RFC 6047 4.1, 4.2 and 4.3: a lone part, one beside its plain text, one beside an attachment.
var examples = map[string]string{
	"single": `From: foo1@example.com
To: foo2@example.com
Subject: Phone Conference
Mime-Version: 1.0
Content-Type: text/calendar; method=REQUEST; charset=US-ASCII
Content-Transfer-Encoding: 7bit

` + event,
	"alternative": `From: foo1@example.com
To: foo2@example.com
Subject: Phone Conference
Mime-Version: 1.0
Content-Type: multipart/alternative; boundary="01BD3665.3AF0D360"

--01BD3665.3AF0D360
Content-Type: text/plain; charset=us-ascii
Content-Transfer-Encoding: 7bit

This is an alternative representation of a "text/calendar"
MIME object.
--01BD3665.3AF0D360
Content-Type: text/calendar; method=REQUEST; charset=US-ASCII
Content-Transfer-Encoding: 7bit

` + event + `--01BD3665.3AF0D360--
`,
	"related": `From: foo1@example.com
To: foo2@example.com
Subject: Phone Conference
Mime-Version: 1.0
Content-Type: multipart/related; boundary="boundary-example-1"

--boundary-example-1
Content-Type: text/calendar; method=REQUEST; charset=US-ASCII
Content-Transfer-Encoding: 7bit
Content-Disposition: attachment; filename="event.ics"

` + event + `--boundary-example-1
Content-Type: application/msword; name="FieldReport.doc"
Content-Transfer-Encoding: base64
Content-Disposition: inline; filename="FieldReport.doc"
Content-ID: <123456789@example.com>

0M8R4KGxGuE=
--boundary-example-1--
`,
}

func read(t *testing.T, raw string) ([]*ical.Calendar, error) {
	t.Helper()
	e, err := message.Read(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return Read(e)
}

func uid(t *testing.T, cal *ical.Calendar) string {
	t.Helper()
	uid, err := cal.Events()[0].Props.Text(ical.PropUID)
	if err != nil {
		t.Fatal(err)
	}
	return uid
}

func TestReadFindsTheCalendarOfRFCExamples(t *testing.T) {
	for name, raw := range examples {
		t.Run(name, func(t *testing.T) {
			cals, err := read(t, crlf(raw))
			if err != nil {
				t.Fatal(err)
			}
			if len(cals) != 1 || uid(t, cals[0]) != "calsvr.example.com-8739701987387771" {
				t.Fatalf("got %d calendars, want the example's one", len(cals))
			}
		})
	}
}

func TestReadFollowsTheMethodParameter(t *testing.T) {
	single := crlf(examples["single"])
	t.Run("absent", func(t *testing.T) {
		cals, err := read(t, strings.Replace(single, " method=REQUEST;", "", 1))
		if err != nil || cals != nil {
			t.Fatalf("got %v, %v; a text/calendar without method is no iMIP part", cals, err)
		}
	})
	t.Run("contradicting", func(t *testing.T) {
		if _, err := read(t, strings.Replace(single, "method=REQUEST", "method=CANCEL", 1)); err == nil {
			t.Fatal("a method parameter other than METHOD was accepted")
		}
	})
}

func TestWriteRoundTrips(t *testing.T) {
	cals, err := read(t, crlf(examples["single"]))
	if err != nil {
		t.Fatal(err)
	}
	cal := cals[0]
	// Non-ASCII needs the charset and a transfer encoding (RFC 6047 2.4, 2.5).
	cal.Events()[0].Props.SetText(ical.PropSummary, "R\u00e9union t\u00e9l\u00e9phonique")

	for _, tc := range []struct{ text, top string }{
		{"", "text/calendar"},
		{"Phone conference, 1 July 17:00 UTC", "multipart/alternative"},
	} {
		t.Run(tc.top, func(t *testing.T) {
			var h mail.Header
			h.SetSubject("Phone Conference")
			var buf bytes.Buffer
			if err := Write(&buf, h, cal, tc.text); err != nil {
				t.Fatal(err)
			}
			e, err := message.Read(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if top, _, _ := e.Header.ContentType(); top != tc.top {
				t.Fatalf("message is %s, want %s", top, tc.top)
			}
			if !strings.Contains(buf.String(), "Content-Type: text/calendar; charset=utf-8; method=REQUEST") ||
				!strings.Contains(buf.String(), "Content-Transfer-Encoding: quoted-printable") {
				t.Fatalf("calendar part header missing:\n%s", buf.String())
			}
			if tc.text != "" && !strings.Contains(buf.String(), "Content-Type: text/plain") {
				t.Fatalf("no plain alternative:\n%s", buf.String())
			}
			got, err := Read(e)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("read %d calendars back, want 1", len(got))
			}
			if summary, _ := got[0].Events()[0].Props.Text(ical.PropSummary); summary != "R\u00e9union t\u00e9l\u00e9phonique" {
				t.Fatalf("summary came back as %q", summary)
			}
		})
	}
}

func TestWriteRefusesACalendarWithoutMethod(t *testing.T) {
	cals, err := read(t, crlf(examples["single"]))
	if err != nil {
		t.Fatal(err)
	}
	delete(cals[0].Props, ical.PropMethod)
	if err := Write(&bytes.Buffer{}, mail.Header{}, cals[0], ""); err == nil {
		t.Fatal("a calendar without METHOD was written as iMIP")
	}
}
