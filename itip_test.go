package itip

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

// RFC 5546 4.2.1, DTEND's stray digit removed.
const groupRequest = `BEGIN:VCALENDAR
PRODID:-//Example/ExampleCalendarClient//EN
METHOD:REQUEST
VERSION:2.0
BEGIN:VEVENT
ORGANIZER:mailto:a@example.com
ATTENDEE;ROLE=CHAIR;PARTSTAT=ACCEPTED;CN=A:mailto:a@example.com
ATTENDEE;RSVP=TRUE;CUTYPE=INDIVIDUAL;CN=B:mailto:b@example.com
ATTENDEE;RSVP=TRUE;CUTYPE=INDIVIDUAL;CN=C:mailto:c@example.com
ATTENDEE;RSVP=TRUE;CUTYPE=INDIVIDUAL;CN=Hal:mailto:d@example.com
ATTENDEE;RSVP=FALSE;CUTYPE=ROOM:conf_big@example.com
ATTENDEE;ROLE=NON-PARTICIPANT;RSVP=FALSE:mailto:e@example.com
DTSTAMP:19970611T190000Z
DTSTART:19970701T200000Z
DTEND:19970701T210000Z
SUMMARY:Conference
UID:calsrv.example.com-873970198738777@example.com
SEQUENCE:0
STATUS:CONFIRMED
END:VEVENT
END:VCALENDAR
`

// RFC 5546 4.2.2.
const groupReply = `BEGIN:VCALENDAR
PRODID:-//Example/ExampleCalendarClient//EN
METHOD:REPLY
VERSION:2.0
BEGIN:VEVENT
ATTENDEE;PARTSTAT=ACCEPTED:mailto:b@example.com
ORGANIZER:mailto:a@example.com
UID:calsrv.example.com-873970198738777@example.com
SEQUENCE:0
REQUEST-STATUS:2.0;Success
DTSTAMP:19970612T190000Z
END:VEVENT
END:VCALENDAR
`

// RFC 5546 4.4.1.
const zonedRequest = `BEGIN:VCALENDAR
PRODID:-//Example/ExampleCalendarClient//EN
METHOD:REQUEST
VERSION:2.0
BEGIN:VTIMEZONE
TZID:America-SanJose
TZURL:http://example.com/tz/America-SanJose
BEGIN:STANDARD
DTSTART:19671029T020000
RRULE:FREQ=YEARLY;BYDAY=-1SU;BYMONTH=10
TZOFFSETFROM:-0700
TZOFFSETTO:-0800
TZNAME:PST
END:STANDARD
BEGIN:DAYLIGHT
DTSTART:19870405T020000
RRULE:FREQ=YEARLY;BYDAY=1SU;BYMONTH=4
TZOFFSETFROM:-0800
TZOFFSETTO:-0700
TZNAME:PDT
END:DAYLIGHT
END:VTIMEZONE
BEGIN:VEVENT
ORGANIZER:mailto:a@example.com
ATTENDEE;ROLE=CHAIR;PARTSTAT=ACCEPTED;CUTYPE=INDIVIDUAL:a@example.com
ATTENDEE;RSVP=TRUE;CUTYPE=INDIVIDUAL:b@example.fr
ATTENDEE;RSVP=TRUE;CUTYPE=INDIVIDUAL:c@example.jp
DTSTAMP:19970613T190030Z
DTSTART;TZID=America-SanJose:19970701T140000
DTEND;TZID=America-SanJose:19970701T150000
RRULE:FREQ=WEEKLY;COUNT=20;WKST=SU;BYDAY=TU
RDATE;TZID=America-SanJose:19970910T140000
EXDATE;TZID=America-SanJose:19970909T140000
EXDATE;TZID=America-SanJose:19971028T140000
SUMMARY:Weekly Phone Conference
UID:calsrv.example.com-873970198738777@example.com
SEQUENCE:0
STATUS:CONFIRMED
END:VEVENT
END:VCALENDAR
`

