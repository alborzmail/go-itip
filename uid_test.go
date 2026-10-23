package itip

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func identityCalendar(uid, method string) *ical.Calendar {
	cal := message(method)
	event := ical.NewComponent(ical.CompEvent)
	event.Props.SetText(ical.PropUID, uid)
	event.Props.SetText(ical.PropOrganizer, "mailto:organizer@example.com")
	event.Props.SetDateTime(ical.PropDateTimeStamp, time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	attendee := mailto(ical.PropAttendee, "guest@example.com")
	attendee.Params.Set("PARTSTAT", "ACCEPTED")
	event.Props.Add(attendee)
	cal.Children = append(cal.Children, event)
	return cal
}

func TestApplyRejectsDifferentUIDs(t *testing.T) {
	for _, method := range []string{MethodCancel, MethodRequest, MethodPublish, MethodReply} {
		for _, uid := range []string{"different", "prefix-event", "EVENT", ""} {
			t.Run(method+"/"+uid, func(t *testing.T) {
				stored := identityCalendar("event", "")
				msg := identityCalendar(uid, method)
				beforeStored, beforeMsg := cloneCalendar(stored), cloneCalendar(msg)
				out, err := Apply(stored, msg)
				if err == nil || !strings.Contains(err.Error(), "UID") || out != nil {
					t.Fatalf("Apply = %v, %v; want UID error", out, err)
				}
				if !reflect.DeepEqual(stored, beforeStored) || !reflect.DeepEqual(msg, beforeMsg) {
					t.Fatal("inputs changed")
				}
			})
		}
	}
}

func TestApplyValidatesWholeSeriesUID(t *testing.T) {
	for _, method := range []string{MethodCancel, MethodRequest, MethodPublish, MethodReply} {
		for _, corrupt := range []string{"message override", "stored override", "missing message UID", "missing stored UID", "duplicate UID", "empty message", "empty stored"} {
			t.Run(method+"/"+corrupt, func(t *testing.T) {
				stored := identityCalendar("event", "")
				msg := identityCalendar("event", method)
				switch corrupt {
				case "message override", "stored override":
					target := msg
					if corrupt == "stored override" {
						target = stored
					}
					override := cloneComponent(target.Children[0])
					override.Props.SetText(ical.PropUID, "other")
					override.Props.SetDateTime(ical.PropRecurrenceID, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
					target.Children = append(target.Children, override)
				case "missing message UID":
					msg.Children[0].Props.Del(ical.PropUID)
				case "missing stored UID":
					stored.Children[0].Props.Del(ical.PropUID)
				case "duplicate UID":
					msg.Children[0].Props.Add(msg.Children[0].Props.Get(ical.PropUID))
				case "empty message":
					msg.Children = nil
				case "empty stored":
					stored.Children = nil
				}
				beforeStored, beforeMsg := cloneCalendar(stored), cloneCalendar(msg)
				out, err := Apply(stored, msg)
				if err == nil || !strings.Contains(err.Error(), "UID") || out != nil {
					t.Fatalf("Apply = %v, %v; want UID error", out, err)
				}
				if !reflect.DeepEqual(stored, beforeStored) || !reflect.DeepEqual(msg, beforeMsg) {
					t.Fatal("inputs changed")
				}
			})
		}
	}
}

func TestApplyUIDMatchingAndAbsentStored(t *testing.T) {
	for _, method := range []string{MethodCancel, MethodRequest, MethodPublish, MethodReply} {
		stored := identityCalendar("event", "")
		msg := identityCalendar("event", method)
		if _, err := Apply(stored, msg); err != nil {
			t.Fatalf("matching %s: %v", method, err)
		}
		if method != MethodReply {
			out, err := Apply(nil, msg)
			if err != nil || (out == nil) != (method == MethodCancel) {
				t.Fatalf("absent stored %s: %v, %v", method, out, err)
			}
		}
		if _, err := Apply(nil, identityCalendar("", method)); err == nil || !strings.Contains(err.Error(), "UID") {
			t.Errorf("absent stored %s accepted empty UID: %v", method, err)
		}
	}
}

func TestOccurrenceIdentityIncludesUID(t *testing.T) {
	a := identityCalendar("event", "").Children[0]
	b := identityCalendar("other", "").Children[0]
	if sameInstance(a)(b) {
		t.Fatal("different UIDs matched the same occurrence")
	}
}