// RFC 5546 4.4.2, the original request.
const monthlyRequest = `BEGIN:VCALENDAR
METHOD:REQUEST
PRODID:-//Example/ExampleCalendarClient//EN
VERSION:2.0
BEGIN:VEVENT
UID:guid-1@example.com
SEQUENCE:0
RRULE:FREQ=MONTHLY;BYMONTHDAY=1;UNTIL=19980901T210000Z
ORGANIZER:mailto:a@example.com
ATTENDEE;ROLE=CHAIR;PARTSTAT=ACCEPTED:mailto:a@example.com
ATTENDEE:mailto:b@example.com
ATTENDEE:mailto:c@example.com
ATTENDEE:mailto:d@example.com
DESCRIPTION:IETF-C&S Conference Call
CLASS:PUBLIC
SUMMARY:IETF Calendaring Working Group Meeting
DTSTART:19970601T210000Z
DTEND:19970601T220000Z
LOCATION:Conference Call
DTSTAMP:19970526T083000Z
STATUS:CONFIRMED
END:VEVENT
END:VCALENDAR
`

// RFC 5546 4.4.2, the request moving one instance.
const movedInstance = `BEGIN:VCALENDAR
METHOD:REQUEST
PRODID:-//Example/ExampleCalendarClient//EN
VERSION:2.0
BEGIN:VEVENT
UID:guid-1@example.com
RECURRENCE-ID:19970701T210000Z
SEQUENCE:1
ORGANIZER:mailto:a@example.com
ATTENDEE;ROLE=CHAIR;PARTSTAT=ACCEPTED:mailto:a@example.com
ATTENDEE:mailto:b@example.com
ATTENDEE:mailto:c@example.com
ATTENDEE:mailto:d@example.com
DESCRIPTION:IETF-C&S Conference Call
CLASS:PUBLIC
SUMMARY:IETF Calendaring Working Group Meeting
DTSTART:19970703T210000Z
DTEND:19970703T220000Z
LOCATION:Conference Call
DTSTAMP:19970626T093000Z
STATUS:CONFIRMED
END:VEVENT
END:VCALENDAR
`

// RFC 5546 4.4.3.
const cancelInstance = `BEGIN:VCALENDAR
METHOD:CANCEL
PRODID:-//Example/ExampleCalendarClient//EN
VERSION:2.0
BEGIN:VEVENT
UID:guid-1@example.com
ORGANIZER:mailto:a@example.com
ATTENDEE;ROLE=CHAIR;PARTSTAT=ACCEPTED:mailto:a@example.com
ATTENDEE:mailto:b@example.com
ATTENDEE:mailto:c@example.com
ATTENDEE:mailto:d@example.com
RECURRENCE-ID:19970801T210000Z
SEQUENCE:2
STATUS:CANCELLED
DTSTAMP:19970721T093000Z
END:VEVENT
END:VCALENDAR
`

// RFC 5546 4.4.4.
const cancelSeries = `BEGIN:VCALENDAR
METHOD:CANCEL
PRODID:-//Example/ExampleCalendarClient//EN
VERSION:2.0
BEGIN:VEVENT
UID:guid-1@example.com
ORGANIZER:mailto:a@example.com
ATTENDEE;ROLE=CHAIR;PARTSTAT=ACCEPTED:mailto:a@example.com
ATTENDEE:mailto:b@example.com
ATTENDEE:mailto:c@example.com
ATTENDEE:mailto:d@example.com
DTSTAMP:19970721T103000Z
STATUS:CANCELLED
SEQUENCE:3
END:VEVENT
END:VCALENDAR
`

func parse(t *testing.T, text string) *ical.Calendar {
	t.Helper()
	cal, err := Parse([]byte(strings.ReplaceAll(text, "\n", "\r\n")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cal
}

func value(comp *ical.Component, name string) string {
	if prop := comp.Props.Get(name); prop != nil {
		return prop.Value
	}
	return ""
}

func events(cal *ical.Calendar) []*ical.Component {
	var out []*ical.Component
	for _, child := range cal.Children {
		if child.Name == ical.CompEvent {
			out = append(out, child)
		}
	}
	return out
}

func zones(cal *ical.Calendar) []string {
	var out []string
	for _, child := range cal.Children {
		if child.Name == ical.CompTimezone {
			out = append(out, value(child, ical.PropTimezoneID))
		}
	}
	return out
}

var now = time.Date(1997, 7, 25, 12, 0, 0, 0, time.UTC)

func TestReviseRaisesSequenceOnSignificantChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ical.Component)
		want   int
	}{
		{"nothing", func(*ical.Component) {}, 3},
		{"summary", func(c *ical.Component) { c.Props.SetText(ical.PropSummary, "Other") }, 3},
		{"location", func(c *ical.Component) { c.Props.SetText(ical.PropLocation, "Elsewhere") }, 3},
		{"dtstart", func(c *ical.Component) { c.Props.Get(ical.PropDateTimeStart).Value = "19970701T190000Z" }, 4},
		{"dtend", func(c *ical.Component) { c.Props.Get(ical.PropDateTimeEnd).Value = "19970701T230000Z" }, 4},
		{"rrule", func(c *ical.Component) { c.Props.Get(ical.PropRecurrenceRule).Value = "FREQ=WEEKLY" }, 4},
		{"exdate", func(c *ical.Component) {
			c.Props.Add(&ical.Prop{Name: ical.PropExceptionDates, Params: ical.Params{}, Value: "19970801T210000Z"})
		}, 4},
		{"status", func(c *ical.Component) { c.Props.SetText(ical.PropStatus, "TENTATIVE") }, 4},
		{"an attendee dropped", func(c *ical.Component) { c.Props[ical.PropAttendee] = c.Props[ical.PropAttendee][:2] }, 4},
		{"an attendee added", func(c *ical.Component) { c.Props.Add(mailto(ical.PropAttendee, "f@example.com")) }, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := Series(parse(t, monthlyRequest))
			setSequence(before, 3)
			after := cloneComponent(before)
			tc.change(after)
			Revise(before, after)
			if got := Sequence(after); got != tc.want {
				t.Errorf("SEQUENCE %d, want %d", got, tc.want)
			}
		})
	}
	first := ical.NewComponent(ical.CompEvent)
	Revise(nil, first)
	if value(first, ical.PropSequence) != "0" {
		t.Errorf("a first request carries SEQUENCE %q, want 0", value(first, ical.PropSequence))
	}
}

func TestReplyCarriesTheRequestsRevisionAndInstance(t *testing.T) {
	reply, err := Reply(parse(t, movedInstance), "c@example.com", "ACCEPTED", now)
	if err != nil {
		t.Fatal(err)
	}
	got := events(reply)
	if len(got) != 1 {
		t.Fatalf("the reply holds %d events, want 1", len(got))
	}
	if Method(reply) != MethodReply {
		t.Errorf("METHOD %q", Method(reply))
	}
	for name, want := range map[string]string{
		ical.PropSequence: "1", ical.PropRecurrenceID: "19970701T210000Z",
		ical.PropOrganizer: "mailto:a@example.com", ical.PropAttendee: "mailto:c@example.com",
		ical.PropUID: "guid-1@example.com", ical.PropDateTimeStamp: "19970725T120000Z",
	} {
		if v := value(got[0], name); v != want {
			t.Errorf("%s %q, want %q", name, v, want)
		}
	}
	if len(got[0].Props[ical.PropAttendee]) != 1 || got[0].Props.Get(ical.PropAttendee).Params.Get("PARTSTAT") != "ACCEPTED" {
		t.Errorf("the reply's attendees are %v, want c alone, accepted", got[0].Props[ical.PropAttendee])
	}
}

func TestInvitedMatchesAnyOfTheAddresses(t *testing.T) {
	cal := parse(t, groupRequest)
	if got := Invited(cal, []string{"me@example.org", "D@Example.COM"}); got != "d@example.com" {
		t.Errorf("Invited %q, want d@example.com", got)
	}
	if got := Invited(cal, []string{"me@example.org"}); got != "" {
		t.Errorf("Invited %q for an address not on the list", got)
	}
}

func TestCancelWithdrawsTheWholeEvent(t *testing.T) {
	cancel := Cancel(parse(t, groupRequest), nil, now)
	got := events(cancel)
	if Method(cancel) != MethodCancel || len(got) != 1 {
		t.Fatalf("METHOD %q with %d events", Method(cancel), len(got))
	}
	if value(got[0], ical.PropOrganizer) != "mailto:a@example.com" || value(got[0], ical.PropStatus) != "CANCELLED" || value(got[0], ical.PropSequence) != "1" {
		t.Errorf("ORGANIZER %q STATUS %q SEQUENCE %q, want a, CANCELLED, 1",
			value(got[0], ical.PropOrganizer), value(got[0], ical.PropStatus), value(got[0], ical.PropSequence))
	}
	if want := []string{"a@example.com", "b@example.com", "c@example.com", "d@example.com", "conf_big@example.com", "e@example.com"}; !slices.Equal(Attendees(got[0]), want) {
		t.Errorf("attendees %v, want %v", Attendees(got[0]), want)
	}
}

// RFC 5546 4.2.10.
func TestCancelUninvitesTheNamedAttendees(t *testing.T) {
	got := events(Cancel(parse(t, groupRequest), []string{"b@example.com"}, now))[0]
	if !slices.Equal(Attendees(got), []string{"b@example.com"}) {
		t.Errorf("attendees %v, want b alone", Attendees(got))
	}
	if value(got, ical.PropStatus) != "" {
		t.Errorf("STATUS %q; uninviting leaves the meeting on", value(got, ical.PropStatus))
	}
	if value(got, ical.PropSequence) != "1" || value(got, ical.PropOrganizer) != "mailto:a@example.com" {
		t.Errorf("SEQUENCE %q ORGANIZER %q", value(got, ical.PropSequence), value(got, ical.PropOrganizer))
	}
}

func TestMessagesCarryTheirTimezones(t *testing.T) {
	cal := parse(t, zonedRequest)
	if got := zones(Request(cal)); !slices.Equal(got, []string{"America-SanJose"}) {
		t.Errorf("the REQUEST carries zones %v, want the organizer's America-SanJose", got)
	}
	if got := zones(Cancel(cal, nil, now)); !slices.Equal(got, []string{"America-SanJose"}) {
		t.Errorf("the CANCEL carries zones %v", got)
	}
}

func TestRequestIncludesOverrides(t *testing.T) {
	stored, err := Apply(parse(t, monthlyRequest), parse(t, movedInstance))
	if err != nil {
		t.Fatal(err)
	}
	stored.Children[0].Children = append(stored.Children[0].Children, ical.NewComponent(ical.CompAlarm))
	got := events(Request(stored))
	if len(got) != 2 || instanceKey(got[1]) != "19970701T210000Z" {
		t.Fatalf("the REQUEST holds %d events, want the series and its override", len(got))
	}
	if len(got[0].Children) != 0 {
		t.Errorf("the REQUEST carries the organizer's alarms")
	}
}

func TestACancelledInstanceLeavesTheSeries(t *testing.T) {
	stored, err := Apply(parse(t, monthlyRequest), parse(t, movedInstance))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Apply(stored, parse(t, cancelInstance))
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || Series(out) == nil {
		t.Fatalf("cancelling one instance removed the series")
	}
	if got := propValues(Series(out), ical.PropExceptionDates); !slices.Equal(got, []string{"19970801T210000Z"}) {
		t.Errorf("EXDATE %v, want the cancelled 19970801T210000Z", got)
	}
	if len(events(out)) != 2 {
		t.Errorf("the other instance's override went too: %d events left", len(events(out)))
	}

	gone, err := Apply(out, parse(t, cancelSeries))
	if err != nil || gone != nil {
		t.Errorf("cancelling the series left %v, %v", gone, err)
	}
	moved, err := Apply(out, parse(t, strings.Replace(cancelInstance, "RECURRENCE-ID:19970801T210000Z", "RECURRENCE-ID:19970701T210000Z", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(events(moved)) != 1 {
		t.Errorf("cancelling the moved instance left %d events, want the series alone", len(events(moved)))
	}
}

func TestACancelIsCheckedAgainstTheStoredEvent(t *testing.T) {
	stored := parse(t, monthlyRequest)
	setSequence(Series(stored), 3)
	if _, err := Apply(stored, parse(t, strings.Replace(cancelSeries, "ORGANIZER:mailto:a@example.com", "ORGANIZER:mailto:x@example.com", 1))); !errors.Is(err, ErrOrganizer) {
		t.Errorf("a CANCEL from another organizer: %v, want ErrOrganizer", err)
	}
	if _, err := Apply(stored, parse(t, strings.Replace(cancelSeries, "SEQUENCE:3", "SEQUENCE:2", 1))); !errors.Is(err, ErrOutdated) {
		t.Errorf("an older CANCEL: %v, want ErrOutdated", err)
	}
	if out, err := Apply(stored, parse(t, cancelSeries)); err != nil || out != nil {
		t.Errorf("a CANCEL at the stored SEQUENCE: %v, %v", out, err)
	}
}

func TestAReplyUpdatesTheAttendee(t *testing.T) {
	stored := parse(t, groupRequest)
	out, err := Apply(stored, parse(t, groupReply))
	if err != nil {
		t.Fatal(err)
	}
	b := Series(out).Props[ical.PropAttendee][1]
	if b.Params.Get("PARTSTAT") != "ACCEPTED" {
		t.Errorf("b's PARTSTAT %q, want ACCEPTED", b.Params.Get("PARTSTAT"))
	}
	if Series(stored).Props[ical.PropAttendee][1].Params.Get("PARTSTAT") != "" {
		t.Errorf("Apply changed the stored calendar it was given")
	}

	declined := strings.Replace(groupReply, "PARTSTAT=ACCEPTED", "PARTSTAT=DECLINED", 1)
	if _, err := Apply(out, parse(t, declined)); !errors.Is(err, ErrSuperseded) {
		t.Errorf("a reply as old as the applied one: %v, want ErrSuperseded", err)
	}
	later := strings.Replace(declined, "DTSTAMP:19970612T190000Z", "DTSTAMP:19970613T190000Z", 1)
	again, err := Apply(out, parse(t, later))
	if err != nil {
		t.Fatal(err)
	}
	if got := Series(again).Props[ical.PropAttendee][1].Params.Get("PARTSTAT"); got != "DECLINED" {
		t.Errorf("a later reply left PARTSTAT %q, want DECLINED", got)
	}
	for text, want := range map[string]error{
		strings.Replace(later, "SEQUENCE:0", "SEQUENCE:1", 1):                                         ErrOutdated,
		strings.Replace(later, "mailto:b@example.com", "mailto:z@example.com", 1):                     ErrUninvited,
		strings.Replace(later, "ORGANIZER:mailto:a@example.com", "ORGANIZER:mailto:x@example.com", 1): ErrOrganizer,
		strings.Replace(later, "SEQUENCE:0", "RECURRENCE-ID:19970801T210000Z\nSEQUENCE:0", 1):         ErrInstance,
	} {
		if _, err := Apply(out, parse(t, text)); !errors.Is(err, want) {
			t.Errorf("got %v, want %v", err, want)
		}
	}
}
